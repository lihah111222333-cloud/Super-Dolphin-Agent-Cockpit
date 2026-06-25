package prompt

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// SectionRegistry 保存所有已注册的 prompt section，并发安全。
type SectionRegistry struct {
	mu       sync.RWMutex
	sections map[string]PromptSection
}

// NewSectionRegistry 创建section注册表。
func NewSectionRegistry() *SectionRegistry {
	return &SectionRegistry{sections: map[string]PromptSection{}}
}

// Register 注册prompt。
func (r *SectionRegistry) Register(section PromptSection) error {
	name := strings.TrimSpace(section.Name)
	if name == "" {
		return fmt.Errorf("section name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sections[name]; exists {
		return fmt.Errorf("section %q already registered", name)
	}
	section.Name = name
	r.sections[name] = section
	return nil
}

// Sections 处理sections。
func (r *SectionRegistry) Sections() []PromptSection {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]PromptSection, 0, len(r.sections))
	for _, section := range r.sections {
		out = append(out, section)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order == out[j].Order {
			return out[i].Name < out[j].Name
		}
		return out[i].Order < out[j].Order
	})
	return out
}
