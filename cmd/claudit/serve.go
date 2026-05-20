package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/serve"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("claudit serve", flag.ExitOnError)
	defaultRoot := defaultProjectsRoot()
	root := fs.String("root", defaultRoot, "root directory to watch (defaults to $CLAUDE_CONFIG_DIR/projects or ~/.claude/projects)")
	bind := fs.String("bind", "127.0.0.1", "host to bind on (loopback only by default; pass 0.0.0.0 to expose to your LAN at your own risk)")
	port := fs.Int("port", 8787, "TCP port to listen on")
	pricesPath := fs.String("prices", "", "override pricing YAML path (default: ~/.config/claudit/prices.yaml)")
	pollMS := fs.Int("poll-ms", 2000, "how often to re-scan the projects root for changed JSONLs")
	last := fs.String("last", "7d", "default time window when the URL doesn't specify one — Nd or Nw (use empty string to disable; ?scope=all also lifts it per-request)")
	hotspots := fs.Int("hotspots", 10, "default hotspots count when ?hotspots is not in the URL (0 disables)")
	sessionsTop := fs.Int("sessions", 10, "default top-N sessions in the drill-down view when ?sessions is not in the URL (0 disables; ?scope=all lifts it per-request)")
	by := fs.String("by", "day", "default trend bucket when ?by is not in the URL (day|week|month|off)")
	redact := fs.Bool("redact", false, "default redaction state when ?redact is not in the URL")
	reloadSec := fs.Int("reload-sec", 30, "in-page silent-reload cadence; deferred while you're reading (any details open or recent activity)")
	cacheSize := fs.Int("cache", 16, "max rendered HTML responses to cache, keyed on (filter, generation); 0 disables")
	openBrowser := fs.Bool("open", true, "open the report in your default browser on startup (skipped on headless hosts)")
	fs.Usage = func() {
		ew := &errWriter{w: fs.Output()}
		ew.Println("claudit serve — run a local web daemon that serves a live-updating report.")
		ew.Println()
		ew.Println("Usage:")
		ew.Println("  claudit serve [flags]")
		ew.Println()
		ew.Println("The server binds to 127.0.0.1 by default. The report can contain prompt")
		ew.Println("text and CWD paths, has no authentication, and is meant for single-user")
		ew.Println("localhost use only. Exposing it to other hosts is at your own risk.")
		ew.Println()
		ew.Println("Filter via URL query params (same names as `claudit report` flags):")
		ew.Println("  /?project=myrepo&last=7d")
		ew.Println("  /?since=2026-05-01&until=2026-05-15&by=week")
		ew.Println()
		ew.Println("Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *port < 1 || *port > 65535 {
		return fmt.Errorf("--port: must be 1..65535, got %d", *port)
	}
	period := aggregate.Period(*by)
	switch *by {
	case "off":
		period = aggregate.Period("")
	case "day", "week", "month":
		// already set
	default:
		return fmt.Errorf("--by: must be one of day|week|month|off, got %q", *by)
	}

	var defaultLast time.Duration
	if *last != "" {
		d, err := serveParseLastDuration(*last)
		if err != nil {
			return fmt.Errorf("--last: %w", err)
		}
		defaultLast = d
	}

	prices, err := loadPrices(*pricesPath)
	if err != nil {
		return err
	}

	cache := serve.NewCache(*root)
	srv := serve.NewServer(cache, serve.Options{
		Bind:               fmt.Sprintf("%s:%d", *bind, *port),
		Prices:             prices,
		PollInterval:       time.Duration(*pollMS) * time.Millisecond,
		DefaultLast:        defaultLast,
		DefaultHotspots:    *hotspots,
		DefaultSessionsTop: *sessionsTop,
		DefaultPeriod:      period,
		DefaultRedact:      *redact,
		ReloadIntervalSec:  *reloadSec,
		MaxCachedRenders:   *cacheSize,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv.Start(ctx)

	// Banner. Print before we block on Serve so users see the URL
	// even if the listener fails — surface address conflicts loud.
	addr := srv.Addr()
	fmt.Fprintf(os.Stderr, "claudit serve: watching %s\n", *root)
	fmt.Fprintf(os.Stderr, "claudit serve: listening on %s (Ctrl-C to stop)\n", addr)
	if strings.HasPrefix(*bind, "0.") || *bind == "" {
		fmt.Fprintln(os.Stderr, "claudit serve: WARNING — bound to a non-loopback address; this exposes prompt text on your network.")
	}

	if *openBrowser {
		if serve.LooksHeadless() {
			fmt.Fprintln(os.Stderr, "claudit serve: headless host detected; skipping --open")
		} else {
			// Fire-and-forget. Errors here aren't fatal — the URL is
			// printed above and the user can click it directly.
			if err := serve.OpenBrowser(addr); err != nil {
				fmt.Fprintf(os.Stderr, "claudit serve: --open failed: %v\n", err)
			}
		}
	}

	return srv.ListenAndServe(ctx)
}

// serveParseLastDuration parses "Nd" or "Nw" — same shape as the
// report command's --last and the URL ?last= parser. Local copy to
// keep cmd/claudit's dep surface on internal/serve narrow.
func serveParseLastDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("expected Nd or Nw, got %q", s)
	}
	unit := s[len(s)-1]
	var mult time.Duration
	switch unit {
	case 'd':
		mult = 24 * time.Hour
	case 'w':
		mult = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("unit must be 'd' or 'w', got %q", string(unit))
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, fmt.Errorf("expected positive integer prefix, got %q", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be positive, got %d", n)
	}
	return time.Duration(n) * mult, nil
}
