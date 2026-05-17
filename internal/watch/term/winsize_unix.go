//go:build darwin || linux

package term

import (
	"syscall"
	"unsafe"
)

type winsize struct {
	Row, Col, X, Y uint16
}

// sysWinsize asks the kernel for the current terminal dimensions of fd
// via TIOCGWINSZ. Returns ok=false when the fd is not a terminal or
// the ioctl fails — callers fall back to env-var / default size.
func sysWinsize(fd uintptr) (cols, rows int, ok bool) {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(tiocgwinsz), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0, 0, false
	}
	if ws.Col == 0 || ws.Row == 0 {
		return 0, 0, false
	}
	return int(ws.Col), int(ws.Row), true
}
