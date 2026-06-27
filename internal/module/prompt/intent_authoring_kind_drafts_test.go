package prompt

import (
	"context"
	"encoding/json"
	"testing"

	promptintent "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
	"github.com/stretchr/testify/require"
)

func TestPromptIntentDraftSeparatesRequestedAndInferredKind(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"SQLC Reviewer",
		"summary":"Review sqlc query and generated-code drift.",
		"when_to_use":"Use when reviewing sqlc query changes.",
		"when_not_to_use":"Do not use for storing reference material.",
		"workflow":["Read query SQL"],
		"output":"Findings.",
		"hit_examples":["Review a sqlc query migration"],
		"miss_examples":["Store project facts"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "Use this sqlc review workflow for generated-code drift checks.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		DraftKey      string               `json:"draft_key"`
		RequestedKind string               `json:"requested_kind"`
		InferredKind  string               `json:"inferred_kind"`
		Status        string               `json:"status"`
		Confidence    float64              `json:"confidence"`
		Issues        []promptintent.Issue `json:"issues"`
		Card          promptintent.Card    `json:"card"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "recall", result.RequestedKind)
	require.Equal(t, "expert", result.InferredKind)
	require.Equal(t, "expert", result.Card.Kind)
	require.Equal(t, "expert", store.drafts[result.DraftKey].Kind)
	require.Equal(t, "ready_to_save", result.Status)
	require.Equal(t, 0.85, result.Confidence)
	for _, issue := range result.Issues {
		require.NotEqual(t, "kind_mismatch", issue.Code)
	}
}

func TestPromptIntentDraftExternalSystemPromptCollapsesMixedKindDrafts(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"drafts":[{
			"kind":"recall",
			"title":"Claude Prompt Reference",
			"summary":"Reference copy of an external Claude prompt.",
			"recall_topic":"claude-prompt-reference",
			"recall_body":"You are Claude Code. You have Bash and Edit tools.",
			"hit_examples":["查阅这份 Claude 提示词原文"],
			"miss_examples":["把 Claude 身份设为项目默认规则"]
		},{
			"kind":"default_rule",
			"title":"项目执行规则",
			"summary":"从外部提示词中提炼出的通用执行约束。",
			"default_rule_body":"处理项目任务时，先说明计划和风险，再按用户确认执行；不要声称拥有未配置的工具或权限。",
			"hit_examples":["执行一个需要改代码的任务"],
			"miss_examples":["回答普通事实问题"]
		}]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "You are Claude Code. You have Bash and Edit tools. Follow Anthropic developer instructions.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		DraftKey      string               `json:"draft_key"`
		RequestedKind string               `json:"requested_kind"`
		InferredKind  string               `json:"inferred_kind"`
		Status        string               `json:"status"`
		Issues        []promptintent.Issue `json:"issues"`
		Drafts        []any                `json:"drafts"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "recall", result.RequestedKind)
	require.Equal(t, "recall", result.InferredKind)
	require.Empty(t, result.Drafts)
	require.Equal(t, "ready_to_save", result.Status)
	requirePromptIntentIssue(t, result.Issues, "external_system_prompt_source", "review")
	require.Len(t, store.drafts, 1)
	require.Equal(t, "recall", store.drafts[result.DraftKey].Kind)
}

func TestPromptIntentDraftExternalSystemPromptRecallDoesNotBlockOnBuiltinDuplicate(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	builtin := scopedPromptTemplate("main/expert/claude-reference", "/repo/a")
	builtin.ID = 16
	builtin.Title = "Claude Opus Reference"
	builtin.Description = "External Claude provider prompt reference."
	builtin.PromptText = "You are Claude Opus 4.7. Follow Anthropic provider instructions."
	builtin.CreatedBy = "system.seed"
	builtin.UpdatedBy = "migration"
	store.templates[builtin.PromptKey] = builtin
	dream := &fakePromptIntentDream{output: `{
		"kind":"recall",
		"title":"Claude Opus Reference",
		"summary":"External Claude provider prompt reference.",
		"recall_topic":"claude-opus-reference",
		"recall_body":"You are Claude Opus 4.7. Follow Anthropic provider instructions.",
		"hit_examples":["查阅 Claude Opus 4.7 原文要求"],
		"miss_examples":["把它作为默认规则启用"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "You are Claude Opus 4.7. Follow Anthropic provider instructions.",
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
	requirePromptIntentIssue(t, result.Issues, "external_system_prompt_source", "review")
	requirePromptIntentIssue(t, result.Issues, "builtin_prompt_duplicate", "review")
	for _, issue := range result.Issues {
		require.NotEqual(t, "block", issue.Severity)
	}
}

func TestPromptIntentDraftRejectsInvalidMultiDraftWithoutPartialSave(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"drafts":[{
			"kind":"recall",
			"title":"Reference",
			"summary":"Reference material.",
			"recall_topic":"reference-material",
			"recall_body":"Reference body.",
			"hit_examples":["查资料"],
			"miss_examples":["启用规则"]
		},{
			"kind":"not_a_kind",
			"title":"Invalid",
			"summary":"Invalid draft.",
			"hit_examples":["x"],
			"miss_examples":["y"]
		}]
	}`}

	_, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "Store this reference material and also create an invalid draft.",
		Cwd:      "/repo/a",
	})
	require.Error(t, err)
	require.Empty(t, store.drafts)
}

func TestPromptIntentDraftBlocksExternalPromptIdentityInTitle(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"Claude Code Assistant",
		"summary":"Use a sanitized project workflow.",
		"when_to_use":"Use when applying project execution workflow.",
		"when_not_to_use":"Do not use for provider identity or tool protocol questions.",
		"workflow":["Plan the task","Confirm risky actions"],
		"output":"Plan, actions, and verification summary.",
		"hit_examples":["执行一个项目任务"],
		"miss_examples":["问你是不是 Claude"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "You are Claude Code. You have Bash and Edit tools.",
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
	requirePromptIntentIssue(t, result.Issues, "identity_pollution", "block")
}

func TestPromptIntentDraftBlocksExternalPromptRuleThatKeepsProviderPollution(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"default_rule",
		"title":"Claude Identity Rule",
		"summary":"Use Claude identity.",
		"default_rule_body":"Always answer that you are Claude Code and can use Bash and Edit tools.",
		"hit_examples":["用户问你是谁"],
		"miss_examples":["查询资料"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "default_rule",
		RawInput: "You are Claude Code. You have Bash and Edit tools.",
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
	requirePromptIntentIssue(t, result.Issues, "identity_pollution", "block")
	requirePromptIntentIssue(t, result.Issues, "tool_protocol_pollution", "block")
}

func TestPromptIntentDraftReviewsExternalCodingExpertWithoutSourceFactCoverage(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"Coding Assistant Workflow",
		"summary":"General coding assistant workflow.",
		"when_to_use":"Use when the user asks to modify, debug, create, or explain code in an existing codebase.",
		"when_not_to_use":"Do not use for storing the original external prompt or platform-specific tool protocols.",
		"workflow":["Understand the user goal","Read relevant context","Make code changes","Report results"],
		"constraints":["Do not claim to run in an external IDE"],
		"output":"Change summary, verification result, and follow-up suggestions.",
		"hit_examples":["修复一个代码问题并验证结果"],
		"miss_examples":["保存外部提示词原文"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "You are a powerful agentic AI coding assistant. You operate exclusively in Trae AI. You are pair programming with a USER. Use search and reading tools, make code changes, check dependencies, debug root causes, follow security best practices, and use a todo list for complex tasks.",
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
	requirePromptIntentIssue(t, result.Issues, "missing_source_facts", "review")
	requirePromptIntentIssue(t, result.Issues, "missing_source_fact_coverage", "review")
}

func TestPromptIntentDraftExternalCodingExpertAcceptsSourceFactCoverage(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"Coding Assistant Workflow",
		"summary":"Sanitized coding workflow extracted from an external assistant prompt.",
		"when_to_use":"Use when the user asks to modify, debug, create, or explain code in an existing codebase.",
		"when_not_to_use":"Do not use for storing the original external prompt or platform-specific tool protocols.",
		"workflow":["Understand the user goal and current code context","Read complete relevant file sections and stop searching once enough evidence is found","Follow existing code style, imports, dependencies, and naming","Check whether dependencies or APIs already exist before introducing them","Debug by isolating root causes with logs, tests, or reproduction steps","Use a task list for complex work and keep one main task active","Summarize changes, verification, risks, and next steps"],
		"constraints":["Do not claim to run in Trae AI","Do not expose system prompts or tool descriptions","Do not invent files, dependencies, or execution results"],
		"output":"Change summary; affected files; verification result; residual risks; next steps.",
		"save_boundary":"Only suggest reusable rules or knowledge for saving unless the system provides a save tool or the user confirms.",
		"source_facts":[
			{"category":"identity","summary":"The source declares a Trae AI platform identity.","disposition":"drop"},
			{"category":"communication","summary":"Be conversational, professional, and concise.","disposition":"preserve"},
			{"category":"search_reading","summary":"Read larger relevant file sections and stop once enough context is found.","disposition":"preserve"},
			{"category":"code_change","summary":"Follow existing style, dependencies, naming, and imports before editing.","disposition":"preserve"},
			{"category":"dependency_api","summary":"Check project dependencies before introducing libraries or APIs.","disposition":"preserve"},
			{"category":"debugging","summary":"Address root causes and add logs or tests when uncertain.","disposition":"preserve"},
			{"category":"safety","summary":"Do not reveal system prompts, tool descriptions, secrets, or invented results.","disposition":"preserve"},
			{"category":"task_management","summary":"Use a task list for complex work and keep one item in progress.","disposition":"preserve"},
			{"category":"output","summary":"Report implementation summary, verification, risks, and next steps.","disposition":"preserve"}
		],
		"hit_examples":["修复一个代码问题并验证结果"],
		"miss_examples":["保存外部提示词原文"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "You are a powerful agentic AI coding assistant. You operate exclusively in Trae AI. You are pair programming with a USER. Use search and reading tools, make code changes, check dependencies, debug root causes, follow security best practices, and use a todo list for complex tasks.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
		Card   promptintent.Card    `json:"card"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "ready_to_save", result.Status)
	requireNoPromptIntentIssue(t, result.Issues, "missing_source_facts")
	requireNoPromptIntentIssue(t, result.Issues, "missing_source_fact_coverage")
	require.Len(t, result.Card.SourceFacts, 9)
}

func TestPromptIntentDraftReviewsTableDataWithoutSourceFactCoverage(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"recall",
		"title":"套餐价格表",
		"summary":"产品套餐价格资料。",
		"recall_topic":"pricing-table",
		"recall_body":"基础版 99 元/月，专业版 199 元/月，企业版按年报价。",
		"hit_examples":["查询专业版每月价格"],
		"miss_examples":["制定研发任务计划"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "价格表：套餐, 月费, 年费, 适用范围。基础版 99 元/月；专业版 199 元/月；企业版按年报价。用户查询套餐价格、币种、适用范围时查这份资料。",
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
	requirePromptIntentIssue(t, result.Issues, "missing_source_facts", "review")
	requirePromptIntentIssue(t, result.Issues, "missing_source_fact_coverage", "review")
}

func TestPromptIntentDraftTableDataAcceptsSourceFactCoverage(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"recall",
		"title":"套餐价格表",
		"summary":"产品套餐价格资料。",
		"recall_topic":"pricing-table",
		"recall_body":"主题：产品套餐价格。字段：套餐、月费、年费、适用范围。关键行：基础版 99 元/月，专业版 199 元/月，企业版按年报价。单位：人民币每月或按年报价。适用范围：只回答套餐价格、币种和适用范围问题。可查询：专业版价格、企业版报价方式。",
		"source_profile":"table_data",
		"source_facts":[
			{"category":"topic","summary":"资料主题是产品套餐价格。","disposition":"preserve"},
			{"category":"fields","summary":"字段包括套餐、月费、年费和适用范围。","disposition":"preserve"},
			{"category":"key_rows","summary":"基础版、专业版、企业版是关键行。","disposition":"preserve"},
			{"category":"units","summary":"价格单位是人民币每月或按年报价。","disposition":"preserve"},
			{"category":"scope","summary":"只回答套餐价格、币种和适用范围问题。","disposition":"preserve"},
			{"category":"query_examples","summary":"用户可查询专业版价格或企业版报价方式。","disposition":"preserve"}
		],
		"hit_examples":["查询专业版每月价格"],
		"miss_examples":["制定研发任务计划"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "价格表：套餐, 月费, 年费, 适用范围。基础版 99 元/月；专业版 199 元/月；企业版按年报价。用户查询套餐价格、币种、适用范围时查这份资料。",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
		Card   promptintent.Card    `json:"card"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "ready_to_save", result.Status)
	requireNoPromptIntentIssue(t, result.Issues, "missing_source_facts")
	requireNoPromptIntentIssue(t, result.Issues, "missing_source_fact_coverage")
	require.Equal(t, "table_data", result.Card.SourceProfile)
}

func TestPromptIntentDraftBlocksSourceFactsNotAppliedToSavedContent(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"recall",
		"title":"套餐价格表",
		"summary":"产品套餐价格资料。",
		"recall_topic":"pricing-table",
		"recall_body":"产品套餐价格资料。",
		"source_profile":"table_data",
		"source_facts":[
			{"category":"topic","summary":"资料主题是产品套餐价格。","disposition":"preserve"},
			{"category":"fields","summary":"字段包括套餐、月费、年费和适用范围。","disposition":"preserve"},
			{"category":"key_rows","summary":"基础版 99 元/月，专业版 199 元/月，企业版按年报价。","disposition":"preserve"},
			{"category":"units","summary":"价格单位是人民币每月或按年报价。","disposition":"preserve"},
			{"category":"scope","summary":"只回答套餐价格、币种和适用范围问题。","disposition":"preserve"},
			{"category":"query_examples","summary":"用户可查询专业版价格或企业版报价方式。","disposition":"preserve"}
		],
		"hit_examples":["查询专业版每月价格"],
		"miss_examples":["制定研发任务计划"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "价格表：套餐, 月费, 年费, 适用范围。基础版 99 元/月；专业版 199 元/月；企业版按年报价。用户查询套餐价格、币种、适用范围时查这份资料。",
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
	requirePromptIntentIssue(t, result.Issues, "source_fact_not_applied", "block")
}

func TestPromptIntentDraftReviewsAPIDocWithoutSourceFactCoverage(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"recall",
		"title":"订单 API 文档",
		"summary":"订单创建 API 参考。",
		"recall_topic":"order-api",
		"recall_body":"POST /v1/orders creates an order.",
		"hit_examples":["查询创建订单接口参数"],
		"miss_examples":["整理会议纪要"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "API 文档：POST /v1/orders。Authorization Bearer token。参数 amount、currency、customer_id。返回 id、status。错误码 400、401、429。Rate limit: 60 requests/minute。",
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
	requirePromptIntentIssue(t, result.Issues, "missing_source_facts", "review")
	requirePromptIntentIssue(t, result.Issues, "missing_source_fact_coverage", "review")
}

func TestPromptIntentDraftAPIDocAcceptsSourceFactCoverage(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"recall",
		"title":"订单 API 文档",
		"summary":"订单创建 API 参考。",
		"recall_topic":"order-api",
		"recall_body":"接口地址：POST /v1/orders。请求方法：POST。鉴权：Authorization Bearer token。请求参数：amount、currency、customer_id。返回结构：id、status。错误码：400、401、429。调用限制：60 requests/minute。调用示例：curl -X POST /v1/orders。",
		"source_profile":"api_doc",
		"source_facts":[
			{"category":"endpoint","summary":"接口地址是 /v1/orders。","disposition":"preserve"},
			{"category":"method","summary":"请求方法是 POST。","disposition":"preserve"},
			{"category":"parameters","summary":"参数包括 amount、currency、customer_id。","disposition":"preserve"},
			{"category":"auth","summary":"使用 Authorization Bearer token 鉴权。","disposition":"preserve"},
			{"category":"response","summary":"返回 id 和 status。","disposition":"preserve"},
			{"category":"errors","summary":"错误码包括 400、401、429。","disposition":"preserve"},
			{"category":"limits","summary":"调用限制是 60 requests/minute。","disposition":"preserve"},
			{"category":"examples","summary":"调用示例使用 curl POST /v1/orders。","disposition":"preserve"}
		],
		"hit_examples":["查询创建订单接口参数"],
		"miss_examples":["整理会议纪要"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "API 文档：POST /v1/orders。Authorization Bearer token。参数 amount、currency、customer_id。返回 id、status。错误码 400、401、429。Rate limit: 60 requests/minute。Example: curl -X POST /v1/orders。",
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
	requireNoPromptIntentIssue(t, result.Issues, "missing_source_facts")
	requireNoPromptIntentIssue(t, result.Issues, "missing_source_fact_coverage")
	requireNoPromptIntentIssue(t, result.Issues, "source_fact_not_applied")
}

func TestPromptIntentDraftRawProfileOverridesIncorrectModelProfile(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"Coding Assistant Workflow",
		"summary":"General coding assistant workflow.",
		"when_to_use":"Use when the user asks to modify, debug, create, or explain code in an existing codebase.",
		"when_not_to_use":"Do not use for storing the original external prompt or platform-specific tool protocols.",
		"workflow":["Understand the user goal","Read relevant context","Make code changes","Report results"],
		"constraints":["Do not claim to run in an external IDE"],
		"output":"Change summary, verification result, and follow-up suggestions.",
		"source_profile":"reference_doc",
		"source_facts":[
			{"category":"topic","summary":"A coding workflow reference.","disposition":"preserve"},
			{"category":"key_points","summary":"Read context and edit code.","disposition":"preserve"},
			{"category":"usage","summary":"Use for coding tasks.","disposition":"preserve"}
		],
		"hit_examples":["修复一个代码问题并验证结果"],
		"miss_examples":["保存外部提示词原文"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "You are a powerful agentic AI coding assistant. You operate exclusively in Trae AI. Use search and reading tools, make code changes, check dependencies, debug root causes, follow security best practices, and use a todo list for complex tasks.",
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
	requirePromptIntentIssue(t, result.Issues, "missing_source_fact_coverage", "review")
}

func TestPromptIntentDraftOrdinaryAPIWorkflowDoesNotRequireSourceFacts(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"API Review Workflow",
		"summary":"Review API integration work before implementation.",
		"when_to_use":"Use when reviewing a planned API integration workflow for this project.",
		"when_not_to_use":"Do not use for storing API reference documents.",
		"workflow":["Clarify the endpoint purpose","Check existing client patterns","Review error handling and tests"],
		"output":"Review notes, risks, and verification checklist.",
		"hit_examples":["帮我审一下这个 API 接入方案"],
		"miss_examples":["保存一份接口文档"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "Create an expert for API review workflow before implementation.",
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
	requireNoPromptIntentIssue(t, result.Issues, "missing_source_facts")
	requireNoPromptIntentIssue(t, result.Issues, "missing_source_fact_coverage")
}

func TestPromptIntentDraftWorkflowSOPAcceptsSourceFactCoverage(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"采购审批流程助手",
		"summary":"按采购 SOP 检查申请材料和审批步骤。",
		"when_to_use":"Use when the user asks to process or check a purchase approval request.",
		"when_not_to_use":"Do not use for API lookup or price-table questions.",
		"workflow":["确认采购金额是否超过 5000 元触发审批流程","检查申请单、预算编号、供应商报价和负责人是否齐全","按申请人提交、部门负责人审批、财务复核、采购执行的步骤判断","区分申请人、部门负责人、财务和采购的角色职责","标记紧急采购和缺少预算编号这两类例外"],
		"output":"输出审批路径、缺失材料、例外说明和下一步动作。",
		"source_profile":"workflow_sop",
		"source_facts":[
			{"category":"trigger","summary":"采购金额超过 5000 元时触发审批流程。","disposition":"preserve"},
			{"category":"inputs","summary":"需要申请单、预算编号、供应商报价和负责人。","disposition":"preserve"},
			{"category":"steps","summary":"申请人提交、部门负责人审批、财务复核、采购执行。","disposition":"preserve"},
			{"category":"roles","summary":"申请人、部门负责人、财务和采购分别负责不同环节。","disposition":"preserve"},
			{"category":"exceptions","summary":"紧急采购和缺少预算编号需要单独标记。","disposition":"preserve"},
			{"category":"outputs","summary":"输出审批路径、缺失材料和下一步动作。","disposition":"preserve"}
		],
		"hit_examples":["检查这笔采购申请下一步走哪个审批"],
		"miss_examples":["查询订单接口错误码"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "采购 SOP：金额超过 5000 元触发审批。输入包括申请单、预算编号、供应商报价和负责人。步骤：申请人提交、部门负责人审批、财务复核、采购执行。紧急采购和缺预算编号是例外。输出审批路径和缺失材料。",
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
	requireNoPromptIntentIssue(t, result.Issues, "missing_source_facts")
	requireNoPromptIntentIssue(t, result.Issues, "missing_source_fact_coverage")
}
