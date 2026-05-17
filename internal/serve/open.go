package serve

import (
	"os"
	"os/exec"
	"runtime"
)

// OpenBrowser asks the OS to open url in the user's default browser.
// Returns nil on success of the launcher (which does NOT mean a
// browser actually appeared — the launcher just hands off to the
// shell). Best-effort: callers should ignore the error and print the
// URL anyway so a user on a headless box still has the link.
func OpenBrowser(url string) error {
	cmd, args := browserCommand(url)
	if cmd == "" {
		return nil
	}
	c := exec.Command(cmd, args...)
	// We don't want browser stdout/stderr scribbling over the
	// daemon's own log line. Drop both.
	c.Stdout = nil
	c.Stderr = nil
	// Start (not Run) — the browser process keeps running after we
	// release it. We don't care about exit status.
	return c.Start()
}

// browserCommand returns the (cmd, args) tuple to invoke for the
// current OS, or ("", nil) when the platform is unsupported.
func browserCommand(url string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{url}
	case "windows":
		// `rundll32 url.dll,FileProtocolHandler <url>` is the
		// canonical "open in default browser" incantation that
		// avoids the cmd.exe quoting traps `start` runs into.
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "linux", "freebsd", "openbsd", "netbsd":
		return "xdg-open", []string{url}
	}
	return "", nil
}

// LooksHeadless returns true when auto-open is almost certainly the
// wrong default: stdout is not a terminal (script/pipe), or we're on
// Linux with no DISPLAY/WAYLAND_DISPLAY (so xdg-open has nowhere to
// go). Conservatively false on macOS/Windows where the desktop is
// usually available.
func LooksHeadless() bool {
	fi, err := os.Stdout.Stat()
	if err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		return true
	}
	if runtime.GOOS == "linux" {
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return true
		}
	}
	return false
}
