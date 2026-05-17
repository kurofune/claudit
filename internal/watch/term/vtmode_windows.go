//go:build windows

package term

import (
	"os"
	"unsafe"
)

// Enable Windows' "virtual terminal" console mode at package init so
// ANSI escape sequences emitted by Style/Screen render as colors and
// cursor moves rather than literal text. Windows 10 1903+ supports
// this; Windows Terminal / PowerShell 7+ / VS Code terminal usually
// have it on already. Calling SetConsoleMode is idempotent.
//
// Failures are silent — a console that refuses VT mode (legacy
// console host on a very old Windows) will render literal escapes,
// which is no worse than what would happen if we did nothing. The
// alternative (refusing to start) is worse.

const enableVirtualTerminalProcessing = 0x0004

var (
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

func init() {
	enableVT(os.Stdout.Fd())
	enableVT(os.Stderr.Fd())
}

func enableVT(fd uintptr) {
	var mode uint32
	r1, _, _ := procGetConsoleMode.Call(fd, uintptr(unsafe.Pointer(&mode)))
	if r1 == 0 {
		return // not a console (redirected to file/pipe) — nothing to do
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return // already enabled
	}
	procSetConsoleMode.Call(fd, uintptr(mode|enableVirtualTerminalProcessing))
}
