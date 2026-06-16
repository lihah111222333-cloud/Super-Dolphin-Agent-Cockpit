package kernel

import (
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
)

// LooksLikeUUID reports whether s has a UUID-like shape.
func LooksLikeUUID(s string) bool {
	return identifier.LooksLikeUUID(s)
}

// IsClaudeCLISessionUUID reports whether s is a Claude CLI session UUID.
func IsClaudeCLISessionUUID(s string) bool {
	return identifier.IsClaudeCLISessionUUID(s)
}
