package intent

import (
	"context"
	"strings"
	"unicode"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

const promptIntentDuplicateListLimit = 1000

func promptIntentDuplicateIssues(
	ctx context.Context,
	store promptstore.Store,
	builtin contract.BuiltinPromptRegistry,
	cwd string,
	kind Kind,
	rawInput string,
	card Card,
	targetGlobal bool,
) ([]Issue, error) {
	templates, err := store.List(ctx, promptstore.ListFilter{CWD: cwd, Limit: promptIntentDuplicateListLimit})
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

// promptIntentDuplicateIssuesFromBuiltin 从builtin处理promptintentduplicateissues。
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
		mapped := promptstore.PromptTemplate{
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

func promptIntentBuiltinSections(templateID int64, builtin contract.BuiltinPromptRegistry) []promptstore.PromptTemplateSection {
	sections := builtin.SectionsByTemplateID(templateID)
	out := make([]promptstore.PromptTemplateSection, 0, len(sections))
	for _, section := range sections {
		out = append(out, promptstore.PromptTemplateSection{
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

// promptIntentSectionsByTemplateID 按templateID处理promptintentsections。
func promptIntentSectionsByTemplateID(
	ctx context.Context,
	store promptstore.Store,
	templates []promptstore.PromptTemplate,
) (map[int64][]promptstore.PromptTemplateSection, error) {
	ids := make([]int64, 0, len(templates))
	for _, template := range templates {
		if template.ID != 0 {
			ids = append(ids, template.ID)
		}
	}
	if len(ids) == 0 {
		return map[int64][]promptstore.PromptTemplateSection{}, nil
	}
	sections, err := store.ListSectionsByTemplateIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := map[int64][]promptstore.PromptTemplateSection{}
	for _, section := range sections {
		out[section.TemplateID] = append(out[section.TemplateID], section)
	}
	return out, nil
}

// promptIntentDuplicateIssuesFromTemplates 从templates处理promptintentduplicateissues。
func promptIntentDuplicateIssuesFromTemplates(
	cwd string,
	kind Kind,
	rawInput string,
	card Card,
	templates []promptstore.PromptTemplate,
	sectionsByID map[int64][]promptstore.PromptTemplateSection,
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

func promptIntentBuiltinDuplicateIssue(kind Kind, rawInput string) Issue {
	if kind == KindRecall && promptIntentLooksLikeExternalSystemPrompt(normalizePromptIntentText(rawInput)) {
		return Issue{Code: "builtin_prompt_duplicate", Severity: "review", Message: "系统可能已有相近能力；如需保留原文出处，可作为参考资料保存"}
	}
	return Issue{Code: "builtin_prompt_duplicate", Severity: "block", Message: "系统已内置相同或高度相似的提示词，不需要另存为用户资产"}
}

func promptIntentTemplateDuplicates(
	candidateTitleSlug, candidateText string,
	template promptstore.PromptTemplate,
	sections []promptstore.PromptTemplateSection,
) bool {
	if candidateTitleSlug != "" && candidateTitleSlug != "prompt" && candidateTitleSlug == stableSlug(template.Title) {
		return true
	}
	return promptIntentTextHighlySimilar(candidateText, promptIntentTemplateComparableText(template, sections))
}

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

func promptIntentTemplateComparableText(template promptstore.PromptTemplate, sections []promptstore.PromptTemplateSection) string {
	parts := []string{template.Title, template.Description, template.WhenToUse, template.PromptText}
	for _, section := range sections {
		if section.Enabled {
			parts = append(parts, section.Body, section.RecallTopic)
		}
	}
	return strings.Join(parts, "\n")
}

// promptIntentTextHighlySimilar 处理promptintent文本highlysimilar。
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

// promptIntentTokenOverlap 处理promptintent令牌overlap。
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

func promptIntentTokenSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, token := range strings.Fields(text) {
		if promptIntentRuneLen(token) >= 2 {
			out[token] = true
		}
	}
	return out
}

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

func promptIntentTemplateVisibleForCWD(template promptstore.PromptTemplate, cwd string) bool {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return true
	}
	for _, tag := range promptstore.TemplateTags(template.Tags) {
		if value, ok := strings.CutPrefix(strings.TrimSpace(tag), promptScopeTagPrefix); ok {
			return strings.TrimSpace(value) == requestScope
		}
	}
	return true
}

func promptIntentTemplateHasCurrentProjectScope(template promptstore.PromptTemplate, cwd string) bool {
	want := promptScopeTagPrefix + strings.TrimSpace(cwd)
	if want == promptScopeTagPrefix {
		return false
	}
	for _, tag := range promptstore.TemplateTags(template.Tags) {
		if strings.TrimSpace(tag) == want {
			return true
		}
	}
	return false
}

func promptIntentRecallDuplicateConflicts(targetGlobal bool, template promptstore.PromptTemplate, cwd string) bool {
	if targetGlobal && promptIntentTemplateHasCurrentProjectOnlyScope(template, cwd) {
		return false
	}
	if !targetGlobal && promptIntentTemplateHasGlobalOnlyScope(template, cwd) {
		return false
	}
	return true
}

func promptIntentTemplateHasCurrentProjectOnlyScope(template promptstore.PromptTemplate, cwd string) bool {
	hasCurrentProject, hasGlobal := promptIntentTemplateScopeFlags(template, cwd)
	return hasCurrentProject && !hasGlobal
}

func promptIntentTemplateHasGlobalOnlyScope(template promptstore.PromptTemplate, cwd string) bool {
	hasCurrentProject, hasGlobal := promptIntentTemplateScopeFlags(template, cwd)
	return hasGlobal && !hasCurrentProject
}

func promptIntentTemplateScopeFlags(template promptstore.PromptTemplate, cwd string) (bool, bool) {
	want := promptScopeTagPrefix + strings.TrimSpace(cwd)
	hasCurrentProject := false
	hasGlobal := false
	for _, tag := range promptstore.TemplateTags(template.Tags) {
		switch strings.TrimSpace(tag) {
		case want:
			hasCurrentProject = want != promptScopeTagPrefix
		case "scope.global":
			hasGlobal = true
		}
	}
	return hasCurrentProject, hasGlobal
}

func promptIntentTemplateLooksBuiltin(template promptstore.PromptTemplate) bool {
	return promptIntentAuthorLooksSystem(template.CreatedBy) || promptIntentAuthorLooksSystem(template.UpdatedBy)
}

func promptIntentAuthorLooksSystem(author string) bool {
	normalized := strings.ToLower(strings.TrimSpace(author))
	return strings.HasPrefix(normalized, "system") || strings.Contains(normalized, "seed") || strings.Contains(normalized, "migration")
}

func promptIntentRecallTopicExists(topic string, sections []promptstore.PromptTemplateSection) bool {
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

func promptIntentRuneLen(value string) int {
	return len([]rune(value))
}

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
