package prompt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPromptWriteRejectsBuiltinPromptKey(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	svc := newPromptServiceWithBuiltin(store, fakeBuiltinRegistryWithKeys("main/default"))

	_, err := svc.WritePrompt(context.Background(), "/repo/a", PromptWriteRequest{
		ID:           "main/default",
		Name:         "User Default",
		Content:      "user content",
		ContentSet:   true,
		WhenToUse:    "Use when user asks.",
		WhenToUseSet: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "builtin prompt")
	require.Contains(t, err.Error(), "read-only")
	require.Zero(t, store.upsertCalls)
}

func TestPromptMutationRejectsBuiltinPromptKeyForSectionsAndDelete(t *testing.T) {
	t.Parallel()

	store := newInMemoryPromptStore()
	svc := newPromptServiceWithBuiltin(store, fakeBuiltinRegistryWithKeys("main/default"))

	_, err := svc.WriteSection(context.Background(), "/repo/a", PromptSectionWriteRequest{
		PromptKey:   "main/default",
		SectionKey:  "identity",
		Region:      "static",
		Body:        "updated builtin",
		Enabled:     true,
		TriggerType: "always",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "builtin prompt")

	err = svc.DeleteSection(context.Background(), "/repo/a", "main/default", "identity")
	require.Error(t, err)
	require.Contains(t, err.Error(), "builtin prompt")

	err = svc.DeletePrompt(context.Background(), "/repo/a", "main/default")
	require.Error(t, err)
	require.Contains(t, err.Error(), "builtin prompt")
	require.Zero(t, store.deleteCalls)
}
