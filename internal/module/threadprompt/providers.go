package threadprompt

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// registerProviders 向 registrar 注册三个动态 section provider：项目默认规则、可用专家列表和知识回忆目录。
func registerProviders(registrar contract.DynamicSectionRegistrar, catalog RuntimePromptCatalog) error {
	if registrar == nil {
		return nil
	}
	if catalog == nil {
		return fmt.Errorf("thread: runtime prompt catalog is required")
	}
	providers := []contract.DynamicSectionProvider{
		ProjectDefaultRulesProvider{catalog: catalog},
		AvailableExpertsProvider{catalog: catalog},
		RecallCatalogProvider{catalog: catalog},
	}
	for _, provider := range providers {
		if err := registrar.RegisterDynamicProvider(provider); err != nil {
			return fmt.Errorf("thread: register dynamic provider %s: %w", provider.SectionName(), err)
		}
	}
	return nil
}

// AvailableExpertsProvider 根据 prompt catalog 暴露当前 CWD 可委派的专家列表。
type AvailableExpertsProvider struct{ catalog RuntimePromptCatalog }

// SectionName 返回本 provider 负责的动态 section 名称。
func (AvailableExpertsProvider) SectionName() string { return contract.DynamicSectionAvailableExperts }

// Resolve 解析可用专家列表，根据当前 CWD 过滤模板，按用户输入决定返回摘要还是完整说明。
func (p AvailableExpertsProvider) Resolve(ctx context.Context, input contract.SectionContext) (*string, error) {
	start := time.Now()
	if p.catalog == nil {
		err := fmt.Errorf("runtime prompt catalog is required for dynamic section %q", contract.DynamicSectionAvailableExperts)
		logDynamicSectionResolveFailed(contract.DynamicSectionAvailableExperts, start, err)
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionAvailableExperts, err)
	}
	userText := availableExpertsUserText(input)
	if userText == "" {
		return nil, nil
	}
	cwd := availableExpertsCWD(input)
	if cwd == "" {
		err := fmt.Errorf("cwd is required for dynamic section %q", contract.DynamicSectionAvailableExperts)
		logDynamicSectionResolveFailed(contract.DynamicSectionAvailableExperts, start, err)
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionAvailableExperts, err)
	}
	templates, err := p.catalog.ListTemplates(ctx, RuntimeListFilter{
		Limit: 200,
		CWD:   cwd,
	})
	if err != nil {
		logDynamicSectionResolveFailed(contract.DynamicSectionAvailableExperts, start, err)
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionAvailableExperts, err)
	}
	experts := availableExpertsFromTemplates(templates, availableExpertsPromptKey(input))
	if len(experts) == 0 {
		return nil, nil
	}
	text := renderAvailableExpertsShort(experts)
	renderMode := "short"
	if availableExpertsNeedsFull(userText) {
		text = text + "\n\n" + renderAvailableExpertsFull(experts)
		renderMode = "full"
	}
	logDynamicSectionResolved(contract.DynamicSectionAvailableExperts, start, true, "template_count", len(templates), "candidate_count", len(experts), "render_mode", renderMode, "body_len", len(text))
	return &text, nil
}

type availableExpert struct {
	PromptKey string
	Title     string
	WhenToUse string
	Priority  int
	ScopeRank int
}

func availableExpertsUserText(input contract.SectionContext) string {
	if input.Turn != nil {
		if text := strings.TrimSpace(input.Turn.UserText); text != "" {
			return text
		}
	}
	if input.Start != nil {
		return strings.TrimSpace(input.Start.Prompt)
	}
	return ""
}

func availableExpertsCWD(input contract.SectionContext) string {
	return contract.SectionContextCWD(input)
}

func availableExpertsPromptKey(input contract.SectionContext) string {
	if input.Start != nil {
		if promptKey := strings.TrimSpace(input.Start.PromptKey); promptKey != "" {
			return promptKey
		}
	}
	if input.Turn != nil {
		return strings.TrimSpace(input.Turn.PromptKey)
	}
	return ""
}

// availableExpertsFromTemplates 从模板列表提取可用专家，按 identity 去重并优先保留作用域更精确的候选。
func availableExpertsFromTemplates(templates []PromptTemplate, currentPromptKey string) []availableExpert {
	byIdentity := map[string]availableExpert{}
	currentPromptKey = strings.TrimSpace(currentPromptKey)
	for _, template := range templates {
		expert, ok := availableExpertFromTemplate(template, currentPromptKey)
		if !ok {
			continue
		}
		identity := availableExpertIdentity(expert)
		if current, ok := byIdentity[identity]; !ok || preferScopedCandidate(expert.ScopeRank, expert.Priority, expert.PromptKey, current.ScopeRank, current.Priority, current.PromptKey) {
			byIdentity[identity] = expert
		}
	}
	experts := make([]availableExpert, 0, len(byIdentity))
	for _, expert := range byIdentity {
		experts = append(experts, expert)
	}
	sort.SliceStable(experts, func(i, j int) bool {
		if experts[i].Priority != experts[j].Priority {
			return experts[i].Priority > experts[j].Priority
		}
		return experts[i].PromptKey < experts[j].PromptKey
	})
	return experts
}

// availableExpertFromTemplate 从单个模板构造专家信息，禁用、无 when_to_use 或当前 prompt key 相同时返回 false。
func availableExpertFromTemplate(template PromptTemplate, currentPromptKey string) (availableExpert, bool) {
	promptKey := strings.TrimSpace(template.PromptKey)
	whenToUse := strings.TrimSpace(template.WhenToUse)
	if !template.Enabled || promptKey == "" || whenToUse == "" || promptKey == currentPromptKey {
		return availableExpert{}, false
	}
	if isRuntimeAssetTemplate(template) {
		return availableExpert{}, false
	}
	return availableExpert{
		PromptKey: promptKey,
		Title:     strings.TrimSpace(template.Title),
		WhenToUse: whenToUse,
		Priority:  template.Priority,
		ScopeRank: templateScopeRank(template.Tags),
	}, true
}

func availableExpertIdentity(expert availableExpert) string {
	if title := strings.ToLower(strings.TrimSpace(expert.Title)); title != "" {
		return title
	}
	return strings.ToLower(strings.TrimSpace(expert.PromptKey))
}

func preferScopedCandidate(leftRank, leftPriority int, leftKey string, rightRank, rightPriority int, rightKey string) bool {
	if leftRank != rightRank {
		return leftRank < rightRank
	}
	if leftPriority != rightPriority {
		return leftPriority > rightPriority
	}
	return strings.TrimSpace(leftKey) < strings.TrimSpace(rightKey)
}

func availableExpertsNeedsFull(userText string) bool {
	text := strings.ToLower(strings.TrimSpace(userText))
	if text == "" {
		return false
	}
	for _, keyword := range availableExpertsFullKeywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return containsAny(text, availableExpertsSplitKeywords) && containsAny(text, availableExpertsDelegationTargetKeywords)
}

func containsAny(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

var availableExpertsFullKeywords = []string{
	"launch_agent",
	"delegate",
	"delegation",
	"orchestrate",
	"orchestration",
	"subagent",
	"sub-agent",
	"multiple agents",
	"parallel agents",
	"agent team",
	"子agent",
	"子 agent",
	"多agent",
	"多 agent",
	"多个agent",
	"多个 agent",
	"多个专家",
	"多位专家",
	"专家一起",
	"专家协作",
}

var availableExpertsSplitKeywords = []string{"拆分", "拆解", "分配", "分派", "派给", "交给"}

var availableExpertsDelegationTargetKeywords = []string{"专家", "agent", "子agent", "子 agent", "并行"}

func renderAvailableExpertsShort(experts []availableExpert) string {
	lines := []string{"可用专家（通过 launch_agent 调用）："}
	for _, expert := range experts {
		escapedPromptKey := escapePromptKeyForInstruction(expert.PromptKey)
		lines = append(lines, "- "+expert.PromptKey+" (prompt_key='"+escapedPromptKey+"'): "+expert.WhenToUse)
	}
	lines = append(lines, "", "判断标准：简单任务直接答；多领域 / 单一深度专业 / 用户明确拆解时再 delegate。", "不确定时直接答，不要为了显得努力而 delegate。")
	return strings.Join(lines, "\n")
}

func renderAvailableExpertsFull(experts []availableExpert) string {
	lines := []string{"可用专家详细说明：", ""}
	for _, expert := range experts {
		escapedPromptKey := escapePromptKeyForInstruction(expert.PromptKey)
		lines = append(lines, "【"+expert.PromptKey+"】", "when_to_use: "+expert.WhenToUse, "调用：launch_agent(name='"+escapedPromptKey+"', prompt_key='"+escapedPromptKey+"', prompt='<具体任务>')", "")
	}
	lines = append(lines, "判断准则：", "1. 多个独立子任务（如\"加 SQL + 写前端 + 改 changelog\"）→ 拆给多个专家并行", "2. 单领域且需深度专业 → delegate 给该领域专家", "3. 简单 / 单文件 / 解释概念 → 直接答", "4. 永远不把同一任务派给多个专家", "5. delegate 后等子 agent 完成，不要并行问用户")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func escapePromptKeyForInstruction(promptKey string) string {
	return strings.ReplaceAll(promptKey, "'", "\\'")
}

// RecallCatalogProvider 渲染当前 CWD 可用的 recall section 目录。
type RecallCatalogProvider struct{ catalog RuntimePromptCatalog }

// SectionName 返回本 provider 负责的动态 section 名称。
func (RecallCatalogProvider) SectionName() string { return contract.DynamicSectionRecallCatalog }

// Resolve 解析知识回忆目录，列出当前 CWD 下所有 recall section 并渲染为 prompt 文本。
func (p RecallCatalogProvider) Resolve(ctx context.Context, input contract.SectionContext) (*string, error) {
	start := time.Now()
	if p.catalog == nil {
		err := fmt.Errorf("runtime prompt catalog is required for dynamic section %q", contract.DynamicSectionRecallCatalog)
		logDynamicSectionResolveFailed(contract.DynamicSectionRecallCatalog, start, err)
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionRecallCatalog, err)
	}
	cwd := contract.SectionContextCWD(input)
	if cwd == "" {
		err := fmt.Errorf("cwd is required for dynamic section %q", contract.DynamicSectionRecallCatalog)
		logDynamicSectionResolveFailed(contract.DynamicSectionRecallCatalog, start, err)
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionRecallCatalog, err)
	}
	sections, err := p.catalog.ListRecallSections(ctx, cwd)
	if err != nil {
		logDynamicSectionResolveFailed(contract.DynamicSectionRecallCatalog, start, err)
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionRecallCatalog, err)
	}
	text := renderRecallCatalog(sections)
	if text == "" {
		return nil, nil
	}
	logDynamicSectionResolved(contract.DynamicSectionRecallCatalog, start, true, "topic_count", len(sections), "body_len", len(text))
	return &text, nil
}

func logDynamicSectionResolved(section string, start time.Time, rendered bool, fields ...any) {
	args := []any{"section", section, "rendered", rendered, pkglogger.FieldLatencyMS, time.Since(start).Milliseconds()}
	args = append(args, fields...)
	pkglogger.Info("thread: dynamic section resolved", args...)
}

func logDynamicSectionResolveFailed(section string, start time.Time, err error) {
	pkglogger.Warn("thread: dynamic section resolve failed", "section", section, "rendered", false, pkglogger.FieldLatencyMS, time.Since(start).Milliseconds(), pkglogger.FieldError, err)
}

func recallCatalogSnippet(body string) string {
	text := strings.Join(strings.Fields(body), " ")
	if text == "" {
		return ""
	}
	for _, marker := range []string{"。", "！", "？", ".", "!", "?", "\n"} {
		if idx := strings.Index(text, marker); idx >= 0 {
			text = text[:idx+len(marker)]
			break
		}
	}
	const limit = 96
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func recallCatalogMetadataSnippet(section PromptTemplateSection) string {
	for _, candidate := range []string{
		section.TemplateDescription,
		section.TemplateWhenToUse,
		section.TemplateTitle,
		section.RecallTopic,
	} {
		if snippet := recallCatalogSnippet(stripRecallCatalogMetadataPrefix(candidate)); snippet != "" {
			return snippet
		}
	}
	return ""
}

func stripRecallCatalogMetadataPrefix(value string) string {
	text := strings.TrimSpace(value)
	for _, prefix := range []string{
		"Knowledge material:",
		"Knowledge material：",
	} {
		if withoutPrefix, ok := strings.CutPrefix(text, prefix); ok {
			return strings.TrimSpace(withoutPrefix)
		}
	}
	return text
}

func renderRecallCatalog(sections []PromptTemplateSection) string {
	sections = effectiveRecallSections(sections)
	sort.SliceStable(sections, func(i, j int) bool {
		return strings.TrimSpace(sections[i].RecallTopic) < strings.TrimSpace(sections[j].RecallTopic)
	})
	lines := []string{"可回忆知识目录："}
	for _, section := range sections {
		topic := strings.TrimSpace(section.RecallTopic)
		snippet := recallCatalogMetadataSnippet(section)
		if topic == "" || snippet == "" {
			continue
		}
		lines = append(lines, "- topic=\""+strings.ReplaceAll(topic, `"`, `\"`)+"\" — "+snippet)
	}
	if len(lines) == 1 {
		return ""
	}
	lines = append(lines,
		"",
		"判断准则：先根据用户问题选择最相关 topic；需要具体知识包时调用 prompt_recall(topic=\"<topic>\")；没有明确命中时不要调用。",
	)
	return strings.Join(lines, "\n")
}

// effectiveRecallSections 按 topic 去重，保留作用域更精确或 ordinal/id 更小的 section。
func effectiveRecallSections(sections []PromptTemplateSection) []PromptTemplateSection {
	byTopic := map[string]PromptTemplateSection{}
	for _, section := range sections {
		topic := strings.TrimSpace(section.RecallTopic)
		if topic == "" {
			continue
		}
		if current, ok := byTopic[topic]; !ok || preferPromptSection(section, current) {
			byTopic[topic] = section
		}
	}
	out := make([]PromptTemplateSection, 0, len(byTopic))
	for _, section := range byTopic {
		out = append(out, section)
	}
	return out
}

func preferPromptSection(left, right PromptTemplateSection) bool {
	leftRank := templateScopeRank(left.TemplateTags)
	rightRank := templateScopeRank(right.TemplateTags)
	if leftRank != rightRank {
		return leftRank < rightRank
	}
	if left.Ordinal != right.Ordinal {
		return left.Ordinal < right.Ordinal
	}
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	return strings.TrimSpace(left.TemplatePromptKey) < strings.TrimSpace(right.TemplatePromptKey)
}

func templateScopeRank(raw json.RawMessage) int {
	tags := templateTags(raw)
	for _, tag := range tags {
		if strings.HasPrefix(strings.TrimSpace(tag), "scope.cwd:") {
			return 0
		}
	}
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "scope.global" {
			return 1
		}
	}
	return 0
}
