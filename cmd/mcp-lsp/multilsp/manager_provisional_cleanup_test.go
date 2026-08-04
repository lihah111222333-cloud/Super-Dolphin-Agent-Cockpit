package multilsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestCreateAndRegisterClientRetainsExactProcessTreeOwnerWithHashedLifecycle(t *testing.T) {
	cleanupErr := errors.New("release failed once")
	owner := &provisionalCleanupTestOwner{
		releaseErrors: []error{cleanupErr, nil},
		identity:      hiddenexec.ProcessIdentity{PID: 4242, StartToken: "start"},
	}
	factoryErr := errors.Join(
		fmt.Errorf("factory wrapped startup failure: %w", &processTreeCleanupError{owner: owner, cause: cleanupErr}),
		errors.New("secondary startup detail"),
	)
	key := "/private/workspaces/project-with-sensitive-path:go"
	mgr := &manager{
		instanceID: "test-owner-manager",
		factory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
			return nil, factoryErr
		}),
		workspaces: make(map[string]*workspaceClient),
	}
	cfg := workspaceConfig{key: key, rootPath: t.TempDir(), rootURI: "file:///project", languageID: "go"}

	if _, err := mgr.createAndRegisterClient(context.Background(), cfg); !errors.Is(err, cleanupErr) {
		t.Fatalf("createAndRegisterClient() error = %v, want cleanup error", err)
	}
	assertExactOwnerState(t, mgr, key, owner)

	if err := mgr.retryProvisionalClientCleanups(key); !errors.Is(err, cleanupErr) {
		t.Fatalf("first cleanup retry error = %v, want release failure", err)
	}
	assertPendingOwnerCount(t, mgr, key, 1, "after failed retry")
	if err := mgr.retryProvisionalClientCleanups(key); err != nil {
		t.Fatalf("second cleanup retry error = %v, want nil", err)
	}
	assertPendingOwnerCount(t, mgr, key, 0, "after successful retry")
	if got := owner.terminateCalls.Load(); got != 2 {
		t.Fatalf("Terminate calls = %d, want one per retry", got)
	}
}

func TestCleanupFailureLogsHashedWorkspaceAndUnknownAction(t *testing.T) {
	var logs bytes.Buffer
	mgr := &manager{instanceID: "test-log-manager", logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	key := "/Users/private/project/secret:go"
	state := newProvisionalTestPending(t, mgr, key, 7, nil, &provisionalCleanupTestOwner{})
	cleanupMessage := "cleanup pending path=/Users/private/project/secret uri=file:///private/project/secret token=Bearer-secret-token"
	if err := mgr.observeCleanupFailure(state, errors.New(cleanupMessage)); err != nil {
		t.Fatalf("observeCleanupFailure() error = %v", err)
	}
	output := logs.String()
	assertRedactedCleanupLog(t, output, key)
	assertCleanupPairMetadata(t, output, state)
}

func TestProvisionalRetryAllocatesAttemptOperationAndTerminalPair(t *testing.T) {
	var logs bytes.Buffer
	releaseErr := errors.New("release failed once")
	owner := &provisionalCleanupTestOwner{
		releaseErrors: []error{releaseErr, nil},
		identity:      hiddenexec.ProcessIdentity{PID: 4243, StartToken: "start"},
	}
	mgr := &manager{
		logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
		instanceID: "test-manager",
	}
	key := "/private/workspaces/operation-audit:go"
	mgr.retainProvisionalClientCleanups(key, []pendingClientShutdown{newProvisionalTestPending(t, mgr, key, 4, nil, owner)})
	if err := mgr.retryProvisionalClientCleanups(key); !errors.Is(err, releaseErr) {
		t.Fatalf("first provisional retry error = %v, want release failure", err)
	}
	if err := mgr.retryProvisionalClientCleanups(key); err != nil {
		t.Fatalf("second provisional retry error = %v, want success", err)
	}

	records := decodeCleanupRecords(t, logs.String())
	if len(records) != 3 {
		t.Fatalf("cleanup log records = %d, want failure pair and one success terminal", len(records))
	}
	assertRetryLogCorrelation(t, records)
}

func TestCleanupFailureWithoutIdentityDoesNotProbePIDZero(t *testing.T) {
	mgr := &manager{instanceID: "test-identity-manager"}
	state := newProvisionalTestPending(t, mgr, "workspace", 1, nil, &provisionalCleanupTestOwner{})
	if err := mgr.observeCleanupFailure(state, errors.New("cleanup pending")); err != nil {
		t.Fatalf("observeCleanupFailure() error = %v", err)
	}
	if mgr.processObserver != nil {
		t.Fatal("cleanup failure without identity/PID initialized process observer")
	}
}

func TestNewManagerWithErrorPropagatesEntropyFailureBeforeSpawn(t *testing.T) {
	var factoryCalls atomic.Int32
	manager, err := NewManagerWithError(Config{
		IdleTimeout: time.Minute,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
			factoryCalls.Add(1)
			return nil, errors.New("factory must not run")
		}),
		provisionalEntropy: failingProvisionalEntropy{},
	})
	if err == nil || manager != nil {
		t.Fatalf("NewManagerWithError() = (%T, %v), want construction error and nil manager", manager, err)
	}
	if got := factoryCalls.Load(); got != 0 {
		t.Fatalf("factory calls after entropy failure = %d, want 0", got)
	}
}

func TestProvisionalManagerAndCloneLifecycleIdentityDoesNotCollide(t *testing.T) {
	primaryRoot := t.TempDir()
	first := NewManager(Config{WorkspaceRoot: primaryRoot}).(*manager)
	second := NewManager(Config{WorkspaceRoot: primaryRoot}).(*manager)
	firstState := newProvisionalTestPending(t, first, "same-workspace:go", 1, nil, nil)
	secondState := newProvisionalTestPending(t, second, "same-workspace:go", 1, nil, nil)
	if firstState.lifecycleID == secondState.lifecycleID {
		t.Fatalf("manager lifecycle IDs collided: %q", firstState.lifecycleID)
	}
	cloneA := first.cloneForWorkspace(primaryRoot)
	cloneB := first.cloneForWorkspace(primaryRoot)
	if cloneA.instanceID == cloneB.instanceID || !strings.Contains(cloneA.instanceID, first.instanceID) {
		t.Fatalf("clone identities are not parent-scoped and unique: %q, %q", cloneA.instanceID, cloneB.instanceID)
	}
}

func TestCleanupFailureWithPIDOnlyOwnerEmitsReadOnlyPair(t *testing.T) {
	var logs bytes.Buffer
	key := "/Users/private/project/pid-only:go"
	mgr := &manager{
		instanceID:              "test-observer-manager",
		logger:                  slog.New(slog.NewJSONHandler(&logs, nil)),
		processObservationStore: processobserve.NewMemoryStore(),
	}
	state := newProvisionalTestPending(t, mgr, key, 11, nil, &provisionalPIDOnlyOwner{pid: os.Getpid()})
	if err := mgr.observeCleanupFailure(state, errors.New("identity unavailable")); err != nil {
		t.Fatalf("observeCleanupFailure() error = %v", err)
	}
	decisions, err := mgr.processObservationStore.ListDecisions(context.Background())
	if err != nil {
		t.Fatalf("ListDecisions() error = %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("observation decisions = %d, want one paired decision", len(decisions))
	}
	output := logs.String()
	if strings.Count(output, state.operationID) < 2 || strings.Count(output, state.lifecycleID) != 2 {
		t.Fatalf("process observation pair correlation missing: %s", output)
	}
	if strings.Count(output, "signal_sent") != 2 || strings.Contains(output, key) || strings.Contains(output, "workspace_key") {
		t.Fatalf("process observation pair metadata invalid: %s", output)
	}
}

func TestEnsureClientBlocksSpawnUntilProvisionalOwnerRetrySucceeds(t *testing.T) {
	cleanupErr := errors.New("owner release blocked")
	owner := &provisionalCleanupTestOwner{releaseErrors: []error{cleanupErr, nil}, identity: hiddenexec.ProcessIdentity{PID: 4343}}
	replacement := &provisionalCleanupClient{}
	factory := &provisionalCleanupFactory{clients: []Client{replacement}}
	mgr := &manager{instanceID: "test-ensure-manager", factory: ClientFactoryFunc(factory.newClient), workspaces: make(map[string]*workspaceClient)}
	key := "ensure-owner-gate:go"
	mgr.retainProvisionalClientCleanups(key, []pendingClientShutdown{newProvisionalTestPending(t, mgr, key, 3, nil, owner)})
	cfg := workspaceConfig{key: key, rootPath: t.TempDir(), rootURI: "file:///ensure-owner-gate", languageID: "go"}
	if client, err := mgr.ensureClient(context.Background(), cfg); client != nil || !errors.Is(err, cleanupErr) {
		t.Fatalf("first ensure = (%T, %v), want blocked cleanup", client, err)
	}
	if got := factory.callCount(); got != 0 {
		t.Fatalf("factory calls while owner cleanup pending = %d, want 0", got)
	}
	client, err := mgr.ensureClient(context.Background(), cfg)
	if err != nil || client != replacement {
		t.Fatalf("second ensure = (%T, %v), want replacement client", client, err)
	}
	if got := factory.callCount(); got != 1 {
		t.Fatalf("factory calls after owner cleanup retry = %d, want 1", got)
	}
}

func TestStaleProvisionalGenerationCannotMutateReplacementWorkspace(t *testing.T) {
	closeErr := errors.New("discarded generation close failed")
	key := "stale-generation-replacement:go"
	existing := &provisionalCleanupClient{}
	discarded := &provisionalCleanupClient{closeErrors: []error{closeErr, nil}}
	mgr := &manager{
		instanceID: "test-generation-manager",
		factory:    ClientFactoryFunc((&provisionalCleanupFactory{clients: []Client{discarded}}).newClient),
		workspaces: map[string]*workspaceClient{
			key: {key: key, client: existing, generation: 12, state: workspaceStateActive},
		},
	}
	mgr.workspaceGeneration.Store(12)
	cfg := workspaceConfig{key: key, rootPath: t.TempDir(), rootURI: "file:///stale-generation", languageID: "go"}
	got, err := mgr.createAndRegisterClient(context.Background(), cfg)
	if got != existing || !errors.Is(err, closeErr) {
		t.Fatalf("discarded stale generation create = (%T, %v), want existing and close error", got, err)
	}
	assertWorkspaceGeneration(t, mgr, key, existing, 12, "after stale cleanup")
	assertPendingGeneration(t, mgr, key, 13)
	if err := mgr.retryProvisionalClientCleanups(key); err != nil {
		t.Fatalf("stale cleanup retry error = %v", err)
	}
	assertWorkspaceGeneration(t, mgr, key, existing, 12, "after stale cleanup retry")
}

func assertExactOwnerState(t *testing.T, mgr *manager, key string, owner processTreeCleanupTarget) {
	t.Helper()
	mgr.mu.RLock()
	states := append([]pendingClientShutdown(nil), mgr.provisionalCleanups[key]...)
	mgr.mu.RUnlock()
	if len(states) != 1 || states[0].owner != owner {
		t.Fatalf("retained owner = %#v, want exact owner %p", states, owner)
	}
	if states[0].generation != 1 {
		t.Fatalf("retained generation = %d, want preallocated generation 1", states[0].generation)
	}
	if states[0].workspaceHash == "" || strings.Contains(states[0].lifecycleID, key) {
		t.Fatalf("lifecycle metadata leaked workspace key: hash=%q lifecycle=%q", states[0].workspaceHash, states[0].lifecycleID)
	}
}

func assertPendingOwnerCount(t *testing.T, mgr *manager, key string, want int, phase string) {
	t.Helper()
	mgr.mu.RLock()
	got := len(mgr.provisionalCleanups[key])
	mgr.mu.RUnlock()
	if got != want {
		t.Fatalf("pending owner count %s = %d, want %d", phase, got, want)
	}
}

func assertWorkspaceGeneration(t *testing.T, mgr *manager, key string, client Client, generation uint64, phase string) {
	t.Helper()
	mgr.mu.RLock()
	workspace := mgr.workspaces[key]
	mgr.mu.RUnlock()
	if workspace == nil || workspace.client != client || workspace.generation != generation || workspace.state != workspaceStateActive {
		t.Fatalf("workspace %s mutated: %#v", phase, workspace)
	}
}

func assertPendingGeneration(t *testing.T, mgr *manager, key string, generation uint64) {
	t.Helper()
	mgr.mu.RLock()
	states := append([]pendingClientShutdown(nil), mgr.provisionalCleanups[key]...)
	mgr.mu.RUnlock()
	if len(states) != 1 || states[0].generation != generation {
		t.Fatalf("stale cleanup generation = %#v, want %d", states, generation)
	}
}

func TestManagerCloseRetriesRetainedProcessTreeOwner(t *testing.T) {
	cleanupErr := errors.New("close owner release failed")
	owner := &provisionalCleanupTestOwner{releaseErrors: []error{cleanupErr, nil}, identity: hiddenexec.ProcessIdentity{PID: 4444}}
	mgr := &manager{instanceID: "test-close-manager", workspaces: make(map[string]*workspaceClient)}
	key := "close-owner-retry:go"
	mgr.retainProvisionalClientCleanups(key, []pendingClientShutdown{newProvisionalTestPending(t, mgr, key, 5, nil, owner)})
	if err := mgr.Close(); !errors.Is(err, cleanupErr) {
		t.Fatalf("first manager Close() error = %v, want retained owner failure", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("second manager Close() error = %v, want success", err)
	}
	if got := owner.releaseCalls.Load(); got != 2 {
		t.Fatalf("owner Release calls = %d, want retry", got)
	}
}

func TestRecyclerRetriesAndAccountsProvisionalOwner(t *testing.T) {
	cleanupErr := errors.New("recycler owner release failed")
	owner := &provisionalCleanupTestOwner{releaseErrors: []error{cleanupErr, nil}, identity: hiddenexec.ProcessIdentity{PID: 4545}}
	mgr := &manager{instanceID: "test-recycler-manager", workspaces: make(map[string]*workspaceClient)}
	key := "recycler-owner-retry:go"
	mgr.retainProvisionalClientCleanups(key, []pendingClientShutdown{newProvisionalTestPending(t, mgr, key, 6, nil, owner)})
	pool := NewManagerPool(mgr, 1)
	recycler := newPoolRecycler(pool)
	recycler.retryProvisionalCleanups(mgr)
	mgr.mu.RLock()
	remaining := len(mgr.provisionalCleanups[key])
	mgr.mu.RUnlock()
	if remaining != 1 {
		t.Fatalf("recycler pending owner count after failure = %d, want 1", remaining)
	}
	recycler.retryProvisionalCleanups(mgr)
	mgr.mu.RLock()
	remaining = len(mgr.provisionalCleanups[key])
	mgr.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("recycler pending owner count after success = %d, want 0", remaining)
	}
}

func TestEnsureRecyclerCloseRaceUsesOneProvisionalOwnerTransaction(t *testing.T) {
	owner := &provisionalGateOwner{entered: make(chan struct{}), release: make(chan struct{})}
	replacement := &provisionalCleanupClient{}
	factory := &provisionalCleanupFactory{clients: []Client{replacement}}
	mgr := &manager{instanceID: "test-race-manager", factory: ClientFactoryFunc(factory.newClient), workspaces: make(map[string]*workspaceClient)}
	key := "owner-transaction-race:go"
	mgr.retainProvisionalClientCleanups(key, []pendingClientShutdown{newProvisionalTestPending(t, mgr, key, 9, nil, owner)})
	cfg := workspaceConfig{key: key, rootPath: t.TempDir(), rootURI: "file:///owner-transaction-race", languageID: "go"}
	pool := NewManagerPool(mgr, 1)
	recycler := newPoolRecycler(pool)

	ensureDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.ownerRace.ensure", func(context.Context) {
		_, err := mgr.ensureClient(context.Background(), cfg)
		ensureDone <- err
	})
	waitOwnerRaceSignal(t, owner.entered, "ensure did not enter owner transaction")

	recyclerDone := make(chan struct{})
	safego.Go(context.Background(), nil, "multilsp.ownerRace.recycler", func(context.Context) {
		recycler.retryProvisionalCleanups(mgr)
		close(recyclerDone)
	})
	closeDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.ownerRace.close", func(context.Context) { closeDone <- mgr.Close() })
	close(owner.release)
	waitOwnerRaceSignal(t, recyclerDone, "recycler did not finish owner transaction race")
	waitOwnerRaceResult(t, ensureDone, "ensure did not finish owner transaction race")
	waitOwnerRaceResult(t, closeDone, "manager Close did not finish owner transaction race")
	if got := owner.terminateCalls.Load(); got != 1 {
		t.Fatalf("owner Terminate calls = %d, want one transaction", got)
	}
	if got := owner.releaseCalls.Load(); got != 1 {
		t.Fatalf("owner Release calls = %d, want one transaction", got)
	}
}

// waitOwnerRaceSignal 等待 race 测试中的完成信号，避免裸 goroutine 与复杂分支。
func waitOwnerRaceSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(label)
	}
}

// waitOwnerRaceResult 等待 race 测试中的错误结果并在超时时失败。
func waitOwnerRaceResult[T any](t *testing.T, result <-chan T, label string) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal(label)
		var zero T
		return zero
	}
}

func newProvisionalTestPending(
	t *testing.T,
	mgr *manager,
	key string,
	generation uint64,
	client Client,
	owner processTreeCleanupTarget,
) pendingClientShutdown {
	t.Helper()
	state, err := mgr.newPendingClientShutdown(key, generation, client, owner)
	if err != nil {
		t.Fatalf("newPendingClientShutdown() error = %v", err)
	}
	return state
}

func assertRedactedCleanupLog(t *testing.T, output, key string) {
	t.Helper()
	if strings.Contains(output, key) || strings.Contains(output, "workspace_key") {
		t.Fatalf("cleanup logs leaked raw workspace identity: %s", output)
	}
	for _, secret := range []string{"/Users/private/project/secret", "file:///private/project/secret", "Bearer-secret-token"} {
		if strings.Contains(output, secret) {
			t.Fatalf("cleanup logs leaked raw cleanup payload %q: %s", secret, output)
		}
	}
	if strings.Contains(output, "signal_sent") {
		t.Fatalf("known-owner cleanup logs asserted signal_sent: %s", output)
	}
}

func assertCleanupPairMetadata(t *testing.T, output string, state pendingClientShutdown) {
	t.Helper()
	if !strings.Contains(output, "workspace_hash") || !strings.Contains(output, "action_result") || !strings.Contains(output, "unknown") {
		t.Fatalf("cleanup logs missing redacted action metadata: %s", output)
	}
	if strings.Count(output, state.operationID) < 2 || strings.Count(output, state.lifecycleID) != 2 {
		t.Fatalf("cleanup pair correlation missing: %s", output)
	}
}

func decodeCleanupRecords(t *testing.T, output string) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(output))
	var records []map[string]any
	for {
		var record map[string]any
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			return records
		}
		if err != nil {
			t.Fatalf("decode cleanup log: %v", err)
		}
		records = append(records, record)
	}
}

func assertRetryLogCorrelation(t *testing.T, records []map[string]any) {
	t.Helper()
	if len(records) != 3 {
		t.Fatalf("cleanup log records = %d, want failure pair and one success terminal", len(records))
	}
	if records[0]["operation_id"] == records[2]["operation_id"] || records[0]["operation_id"] != records[1]["operation_id"] {
		t.Fatalf("operation IDs do not correlate per attempt: %#v", records)
	}
	if records[1]["lifecycle_id"] != records[0]["lifecycle_id"] || records[2]["lifecycle_id"] != records[0]["lifecycle_id"] {
		t.Fatalf("lifecycle ID changed across retries: %#v", records)
	}
	if records[2]["event"] != "lsp_cleanup_succeeded" || records[2]["action_result"] != "completed" {
		t.Fatalf("successful retry terminal = %#v", records[2])
	}
}

type provisionalCleanupTestOwner struct {
	terminateCalls atomic.Int32
	releaseCalls   atomic.Int32
	releaseErrors  []error
	identity       hiddenexec.ProcessIdentity
}

type provisionalPIDOnlyOwner struct {
	pid int
}

type failingProvisionalEntropy struct{}

func (failingProvisionalEntropy) Read([]byte) (int, error) {
	return 0, errors.New("injected entropy failure")
}

func (o *provisionalPIDOnlyOwner) Terminate() error           { return nil }
func (o *provisionalPIDOnlyOwner) Wait(context.Context) error { return nil }
func (o *provisionalPIDOnlyOwner) Release() error             { return nil }
func (o *provisionalPIDOnlyOwner) PID() int                   { return o.pid }

type provisionalGateOwner struct {
	entered        chan struct{}
	release        chan struct{}
	terminateCalls atomic.Int32
	releaseCalls   atomic.Int32
}

func (o *provisionalGateOwner) Terminate() error {
	o.terminateCalls.Add(1)
	select {
	case <-o.entered:
	default:
		close(o.entered)
	}
	<-o.release
	return nil
}

func (o *provisionalGateOwner) Wait(context.Context) error { return nil }

func (o *provisionalGateOwner) Release() error {
	o.releaseCalls.Add(1)
	return nil
}

func (o *provisionalCleanupTestOwner) Terminate() error {
	o.terminateCalls.Add(1)
	return nil
}

func (o *provisionalCleanupTestOwner) Wait(context.Context) error { return nil }

func (o *provisionalCleanupTestOwner) Release() error {
	index := int(o.releaseCalls.Add(1)) - 1
	if index < len(o.releaseErrors) {
		return o.releaseErrors[index]
	}
	return nil
}

func (o *provisionalCleanupTestOwner) Identity() (hiddenexec.ProcessIdentity, error) {
	if o.identity == (hiddenexec.ProcessIdentity{}) {
		return hiddenexec.ProcessIdentity{}, errors.New("identity unavailable")
	}
	return o.identity, nil
}
