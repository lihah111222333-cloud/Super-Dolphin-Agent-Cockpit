package threadprompt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

type ProjectDefaultRulesProvider struct{ catalog RuntimePromptCatalog }

// SectionName 处理section名称。
func (ProjectDefaultRulesProvider) SectionName() string {
	return contract.DynamicSectionProjectDefaultRules
}

// Resolve 解析threadprompt。
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

func renderProjectDefaultRules(sections []promptstore.PromptTemplateSection) string {
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

// effectiveDefaultRuleSections 处理effectivedefaultrulesections。
func effectiveDefaultRuleSections(sections []promptstore.PromptTemplateSection) []promptstore.PromptTemplateSection {
	byKey := map[string]promptstore.PromptTemplateSection{}
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
	out := make([]promptstore.PromptTemplateSection, 0, len(order))
	for _, key := range order {
		if section, ok := byKey[key]; ok {
			out = append(out, section)
		}
	}
	return out
}

// defaultRuleIdentity 处理defaultrule身份。
func defaultRuleIdentity(section promptstore.PromptTemplateSection) string {
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
