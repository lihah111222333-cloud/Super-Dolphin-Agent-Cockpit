package shared

import (
	"fmt"
	"time"
)

// NewAgentID generates a root agent ID: agent_{millisecondTimestamp}.
// For single-user desktop applications the millisecond resolution is
// sufficient to avoid collisions.
func NewAgentID() string {
	return fmt.Sprintf("agent_%d", time.Now().UnixMilli())
}

// NewChildAgentID generates a child agent ID by appending a sequential
// suffix to the parent's ID: {parentID}-{seq}.
// The caller is responsible for determining the correct sequence number
// (typically via a COUNT query on existing children in the database).
func NewChildAgentID(parentID string, seq int) string {
	return fmt.Sprintf("%s-%d", parentID, seq)
}
