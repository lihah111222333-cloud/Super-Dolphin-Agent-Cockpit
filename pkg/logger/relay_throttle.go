package logger

import "sync/atomic"

// RelayFailureThrottle suppresses repetitive relay failure logs.
// Reports the first 3 failures, then every 20th. Aligned with V2
// internal/mcp/log_relay.go reportFailure pattern.
type RelayFailureThrottle struct {
	count atomic.Int64
}

// ShouldReport returns true if this failure occurrence should be logged.
// ShouldReport 判断report是否可用。
func (t *RelayFailureThrottle) ShouldReport() bool {
	n := t.count.Add(1)
	return n <= 3 || n%20 == 0
}
