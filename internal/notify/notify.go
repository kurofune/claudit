// Package notify sends best-effort desktop notifications for budget
// crosses and turn-cost spikes from `claudit watch`. The whole thing
// is intentionally simple: shell out to the platform's notifier (no
// extra deps), swallow any errors, and never block the caller.
package notify

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// Notifier sends a desktop notification. Implementations should be
// non-blocking and safe to call from any goroutine. Send returns an
// error for diagnostics but callers in `claudit watch` ignore it —
// failed notifications are not worth interrupting the live stream.
type Notifier interface {
	Send(title, body string) error
}

// Default picks the right notifier for the current OS.
//   - darwin:  osascript "display notification"
//   - linux:   notify-send (if on PATH)
//   - windows: PowerShell NotifyIcon balloon tip
//   - other:   a no-op
//
// Returns a non-nil Notifier in all cases — callers don't need to
// nil-check. The no-op variant exists so platform code stays linear.
func Default() Notifier {
	if n := tryWindowsNotifier(); n != nil {
		return n
	}
	switch runtime.GOOS {
	case "darwin":
		return macNotifier{}
	case "linux":
		if _, err := exec.LookPath("notify-send"); err == nil {
			return linuxNotifier{}
		}
	}
	return noopNotifier{}
}

type noopNotifier struct{}

func (noopNotifier) Send(string, string) error { return nil }

type macNotifier struct{}

// Send invokes `osascript -e 'display notification "body" with title "title"'`.
// Both fields are escaped for AppleScript string literals (backslash,
// double-quote). Anything stranger — multi-line bodies, unicode quotes
// — gets through unchanged; AppleScript tolerates it.
func (macNotifier) Send(title, body string) error {
	script := "display notification \"" + escapeAppleScript(body) +
		"\" with title \"" + escapeAppleScript(title) + "\""
	return exec.Command("osascript", "-e", script).Run()
}

type linuxNotifier struct{}

// Send invokes `notify-send "title" "body"`. We rely on notify-send's
// own argv handling — no shell, no quoting issues.
func (linuxNotifier) Send(title, body string) error {
	return exec.Command("notify-send", title, body).Run()
}

// escapeAppleScript escapes backslashes and double-quotes for embedding
// inside an AppleScript double-quoted string. We do not strip control
// characters — AppleScript handles them and the user already controls
// the message content (this is local-only).
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// ErrUnsupported is returned by some platforms when notifications are
// not available. Currently unused by Default() (which falls back to a
// no-op) but exposed for callers that want to detect "no notifier
// configured" via a typed error.
var ErrUnsupported = errors.New("notify: not supported on this platform")
