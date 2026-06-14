package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// mergeTemplateSections folds DB-sourced prompt_template sections into the
// resolver's output. Blocks are stable-sorted by (Region, Ordinal) so callers
// don't need to pre-sort; static blocks feed CachedPrefix, dynamic blocks feed
// UncachedTail via renderResolvedSectionsByRegion in assembler.go.
//
// Semantics:
//   - Empty bodies are dropped.
//   - Blocks whose EnableWhen rejects the current BuildCtx are dropped
//     (section-level feature gate).
//   - A block whose Key matches an already-resolved built-in section REPLACES
//     the built-in content (same Name, same Region). This is the intended
//     override path when a template ships section_keys like "identity",
//     "tone_and_style", etc.: operators can edit the built-in persona instead
//     of having both copies concatenated.
//   - A block with a novel Key is appended as "tpl:<key>" so it cannot collide
//     with a future built-in addition.
//
// mergeTemplateSections 合并templatesections。
func mergeTemplateSections(
	resolved []contract.ResolvedPromptSection,
	blocks []contract.BaseInstructionBlock,
	buildCtx contract.BuildCtx,
	userPrompt string,
) []contract.ResolvedPromptSection {
	if len(blocks) == 0 {
		return resolved
	}
	sorted := make([]contract.BaseInstructionBlock, len(blocks))
	copy(sorted, blocks)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Region != sorted[j].Region {
			return sorted[i].Region < sorted[j].Region
		}
		return sorted[i].Ordinal < sorted[j].Ordinal
	})
	builtinIndex := indexResolvedByName(resolved)
	for _, b := range sorted {
		body := strings.TrimSpace(b.Body)
		if body == "" {
			continue
		}
		if !EvaluateEnableWhen(b.EnableWhen, buildCtx, userPrompt) {
			continue
		}
		key := strings.TrimSpace(b.Key)
		if idx, ok := builtinIndex[key]; ok {
			resolved[idx].Content = body
			resolved[idx].Region = b.Region
			continue
		}
		resolved = append(resolved, contract.ResolvedPromptSection{
			Name:    "tpl:" + key,
			Region:  b.Region,
			Content: body,
		})
	}
	return resolved
}

func indexResolvedByName(resolved []contract.ResolvedPromptSection) map[string]int {
	out := make(map[string]int, len(resolved))
	for i, r := range resolved {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		out[name] = i
	}
	return out
}

func requirePromptCWD(cwd string) (string, error) {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return "", errors.New("dashboard: cwd is required")
	}
	return requestScope, nil
}

// validatePromptScope 校验prompt作用域。
func validatePromptScope(current *promptstore.PromptTemplate, cwd string) error {
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	if promptHasScopeCWD(current.Tags, requestScope) {
		return nil
	}
	if promptHasAnyScopeCWD(current.Tags) || promptHasGlobalScope(current.Tags) {
		return fmt.Errorf("dashboard: prompt %q is outside cwd scope", current.PromptKey)
	}
	if promptScopeFromTags(current.Tags) == "" {
		return nil
	}
	return fmt.Errorf("dashboard: prompt %q is outside cwd scope", current.PromptKey)
}

// validatePromptWriteScope 校验promptwrite作用域。
func validatePromptWriteScope(current *promptstore.PromptTemplate, cwd, scope string, scopeSet bool) error {
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	if promptHasScopeCWD(current.Tags, requestScope) {
		return nil
	}
	if promptHasGlobalScope(current.Tags) {
		if scopeSet {
			switch normalizePromptScope(scope) {
			case "global", "project":
				return nil
			}
		}
		return fmt.Errorf("dashboard: prompt %q is outside cwd scope", current.PromptKey)
	}
	if promptHasAnyScopeCWD(current.Tags) {
		return fmt.Errorf("dashboard: prompt %q is outside cwd scope", current.PromptKey)
	}
	if promptScopeFromTags(current.Tags) == "" {
		return nil
	}
	return fmt.Errorf("dashboard: prompt %q is outside cwd scope", current.PromptKey)
}

// validatePromptMutationScope 校验promptmutation作用域。
func validatePromptMutationScope(current *promptstore.PromptTemplate, cwd, scope string, scopeSet bool) error {
	requestScope, err := requirePromptCWD(cwd)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	if promptHasScopeCWD(current.Tags, requestScope) {
		return nil
	}
	if promptHasGlobalScope(current.Tags) && scopeSet && normalizePromptScope(scope) == "global" {
		return nil
	}
	if promptHasAnyScopeCWD(current.Tags) || promptHasGlobalScope(current.Tags) {
		return fmt.Errorf("dashboard: prompt %q is outside cwd scope", current.PromptKey)
	}
	if promptScopeFromTags(current.Tags) == "" {
		return nil
	}
	return fmt.Errorf("dashboard: prompt %q is outside cwd scope", current.PromptKey)
}

func promptVisibleForCWD(template promptstore.PromptTemplate, cwd string) bool {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return false
	}
	if promptHasGlobalScope(template.Tags) {
		return true
	}
	storedScope := promptScopeFromTags(template.Tags)
	return storedScope == "" || storedScope == requestScope
}

// promptScopeForWrite 为write处理prompt作用域。
func promptScopeForWrite(current *promptstore.PromptTemplate, cwd, scope string, scopeSet bool) string {
	if scopeSet {
		if normalized := normalizePromptScope(scope); normalized != "" {
			return normalized
		}
	}
	if current != nil && promptHasScopeCWD(current.Tags, cwd) {
		return "project"
	}
	if current != nil && promptHasGlobalScope(current.Tags) {
		return "global"
	}
	if strings.TrimSpace(cwd) != "" {
		return "project"
	}
	return ""
}

func promptScopeFromTags(raw json.RawMessage) string {
	for _, tag := range promptTags(raw) {
		if value, ok := strings.CutPrefix(tag, promptScopeTagPrefix); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizePromptScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "project", "cwd", "current_project":
		return "project"
	case "global", "all", "all_projects":
		return "global"
	default:
		return ""
	}
}

func promptScopeForTemplate(template promptstore.PromptTemplate) string {
	if promptHasGlobalScope(template.Tags) {
		return "global"
	}
	return "project"
}

func withPromptScopeTag(raw json.RawMessage, cwd string) json.RawMessage {
	return withPromptScopeKindTag(raw, cwd, "project")
}

// withPromptScopeKindTag 设置prompt作用域kindtag。
func withPromptScopeKindTag(raw json.RawMessage, cwd, scope string) json.RawMessage {
	tags := promptTags(raw)
	next := make([]string, 0, len(tags)+1)
	for _, tag := range tags {
		if tag = strings.TrimSpace(tag); tag != "" && tag != "scope.global" && !strings.HasPrefix(tag, promptScopeTagPrefix) {
			next = append(next, tag)
		}
	}
	if normalizePromptScope(scope) == "global" {
		next = append(next, "scope.global")
	} else if cwd = strings.TrimSpace(cwd); cwd != "" {
		next = append(next, promptScopeTagPrefix+cwd)
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return json.RawMessage("[]")
	}
	return json.RawMessage(encoded)
}

// rejectDuplicateRecallTopicInCWD 在工作目录处理rejectduplicaterecalltopic。
func rejectDuplicateRecallTopicInCWD(
	ctx context.Context,
	store promptstore.Store,
	cwd, topic string,
	targetScope string,
	templateID int64,
	sectionKey string,
) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil
	}
	if err := store.LockRecallTopicInCWD(ctx, cwd, topic); err != nil {
		return err
	}
	templates, err := store.List(ctx, promptstore.ListFilter{CWD: cwd, Limit: promptRPCLimit})
	if err != nil {
		return err
	}
	visible := promptRecallDuplicateVisibleTemplates(templates, cwd)
	sectionsByID, err := promptAssetSectionsByTemplateID(ctx, store, visible)
	if err != nil {
		return err
	}
	sectionKey = strings.TrimSpace(sectionKey)
	if promptRecallDuplicateExists(visible, sectionsByID, cwd, topic, targetScope, templateID, sectionKey) {
		return fmt.Errorf("dashboard: duplicate recall_topic %q in cwd", topic)
	}
	return nil
}

// promptRecallDuplicateExists 处理promptrecallduplicateexists。
func promptRecallDuplicateExists(
	templates []promptstore.PromptTemplate,
	sectionsByID map[int64][]promptstore.PromptTemplateSection,
	cwd, topic, targetScope string,
	templateID int64,
	sectionKey string,
) bool {
	for _, template := range templates {
		if !promptRecallTemplateConflictsWithTarget(targetScope, template, cwd) {
			continue
		}
		for _, section := range sectionsByID[template.ID] {
			if !section.Enabled || strings.TrimSpace(strings.ToLower(section.TriggerType)) != "recall" || strings.TrimSpace(section.RecallTopic) != topic {
				continue
			}
			if section.TemplateID != templateID || strings.TrimSpace(section.SectionKey) != sectionKey {
				return true
			}
		}
	}
	return false
}

// promptRecallDuplicateTargetScope 处理promptrecallduplicatetarget作用域。
func promptRecallDuplicateTargetScope(current *promptstore.PromptTemplate, cwd, scope string, scopeSet bool) string {
	if current != nil {
		hasProject := promptHasScopeCWD(current.Tags, cwd)
		hasGlobal := promptHasGlobalScope(current.Tags)
		if hasProject || hasGlobal {
			switch {
			case hasGlobal && !hasProject:
				return "global"
			case hasProject && !hasGlobal:
				return "project"
			default:
				return ""
			}
		}
	}
	if scopeSet {
		return normalizePromptScope(scope)
	}
	if strings.TrimSpace(cwd) != "" {
		return "project"
	}
	return ""
}

func promptRecallDuplicateVisibleTemplates(templates []promptstore.PromptTemplate, cwd string) []promptstore.PromptTemplate {
	out := make([]promptstore.PromptTemplate, 0, len(templates))
	for _, template := range templates {
		if template.Enabled && template.ID != 0 && promptVisibleForCWD(template, cwd) {
			out = append(out, template)
		}
	}
	return out
}

func promptRecallTemplateConflictsWithTarget(targetScope string, template promptstore.PromptTemplate, cwd string) bool {
	switch targetScope {
	case "global":
		return !promptTemplateHasCurrentProjectOnlyScope(template, cwd)
	case "project":
		return !promptTemplateHasGlobalOnlyScope(template, cwd)
	default:
		return true
	}
}

func promptTemplateHasCurrentProjectOnlyScope(template promptstore.PromptTemplate, cwd string) bool {
	return promptHasScopeCWD(template.Tags, cwd) && !promptHasGlobalScope(template.Tags)
}

func promptTemplateHasGlobalOnlyScope(template promptstore.PromptTemplate, cwd string) bool {
	return promptHasGlobalScope(template.Tags) && !promptHasScopeCWD(template.Tags, cwd)
}

type promptAssetListParams struct {
	Cwd string `json:"cwd,omitempty"`
}

type promptAssetRPCItem struct {
	promptRPCItem
	State       string `json:"state,omitempty"`
	DraftKey    string `json:"draft_key,omitempty"`
	DraftStatus string `json:"draft_status,omitempty"`
	SourceType  string `json:"source_type,omitempty"`
	Card        any    `json:"card,omitempty"`
	Issues      any    `json:"issues,omitempty"`
}

// handlePromptAssetList 处理promptassetlist。
func handlePromptAssetList(ctx context.Context, store promptstore.Store, p promptAssetListParams) (any, error) {
	if store == nil {
		return nil, errPromptStoreRequired
	}
	cwd, err := requirePromptCWD(p.Cwd)
	if err != nil {
		return nil, err
	}
	templates, err := store.List(ctx, promptstore.ListFilter{CWD: cwd, Limit: promptRPCLimit})
	if err != nil {
		return nil, err
	}
	assets := promptAssetTemplatesForCWD(templates, cwd)
	sections, err := promptAssetSectionsByTemplateID(ctx, store, assets)
	if err != nil {
		return nil, err
	}
	items := promptAssetItemsFromTemplates(assets, sections)
	drafts, err := store.ListIntentDrafts(ctx, promptstore.PromptIntentDraftListFilter{
		CWD:    cwd,
		Status: "ready_to_save",
		Limit:  promptRPCLimit,
	})
	if err != nil {
		return nil, err
	}
	items = append(items, promptAssetItemsFromDrafts(drafts)...)
	return map[string]any{"prompts": items}, nil
}

func promptAssetTemplatesForCWD(templates []promptstore.PromptTemplate, cwd string) []promptstore.PromptTemplate {
	assets := make([]promptstore.PromptTemplate, 0, len(templates))
	for _, template := range templates {
		if promptAssetVisibleForCWD(template, cwd) && promptTemplateIsUserAsset(template) {
			assets = append(assets, template)
		}
	}
	return effectivePromptAssetTemplates(assets, cwd)
}

func promptAssetVisibleForCWD(template promptstore.PromptTemplate, cwd string) bool {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return false
	}
	return promptHasGlobalScope(template.Tags) || promptHasScopeCWD(template.Tags, requestScope)
}

// effectivePromptAssetTemplates 处理effectivepromptassettemplates。
func effectivePromptAssetTemplates(templates []promptstore.PromptTemplate, cwd string) []promptstore.PromptTemplate {
	type pickedAsset struct {
		template promptstore.PromptTemplate
		rank     int
		index    int
	}
	byKey := map[string]pickedAsset{}
	for index, template := range templates {
		key := promptAssetLogicalKey(template)
		if key == "" {
			continue
		}
		next := pickedAsset{template: template, rank: promptAssetScopeRank(template, cwd), index: index}
		current, ok := byKey[key]
		if !ok || preferPromptAsset(next.rank, current.rank, next.template.Priority, current.template.Priority, next.index, current.index) {
			byKey[key] = next
		}
	}
	out := make([]pickedAsset, 0, len(byKey))
	for _, asset := range byKey {
		out = append(out, asset)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].index < out[j].index })
	templates = make([]promptstore.PromptTemplate, 0, len(out))
	for _, asset := range out {
		templates = append(templates, asset.template)
	}
	return templates
}

func promptAssetLogicalKey(template promptstore.PromptTemplate) string {
	agent := strings.ToLower(strings.TrimSpace(template.AgentKey))
	intentTag := ""
	for _, tag := range promptTags(template.Tags) {
		if tag = strings.TrimSpace(tag); strings.HasPrefix(tag, "intent:") {
			intentTag = strings.ToLower(tag)
			break
		}
	}
	title := strings.ToLower(strings.TrimSpace(template.Title))
	if title == "" {
		title = strings.ToLower(strings.TrimSpace(template.PromptKey))
	}
	return strings.Join([]string{agent, intentTag, title}, "\x00")
}

func promptAssetScopeRank(template promptstore.PromptTemplate, cwd string) int {
	if promptHasGlobalScope(template.Tags) && !promptHasScopeCWD(template.Tags, cwd) {
		return 1
	}
	return 0
}

func preferPromptAsset(leftRank, rightRank, leftPriority, rightPriority, leftIndex, rightIndex int) bool {
	if leftRank != rightRank {
		return leftRank < rightRank
	}
	if leftPriority != rightPriority {
		return leftPriority > rightPriority
	}
	return leftIndex < rightIndex
}

func promptTemplateIsUserAsset(template promptstore.PromptTemplate) bool {
	if promptTemplateHasTag(template.Tags, "builtin:system") {
		return false
	}
	if promptTemplateAuthoredByUser(template) {
		return true
	}
	if template.ManuallyEdited {
		return true
	}
	if !promptTemplateHasIntentAssetMarker(template) {
		return false
	}
	return !promptTemplateAuthoredBySystem(template)
}

func promptTemplateHasTag(raw json.RawMessage, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, tag := range promptTags(raw) {
		if strings.TrimSpace(tag) == want {
			return true
		}
	}
	return false
}

func promptTemplateAuthoredByUser(template promptstore.PromptTemplate) bool {
	return promptAuthorIsRPC(template.CreatedBy) || promptAuthorIsRPC(template.UpdatedBy)
}

func promptAuthorIsRPC(author string) bool {
	return strings.TrimSpace(author) == promptUpdatedBy
}

func promptTemplateAuthoredBySystem(template promptstore.PromptTemplate) bool {
	return promptAuthorLooksSystem(template.CreatedBy) || promptAuthorLooksSystem(template.UpdatedBy)
}

func promptAuthorLooksSystem(author string) bool {
	normalized := strings.ToLower(strings.TrimSpace(author))
	return strings.HasPrefix(normalized, "system") ||
		strings.HasPrefix(normalized, "builtin.") ||
		strings.Contains(normalized, "seed") ||
		strings.Contains(normalized, "migration")
}

func promptTemplateHasIntentAssetMarker(template promptstore.PromptTemplate) bool {
	if strings.TrimSpace(template.AgentKey) == "default_rule" {
		return true
	}
	for _, tag := range promptTags(template.Tags) {
		switch strings.TrimSpace(tag) {
		case "intent:expert", "intent:recall", "intent:default_rule":
			return true
		}
	}
	return false
}

func promptAssetSectionsByTemplateID(
	ctx context.Context,
	store promptstore.Store,
	templates []promptstore.PromptTemplate,
) (map[int64][]promptstore.PromptTemplateSection, error) {
	sectionsByTemplateID := map[int64][]promptstore.PromptTemplateSection{}
	ids := promptTemplateIDs(templates)
	if len(ids) == 0 {
		return sectionsByTemplateID, nil
	}
	sections, err := store.ListSectionsByTemplateIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, section := range sections {
		sectionsByTemplateID[section.TemplateID] = append(sectionsByTemplateID[section.TemplateID], section)
	}
	return sectionsByTemplateID, nil
}

func promptAssetItemsFromTemplates(
	templates []promptstore.PromptTemplate,
	sectionsByTemplateID map[int64][]promptstore.PromptTemplateSection,
) []promptAssetRPCItem {
	items := make([]promptAssetRPCItem, 0, len(templates))
	for _, template := range templates {
		sections := sectionsByTemplateID[template.ID]
		template = promptTemplateWithInferredSectionIntent(template, sections)
		item := promptItemFromTemplate(template)
		if content := promptEditableSectionsContent(template, sections); content != "" {
			item.Content = content
		}
		items = append(items, promptAssetRPCItem{promptRPCItem: item})
	}
	return items
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func promptHasScopeCWD(raw json.RawMessage, cwd string) bool {
	want := promptScopeTagPrefix + strings.TrimSpace(cwd)
	for _, tag := range promptTags(raw) {
		if strings.TrimSpace(tag) == want {
			return true
		}
	}
	return false
}

func promptHasAnyScopeCWD(raw json.RawMessage) bool {
	for _, tag := range promptTags(raw) {
		if strings.HasPrefix(strings.TrimSpace(tag), promptScopeTagPrefix) {
			return true
		}
	}
	return false
}

func promptHasGlobalScope(raw json.RawMessage) bool {
	for _, tag := range promptTags(raw) {
		if strings.TrimSpace(tag) == "scope.global" {
			return true
		}
	}
	return false
}

func promptTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return []string{}
	}
	return tags
}
