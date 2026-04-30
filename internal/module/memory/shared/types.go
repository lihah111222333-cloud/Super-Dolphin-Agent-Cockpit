package memshared

import (
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type MemoryType string

const (
	MemoryTypeUnknown   MemoryType = "unknown"
	MemoryTypeUser      MemoryType = "user"
	MemoryTypeFeedback  MemoryType = "feedback"
	MemoryTypeProject   MemoryType = "project"
	MemoryTypeReference MemoryType = "reference"
)

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

func (t MemoryType) IsKnown() bool {
	switch ParseMemoryType(string(t)) {
	case MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference:
		return true
	default:
		return false
	}
}

type MemoryFrontmatter struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Type        *MemoryType `yaml:"type,omitempty"`
	Lang        string      `yaml:"lang,omitempty"`
	Aliases     []string    `yaml:"aliases,omitempty"`
	SearchKeys  []string    `yaml:"search_keys,omitempty"`
	// Source 标记记忆条目的来源，例如 dream 自动巩固写入时为 "dream"；用户手动写入时留空。
	Source string `yaml:"source,omitempty"`
}

type MemoryEntry struct {
	Frontmatter   MemoryFrontmatter `yaml:",inline"`
	Content       string            `yaml:"-"`
	FilePath      string            `yaml:"-"`
	CanonicalName string            `yaml:"-"`
	UpdatedAt     time.Time         `yaml:"-"`
}

type ParsedMemory struct {
	Content                string
	RawContent             string
	Frontmatter            MemoryFrontmatter
	Includes               []string
	ContentDiffersFromDisk bool
}

func (e MemoryEntry) Type() MemoryType {
	if e.Frontmatter.Type == nil {
		return MemoryTypeUnknown
	}
	return ParseMemoryType(string(*e.Frontmatter.Type))
}

func CanonicalName(raw string) string {
	folded := cases.Fold().String(norm.NFC.String(strings.TrimSpace(raw)))
	return strings.Join(strings.Fields(folded), " ")
}

func CloneMemoryType(t MemoryType) *MemoryType {
	parsed := ParseMemoryType(string(t))
	return &parsed
}

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
