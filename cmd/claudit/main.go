// claudit — audit Claude Code session JSONL files for token & cost spend.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "claudit:", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			if _, err := fmt.Fprint(os.Stdout, topLevelUsage); err != nil {
				return err
			}
			return nil
		case "version", "--version":
			fmt.Println(versionString())
			return nil
		case "report":
			return runReport(args[1:])
		case "diff":
			return runDiff(args[1:])
		case "watch":
			return runWatch(args[1:])
		case "serve":
			return runServe(args[1:])
		}
	}
	return runReport(args)
}

const topLevelUsage = `claudit — audit Claude Code session JSONL files for token & cost spend.

Usage:
  claudit <command> [flags]
  claudit [flags]              (alias for "claudit report")

Commands:
  report   Generate a cost/usage report (HTML by default; --json or unset --html for markdown).
  diff     Compare two date ranges and report top movers (HTML by default; --json or unset --html for markdown).
  watch    Tail a live session and print running cost.
  serve    Run a local web daemon that serves a live-updating report (filters via URL query).
  version  Print the installed claudit version and exit (also: --version).

Run "claudit <command> --help" for command-specific flags.
`
