package intent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt/intent/draftdream"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

// DryRunDisclaimer 是 dry-run RPC 的固定提示，明确该路径不会写库也不代表真实路由承诺。
const DryRunDisclaimer = "这是创建前试问解释，不会写入路由索引，也不承诺真实模型一定做出相同选择。"

// DraftResult 是单张草稿卡片的返回结果。
type DraftResult struct {
	DraftKey      string  `json:"draft_key"`
	RequestedKind string  `json:"requested_kind"`
	InferredKind  string  `json:"inferred_kind"`
	Status        string  `json:"status"`
	Confidence    float64 `json:"confidence"`
	Scope         string  `json:"scope"`
	Issues        []Issue `json:"issues"`
	Card          Card    `json:"card"`
}

// DraftSetResult 是多张草稿卡片的批量返回结果。
type DraftSetResult struct {
	RequestedKind string        `json:"requested_kind"`
	InferredKind  string        `json:"inferred_kind"`
	Drafts        []DraftResult `json:"drafts"`
}

// DryRunResult 是试问（dry-run）操作的返回结果，告知前端这份草稿会触发什么动作。
type DryRunResult struct {
	WouldUse   bool     `json:"would_use"`
	Action     string   `json:"action"`
	Target     string   `json:"target,omitempty"`
	Reasons    []string `json:"reasons"`
	Candidates []string `json:"candidates,omitempty"`
	Disclaimer string   `json:"disclaimer"`
}

// HandleDraft 处理草稿创建请求：调用 dream 生成卡片、规范化、修复质量问题、去重，
// 最后将草稿持久化并返回结果。
func HandleDraft(
	ctx context.Context,
	promptStore Store,
	dream contract.DreamExecutor,
	builtin contract.BuiltinPromptRegistry,
	p DraftParams,
) (any, error) {
	cwd, kind, rawInput, err := validatePromptIntentDraftRequest(promptStore, dream, p)
	if err != nil {
		return nil, err
	}
	ctx, cancel := platformconfig.WithTimeoutIfNone(ctx, platformconfig.PromptIntentDraftTimeout)
	defer cancel()
	prompt, err := buildPromptIntentDraftPrompt(ctx, promptStore, cwd, kind, rawInput)
	if err != nil {
		return nil, err
	}
	dreamOptions := promptIntentDraftDreamOptions(p)
	cards, err := draftdream.ExecuteWithOptions(ctx, dream, prompt, dreamOptions, parsePromptIntentCards)
	if err != nil {
		return nil, err
	}
	cards = promptIntentNormalizeGeneratedCards(kind, rawInput, cards)
	cards = promptIntentSingleTypeCards(kind, rawInput, cards)
	cards, err = repairPromptIntentCardsIfNeeded(ctx, dream, kind, rawInput, cards, dreamOptions)
	if err != nil {
		return nil, err
	}
	cards = promptIntentNormalizeGeneratedCards(kind, rawInput, cards)
	cards = promptIntentSingleTypeCards(kind, rawInput, cards)
	results, drafts, err := buildPromptIntentDrafts(ctx, promptStore, builtin, cwd, kind, rawInput, p, cards)
	if err != nil {
		return nil, err
	}
	if err := upsertPromptIntentDrafts(ctx, promptStore, drafts); err != nil {
		return nil, err
	}
	if len(results) == 1 {
		return results[0], nil
	}
	return DraftSetResult{
		RequestedKind: string(kind),
		InferredKind:  results[0].InferredKind,
		Drafts:        results,
	}, nil
}

// promptIntentDraftDreamOptions 从 DraftParams 中提取 dream 调用选项。
func promptIntentDraftDreamOptions(p DraftParams) contract.DreamOptions {
	return contract.DreamOptions{
		Provider:      strings.TrimSpace(p.Provider),
		Model:         strings.TrimSpace(p.Model),
		ModelProvider: strings.TrimSpace(p.ModelProvider),
	}
}

// buildPromptIntentDrafts 对多张卡片逐一构建 DraftResult 和 PromptIntentDraft，返回两个等长切片。
func buildPromptIntentDrafts(
	ctx context.Context,
	promptStore Store,
	builtin contract.BuiltinPromptRegistry,
	cwd string,
	requestedKind Kind,
	rawInput string,
	p DraftParams,
	cards []Card,
) ([]DraftResult, []PromptIntentDraft, error) {
	results := make([]DraftResult, 0, len(cards))
	drafts := make([]PromptIntentDraft, 0, len(cards))
	for _, card := range cards {
		result, draft, err := buildPromptIntentDraft(ctx, promptStore, builtin, cwd, requestedKind, rawInput, p, card)
		if err != nil {
			return nil, nil, err
		}
		results = append(results, result)
		drafts = append(drafts, draft)
	}
	return results, drafts, nil
}

// upsertPromptIntentDrafts 在事务中批量 upsert 草稿记录。
func upsertPromptIntentDrafts(ctx context.Context, promptStore Store, drafts []PromptIntentDraft) error {
	return promptStore.WithTx(ctx, func(txStore Store) error {
		for _, draft := range drafts {
			if _, err := txStore.UpsertIntentDraft(ctx, draft); err != nil {
				return err
			}
		}
		return nil
	})
}

// validatePromptIntentDraftRequest 校验草稿请求的必要字段：dream executor、prompt store、cwd、kind 和 raw_input。
func validatePromptIntentDraftRequest(
	promptStore Store,
	dream contract.DreamExecutor,
	p DraftParams,
) (string, Kind, string, error) {
	if dream == nil {
		return "", "", "", contract.ErrDreamExecutorNotConfigured
	}
	if promptStore == nil {
		return "", "", "", errors.New("prompt store is required for prompt intent authoring")
	}
	cwd, err := requireCWD(p.Cwd)
	if err != nil {
		return "", "", "", err
	}
	kind, err := normalizeKind(p.Kind)
	if err != nil {
		return "", "", "", err
	}
	rawInput := strings.TrimSpace(p.RawInput)
	if rawInput == "" {
		return "", "", "", platformrpc.ErrInvalidParams("prompt intent raw_input is required")
	}
	return cwd, kind, rawInput, nil
}

// newPromptIntentDraftKeyFromEntropy 使用加密随机数生成草稿唯一键。
func newPromptIntentDraftKeyFromEntropy(kind Kind) (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return newPromptIntentDraftKey(kind, time.Now(), random)
}

// buildPromptIntentDraftResult 根据卡片内容构建 DraftResult 和待持久化的 PromptIntentDraft。
func buildPromptIntentDraftResult(
	draftKey, cwd string,
	requestedKind, inferredKind Kind,
	rawInput string,
	p DraftParams,
	card Card,
	extraIssues []Issue,
) (DraftResult, PromptIntentDraft, error) {
	issues := promptIntentDraftIssues(inferredKind, rawInput, card)
	issues = append(issues, extraIssues...)
	confidence, status := promptIntentDraftConfidenceAndStatus(issues)
	cardJSON, err := json.Marshal(card)
	if err != nil {
		return DraftResult{}, PromptIntentDraft{}, err
	}
	issuesJSON, err := json.Marshal(issues)
	if err != nil {
		return DraftResult{}, PromptIntentDraft{}, err
	}
	draft := PromptIntentDraft{
		DraftKey:      draftKey,
		CWD:           cwd,
		Kind:          string(inferredKind),
		RawInput:      rawInput,
		SourceType:    strings.TrimSpace(p.SourceType),
		SourceURL:     strings.TrimSpace(p.SourceURL),
		OriginHash:    promptIntentOriginHash(rawInput),
		LicenseHint:   strings.TrimSpace(p.LicenseHint),
		GeneratedCard: cardJSON,
		Confidence:    confidence,
		Status:        status,
		Scope:         promptIntentDraftScope(p.EnableGlobal),
		Issues:        issuesJSON,
	}
	result := DraftResult{
		DraftKey:      draftKey,
		RequestedKind: string(requestedKind),
		InferredKind:  string(inferredKind),
		Status:        status,
		Confidence:    confidence,
		Scope:         draft.Scope,
		Issues:        issues,
		Card:          card,
	}
	return result, draft, nil
}

// buildPromptIntentDraft 为单张卡片构建草稿，包含 kind 推断、key 生成和重复检测。
func buildPromptIntentDraft(
	ctx context.Context,
	promptStore Store,
	builtin contract.BuiltinPromptRegistry,
	cwd string,
	requestedKind Kind,
	rawInput string,
	p DraftParams,
	card Card,
) (DraftResult, PromptIntentDraft, error) {
	inferredKind, err := normalizeKind(card.Kind)
	if err != nil {
		return DraftResult{}, PromptIntentDraft{}, err
	}
	draftKey, err := newPromptIntentDraftKeyFromEntropy(inferredKind)
	if err != nil {
		return DraftResult{}, PromptIntentDraft{}, err
	}
	duplicateIssues, err := promptIntentDuplicateIssues(ctx, promptStore, builtin, cwd, inferredKind, rawInput, card, p.EnableGlobal)
	if err != nil {
		return DraftResult{}, PromptIntentDraft{}, err
	}
	return buildPromptIntentDraftResult(draftKey, cwd, requestedKind, inferredKind, rawInput, p, card, duplicateIssues)
}

// promptIntentDraftScope 将 bool 转换为 scope 字符串。
func promptIntentDraftScope(global bool) string {
	if global {
		return "global"
	}
	return "project"
}

// promptIntentDraftConfidenceAndStatus 根据是否有 block 问题决定草稿置信度和状态：
// 有 block → 0.3/draft；无 block → 0.85/ready_to_save。
func promptIntentDraftConfidenceAndStatus(issues []Issue) (float64, string) {
	if promptIntentHasBlockIssue(issues) {
		return 0.3, "draft"
	}
	return 0.85, "ready_to_save"
}

// HandleDryRun 模拟草稿被使用时会触发的动作（不写入路由），帮助用户理解草稿效果。
func HandleDryRun(
	ctx context.Context,
	promptStore Store,
	_ contract.DreamExecutor,
	_ contract.BuiltinPromptRegistry,
	p DryRunParams,
) (any, error) {
	question := strings.TrimSpace(p.Question)
	if question == "" {
		return nil, platformrpc.ErrInvalidParams("prompt intent dry-run question is required")
	}
	card, err := promptIntentDryRunCard(ctx, promptStore, p)
	if err != nil {
		return nil, err
	}
	kind, err := normalizeKind(card.Kind)
	if err != nil {
		return nil, err
	}
	result := DryRunResult{
		WouldUse:   true,
		Reasons:    []string{"输入的问题会用来判断这份草稿是否适合参与回答。"},
		Disclaimer: DryRunDisclaimer,
	}
	switch kind {
	case KindRecall:
		result.Action = "prompt_recall"
		result.Target = strings.TrimSpace(card.RecallTopic)
		result.Candidates = nonEmptyStrings(card.RecallTopic, card.Title)
		result.Reasons = []string{"问题可能需要查阅这份资料。"}
	case KindExpert:
		result.Action = "launch_agent"
		result.Target = strings.TrimSpace(card.Title)
		result.Candidates = nonEmptyStrings(card.Title, card.WhenToUse)
		result.Reasons = []string{"问题可能适合按这项能力的步骤处理。"}
	case KindDefaultRule:
		result.Action = "default_rule"
		result.Target = strings.TrimSpace(card.Title)
		result.Candidates = nonEmptyStrings(card.Title)
		result.Reasons = []string{"默认规则会作为类似问题的回答约束。"}
	default:
		result.WouldUse = false
		result.Action = "none"
	}
	if result.Target == "" {
		result.WouldUse = false
		result.Action = "none"
	}
	return result, nil
}

// HandleE2EHealth 向 dream executor 发送固定探测请求，验证 fixture provider 是否就绪。
func HandleE2EHealth(ctx context.Context, dream contract.DreamExecutor, _ E2EHealthParams) (E2EHealthResult, error) {
	if dream == nil {
		return E2EHealthResult{}, contract.ErrDreamExecutorNotConfigured
	}
	raw, err := dream.ExecuteDream(ctx, "Super-Dolphin prompt intent e2e health: strict JSON only.")
	if err != nil {
		return E2EHealthResult{}, err
	}
	var out E2EHealthResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		return E2EHealthResult{}, err
	}
	if strings.TrimSpace(out.Provider) != "e2e-fixture" {
		return E2EHealthResult{}, platformrpc.ErrInvalidState("prompt intent e2e fixture provider is required")
	}
	return out, nil
}

// newPromptIntentDraftKey 构造格式为 intent/<kind>/<timestamp>-<hex8> 的草稿键。
func newPromptIntentDraftKey(kind Kind, now time.Time, random []byte) (string, error) {
	if len(random) < 8 {
		return "", errors.New("prompt intent draft key requires 8 random bytes")
	}
	if _, err := normalizeKind(string(kind)); err != nil {
		return "", err
	}
	return fmt.Sprintf("intent/%s/%d-%s", kind, now.UnixNano(), hex.EncodeToString(random[:8])), nil
}

// buildPromptIntentDraftPrompt 构建传给 LLM 的草稿生成 prompt，
// default_rule 类型会附加当前项目已有规则列表以便检查冲突。
func buildPromptIntentDraftPrompt(ctx context.Context, store Store, cwd string, kind Kind, rawInput string) (string, error) {
	var existingRules []string
	if kind == KindDefaultRule {
		sections, err := store.ListDefaultRuleSections(ctx, cwd)
		if err != nil {
			return "", err
		}
		for _, section := range sections {
			if body := strings.TrimSpace(section.Body); body != "" {
				existingRules = append(existingRules, body)
			}
		}
	}
	return fmt.Sprintf(`你是 Super-Dolphin 提示词创建助手。
你只在创建期整理用户输入，不能设计运行时路由器。
严格输出 JSON，不要 Markdown。

输出 schema:
单一草稿时输出一个对象；同一输入确实包含多个同类型、可独立保存资产时，输出 {"drafts":[对象, ...]}。
对象里的 kind 是你判断后的 inferred_kind，不必等于 requested_kind。
{
  "kind": "expert|recall|default_rule",
  "title": "...",
  "summary": "...",
  "when_to_use": "...",
  "when_not_to_use": "...",
  "workflow": ["..."],
  "constraints": ["..."],
  "output": "...",
  "save_boundary": "...",
  "recall_topic": "...",
	  "recall_body": "...",
	  "default_rule_body": "...",
  "source_profile": "external_prompt|reference_doc|table_data|workflow_sop|api_doc|meeting_notes|business_rule|unknown",
  "source_facts": [{"category": "identity|communication|search_reading|code_change|dependency_api|debugging|safety|task_management|output|tool_protocol|topic|fields|key_rows|units|scope|query_examples|endpoint|method|parameters|auth|response|errors|limits|examples|trigger|inputs|steps|roles|exceptions|outputs|facts|decisions|action_items|owners|dates|open_questions|key_points|source|usage|rule|conditions|enforcement", "summary": "...", "disposition": "preserve|translate|drop"}],
	  "hit_examples": ["..."],
	  "miss_examples": ["..."],
  "conflicting_rules": [{"title": "...", "summary": "..."}],
  "suggested_alternative": {"kind": "expert|recall|default_rule", "reason": "..."}
}

硬规则:
0. 先判断 source_profile，再提取 source_facts，再判断用户输入更像专家能力、给 AI 查阅的资料还是默认规则；若 requested_kind 明显不合适，直接用更合适的 kind 输出，并在 suggested_alternative 只推荐一个最合适类型和原因；判断后的类型和 requested_kind 一致时必须省略 suggested_alternative。
1. 外部 system/provider/persona prompt 原文不能变成 default_rule。
2. default_rule 不能包含模型身份、供应商身份、外部工具协议或权限扩大描述。
3. recall_body 存放资料正文；资料正文不得进入默认规则。
4. when_to_use 必须具体，不能写“所有任务”。
5. 不要输出或决定 prompt_key；保存主键只能由后端根据类型、标题、topic 和 draft_key 生成。
6. hit_examples 和 miss_examples 必须各至少 1 条，且必须是普通用户能理解的自然语言场景。
7. default_rule 必须检查现有项目默认规则摘要；如可能冲突，写入 conflicting_rules，并建议改成 expert 或 recall 的替代落点。
8. 输出格式必须具体说明，不能只写“整理结果”或“回答用户”。
9. 专家能力如果涉及保存、记忆、知识沉淀、长期复用或写入知识库，必须写 save_boundary，说明只输出建议保存条目，除非系统提供明确保存工具或用户确认；不能声称已经保存。
10. 不要用多草稿表达类型选择。用户选择类型不合适时，只输出一个最合适类型的草稿；多草稿只允许用于多个同类型独立资产。
11. suggested_alternative.reason 必须写给普通用户看，不要出现 requested_kind、inferred_kind、default_rule、recall、expert 等底层字段名；直接说明“更适合做专家能力/参考资料/默认规则”的原因和下一步。
12. source_profile 和 source_facts 只用于内部质检，不要照搬到摘要；外部身份、外部平台、外部工具协议必须用 disposition=drop 或 translate 标明。
13. source_facts 必须覆盖原文中会影响 AI 使用的关键事实：外部 prompt 覆盖身份/平台/安全/工具协议；价格表或表格覆盖主题、字段、关键行、单位、适用范围、可查询问题；API 文档覆盖端点、方法、参数、鉴权、返回、错误、限制、示例；流程 SOP 覆盖触发条件、输入、步骤、角色、例外、输出；会议纪要覆盖事实、决策、待办、负责人、日期、未决问题；业务规则覆盖规则、条件、例外、执行方式、示例。生成内容必须逐项吸收 preserve/translate 的要点，不能只写泛化摘要。
14. 面向普通用户写自然中文，避免错别字和生硬翻译；外部 prompt 中的 pair programming 应转写为“协作编程”或“编程协作”，不要写成“结对编程”或“结队编程”。

requested_kind: %s
cwd: %s
existing_project_default_rules:
%s

user_input:
%s`, kind, cwd, strings.Join(existingRules, "\n---\n"), rawInput), nil
}

// parsePromptIntentCard 解析单个卡片 JSON。
func parsePromptIntentCard(raw string) (Card, error) {
	var card Card
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &card); err != nil {
		return Card{}, err
	}
	return card, nil
}

// parsePromptIntentCards 支持解析单对象和 {"drafts":[...]} 两种格式，统一返回卡片切片。
func parsePromptIntentCards(raw string) ([]Card, error) {
	trimmed := strings.TrimSpace(raw)
	var set struct {
		Drafts []Card `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(trimmed), &set); err == nil && len(set.Drafts) > 0 {
		return set.Drafts, nil
	}
	card, err := parsePromptIntentCard(trimmed)
	if err != nil {
		return nil, err
	}
	return []Card{card}, nil
}

// promptIntentDraftIssues 汇总所有安全、质量和示例校验问题，以及 default_rule 冲突提示。
func promptIntentDraftIssues(kind Kind, rawInput string, card Card) []Issue {
	issues := SafetyIssues(kind, rawInput, card)
	issues = append(issues, promptIntentQualityIssues(kind, rawInput, card)...)
	if len(trimmedPromptIntentExamples(card.HitExamples)) == 0 {
		issues = append(issues, Issue{Code: "missing_hit_examples", Severity: "block", Message: "hit_examples must include at least one scenario"})
	}
	if len(trimmedPromptIntentExamples(card.MissExamples)) == 0 {
		issues = append(issues, Issue{Code: "missing_miss_examples", Severity: "block", Message: "miss_examples must include at least one scenario"})
	}
	if kind == KindDefaultRule && len(card.ConflictingRules) > 0 {
		issues = append(issues, Issue{Code: "default_rule_conflict", Severity: "review", Message: "和已有默认规则可能重复或冲突，保存前需要确认"})
	}
	return issues
}

// promptIntentQualityIssues 检查卡片的字段完整性、when_to_use 具体性、output 明确性和 save_boundary 必要性。
func promptIntentQualityIssues(kind Kind, rawInput string, card Card) []Issue {
	issues := promptIntentRequiredFieldIssues(kind, card)
	if kind == KindExpert {
		if strings.TrimSpace(card.WhenToUse) != "" && promptIntentVagueWhenToUse(card.WhenToUse) {
			issues = append(issues, Issue{
				Code:     "vague_when_to_use",
				Severity: "block",
				Message:  "AI 什么时候会使用它必须具体到任务场景，不能写“需要时使用”这类泛化描述。",
			})
		}
		if strings.TrimSpace(card.Output) != "" && promptIntentVagueOutput(card.Output) {
			issues = append(issues, Issue{
				Code:     "vague_output",
				Severity: "block",
				Message:  "输出格式必须具体说明要产出哪些栏目或结构，不能只写“整理结果”。",
			})
		}
		if promptIntentNeedsSaveBoundary(rawInput, card) && strings.TrimSpace(card.SaveBoundary) == "" {
			issues = append(issues, Issue{
				Code:     "missing_save_boundary",
				Severity: "block",
				Message:  "涉及保存、记忆或知识沉淀的专家能力必须说明保存边界：没有明确保存工具或用户确认时，只能输出建议保存的结构化条目，不能声称已经保存。",
			})
		}
	}
	issues = append(issues, promptIntentSourceFactIssues(kind, rawInput, card)...)
	return issues
}

// promptIntentRequiredFieldIssues 检查各 kind 必填字段是否存在，缺失则返回 block 问题。
func promptIntentRequiredFieldIssues(kind Kind, card Card) []Issue {
	issues := make([]Issue, 0, 6)
	issues = appendPromptIntentMissingIssue(issues, "title", "标题不能为空。", card.Title)
	issues = appendPromptIntentMissingIssue(issues, "summary", "摘要不能为空。", card.Summary)
	switch kind {
	case KindExpert:
		issues = appendPromptIntentMissingIssue(issues, "when_to_use", "AI 什么时候会使用它不能为空。", card.WhenToUse)
		issues = appendPromptIntentMissingIssue(issues, "when_not_to_use", "哪些问题不用它不能为空。", card.WhenNotToUse)
		issues = appendPromptIntentMissingIssue(issues, "workflow", "专家能力必须包含具体执行步骤。", strings.Join(card.Workflow, "\n"))
		issues = appendPromptIntentMissingIssue(issues, "output", "专家能力必须包含具体输出格式。", card.Output)
	case KindRecall:
		issues = appendPromptIntentMissingIssue(issues, "recall_topic", "资料必须有可检索主题。", card.RecallTopic)
		issues = appendPromptIntentMissingIssue(issues, "recall_body", "资料正文不能为空。", card.RecallBody)
	case KindDefaultRule:
		issues = appendPromptIntentMissingIssue(issues, "default_rule_body", "默认规则正文不能为空。", card.DefaultRuleBody)
	}
	return issues
}

// appendPromptIntentMissingIssue 在字段为空时追加 block 级问题。
// 调用方传入已本地化的 message，便于不同 kind 使用更贴近用户的错误说明。
func appendPromptIntentMissingIssue(issues []Issue, field, message, value string) []Issue {
	if strings.TrimSpace(value) != "" {
		return issues
	}
	return append(issues, Issue{
		Code:     "missing_" + field,
		Severity: "block",
		Message:  message,
	})
}

// promptIntentVagueWhenToUse 检查 when_to_use 是否仍是泛化占位文本。
// 仅匹配明确的禁用词，避免误伤用户写出的具体场景说明。
func promptIntentVagueWhenToUse(value string) bool {
	text := normalizePromptIntentComparableText(value)
	return promptIntentEqualsAnyTerm(text, promptIntentVagueWhenToUseTerms())
}

// promptIntentVagueOutput 判断 output 是否短到无法指导模型产出结构化结果。
// 该校验会结合长度和禁用词，防止“整理结果”这类低信息输出说明进入 ready 状态。
func promptIntentVagueOutput(value string) bool {
	text := normalizePromptIntentText(value)
	return compactRuneLen(text) < 6 ||
		promptIntentEqualsAnyTerm(text, promptIntentVagueOutputTerms()) ||
		(compactRuneLen(text) <= 16 && containsAnyPromptIntentTerm(text, promptIntentVagueOutputTerms()))
}

// promptIntentNeedsSaveBoundary 判断草稿是否涉及保存、记忆或知识沉淀。
// 命中时 expert 卡片必须说明保存边界，避免模型把建议误写成已持久化承诺。
func promptIntentNeedsSaveBoundary(rawInput string, card Card) bool {
	text := normalizePromptIntentText(strings.Join([]string{
		rawInput,
		card.Summary,
		card.WhenToUse,
		strings.Join(card.Workflow, "\n"),
		strings.Join(card.Constraints, "\n"),
		card.Output,
		strings.Join(card.HitExamples, "\n"),
	}, "\n"))
	return containsAnyPromptIntentTerm(text, promptIntentSaveBoundaryTerms())
}

// promptIntentSaveBoundaryTerms 返回触发保存边界说明的中英文关键词集合。
func promptIntentSaveBoundaryTerms() []string {
	return []string{
		"保存",
		"记忆",
		"沉淀",
		"长期复用",
		"知识库",
		"持久化",
		"save",
		"saved",
		"saving",
		"remember",
		"save to memory",
		"store in memory",
		"project memory",
		"persist",
		"knowledge base",
	}
}

// promptIntentVagueWhenToUseTerms 返回 when_to_use 中不可接受的泛化占位短语。
func promptIntentVagueWhenToUseTerms() []string {
	return []string{
		"需要时使用",
		"需要时",
		"适用时使用",
		"有需要时",
		"所有任务",
		"任何任务",
		"use when needed",
		"when needed",
		"as needed",
		"all tasks",
		"any request",
	}
}

// promptIntentVagueOutputTerms 返回 output 中不可接受的低信息占位短语。
func promptIntentVagueOutputTerms() []string {
	return []string{
		"整理结果",
		"回答用户",
		"输出结果",
		"处理结果",
		"结果",
		"answer",
		"response",
		"output result",
	}
}

// promptIntentEqualsAnyTerm 按规范化后的可比较文本做等值匹配。
// 末尾标点和空白不参与比较，避免中英文句号导致漏检。
func promptIntentEqualsAnyTerm(text string, terms []string) bool {
	for _, term := range terms {
		if text == normalizePromptIntentComparableText(term) {
			return true
		}
	}
	return false
}

// normalizePromptIntentComparableText 生成用于低信息短语等值比较的文本。
func normalizePromptIntentComparableText(text string) string {
	return strings.Trim(normalizePromptIntentText(text), "。.!！ ")
}

// promptIntentDryRunCard 从 draft_key 加载草稿卡片，或直接解析请求中的 card 字段。
func promptIntentDryRunCard(ctx context.Context, promptStore Store, p DryRunParams) (Card, error) {
	if draftKey := strings.TrimSpace(p.DraftKey); draftKey != "" {
		if promptStore == nil {
			return Card{}, errors.New("prompt store is required for prompt intent dry-run")
		}
		cwd, err := requireCWD(p.Cwd)
		if err != nil {
			return Card{}, err
		}
		draft, err := promptStore.GetIntentDraft(ctx, cwd, draftKey)
		if err != nil {
			return Card{}, err
		}
		return parsePromptIntentCard(string(draft.GeneratedCard))
	}
	if len(p.Card) == 0 {
		return Card{}, platformrpc.ErrInvalidParams("prompt intent card is required")
	}
	return parsePromptIntentCard(string(p.Card))
}

// normalizeKind 解析并校验 kind 字符串，不合法时返回 invalid_params 错误。
func normalizeKind(raw string) (Kind, error) {
	switch kind := Kind(strings.TrimSpace(raw)); kind {
	case KindExpert, KindRecall, KindDefaultRule:
		return kind, nil
	default:
		return "", platformrpc.ErrInvalidParams("prompt intent kind must be expert, recall, or default_rule")
	}
}

// promptIntentOriginHash 对原始输入做 SHA-256，用于识别同批次草稿。
func promptIntentOriginHash(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

// promptIntentHasBlockIssue 判断问题列表中是否含有 block 级别问题。
func promptIntentHasBlockIssue(issues []Issue) bool {
	for _, issue := range issues {
		if strings.TrimSpace(issue.Severity) == "block" {
			return true
		}
	}
	return false
}

// trimmedPromptIntentExamples 过滤并去除列表中的空字符串。
func trimmedPromptIntentExamples(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// nonEmptyStrings 过滤并返回非空字符串切片。
func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
