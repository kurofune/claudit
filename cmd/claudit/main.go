// claudit — audit Claude Code session JSONL files for token & cost spend.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "claudit:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd, rest, err := dispatch(args)
	if err != nil {
		return err
	}
	switch cmd {
	case "help":
		_, err := fmt.Fprint(os.Stdout, topLevelUsage)
		return err
	case "version":
		fmt.Println(versionString())
		return nil
	case "watch":
		return runWatch(rest)
	default:
		return runServe(rest)
	}
}

// dispatch maps argv (without the program name) to a command and the
// arguments that command receives. No arguments, or leading flags, run serve.
func dispatch(args []string) (cmd string, rest []string, err error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") && !isTopLevelFlag(args[0]) {
		return "serve", args, nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		return "help", args[1:], nil
	case "version", "--version":
		return "version", args[1:], nil
	case "watch", "serve":
		return args[0], args[1:], nil
	}
	return "", nil, fmt.Errorf("unknown command %q", args[0])
}

func isTopLevelFlag(arg string) bool {
	switch arg {
	case "-h", "--help", "--version":
		return true
	}
	return false
}

const topLevelUsage = `claudit — audit Claude Code session JSONL files for token & cost spend.

Usage:
  claudit <command> [flags]
  claudit [flags]              (alias for "claudit serve")

Commands:
  serve    Run a local web daemon that serves a live-updating dashboard (filters via URL query).
  watch    Tail a live session and print running cost.
  version  Print the installed claudit version and exit (also: --version).

Run "claudit <command> --help" for command-specific flags.
`
