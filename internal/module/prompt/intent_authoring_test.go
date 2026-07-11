package prompt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	promptintent "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt/intent"
	"github.com/stretchr/testify/require"
)

type fakePromptIntentDream struct {
	output      string
	outputs     []string
	err         error
	prompts     []string
	hasDeadline bool
	deadline    time.Time
	deadlines   []time.Time
}

func (f *fakePromptIntentDream) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	if deadline, ok := ctx.Deadline(); ok {
		f.hasDeadline = true
		f.deadline = deadline
		f.deadlines = append(f.deadlines, deadline)
	}
	if f.err != nil {
		return "", f.err
	}
	if len(f.outputs) > 0 {
		index := len(f.prompts) - 1
		if index >= len(f.outputs) {
			index = len(f.outputs) - 1
		}
		return f.outputs[index], nil
	}
	return f.output, nil
}

func TestPromptIntentDraftExpertReadyToSave(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"SQLC Reviewer",
		"summary":"Review sqlc query and generated-code drift.",
		"when_to_use":"Use when reviewing sqlc query changes.",
		"when_not_to_use":"Do not use for frontend-only work.",
		"workflow":["Read query SQL","Compare generated code"],
		"constraints":["Do not edit generated code first"],
		"output":"Findings with file references.",
		"hit_examples":["Review a sqlc query migration"],
		"miss_examples":["Polish a CSS button"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "Create an expert for sqlc review with generated-code drift checks.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, err := json.Marshal(got)
	require.NoError(t, err)
	var result struct {
		DraftKey   string               `json:"draft_key"`
		Status     string               `json:"status"`
		Confidence float64              `json:"confidence"`
		Issues     []promptintent.Issue `json:"issues"`
		Card       promptintent.Card    `json:"card"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.NotEmpty(t, result.DraftKey)
	require.Equal(t, "ready_to_save", result.Status)
	require.Equal(t, 0.85, result.Confidence)
	require.Empty(t, result.Issues)
	require.Equal(t, "expert", result.Card.Kind)
	require.Contains(t, dream.prompts[0], "严格输出 JSON")
	require.Contains(t, store.drafts, result.DraftKey)
	require.Equal(t, "ready_to_save", store.drafts[result.DraftKey].Status)
}

func TestPromptIntentDraftPromptRequiresTypedQualitySchema(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"Conversation Knowledge Curator",
		"summary":"Extract reusable knowledge from daily conversations.",
		"when_to_use":"Use when the user asks to summarize a day or conversation and extract reusable knowledge.",
		"when_not_to_use":"Do not use for direct bug fixes, translation, fact lookup, or continuing the current task.",
		"workflow":["Read the conversation","Classify reusable facts, decisions, preferences, and todos","Mark uncertain items for confirmation"],
		"constraints":["Do not claim knowledge was saved unless a save tool is available or the user confirms"],
		"output":"Daily summary; suggested knowledge items; content not worth saving; open questions; next actions.",
		"save_boundary":"Only output suggested knowledge items unless an explicit save tool is available or the user confirms saving.",
		"hit_examples":["整理今天的对话，提取值得保存的知识"],
		"miss_examples":["继续修这个函数的 bug"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "我希望你帮我整理每日对话，并提取有用的知识保存下来",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)
	require.Len(t, dream.prompts, 1)
	require.Contains(t, dream.prompts[0], `"save_boundary"`)
	require.Contains(t, dream.prompts[0], `"source_profile"`)
	require.Contains(t, dream.prompts[0], `"source_facts"`)
	require.Contains(t, dream.prompts[0], "examples")
	require.Contains(t, dream.prompts[0], "先判断 source_profile，再提取 source_facts")
	require.Contains(t, dream.prompts[0], "再判断用户输入更像专家能力、给 AI 查阅的资料还是默认规则")
	require.Contains(t, dream.prompts[0], "只推荐一个最合适类型")
	require.Contains(t, dream.prompts[0], "判断后的类型和 requested_kind 一致时必须省略 suggested_alternative")
	require.Contains(t, dream.prompts[0], "pair programming")
	require.Contains(t, dream.prompts[0], "协作编程")
	require.Contains(t, dream.prompts[0], "输出格式必须具体说明")
	require.Contains(t, dream.prompts[0], "不能声称已经保存")

	raw, err := json.Marshal(got)
	require.NoError(t, err)
	var result struct {
		DraftKey   string               `json:"draft_key"`
		Status     string               `json:"status"`
		Confidence float64              `json:"confidence"`
		Issues     []promptintent.Issue `json:"issues"`
		Card       promptintent.Card    `json:"card"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "ready_to_save", result.Status)
	require.Equal(t, 0.85, result.Confidence)
	require.Empty(t, result.Issues)
	require.NotEmpty(t, result.Card.SaveBoundary)
	require.Equal(t, "ready_to_save", store.drafts[result.DraftKey].Status)
	require.JSONEq(t, string(store.drafts[result.DraftKey].GeneratedCard), string(mustJSONForPromptIntentTest(t, result.Card)))
}

func TestPromptIntentDraftBlocksSaveIntentWithoutSaveBoundary(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"每日对话整理与知识沉淀助手",
		"summary":"整理当天对话内容，提取可复用信息、决策、经验和后续事项。",
		"when_to_use":"当用户希望回顾一段或一天的对话，整理重要信息、结论、经验、待办事项、偏好或可复用知识时使用。",
		"when_not_to_use":"当用户只是想继续当前对话、执行一次具体操作、查询事实答案，或只需要原文转写时不要使用。",
		"workflow":["通读对话内容，区分事实、观点、决策、待办、偏好和临时闲聊","提取对未来有用的信息，过滤一次性、重复、过期或没有复用价值的内容","将可保存的信息改写为清晰、简短、可独立理解的知识条目"],
		"constraints":["发现不确定、互相矛盾或缺少上下文的信息时，先列为待确认，不要强行保存为确定事实。"],
		"output":"输出每日对话摘要、可保存知识条目、无需保存内容、待确认问题和后续行动建议。",
		"hit_examples":["帮我总结今天和你聊过的内容，看看哪些经验可以保存下来"],
		"miss_examples":["继续帮我修改这个函数的 bug"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "我希望你帮我整理每日对话，并提取有用的知识保存下来",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "draft", result.Status)
	requirePromptIntentIssue(t, result.Issues, "missing_save_boundary", "block")
}

func TestPromptIntentDraftBlocksExpertWithoutConcreteUsageAndOutput(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"对话整理助手",
		"summary":"帮助整理对话。",
		"when_to_use":"需要时使用。",
		"workflow":["整理内容"],
		"output":"整理结果",
		"hit_examples":["整理今天的对话"],
		"miss_examples":["查询价格表"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "我希望你帮我整理每日对话，并提取有用的信息。",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "draft", result.Status)
	requirePromptIntentIssue(t, result.Issues, "vague_when_to_use", "block")
	requirePromptIntentIssue(t, result.Issues, "missing_when_not_to_use", "block")
	requirePromptIntentIssue(t, result.Issues, "vague_output", "block")
}

func TestPromptIntentDraftBlocksRecallWithoutBody(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"recall",
		"title":"价格表资料",
		"summary":"产品价格表。",
		"recall_topic":"pricing-table",
		"hit_examples":["查询套餐价格"],
		"miss_examples":["写日报"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "把这份价格表给 AI 查询：基础版 99 元，专业版 199 元。",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "draft", result.Status)
	requirePromptIntentIssue(t, result.Issues, "missing_recall_body", "block")
}

func TestPromptIntentDraftBlocksDefaultRuleWithoutRuleBody(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"default_rule",
		"title":"数据库修改规则",
		"summary":"数据库修改前要说明影响。",
		"hit_examples":["修改 migration"],
		"miss_examples":["整理会议纪要"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "default_rule",
		RawInput: "以后修改数据库前，先说明影响范围。",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "draft", result.Status)
	requirePromptIntentIssue(t, result.Issues, "missing_default_rule_body", "block")
}

func TestPromptIntentDraftRequiresDreamExecutor(t *testing.T) {
	t.Parallel()

	_, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(newInMemoryPromptStore()), nil, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "Create a focused expert.",
		Cwd:      "/repo/a",
	})
	require.ErrorIs(t, err, contract.ErrDreamExecutorNotConfigured)
}

func TestPromptIntentDraftRequiresHitAndMissExamples(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"SQLC Reviewer",
		"summary":"Review sqlc query and generated-code drift.",
		"when_to_use":"Use when reviewing sqlc query changes.",
		"workflow":["Read query SQL"],
		"output":"Findings.",
		"hit_examples":[],
		"miss_examples":["Frontend CSS changes"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "Create an expert for sqlc review with generated-code drift checks.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "draft", result.Status)
	requirePromptIntentIssue(t, result.Issues, "missing_hit_examples", "block")
}

func TestPromptIntentDraftExternalSystemPromptRecallCanBeReadyWithReviewIssue(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"recall",
		"title":"External Provider Notes",
		"summary":"Reference notes from an external provider prompt.",
		"recall_topic":"external-provider-notes",
		"recall_body":"You are Claude and must use provider-specific commands.",
		"hit_examples":["Look up external provider prompt notes"],
		"miss_examples":["Enable a project default behavior"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "You are Claude and should remember these provider prompt notes.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status     string               `json:"status"`
		Confidence float64              `json:"confidence"`
		Issues     []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "ready_to_save", result.Status)
	require.Equal(t, 0.85, result.Confidence)
	requirePromptIntentIssue(t, result.Issues, "external_system_prompt_source", "review")
}

func TestPromptIntentDraftBlocksBuiltinDuplicate(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	builtin := scopedPromptTemplate("main/general-zh", "/repo/a")
	builtin.ID = 11
	builtin.Title = "General Assistant"
	builtin.Description = "Repository-aware coding assistant behavior."
	builtin.PromptText = "Use repository instructions and run focused verification before reporting completion."
	builtin.Tags = json.RawMessage(`["scope.global"]`)
	builtin.CreatedBy = "system.seed"
	builtin.UpdatedBy = "migration"
	store.templates[builtin.PromptKey] = builtin
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"General Assistant",
		"summary":"Repository-aware coding assistant behavior.",
		"when_to_use":"Use repository instructions and run focused verification before reporting completion.",
		"when_not_to_use":"Do not use as a project-specific workflow.",
		"workflow":["Use repository instructions","Run focused verification"],
		"constraints":["Do not skip verification"],
		"output":"Concise completion report.",
		"hit_examples":["Ask the assistant to work in this repository"],
		"miss_examples":["Store a project-specific SQLC workflow"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "Use repository instructions and run focused verification before reporting completion.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "draft", result.Status)
	requirePromptIntentIssue(t, result.Issues, "builtin_prompt_duplicate", "block")
}

func TestPromptIntentDraftReviewsProjectDuplicate(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	existing := scopedPromptTemplate("main/expert/sqlc-reviewer", "/repo/a")
	existing.ID = 12
	existing.Title = "SQLC Reviewer"
	existing.Description = "Review sqlc query and generated-code drift."
	existing.WhenToUse = "Use when reviewing sqlc query changes."
	existing.CreatedBy = "rpc.prompts"
	existing.UpdatedBy = "rpc.prompts"
	store.templates[existing.PromptKey] = existing
	card := readyExpertIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	dream := &fakePromptIntentDream{output: string(cardJSON)}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "Create an expert for sqlc review with generated-code drift checks.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "ready_to_save", result.Status)
	requirePromptIntentIssue(t, result.Issues, "project_prompt_duplicate", "review")
}

func TestPromptIntentDraftBlocksDuplicateRecallTopic(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	existing := scopedPromptTemplate("main/knowledge/sqlc-workflow", "/repo/a")
	existing.ID = 13
	existing.Title = "SQLC Workflow"
	existing.CreatedBy = "rpc.prompts"
	existing.UpdatedBy = "rpc.prompts"
	store.templates[existing.PromptKey] = existing
	store.sections[existing.ID] = map[string]TemplateSection{
		"recall_sqlc_workflow": {
			TemplateID:  existing.ID,
			SectionKey:  "recall_sqlc_workflow",
			Enabled:     true,
			TriggerType: "recall",
			RecallTopic: "sqlc-workflow",
			Body:        "Read source SQL before generated code.",
		},
	}
	card := readyRecallIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	dream := &fakePromptIntentDream{output: string(cardJSON)}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "Remember this sqlc workflow as project knowledge.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "draft", result.Status)
	requirePromptIntentIssue(t, result.Issues, "duplicate_recall_topic", "block")
}

func TestPromptIntentDraftAllowsProjectRecallOverrideOfGlobalTopic(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	global := scopedPromptTemplate("main/knowledge/sqlc-global", "")
	global.ID = 14
	global.Title = "Global SQLC Knowledge"
	global.Tags = json.RawMessage(`["intent:recall","scope.global"]`)
	global.CreatedBy = "rpc.prompts"
	global.UpdatedBy = "rpc.prompts"
	store.templates[global.PromptKey] = global
	store.sections[global.ID] = map[string]TemplateSection{
		"recall_sqlc_workflow": {
			TemplateID:  global.ID,
			SectionKey:  "recall_sqlc_workflow",
			Enabled:     true,
			TriggerType: "recall",
			RecallTopic: "sqlc-workflow",
			Body:        "Global SQLC workflow guidance.",
		},
	}
	card := readyRecallIntentCard()
	card.Title = "Project SQLC Workflow"
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	dream := &fakePromptIntentDream{output: string(cardJSON)}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "Remember this project-specific sqlc workflow.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "ready_to_save", result.Status)
	for _, issue := range result.Issues {
		require.NotEqual(t, "duplicate_recall_topic", issue.Code)
	}
}

func TestPromptIntentDraftAllowsGlobalRecallFallbackWhenProjectTopicExists(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	project := scopedPromptTemplate("main/knowledge/sqlc-project", "/repo/a")
	project.ID = 15
	project.Title = "Project SQLC Knowledge"
	project.Tags = json.RawMessage(`["intent:recall","scope.cwd:/repo/a"]`)
	project.CreatedBy = "rpc.prompts"
	project.UpdatedBy = "rpc.prompts"
	store.templates[project.PromptKey] = project
	store.sections[project.ID] = map[string]TemplateSection{
		"recall_sqlc_workflow": {
			TemplateID:  project.ID,
			SectionKey:  "recall_sqlc_workflow",
			Enabled:     true,
			TriggerType: "recall",
			RecallTopic: "sqlc-workflow",
			Body:        "Project SQLC workflow guidance.",
		},
	}
	card := readyRecallIntentCard()
	card.Title = "Global SQLC Workflow"
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	dream := &fakePromptIntentDream{output: string(cardJSON)}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:         "recall",
		RawInput:     "Remember this global sqlc workflow.",
		Cwd:          "/repo/a",
		EnableGlobal: true,
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "ready_to_save", result.Status)
	for _, issue := range result.Issues {
		require.NotEqual(t, "duplicate_recall_topic", issue.Code)
	}
}

func TestPromptIntentDryRunRecallExplainsPromptRecall(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	card := readyRecallIntentCard()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store.drafts["intent/recall/1"] = promptIntentDraftForTest("intent/recall/1", "/repo/a", "recall", "ready_to_save", cardJSON, nil)

	got, err := promptintent.HandleDryRun(context.Background(), promptIntentStoreForTest(store), nil, nil, promptintent.DryRunParams{
		DraftKey: "intent/recall/1",
		Cwd:      "/repo/a",
		Question: "Where do I find sqlc workflow guidance?",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result promptintent.DryRunResult
	require.NoError(t, json.Unmarshal(raw, &result))
	require.True(t, result.WouldUse)
	require.Equal(t, "prompt_recall", result.Action)
	require.Equal(t, "sqlc-workflow", result.Target)
	require.Equal(t, []string{"问题可能需要查阅这份资料。"}, result.Reasons)
	require.Equal(t, promptintent.DryRunDisclaimer, result.Disclaimer)
	require.Zero(t, store.upsertCalls)
	require.Zero(t, store.txCalls)
	require.Equal(t, "ready_to_save", store.drafts["intent/recall/1"].Status)
}

func TestPromptIntentDryRunRequiresQuestion(t *testing.T) {
	t.Parallel()

	_, err := promptintent.HandleDryRun(context.Background(), promptIntentStoreForTest(newInMemoryPromptStore()), nil, nil, promptintent.DryRunParams{
		Card: json.RawMessage(`{"kind":"expert","title":"SQLC Reviewer"}`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "question is required")
}

func TestPromptIntentE2EHealthRequiresFixtureProvider(t *testing.T) {
	t.Parallel()

	dream := &fakePromptIntentDream{output: `{"provider":"e2e-fixture","fixture_path_hash":"abc123"}`}
	got, err := promptintent.HandleE2EHealth(context.Background(), dream, promptintent.E2EHealthParams{})
	require.NoError(t, err)
	require.Equal(t, promptintent.E2EHealthResult{Provider: "e2e-fixture", FixturePathHash: "abc123"}, got)
	require.Len(t, dream.prompts, 1)
}

func TestPromptIntentE2EHealthNotRegisteredWithoutFixtureEnv(t *testing.T) {
	t.Setenv("PROMPT_INTENT_E2E_DREAM_FIXTURE", "")
	handlers := buildPromptHandlersWithService(newPromptService(newInMemoryPromptStore()), newInMemoryPromptStore(), nil, &fakePromptIntentDream{}).Handlers
	require.Nil(t, handlers["prompt-intents/e2e-health"])
}

func readyRecallIntentCard() promptintent.Card {
	return promptintent.Card{
		Kind:         "recall",
		Title:        "SQLC Workflow",
		Summary:      "SQLC workflow guidance.",
		RecallTopic:  "sqlc-workflow",
		RecallBody:   "Read source SQL before generated code.",
		HitExamples:  []string{"Find sqlc workflow guidance"},
		MissExamples: []string{"Review CSS layout"},
	}
}

func promptIntentDraftForTest(draftKey, cwd, kind, status string, card json.RawMessage, issues []promptintent.Issue) IntentDraft {
	issuesJSON, _ := json.Marshal(issues)
	return IntentDraft{
		DraftKey:      draftKey,
		CWD:           cwd,
		Kind:          kind,
		RawInput:      "raw input for " + kind,
		GeneratedCard: card,
		Confidence:    0.85,
		Status:        status,
		Scope:         "project",
		Issues:        issuesJSON,
	}
}

func requirePromptIntentIssue(t *testing.T, issues []promptintent.Issue, code, severity string) {
	t.Helper()
	for _, issue := range issues {
		if strings.TrimSpace(issue.Code) == code && strings.TrimSpace(issue.Severity) == severity {
			return
		}
	}
	t.Fatalf("issues = %+v, want code=%s severity=%s", issues, code, severity)
}

func requireNoPromptIntentIssue(t *testing.T, issues []promptintent.Issue, code string) {
	t.Helper()
	for _, issue := range issues {
		require.NotEqualf(t, code, strings.TrimSpace(issue.Code), "unexpected issue: %+v", issue)
	}
}

func mustJSONForPromptIntentTest(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return raw
}
