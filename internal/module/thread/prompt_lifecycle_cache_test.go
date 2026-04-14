package thread

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestLifecycleInvalidationClearsCache(t *testing.T) {
	t.Parallel()

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

	firstStart, err := promptAssembly.AssembleStart(context.Background(), promptpkg.StartInput{})
	if err != nil {
		t.Fatalf("first AssembleStart() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("dynamic provider calls before resume = %d, want 1", calls)
	}
	if !strings.Contains(firstStart.BaseInstructions, "memory build #1") {
		t.Fatalf("first BaseInstructions missing cached section:\n%s", firstStart.BaseInstructions)
	}

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-resume",
		AgentID:   "agent-resume",
		Prompt:    "resume name",
		Model:     "gpt-5.4",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-resume",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-resume",
		CodexThreadID:    "thread-resume",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		session := &stubSession{threadID: req.ProviderThreadID}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewServiceWithPromptAssembly(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil, promptAssembly, nil, nil).(*service)

	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-resume"}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}

	secondStart, err := promptAssembly.AssembleStart(context.Background(), promptpkg.StartInput{})
	if err != nil {
		t.Fatalf("second AssembleStart() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("dynamic provider calls after resume = %d, want 2", calls)
	}
	if !strings.Contains(secondStart.BaseInstructions, "memory build #2") {
		t.Fatalf("second BaseInstructions missing rebuilt section:\n%s", secondStart.BaseInstructions)
	}
	if secondStart.BaseInstructions == firstStart.BaseInstructions {
		t.Fatalf("BaseInstructions reused cached content after resume:\n%s", secondStart.BaseInstructions)
	}
	if secondStart.Snapshot.Generation == firstStart.Snapshot.Generation {
		t.Fatalf("prompt snapshot generation = %d before and after resume, want invalidated cache generation", secondStart.Snapshot.Generation)
	}
}
