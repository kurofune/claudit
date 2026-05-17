//go:build windows

package notify

import (
	"os/exec"
	"strings"
	"syscall"
)

// Windows notifier uses a one-shot PowerShell script that creates a
// System.Windows.Forms.NotifyIcon, shows a balloon tip, and disposes
// itself after a short delay. Works on all Windows 10/11 editions
// without requiring the BurntToast PowerShell module or admin rights.
//
// Native toast notifications via Windows.UI.Notifications would be
// nicer (they integrate with Action Center) but require an AppUserModelID
// and a shortcut registered in the Start menu — too much setup for a
// CLI tool. Balloon tips are good enough.

type winNotifier struct{}

func (winNotifier) Send(title, body string) error {
	script := `Add-Type -AssemblyName System.Windows.Forms; ` +
		`$n = New-Object System.Windows.Forms.NotifyIcon; ` +
		`$n.Icon = [System.Drawing.SystemIcons]::Information; ` +
		`$n.Visible = $true; ` +
		`$n.ShowBalloonTip(5000, '` + escapePowerShell(title) + `', '` + escapePowerShell(body) + `', 'Info'); ` +
		`Start-Sleep -Seconds 6; ` +
		`$n.Dispose()`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command", script)
	// Hide the PowerShell console window so the user doesn't see a
	// blank black window pop up alongside the balloon tip.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start() // fire-and-forget; the PowerShell process owns the balloon's lifetime
}

// tryWindowsNotifier returns a winNotifier on Windows so Default()
// can stay platform-neutral.
func tryWindowsNotifier() Notifier { return winNotifier{} }

// escapePowerShell escapes a string for embedding inside a single-
// quoted PowerShell literal. PowerShell single quotes have one
// escape rule: a literal single quote is written as two consecutive
// single quotes. Backslashes, newlines, and other characters pass
// through unchanged.
func escapePowerShell(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
