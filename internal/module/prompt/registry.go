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

// NewSectionRegistry 创建空的并发安全 section 注册表。
func NewSectionRegistry() *SectionRegistry {
	return &SectionRegistry{sections: map[string]PromptSection{}}
}

// Register 注册单个 prompt section；重复名称直接报错，防止启动期覆盖内置 section。
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

// Sections 返回按 order/name 排序的 section 快照，调用方可安全修改返回切片。
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
