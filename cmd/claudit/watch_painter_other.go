//go:build !darwin && !linux && !windows

package main

// Catch-all for any other Unix variant we haven't special-cased.
// SIGWINCH support could be added (it's a Unix signal) but we'd need
// to confirm the file already builds — leaving as a no-op for safety.
func (p *screenPainter) startResizeHandler() {}
