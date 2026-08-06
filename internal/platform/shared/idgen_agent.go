package shared

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"

// NewAgentID 使用调用方显式持有的生成器创建顶层 agent ID。
func NewAgentID(generator *idgen.Generator) string { return generator.NewAgentID() }

// NewChildAgentID 根据父 agent ID 和序号生成子 agent ID。
func NewChildAgentID(parentID string, seq int) string { return idgen.NewChildAgentID(parentID, seq) }
