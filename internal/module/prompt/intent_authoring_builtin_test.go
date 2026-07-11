package prompt

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	promptintent "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt/intent"
	"github.com/stretchr/testify/require"
)

func TestPromptIntentDraftDetectsBuiltinRegistryDuplicate(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	builtin := &fakeBuiltinPromptRegistry{
		templates: []contract.BuiltinPromptTemplate{
			{
				ID:          -100001,
				PromptKey:   "main/general-zh",
				Kind:        "base",
				Title:       "General Assistant",
				AgentKey:    "main",
				Description: "Repository-aware coding assistant behavior.",
				PromptText:  "Use repository instructions and run focused verification before reporting completion.",
				Enabled:     true,
				Scope:       "global",
				Tags:        []string{"builtin:system"},
			},
		},
	}
	dream := &fakePromptIntentDream{output: `{
		"kind":"expert",
		"title":"General Assistant",
		"summary":"Repository-aware coding assistant behavior.",
		"when_to_use":"Use for repository-aware coding tasks.",
		"when_not_to_use":"Do not use for unrelated personal notes.",
		"workflow":["Read repository instructions","Run focused verification before reporting completion"],
		"constraints":["Do not skip verification"],
		"output":"Concise engineering answer with verification evidence.",
		"hit_examples":["修复一个 Go 测试失败"],
		"miss_examples":["记录晚饭吃什么"]
	}`}

	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, builtin, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "Create an expert for repository-aware coding tasks and focused verification.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, err := json.Marshal(got)
	require.NoError(t, err)
	var result struct {
		Status string               `json:"status"`
		Issues []promptintent.Issue `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, "draft", result.Status)
	requirePromptIntentIssue(t, result.Issues, "builtin_prompt_duplicate", "block")
}
