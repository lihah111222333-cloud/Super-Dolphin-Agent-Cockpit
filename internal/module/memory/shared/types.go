package memshared

import (
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// MemoryType 是记忆条目的分类标签，用于过滤和路由。
type MemoryType string

// 记忆类型枚举：unknown 为解析失败兜底，其余为业务分类。
const (
	MemoryTypeUnknown   MemoryType = "unknown"
	MemoryTypeUser      MemoryType = "user"
	MemoryTypeFeedback  MemoryType = "feedback"
	MemoryTypeProject   MemoryType = "project"
	MemoryTypeReference MemoryType = "reference"
)

// ParseMemoryType 将字符串解析为 MemoryType，大小写不敏感，未知值返回 MemoryTypeUnknown。
func ParseMemoryType(raw string) MemoryType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user":
		return MemoryTypeUser
	case "feedback":
		return MemoryTypeFeedback
	case "project":
		return MemoryTypeProject
	case "reference":
		return MemoryTypeReference
	default:
		return MemoryTypeUnknown
	}
}

// IsKnown 判断当前 MemoryType 是否为已知的有效类型。
func (t MemoryType) IsKnown() bool {
	switch ParseMemoryType(string(t)) {
	case MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference:
		return true
	default:
		return false
	}
}

// MemoryFrontmatter 是记忆文件 YAML frontmatter 的结构化表示。
type MemoryFrontmatter struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Type        *MemoryType `yaml:"type,omitempty"`
	Lang        string      `yaml:"lang,omitempty"`
	Aliases     []string    `yaml:"aliases,omitempty"`
	SearchKeys  []string    `yaml:"search_keys,omitempty"`
	Title       string      `yaml:"title,omitempty"`
	// Source 标记记忆条目的来源，例如 dream 自动巩固写入时为 "dream"；用户手动写入时留空。
	Source string `yaml:"source,omitempty"`
}

// MemoryEntry 是加载到内存中的单条记忆，包含 frontmatter 元数据与正文内容。
type MemoryEntry struct {
	Frontmatter   MemoryFrontmatter `yaml:",inline"`
	Content       string            `yaml:"-"`
	FilePath      string            `yaml:"-"`
	CanonicalName string            `yaml:"-"`
	UpdatedAt     time.Time         `yaml:"-"`
}

// ParsedMemory 是从磁盘解析后的记忆文件结构，含原始内容和 include 引用列表。
type ParsedMemory struct {
	Content                string
	RawContent             string
	Frontmatter            MemoryFrontmatter
	Includes               []string
	ContentDiffersFromDisk bool
}

// Type 返回该记忆条目的分类类型。
func (e MemoryEntry) Type() MemoryType {
	if e.Frontmatter.Type == nil {
		return MemoryTypeUnknown
	}
	return ParseMemoryType(string(*e.Frontmatter.Type))
}

// CanonicalName 将原始字符串规范化为 Unicode NFC 折叠后的标准名称，用于去重和查找。
func CanonicalName(raw string) string {
	folded := cases.Fold().String(norm.NFC.String(strings.TrimSpace(raw)))
	return strings.Join(strings.Fields(folded), " ")
}

// CloneMemoryType 深拷贝 MemoryType 并返回指针，确保写操作不影响原始值。
func CloneMemoryType(t MemoryType) *MemoryType {
	parsed := ParseMemoryType(string(t))
	return &parsed
}

// NormalizeStringSlice 去重并规范化字符串切片，忽略空白项，保持原始大小写但以标准名称去重。
func NormalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(norm.NFC.String(strings.TrimSpace(value))), " ")
		if value == "" {
			continue
		}
		key := CanonicalName(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, value)
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}
