package main

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestDispatch(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantCmd  string
		wantRest []string
	}{
		{"bare runs serve", nil, "serve", nil},
		{"empty runs serve", []string{}, "serve", nil},
		{"serve with flags", []string{"serve", "--port=1"}, "serve", []string{"--port=1"}},
		{"bare flags go to serve", []string{"--port=1"}, "serve", []string{"--port=1"}},
		{"watch", []string{"watch", "-x"}, "watch", []string{"-x"}},
		{"prices", []string{"prices", "--json"}, "prices", []string{"--json"}},
		{"version", []string{"version"}, "version", []string{}},
		{"--version", []string{"--version"}, "version", []string{}},
		{"help", []string{"help"}, "help", []string{}},
		{"-h", []string{"-h"}, "help", []string{}},
		{"--help", []string{"--help"}, "help", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, rest, err := dispatch(tc.args)
			if err != nil {
				t.Fatalf("dispatch(%q) error: %v", tc.args, err)
			}
			if cmd != tc.wantCmd {
				t.Errorf("dispatch(%q) cmd = %q, want %q", tc.args, cmd, tc.wantCmd)
			}
			if len(rest) != 0 || len(tc.wantRest) != 0 {
				if !reflect.DeepEqual(rest, tc.wantRest) {
					t.Errorf("dispatch(%q) rest = %q, want %q", tc.args, rest, tc.wantRest)
				}
			}
		})
	}
}

func TestDispatchRemovedCommands(t *testing.T) {
	for _, name := range []string{"report", "diff", "bogus"} {
		_, _, err := dispatch([]string{name, "--json"})
		if err == nil {
			t.Fatalf("dispatch(%q) returned no error", name)
		}
		want := `unknown command "` + name + `"`
		if err.Error() != want {
			t.Errorf("dispatch(%q) error = %q, want %q", name, err.Error(), want)
		}
	}
}

func TestRunRemovedCommandsFail(t *testing.T) {
	for _, name := range []string{"report", "diff"} {
		if err := run([]string{name}); err == nil || !strings.Contains(err.Error(), `unknown command "`+name+`"`) {
			t.Errorf("run(%q) error = %v, want unknown command", name, err)
		}
	}
}

func TestTopLevelUsage(t *testing.T) {
	for _, banned := range []string{"report", "diff"} {
		if strings.Contains(topLevelUsage, banned) {
			t.Errorf("topLevelUsage mentions %q:\n%s", banned, topLevelUsage)
		}
	}
	_, commands, ok := strings.Cut(topLevelUsage, "Commands:\n")
	if !ok {
		t.Fatalf("topLevelUsage has no Commands section:\n%s", topLevelUsage)
	}
	commands, _, _ = strings.Cut(commands, "\n\n")
	var got []string
	for _, line := range strings.Split(commands, "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			got = append(got, f[0])
		}
	}
	if want := []string{"serve", "watch", "prices", "version"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Commands = %q, want %q", got, want)
	}
	if !strings.Contains(topLevelUsage, `alias for "claudit serve"`) {
		t.Errorf("topLevelUsage does not say bare claudit runs serve:\n%s", topLevelUsage)
	}
	first, _, _ := strings.Cut(topLevelUsage, "\n")
	for _, provider := range []string{"Claude Code", "Codex"} {
		if !strings.Contains(first, provider) {
			t.Errorf("topLevelUsage first line %q does not name %s", first, provider)
		}
	}
	if strings.Contains(topLevelUsage, "Claude Code session JSONL files") {
		t.Errorf("topLevelUsage still describes claudit as Claude Code only:\n%s", topLevelUsage)
	}
}

func TestMainExitsOneOnRemovedCommands(t *testing.T) {
	if name := os.Getenv("CLAUDIT_TEST_MAIN_ARG"); name != "" {
		os.Args = []string{"claudit", name}
		main()
		return
	}
	for _, name := range []string{"report", "diff"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMainExitsOneOnRemovedCommands$")
		cmd.Env = append(os.Environ(), "CLAUDIT_TEST_MAIN_ARG="+name)
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("claudit %s: err = %v, want exit status 1; output:\n%s", name, err, out)
		}
		if want := `claudit: unknown command "` + name + `"`; !strings.Contains(string(out), want) {
			t.Errorf("claudit %s output = %q, want it to contain %q", name, out, want)
		}
	}
}
