package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeedbackTracker_RecordAndCount(t *testing.T) {
	tracker := NewFeedbackTracker(3)

	tracker.Record("reply-chinese", ExtractedMemory{
		Name:    "reply-in-chinese",
		Type:    MemoryTypeFeedback,
		Content: "用中文回复",
	})

	assert.Equal(t, 1, tracker.Count("reply-chinese"))
	assert.False(t, tracker.ThresholdReached("reply-chinese"))
}

func TestFeedbackTracker_ThresholdReached(t *testing.T) {
	tracker := NewFeedbackTracker(2)

	tracker.Record("git-rules", ExtractedMemory{Name: "git-rules-1", Type: MemoryTypeFeedback, Content: "commit前跑测试"})
	assert.False(t, tracker.ThresholdReached("git-rules"))

	tracker.Record("git-rules", ExtractedMemory{Name: "git-rules-2", Type: MemoryTypeFeedback, Content: "push前跑测试"})
	assert.True(t, tracker.ThresholdReached("git-rules"))

	group := tracker.GetGroup("git-rules")
	require.Len(t, group, 2)
	assert.Equal(t, "git-rules-1", group[0].Name)
	assert.Equal(t, "git-rules-2", group[1].Name)
}

func TestFeedbackTracker_CountNonExistent(t *testing.T) {
	tracker := NewFeedbackTracker(3)
	assert.Equal(t, 0, tracker.Count("nonexistent"))
	assert.False(t, tracker.ThresholdReached("nonexistent"))
	assert.Empty(t, tracker.GetGroup("nonexistent"))
}

func TestFeedbackTracker_MinThreshold(t *testing.T) {
	tracker := NewFeedbackTracker(0)
	assert.Equal(t, 2, tracker.threshold)
}

func TestFeedbackTopicSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"reply-in-chinese", "reply-in-chinese"},
		{"git commit/push 必须等用户要求", "git-commit-push-必须等用户要求"},
		{"", ""},
		{"simple", "simple"},
		{"a-b-c-d-e-f-g", "b-c-d-e"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := FeedbackTopicSlug(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFeedbackTracker_MarkProposed(t *testing.T) {
	tracker := NewFeedbackTracker(2)

	tracker.Record("topic-a", ExtractedMemory{Name: "a1", Type: MemoryTypeFeedback})
	tracker.Record("topic-a", ExtractedMemory{Name: "a2", Type: MemoryTypeFeedback})
	assert.True(t, tracker.ThresholdReached("topic-a"))

	tracker.MarkProposed("topic-a")
	assert.False(t, tracker.ThresholdReached("topic-a"))
	assert.Equal(t, 0, tracker.Count("topic-a"))
}
