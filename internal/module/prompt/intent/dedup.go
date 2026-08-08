package intent

import (
	"context"
	"strings"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// promptIntentDuplicateListLimit 限制重复检测一次最多读取的模板数量，避免创建期扫描无界放大。
const promptIntentDuplicateListLimit = 1000

// promptIntentDuplicateIssues 检查候选草稿与已有 prompt 模板（含 builtin）是否重复，返回重复问题列表。
func promptIntentDuplicateIssues(
	ctx context.Context,
	store Store,
	builtin contract.BuiltinPromptRegistry,
	cwd string,
	kind Kind,
	rawInput string,
	card Card,
	targetGlobal bool,
) ([]Issue, error) {
	templates, err := store.List(ctx, ListFilter{CWD: cwd, Limit: promptIntentDuplicateListLimit})
	if err != nil {
		return nil, err
	}
	sectionsByID, err := promptIntentSectionsByTemplateID(ctx, store, templates)
	if err != nil {
		return nil, err
	}
	issues := promptIntentDuplicateIssuesFromTemplates(cwd, kind, rawInput, card, templates, sectionsByID, targetGlobal)
	issues = append(issues, promptIntentDuplicateIssuesFromBuiltin(kind, rawInput, card, builtin)...)
	return promptIntentUniqueIssues(issues), nil
}

// promptIntentDuplicateIssuesFromBuiltin 检查候选草稿是否与 builtin 模板高度相似，相似则返回 block/review 问题。
func promptIntentDuplicateIssuesFromBuiltin(
	kind Kind,
	rawInput string,
	card Card,
	builtin contract.BuiltinPromptRegistry,
) []Issue {
	if builtin == nil {
		return nil
	}
	candidateText := promptIntentCandidateText(rawInput, card)
	candidateTitleSlug := stableSlug(card.Title)
	var issues []Issue
	for _, template := range builtin.ListTemplates() {
		mapped := PromptTemplate{
			ID:          template.ID,
			PromptKey:   template.PromptKey,
			Title:       template.Title,
			AgentKey:    template.AgentKey,
			PromptText:  template.PromptText,
			Description: template.Description,
			WhenToUse:   template.WhenToUse,
			Enabled:     template.Enabled,
			CreatedBy:   "builtin.registry",
			UpdatedBy:   "builtin.registry",
		}
		if !mapped.Enabled {
			continue
		}
		sections := promptIntentBuiltinSections(template.ID, builtin)
		if promptIntentTemplateDuplicates(candidateTitleSlug, candidateText, mapped, sections) {
			issues = append(issues, promptIntentBuiltinDuplicateIssue(kind, rawInput))
		}
	}
	return issues
}

// promptIntentBuiltinSections 将 builtin section 转换为 intent section 类型，供重复检测使用。
func promptIntentBuiltinSections(templateID int64, builtin contract.BuiltinPromptRegistry) []PromptTemplateSection {
	sections := builtin.SectionsByTemplateID(templateID)
	out := make([]PromptTemplateSection, 0, len(sections))
	for _, section := range sections {
		out = append(out, PromptTemplateSection{
			ID:          section.ID,
			TemplateID:  section.TemplateID,
			SectionKey:  section.SectionKey,
			Body:        section.Body,
			Enabled:     section.Enabled,
			TriggerType: section.TriggerType,
			RecallTopic: section.RecallTopic,
		})
	}
	return out
}

// promptIntentSectionsByTemplateID 批量查询模板 section，返回按 template_id 索引的 map。
func promptIntentSectionsByTemplateID(
	ctx context.Context,
	store Store,
	templates []PromptTemplate,
) (map[int64][]PromptTemplateSection, error) {
	ids := make([]int64, 0, len(templates))
	for _, template := range templates {
		if template.ID != 0 {
			ids = append(ids, template.ID)
		}
	}
	if len(ids) == 0 {
		return map[int64][]PromptTemplateSection{}, nil
	}
	sections, err := store.ListSectionsByTemplateIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := map[int64][]PromptTemplateSection{}
	for _, section := range sections {
		out[section.TemplateID] = append(out[section.TemplateID], section)
	}
	return out, nil
}

// promptIntentDuplicateIssuesFromTemplates 检查候选草稿与 CWD 内已有模板是否重复，
// 同名 recall_topic 直接 block，文本高度相似则 review。
func promptIntentDuplicateIssuesFromTemplates(
	cwd string,
	kind Kind,
	rawInput string,
	card Card,
	templates []PromptTemplate,
	sectionsByID map[int64][]PromptTemplateSection,
	targetGlobal bool,
) []Issue {
	candidateText := promptIntentCandidateText(rawInput, card)
	candidateTitleSlug := stableSlug(card.Title)
	recallTopic := strings.TrimSpace(card.RecallTopic)
	var issues []Issue
	for _, template := range templates {
		if !template.Enabled || !promptIntentTemplateVisibleForCWD(template, cwd) {
			continue
		}
		sections := sectionsByID[template.ID]
		if kind == KindRecall && promptIntentRecallTopicExists(recallTopic, sections) {
			if promptIntentRecallDuplicateConflicts(targetGlobal, template, cwd) {
				issues = append(issues, Issue{Code: "duplicate_recall_topic", Severity: "block", Message: "当前项目已存在同名知识主题，请更新已有资料或换一个更具体的主题"})
				continue
			}
		}
		if !promptIntentTemplateDuplicates(candidateTitleSlug, candidateText, template, sections) {
			continue
		}
		if promptIntentTemplateLooksBuiltin(template) {
			issues = append(issues, promptIntentBuiltinDuplicateIssue(kind, rawInput))
			continue
		}
		issues = append(issues, Issue{Code: "project_prompt_duplicate", Severity: "review", Message: "当前项目已有相似提示词，建议更新已有条目或改成项目补充"})
	}
	return promptIntentUniqueIssues(issues)
}

// promptIntentBuiltinDuplicateIssue 根据 kind 和原文生成 builtin 重复问题：
// recall+外部 prompt 组合返回 review，其余返回 block。
func promptIntentBuiltinDuplicateIssue(kind Kind, rawInput string) Issue {
	if kind == KindRecall && promptIntentLooksLikeExternalSystemPrompt(normalizePromptIntentText(rawInput)) {
		return Issue{Code: "builtin_prompt_duplicate", Severity: "review", Message: "系统可能已有相近能力；如需保留原文出处，可作为参考资料保存"}
	}
	return Issue{Code: "builtin_prompt_duplicate", Severity: "block", Message: "系统已内置相同或高度相似的提示词，不需要另存为用户资产"}
}

// promptIntentTemplateDuplicates 判断候选草稿是否与单个模板重复。
// 标题 slug 完全相同会直接命中；否则比较正文和 section 聚合文本的相似度。
func promptIntentTemplateDuplicates(
	candidateTitleSlug, candidateText string,
	template PromptTemplate,
	sections []PromptTemplateSection,
) bool {
	if candidateTitleSlug != "" && candidateTitleSlug != "prompt" && candidateTitleSlug == stableSlug(template.Title) {
		return true
	}
	return promptIntentTextHighlySimilar(candidateText, promptIntentTemplateComparableText(template, sections))
}

// promptIntentCandidateText 拼接候选草稿的全部可比较文本，用于相似度检测。
func promptIntentCandidateText(rawInput string, card Card) string {
	return strings.Join([]string{
		rawInput,
		card.Title,
		card.Summary,
		card.WhenToUse,
		card.WhenNotToUse,
		strings.Join(card.Workflow, "\n"),
		strings.Join(card.Constraints, "\n"),
		card.Output,
		card.RecallTopic,
		card.RecallBody,
		card.DefaultRuleBody,
	}, "\n")
}

// promptIntentTemplateComparableText 拼接模板及其启用 section 的可比较文本。
// recall_topic 也参与比较，避免同名资料主题重复保存。
func promptIntentTemplateComparableText(template PromptTemplate, sections []PromptTemplateSection) string {
	parts := []string{template.Title, template.Description, template.WhenToUse, template.PromptText}
	for _, section := range sections {
		if section.Enabled {
			parts = append(parts, section.Body, section.RecallTopic)
		}
	}
	return strings.Join(parts, "\n")
}

// promptIntentTextHighlySimilar 判断两段文本是否高度相似：
// 较短串 ≥48 rune 且被较长串包含，或 token 重叠率 ≥85%。
func promptIntentTextHighlySimilar(left, right string) bool {
	left = promptIntentComparableText(left)
	right = promptIntentComparableText(right)
	if promptIntentRuneLen(left) < 32 || promptIntentRuneLen(right) < 32 {
		return false
	}
	shorter, longer := left, right
	if promptIntentRuneLen(shorter) > promptIntentRuneLen(longer) {
		shorter, longer = longer, shorter
	}
	if promptIntentRuneLen(shorter) >= 48 && strings.Contains(longer, shorter) {
		return true
	}
	return promptIntentTokenOverlap(left, right) >= 0.85
}

// promptIntentTokenOverlap 计算两段文本的 token 集合重叠率（Jaccard 最小集），
// token 数 <6 时返回 0 避免短文本误判。
func promptIntentTokenOverlap(left, right string) float64 {
	leftTokens := promptIntentTokenSet(left)
	rightTokens := promptIntentTokenSet(right)
	if len(leftTokens) < 6 || len(rightTokens) < 6 {
		return 0
	}
	shared := 0
	for token := range leftTokens {
		if rightTokens[token] {
			shared++
		}
	}
	smaller := len(leftTokens)
	if len(rightTokens) < smaller {
		smaller = len(rightTokens)
	}
	return float64(shared) / float64(smaller)
}

// promptIntentTokenSet 将可比较文本拆成 token 集合。
// 少于两个 rune 的 token 不参与相似度，降低标点或短词造成的噪声。
func promptIntentTokenSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, token := range strings.Fields(text) {
		if promptIntentRuneLen(token) >= 2 {
			out[token] = true
		}
	}
	return out
}

// promptIntentComparableText 生成相似度比较使用的归一化文本。
// 仅保留字母数字并折叠空白，保证中英文标点差异不会影响重复判断。
func promptIntentComparableText(value string) string {
	var b strings.Builder
	lastSpace := true
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// promptIntentTemplateVisibleForCWD 判断模板的 scope tag 是否与当前 cwd 匹配（无 scope tag 时全局可见）。
func promptIntentTemplateVisibleForCWD(template PromptTemplate, cwd string) bool {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return true
	}
	for _, tag := range promptTags(template.Tags) {
		if value, ok := strings.CutPrefix(strings.TrimSpace(tag), promptScopeTagPrefix); ok {
			return strings.TrimSpace(value) == requestScope
		}
	}
	return true
}

// promptIntentRecallDuplicateConflicts 判断 recall topic 重复是否会与目标 scope 冲突。
// global 与项目级独占模板互不阻断，避免不同可见范围的资料误报重复。
func promptIntentRecallDuplicateConflicts(targetGlobal bool, template PromptTemplate, cwd string) bool {
	if targetGlobal && promptIntentTemplateHasCurrentProjectOnlyScope(template, cwd) {
		return false
	}
	if !targetGlobal && promptIntentTemplateHasGlobalOnlyScope(template, cwd) {
		return false
	}
	return true
}

// promptIntentTemplateHasCurrentProjectOnlyScope 判断模板是否只属于当前项目 scope。
func promptIntentTemplateHasCurrentProjectOnlyScope(template PromptTemplate, cwd string) bool {
	hasCurrentProject, hasGlobal := promptIntentTemplateScopeFlags(template, cwd)
	return hasCurrentProject && !hasGlobal
}

// promptIntentTemplateHasGlobalOnlyScope 判断模板是否只属于 global scope。
func promptIntentTemplateHasGlobalOnlyScope(template PromptTemplate, cwd string) bool {
	hasCurrentProject, hasGlobal := promptIntentTemplateScopeFlags(template, cwd)
	return hasGlobal && !hasCurrentProject
}

// promptIntentTemplateScopeFlags 返回模板是否同时具备当前项目和 global scope。
func promptIntentTemplateScopeFlags(template PromptTemplate, cwd string) (bool, bool) {
	want := promptScopeTagPrefix + strings.TrimSpace(cwd)
	hasCurrentProject := false
	hasGlobal := false
	for _, tag := range promptTags(template.Tags) {
		switch strings.TrimSpace(tag) {
		case want:
			hasCurrentProject = want != promptScopeTagPrefix
		case "scope.global":
			hasGlobal = true
		}
	}
	return hasCurrentProject, hasGlobal
}

// promptIntentTemplateLooksBuiltin 判断模板是否来自系统或批量导入路径，用于内置重复识别。
func promptIntentTemplateLooksBuiltin(template PromptTemplate) bool {
	return promptIntentAuthorLooksSystem(template.CreatedBy) || promptIntentAuthorLooksSystem(template.UpdatedBy)
}

// promptIntentAuthorLooksSystem 根据 author 文本识别系统写入来源。
func promptIntentAuthorLooksSystem(author string) bool {
	normalized := strings.ToLower(strings.TrimSpace(author))
	return strings.HasPrefix(normalized, "system") || strings.Contains(normalized, "seed") || strings.Contains(normalized, "migration")
}

// promptIntentRecallTopicExists 判断 section 列表中是否已有启用的同名 recall topic。
func promptIntentRecallTopicExists(topic string, sections []PromptTemplateSection) bool {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return false
	}
	for _, section := range sections {
		if section.Enabled && strings.TrimSpace(section.RecallTopic) == topic {
			return true
		}
	}
	return false
}

// promptIntentRuneLen 返回字符串的 rune 数量。
func promptIntentRuneLen(value string) int {
	return len([]rune(value))
}

// promptIntentUniqueIssues 按 code+severity 去重，保留首次出现的问题。
func promptIntentUniqueIssues(issues []Issue) []Issue {
	seen := map[string]bool{}
	out := make([]Issue, 0, len(issues))
	for _, issue := range issues {
		key := strings.TrimSpace(issue.Code) + "\x00" + strings.TrimSpace(issue.Severity)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, issue)
	}
	return out
}
