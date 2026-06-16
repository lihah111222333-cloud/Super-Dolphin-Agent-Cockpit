package memdata

import (
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// MemoryType identifies the semantic category of a memory entry.
type MemoryType string

const (
	MemoryTypeUnknown   MemoryType = "unknown"
	MemoryTypeUser      MemoryType = "user"
	MemoryTypeFeedback  MemoryType = "feedback"
	MemoryTypeProject   MemoryType = "project"
	MemoryTypeReference MemoryType = "reference"
)

// ParseMemoryType 解析记忆type。
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

// IsKnown 判断known是否可用。
func (t MemoryType) IsKnown() bool {
	switch ParseMemoryType(string(t)) {
	case MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference:
		return true
	default:
		return false
	}
}

// MemoryFrontmatter is the YAML metadata stored before memory content.
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

// MemoryEntry is the canonical in-memory representation of a memory file.
type MemoryEntry struct {
	Frontmatter   MemoryFrontmatter `yaml:",inline"`
	Content       string            `yaml:"-"`
	FilePath      string            `yaml:"-"`
	CanonicalName string            `yaml:"-"`
	UpdatedAt     time.Time         `yaml:"-"`
}

// ParsedMemory is the parsed content and metadata returned by memory readers.
type ParsedMemory struct {
	Content                string
	RawContent             string
	Frontmatter            MemoryFrontmatter
	Includes               []string
	ContentDiffersFromDisk bool
}

// Type 返回事件分发用的类型编号。
func (e MemoryEntry) Type() MemoryType {
	if e.Frontmatter.Type == nil {
		return MemoryTypeUnknown
	}
	return ParseMemoryType(string(*e.Frontmatter.Type))
}

// CanonicalName 处理canonical名称。
func CanonicalName(raw string) string {
	folded := cases.Fold().String(norm.NFC.String(strings.TrimSpace(raw)))
	return strings.Join(strings.Fields(folded), " ")
}

// CloneMemoryType 复制记忆type。
func CloneMemoryType(t MemoryType) *MemoryType {
	parsed := ParseMemoryType(string(t))
	return &parsed
}

// NormalizeStringSlice 规范化stringslice。
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
