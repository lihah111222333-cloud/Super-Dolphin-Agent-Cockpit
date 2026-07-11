package thread

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	promptpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt"
)

func TestWorktreeResumeInvalidationClearsCache(t *testing.T) {
	t.Parallel()

	_, worktreeCWD := newPromptGitFixture(t)
	promptAssembly, calls := newCountingMemoryPromptAssembly(t)

	firstStart := assemblePromptStart(t, promptAssembly, "first")
	assertMemoryPromptBuild(t, *calls, firstStart.BaseInstructions, 1, "before resume")

	svc := newResumeInvalidationService(t, worktreeCWD, promptAssembly)
	resumeThreadForPromptInvalidation(t, svc)

	secondStart := assemblePromptStart(t, promptAssembly, "second")
	assertMemoryPromptBuild(t, *calls, secondStart.BaseInstructions, 2, "after resume")
	if secondStart.BaseInstructions == firstStart.BaseInstructions {
		t.Fatalf("BaseInstructions reused cached content after resume:\n%s", secondStart.BaseInstructions)
	}
	if secondStart.Snapshot.Generation == firstStart.Snapshot.Generation {
		t.Fatalf("prompt snapshot generation = %d before and after resume, want invalidated cache generation", secondStart.Snapshot.Generation)
	}
}

func newCountingMemoryPromptAssembly(t *testing.T) (promptpkg.Service, *int) {
	t.Helper()
	promptAssembly := promptpkg.NewService(&promptpkg.Config{}, nil)
	calls := 0
	if err := promptAssembly.RegisterDynamicProvider(promptpkg.DynamicTextProvider{
		Name: promptpkg.DynamicSectionMemory,
		ResolveFunc: func(context.Context, promptpkg.SectionContext) (*string, error) {
			calls++
			text := fmt.Sprintf("memory build #%d", calls)
			return &text, nil
		},
	}); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}
	return promptAssembly, &calls
}

func assemblePromptStart(t *testing.T, promptAssembly promptpkg.Service, label string) promptpkg.StartAssembly {
	t.Helper()
	out, err := promptAssembly.AssembleStart(context.Background(), promptpkg.StartInput{})
	if err != nil {
		t.Fatalf("%s AssembleStart() error = %v", label, err)
	}
	return out
}

func assertMemoryPromptBuild(t *testing.T, gotCalls int, instructions string, wantCalls int, phase string) {
	t.Helper()
	if gotCalls != wantCalls {
		t.Fatalf("dynamic provider calls %s = %d, want %d", phase, gotCalls, wantCalls)
	}
	wantText := fmt.Sprintf("memory build #%d", wantCalls)
	if !strings.Contains(instructions, wantText) {
		t.Fatalf("BaseInstructions %s missing %s:\n%s", phase, wantText, instructions)
	}
}

func newResumeInvalidationService(t *testing.T, worktreeCWD string, promptAssembly promptpkg.Service) *service {
	t.Helper()
	const providerThreadID = "019d5f6b-fb3c-7760-9d6f-54005553f705"
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-resume",
		AgentID:        "agent-resume",
		Prompt:         "resume name",
		Model:          "gpt-5.5",
		Cwd:            worktreeCWD,
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:       "agent-resume",
		Provider:      "codex",
		CodexThreadID: "thread-resume",
		RolloutPath:   writeExistingProviderHistoryFile(t),
		SessionUUID:   providerThreadID,
		Cwd:           worktreeCWD,
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		session := &stubSession{threadID: req.ProviderThreadID}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	return NewServiceWithPromptAssembly(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil, promptAssembly, nil, nil).(*service)
}

func resumeThreadForPromptInvalidation(t *testing.T, svc *service) {
	t.Helper()
	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-resume"}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
}
