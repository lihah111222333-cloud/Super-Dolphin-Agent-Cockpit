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

// ParseMemoryType 将字符串解析为共享记忆类型枚举。
// retrieval 子包通过该包装避免直接扩散 shared 实现细节。
func ParseMemoryType(raw string) MemoryType { return memshared.ParseMemoryType(raw) }

// CanonicalName 生成检索和去重使用的规范化名称。
// 与父 memory 包保持同一实现，确保 manifest、索引和检索排序使用相同 key。
func CanonicalName(raw string) string { return memshared.CanonicalName(raw) }

// cloneMemoryType 返回记忆类型指针副本。
// frontmatter 结构需要指针表示缺失类型，克隆可避免共享可变指针。
func cloneMemoryType(t MemoryType) *MemoryType {
	return memshared.CloneMemoryType(t)
}

// normalizeStringSlice 清理、去重并规范化字符串切片。
// retrieval 读取 aliases/search_keys 时与父包保持一致。
func normalizeStringSlice(values []string) []string {
	return memshared.NormalizeStringSlice(values)
}
