// Package aggregate rolls per-turn data up into the slices the renderer
// needs. The aggregator is single-threaded; concurrency happens at the
// file-walking layer in cmd/claudit. Add() is the only mutator.
package aggregate

import (
	"sort"
	"strings"
	"time"

	"github.com/nategross/claudit/internal/parse"
	"github.com/nategross/claudit/internal/pricing"
)

// Filter narrows which turns are counted.
type Filter struct {
	Since, Until     time.Time // zero means open-ended
	ProjectSubstring string    // case-insensitive substring of cwd
}

// Tokens is the canonical token-count tuple we render in every section.
type Tokens struct {
	InputTokens          int64
	OutputTokens         int64
	CacheCreate5mTokens  int64
	CacheCreate1hTokens  int64
	CacheReadTokens      int64
}

func (a *Tokens) addUsage(u parse.Usage) {
	a.InputTokens += int64(u.InputTokens)
	a.OutputTokens += int64(u.OutputTokens)
	a.CacheCreate5mTokens += int64(u.CacheCreate5mTokens)
	a.CacheCreate1hTokens += int64(u.CacheCreate1hTokens)
	a.CacheReadTokens += int64(u.CacheReadTokens)
}

// Totals is the top-line summary.
type Totals struct {
	Tokens
	CostUSD  float64
	Sessions int
	Turns    int
	First    time.Time
	Last     time.Time
}

// ModelBucket is one row in the by-model section.
type ModelBucket struct {
	Model string
	Tokens
	CostUSD float64
	Turns   int
}

// ProjectBucket is one row in the by-project section.
type ProjectBucket struct {
	Project       string // best-effort decoded path
	Tokens
	CostUSD       float64
	Sessions      int
	DominantModel string
	Turns         int
}

// ToolBucket: tool name + count + the output tokens of the surrounding turns.
type ToolBucket struct {
	Name         string
	Count        int     // total tool_use occurrences
	TurnCount    int     // number of distinct turns that called this tool
	OutputTokens int64   // sum of output_tokens of those turns
	CostUSD      float64 // proportional cost of those turns (best-effort)
}

// SkillBucket: same as ToolBucket but keyed on skill or slash command name.
type SkillBucket struct {
	Key          string // "skill:<name>" or "command:</cmd>"
	Count        int
	TurnCount    int
	OutputTokens int64
	CostUSD      float64
}

// DetailBucket is one row in the per-tool drill-down (e.g. for Bash:
// "git status" / 1242 / $89.40). Same accounting as ToolBucket.
type DetailBucket struct {
	Detail       string
	Count        int
	TurnCount    int
	OutputTokens int64
	CostUSD      float64
}

// SidechainSplit is just two Tokens-with-cost stacks.
type SidechainSplit struct {
	Main      bucketTotals
	Sidechain bucketTotals
}

type bucketTotals struct {
	Tokens
	CostUSD float64
	Turns   int
}

// SubagentBucket is a per-subagent-type row.
type SubagentBucket struct {
	Type string // "" or "unknown" gets folded to "sidechain — unknown subagent"
	Tokens
	CostUSD float64
	Turns   int
}

// AgentInvocation is one specific subagent run — the unit corresponds to
// one `<encoded-cwd>/<sessionId>/subagents/agent-<id>.jsonl` file. Useful
// for "show me the 20 most expensive general-purpose runs and what they
// were doing."
type AgentInvocation struct {
	SourceFile   string    // the agent-<id>.jsonl path
	SubagentType string    // from sibling .meta.json (or "" if missing)
	Description  string    // from sibling .meta.json (or "" if missing)
	Project      string    // cwd of the launching session
	First, Last  time.Time // turn timestamp range
	Tokens
	CostUSD float64
	Turns   int
}

// Aggregator accumulates per-turn data.
type Aggregator struct {
	prices *pricing.Table
	filter Filter

	totals    Totals
	byModel   map[string]*ModelBucket
	byProject map[string]*ProjectBucket
	byTool       map[string]*ToolBucket
	byToolDetail map[string]map[string]*DetailBucket // tool -> detail -> bucket
	bySkill      map[string]*SkillBucket
	side          SidechainSplit
	bySub         map[string]*SubagentBucket
	byInvocation  map[string]*AgentInvocation // keyed by SourceFile

	// Used to compute "dominant model" per project.
	projectModelTurns map[string]map[string]int

	sessions       map[string]struct{}
	projectSession map[string]map[string]struct{}
	bySession      map[string]*SessionBucket

	unknownModels map[string]struct{}

	// Trend mode. Zero-valued (PeriodNone) means trend tracking is off
	// and the per-bucket maps stay nil. WithPeriod flips it on.
	period         Period
	trendTotals    map[time.Time]*TrendPoint
	trendByModel   map[string]map[time.Time]*TrendPoint
	trendByProject map[string]map[time.Time]*TrendPoint
	trendByTool    map[string]map[time.Time]*TrendPoint
	trendBySession map[string]map[time.Time]*TrendPoint
	trendBySub     map[string]map[time.Time]*TrendPoint
	// Per-bucket session-id sets, used to backfill TrendPoint.Sessions
	// in TrendTotals(). Counted at gap-fill time rather than in addTrend
	// because addTrend doesn't have the session ID in scope.
	bucketSessions map[time.Time]map[string]struct{}

	// Per-prompt attribution. WithPromptIndex sets promptIndex; without
	// it, byPrompt stays empty and ByPrompt() returns nil.
	promptIndex *PromptIndex
	byPrompt    map[string]*promptBucketInternal
}

// New returns an empty aggregator using the given pricing table.
func New(p *pricing.Table) *Aggregator {
	return &Aggregator{
		prices:            p,
		byModel:           map[string]*ModelBucket{},
		byProject:         map[string]*ProjectBucket{},
		byTool:            map[string]*ToolBucket{},
		byToolDetail:      map[string]map[string]*DetailBucket{},
		bySkill:           map[string]*SkillBucket{},
		bySub:             map[string]*SubagentBucket{},
		byInvocation:      map[string]*AgentInvocation{},
		projectModelTurns: map[string]map[string]int{},
		sessions:          map[string]struct{}{},
		projectSession:    map[string]map[string]struct{}{},
		bySession:         map[string]*SessionBucket{},
		unknownModels:     map[string]struct{}{},
		byPrompt:          map[string]*promptBucketInternal{},
	}
}

// WithFilter sets a filter and returns the aggregator (chainable).
func (a *Aggregator) WithFilter(f Filter) *Aggregator {
	a.filter = f
	return a
}

// WithPromptIndex enables per-prompt attribution. Without one,
// ByPrompt() returns nil and per-prompt buckets aren't populated.
// Must be set before Add() runs.
func (a *Aggregator) WithPromptIndex(p *PromptIndex) *Aggregator {
	a.promptIndex = p
	return a
}

// WithPeriod enables trend tracking at the given bucket size. Pass
// PeriodNone to disable. Must be called before Add().
func (a *Aggregator) WithPeriod(p Period) *Aggregator {
	a.period = p
	if p.Valid() {
		a.trendTotals = map[time.Time]*TrendPoint{}
		a.trendByModel = map[string]map[time.Time]*TrendPoint{}
		a.trendByProject = map[string]map[time.Time]*TrendPoint{}
		a.trendByTool = map[string]map[time.Time]*TrendPoint{}
		a.trendBySession = map[string]map[time.Time]*TrendPoint{}
		a.trendBySub = map[string]map[time.Time]*TrendPoint{}
		a.bucketSessions = map[time.Time]map[string]struct{}{}
	}
	return a
}

func (a *Aggregator) match(t parse.Turn) bool {
	if !a.filter.Since.IsZero() && t.Timestamp.Before(a.filter.Since) {
		return false
	}
	if !a.filter.Until.IsZero() && !t.Timestamp.Before(a.filter.Until) {
		return false
	}
	if a.filter.ProjectSubstring != "" {
		if !strings.Contains(strings.ToLower(t.CWD), strings.ToLower(a.filter.ProjectSubstring)) {
			return false
		}
	}
	return true
}

// SubagentLookup is called per sidechain turn to resolve subagent
// metadata (typically by reading the sibling .meta.json). Returning
// empty strings is fine — the aggregator falls back to "unknown" buckets.
type SubagentLookup func(turn parse.Turn) (subagentType, description string)

// Add accumulates one turn. Returns whether the turn was counted (i.e. passed filters).
func (a *Aggregator) Add(t parse.Turn) bool {
	return a.AddWithSubagent(t, nil)
}

// AddWithSubagent is Add plus a hook for resolving subagent type from turn metadata.
func (a *Aggregator) AddWithSubagent(t parse.Turn, lookup SubagentLookup) bool {
	if !a.match(t) {
		return false
	}
	cost, known := a.prices.Cost(t.Model,
		t.Usage.InputTokens, t.Usage.OutputTokens,
		t.Usage.CacheCreate5mTokens, t.Usage.CacheCreate1hTokens,
		t.Usage.CacheReadTokens)
	// Skip pricing warnings for synthetic / zero-token markers — Claude Code
	// emits "<synthetic>" turns for internal events (compaction, prompt
	// suggestions) that legitimately cost nothing.
	if !known && t.Model != "" && t.Model != "<synthetic>" {
		nonZero := t.Usage.InputTokens != 0 || t.Usage.OutputTokens != 0 ||
			t.Usage.CacheCreate5mTokens != 0 || t.Usage.CacheCreate1hTokens != 0 ||
			t.Usage.CacheReadTokens != 0
		if nonZero {
			a.unknownModels[t.Model] = struct{}{}
		}
	}

	a.totals.Tokens.addUsage(t.Usage)
	a.totals.CostUSD += cost
	a.totals.Turns++
	if a.totals.First.IsZero() || t.Timestamp.Before(a.totals.First) {
		a.totals.First = t.Timestamp
	}
	if t.Timestamp.After(a.totals.Last) {
		a.totals.Last = t.Timestamp
	}

	// Trend bucketing — only when WithPeriod was called and the turn has
	// a real timestamp. Per-tool trend is filled inside the tool loop
	// below to dedupe multi-tool-call turns.
	var bucket time.Time
	trendOn := a.period.Valid() && !t.Timestamp.IsZero()
	if trendOn {
		bucket = a.period.Truncate(t.Timestamp)
		addTrend(a.trendTotals, bucket, cost, t.Usage)
	}

	// By model.
	mb := a.byModel[t.Model]
	if mb == nil {
		mb = &ModelBucket{Model: t.Model}
		a.byModel[t.Model] = mb
	}
	mb.Tokens.addUsage(t.Usage)
	mb.CostUSD += cost
	mb.Turns++

	if trendOn {
		m := a.trendByModel[t.Model]
		if m == nil {
			m = map[time.Time]*TrendPoint{}
			a.trendByModel[t.Model] = m
		}
		addTrend(m, bucket, cost, t.Usage)
	}

	// By project. Use cwd; fall back to "(unknown)".
	proj := t.CWD
	if proj == "" {
		proj = "(unknown)"
	}
	pb := a.byProject[proj]
	if pb == nil {
		pb = &ProjectBucket{Project: proj}
		a.byProject[proj] = pb
		a.projectModelTurns[proj] = map[string]int{}
		a.projectSession[proj] = map[string]struct{}{}
	}
	pb.Tokens.addUsage(t.Usage)
	pb.CostUSD += cost
	pb.Turns++
	a.projectModelTurns[proj][t.Model]++

	if trendOn {
		m := a.trendByProject[proj]
		if m == nil {
			m = map[time.Time]*TrendPoint{}
			a.trendByProject[proj] = m
		}
		addTrend(m, bucket, cost, t.Usage)
	}
	if t.SessionID != "" {
		pb.Sessions = 0 // recomputed below
		a.projectSession[proj][t.SessionID] = struct{}{}
	}

	// Sessions overall + per-session bucket. The per-session bucket is
	// what powers the cache-efficiency by-session view; we never sort by
	// it elsewhere, so we only roll up Tokens / cost / turns / project.
	if t.SessionID != "" {
		a.sessions[t.SessionID] = struct{}{}
		sb := a.bySession[t.SessionID]
		if sb == nil {
			sb = &SessionBucket{SessionID: t.SessionID, Project: proj}
			a.bySession[t.SessionID] = sb
		}
		sb.Tokens.addUsage(t.Usage)
		sb.CostUSD += cost
		sb.Turns++

		if trendOn {
			m := a.trendBySession[t.SessionID]
			if m == nil {
				m = map[time.Time]*TrendPoint{}
				a.trendBySession[t.SessionID] = m
			}
			addTrend(m, bucket, cost, t.Usage)
			// Record this bucket → session ID so TrendTotals() can
			// backfill TrendPoint.Sessions for the headline delta.
			s := a.bucketSessions[bucket]
			if s == nil {
				s = map[string]struct{}{}
				a.bucketSessions[bucket] = s
			}
			s[t.SessionID] = struct{}{}
		}
	}

	// Per-prompt attribution. Walks back to the originating user
	// message via the prompt index built before Add() ran. Orphans
	// bucket under noPromptKey so the renderer can show "(no prompt)"
	// rather than dropping spend on the floor.
	if a.promptIndex != nil {
		entry := a.promptIndex.Lookup(t.UUID)
		pb := a.byPrompt[entry.Key]
		if pb == nil {
			pb = &promptBucketInternal{
				Key:      entry.Key,
				Sample:   entry.Sample,
				InvocSet: map[string]struct{}{},
				SessSet:  map[string]struct{}{},
			}
			a.byPrompt[entry.Key] = pb
		}
		// First non-empty sample wins; later turns can attach without
		// overwriting a real prompt with an orphan placeholder.
		if pb.Sample == "" && entry.Sample != "" {
			pb.Sample = entry.Sample
		}
		pb.Tokens.addUsage(t.Usage)
		pb.CostUSD += cost
		pb.TurnCount++
		if entry.UserUUID != "" {
			pb.InvocSet[entry.UserUUID] = struct{}{}
		}
		if t.SessionID != "" {
			pb.SessSet[t.SessionID] = struct{}{}
		}
	}

	// Sidechain split.
	tgt := &a.side.Main
	if t.Sidechain {
		tgt = &a.side.Sidechain
	}
	tgt.Tokens.addUsage(t.Usage)
	tgt.CostUSD += cost
	tgt.Turns++

	// Subagent type + per-invocation tracking (only meaningful for sidechain).
	if t.Sidechain {
		var subType, desc string
		if lookup != nil {
			subType, desc = lookup(t)
		}
		bucketKey := subType
		if bucketKey == "" {
			bucketKey = "sidechain — unknown subagent"
		}
		sb := a.bySub[bucketKey]
		if sb == nil {
			sb = &SubagentBucket{Type: bucketKey}
			a.bySub[bucketKey] = sb
		}
		sb.Tokens.addUsage(t.Usage)
		sb.CostUSD += cost
		sb.Turns++

		if trendOn {
			m := a.trendBySub[bucketKey]
			if m == nil {
				m = map[time.Time]*TrendPoint{}
				a.trendBySub[bucketKey] = m
			}
			addTrend(m, bucket, cost, t.Usage)
		}

		// Per-invocation row, one per source file.
		if t.SourceFile != "" {
			inv := a.byInvocation[t.SourceFile]
			if inv == nil {
				inv = &AgentInvocation{
					SourceFile:   t.SourceFile,
					SubagentType: subType,
					Description:  desc,
					Project:      t.CWD,
					First:        t.Timestamp,
					Last:         t.Timestamp,
				}
				a.byInvocation[t.SourceFile] = inv
			}
			inv.Tokens.addUsage(t.Usage)
			inv.CostUSD += cost
			inv.Turns++
			if !t.Timestamp.IsZero() && (inv.First.IsZero() || t.Timestamp.Before(inv.First)) {
				inv.First = t.Timestamp
			}
			if t.Timestamp.After(inv.Last) {
				inv.Last = t.Timestamp
			}
		}
	}

	// By tool: each turn contributes its output tokens once per distinct
	// tool name (so a turn that calls Bash twice still adds output_tokens
	// to Bash only once). Count tracks total tool_use occurrences.
	seen := map[string]bool{}
	for _, tu := range t.ToolUses {
		tb := a.byTool[tu.Name]
		if tb == nil {
			tb = &ToolBucket{Name: tu.Name}
			a.byTool[tu.Name] = tb
		}
		tb.Count++
		if !seen[tu.Name] {
			tb.OutputTokens += int64(t.Usage.OutputTokens)
			tb.CostUSD += cost
			tb.TurnCount++
			seen[tu.Name] = true
			if trendOn {
				m := a.trendByTool[tu.Name]
				if m == nil {
					m = map[time.Time]*TrendPoint{}
					a.trendByTool[tu.Name] = m
				}
				addTrend(m, bucket, cost, t.Usage)
			}
		}

		// Detail rollup. Same TurnCount-style accounting: each turn
		// contributes output/cost to each (tool, detail) pair only once.
		if tu.Detail != "" {
			details := a.byToolDetail[tu.Name]
			if details == nil {
				details = map[string]*DetailBucket{}
				a.byToolDetail[tu.Name] = details
			}
			db := details[tu.Detail]
			if db == nil {
				db = &DetailBucket{Detail: tu.Detail}
				details[tu.Detail] = db
			}
			db.Count++
			detailKey := "__detail__" + tu.Name + "\x00" + tu.Detail
			if !seen[detailKey] {
				db.OutputTokens += int64(t.Usage.OutputTokens)
				db.CostUSD += cost
				db.TurnCount++
				seen[detailKey] = true
			}
		}

		// Skill / SlashCommand.
		var skillKey string
		switch tu.Name {
		case "Skill":
			if tu.SkillName != "" {
				skillKey = "skill:" + tu.SkillName
			}
		case "SlashCommand":
			if tu.SlashCommand != "" {
				skillKey = "command:" + tu.SlashCommand
			}
		}
		if skillKey != "" {
			sb := a.bySkill[skillKey]
			if sb == nil {
				sb = &SkillBucket{Key: skillKey}
				a.bySkill[skillKey] = sb
			}
			sb.Count++
			if !seen["__skill__"+skillKey] {
				sb.OutputTokens += int64(t.Usage.OutputTokens)
				sb.CostUSD += cost
				sb.TurnCount++
				seen["__skill__"+skillKey] = true
			}
		}
	}

	return true
}

// Totals returns top-line totals.
func (a *Aggregator) Totals() Totals {
	out := a.totals
	out.Sessions = len(a.sessions)
	return out
}

// ByModel returns model rows sorted by cost descending.
func (a *Aggregator) ByModel() []ModelBucket {
	out := make([]ModelBucket, 0, len(a.byModel))
	for _, v := range a.byModel {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CostUSD > out[j].CostUSD })
	return out
}

// ByProject returns project rows with dominant-model populated, sorted by cost desc.
func (a *Aggregator) ByProject() []ProjectBucket {
	out := make([]ProjectBucket, 0, len(a.byProject))
	for k, v := range a.byProject {
		row := *v
		row.Sessions = len(a.projectSession[k])
		// Dominant = highest turn count.
		var bestModel string
		bestCount := -1
		for m, c := range a.projectModelTurns[k] {
			if c > bestCount {
				bestCount = c
				bestModel = m
			}
		}
		row.DominantModel = bestModel
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CostUSD > out[j].CostUSD })
	return out
}

// ByTool returns tool rows sorted by cost desc.
func (a *Aggregator) ByTool() []ToolBucket {
	out := make([]ToolBucket, 0, len(a.byTool))
	for _, v := range a.byTool {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CostUSD > out[j].CostUSD })
	return out
}

// ByToolDetail returns the per-tool drill-down: a map from tool name to
// detail rows sorted by cost desc. Tools with no detail data are absent.
func (a *Aggregator) ByToolDetail() map[string][]DetailBucket {
	out := make(map[string][]DetailBucket, len(a.byToolDetail))
	for tool, details := range a.byToolDetail {
		rows := make([]DetailBucket, 0, len(details))
		for _, v := range details {
			rows = append(rows, *v)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].CostUSD > rows[j].CostUSD })
		out[tool] = rows
	}
	return out
}

// BySkill returns skill rows sorted by cost desc.
func (a *Aggregator) BySkill() []SkillBucket {
	out := make([]SkillBucket, 0, len(a.bySkill))
	for _, v := range a.bySkill {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CostUSD > out[j].CostUSD })
	return out
}

// SidechainSplit returns the main vs sidechain split.
func (a *Aggregator) SidechainSplit() SidechainSplit {
	return a.side
}

// MainTotals exposes the main-turns totals (renderer convenience).
func (a *Aggregator) MainTotals() (Tokens, float64, int) {
	return a.side.Main.Tokens, a.side.Main.CostUSD, a.side.Main.Turns
}

// SidechainTotals exposes the sidechain totals.
func (a *Aggregator) SidechainTotals() (Tokens, float64, int) {
	return a.side.Sidechain.Tokens, a.side.Sidechain.CostUSD, a.side.Sidechain.Turns
}

// BySubagent returns subagent rows sorted by cost desc.
func (a *Aggregator) BySubagent() []SubagentBucket {
	out := make([]SubagentBucket, 0, len(a.bySub))
	for _, v := range a.bySub {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CostUSD > out[j].CostUSD })
	return out
}

// AgentInvocations returns one row per detected subagent run, sorted by
// cost desc. If filterType is non-empty, only invocations matching that
// subagent type are returned.
func (a *Aggregator) AgentInvocations(filterType string) []AgentInvocation {
	out := make([]AgentInvocation, 0, len(a.byInvocation))
	for _, v := range a.byInvocation {
		if filterType != "" && v.SubagentType != filterType {
			continue
		}
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CostUSD > out[j].CostUSD })
	return out
}

// UnknownModels returns the sorted list of model names we couldn't price.
func (a *Aggregator) UnknownModels() []string {
	out := make([]string, 0, len(a.unknownModels))
	for k := range a.unknownModels {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
