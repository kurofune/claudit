package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestApplyServeMemoryLimit_SetsLimitWhenGOMEMLIMITUnset(t *testing.T) {
	// Tests share a process: capture the current limit and restore it
	// afterwards so other tests see the runtime state they expect.
	prev := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(prev) })
	t.Setenv("GOMEMLIMIT", "")

	applyServeMemoryLimit()

	got := debug.SetMemoryLimit(-1)
	const want = int64(1536 << 20) // 1.5 GiB
	if got != want {
		t.Errorf("memory limit = %d, want %d", got, want)
	}
}

func TestApplyServeMemoryLimit_RespectsExplicitGOMEMLIMIT(t *testing.T) {
	prev := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(prev) })
	t.Setenv("GOMEMLIMIT", "2GiB")

	// Pin the limit to a sentinel value; the helper must not touch it
	// when GOMEMLIMIT is explicitly set (the runtime already applied it).
	const sentinel = int64(2 << 30)
	debug.SetMemoryLimit(sentinel)

	applyServeMemoryLimit()

	if got := debug.SetMemoryLimit(-1); got != sentinel {
		t.Errorf("memory limit = %d, want untouched sentinel %d", got, sentinel)
	}
}

func TestBindListenAddr_RejectsHostWithPort(t *testing.T) {
	rejected := []string{
		"127.0.0.1:8791",
		"localhost:8080",
		"[::1]:8080",
	}
	for _, bind := range rejected {
		t.Run(bind, func(t *testing.T) {
			_, err := bindListenAddr(bind, 8787)
			if err == nil {
				t.Fatalf("bindListenAddr(%q, 8787) = nil error, want error", bind)
			}
			if !strings.Contains(err.Error(), "--bind takes a host only") {
				t.Errorf("error = %q, want it to mention \"--bind takes a host only\"", err)
			}
			if !strings.Contains(err.Error(), "--port") {
				t.Errorf("error = %q, want it to point at --port", err)
			}
		})
	}
}

func TestBindListenAddr_AcceptsHostAndComposesAddress(t *testing.T) {
	cases := []struct {
		bind string
		port int
		want string
	}{
		{"127.0.0.1", 8787, "127.0.0.1:8787"},
		{"::1", 8787, "[::1]:8787"},
		{"[::1]", 8787, "[::1]:8787"},
		{"localhost", 8787, "localhost:8787"},
		{"0.0.0.0", 8787, "0.0.0.0:8787"},
		// Empty host keeps the historical all-interfaces ":port" form
		// (net.Listen treats ":8787" as bind-everything; the startup
		// warning still fires via isNonLoopbackBind("")).
		{"", 8787, ":8787"},
	}
	for _, tc := range cases {
		t.Run(tc.bind, func(t *testing.T) {
			got, err := bindListenAddr(tc.bind, tc.port)
			if err != nil {
				t.Fatalf("bindListenAddr(%q, %d) error: %v", tc.bind, tc.port, err)
			}
			if got != tc.want {
				t.Errorf("bindListenAddr(%q, %d) = %q, want %q", tc.bind, tc.port, got, tc.want)
			}
		})
	}
}

func TestRunServe_RejectsBindWithPort(t *testing.T) {
	err := runServe([]string{"--bind", "127.0.0.1:8791", "--open=false", "--root", t.TempDir()})
	if err == nil {
		t.Fatal("runServe with --bind 127.0.0.1:8791 = nil error, want validation error")
	}
	if !strings.Contains(err.Error(), "--bind takes a host only") {
		t.Errorf("error = %q, want the --bind validation message", err)
	}
}

func TestIsNonLoopbackBind(t *testing.T) {
	cases := []struct {
		bind string
		want bool
	}{
		{"127.0.0.1", false},
		{"0.0.0.0", true},
		{"", true},
		{"::1", false},
		{"::", true},
		{"[::]", true},
		{"[::1]", false},
		{"::0", true},
		{"[::0]", true},
		{"0.0.0.1", true},
		{"127.0.0.5", false},
		{"192.168.1.10", true},
		{"localhost", false},
		{"Localhost", false},
		{"myhost.local", true},
	}
	for _, tc := range cases {
		t.Run(tc.bind, func(t *testing.T) {
			if got := isNonLoopbackBind(tc.bind); got != tc.want {
				t.Errorf("isNonLoopbackBind(%q) = %v, want %v", tc.bind, got, tc.want)
			}
		})
	}
}
