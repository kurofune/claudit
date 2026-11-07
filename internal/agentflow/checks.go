package agentflow

import (
	"regexp"
	"strings"
)

// heredocDelim reads a here-document's delimiter after its << operator:
// WORD, -WORD, 'WORD' or "WORD".
var heredocDelim = regexp.MustCompile(`^-?[ \t]*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// cannotFail are the commands whose exit is zero unless a redirection
// fails them (`echo ok >&-`), so one run unredirected after && leaves the
// call's exit to the command before it.
var cannotFail = map[string]bool{"echo": true, "printf": true, "true": true, ":": true}

// envAssign is a leading NAME=value word of a shell segment.
var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// checkSubcommands are the checks a tool runs by subcommand: the first
// word, then the second word that makes it a check.
var checkSubcommands = map[string][]string{
	"go":    {"test", "build", "vet"},
	"cargo": {"test", "build", "check"},
	"git":   {"commit", "push"},
}

// checkRun is the check a shell command runs and whether the call's exit
// status may belong to an earlier command that kept the check from running.
// afterAnd: an earlier command that can fail gates the check with &&, so an
// error may be that command's and the check never ran. afterOr: an earlier
// command gates it with ||, so a success may be that command's.
type checkRun struct {
	kind              string
	afterAnd, afterOr bool
}

// owns reports whether a call that ran c and errored (isErr) or succeeded
// carries the check's own exit status.
func (c checkRun) owns(isErr bool) bool {
	if c.kind == "" {
		return false
	}
	if isErr {
		return !c.afterAnd
	}
	return !c.afterOr
}

// checkKind is the check a shell command runs, labelled by its words
// ("go test", "npm run build", "make"), with kind "" when the call's exit
// status is not that check's. A call exits with its last command's status,
// so only the final segment can name the check (shellSegments splits at the
// &&, ||, ; and newlines the shell reads as separators): trailing segments
// that cannot fail (cannotFail) are skipped when && joins them, since they
// run only after the check passed; blank and comment-only segments are
// skipped. A piped final segment exits with its last stage, so it is no
// check, even when its first stage cannot fail. The commands joined to the
// check by && and || before it set afterAnd and afterOr. In a segment, a
// leading "(", NAME=value assignments, `time` and `timeout N` are skipped,
// and a `cd` runs no check, so `ps | rg 'go test'` and `echo go test` are
// not checks. A here-document's body is data, not commands, so its lines
// start no segment. A redirection other than fd duplication (`> out.log`,
// `2>/dev/null`, `>&-`) can fail the command it is on, and a background job
// (`go test &`) returns before its exit is known, so a call with either on
// the check, on a trailing segment skipped after it, or on a command that
// gates it runs no check.
func checkKind(command string) checkRun {
	segs := shellSegments(command)
	for i := len(segs) - 1; i >= 0; i-- {
		s := segs[i]
		words := segmentWords(s.text)
		if len(words) == 0 {
			continue
		}
		if s.piped || s.redirected || s.background {
			return checkRun{}
		}
		if s.sep == "&&" && cannotFail[words[0]] {
			continue
		}
		kind := segmentCheck(words)
		if kind == "" {
			return checkRun{}
		}
		return gate(segs[:i+1], kind)
	}
	return checkRun{}
}

// gate reads the && and || chain that ends at the check, the last of segs,
// back to the ;, & or newline that starts it. A redirected command in the
// chain makes the call's exit no surer than its own, so the chain runs no
// check.
func gate(segs []segment, kind string) checkRun {
	c := checkRun{kind: kind}
	for i := len(segs) - 1; i > 0; i-- {
		if sep := segs[i].sep; (sep == "&&" || sep == "||") && segs[i-1].redirected {
			return checkRun{}
		}
		switch segs[i].sep {
		case "&&":
			if canFail(segs[i-1]) {
				c.afterAnd = true
			}
		case "||":
			c.afterOr = true
		default:
			return c
		}
	}
	return c
}

// canFail reports whether a segment may exit non-zero: any command but an
// unredirected cannotFail one, and any pipeline.
func canFail(s segment) bool {
	words := segmentWords(s.text)
	return len(words) > 0 && (s.piped || s.redirected || !cannotFail[words[0]])
}

func segmentWords(seg string) []string {
	words := strings.Fields(strings.TrimLeft(strings.TrimSpace(seg), "( \t"))
	for i := range words {
		words[i] = strings.TrimRight(words[i], ")")
	}
	return words
}

func segmentCheck(words []string) string {
	for len(words) > 0 {
		switch w := words[0]; {
		case envAssign.MatchString(w), w == "time":
			words = words[1:]
		case w == "timeout":
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
			if len(words) > 0 {
				words = words[1:]
			}
		default:
			return wordsCheck(words)
		}
	}
	return ""
}

func wordsCheck(words []string) string {
	second := func(set ...string) string {
		if len(words) < 2 {
			return ""
		}
		for _, s := range set {
			if words[1] == s {
				return words[0] + " " + s
			}
		}
		return ""
	}
	switch words[0] {
	case "make", "pytest", "tsc":
		return words[0]
	case "npm":
		if k := second("test"); k != "" {
			return k
		}
		if len(words) >= 3 && words[1] == "run" {
			switch words[2] {
			case "test", "build", "lint", "check":
				return "npm run " + words[2]
			}
		}
	case "node":
		return second("--test")
	default:
		if set, ok := checkSubcommands[words[0]]; ok {
			return second(set...)
		}
	}
	return ""
}

// segment is one command between shell separators: its text without any
// trailing comment, the separator before it ("" for the first, else "&&",
// "||", ";", "&" or "\n"), whether a | pipes it, whether it carries a
// redirection other than fd duplication, and whether a & after it runs it
// in the background.
type segment struct {
	text, sep                     string
	piped, redirected, background bool
}

// shellSegments splits a shell command at each &&, ||, ;, & and newline
// that the shell reads as a separator: one outside '...' and "..." and not
// escaped by a backslash, so `rg 'a; go test' .` is one segment. A # that
// begins a word starts a comment through the end of its line, so a quote,
// separator or << inside it is text: `# don't` opens no quote. A << outside
// quotes and comments (not a <<< here-string) names a here-document whose
// body, the lines after its line through the one that is only the
// delimiter (leading tabs allowed) or to the end, is dropped. A single &
// (not &&, |&, &> or part of a >& redirection) ends a background segment.
// A >, >>, >|, <, <>, &> or >& outside quotes and comments marks its segment
// redirected, except N>&M and <&M, which only duplicate a descriptor and
// cannot fail; <( and >( are process substitutions, not redirections.
func shellSegments(command string) []segment {
	var segs []segment
	start, cut := 0, -1
	sep := ""
	var piped, redirected bool
	var quote byte
	var pending []string
	emit := func(end int, next string, background bool) {
		if cut >= 0 {
			end = cut
		}
		segs = append(segs, segment{command[start:end], sep, piped, redirected, background})
		sep, piped, redirected, cut = next, false, false, -1
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case c == '\\':
			i++
		case quote == '"':
			if c == '"' {
				quote = 0
			}
		case c == '#' && (i == 0 || strings.IndexByte(" \t\n;&|()<>", command[i-1]) >= 0):
			cut = i
			for i+1 < len(command) && command[i+1] != '\n' {
				i++
			}
		case c == '\'' || c == '"':
			quote = c
		case strings.HasPrefix(command[i:], "<<<"):
			i += 2
		case strings.HasPrefix(command[i:], "<<"):
			i++
			if m := heredocDelim.FindStringSubmatch(command[i+1:]); m != nil {
				pending = append(pending, m[1])
				i += len(m[0])
			}
		case c == ';':
			emit(i, ";", false)
			start = i + 1
		case c == '\n':
			if !continued(command, start, i, cut, sep) {
				emit(i, "\n", false)
			}
			cut = -1
			start = skipHeredocBodies(command, i+1, pending)
			pending = nil
			i = start - 1
		case (c == '&' || c == '|') && i+1 < len(command) && command[i+1] == c:
			emit(i, command[i:i+2], false)
			i++
			start = i + 1
		case c == '|' && i+1 < len(command) && command[i+1] == '&':
			piped = true
			i++
		case c == '|':
			piped = true
		case c == '&' && i+1 < len(command) && command[i+1] == '>':
			redirected = true
			i++
		case c == '&':
			emit(i, "&", true)
			start = i + 1
		case c == '>' || c == '<':
			n, harmless := redirection(command[i:])
			if !harmless {
				redirected = true
			}
			i += n - 1
		}
	}
	emit(len(command), "", false)
	return segs
}

// redirection reads the > or < operator at the start of s (a << is read
// before this) and returns how many bytes it spans and whether it only
// duplicates a descriptor (>&2, <&0) or opens a process substitution, so it
// cannot fail the command. The operand after a plain >, >> or < is left
// to the scan.
func redirection(s string) (n int, harmless bool) {
	if len(s) < 2 {
		return 1, false
	}
	switch s[1] {
	case '(':
		return 1, true
	case '>', '|':
		return 2, false
	case '&':
		k := 2
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > 2 && (k == len(s) || strings.IndexByte(" \t\n;&|)", s[k]) >= 0) {
			return k, true
		}
		return 2, false
	}
	return 1, false
}

// continued reports whether the line ending at end only finishes an && or
// || before it (blank, or a comment): the shell reads the next line as the
// rest of that list, so the newline separates nothing.
func continued(command string, start, end, cut int, sep string) bool {
	if cut >= 0 {
		end = cut
	}
	return (sep == "&&" || sep == "||") && strings.TrimSpace(command[start:end]) == ""
}

// skipHeredocBodies returns the offset just past the here-document bodies
// that begin at from, one per delimiter in order, or len(command) when one
// never closes.
func skipHeredocBodies(command string, from int, delims []string) int {
	for _, d := range delims {
		for {
			if from >= len(command) {
				return len(command)
			}
			nl := strings.IndexByte(command[from:], '\n')
			line := command[from:]
			if nl >= 0 {
				line = command[from : from+nl]
			}
			if nl < 0 {
				from = len(command)
			} else {
				from += nl + 1
			}
			if strings.TrimLeft(line, "\t") == d {
				break
			}
		}
	}
	return from
}
