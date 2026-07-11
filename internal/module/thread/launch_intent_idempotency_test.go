package thread

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idempotency"
)

func TestStartLaunchIntentReusesPendingThreadResult(t *testing.T) {
	cwd := wantStartCWD(t)
	threads := &stubThreadStore{}
	svc := &service{threadStore: threads}
	req := StartRequest{
		LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider:       "codex",
		CWD:            cwd,
		DeferSpawn:     true,
	}

	first, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	second, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}

	if first.ThreadID == "" || first.AgentID == "" {
		t.Fatalf("first Start() returned empty ids: %+v", first)
	}
	if second.ThreadID != first.ThreadID || second.AgentID != first.AgentID {
		t.Fatalf("second Start() = %+v, want same ids as %+v", second, first)
	}
	if first.AgentID == req.LaunchIntentID {
		t.Fatalf("agent_id must be backend-generated, got client launch intent id %q", first.AgentID)
	}
	if threads.upsertCount != 1 {
		t.Fatalf("thread upserts = %d, want 1", threads.upsertCount)
	}
}

func TestStartLaunchIntentConcurrentCallsCreateOnePendingThread(t *testing.T) {
	cwd := wantStartCWD(t)
	threads := &stubThreadStore{}
	svc := &service{threadStore: threads}
	req := StartRequest{
		LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider:       "codex",
		CWD:            cwd,
		DeferSpawn:     true,
	}
	const n = 8
	start := make(chan struct{})
	results := make(chan StartResult, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	workersDone := make(chan struct{})
	registerThreadGoroutineCleanup(t, workersDone, "launch intent")
	for range n {
		wg.Go(func() {
			<-start
			result, err := svc.Start(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			results <- result
		})
	}
	close(start)
	wg.Wait()
	close(workersDone)
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("Start() error = %v", err)
	}
	var first StartResult
	for result := range results {
		if first.ThreadID == "" {
			first = result
			continue
		}
		if result.ThreadID != first.ThreadID || result.AgentID != first.AgentID {
			t.Fatalf("result = %+v, want same ids as %+v", result, first)
		}
	}
	if threads.upsertCount != 1 {
		t.Fatalf("thread upserts = %d, want 1", threads.upsertCount)
	}
}

func TestStartLaunchIntentRejectsMalformedID(t *testing.T) {
	svc := &service{threadStore: &stubThreadStore{}}

	_, err := svc.Start(context.Background(), StartRequest{
		LaunchIntentID: "../agent_unsafe",
		Provider:       "codex",
		CWD:            wantStartCWD(t),
		DeferSpawn:     true,
	})

	if err == nil {
		t.Fatal("Start() error = nil, want malformed launch intent rejection")
	}
	if !strings.Contains(err.Error(), "launch_intent_id") {
		t.Fatalf("Start() error = %v, want launch_intent_id context", err)
	}
}

func TestStartLaunchIntentRejectsParameterMismatch(t *testing.T) {
	cwd := wantStartCWD(t)
	threads := &stubThreadStore{}
	svc := &service{threadStore: threads}
	req := StartRequest{
		LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider:       "codex",
		CWD:            cwd,
		DeferSpawn:     true,
	}
	if _, err := svc.Start(context.Background(), req); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	req.Model = "opus"

	_, err := svc.Start(context.Background(), req)

	if err == nil {
		t.Fatal("Start() error = nil, want launch intent parameter mismatch")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Fatalf("Start() error = %v, want already used context", err)
	}
	if threads.upsertCount != 1 {
		t.Fatalf("thread upserts = %d, want 1 after rejected duplicate", threads.upsertCount)
	}
}

func TestStartLaunchIntentPendingFailureKeepsRowAndRetainsKey(t *testing.T) {
	cwd := wantStartCWD(t)
	threads := &cleanupCountingThreadStore{}
	svc := &service{threadStore: threads}
	req := StartRequest{
		LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider:       "codex",
		CWD:            cwd,
		DeferSpawn:     true,
	}
	first, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := svc.cleanupFailedPendingLaunch(context.Background(), first.ThreadID, first.AgentID, errors.New("bind failed")); err == nil {
		t.Fatal("cleanupFailedPendingLaunch() error = nil, want original failure")
	}
	if _, err := svc.Start(context.Background(), req); err == nil || !strings.Contains(err.Error(), "bind failed") {
		t.Fatalf("second Start() error = %v, want retained launch failure", err)
	}

	if threads.upsertCount != 1 || threads.deleteCount != 0 {
		t.Fatalf("upserts/deletes = %d/%d, want 1/0 so failed row remains visible", threads.upsertCount, threads.deleteCount)
	}
	if threads.status.ThreadID != first.ThreadID || threads.status.Status != statusFailed {
		t.Fatalf("UpdateStatus = %+v, want failed status for %q", threads.status, first.ThreadID)
	}
}

func TestStartLaunchIntentRetainsKeyAfterPendingCleanupRetainedError(t *testing.T) {
	cwd := wantStartCWD(t)
	threads := &cleanupCountingThreadStore{}
	svc := &service{threadStore: threads}
	req := StartRequest{
		LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider:       "codex",
		CWD:            cwd,
		DeferSpawn:     true,
	}
	first, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	cause := errors.New("post-launch cleanup uncertain")
	if err := svc.cleanupFailedPendingLaunch(context.Background(), first.ThreadID, first.AgentID, idempotency.Retain(cause)); !errors.Is(err, cause) {
		t.Fatalf("cleanupFailedPendingLaunch() error = %v, want retained cause", err)
	}
	if _, err := svc.Start(context.Background(), req); !errors.Is(err, cause) {
		t.Fatalf("second Start() error = %v, want retained cause", err)
	}
	if threads.upsertCount != 1 || threads.deleteCount != 0 {
		t.Fatalf("upserts/deletes = %d/%d, want retained error without relaunch or deletion", threads.upsertCount, threads.deleteCount)
	}
	if threads.status.ThreadID != first.ThreadID || threads.status.Status != statusFailed {
		t.Fatalf("UpdateStatus = %+v, want failed status for %q", threads.status, first.ThreadID)
	}
}

func TestStartLaunchIntentRetainsKeyWhenPendingFailureStatusUpdateFails(t *testing.T) {
	statusErr := errors.New("status update failed")
	threads := &cleanupCountingThreadStore{statusErr: statusErr}
	svc := &service{threadStore: threads}
	req := StartRequest{
		LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider:       "codex",
		CWD:            wantStartCWD(t),
		DeferSpawn:     true,
	}
	first, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	cause := errors.New("post-launch cleanup uncertain")
	if err := svc.cleanupFailedPendingLaunch(context.Background(), first.ThreadID, first.AgentID, idempotency.Retain(cause)); !errors.Is(err, statusErr) {
		t.Fatalf("cleanupFailedPendingLaunch() error = %v, want status failure", err)
	}
	threads.statusErr = nil
	if _, err := svc.Start(context.Background(), req); !errors.Is(err, statusErr) {
		t.Fatalf("second Start() error = %v, want retained status failure", err)
	}
	if threads.upsertCount != 1 {
		t.Fatalf("thread upserts = %d, want retained error without relaunch", threads.upsertCount)
	}
}

func TestStartLaunchIntentRetainedPendingStatusFailureBlocksDirectSpawnRetry(t *testing.T) {
	threads, _, orch, svc := eagerSnapshotFailureFixture(t)
	req := StartRequest{
		LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider:       "codex",
		CWD:            wantStartCWD(t),
		DeferSpawn:     true,
	}
	first, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	threads.thread.PendingLaunch = true

	statusErr := errors.New("status update failed")
	cause := errors.New("post-launch cleanup uncertain")
	threads.statusErr = statusErr
	if err := svc.cleanupFailedPendingLaunch(context.Background(), first.ThreadID, first.AgentID, idempotency.Retain(cause)); !errors.Is(err, statusErr) {
		t.Fatalf("cleanupFailedPendingLaunch() error = %v, want status failure", err)
	}
	threads.statusErr = nil
	launchesBefore := len(orch.launches)
	upsertsBefore := threads.upsertCount

	launched, _, err := svc.SpawnIfNeeded(context.Background(), first.ThreadID, "hello", req.CWD)
	if !errors.Is(err, cause) {
		t.Fatalf("SpawnIfNeeded() error = %v, want retained cause", err)
	}
	if launched {
		t.Fatal("SpawnIfNeeded() launched = true, want retained failure without launch")
	}
	if len(orch.launches) != launchesBefore {
		t.Fatalf("launches = %d, want %d after retained cleanup delete failure", len(orch.launches), launchesBefore)
	}
	if threads.upsertCount != upsertsBefore {
		t.Fatalf("thread upserts = %d, want %d after retained cleanup delete failure", threads.upsertCount, upsertsBefore)
	}
}

func TestStartLaunchIntentCompleteAllowsKeyReuse(t *testing.T) {
	cwd := wantStartCWD(t)
	threads := &stubThreadStore{}
	svc := &service{threadStore: threads}
	req := StartRequest{
		LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider:       "codex",
		CWD:            cwd,
		DeferSpawn:     true,
	}
	first, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	svc.CompleteLaunchIntent(context.Background(), first.ThreadID)
	second, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}

	if second.ThreadID == first.ThreadID || second.AgentID == first.AgentID {
		t.Fatalf("second Start() = %+v, want fresh ids after completion of %+v", second, first)
	}
	if threads.upsertCount != 2 {
		t.Fatalf("thread upserts = %d, want 2", threads.upsertCount)
	}
}

func TestStartLaunchIntentStopPendingAllowsKeyReuse(t *testing.T) {
	assertPendingTerminalAllowsKeyReuse(t, func(ctx context.Context, svc *service, threadID string) error {
		return svc.Stop(ctx, threadID)
	})
}

func TestStartLaunchIntentArchivePendingAllowsKeyReuse(t *testing.T) {
	assertPendingTerminalAllowsKeyReuse(t, func(ctx context.Context, svc *service, threadID string) error {
		return svc.Archive(ctx, threadID)
	})
}

func TestStartLaunchIntentDeletePendingAllowsKeyReuse(t *testing.T) {
	assertPendingTerminalAllowsKeyReuse(t, func(ctx context.Context, svc *service, threadID string) error {
		return svc.Delete(ctx, threadID)
	})
}

func assertPendingTerminalAllowsKeyReuse(t *testing.T, terminate func(context.Context, *service, string) error) {
	t.Helper()
	cwd := wantStartCWD(t)
	threads := &stubThreadStore{}
	svc := &service{threadStore: threads}
	req := StartRequest{
		LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider:       "codex",
		CWD:            cwd,
		DeferSpawn:     true,
	}
	first, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	threads.thread.PendingLaunch = true
	if err := terminate(context.Background(), svc, first.ThreadID); err != nil {
		t.Fatalf("terminate pending launch error = %v", err)
	}
	second, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}

	if second.ThreadID == first.ThreadID || second.AgentID == first.AgentID {
		t.Fatalf("second Start() = %+v, want fresh ids after stop of %+v", second, first)
	}
	if threads.upsertCount != 2 {
		t.Fatalf("thread upserts = %d, want 2", threads.upsertCount)
	}
}

func TestStartLaunchIntentCleansEagerStateAfterSnapshotFailure(t *testing.T) {
	threads, bindings, orch, svc := eagerSnapshotFailureFixture(t)
	threads.promptSnapshotError = errors.New("snapshot failed")
	req := StartRequest{LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111", Provider: "codex", CWD: wantStartCWD(t), Prompt: "hello"}

	_, err := svc.Start(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "snapshot failed") {
		t.Fatalf("Start() error = %v, want snapshot failure", err)
	}
	if len(orch.stops) != 1 || orch.stops[0] != orch.launches[0].AgentID {
		t.Fatalf("stopped agents = %#v, want launched agent %q", orch.stops, orch.launches[0].AgentID)
	}
	if threads.deleteCount != 1 {
		t.Fatalf("thread deletes = %d, want 1", threads.deleteCount)
	}
	if got := bindings.deleteAgentIDs; len(got) != 1 || got[0] != orch.launches[0].AgentID {
		t.Fatalf("binding deletes = %#v, want launched agent %q", got, orch.launches[0].AgentID)
	}
	threads.promptSnapshotError = nil
	if _, err := svc.Start(context.Background(), req); err != nil {
		t.Fatalf("retry Start() error = %v", err)
	}
	if len(orch.launches) != 2 {
		t.Fatalf("launches = %d, want retry after completed cleanup", len(orch.launches))
	}
}

func TestStartLaunchIntentRetainsKeyWhenEagerCleanupFails(t *testing.T) {
	threads, bindings, orch, svc := eagerSnapshotFailureFixture(t)
	cleanupErr := errors.New("delete failed")
	threads.promptSnapshotError = errors.New("snapshot failed")
	threads.deleteErr = cleanupErr
	req := StartRequest{
		LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider:       "codex",
		CWD:            wantStartCWD(t),
		Prompt:         "hello",
	}

	if _, err := svc.Start(context.Background(), req); !errors.Is(err, cleanupErr) {
		t.Fatalf("first Start() error = %v, want cleanup failure", err)
	}
	threads.promptSnapshotError, threads.deleteErr = nil, nil
	if _, err := svc.Start(context.Background(), req); !errors.Is(err, cleanupErr) {
		t.Fatalf("second Start() error = %v, want retained cleanup failure", err)
	}

	if len(orch.launches) != 1 {
		t.Fatalf("launches = %d, want retained error replay without relaunch", len(orch.launches))
	}
	if got := bindings.deleteAgentIDs; len(got) != 1 || got[0] != orch.launches[0].AgentID {
		t.Fatalf("binding deletes = %#v, want one cleanup attempt for %q", got, orch.launches[0].AgentID)
	}
}

func TestStartLaunchIntentRetainsKeyWhenEagerLaunchAgentFails(t *testing.T) {
	_, _, orch, svc := eagerSnapshotFailureFixture(t)
	launchErr := errors.New("launch uncertain")
	orch.launchErr = launchErr
	req := StartRequest{
		LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider:       "codex",
		CWD:            wantStartCWD(t),
		Prompt:         "hello",
	}

	if _, err := svc.Start(context.Background(), req); !errors.Is(err, launchErr) {
		t.Fatalf("first Start() error = %v, want launch failure", err)
	}
	orch.launchErr = nil
	if _, err := svc.Start(context.Background(), req); !errors.Is(err, launchErr) {
		t.Fatalf("second Start() error = %v, want retained launch failure", err)
	}
	if len(orch.launches) != 1 {
		t.Fatalf("launches = %d, want retained error replay without relaunch", len(orch.launches))
	}
}

func TestStartLaunchIntentRetainsKeyWhenPendingLaunchAgentFails(t *testing.T) {
	threads, _, orch, svc := eagerSnapshotFailureFixture(t)
	req := StartRequest{LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111", Provider: "codex", CWD: wantStartCWD(t), DeferSpawn: true}
	started, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	threads.thread.PendingLaunch = true
	launchErr := errors.New("pending launch uncertain")
	orch.launchErr = launchErr
	launched, _, err := svc.SpawnIfNeeded(context.Background(), started.ThreadID, "hello", req.CWD)
	if !errors.Is(err, launchErr) {
		t.Fatalf("first SpawnIfNeeded() error = %v, want launch failure", err)
	}
	if launched {
		t.Fatal("first SpawnIfNeeded() launched = true, want failure")
	}
	orch.launchErr = nil
	launched, _, err = svc.SpawnIfNeeded(context.Background(), started.ThreadID, "hello", req.CWD)
	if !errors.Is(err, launchErr) {
		t.Fatalf("second SpawnIfNeeded() error = %v, want retained launch failure", err)
	}
	if launched {
		t.Fatal("second SpawnIfNeeded() launched = true, want retained failure")
	}
	if len(orch.launches) != 1 {
		t.Fatalf("launches = %d, want retained error replay without relaunch", len(orch.launches))
	}
}

type cleanupCountingThreadStore struct {
	stubThreadStore
	deleteCount int
	deleteErr   error
	statusErr   error
}

func (s *cleanupCountingThreadStore) DeleteByThreadID(context.Context, string) error {
	s.deleteCount++
	return s.deleteErr
}

func (s *cleanupCountingThreadStore) UpdateStatus(_ context.Context, params ThreadStatusUpdate) error {
	s.status = params
	return s.statusErr
}

type snapshotPromptAssembly struct{}

func (snapshotPromptAssembly) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{
		DisplayName: "hello",
		Snapshot: contract.PromptAssemblySnapshot{
			DisplayName: "hello",
			Provider:    "codex",
			Version:     contract.PromptAssemblySnapshotVersion,
			Hash:        "hash",
		},
	}, nil
}

func (snapshotPromptAssembly) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (snapshotPromptAssembly) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (snapshotPromptAssembly) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

type launchIntentSessionProvider struct {
	*stubSessionProvider
	generation uint64
}

func (p *launchIntentSessionProvider) SessionGeneration(string) uint64 { return p.generation }

func (p *launchIntentSessionProvider) RemoveSessionGeneration(agentID string, generation uint64) {
	if generation == p.generation {
		p.RemoveSession(agentID)
	}
}

func (p *launchIntentSessionProvider) register(session contract.Session) {
	p.generation++
	p.session = session
}

type launchIntentOrchestration struct {
	launches       []LaunchAgentRequest
	stops          []string
	bindGeneration uint64
	launchErr      error
}

func (o *launchIntentOrchestration) LaunchAgent(_ context.Context, req LaunchAgentRequest) error {
	o.launches = append(o.launches, req)
	return o.launchErr
}

func (o *launchIntentOrchestration) StopAgent(_ context.Context, agentID string) error {
	o.stops = append(o.stops, agentID)
	return nil
}

func (o *launchIntentOrchestration) Recover(context.Context, string) error { return nil }

func (o *launchIntentOrchestration) BindSessionGeneration(context.Context, string, uint64) error {
	return nil
}

func eagerSnapshotFailureFixture(t *testing.T) (*cleanupCountingThreadStore, *stubBindingStore, *launchIntentOrchestration, *service) {
	t.Helper()
	threads := &cleanupCountingThreadStore{}
	bindings := &stubBindingStore{}
	session := &stubSession{threadID: "018f00e0-39fc-72ac-a47a-2a858c75d111"}
	orch := &launchIntentOrchestration{bindGeneration: 1}
	sessionProvider := &launchIntentSessionProvider{stubSessionProvider: &stubSessionProvider{}}
	svc := &service{
		threadStore:  threads,
		bindingStore: bindings,
		starter: &stubSessionStarter{onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			sessionProvider.register(session)
			return session, nil
		}},
		sessions:                sessionProvider,
		orchestration:           orch,
		sessionGenerationBinder: orch,
		promptAssembly:          snapshotPromptAssembly{},
	}
	return threads, bindings, orch, svc
}
