package serve

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// sseKeepaliveInterval bounds how long an idle event stream can sit
// silent before the server emits a comment frame. 15s is well under
// the default 60s nginx proxy timeout and the typical 30-60s router
// NAT timeout; without it, an SSE connection through a middlebox
// silently dies and the client only learns when its next write fails.
const sseKeepaliveInterval = 15 * time.Second

// handleEvents serves the Server-Sent Events stream that the SPA's
// auto-reload nudge subscribes to. Each frame is one of:
//   - `data: {"generation":N}\n\n` — a snapshot generation bump
//   - `: keepalive\n\n` — a comment frame (idle heartbeat)
//
// The handler emits an initial generation event on connect so a
// browser that joins mid-stream syncs to the current state without
// waiting for the next refresh.
//
// Shutdown drain: selects on s.shutdownCh in addition to the request
// context so a serve()-level context cancel wakes every active stream
// before http.Server.Shutdown is called. Without this, in-flight SSE
// connections would pin Shutdown until shutdownTimeout fires — the
// Phase-2 risk the plan calls out and TestEvents_DrainsOnServerShutdown
// guards against.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// httptest.NewRecorder doesn't implement Flusher; production
		// net/http does. Surface as 500 rather than silently writing
		// bytes that never reach the client.
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch, unsub := s.cache.SubscribeGeneration()
	defer unsub()

	// Initial event: send the current generation so the just-connected
	// client knows what it's looking at. Without this, the client has
	// to wait for the next file change to learn anything — which on a
	// quiet system could be hours.
	if snap := s.cache.Snapshot(); snap != nil {
		if !writeGenerationEvent(w, flusher, snap.Generation) {
			return
		}
	}

	ticker := time.NewTicker(sseKeepaliveInterval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.shutdownCh:
			return
		case gen, ok := <-ch:
			if !ok {
				// Subscribe channel closed under us (e.g., concurrent
				// unsubscribe). Treat as end-of-stream.
				return
			}
			if !writeGenerationEvent(w, flusher, gen) {
				return
			}
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				s.reqLogger(ctx).LogAttrs(ctx, slog.LevelDebug, "serve: events keepalive write failed",
					slog.Any("err", err),
					slog.String("path", "/events"))
				return
			}
			flusher.Flush()
		}
	}
}

// writeGenerationEvent emits one SSE data frame and flushes. Returns
// false on a write error so the caller can abandon the stream.
// Logging is at Debug because a closed client connection is the
// expected end-of-life for any SSE handler and shouldn't ladder up to
// operator-visible Error noise.
func writeGenerationEvent(w http.ResponseWriter, flusher http.Flusher, gen int64) bool {
	if _, err := fmt.Fprintf(w, "data: {\"generation\":%d}\n\n", gen); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
