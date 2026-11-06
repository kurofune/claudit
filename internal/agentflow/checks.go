package agentflow

import (
	"regexp"
	"strings"
)

// checkSeparators splits a shell command into the segments a check can
// start: one per &&, ||, ; or newline.
var checkSeparators = regexp.MustCompile(`&&|\|\||;|\n`)

// heredocStart finds a here-document's delimiter on a command line:
// <<WORD, <<-WORD, <<'WORD' or <<"WORD", never a <<< here-string.
var heredocStart = regexp.MustCompile(`(?:^|[^<])<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// envAssign is a leading NAME=value word of a shell segment.
var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// checkSubcommands are the checks a tool runs by subcommand: the first
// word, then the second word that makes it a check.
var checkSubcommands = map[string][]string{
	"go":    {"test", "build", "vet"},
	"cargo": {"test", "build", "check"},
	"git":   {"commit", "push"},
}

// checkKind is the check a shell command runs, labelled by its words
// ("go test", "npm run build", "make"), or "" when no segment of it starts
// one. A segment is a piece between &&, ||, ; and newlines; a leading "(",
// NAME=value assignments, `time` and `timeout N` are skipped, and a `cd`
// segment runs no check. The first segment that starts a check names it, so
// `ps | rg 'go test'` and `echo go test` are not checks. A here-document's
// body is data, not commands, so its lines start no segment.
func checkKind(command string) string {
	for _, seg := range checkSeparators.Split(dropHeredocs(command), -1) {
		if k := segmentCheck(seg); k != "" {
			return k
		}
	}
	return ""
}

func segmentCheck(seg string) string {
	words := strings.Fields(strings.TrimLeft(strings.TrimSpace(seg), "( \t"))
	for i := range words {
		words[i] = strings.TrimRight(words[i], ")")
	}
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

// dropHeredocs removes every here-document body from a shell command: the
// lines after one naming a delimiter, through the line that is only that
// delimiter (leading tabs allowed), or to the end when none closes it.
func dropHeredocs(command string) string {
	if !strings.Contains(command, "<<") {
		return command
	}
	var keep []string
	var pending []string
	for _, line := range strings.Split(command, "\n") {
		if len(pending) > 0 {
			if strings.TrimLeft(line, "\t") == pending[0] {
				pending = pending[1:]
			}
			continue
		}
		keep = append(keep, line)
		for _, m := range heredocStart.FindAllStringSubmatch(line, -1) {
			pending = append(pending, m[1])
		}
	}
	return strings.Join(keep, "\n")
}
