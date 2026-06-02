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

func TestPromptIntentDraftRetriesInvalidModelShapeWithinInteractiveTimeout(t *testing.T) {
	t.Parallel()

	cardJSON, err := json.Marshal(readyExpertIntentCard())
	require.NoError(t, err)
	store := newInMemoryPromptStore()
	dream := &fakePromptIntentDream{outputs: []string{`not-json`, string(cardJSON)}}
	got, err := promptintent.HandleDraft(context.Background(), store, dream, nil, promptintent.DraftParams{
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
	require.LessOrEqual(t, remaining, platformconfig.RPCRequestTimeout)
}
