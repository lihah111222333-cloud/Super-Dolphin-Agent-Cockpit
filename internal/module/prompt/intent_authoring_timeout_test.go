package prompt

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	promptintent "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/stretchr/testify/require"
)

func TestPromptIntentDraftRetriesInvalidModelShapeWithinDreamTimeout(t *testing.T) {
	t.Parallel()

	cardJSON, err := json.Marshal(readyExpertIntentCard())
	require.NoError(t, err)
	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{outputs: []string{`not-json`, string(cardJSON)}}
	got, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "Create an expert for sqlc review with generated-code drift checks.",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	raw, err := json.Marshal(got)
	require.NoError(t, err)
	var result struct {
		DraftKey string `json:"draft_key"`
		Status   string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.NotEmpty(t, result.DraftKey)
	require.Equal(t, "ready_to_save", result.Status)
	require.Len(t, dream.prompts, 2)
	require.True(t, dream.hasDeadline, "dream executor context has no deadline")
	remaining := time.Until(dream.deadline)
	require.Positive(t, remaining)
	require.Greater(t, remaining, platformconfig.RPCRequestTimeout)
	require.LessOrEqual(t, remaining, platformconfig.PromptIntentDraftTimeout)
}

func TestPromptIntentDraftRepairUsesDreamTimeoutBudget(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{outputs: []string{
		`{
			"kind":"expert",
			"title":"酒精冲动阻断",
			"summary":"在用户想喝酒时提醒停下。",
			"when_to_use":"需要时使用。",
			"workflow":["提醒用户不要喝酒"],
			"output":"整理结果。",
			"hit_examples":["我想喝酒"],
			"miss_examples":["生成代码"]
		}`,
		`{
			"kind":"expert",
			"title":"酒后提醒",
			"summary":"阻止用户在想喝酒时继续喝酒。",
			"when_to_use":"当用户说想喝酒、正在喝酒或需要戒酒提醒时使用。",
			"when_not_to_use":"不要用于普通饮食建议或医疗诊断。",
			"workflow":["识别用户喝酒意图","提醒停止喝酒","建议转移注意力或联系可信任的人"],
			"constraints":["不提供医疗诊断"],
			"output":"明确阻止喝酒，并给出安全替代行动。",
			"hit_examples":["我现在想喝酒"],
			"miss_examples":["推荐一杯咖啡"]
		}`,
	}}
	_, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:     "expert",
		RawInput: "在我想喝酒的时候阻止我",
		Cwd:      "/repo/a",
	})
	require.NoError(t, err)

	require.Len(t, dream.prompts, 2)
	require.Len(t, dream.deadlines, 2)
	remaining := time.Until(dream.deadlines[1])
	require.Positive(t, remaining)
	require.Greater(t, remaining, platformconfig.RPCRequestTimeout)
	require.LessOrEqual(t, remaining, platformconfig.PromptIntentDraftTimeout)
}
