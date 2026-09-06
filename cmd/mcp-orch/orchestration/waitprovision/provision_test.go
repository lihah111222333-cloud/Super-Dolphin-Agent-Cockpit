package waitprovision

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type fakePort struct {
	mu           sync.Mutex
	grants       []accesswait.AccessGrantRequest
	waits        []accesswait.WaitRequest
	cancels      []accesswait.CancelRequest
	revokes      []accesswait.RevokeGrantRequest
	grantByID    map[string]accesswait.AccessGrant
	grantErr     error
	failWait     bool
	unknownGrant bool
	unknownWait  bool
}

func (f *fakePort) CreateGrant(_ context.Context, r accesswait.AccessGrantRequest) (accesswait.AccessGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants = append(f.grants, r)
	grant := accesswait.AccessGrant{GrantID: r.GrantID, CallerPrincipalRef: r.CallerPrincipalRef, ScopeRef: r.ScopeRef, Source: r.Source, Target: r.Target, CanRead: r.CanRead, CanSend: r.CanSend, IdempotencyNamespace: r.IdempotencyNamespace, IdempotencyKeyDigest: r.IdempotencyKeyDigest, RequestDigest: r.RequestDigest, IssuedAt: r.IssuedAt, ExpiresAt: r.ExpiresAt, Status: "active", Revision: 1, SchemaVersion: 1, CreatedAt: r.IssuedAt, UpdatedAt: r.IssuedAt}
	if f.grantByID == nil {
		f.grantByID = make(map[string]accesswait.AccessGrant)
	}
	f.grantByID[grant.GrantID] = grant
	if f.unknownGrant {
		return grant, errors.New("grant outcome unknown")
	}
	return grant, nil
}
func (f *fakePort) GetGrant(_ context.Context, grantID string) (accesswait.AccessGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.grantErr != nil {
		return accesswait.AccessGrant{}, f.grantErr
	}
	grant, ok := f.grantByID[grantID]
	if !ok {
		return accesswait.AccessGrant{}, accesswait.ErrAccessNotFound
	}
	return grant, nil
}
func (f *fakePort) CreateWait(_ context.Context, r accesswait.WaitRequest) (accesswait.WaitHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waits = append(f.waits, r)
	if f.failWait {
		handle := accesswait.WaitHandle{WaitHandleID: "wait:unknown", GrantID: r.GrantID, GrantRevision: r.GrantRevision}
		if f.unknownWait {
			return handle, errors.New("wait outcome unknown")
		}
		return accesswait.WaitHandle{}, errors.New("wait failed")
	}
	return accesswait.WaitHandle{WaitHandleID: "wait:" + r.GrantID, GrantID: r.GrantID, GrantRevision: r.GrantRevision, CallerPrincipalRef: r.CallerPrincipalRef, ScopeRef: r.ScopeRef, SourceAgentID: "source", Target: accesswait.FenceSnapshot{AgentID: r.Selector.TargetAgentID, ReservationID: "reservation-target", ReservationGeneration: 1, IncarnationID: "inc-target", IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: digest('b'), SourceFenceRef: "fence/target"}, Selector: r.Selector, AfterSequence: r.AfterSequence, AfterCursor: r.AfterCursor, DeadlineAt: r.DeadlineAt, IdempotencyNamespace: r.IdempotencyNamespace, IdempotencyKeyDigest: r.IdempotencyKeyDigest, RequestDigest: r.RequestDigest, Status: "pending", Revision: 1, CreatedAt: r.DeadlineAt.Add(-time.Second), UpdatedAt: r.DeadlineAt.Add(-time.Second)}, nil
}
func (f *fakePort) CancelWait(_ context.Context, r accesswait.CancelRequest) (accesswait.WaitResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, r)
	return accesswait.WaitResult{}, nil
}
func (f *fakePort) RevokeGrant(_ context.Context, r accesswait.RevokeGrantRequest) (accesswait.AccessGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokes = append(f.revokes, r)
	return accesswait.AccessGrant{}, nil
}

type fakeReader struct {
	result Result
	err    error
}

func (r fakeReader) ReadWaitProvision(context.Context, string) (Result, error) {
	return r.result, r.err
}

// durableFakePort models the accesswait owner's idempotency boundary. Calls
// may be retried when the external replay reader is unavailable, but only the
// first request for each namespace/key pair creates a durable row.
type durableFakePort struct {
	mu sync.Mutex

	grantRequests map[string]accesswait.AccessGrantRequest
	grants        map[string]accesswait.AccessGrant
	waitRequests  map[string]accesswait.WaitRequest
	waits         map[string]accesswait.WaitHandle
	grantAttempts int
	waitAttempts  int
	grantCreates  int
	waitCreates   int
}

func newDurableFakePort() *durableFakePort {
	return &durableFakePort{
		grantRequests: make(map[string]accesswait.AccessGrantRequest),
		grants:        make(map[string]accesswait.AccessGrant),
		waitRequests:  make(map[string]accesswait.WaitRequest),
		waits:         make(map[string]accesswait.WaitHandle),
	}
}

func (f *durableFakePort) CreateGrant(_ context.Context, r accesswait.AccessGrantRequest) (accesswait.AccessGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grantAttempts++
	key := durableFakeKey(r.IdempotencyNamespace, r.IdempotencyKeyDigest)
	if prior, ok := f.grantRequests[key]; ok {
		if !reflect.DeepEqual(prior, r) {
			return f.grants[key], accesswait.ErrAccessConflict
		}
		return f.grants[key], nil
	}
	grant := accesswait.AccessGrant{
		GrantID: r.GrantID, CallerPrincipalRef: r.CallerPrincipalRef, ScopeRef: r.ScopeRef,
		Source: r.Source, Target: r.Target, CanRead: r.CanRead, CanSend: r.CanSend,
		IdempotencyNamespace: r.IdempotencyNamespace, IdempotencyKeyDigest: r.IdempotencyKeyDigest,
		RequestDigest: r.RequestDigest, IssuedAt: r.IssuedAt, ExpiresAt: r.ExpiresAt,
		Status: "active", Revision: 1, SchemaVersion: 1, CreatedAt: r.IssuedAt, UpdatedAt: r.IssuedAt,
	}
	f.grantRequests[key] = r
	f.grants[key] = grant
	f.grantCreates++
	return grant, nil
}

func (f *durableFakePort) CreateWait(_ context.Context, r accesswait.WaitRequest) (accesswait.WaitHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waitAttempts++
	key := durableFakeKey(r.IdempotencyNamespace, r.IdempotencyKeyDigest)
	if prior, ok := f.waitRequests[key]; ok {
		if !reflect.DeepEqual(prior, r) {
			return f.waits[key], accesswait.ErrAccessConflict
		}
		return f.waits[key], nil
	}
	handle := accesswait.WaitHandle{
		WaitHandleID: "wait:" + r.GrantID, GrantID: r.GrantID, GrantRevision: r.GrantRevision,
		CallerPrincipalRef: r.CallerPrincipalRef, ScopeRef: r.ScopeRef, SourceAgentID: "source",
		Target:   accesswait.FenceSnapshot{AgentID: r.Selector.TargetAgentID, ReservationID: "reservation-target", ReservationGeneration: 1, IncarnationID: "inc-target", IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: digest('b'), SourceFenceRef: "fence/target"},
		Selector: r.Selector, AfterSequence: r.AfterSequence, AfterCursor: r.AfterCursor,
		DeadlineAt: r.DeadlineAt, IdempotencyNamespace: r.IdempotencyNamespace,
		IdempotencyKeyDigest: r.IdempotencyKeyDigest, RequestDigest: r.RequestDigest,
		Status: "pending", Revision: 1, CreatedAt: r.DeadlineAt.Add(-time.Second), UpdatedAt: r.DeadlineAt.Add(-time.Second),
	}
	f.waitRequests[key] = r
	f.waits[key] = handle
	f.waitCreates++
	return handle, nil
}

func (f *durableFakePort) GetGrant(_ context.Context, grantID string) (accesswait.AccessGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, grant := range f.grants {
		if grant.GrantID == grantID {
			return grant, nil
		}
	}
	return accesswait.AccessGrant{}, accesswait.ErrAccessNotFound
}

func (f *durableFakePort) CancelWait(context.Context, accesswait.CancelRequest) (accesswait.WaitResult, error) {
	return accesswait.WaitResult{}, nil
}

func (f *durableFakePort) RevokeGrant(context.Context, accesswait.RevokeGrantRequest) (accesswait.AccessGrant, error) {
	return accesswait.AccessGrant{}, nil
}

func durableFakeKey(namespace, digest string) string { return namespace + "\x00" + digest }

func (f *durableFakePort) counts() (grantAttempts, waitAttempts, grantCreates, waitCreates int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.grantAttempts, f.waitAttempts, f.grantCreates, f.waitCreates
}

func digest(ch byte) string { return strings.Repeat(string(ch), 64) }

func TestNewRejectsTypedNilPorts(t *testing.T) {
	var access *fakePort
	var cleanup *fakePort
	var reader *fakeReader
	if adapter, err := New(Config{Access: access, Cleanup: &fakePort{}, Reader: fakeReader{err: ErrNotFound}}); adapter != nil || !errors.Is(err, ErrNilPort) {
		t.Fatalf("typed nil access=(%#v,%v), want ErrNilPort", adapter, err)
	}
	if adapter, err := New(Config{Access: &fakePort{}, Cleanup: cleanup, Reader: fakeReader{err: ErrNotFound}}); adapter != nil || !errors.Is(err, ErrNilPort) {
		t.Fatalf("typed nil cleanup=(%#v,%v), want ErrNilPort", adapter, err)
	}
	if adapter, err := New(Config{Access: &fakePort{}, Cleanup: &fakePort{}, Reader: reader}); adapter != nil || !errors.Is(err, ErrNilPort) {
		t.Fatalf("typed nil reader=(%#v,%v), want ErrNilPort", adapter, err)
	}
}

func fixtureRequest(now time.Time) Request {
	source := accesswait.FenceSnapshot{AgentID: "source", ReservationID: "reservation-source", ReservationGeneration: 1, IncarnationID: "inc-source", IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: digest('a'), SourceFenceRef: "fence/source"}
	target := accesswait.FenceSnapshot{AgentID: "target", ReservationID: "reservation-target", ReservationGeneration: 1, IncarnationID: "inc-target", IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: digest('b'), SourceFenceRef: "fence/target"}
	selector := accesswait.EventSelector{Version: 1, SourceID: "task/source", SourceEpoch: "inc-source", Lane: eventstore.LaneOutput, TargetAgentID: "target", EventTypes: []string{"turn.completed"}}
	return Request{OperationKey: "op-1", OperationDigest: digest('c'), CallerPrincipalRef: "caller", ScopeRef: "scope", SourceFence: source, Targets: []Target{{Key: "target-1", TargetFence: target, Selector: selector}}, DeadlineAt: now.Add(30 * time.Second).UTC()}
}

func TestProvisionMintsWaitHandleAndPreservesDigest(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	fake := &fakePort{}
	a, err := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Provision(context.Background(), fixtureRequest(now))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Wait.WaitHandleID == "" {
		t.Fatalf("result=%+v", result)
	}
	if len(fake.waits) != 1 || fake.waits[0].WaitHandleID != "" {
		t.Fatalf("wait request did not delegate server mint: %+v", fake.waits)
	}
	if fake.grants[0].RequestDigest != digest('c') || fake.waits[0].RequestDigest != digest('c') {
		t.Fatal("operation digest was not carried to both owner calls")
	}
}

func TestProvisionReplayReusesDurableOwnerWhenReaderUnavailable(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	req := fixtureRequest(now)
	fake := newDurableFakePort()
	a, err := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.Provision(context.Background(), req)
	if err != nil {
		t.Fatalf("first Provision() error = %v", err)
	}
	replayed, err := a.Provision(context.Background(), req)
	if err != nil {
		t.Fatalf("replayed Provision() error = %v", err)
	}
	if !reflect.DeepEqual(replayed, first) {
		t.Fatalf("replayed result = %#v, want exact durable result %#v", replayed, first)
	}
	if got, want := first.Items[0].Grant.GrantID, grantIDFor(req, req.Targets[0]); got != want {
		t.Fatalf("grant id = %q, want deterministic opaque id %q", got, want)
	}
	grantAttempts, waitAttempts, grantCreates, waitCreates := fake.counts()
	if grantAttempts != 2 || waitAttempts != 2 || grantCreates != 1 || waitCreates != 1 {
		t.Fatalf("owner attempts/creates = grants %d/%d waits %d/%d, want two retries but one durable row each", grantAttempts, grantCreates, waitAttempts, waitCreates)
	}
}

func TestProvisionReplayRejectsDigestAndTargetChanges(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	req := fixtureRequest(now)
	fake := &fakePort{}
	producer, err := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := producer.Provision(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{result: canonical}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	digestConflict := req
	digestConflict.OperationDigest = digest('d')
	if _, err := replay.Provision(context.Background(), digestConflict); !errors.Is(err, ErrDigestConflict) {
		t.Fatalf("digest change error = %v, want ErrDigestConflict", err)
	}
	targetConflict := req
	targetConflict.Targets = append([]Target(nil), req.Targets...)
	targetConflict.Targets[0].TargetFence.BindingDigest = digest('e')
	if _, err := replay.Provision(context.Background(), targetConflict); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("target change error = %v, want ErrReplayConflict", err)
	}
}

func TestProvisionConcurrentReplayHasOneDurableWinner(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	req := fixtureRequest(now)
	fake := newDurableFakePort()
	a, err := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 24
	start := make(chan struct{})
	results := make(chan struct {
		result Result
		err    error
	}, attempts)
	var group sync.WaitGroup
	group.Add(attempts)
	for range attempts {
		runtimesafe.SafeGo(t.Context(), nil, "waitprovision.test.concurrent-provision", func(context.Context) {
			defer group.Done()
			<-start
			result, err := a.Provision(context.Background(), req)
			results <- struct {
				result Result
				err    error
			}{result: result, err: err}
		})
	}
	close(start)
	group.Wait()
	assertConcurrentProvisionResults(t, results, attempts)
	grantAttempts, waitAttempts, grantCreates, waitCreates := fake.counts()
	if grantAttempts != attempts || waitAttempts != attempts || grantCreates != 1 || waitCreates != 1 {
		t.Fatalf("concurrent owner attempts/creates = grants %d/%d waits %d/%d, want %d attempts and one winner each", grantAttempts, grantCreates, waitAttempts, waitCreates, attempts)
	}
}

// assertConcurrentProvisionResults 校验并发重放返回同一个持久化结果。
func assertConcurrentProvisionResults(t *testing.T, results chan struct {
	result Result
	err    error
}, attempts int) {
	t.Helper()
	var winner Result
	for i := 0; i < attempts; i++ {
		outcome := <-results
		if outcome.err != nil {
			t.Fatalf("concurrent Provision() error = %v", outcome.err)
		}
		if winner.Items == nil {
			winner = outcome.result
			continue
		}
		if !reflect.DeepEqual(outcome.result, winner) {
			t.Fatalf("concurrent replay result = %#v, want winner %#v", outcome.result, winner)
		}
	}
}

func TestProvisionRejectsNonCanonicalTargetCursorBeforeOwnerCalls(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	req := fixtureRequest(now)
	req.Targets[0].AfterSequence = 1
	fake := &fakePort{}
	a, err := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing target cursor error = %v, want ErrInvalidRequest", err)
	}
	if len(fake.grants) != 0 || len(fake.waits) != 0 {
		t.Fatalf("invalid target cursor caused owner calls: grants=%d waits=%d", len(fake.grants), len(fake.waits))
	}
	canonical, err := eventstore.CanonicalCursor(req.Targets[0].Selector.SourceID, req.Targets[0].Selector.SourceEpoch, req.Targets[0].Selector.Lane, req.Targets[0].AfterSequence)
	if err != nil {
		t.Fatal(err)
	}
	req.Targets[0].AfterCursor = canonical + "/tampered"
	if _, err := a.Provision(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-canonical target cursor error = %v, want ErrInvalidRequest", err)
	}
	if len(fake.grants) != 0 || len(fake.waits) != 0 {
		t.Fatalf("non-canonical target cursor caused owner calls: grants=%d waits=%d", len(fake.grants), len(fake.waits))
	}
}

func TestProvisionReplayAndDigestConflict(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	req := fixtureRequest(now)
	fake := &fakePort{}
	a, _ := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	first, err := a.Provision(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	replayAdapter, _ := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{result: first}, Clock: func() time.Time { return now }})
	replayed, err := replayAdapter.Provision(context.Background(), req)
	if err != nil || len(replayed.Items) != 1 {
		t.Fatalf("replay result=%+v err=%v", replayed, err)
	}
	conflict := req
	conflict.OperationDigest = digest('d')
	if _, err := replayAdapter.Provision(context.Background(), conflict); !errors.Is(err, ErrDigestConflict) {
		t.Fatalf("digest conflict err=%v", err)
	}
}

func TestProvisionReplayRejectsImmutableFieldDrift(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	req := fixtureRequest(now)
	fake := &fakePort{}
	producer, err := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := producer.Provision(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*Result){
		"grant capability":            func(result *Result) { result.Items[0].Grant.CanRead = false },
		"grant idempotency namespace": func(result *Result) { result.Items[0].Grant.IdempotencyNamespace = "other" },
		"grant status":                func(result *Result) { result.Items[0].Grant.Status = "revoked" },
		"wait idempotency digest":     func(result *Result) { result.Items[0].Wait.IdempotencyKeyDigest = digest('z') },
		"wait selector":               func(result *Result) { result.Items[0].Wait.Selector.SourceEpoch = "other" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			mutated := canonical
			mutated.Items = append([]Item(nil), canonical.Items...)
			mutated.Items[0] = canonical.Items[0]
			mutate(&mutated)
			replay, err := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{result: mutated}, Clock: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := replay.Provision(context.Background(), req); !errors.Is(err, ErrReplayConflict) {
				t.Fatalf("replay drift error=%v, want ErrReplayConflict", err)
			}
		})
	}
}

func TestProvisionPartialFailureRetainsGrantForExplicitCleanup(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	fake := &fakePort{failWait: true}
	a, _ := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	result, err := a.Provision(context.Background(), fixtureRequest(now))
	if !errors.Is(err, ErrPartial) || len(result.Items) != 1 || result.Items[0].Grant.GrantID == "" || result.Items[0].Wait.WaitHandleID != "" {
		t.Fatalf("partial result=%+v err=%v", result, err)
	}
	if err := a.Cleanup(context.Background(), fixtureRequest(now), result, "partial-failure"); err != nil {
		t.Fatalf("partial cleanup err=%v", err)
	}
	if len(fake.revokes) != 1 || len(fake.cancels) != 0 {
		t.Fatalf("partial cleanup calls cancel=%d revoke=%d", len(fake.cancels), len(fake.revokes))
	}
}

func TestProvisionRetainsUnknownOwnerOutputs(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	fake := &fakePort{failWait: true, unknownWait: true}
	a, _ := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	result, err := a.Provision(context.Background(), fixtureRequest(now))
	if !errors.Is(err, ErrPartial) || len(result.Items) != 1 || result.Items[0].Grant.GrantID == "" || result.Items[0].Wait.WaitHandleID == "" {
		t.Fatalf("unknown outputs were dropped: result=%+v err=%v", result, err)
	}
	fake = &fakePort{unknownGrant: true}
	a, _ = New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	result, err = a.Provision(context.Background(), fixtureRequest(now))
	if !errors.Is(err, ErrPartial) || len(result.Items) != 1 || result.Items[0].Grant.GrantID == "" {
		t.Fatalf("unknown grant was dropped: result=%+v err=%v", result, err)
	}
}

func TestCleanupUsesOnlyExplicitCancelAndRevokePorts(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	fake := &fakePort{}
	a, err := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	req := fixtureRequest(now)
	// A canonical complete result exercises strict cleanup validation, while the
	// explicit narrow ports remain the only side-effect path.
	result, err := a.Provision(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Cleanup(context.Background(), req, result, "caller-request"); err != nil {
		t.Fatal(err)
	}
	if len(fake.cancels) != 1 || len(fake.revokes) != 1 {
		t.Fatalf("cleanup calls cancel=%d revoke=%d", len(fake.cancels), len(fake.revokes))
	}
}

func TestCleanupAcceptsUnknownOpaqueIDsWithKnownRevision(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	fake := &fakePort{}
	a, err := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	req := fixtureRequest(now)
	target := req.Targets[0]
	grant, err := fake.CreateGrant(context.Background(), accesswait.AccessGrantRequest{
		GrantID: "grant:unknown", CallerPrincipalRef: req.CallerPrincipalRef, ScopeRef: req.ScopeRef,
		Source: req.SourceFence, Target: target.TargetFence, CanRead: true,
		IdempotencyNamespace: expectedGrantNamespace(req, target), IdempotencyKeyDigest: digestFor("grant", req.OperationKey, req.OperationDigest, target.Key), RequestDigest: req.OperationDigest,
		IssuedAt: now, ExpiresAt: req.DeadlineAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := Result{OperationKey: req.OperationKey, OperationDigest: req.OperationDigest, Items: []Item{{
		Key:   "target-1",
		Grant: accesswait.AccessGrant{GrantID: grant.GrantID, Revision: grant.Revision, Source: req.SourceFence, Target: target.TargetFence},
		Wait:  accesswait.WaitHandle{WaitHandleID: "wait:unknown"},
	}}}
	if err := a.Cleanup(context.Background(), req, result, "unknown-owner-outcome"); err != nil {
		t.Fatal(err)
	}
	if len(fake.cancels) != 1 || fake.cancels[0].GrantRevision != grant.Revision || len(fake.revokes) != 1 || fake.revokes[0].GrantRevision != grant.Revision {
		t.Fatalf("cleanup calls cancels=%+v revokes=%+v", fake.cancels, fake.revokes)
	}
}

type cleanupOnly struct{ port *fakePort }

func (c cleanupOnly) CancelWait(ctx context.Context, request accesswait.CancelRequest) (accesswait.WaitResult, error) {
	return c.port.CancelWait(ctx, request)
}

func (c cleanupOnly) RevokeGrant(ctx context.Context, request accesswait.RevokeGrantRequest) (accesswait.AccessGrant, error) {
	return c.port.RevokeGrant(ctx, request)
}

func TestCleanupRejectsPartialResultWithoutDurableGrantReader(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	fake := &fakePort{}
	req := fixtureRequest(now)
	grant, err := fake.CreateGrant(context.Background(), accesswait.AccessGrantRequest{
		GrantID: "grant:opaque-no-reader", CallerPrincipalRef: req.CallerPrincipalRef, ScopeRef: req.ScopeRef,
		Source: req.SourceFence, Target: req.Targets[0].TargetFence, CanRead: true,
		IdempotencyNamespace: expectedGrantNamespace(req, req.Targets[0]), IdempotencyKeyDigest: digestFor("grant", req.OperationKey, req.OperationDigest, req.Targets[0].Key), RequestDigest: req.OperationDigest,
		IssuedAt: now, ExpiresAt: req.DeadlineAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{Access: fake, Cleanup: cleanupOnly{port: fake}, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result := Result{OperationKey: req.OperationKey, OperationDigest: req.OperationDigest, Items: []Item{{Key: req.Targets[0].Key, Grant: accesswait.AccessGrant{GrantID: grant.GrantID, Revision: grant.Revision, Source: req.SourceFence, Target: req.Targets[0].TargetFence}}}}
	if err := a.Cleanup(context.Background(), req, result, "unknown-owner-outcome"); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("Cleanup(partial without reader) = %v, want ErrReplayConflict", err)
	}
	if len(fake.revokes) != 0 || len(fake.cancels) != 0 {
		t.Fatalf("partial cleanup without reader performed side effects: cancels=%d revokes=%d", len(fake.cancels), len(fake.revokes))
	}
}

func TestCleanupRejectsForgedPartialGrantFromAnotherOperation(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	fake := &fakePort{}
	req := fixtureRequest(now)
	target := req.Targets[0]
	otherDigest := digest('d')
	grant, err := fake.CreateGrant(context.Background(), accesswait.AccessGrantRequest{
		GrantID: "grant:forged", CallerPrincipalRef: req.CallerPrincipalRef, ScopeRef: req.ScopeRef,
		Source: req.SourceFence, Target: target.TargetFence, CanRead: true,
		IdempotencyNamespace: "wait-provision/other-operation/grant/target-1", IdempotencyKeyDigest: digestFor("grant", "other-operation", otherDigest, target.Key), RequestDigest: otherDigest,
		IssuedAt: now, ExpiresAt: req.DeadlineAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{Access: fake, Cleanup: fake, Reader: fakeReader{err: ErrNotFound}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result := Result{OperationKey: req.OperationKey, OperationDigest: req.OperationDigest, Items: []Item{{Key: target.Key, Grant: accesswait.AccessGrant{GrantID: grant.GrantID, Revision: grant.Revision, Source: req.SourceFence, Target: target.TargetFence}}}}
	if err := a.Cleanup(context.Background(), req, result, "forged-owner-outcome"); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("Cleanup(forged partial grant) = %v, want ErrReplayConflict", err)
	}
	if len(fake.revokes) != 0 || len(fake.cancels) != 0 {
		t.Fatalf("forged partial cleanup performed side effects: cancels=%d revokes=%d", len(fake.cancels), len(fake.revokes))
	}
}
