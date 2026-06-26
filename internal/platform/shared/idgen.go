package shared

import "github.com/anthropic-ai/super-agent-v3/internal/util/idgen"

// NewID 生成带前缀的随机 ID，保持 shared 包旧入口兼容。
func NewID(prefix string) string { return idgen.NewID(prefix) }
