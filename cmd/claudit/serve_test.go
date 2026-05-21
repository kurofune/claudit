package main

import "testing"

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
