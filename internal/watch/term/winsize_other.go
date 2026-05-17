//go:build !darwin && !linux && !windows

package term

// Stub for platforms without a kernel terminal-size syscall. Currently
// catches BSDs other than darwin and any other Unix variant we haven't
// special-cased.
func sysWinsize(_ uintptr) (cols, rows int, ok bool) {
	return 0, 0, false
}
