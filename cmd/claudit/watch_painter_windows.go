//go:build windows

package main

import "time"

// windowsResizePoll is how often the Windows resize watcher checks
// the console buffer dimensions. Half a second is short enough that
// a drag-resize redraws while the user is still adjusting the window
// and long enough that we don't wake the runtime needlessly.
const windowsResizePoll = 500 * time.Millisecond

// startResizeHandler runs a polling loop on Windows because the
// platform has no SIGWINCH equivalent. The loop exits when Close()
// closes p.stopCh.
func (p *screenPainter) startResizeHandler() {
	go func() {
		ticker := time.NewTicker(windowsResizePoll)
		defer ticker.Stop()
		for {
			select {
			case <-p.stopCh:
				return
			case <-ticker.C:
				p.pollResize()
			}
		}
	}()
}
