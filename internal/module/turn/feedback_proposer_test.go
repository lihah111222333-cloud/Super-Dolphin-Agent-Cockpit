package turn

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/skillcandidate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockDreamExecutor struct {
	result string
	err    error
}

func (m *mockDreamExecutor) ExecuteDream(_ context.Context, _ string) (string, error) {
	return m.result, m.err
}

type mockCandidateStore struct {
	mu          sync.Mutex
	inserted    []skillcandidate.InsertParams
	insertCount int
}

func (m *mockCandidateStore) Insert(_ context.Context, p skillcandidate.InsertParams) (skillcandidate.Candidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertCount++
	m.inserted = append(m.inserted, p)
	return skillcandidate.Candidate{ID: int64(m.insertCount), Status: skillcandidate.StatusPendingReview}, nil
}

func (m *mockCandidateStore) GetByID(context.Context, int64) (skillcandidate.Candidate, error) {
	return skillcandidate.Candidate{}, nil
}
func (m *mockCandidateStore) ListPending(context.Context, string, int32, int32) ([]skillcandidate.Candidate, error) {
	return nil, nil
}
func (m *mockCandidateStore) MarkSuperseded(context.Context, string, string, string, int64) (int64, error) {
	return 0, nil
}
func (m *mockCandidateStore) Approve(_ context.Context, _ int64, _, _ string, _ time.Time) (skillcandidate.Candidate, error) {
	return skillcandidate.Candidate{}, nil
}
func (m *mockCandidateStore) Reject(context.Context, int64, string) (skillcandidate.Candidate, error) {
	return skillcandidate.Candidate{}, nil
}
func (m *mockCandidateStore) MarkPromoted(context.Context, int64) (skillcandidate.Candidate, error) {
	return skillcandidate.Candidate{}, nil
}
func (m *mockCandidateStore) LookupApproval(context.Context, string, string, string, string) (*skillcandidate.Candidate, error) {
	return nil, nil
}

func TestFeedbackProposer_Propose(t *testing.T) {
	dream := &mockDreamExecutor{
		result: "---\nname: reply-chinese\ndescription: Use when collaborating with this user\n---\n# 中文回复规则\n用中文回复",
	}
	store := &mockCandidateStore{}

	proposer := NewFeedbackProposer(dream, store, nil)

	feedbacks := []FeedbackItem{
		{Content: "用中文回复"},
		{Content: "面向用户正文用中文"},
		{Content: "commit message 也用中文"},
	}

	err := proposer.Propose(context.Background(), "reply-chinese", feedbacks, "abcd1234abcd1234abcd1234abcd1234")
	require.NoError(t, err)

	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, 1, store.insertCount)
	assert.Equal(t, "reply-chinese", store.inserted[0].Slug)
	assert.Equal(t, skillcandidate.ScopeProject, store.inserted[0].Scope)
	assert.NotEmpty(t, store.inserted[0].SkillMD)
	assert.NotEmpty(t, store.inserted[0].ContentHash)
}

func TestBuildFeedbackProposalPrompt(t *testing.T) {
	prompt := buildFeedbackProposalPrompt("test-topic", []string{"feedback1", "feedback2"})
	assert.Contains(t, prompt, "反馈 1")
	assert.Contains(t, prompt, "反馈 2")
	assert.Contains(t, prompt, "feedback1")
	assert.Contains(t, prompt, "SKILL.md")
}
