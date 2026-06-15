package shared

import "github.com/anthropic-ai/super-agent-v3/internal/util/idgen"

// NewAgentID delegates to idgen.NewAgentID.
// NewAgentID 创建代理ID。
func NewAgentID() string { return idgen.NewAgentID() }

// NewChildAgentID delegates to idgen.NewChildAgentID.
// NewChildAgentID 创建child代理ID。
func NewChildAgentID(parentID string, seq int) string { return idgen.NewChildAgentID(parentID, seq) }
