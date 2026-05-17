package notify

import "testing"

func TestEscapeAppleScript(t *testing.T) {
	cases := []struct{ in, want string }{
		{`plain`, `plain`},
		{`with "quote"`, `with \"quote\"`},
		{`back\slash`, `back\\slash`},
		{`both "and" \stuff`, `both \"and\" \\stuff`},
	}
	for _, c := range cases {
		if got := escapeAppleScript(c.in); got != c.want {
			t.Errorf("escapeAppleScript(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDefault_ReturnsNonNilNotifier(t *testing.T) {
	// On every platform Default must return something the caller can
	// invoke without nil-checking. We don't assert *which* notifier —
	// that varies by OS — only the contract.
	n := Default()
	if n == nil {
		t.Fatal("Default() returned nil")
	}
	// Send must not panic. We don't care whether it succeeds in the
	// test environment (CI has no display).
	_ = n.Send("test", "body")
}

func TestNoopNotifier_AlwaysSucceeds(t *testing.T) {
	if err := (noopNotifier{}).Send("t", "b"); err != nil {
		t.Errorf("noop.Send = %v", err)
	}
}
