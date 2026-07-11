package shared

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"

// NewAgentID 生成顶层 agent ID。
func NewAgentID() string { return idgen.NewAgentID() }

// NewChildAgentID 根据父 agent ID 和序号生成子 agent ID。
func NewChildAgentID(parentID string, seq int) string { return idgen.NewChildAgentID(parentID, seq) }
