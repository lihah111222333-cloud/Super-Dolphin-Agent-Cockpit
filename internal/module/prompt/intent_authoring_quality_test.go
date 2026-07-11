package prompt

import (
	"context"
	"encoding/json"
	"testing"

	promptintent "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt/intent"
	"github.com/stretchr/testify/require"
)

func TestPromptIntentDraftAllowsConcreteMemoryLeakExpertWithoutSaveBoundary(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"Memory Leak Debugger",
		"summary":"Debug memory leak regressions in Go services.",
		"when_to_use":"Use when the user asks to diagnose heap growth, pprof output, or suspected memory leaks in a Go service.",
		"when_not_to_use":"Do not use for saving notes, extracting reusable knowledge, or unrelated feature work.",
		"workflow":["Collect heap profiles and allocation traces","Compare steady-state and regression profiles","Identify retaining references and propose verification steps"],
		"constraints":["Do not assume the leak source without profile evidence"],
		"output":"Root-cause hypotheses; profile evidence; verification commands; patch risks.",
		"hit_examples":["Debug this Go memory leak using pprof"],
		"miss_examples":["Save today’s conversation as knowledge"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "Create an expert to debug Go memory leak incidents with pprof evidence.",
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
	requireNoPromptIntentIssue(t, result.Issues, "missing_save_boundary")
}

func TestPromptIntentDraftBlocksKnowledgeBaseReuseWithoutSaveBoundary(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"知识库条目整理助手",
		"summary":"把每日对话整理成可长期复用的知识库条目。",
		"when_to_use":"当用户希望从一段对话中提取可长期复用的项目知识库条目时使用。",
		"when_not_to_use":"当用户只是继续当前任务或查询一次性事实时不要使用。",
		"workflow":["通读对话","提取可复用结论","整理为结构化条目"],
		"constraints":["不确定内容列为待确认"],
		"output":"知识库条目；待确认问题；不建议沉淀的内容。",
		"hit_examples":["把今天的对话整理成知识库条目"],
		"miss_examples":["继续修当前 bug"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "把每日对话整理成可长期复用的知识库条目",
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

func TestPromptIntentDraftNormalizesRecallTopicSlugBeforeReady(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"recall",
		"title":"SQLite Prompt Intent Draft Acceptance Token",
		"summary":"SQLite prompt intent acceptance token notes.",
		"recall_topic":"sqlite_prompt_intent_draft_acceptance_token",
		"recall_body":"The acceptance token must survive recall draft save.",
		"hit_examples":["Find the sqlite prompt intent acceptance token"],
		"miss_examples":["Review frontend spacing"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "recall",
		RawInput: "Add this SQLite prompt intent draft acceptance token for AI recall.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, _ := json.Marshal(got)
	var result struct {
		DraftKey string               `json:"draft_key"`
		Status   string               `json:"status"`
		Issues   []promptintent.Issue `json:"issues"`
		Card     promptintent.Card    `json:"card"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "ready_to_save", result.Status)
	require.Equal(t, "sqlite-prompt-intent-draft-acceptance-token", result.Card.RecallTopic)
	requireNoPromptIntentIssue(t, result.Issues, "missing_recall_topic")

	var stored promptintent.Card
	require.NoError(t, json.Unmarshal(store.drafts[result.DraftKey].GeneratedCard, &stored))
	require.Equal(t, "sqlite-prompt-intent-draft-acceptance-token", stored.RecallTopic)
}

func TestPromptIntentDraftBlocksVagueOutputContainingGenericResult(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"对话整理助手",
		"summary":"整理每日对话中的关键信息。",
		"when_to_use":"当用户要求回顾一段对话并整理关键信息、结论和待办时使用。",
		"when_not_to_use":"当用户只是继续当前任务、查询事实或要求翻译时不要使用。",
		"workflow":["通读对话","提取结论和待办","标注不确定事项"],
		"constraints":["不要把闲聊当成结论"],
		"output":"输出整理结果给用户",
		"hit_examples":["整理今天的对话"],
		"miss_examples":["查询套餐价格"]
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
	requirePromptIntentIssue(t, result.Issues, "vague_output", "block")
}
