package intent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

const (
	promptScopeTagPrefix = "scope.cwd:"  // 项目级 scope tag 前缀
	promptUpdatedBy      = "rpc.prompts" // 创建/更新者标识
	promptDraftListLimit = 1000          // 列出草稿的最大条数
)

var promptSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// CommitResult 是草稿提交成功后的返回结果。
type CommitResult struct {
	DraftKey  string `json:"draft_key"`
	PromptKey string `json:"prompt_key"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
}

// DiscardResult 是草稿丢弃操作的返回结果。
type DiscardResult struct {
	DraftKey string `json:"draft_key"`
	Status   string `json:"status"`
}

// HandleCommit 提交草稿为正式 prompt 模板，在事务中完成校验、写库、状态更新及旁系草稿拒绝，
// 最后触发 section 缓存失效。
func HandleCommit(
	ctx context.Context,
	promptStore promptstore.Store,
	invalidator contract.SectionInvalidator,
	builtin contract.BuiltinPromptRegistry,
	p CommitParams,
) (any, error) {
	if promptStore == nil {
		return nil, errors.New("prompt store is required for prompt intent commit")
	}
	cwd, err := requireCWD(p.Cwd)
	if err != nil {
		return nil, err
	}
	var result CommitResult
	err = promptStore.WithTx(ctx, func(txStore promptstore.Store) error {
		draft, err := txStore.GetIntentDraft(ctx, cwd, p.DraftKey)
		if err != nil {
			return err
		}
		global, err := promptIntentCommitGlobalScope(draft, p)
		if err != nil {
			return err
		}
		if strings.TrimSpace(draft.Status) != "ready_to_save" {
			return platformrpc.ErrInvalidParams("prompt intent draft is not ready to save")
		}
		if promptIntentDraftHasReviewIssue(draft) && !p.ConfirmRisk {
			return platformrpc.ErrInvalidParams("prompt intent draft requires risk confirmation")
		}
		result, err = commitPromptIntentDraft(ctx, txStore, builtin, cwd, draft, global)
		return err
	})
	if err != nil {
		return nil, err
	}
	invalidatePromptIntentCommit(invalidator, result.Kind)
	return result, nil
}

// promptIntentCommitGlobalScope 根据草稿 scope 和请求参数决定是否全局提交；scope 不匹配时报错。
func promptIntentCommitGlobalScope(draft *promptstore.PromptIntentDraft, p CommitParams) (bool, error) {
	scope := strings.TrimSpace(draft.Scope)
	switch scope {
	case "", "project":
		if p.EnableGlobal {
			return false, platformrpc.ErrInvalidParams("prompt intent global commit does not match draft scope")
		}
		return false, nil
	case "global":
		if !p.ConfirmGlobal {
			return false, platformrpc.ErrInvalidParams("prompt intent global commit requires global confirmation")
		}
		return true, nil
	default:
		return false, platformrpc.ErrInvalidParams("prompt intent draft scope must be project or global")
	}
}

// HandleDiscard 将草稿状态更新为 rejected。
func HandleDiscard(ctx context.Context, promptStore promptstore.Store, p DiscardParams) (any, error) {
	if promptStore == nil {
		return nil, errors.New("prompt store is required for prompt intent discard")
	}
	cwd, err := requireCWD(p.Cwd)
	if err != nil {
		return nil, err
	}
	draftKey := strings.TrimSpace(p.DraftKey)
	if draftKey == "" {
		return nil, platformrpc.ErrInvalidParams("prompt intent draft_key is required")
	}
	draft, err := promptStore.UpdateIntentDraftStatus(ctx, cwd, draftKey, "rejected")
	if err != nil {
		return nil, err
	}
	return DiscardResult{DraftKey: draft.DraftKey, Status: draft.Status}, nil
}

// commitPromptIntentDraft 执行草稿提交的核心逻辑：规范化卡片、按 kind 分派写库、
// 更新草稿状态为 enabled、拒绝同批次其他草稿。
func commitPromptIntentDraft(ctx context.Context, store promptstore.Store, builtin contract.BuiltinPromptRegistry, cwd string, draft *promptstore.PromptIntentDraft, global bool) (CommitResult, error) {
	kind, err := normalizeKind(draft.Kind)
	if err != nil {
		return CommitResult{}, err
	}
	card, err := parsePromptIntentCard(string(draft.GeneratedCard))
	if err != nil {
		return CommitResult{}, err
	}
	card = NormalizeGeneratedCard(string(kind), draft.RawInput, card)
	if err := validatePromptIntentCommitCard(kind, draft.RawInput, card); err != nil {
		return CommitResult{}, err
	}
	var saved *promptstore.PromptTemplate
	switch kind {
	case KindExpert:
		saved, err = commitPromptIntentExpert(ctx, store, builtin, cwd, draft.DraftKey, card, global)
	case KindRecall:
		saved, err = commitPromptIntentRecall(ctx, store, builtin, cwd, draft.DraftKey, card, global)
	case KindDefaultRule:
		saved, err = commitPromptIntentDefaultRule(ctx, store, builtin, cwd, draft.DraftKey, card, global)
	}
	if err != nil {
		return CommitResult{}, err
	}
	if _, err := store.UpdateIntentDraftStatus(ctx, cwd, draft.DraftKey, "enabled"); err != nil {
		return CommitResult{}, err
	}
	if err := rejectSiblingPromptIntentDrafts(ctx, store, cwd, draft); err != nil {
		return CommitResult{}, err
	}
	return CommitResult{DraftKey: draft.DraftKey, PromptKey: saved.PromptKey, Kind: string(kind), Status: "enabled"}, nil
}

// rejectSiblingPromptIntentDrafts 拒绝与已提交草稿同批次（同 origin_hash + raw_input + scope）的其他草稿。
func rejectSiblingPromptIntentDrafts(ctx context.Context, store promptstore.Store, cwd string, committed *promptstore.PromptIntentDraft) error {
	originHash := strings.TrimSpace(committed.OriginHash)
	rawInput := strings.TrimSpace(committed.RawInput)
	if originHash == "" || rawInput == "" {
		return nil
	}
	drafts, err := store.ListIntentDrafts(ctx, promptstore.PromptIntentDraftListFilter{
		CWD:    cwd,
		Status: "ready_to_save",
		Limit:  promptDraftListLimit,
	})
	if err != nil {
		return err
	}
	for _, draft := range drafts {
		if !samePromptIntentDraftBatch(committed, draft, originHash, rawInput) {
			continue
		}
		if _, err := store.UpdateIntentDraftStatus(ctx, cwd, draft.DraftKey, "rejected"); err != nil {
			return err
		}
	}
	return nil
}

// samePromptIntentDraftBatch 判断两个草稿是否属于同一批次，排除已提交草稿自身。
func samePromptIntentDraftBatch(committed *promptstore.PromptIntentDraft, draft promptstore.PromptIntentDraft, originHash, rawInput string) bool {
	if strings.TrimSpace(draft.DraftKey) == strings.TrimSpace(committed.DraftKey) {
		return false
	}
	if strings.TrimSpace(draft.OriginHash) != originHash {
		return false
	}
	if strings.TrimSpace(draft.RawInput) != rawInput {
		return false
	}
	return promptIntentDraftScopeIsGlobal(draft.Scope) == promptIntentDraftScopeIsGlobal(committed.Scope)
}

// promptIntentDraftScopeIsGlobal 判断草稿 scope 是否为全局。
func promptIntentDraftScopeIsGlobal(scope string) bool {
	return strings.TrimSpace(scope) == "global"
}

// commitPromptIntentExpert 将 expert 类型草稿写入 prompt 模板，包含 identity/workflow/constraints/output 四个 section。
func commitPromptIntentExpert(ctx context.Context, store promptstore.Store, builtin contract.BuiltinPromptRegistry, cwd, draftKey string, card Card, global bool) (*promptstore.PromptTemplate, error) {
	candidates, err := promptIntentPromptKeyCandidates(KindExpert, card.Title, "", draftKey)
	if err != nil {
		return nil, err
	}
	saved, err := createPromptIntentTemplateFromCandidates(ctx, store, cwd, global, promptstore.PromptTemplate{
		Title:       strings.TrimSpace(card.Title),
		PromptText:  "",
		Description: strings.TrimSpace(card.Summary),
		AgentKey:    "main",
		WhenToUse:   strings.TrimSpace(card.WhenToUse),
		Tags:        mustJSONTags("intent:expert"),
		Variables:   json.RawMessage("{}"),
	}, candidates, builtin)
	if err != nil {
		return nil, err
	}
	sections := []promptstore.PromptTemplateSection{
		{TemplateID: saved.ID, SectionKey: "identity", Region: "static", Ordinal: 10, Body: strings.TrimSpace(card.Title + "\n\n" + card.Summary), Enabled: true, TriggerType: "always"},
		{TemplateID: saved.ID, SectionKey: "workflow", Region: "dynamic", Ordinal: 20, Body: strings.Join(trimmedPromptIntentExamples(card.Workflow), "\n"), Enabled: true, TriggerType: "always"},
		{TemplateID: saved.ID, SectionKey: "constraints", Region: "dynamic", Ordinal: 30, Body: promptIntentExpertConstraintsBody(card), Enabled: true, TriggerType: "always"},
		{TemplateID: saved.ID, SectionKey: "output", Region: "dynamic", Ordinal: 40, Body: strings.TrimSpace(card.Output), Enabled: true, TriggerType: "always"},
	}
	for _, section := range sections {
		if _, err := store.UpsertSection(ctx, section); err != nil {
			return nil, err
		}
	}
	return saved, nil
}

// promptIntentExpertConstraintsBody 构建 constraints section 正文，包含 when_not_to_use、save_boundary 和 constraints 列表。
func promptIntentExpertConstraintsBody(card Card) string {
	parts := nonEmptyStrings(card.WhenNotToUse)
	if boundary := strings.TrimSpace(card.SaveBoundary); boundary != "" {
		parts = append(parts, "保存边界："+boundary)
	}
	if constraints := strings.Join(trimmedPromptIntentExamples(card.Constraints), "\n"); constraints != "" {
		parts = append(parts, constraints)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// commitPromptIntentRecall 将 recall 类型草稿写入 prompt 模板，并在 cwd 内锁定 recall_topic 防止重复。
func commitPromptIntentRecall(ctx context.Context, store promptstore.Store, builtin contract.BuiltinPromptRegistry, cwd, draftKey string, card Card, global bool) (*promptstore.PromptTemplate, error) {
	candidates, err := promptIntentPromptKeyCandidates(KindRecall, card.Title, card.RecallTopic, draftKey)
	if err != nil {
		return nil, err
	}
	saved, err := createPromptIntentTemplateFromCandidates(ctx, store, cwd, global, promptstore.PromptTemplate{
		Title:       strings.TrimSpace(card.Title),
		PromptText:  "",
		Description: strings.TrimSpace(card.Summary),
		AgentKey:    "main",
		WhenToUse:   "Knowledge material: " + strings.TrimSpace(card.Summary),
		Tags:        mustJSONTags("intent:recall"),
		Variables:   json.RawMessage("{}"),
	}, candidates, builtin)
	if err != nil {
		return nil, err
	}
	sectionKey := "recall_" + strings.ReplaceAll(strings.TrimSpace(card.RecallTopic), "-", "_")
	if err := rejectDuplicateRecallTopicInCWD(ctx, store, cwd, card.RecallTopic, global, saved.ID, sectionKey); err != nil {
		return nil, err
	}
	section, err := store.UpsertSection(ctx, promptstore.PromptTemplateSection{
		TemplateID:  saved.ID,
		SectionKey:  sectionKey,
		Region:      "dynamic",
		Ordinal:     100,
		Body:        strings.TrimSpace(card.RecallBody),
		Enabled:     true,
		TriggerType: "recall",
		RecallTopic: strings.TrimSpace(card.RecallTopic),
	})
	if err != nil {
		return nil, err
	}
	if err := store.UpsertRecallTopicTargetInCWD(ctx, cwd, section.RecallTopic, section.TemplateID, section.SectionKey); err != nil {
		return nil, err
	}
	return saved, nil
}

// commitPromptIntentDefaultRule 将 default_rule 类型草稿写入 prompt 模板。
func commitPromptIntentDefaultRule(ctx context.Context, store promptstore.Store, builtin contract.BuiltinPromptRegistry, cwd, draftKey string, card Card, global bool) (*promptstore.PromptTemplate, error) {
	candidates, err := promptIntentPromptKeyCandidates(KindDefaultRule, card.Title, "", draftKey)
	if err != nil {
		return nil, err
	}
	saved, err := createPromptIntentTemplateFromCandidates(ctx, store, cwd, global, promptstore.PromptTemplate{
		Title:       strings.TrimSpace(card.Title),
		PromptText:  "",
		Description: strings.TrimSpace(card.Summary),
		AgentKey:    "default_rule",
		WhenToUse:   "Project default rule: " + strings.TrimSpace(card.Summary),
		Tags:        mustJSONTags("intent:default_rule"),
		Variables:   json.RawMessage("{}"),
	}, candidates, builtin)
	if err != nil {
		return nil, err
	}
	if _, err := store.UpsertSection(ctx, promptstore.PromptTemplateSection{
		TemplateID:  saved.ID,
		SectionKey:  "project_rule",
		Region:      "dynamic",
		Ordinal:     100,
		Body:        strings.TrimSpace(card.DefaultRuleBody),
		Enabled:     true,
		TriggerType: "always",
	}); err != nil {
		return nil, err
	}
	return saved, nil
}

// promptIntentPromptKeyCandidates 生成最多两个候选 prompt_key：基础 slug 和带 draft_key 短哈希后缀的版本，
// 用于处理名称冲突。
func promptIntentPromptKeyCandidates(kind Kind, title, topic, draftKey string) ([]string, error) {
	base := promptIntentPromptKeyBase(kind, title, topic)
	if base == "" {
		return nil, platformrpc.ErrInvalidParams("prompt intent prompt_key base is required")
	}
	if strings.TrimSpace(draftKey) == "" {
		return nil, platformrpc.ErrInvalidParams("prompt intent draft_key is required for prompt_key collision suffix")
	}
	return []string{base, base + "-" + shortPromptIntentKeySuffix(draftKey)}, nil
}

// promptIntentPromptKeyBase 根据 kind 生成 prompt_key 的基础路径。
func promptIntentPromptKeyBase(kind Kind, title, topic string) string {
	switch kind {
	case KindExpert:
		return "main/expert/" + stableSlug(title)
	case KindRecall:
		return "main/knowledge/" + stableSlug(topic)
	case KindDefaultRule:
		return "main/default-rule/" + stableSlug(title)
	default:
		return ""
	}
}

// stableSlug 将任意字符串转为 slug 格式（小写、非字母数字替换为连字符）。
func stableSlug(value string) string {
	slug := promptSlugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "prompt"
	}
	return slug
}

// shortPromptIntentKeySuffix 取 draft_key SHA-256 前 8 位作为冲突消解后缀。
func shortPromptIntentKeySuffix(draftKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(draftKey)))
	return hex.EncodeToString(sum[:])[:8]
}

// createPromptIntentTemplateFromCandidates 依次尝试候选 prompt_key 写库，遇到冲突则换下一个，
// 全部失败时返回 invalid_params 错误。
func createPromptIntentTemplateFromCandidates(
	ctx context.Context,
	store promptstore.Store,
	cwd string,
	global bool,
	template promptstore.PromptTemplate,
	candidates []string,
	builtin contract.BuiltinPromptRegistry,
) (*promptstore.PromptTemplate, error) {
	var lastConflict error
	for _, candidate := range candidates {
		if promptIntentBuiltinPromptExists(builtin, candidate) {
			lastConflict = contract.ErrConflict
			continue
		}
		template.PromptKey = candidate
		saved, err := createPromptIntentTemplate(ctx, store, cwd, global, template)
		if err == nil {
			return saved, nil
		}
		if platformdb.IsConflict(err) || errors.Is(err, contract.ErrConflict) {
			lastConflict = err
			continue
		}
		return nil, err
	}
	if lastConflict != nil {
		return nil, platformrpc.ErrInvalidParams("prompt intent prompt_key already exists")
	}
	return nil, platformrpc.ErrInvalidParams("prompt intent prompt_key candidate is required")
}

// promptIntentBuiltinPromptExists 检查 builtin registry 中是否已存在指定 prompt_key。
func promptIntentBuiltinPromptExists(builtin contract.BuiltinPromptRegistry, promptKey string) bool {
	if builtin == nil {
		return false
	}
	_, ok := builtin.GetTemplate(strings.TrimSpace(promptKey))
	return ok
}

// createPromptIntentTemplate 写入单条 prompt 模板，补充 scope tag、enabled 和 created/updated_by 字段。
func createPromptIntentTemplate(ctx context.Context, store promptstore.Store, cwd string, global bool, template promptstore.PromptTemplate) (*promptstore.PromptTemplate, error) {
	cwd, err := requireCWD(cwd)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(template.PromptKey)
	if key == "" {
		return nil, platformrpc.ErrInvalidParams("prompt intent prompt_key is required")
	}
	template.PromptKey = key
	template.Tags = withPromptIntentScopeTag(template.Tags, cwd, global)
	template.Enabled = true
	template.ManuallyEdited = false
	template.CreatedBy = promptUpdatedBy
	template.UpdatedBy = promptUpdatedBy
	return store.CreatePromptTemplate(ctx, template)
}

// withPromptIntentScopeTag 用指定 cwd 或全局标记替换模板 tags 中的 scope 相关标签。
func withPromptIntentScopeTag(raw json.RawMessage, cwd string, global bool) json.RawMessage {
	tags := make([]string, 0, len(promptTags(raw))+1)
	for _, tag := range promptTags(raw) {
		tag = strings.TrimSpace(tag)
		if tag == "" || tag == "scope.global" || strings.HasPrefix(tag, promptScopeTagPrefix) {
			continue
		}
		tags = append(tags, tag)
	}
	if global {
		tags = append(tags, "scope.global")
	} else {
		tags = append(tags, promptScopeTagPrefix+strings.TrimSpace(cwd))
	}
	return mustJSONTags(tags...)
}

// promptTags 安全解析 JSON tags 字段，失败返回空切片。
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

// rejectDuplicateRecallTopicInCWD 在事务中加锁并检查 cwd 内是否已有同名 recall_topic，
// 有则返回错误阻止重复提交。
func rejectDuplicateRecallTopicInCWD(
	ctx context.Context,
	store promptstore.Store,
	cwd, topic string,
	targetGlobal bool,
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
	templates, err := store.List(ctx, promptstore.ListFilter{CWD: cwd, Limit: promptIntentDuplicateListLimit})
	if err != nil {
		return err
	}
	sectionsByID, err := promptIntentSectionsByTemplateID(ctx, store, templates)
	if err != nil {
		return err
	}
	sectionKey = strings.TrimSpace(sectionKey)
	if promptIntentRecallDuplicateExists(templates, sectionsByID, cwd, topic, targetGlobal, templateID, sectionKey) {
		return fmt.Errorf("dashboard: duplicate recall_topic %q in cwd", topic)
	}
	return nil
}

// promptIntentRecallDuplicateExists 检查模板列表中是否存在与目标 topic 冲突的 recall section。
func promptIntentRecallDuplicateExists(
	templates []promptstore.PromptTemplate,
	sectionsByID map[int64][]promptstore.PromptTemplateSection,
	cwd, topic string,
	targetGlobal bool,
	templateID int64,
	sectionKey string,
) bool {
	for _, template := range templates {
		if !template.Enabled || !promptIntentTemplateVisibleForCWD(template, cwd) {
			continue
		}
		if !promptIntentRecallDuplicateConflicts(targetGlobal, template, cwd) {
			continue
		}
		if promptIntentRecallDuplicateSectionExists(sectionsByID[template.ID], topic, templateID, sectionKey) {
			return true
		}
	}
	return false
}

// promptIntentRecallDuplicateSectionExists 检查单个模板的 section 列表中是否有重复的 recall topic。
func promptIntentRecallDuplicateSectionExists(
	sections []promptstore.PromptTemplateSection,
	topic string,
	templateID int64,
	sectionKey string,
) bool {
	for _, section := range sections {
		if promptIntentRecallSectionDuplicatesTopic(section, topic, templateID, sectionKey) {
			return true
		}
	}
	return false
}

// promptIntentRecallSectionDuplicatesTopic 判断单个 section 是否与目标 topic 重复（排除自身）。
func promptIntentRecallSectionDuplicatesTopic(
	section promptstore.PromptTemplateSection,
	topic string,
	templateID int64,
	sectionKey string,
) bool {
	if !section.Enabled || strings.TrimSpace(strings.ToLower(section.TriggerType)) != "recall" {
		return false
	}
	if strings.TrimSpace(section.RecallTopic) != topic {
		return false
	}
	return section.TemplateID != templateID || strings.TrimSpace(section.SectionKey) != sectionKey
}

// validatePromptIntentCommitCard 在提交前执行质量和完整性校验，发现 block 问题时返回 invalid_params 错误。
func validatePromptIntentCommitCard(kind Kind, rawInput string, card Card) error {
	if issue, ok := firstPromptIntentBlockIssue(promptIntentDraftIssues(kind, rawInput, card)); ok {
		return platformrpc.ErrInvalidParams(promptIntentCommitBlockMessage(issue))
	}
	var err error
	switch kind {
	case KindExpert:
		err = requireNonEmptyPromptIntentFields(map[string]string{
			"title":       card.Title,
			"summary":     card.Summary,
			"when_to_use": card.WhenToUse,
			"workflow":    strings.Join(card.Workflow, "\n"),
			"output":      card.Output,
		})
	case KindRecall:
		err = requireNonEmptyPromptIntentFields(map[string]string{
			"recall_topic": card.RecallTopic,
			"recall_body":  card.RecallBody,
		})
	case KindDefaultRule:
		err = requireNonEmptyPromptIntentFields(map[string]string{
			"default_rule_body": card.DefaultRuleBody,
		})
	default:
		return platformrpc.ErrInvalidParams("prompt intent kind must be expert, recall, or default_rule")
	}
	if err != nil {
		return err
	}
	return nil
}

// promptIntentCommitBlockMessage 生成提交失败时的错误消息前缀（质量/安全/一致性）。
func promptIntentCommitBlockMessage(issue Issue) string {
	prefix := "prompt intent draft quality failed"
	if promptIntentSafetyIssueCode(issue.Code) {
		prefix = "prompt intent draft safety failed"
	}
	if strings.TrimSpace(issue.Code) == "kind_mismatch" {
		prefix = "prompt intent draft consistency failed"
	}
	message := strings.TrimSpace(issue.Message)
	if message == "" {
		message = strings.TrimSpace(issue.Code)
	}
	return prefix + ": " + message
}

// promptIntentSafetyIssueCode 判断 issue code 是否属于安全类问题。
func promptIntentSafetyIssueCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "input_too_short", "external_system_prompt", "external_system_prompt_source", "identity_pollution", "tool_protocol_pollution", "overbroad_scope":
		return true
	default:
		return false
	}
}

// firstPromptIntentBlockIssue 返回列表中第一个 severity=block 的问题。
func firstPromptIntentBlockIssue(issues []Issue) (Issue, bool) {
	for _, issue := range issues {
		if strings.TrimSpace(issue.Severity) == "block" {
			return issue, true
		}
	}
	return Issue{}, false
}

// requirePromptIntentExamples 校验 hit_examples 和 miss_examples 各至少含一条非空项。
func requirePromptIntentExamples(hit, miss []string) error {
	if len(trimmedPromptIntentExamples(hit)) == 0 {
		return platformrpc.ErrInvalidParams("prompt intent hit_examples is required")
	}
	if len(trimmedPromptIntentExamples(miss)) == 0 {
		return platformrpc.ErrInvalidParams("prompt intent miss_examples is required")
	}
	return nil
}

// requireNonEmptyPromptIntentFields 检查指定字段是否均非空，任一为空返回 invalid_params 错误。
func requireNonEmptyPromptIntentFields(fields map[string]string) error {
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			return platformrpc.ErrInvalidParams(fmt.Sprintf("prompt intent %s is required", name))
		}
	}
	return nil
}

// promptIntentDraftHasReviewIssue 判断草稿是否含有 review 级别问题，有则要求用户确认才能提交。
func promptIntentDraftHasReviewIssue(draft *promptstore.PromptIntentDraft) bool {
	var issues []Issue
	if err := json.Unmarshal(draft.Issues, &issues); err != nil {
		return true
	}
	if kind, err := normalizeKind(draft.Kind); err == nil {
		if card, err := parsePromptIntentCard(string(draft.GeneratedCard)); err == nil {
			issues = append(issues, promptIntentDraftIssues(kind, draft.RawInput, card)...)
		} else {
			return true
		}
	} else {
		return true
	}
	for _, issue := range issues {
		if strings.TrimSpace(issue.Severity) == "review" {
			return true
		}
	}
	return false
}

// invalidatePromptIntentCommit 根据 kind 清除对应的 section 动态缓存。
func invalidatePromptIntentCommit(invalidator contract.SectionInvalidator, kind string) {
	if invalidator == nil {
		return
	}
	switch strings.TrimSpace(kind) {
	case "expert":
		invalidator.InvalidateSections(contract.InvalidateClear, contract.DynamicSectionAvailableExperts)
	case "recall":
		invalidator.InvalidateSections(contract.InvalidateClear, contract.DynamicSectionRecallCatalog)
	case "default_rule":
		invalidator.InvalidateSections(contract.InvalidateClear, contract.DynamicSectionProjectDefaultRules)
	}
}

// mustJSONTags 将 tags 列表序列化为 JSON，失败时返回空数组。
func mustJSONTags(tags ...string) json.RawMessage {
	encoded, err := json.Marshal(tags)
	if err != nil {
		return json.RawMessage("[]")
	}
	return encoded
}
