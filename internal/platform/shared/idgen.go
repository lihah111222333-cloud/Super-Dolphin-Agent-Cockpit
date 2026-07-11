package shared

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"

// NewID 生成带前缀的随机 ID，保持 shared 包旧入口兼容。
func NewID(prefix string) string { return idgen.NewID(prefix) }
