package shared

import (
	"fmt"
	"sync/atomic"
	"time"
)

var lastAgentIDValue atomic.Uint64

// NewAgentID generates a root agent ID: agent_{monotonicNumericTimestamp}.
// Concurrent launches can happen inside the same clock tick, so the value is
// process-local monotonic instead of relying on wall-clock uniqueness alone.
func NewAgentID() string {
	return fmt.Sprintf("agent_%d", nextAgentIDValue())
}

func nextAgentIDValue() uint64 {
	for {
		now := uint64(time.Now().UnixNano())
		last := lastAgentIDValue.Load()
		candidate := now
		if candidate <= last {
			candidate = last + 1
		}
		if lastAgentIDValue.CompareAndSwap(last, candidate) {
			return candidate
		}
	}
}

// NewChildAgentID generates a child agent ID by appending a sequential
// suffix to the parent's ID: {parentID}-{seq}.
// The caller is responsible for determining the correct sequence number
// (typically via a COUNT query on existing children in the database).
func NewChildAgentID(parentID string, seq int) string {
	return fmt.Sprintf("%s-%d", parentID, seq)
}
