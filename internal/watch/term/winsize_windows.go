//go:build windows

package term

import (
	"syscall"
	"unsafe"
)

// Windows-side terminal-size query via GetConsoleScreenBufferInfo on
// kernel32.dll. Done with syscall.NewLazyDLL so we don't pull in
// golang.org/x/sys — keeps the dependency surface limited to stdlib.

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

// coord and smallRect mirror the layouts in wincon.h. Field order is
// load-bearing — do not rearrange.
type coord struct {
	X, Y int16
}

type smallRect struct {
	Left, Top, Right, Bottom int16
}

type consoleScreenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

// sysWinsize queries the visible-window dimensions of the console
// attached to fd. We return Window (the viewport) rather than Size
// (the back-scroll buffer) — Size is typically much taller than what
// the user sees and would cause us to draw off-screen.
func sysWinsize(fd uintptr) (cols, rows int, ok bool) {
	var info consoleScreenBufferInfo
	r1, _, _ := procGetConsoleScreenBufferInfo.Call(fd, uintptr(unsafe.Pointer(&info)))
	if r1 == 0 {
		return 0, 0, false
	}
	cols = int(info.Window.Right-info.Window.Left) + 1
	rows = int(info.Window.Bottom-info.Window.Top) + 1
	if cols <= 0 || rows <= 0 {
		return 0, 0, false
	}
	return cols, rows, true
}
