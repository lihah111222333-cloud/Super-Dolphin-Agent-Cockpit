package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// mergeTemplateSections 将 DB 中的 prompt_template sections 合并进 resolver 输出。
// Blocks 会按 Region/Ordinal 稳定排序：static 进入 CachedPrefix，dynamic 进入 UncachedTail。
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

// indexResolvedByName 建立已解析 section 名称到位置的索引，用于模板 section 覆盖内置内容。
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

// requirePromptCWD 校验 RPC 请求必须携带 cwd，所有项目级 prompt 操作都以该值做 scope 判定。
func requirePromptCWD(cwd string) (string, error) {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return "", errors.New("dashboard: cwd is required")
	}
	return requestScope, nil
}

// validatePromptWriteScope 校验 prompt 更新 scope；显式 scope 可将 global/project 写入意图带入判断。
func validatePromptWriteScope(current *Template, cwd, scope string, scopeSet bool) error {
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

// validatePromptMutationScope 校验删除或 section mutation 的 scope，防止跨项目误删。
func validatePromptMutationScope(current *Template, cwd, scope string, scopeSet bool) error {
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

// promptVisibleForCWD 判断模板对当前 cwd 是否可见；未带 scope tag 的既有模板保持可见以兼容旧数据。
func promptVisibleForCWD(template Template, cwd string) bool {
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

// promptScopeForWrite 解析写入目标 scope；更新时优先继承现有模板 scope。
func promptScopeForWrite(current *Template, cwd, scope string, scopeSet bool) string {
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

// promptScopeFromTags 从内部 scope.cwd tag 中取出项目路径。
func promptScopeFromTags(raw json.RawMessage) string {
	for _, tag := range promptTags(raw) {
		if value, ok := strings.CutPrefix(tag, promptScopeTagPrefix); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// normalizePromptScope 把客户端传入的 scope 别名收敛到 project/global。
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

// promptScopeForTemplate 返回前端展示用 scope，非 global 模板都视为 project。
func promptScopeForTemplate(template Template) string {
	if promptHasGlobalScope(template.Tags) {
		return "global"
	}
	return "project"
}

// withPromptScopeTag 为项目级写入添加当前 cwd scope tag。
func withPromptScopeTag(raw json.RawMessage, cwd string) json.RawMessage {
	return withPromptScopeKindTag(raw, cwd, "project")
}

// withPromptScopeKindTag 重写内部 scope tags，只保留用户标签和目标 scope。
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

// rejectDuplicateRecallTopicInCWD 在事务中锁定 topic 并拒绝 cwd 内重复 recall topic。
func rejectDuplicateRecallTopicInCWD(
	ctx context.Context,
	store Store,
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
	templates, err := store.List(ctx, ListFilter{CWD: cwd, Limit: promptRPCLimit})
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

// promptRecallDuplicateExists 扫描可见模板，判断是否已有与目标 section 冲突的 recall topic。
func promptRecallDuplicateExists(
	templates []Template,
	sectionsByID map[int64][]TemplateSection,
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

// promptRecallDuplicateTargetScope 计算去重检查的目标 scope，避免 global/project recall 相互误判。
func promptRecallDuplicateTargetScope(current *Template, cwd, scope string, scopeSet bool) string {
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

// promptRecallDuplicateVisibleTemplates 只保留启用且当前 cwd 可见的模板参与去重。
func promptRecallDuplicateVisibleTemplates(templates []Template, cwd string) []Template {
	out := make([]Template, 0, len(templates))
	for _, template := range templates {
		if template.Enabled && template.ID != 0 && promptVisibleForCWD(template, cwd) {
			out = append(out, template)
		}
	}
	return out
}

// promptRecallTemplateConflictsWithTarget 判断模板 scope 是否会与目标 recall 写入发生冲突。
func promptRecallTemplateConflictsWithTarget(targetScope string, template Template, cwd string) bool {
	switch targetScope {
	case "global":
		return !promptTemplateHasCurrentProjectOnlyScope(template, cwd)
	case "project":
		return !promptTemplateHasGlobalOnlyScope(template, cwd)
	default:
		return true
	}
}

// promptTemplateHasCurrentProjectOnlyScope 判断模板是否只属于当前项目。
func promptTemplateHasCurrentProjectOnlyScope(template Template, cwd string) bool {
	return promptHasScopeCWD(template.Tags, cwd) && !promptHasGlobalScope(template.Tags)
}

// promptTemplateHasGlobalOnlyScope 判断模板是否只属于 global scope。
func promptTemplateHasGlobalOnlyScope(template Template, cwd string) bool {
	return promptHasGlobalScope(template.Tags) && !promptHasScopeCWD(template.Tags, cwd)
}

// promptAssetListParams 是 prompt-assets/list 的 RPC 请求体，只按 cwd 解析项目可见资产。
type promptAssetListParams struct {
	Cwd string `json:"cwd,omitempty"`
}

// promptAssetRPCItem 是 prompt-assets/list 的前端响应项。
// 它复用正式 prompt 字段，并在草稿资产上附加 draft 状态、来源和质检 issues。
type promptAssetRPCItem struct {
	promptRPCItem
	State       string `json:"state,omitempty"`
	DraftKey    string `json:"draft_key,omitempty"`
	DraftStatus string `json:"draft_status,omitempty"`
	SourceType  string `json:"source_type,omitempty"`
	Card        any    `json:"card,omitempty"`
	Issues      any    `json:"issues,omitempty"`
}

// handlePromptAssetList 返回当前 cwd 可用的用户 prompt assets，并合并可保存草稿。
func handlePromptAssetList(ctx context.Context, store Store, p promptAssetListParams) (any, error) {
	if store == nil {
		return nil, errPromptStoreRequired
	}
	cwd, err := requirePromptCWD(p.Cwd)
	if err != nil {
		return nil, err
	}
	templates, err := store.List(ctx, ListFilter{CWD: cwd, Limit: promptRPCLimit})
	if err != nil {
		return nil, err
	}
	assets := promptAssetTemplatesForCWD(templates, cwd)
	sections, err := promptAssetSectionsByTemplateID(ctx, store, assets)
	if err != nil {
		return nil, err
	}
	items := promptAssetItemsFromTemplates(assets, sections)
	drafts, err := store.ListIntentDrafts(ctx, IntentDraftListFilter{
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

// promptAssetTemplatesForCWD 过滤出当前 cwd 可见的用户资产模板，并按 logical key 去重。
func promptAssetTemplatesForCWD(templates []Template, cwd string) []Template {
	assets := make([]Template, 0, len(templates))
	for _, template := range templates {
		if promptAssetVisibleForCWD(template, cwd) && promptTemplateIsUserAsset(template) {
			assets = append(assets, template)
		}
	}
	return effectivePromptAssetTemplates(assets, cwd)
}

// promptAssetVisibleForCWD 判断资产模板是否对当前 cwd 可见；资产列表只展示显式 global 或当前项目 scope。
func promptAssetVisibleForCWD(template Template, cwd string) bool {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return false
	}
	return promptHasGlobalScope(template.Tags) || promptHasScopeCWD(template.Tags, requestScope)
}

// effectivePromptAssetTemplates 按 logical key 选择最终展示资产，项目级优先于 global，优先级高者胜出。
func effectivePromptAssetTemplates(templates []Template, cwd string) []Template {
	type pickedAsset struct {
		template Template
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
	templates = make([]Template, 0, len(out))
	for _, asset := range out {
		templates = append(templates, asset.template)
	}
	return templates
}

// promptAssetLogicalKey 用 agent、intent tag 和标题构造去重键。
func promptAssetLogicalKey(template Template) string {
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

// promptAssetScopeRank 给项目级资产更高优先级，global 作为兜底。
func promptAssetScopeRank(template Template, cwd string) int {
	if promptHasGlobalScope(template.Tags) && !promptHasScopeCWD(template.Tags, cwd) {
		return 1
	}
	return 0
}

// preferPromptAsset 按 scope rank、priority、原始顺序决定 logical key 下保留哪条资产。
func preferPromptAsset(leftRank, rightRank, leftPriority, rightPriority, leftIndex, rightIndex int) bool {
	if leftRank != rightRank {
		return leftRank < rightRank
	}
	if leftPriority != rightPriority {
		return leftPriority > rightPriority
	}
	return leftIndex < rightIndex
}

// promptTemplateIsUserAsset 排除 builtin/system 模板，只保留用户或 prompt intent 产物。
func promptTemplateIsUserAsset(template Template) bool {
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

// promptTemplateHasTag 判断 tags JSON 中是否存在指定标签。
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

// promptTemplateAuthoredByUser 判断模板是否由 RPC 写入路径创建或更新。
func promptTemplateAuthoredByUser(template Template) bool {
	return promptAuthorIsRPC(template.CreatedBy) || promptAuthorIsRPC(template.UpdatedBy)
}

// promptAuthorIsRPC 判断 author 字段是否来自 prompt RPC 写入路径。
func promptAuthorIsRPC(author string) bool {
	return strings.TrimSpace(author) == promptUpdatedBy
}

// promptTemplateAuthoredBySystem 判断模板是否由系统、内置或批量导入路径写入。
func promptTemplateAuthoredBySystem(template Template) bool {
	return promptAuthorLooksSystem(template.CreatedBy) || promptAuthorLooksSystem(template.UpdatedBy)
}

// promptAuthorLooksSystem 识别系统写入者名称，用于资产列表排除内置内容。
func promptAuthorLooksSystem(author string) bool {
	normalized := strings.ToLower(strings.TrimSpace(author))
	return strings.HasPrefix(normalized, "system") ||
		strings.HasPrefix(normalized, "builtin.") ||
		strings.Contains(normalized, "seed") ||
		strings.Contains(normalized, "migration")
}

// promptTemplateHasIntentAssetMarker 判断模板是否带有 prompt intent 资产标记。
func promptTemplateHasIntentAssetMarker(template Template) bool {
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

// promptAssetSectionsByTemplateID 批量加载资产模板 sections，并按 template ID 分组。
func promptAssetSectionsByTemplateID(
	ctx context.Context,
	store Store,
	templates []Template,
) (map[int64][]TemplateSection, error) {
	sectionsByTemplateID := map[int64][]TemplateSection{}
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

// promptAssetItemsFromTemplates 将模板和 sections 转为前端资产列表项。
func promptAssetItemsFromTemplates(
	templates []Template,
	sectionsByTemplateID map[int64][]TemplateSection,
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

// firstNonEmpty 返回第一个非空白字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// promptHasScopeCWD 判断 tags 中是否包含指定 cwd 的内部 scope tag。
func promptHasScopeCWD(raw json.RawMessage, cwd string) bool {
	want := promptScopeTagPrefix + strings.TrimSpace(cwd)
	for _, tag := range promptTags(raw) {
		if strings.TrimSpace(tag) == want {
			return true
		}
	}
	return false
}

// promptHasAnyScopeCWD 判断 tags 中是否包含任意项目 scope tag。
func promptHasAnyScopeCWD(raw json.RawMessage) bool {
	for _, tag := range promptTags(raw) {
		if strings.HasPrefix(strings.TrimSpace(tag), promptScopeTagPrefix) {
			return true
		}
	}
	return false
}

// promptHasGlobalScope 判断 tags 中是否包含 global scope tag。
func promptHasGlobalScope(raw json.RawMessage) bool {
	for _, tag := range promptTags(raw) {
		if strings.TrimSpace(tag) == "scope.global" {
			return true
		}
	}
	return false
}

// promptTags 解析 tags JSON，非法或空值返回空切片。
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
