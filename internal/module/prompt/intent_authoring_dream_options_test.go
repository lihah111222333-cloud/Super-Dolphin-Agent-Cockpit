package prompt

import (
	"context"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	promptintent "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt/intent"
	"github.com/stretchr/testify/require"
)

type promptIntentOptionsDream struct {
	output      string
	prompts     []string
	options     []contract.DreamOptions
	hasDeadline bool
	deadline    time.Time
}

func (f *promptIntentOptionsDream) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	return f.ExecuteDreamWithOptions(ctx, prompt, contract.DreamOptions{})
}

func (f *promptIntentOptionsDream) ExecuteDreamWithOptions(ctx context.Context, prompt string, options contract.DreamOptions) (string, error) {
	f.prompts = append(f.prompts, prompt)
	f.options = append(f.options, options)
	if deadline, ok := ctx.Deadline(); ok {
		f.hasDeadline = true
		f.deadline = deadline
	}
	return f.output, nil
}

func TestPromptIntentDraftPassesRequestedDreamOptions(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	dream := &promptIntentOptionsDream{output: `{
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
	}`}

	_, err := promptintent.HandleDraft(context.Background(), promptIntentStoreForTest(store), dream, nil, promptintent.DraftParams{
		Kind:          "expert",
		RawInput:      "在我想喝酒的时候阻止我",
		Cwd:           "/repo/a",
		Provider:      "codex",
		Model:         "gpt-5.5",
		ModelProvider: "openrouter",
	})
	require.NoError(t, err)
	require.Len(t, dream.options, 1)
	require.Equal(t, contract.DreamOptions{Provider: "codex", Model: "gpt-5.5", ModelProvider: "openrouter"}, dream.options[0])
}
