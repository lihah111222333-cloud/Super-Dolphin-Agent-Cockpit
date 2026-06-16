package kernel

import "github.com/anthropic-ai/super-agent-v3/internal/util/idgen"

// NewID delegates to idgen.NewID.
// NewID 创建ID。
func NewID(prefix string) string { return idgen.NewID(prefix) }
