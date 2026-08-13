package thread

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
)

// resumeDegradeOrchestration 是降级测试专用的最小 orchestration stub。
// LaunchAgent 必须成功，恢复流程才能走到 provider RPC 层并复现历史丢失。
type resumeDegradeOrchestration struct{}

func (resumeDegradeOrchestration) LaunchAgent(context.Context, LaunchAgentRequest) error { return nil }
func (resumeDegradeOrchestration) StopAgent(context.Context, string) error               { return nil }
func (resumeDegradeOrchestration) Recover(context.Context, string) error                 { return nil }

// resumeDegradeCodexBinding 构造合法 Codex binding fixture。
// 本地 sessions 目录为空（历史丢失），codex 允许 artifact optional missing，
// 因此 resume 仍会走到 provider RPC，由 onResume 返回 rollout 丢失错误。
func resumeDegradeCodexBinding(t *testing.T) *stubBindingStore {
	t.Helper()
	codexHome, err := contract.CanonicalizeCodexHome(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize test codex home: %v", err)
	}
	return &stubBindingStore{binding: &BindingRecord{
		AgentID:            "agent-1",
		Provider:           "codex",
		ProviderThreadID:   "11111111-2222-3333-4444-555555555597",
		CodexThreadID:      "thread-1",
		Cwd:                "/repo",
		CodexHome:          codexHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	}}
}

func TestIsUnrecoverableResumeError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "remote no rollout", err: errors.New("rpc error: code = -32600 desc = no rollout found for thread id 11111111-2222-3333-4444-555555555597"), want: true},
		{name: "local rollout not found", err: errors.New("codexapp: rollout not found for thread-1"), want: true},
		{name: "persisted history not found", err: errors.New("persisted thread history not found: lstat rollout-1.jsonl"), want: true},
		{name: "transient network", err: errors.New("rpc error: code = -32000 desc = connection refused"), want: false},
		{name: "authentication", err: errors.New("rpc error: code = -32001 desc = authentication failed"), want: false},
		{name: "unrelated error", err: errors.New("thread: cwd is required"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUnrecoverableResumeError(tt.err); got != tt.want {
				t.Fatalf("isUnrecoverableResumeError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestBackgroundResumeDegradesOnLostHistory(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-1",
		AgentID:        "agent-1",
		Prompt:         "resume",
		Model:          "gpt-5.5",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	bindings := resumeDegradeCodexBinding(t)
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, _ dto.ResumeSessionRequest) (contract.Session, error) {
			return nil, errors.New("rpc error: code = -32600 desc = no rollout found for thread id 11111111-2222-3333-4444-555555555597")
		},
	}
	var stopped []threaddto.Stopped
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, resumeDegradeOrchestration{}, nil).(*service)
	svc.emitStopped = func(evt threaddto.Stopped) { stopped = append(stopped, evt) }

	svc.backgroundResumeIfNeeded(context.Background(), "thread-1")
	waitForDegradeCondition(t, func() bool { return len(bindings.deleteAgentIDs) > 0 })

	if len(bindings.deleteAgentIDs) != 1 || bindings.deleteAgentIDs[0] != "agent-1" {
		t.Fatalf("binding delete agents = %v, want [agent-1]", bindings.deleteAgentIDs)
	}
	if threads.status.Status != statusFailed {
		t.Fatalf("thread status = %q, want %q", threads.status.Status, statusFailed)
	}
	if len(stopped) != 1 {
		t.Fatalf("stopped events = %d, want 1", len(stopped))
	}
	if got := stopped[0]; got.ThreadID != "thread-1" || got.Status != statusFailed || !strings.Contains(got.Reason, "start a new session") {
		t.Fatalf("stopped event = %#v, want failed with restart hint", got)
	}
	if _, ok := svc.resumeInFlight.Load("agent-1"); ok {
		t.Fatal("resumeInFlight retained after degrade")
	}

	resumeReqCh := make(chan dto.ResumeSessionRequest, 1)
	starter.onResume = func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
		resumeReqCh <- req
		return nil, nil
	}
	svc.backgroundResumeIfNeeded(context.Background(), "thread-1")
	assertNoResumeStarted(t, resumeReqCh)
}

func TestBackgroundResumeKeepsStateOnTransientError(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-1",
		AgentID:        "agent-1",
		Prompt:         "resume",
		Model:          "gpt-5.5",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	bindings := resumeDegradeCodexBinding(t)
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, _ dto.ResumeSessionRequest) (contract.Session, error) {
			return nil, errors.New("rpc error: code = -32000 desc = connection refused")
		},
	}
	resumeCalled := make(chan struct{}, 1)
	starter.onResume = func(_ context.Context, _ dto.ResumeSessionRequest) (contract.Session, error) {
		resumeCalled <- struct{}{}
		return nil, errors.New("rpc error: code = -32000 desc = connection refused")
	}
	var stopped []threaddto.Stopped
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, resumeDegradeOrchestration{}, nil).(*service)
	svc.emitStopped = func(evt threaddto.Stopped) { stopped = append(stopped, evt) }

	svc.backgroundResumeIfNeeded(context.Background(), "thread-1")
	select {
	case <-resumeCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("resume was not attempted for transient failure")
	}
	waitForDegradeCondition(t, func() bool {
		_, loaded := svc.resumeInFlight.Load("agent-1")
		return loaded
	})

	if len(bindings.deleteAgentIDs) != 0 {
		t.Fatalf("binding deleted on transient error: %v", bindings.deleteAgentIDs)
	}
	if threads.status.Status != "" {
		t.Fatalf("thread status updated on transient error: %q", threads.status.Status)
	}
	if len(stopped) != 0 {
		t.Fatalf("stopped events on transient error: %d", len(stopped))
	}
	if _, ok := svc.resumeInFlight.Load("agent-1"); !ok {
		t.Fatal("resumeInFlight cleared on transient error, want retained")
	}
}

func TestExplicitResumeDegradesOnLostHistory(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-1",
		AgentID:        "agent-1",
		Prompt:         "resume",
		Model:          "gpt-5.5",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	bindings := resumeDegradeCodexBinding(t)
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, _ dto.ResumeSessionRequest) (contract.Session, error) {
			return nil, errors.New("codexapp: rollout not found for thread-1")
		},
	}
	var stopped []threaddto.Stopped
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, resumeDegradeOrchestration{}, nil).(*service)
	svc.emitStopped = func(evt threaddto.Stopped) { stopped = append(stopped, evt) }

	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"})
	if err == nil {
		t.Fatal("Resume() error = nil, want lost history error")
	}
	if !strings.Contains(err.Error(), "start a new session") {
		t.Fatalf("Resume() error = %q, want restart hint", err)
	}
	if !strings.Contains(err.Error(), "rollout not found") {
		t.Fatalf("Resume() error = %q, want original provider error retained", err)
	}
	if len(bindings.deleteAgentIDs) != 1 || bindings.deleteAgentIDs[0] != "agent-1" {
		t.Fatalf("binding delete agents = %v, want [agent-1]", bindings.deleteAgentIDs)
	}
	if threads.status.Status != statusFailed {
		t.Fatalf("thread status = %q, want %q", threads.status.Status, statusFailed)
	}
	if len(stopped) != 1 {
		t.Fatalf("stopped events = %d, want 1", len(stopped))
	}
}

func TestExplicitResumeDoesNotPublishStoppedWhenDegradeCleanupFails(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-1",
		AgentID:        "agent-1",
		Prompt:         "resume",
		Model:          "gpt-5.5",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	bindings := resumeDegradeCodexBinding(t)
	bindings.deleteErr = errors.New("delete binding failed")
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, _ dto.ResumeSessionRequest) (contract.Session, error) {
			return nil, errors.New("codexapp: rollout not found for thread-1")
		},
	}
	var stopped []threaddto.Stopped
	svc := NewService(silentLogger(), threads, bindings, &stubSessionProvider{}, starter, nil, resumeDegradeOrchestration{}, nil).(*service)
	svc.emitStopped = func(evt threaddto.Stopped) { stopped = append(stopped, evt) }

	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"})
	if err == nil || !strings.Contains(err.Error(), "delete binding failed") {
		t.Fatalf("Resume() error = %v, want cleanup failure", err)
	}
	if threads.status.Status != "" {
		t.Fatalf("thread status = %q, want unchanged when binding cleanup fails", threads.status.Status)
	}
	if len(stopped) != 0 {
		t.Fatalf("stopped events = %d, want 0 when cleanup fails", len(stopped))
	}
}

func waitForDegradeCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
