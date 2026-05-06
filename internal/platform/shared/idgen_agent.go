package shared

import "github.com/anthropic-ai/super-agent-v3/internal/util/idgen"

// NewAgentID delegates to idgen.NewAgentID.
func NewAgentID() string { return idgen.NewAgentID() }

// NewChildAgentID delegates to idgen.NewChildAgentID.
func NewChildAgentID(parentID string, seq int) string { return idgen.NewChildAgentID(parentID, seq) }
