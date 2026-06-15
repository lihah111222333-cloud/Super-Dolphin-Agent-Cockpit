package retrieval

import memshared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"

type MemoryType = memshared.MemoryType

const (
	MemoryTypeUnknown   = memshared.MemoryTypeUnknown
	MemoryTypeUser      = memshared.MemoryTypeUser
	MemoryTypeFeedback  = memshared.MemoryTypeFeedback
	MemoryTypeProject   = memshared.MemoryTypeProject
	MemoryTypeReference = memshared.MemoryTypeReference
)

type MemoryFrontmatter = memshared.MemoryFrontmatter
type MemoryEntry = memshared.MemoryEntry
type ParsedMemory = memshared.ParsedMemory

// ParseMemoryType 解析记忆type。
func ParseMemoryType(raw string) MemoryType { return memshared.ParseMemoryType(raw) }

// CanonicalName 处理canonical名称。
func CanonicalName(raw string) string { return memshared.CanonicalName(raw) }

func cloneMemoryType(t MemoryType) *MemoryType {
	return memshared.CloneMemoryType(t)
}

func normalizeStringSlice(values []string) []string {
	return memshared.NormalizeStringSlice(values)
}
