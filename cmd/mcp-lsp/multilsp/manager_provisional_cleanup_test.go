package multilsp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
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
		factory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
			return nil, factoryErr
		}),
		workspaces: make(map[string]*workspaceClient),
	}
	cfg := workspaceConfig{key: key, rootPath: t.TempDir(), rootURI: "file:///project", languageID: "go"}

	if _, err := mgr.createAndRegisterClient(context.Background(), cfg); !errors.Is(err, cleanupErr) {
		t.Fatalf("createAndRegisterClient() error = %v, want cleanup error", err)
	}
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

	if err := mgr.retryProvisionalClientCleanups(key); !errors.Is(err, cleanupErr) {
		t.Fatalf("first cleanup retry error = %v, want release failure", err)
	}
	mgr.mu.RLock()
	if got := len(mgr.provisionalCleanups[key]); got != 1 {
		t.Fatalf("pending owner count after failed retry = %d, want 1", got)
	}
	mgr.mu.RUnlock()
	if err := mgr.retryProvisionalClientCleanups(key); err != nil {
		t.Fatalf("second cleanup retry error = %v, want nil", err)
	}
	mgr.mu.RLock()
	if got := len(mgr.provisionalCleanups[key]); got != 0 {
		t.Fatalf("pending owner count after successful retry = %d, want 0", got)
	}
	mgr.mu.RUnlock()
	if got := owner.terminateCalls.Load(); got != 2 {
		t.Fatalf("Terminate calls = %d, want one per retry", got)
	}
}

func TestCleanupFailureLogsHashedWorkspaceAndUnknownAction(t *testing.T) {
	var logs bytes.Buffer
	mgr := &manager{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	key := "/Users/private/project/secret:go"
	state := mgr.newPendingClientShutdown(key, 7, nil, &provisionalCleanupTestOwner{})
	if err := mgr.observeCleanupFailure(state, errors.New("cleanup pending")); err != nil {
		t.Fatalf("observeCleanupFailure() error = %v", err)
	}
	output := logs.String()
	if strings.Contains(output, key) || strings.Contains(output, "workspace_key") {
		t.Fatalf("cleanup logs leaked raw workspace identity: %s", output)
	}
	if !strings.Contains(output, "workspace_hash") || !strings.Contains(output, "action_result") || !strings.Contains(output, "unknown") {
		t.Fatalf("cleanup logs missing redacted action metadata: %s", output)
	}
	if strings.Contains(output, "signal_sent") {
		t.Fatalf("known-owner cleanup logs asserted signal_sent: %s", output)
	}
	if strings.Count(output, state.operationID) < 2 || strings.Count(output, state.lifecycleID) != 2 {
		t.Fatalf("cleanup pair correlation missing: %s", output)
	}
}

func TestCleanupFailureWithoutIdentityDoesNotProbePIDZero(t *testing.T) {
	mgr := &manager{}
	state := mgr.newPendingClientShutdown("workspace", 1, nil, &provisionalCleanupTestOwner{})
	if err := mgr.observeCleanupFailure(state, errors.New("cleanup pending")); err != nil {
		t.Fatalf("observeCleanupFailure() error = %v", err)
	}
	if mgr.processObserver != nil {
		t.Fatal("cleanup failure without identity/PID initialized process observer")
	}
}

func TestCleanupFailureWithPIDOnlyOwnerEmitsReadOnlyPair(t *testing.T) {
	var logs bytes.Buffer
	key := "/Users/private/project/pid-only:go"
	mgr := &manager{
		logger:                  slog.New(slog.NewJSONHandler(&logs, nil)),
		processObservationStore: processobserve.NewMemoryStore(),
	}
	state := mgr.newPendingClientShutdown(key, 11, nil, &provisionalPIDOnlyOwner{pid: os.Getpid()})
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
	mgr := &manager{factory: ClientFactoryFunc(factory.newClient), workspaces: make(map[string]*workspaceClient)}
	key := "ensure-owner-gate:go"
	mgr.retainProvisionalClientCleanups(key, []pendingClientShutdown{mgr.newPendingClientShutdown(key, 3, nil, owner)})
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
		factory: ClientFactoryFunc((&provisionalCleanupFactory{clients: []Client{discarded}}).newClient),
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
	mgr.mu.RLock()
	workspace := mgr.workspaces[key]
	state := mgr.provisionalCleanups[key]
	mgr.mu.RUnlock()
	if workspace == nil || workspace.client != existing || workspace.generation != 12 || workspace.state != workspaceStateActive {
		t.Fatalf("replacement workspace mutated by stale cleanup: %#v", workspace)
	}
	if len(state) != 1 || state[0].generation != 13 {
		t.Fatalf("stale cleanup generation = %#v, want 13", state)
	}
	if err := mgr.retryProvisionalClientCleanups(key); err != nil {
		t.Fatalf("stale cleanup retry error = %v", err)
	}
	mgr.mu.RLock()
	workspace = mgr.workspaces[key]
	mgr.mu.RUnlock()
	if workspace == nil || workspace.client != existing || workspace.generation != 12 {
		t.Fatalf("replacement workspace changed after stale cleanup retry: %#v", workspace)
	}
}

func TestManagerCloseRetriesRetainedProcessTreeOwner(t *testing.T) {
	cleanupErr := errors.New("close owner release failed")
	owner := &provisionalCleanupTestOwner{releaseErrors: []error{cleanupErr, nil}, identity: hiddenexec.ProcessIdentity{PID: 4444}}
	mgr := &manager{workspaces: make(map[string]*workspaceClient)}
	key := "close-owner-retry:go"
	mgr.retainProvisionalClientCleanups(key, []pendingClientShutdown{mgr.newPendingClientShutdown(key, 5, nil, owner)})
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
	mgr := &manager{workspaces: make(map[string]*workspaceClient)}
	key := "recycler-owner-retry:go"
	mgr.retainProvisionalClientCleanups(key, []pendingClientShutdown{mgr.newPendingClientShutdown(key, 6, nil, owner)})
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
	mgr := &manager{factory: ClientFactoryFunc(factory.newClient), workspaces: make(map[string]*workspaceClient)}
	key := "owner-transaction-race:go"
	mgr.retainProvisionalClientCleanups(key, []pendingClientShutdown{mgr.newPendingClientShutdown(key, 9, nil, owner)})
	cfg := workspaceConfig{key: key, rootPath: t.TempDir(), rootURI: "file:///owner-transaction-race", languageID: "go"}
	pool := NewManagerPool(mgr, 1)
	recycler := newPoolRecycler(pool)

	ensureDone := make(chan error, 1)
	go func() {
		_, err := mgr.ensureClient(context.Background(), cfg)
		ensureDone <- err
	}()
	select {
	case <-owner.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("ensure did not enter owner transaction")
	}

	recyclerDone := make(chan struct{})
	go func() {
		recycler.retryProvisionalCleanups(mgr)
		close(recyclerDone)
	}()
	closeDone := make(chan error, 1)
	go func() { closeDone <- mgr.Close() }()
	close(owner.release)

	select {
	case <-recyclerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("recycler did not finish owner transaction race")
	}
	select {
	case <-ensureDone:
	case <-time.After(2 * time.Second):
		t.Fatal("ensure did not finish owner transaction race")
	}
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("manager Close did not finish owner transaction race")
	}
	if got := owner.terminateCalls.Load(); got != 1 {
		t.Fatalf("owner Terminate calls = %d, want one transaction", got)
	}
	if got := owner.releaseCalls.Load(); got != 1 {
		t.Fatalf("owner Release calls = %d, want one transaction", got)
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
