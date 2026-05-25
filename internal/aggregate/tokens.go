package aggregate

// Total returns the sum of all five token categories — the headline
// "tokens used" figure. Unlike Cacheable/Miss (cache.go), Output IS
// included: this is total throughput, not the cacheable subset.
func (t Tokens) Total() int64 {
	return t.InputTokens +
		t.OutputTokens +
		t.CacheCreate5mTokens +
		t.CacheCreate1hTokens +
		t.CacheReadTokens
}
