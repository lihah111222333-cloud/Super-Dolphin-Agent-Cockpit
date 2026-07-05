package threadprompt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ProjectDefaultRulesProvider 渲染当前项目可用的 default_rule 动态 section。
type ProjectDefaultRulesProvider struct{ catalog RuntimePromptCatalog }

// SectionName 返回本 provider 负责的动态 section 名称。
func (ProjectDefaultRulesProvider) SectionName() string {
	return contract.DynamicSectionProjectDefaultRules
}

// Resolve 解析项目默认规则，列出当前 CWD 下所有 default_rule section 并渲染为 prompt 文本。
func (p ProjectDefaultRulesProvider) Resolve(ctx context.Context, input contract.SectionContext) (*string, error) {
	start := time.Now()
	if p.catalog == nil {
		err := fmt.Errorf("runtime prompt catalog is required for dynamic section %q", contract.DynamicSectionProjectDefaultRules)
		logDynamicSectionResolveFailed(contract.DynamicSectionProjectDefaultRules, start, err)
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionProjectDefaultRules, err)
	}
	cwd := contract.SectionContextCWD(input)
	if cwd == "" {
		err := fmt.Errorf("cwd is required for dynamic section %q", contract.DynamicSectionProjectDefaultRules)
		logDynamicSectionResolveFailed(contract.DynamicSectionProjectDefaultRules, start, err)
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionProjectDefaultRules, err)
	}
	sections, err := p.catalog.ListDefaultRuleSections(ctx, cwd)
	if err != nil {
		logDynamicSectionResolveFailed(contract.DynamicSectionProjectDefaultRules, start, err)
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionProjectDefaultRules, err)
	}
	text := renderProjectDefaultRules(sections)
	if text == "" {
		return nil, nil
	}
	logDynamicSectionResolved(contract.DynamicSectionProjectDefaultRules, start, true, "rule_count", len(sections), "body_len", len(text))
	return &text, nil
}

func renderProjectDefaultRules(sections []PromptTemplateSection) string {
	sections = effectiveDefaultRuleSections(sections)
	lines := []string{"项目和全局默认规则："}
	for _, section := range sections {
		body := strings.TrimSpace(section.Body)
		if body == "" {
			continue
		}
		lines = append(lines, "- "+body)
	}
	if len(lines) == 1 {
		return ""
	}
	lines = append(lines, "", "这些规则只适用于当前项目；不得覆盖用户本轮明确指令、系统安全边界或工具权限。")
	return strings.Join(lines, "\n")
}

// effectiveDefaultRuleSections 按 identity 去重并保留作用域更精确的 default_rule section，维持原始顺序。
func effectiveDefaultRuleSections(sections []PromptTemplateSection) []PromptTemplateSection {
	byKey := map[string]PromptTemplateSection{}
	order := make([]string, 0, len(sections))
	for _, section := range sections {
		key := defaultRuleIdentity(section)
		if key == "" {
			continue
		}
		if _, ok := byKey[key]; !ok {
			order = append(order, key)
		}
		if current, ok := byKey[key]; !ok || preferPromptSection(section, current) {
			byKey[key] = section
		}
	}
	out := make([]PromptTemplateSection, 0, len(order))
	for _, key := range order {
		if section, ok := byKey[key]; ok {
			out = append(out, section)
		}
	}
	return out
}

// defaultRuleIdentity 生成 default_rule section 的去重键，依次用 title+sectionKey、sectionKey、promptKey、body 作为候选。
func defaultRuleIdentity(section PromptTemplateSection) string {
	sectionKey := strings.ToLower(strings.TrimSpace(section.SectionKey))
	if title := strings.ToLower(strings.TrimSpace(section.TemplateTitle)); title != "" {
		if sectionKey != "" {
			return title + "\x00" + sectionKey
		}
		return title
	}
	if sectionKey != "" {
		return sectionKey
	}
	if promptKey := strings.ToLower(strings.TrimSpace(section.TemplatePromptKey)); promptKey != "" {
		return promptKey
	}
	if body := strings.ToLower(strings.TrimSpace(section.Body)); body != "" {
		return body
	}
	return ""
}
