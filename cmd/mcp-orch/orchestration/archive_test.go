package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/exitmonitor"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/kelindar/event"
)

func TestArchiveAgentMarksPersistedThreadAndBindingArchivedWhenRuntimeMissing(t *testing.T) {
	threads := &archiveAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "provider-thread-1", AgentID: "agent-1", Status: "created"},
	}}
	bindings := &archiveAgentBindingStore{bindings: map[string]PersistedBinding{
		"agent-1": {AgentID: "agent-1", Provider: "codex", CodexThreadID: "provider-thread-1"},
	}}
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agentThreads = threads
	svc.agentBindings = bindings

	if err := svc.ArchiveAgent(context.Background(), " agent-1 "); err != nil {
		t.Fatalf("ArchiveAgent() error = %v", err)
	}
	if threads.updated.ThreadID != "provider-thread-1" || threads.updated.Status != "archived" {
		t.Fatalf("thread status update = %#v, want provider-thread-1 archived", threads.updated)
	}
	if bindings.archived.AgentID != "agent-1" || !bindings.archived.Archived {
		t.Fatalf("binding archive update = %#v, want agent-1 archived", bindings.archived)
	}
}

func TestArchiveAgentAcceptsProviderThreadIDAndArchivesOwningAgent(t *testing.T) {
	threads := &archiveAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "provider-thread-1", AgentID: "agent-1", Status: "created"},
	}}
	bindings := &archiveAgentBindingStore{bindings: map[string]PersistedBinding{
		"agent-1": {AgentID: "agent-1", Provider: "codex", CodexThreadID: "provider-thread-1"},
	}}
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agentThreads = threads
	svc.agentBindings = bindings

	if err := svc.ArchiveAgent(context.Background(), "provider-thread-1"); err != nil {
		t.Fatalf("ArchiveAgent(provider thread) error = %v", err)
	}
	if threads.updated.ThreadID != "provider-thread-1" || threads.updated.Status != "archived" {
		t.Fatalf("thread status update = %#v, want provider-thread-1 archived", threads.updated)
	}
	if bindings.archived.AgentID != "agent-1" || !bindings.archived.Archived {
		t.Fatalf("binding archive update = %#v, want owning agent archived", bindings.archived)
	}
}

func TestArchiveAgentArchivesPersistedThreadViaSettledLauncherWhenRuntimeMissing(t *testing.T) {
	threads := &archiveAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "thread-1", AgentID: "agent-1", Status: "created"},
	}}
	bindings := &archiveAgentBindingStore{bindings: map[string]PersistedBinding{
		"agent-1": {AgentID: "agent-1", Provider: "claude", CodexThreadID: "thread-1"},
	}}
	launcher := &archiveAgentSettledLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	svc.agentThreads = threads
	svc.agentBindings = bindings

	if err := svc.ArchiveAgent(context.Background(), "agent-1"); err != nil {
		t.Fatalf("ArchiveAgent() error = %v", err)
	}
	if launcher.archivedAgentID != "agent-1" {
		t.Fatalf("launcher archived agent = %q, want agent-1", launcher.archivedAgentID)
	}
	if launcher.archivedThreadID != "thread-1" {
		t.Fatalf("launcher archived thread = %q, want thread-1", launcher.archivedThreadID)
	}
	if threads.updated.ThreadID != "" {
		t.Fatalf("thread status updated locally = %#v, want remote archive to own persisted update", threads.updated)
	}
	if bindings.archived.AgentID != "" {
		t.Fatalf("binding archived locally = %#v, want remote archive to own persisted update", bindings.archived)
	}
}

func TestArchiveAgentStopsLocalRuntimeBeforePersistedArchive(t *testing.T) {
	threads := &archiveAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "provider-thread-1", AgentID: "agent-1", Status: "created"},
	}}
	bindings := &archiveAgentBindingStore{bindings: map[string]PersistedBinding{
		"agent-1": {AgentID: "agent-1", Provider: "codex", CodexThreadID: "provider-thread-1"},
	}}
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.processExitWaitTimeout = 2 * time.Second
	svc.agentThreads = threads
	svc.agentBindings = bindings

	cmd := newLongRunningTestCommand()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}

	agent := svc.newAgentLocked("agent-1")
	agent.cmd = cmd
	agent.state = agentdto.StateIdle
	agent.launchSeq = 1
	svc.agents[agent.id] = agent
	svc.exitMonitor.Arm(exitmonitor.Target{AgentID: agent.id, LaunchSeq: agent.launchSeq, Cmd: cmd})
	agent.monitoredSeq = agent.launchSeq
	t.Cleanup(func() { stopAndDrainServiceTestAgent(t, svc, agent) })

	runCtx, cancelRunner := context.WithCancel(context.Background())
	defer cancelRunner()
	runDone := make(chan error, 1)
	go func() { runDone <- NewRunnerActor(silentLogger(), svc).Run(runCtx) }()
	waitForAgentMonitor(t, svc, agent.id, agent.launchSeq)

	if err := svc.ArchiveAgent(context.Background(), "agent-1"); err != nil {
		t.Fatalf("ArchiveAgent() error = %v", err)
	}

	assertArchivePersistedUpdates(t, threads, bindings)
	assertArchiveRuntimeStopped(t, svc, "agent-1")

	cancelRunner()
	assertArchiveRunnerCanceled(t, runDone)
}

func assertArchivePersistedUpdates(t *testing.T, threads *archiveAgentThreadStore, bindings *archiveAgentBindingStore) {
	t.Helper()

	if threads.updated.ThreadID != "provider-thread-1" || threads.updated.Status != "archived" {
		t.Fatalf("thread status update = %#v, want provider-thread-1 archived", threads.updated)
	}
	if bindings.archived.AgentID != "agent-1" || !bindings.archived.Archived {
		t.Fatalf("binding archive update = %#v, want agent-1 archived", bindings.archived)
	}
}

func assertArchiveRuntimeStopped(t *testing.T, svc *service, agentID string) {
	t.Helper()

	svc.mu.RLock()
	agentAfter := svc.agents[agentID]
	cmdCleared := agentAfter != nil && agentAfter.cmd == nil
	lastExitedSeq := uint64(0)
	if agentAfter != nil {
		lastExitedSeq = agentAfter.lastExitedSeq
	}
	svc.mu.RUnlock()
	if !cmdCleared || lastExitedSeq < 1 {
		t.Fatalf("local runtime not stopped: cmd_cleared=%v last_exited_seq=%d", cmdCleared, lastExitedSeq)
	}
}

func assertArchiveRunnerCanceled(t *testing.T, runDone <-chan error) {
	t.Helper()

	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runner error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestArchiveAgentArchivesOwningRuntimeWhenCalledWithProviderThreadID(t *testing.T) {
	threads := &archiveAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "provider-thread-1", AgentID: "agent-1", Status: "created"},
	}}
	bindings := &archiveAgentBindingStore{bindings: map[string]PersistedBinding{
		"agent-1": {AgentID: "agent-1", Provider: "codex", CodexThreadID: "provider-thread-1"},
	}}
	launcher := &archiveAgentLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	svc.agentThreads = threads
	svc.agentBindings = bindings
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "provider-thread-1"
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	if err := svc.ArchiveAgent(context.Background(), "provider-thread-1"); err != nil {
		t.Fatalf("ArchiveAgent(provider thread) error = %v", err)
	}
	if launcher.archivedAgentID != "agent-1" {
		t.Fatalf("archived agent = %q, want owning agent-1", launcher.archivedAgentID)
	}
	if launcher.stoppedAgentID != "" {
		t.Fatalf("stopped agent = %q, want Stop not called", launcher.stoppedAgentID)
	}
	if threads.updateCalls != 0 {
		t.Fatalf("UpdateStatus calls = %d, want 0 after remote archive", threads.updateCalls)
	}
	if bindings.archiveCalls != 0 {
		t.Fatalf("SetArchived calls = %d, want 0 after remote archive", bindings.archiveCalls)
	}
	t.Log("Archive called; Stop not called")
}

func TestArchiveAgentInvokesLauncherArchiveNotStop(t *testing.T) {
	threads := &archiveAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "provider-thread-1", AgentID: "agent-1", Status: "created"},
	}}
	bindings := &archiveAgentBindingStore{bindings: map[string]PersistedBinding{
		"agent-1": {AgentID: "agent-1", Provider: "codex", CodexThreadID: "provider-thread-1"},
	}}
	launcher := &archiveAgentLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	svc.agentThreads = threads
	svc.agentBindings = bindings
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "provider-thread-1"
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	if err := svc.ArchiveAgent(context.Background(), "agent-1"); err != nil {
		t.Fatalf("ArchiveAgent() error = %v", err)
	}
	if launcher.archivedAgentID != "agent-1" {
		t.Fatalf("archived agent = %q, want agent-1", launcher.archivedAgentID)
	}
	if launcher.stoppedAgentID != "" {
		t.Fatalf("stopped agent = %q, want Stop not called", launcher.stoppedAgentID)
	}
	if threads.updateCalls != 0 {
		t.Fatalf("UpdateStatus calls = %d, want 0 after remote archive", threads.updateCalls)
	}
	if bindings.archiveCalls != 0 {
		t.Fatalf("SetArchived calls = %d, want 0 after remote archive", bindings.archiveCalls)
	}
}

func TestArchiveAgentViaLauncherSettlesNonSettledLauncherWithoutExitMonitorEvent(t *testing.T) {
	launcher := &archiveAgentLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "provider-thread-1"
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	archived, err := svc.archiveAgentViaLauncher(context.Background(), "agent-1", "archived")
	if err != nil || !archived {
		t.Fatalf("archiveAgentViaLauncher() = (%v, %v), want archived nil", archived, err)
	}
	select {
	case ev := <-svc.exitMonitor.ExitEvents():
		t.Fatalf("unexpected synthetic exit event for non-settled archive launcher: %#v", ev)
	default:
	}
	if agent.state != agentdto.StateStopped {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateStopped)
	}
}

func TestArchiveAgentViaLauncherPublishesStoppedAfterArchiveReturns(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	stopped := make(chan agentdto.AgentStopped, 1)
	cancel := event.Subscribe(dispatcher, func(ev agentdto.AgentStopped) {
		stopped <- ev
	})
	defer cancel()

	launcher := &archiveAgentLauncher{}
	svc := NewService(silentLogger(), dispatcher, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "provider-thread-1"
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	archived, err := svc.archiveAgentViaLauncher(context.Background(), "agent-1", "archived")
	if err != nil || !archived {
		t.Fatalf("archiveAgentViaLauncher() = (%v, %v), want archived nil", archived, err)
	}
	requireStopTestEvent(t, stopped, "archived", "agent-1")
	if agent.state != agentdto.StateStopped {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateStopped)
	}
}

func TestArchiveAgentViaLauncherSettledArchivePublishesStoppedOnce(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	stopped := make(chan agentdto.AgentStopped, 2)
	cancel := event.Subscribe(dispatcher, func(ev agentdto.AgentStopped) {
		stopped <- ev
	})
	defer cancel()

	launcher := &archiveAgentSettledLauncher{}
	svc := NewService(silentLogger(), dispatcher, launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "provider-thread-1"
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	archived, err := svc.archiveAgentViaLauncher(context.Background(), "agent-1", "archived")
	if err != nil || !archived {
		t.Fatalf("archiveAgentViaLauncher() = (%v, %v), want archived nil", archived, err)
	}
	requireStopTestEvent(t, stopped, "archived", "agent-1")
	select {
	case ev := <-stopped:
		t.Fatalf("unexpected duplicate AgentStopped event: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

type archiveAgentThreadStore struct {
	threads     []PersistedThread
	updated     PersistedThreadStatusUpdate
	updateCalls int
}

func (s *archiveAgentThreadStore) ListAll(context.Context) ([]PersistedThread, error) {
	return append([]PersistedThread(nil), s.threads...), nil
}

func (s *archiveAgentThreadStore) GetByThreadID(_ context.Context, threadID string) (*PersistedThread, error) {
	for _, thread := range s.threads {
		if thread.ThreadID == threadID {
			found := thread
			return &found, nil
		}
	}
	return nil, errAgentNotFound
}

func (s *archiveAgentThreadStore) UpdateStatus(_ context.Context, params PersistedThreadStatusUpdate) error {
	s.updated = params
	s.updateCalls++
	return nil
}

type archiveAgentBindingStore struct {
	bindings     map[string]PersistedBinding
	archived     PersistedBindingArchiveUpdate
	archiveCalls int
}

func (s *archiveAgentBindingStore) GetByAgentID(_ context.Context, agentID string) (*PersistedBinding, error) {
	binding, ok := s.bindings[agentID]
	if !ok {
		return nil, errAgentNotFound
	}
	return &binding, nil
}

func (s *archiveAgentBindingStore) SetArchived(_ context.Context, params PersistedBindingArchiveUpdate) error {
	s.archived = params
	s.archiveCalls++
	return nil
}

type archiveAgentLauncher struct {
	stoppedAgentID   string
	archivedAgentID  string
	archivedThreadID string
}

type archiveAgentSettledLauncher struct {
	archiveAgentLauncher
}

func (*archiveAgentSettledLauncher) StopSettlesAgent() bool {
	return true
}

func (l *archiveAgentLauncher) Launch(context.Context, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, nil
}

func (l *archiveAgentLauncher) Fork(context.Context, *agentRuntime, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, errors.New("fork should not be called")
}

func (l *archiveAgentLauncher) Stop(_ context.Context, agent *agentRuntime) error {
	if agent != nil {
		l.stoppedAgentID = agent.id
	}
	return nil
}

func (l *archiveAgentLauncher) Archive(_ context.Context, agent *agentRuntime) error {
	if agent != nil {
		l.archivedAgentID = agent.id
		l.archivedThreadID = agent.remoteThreadID
	}
	return nil
}

func (l *archiveAgentLauncher) Interrupt(context.Context, *agentRuntime, string) error {
	return nil
}

func (l *archiveAgentLauncher) SubmitTurn(context.Context, *agentRuntime, TurnSubmission) (string, error) {
	return "", nil
}

func (l *archiveAgentLauncher) IsRunning(_ context.Context, agent *agentRuntime) bool {
	return agent != nil && agent.remoteThreadID != ""
}
