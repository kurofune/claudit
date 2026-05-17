//go:build darwin || linux

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// startResizeHandler subscribes to SIGWINCH and asks the painter to
// re-query terminal size + repaint the last frame on every resize.
// The listener goroutine exits when Close() closes p.stopCh.
func (p *screenPainter) startResizeHandler() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(ch)
		for {
			select {
			case <-p.stopCh:
				return
			case <-ch:
				p.handleResize()
			}
		}
	}()
}
