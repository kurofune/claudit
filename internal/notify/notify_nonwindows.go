//go:build !windows

package notify

// tryWindowsNotifier returns nil everywhere except Windows so the
// shared Default() routine can probe for a Windows notifier without
// pulling Windows-only types into the build on every platform.
func tryWindowsNotifier() Notifier { return nil }
