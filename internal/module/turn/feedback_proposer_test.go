package turn

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockDreamExecutor struct {
	result string
	err    error
	calls  int
}

func (m *mockDreamExecutor) ExecuteDream(_ context.Context, _ string) (string, error) {
	m.calls++
	return m.result, m.err
}

func TestFeedbackProposer_Propose(t *testing.T) {
	dream := &mockDreamExecutor{
		result: "---\nname: reply-chinese\ndescription: Use when collaborating with this user\n---\n# 中文回复规则\n用中文回复",
	}

	proposer := NewFeedbackProposer(dream, struct{}{}, nil)

	feedbacks := []FeedbackItem{
		{Content: "用中文回复"},
		{Content: "面向用户正文用中文"},
		{Content: "commit message 也用中文"},
	}

	err := proposer.Propose(context.Background(), "reply-chinese", feedbacks, "abcd1234abcd1234abcd1234abcd1234")
	require.NoError(t, err)

	assert.Equal(t, 0, dream.calls)
}

func TestBuildFeedbackProposalPrompt(t *testing.T) {
	prompt := buildFeedbackProposalPrompt("test-topic", []string{"feedback1", "feedback2"})
	assert.Contains(t, prompt, "反馈 1")
	assert.Contains(t, prompt, "反馈 2")
	assert.Contains(t, prompt, "feedback1")
	assert.Contains(t, prompt, "SKILL.md")
}
