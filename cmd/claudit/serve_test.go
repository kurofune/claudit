package main

import (
	"runtime/debug"
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
