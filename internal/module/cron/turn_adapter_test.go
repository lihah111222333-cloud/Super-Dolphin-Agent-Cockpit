package cron

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turn "github.com/anthropic-ai/super-agent-v3/internal/module/turn"
)

// fakeSession is a minimal contract.Session used purely to thread a
// ThreadID through the adapter; StartTurn / Interrupt / etc. are not
// exercised (the adapter passes the session to turn.Service which
// is itself a fake here).
type fakeSession struct {
	threadID string
}

func (f *fakeSession) ThreadID() string                  { return f.threadID }
func (f *fakeSession) RolloutPath() string               { return "" }
func (f *fakeSession) Capabilities() providerdto.CapabilitySet { return nil }
func (f *fakeSession) StartTurn(context.Context, providerdto.TurnRequest) (contract.TurnHandle, error) {
	return nil, errors.New("fakeSession.StartTurn not used")
}
func (f *fakeSession) Interrupt(context.Context, providerdto.InterruptRequest) error {
	return nil
}
func (f *fakeSession) ForceComplete(context.Context, providerdto.ForceCompleteRequest) error {
	return nil
}
func (f *fakeSession) ListThreads(context.Context) ([]providerdto.ThreadRef, error) {
	return nil, nil
}
func (f *fakeSession) ForkThread(context.Context, providerdto.ForkRequest) (providerdto.ForkResult, error) {
	return providerdto.ForkResult{}, nil
}
func (f *fakeSession) ReadHistory(context.Context, string, int) ([]providerdto.Message, error) {
	return nil, nil
}
func (f *fakeSession) Configure(context.Context, providerdto.ThreadConfigPatch) error { return nil }
func (f *fakeSession) Close(context.Context) error                                      { return nil }
func (f *fakeSession) ForceStop() error                                                 { return nil }

// fakeResolver maps thread ids to fakeSession values; unknown ids
// return an error.
type fakeResolver struct {
	known map[string]contract.Session
	err   error
}

func (r *fakeResolver) ResolveSession(_ context.Context, threadID string) (contract.Session, error) {
	if r.err != nil {
		return nil, r.err
	}
	s, ok := r.known[threadID]
	if !ok {
		return nil, errors.New("session not found")
	}
	return s, nil
}

// fakeTurnService records PrepareTurn / StartTurn / TrackTurn /
// LookupByDedupeKey calls and returns controllable outcomes.
type fakeTurnService struct {
	turn.Service // embed to only override the 4 methods the adapter uses
	prepareFn    func(context.Context, contract.Session, turn.PrepareInput) (providerdto.TurnRequest, error)
	startFn      func(context.Context, contract.Session, providerdto.TurnRequest) (contract.TurnHandle, error)
	trackFn      func(context.Context, string) (turn.TurnStatus, error)
	lookupFn     func(context.Context, string) (turn.TurnStatus, bool, error)

	prepareCalls []turn.PrepareInput
	lookupCalls  []string
}

func (f *fakeTurnService) PrepareTurn(ctx context.Context, s contract.Session, in turn.PrepareInput) (providerdto.TurnRequest, error) {
	f.prepareCalls = append(f.prepareCalls, in)
	if f.prepareFn != nil {
		return f.prepareFn(ctx, s, in)
	}
	return providerdto.TurnRequest{LocalID: "turn-local-1", ThreadID: s.ThreadID()}, nil
}
func (f *fakeTurnService) StartTurn(ctx context.Context, s contract.Session, req providerdto.TurnRequest) (contract.TurnHandle, error) {
	if f.startFn != nil {
		return f.startFn(ctx, s, req)
	}
	return &fakeHandle{localID: req.LocalID}, nil
}
func (f *fakeTurnService) TrackTurn(ctx context.Context, localID string) (turn.TurnStatus, error) {
	if f.trackFn != nil {
		return f.trackFn(ctx, localID)
	}
	return turn.TurnStatus{LocalID: localID, State: "running"}, nil
}
func (f *fakeTurnService) LookupByDedupeKey(ctx context.Context, key string) (turn.TurnStatus, bool, error) {
	f.lookupCalls = append(f.lookupCalls, key)
	if f.lookupFn != nil {
		return f.lookupFn(ctx, key)
	}
	return turn.TurnStatus{}, false, nil
}

type fakeHandle struct {
	localID    string
	providerID string
}

func (h *fakeHandle) LocalID() string        { return h.localID }
func (h *fakeHandle) ProviderID() string     { return h.providerID }
func (h *fakeHandle) Done() <-chan struct{}  { ch := make(chan struct{}); close(ch); return ch }
func (h *fakeHandle) Err() error             { return nil }

// ----- StartTurn paths -----

func TestAdapterStartTurnRequiresThreadID(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{known: map[string]contract.Session{}})
	_, err := a.StartTurn(context.Background(), StartTurnRequest{AgentID: "a"})
	if !errors.Is(err, ErrJobNotBootstrapped) {
		t.Fatalf("want ErrJobNotBootstrapped, got %v", err)
	}
}

func TestAdapterStartTurnHappyPath(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{}
	sess := &fakeSession{threadID: "thread-1"}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{
		known: map[string]contract.Session{"thread-1": sess},
	})

	res, err := a.StartTurn(context.Background(), StartTurnRequest{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		Prompt:   "daily check",
		Provider: "codex",
		Model:    "gpt-5",
		CWD:      "/repo",
		Skills:   []string{"skill-a", " skill-b ", ""},
		Config:   json.RawMessage(`{"k":"v"}`),
	})
	if err != nil {
		t.Fatalf("StartTurn error = %v", err)
	}
	if res.TurnID != "turn-local-1" || res.ThreadID != "thread-1" || res.AgentID != "agent-1" {
		t.Fatalf("result = %+v", res)
	}
	if len(svc.prepareCalls) != 1 {
		t.Fatalf("want 1 PrepareTurn call, got %d", len(svc.prepareCalls))
	}
	got := svc.prepareCalls[0]
	if got.Prompt != "daily check" || got.Provider != "codex" || got.Model != "gpt-5" || got.CWD != "/repo" || got.AgentID != "agent-1" {
		t.Fatalf("PrepareInput forward wrong: %+v", got)
	}
	if len(got.Skills) != 2 || got.Skills[0].Name != "skill-a" || got.Skills[1].Name != "skill-b" {
		t.Fatalf("Skills trim/skip empty wrong: %+v", got.Skills)
	}
	if got.ThreadRuntimeConfig == nil || got.ThreadRuntimeConfig["k"] != "v" {
		t.Fatalf("ThreadRuntimeConfig not decoded: %+v", got.ThreadRuntimeConfig)
	}
}

func TestAdapterStartTurnResolverError(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{err: errors.New("db down")})
	_, err := a.StartTurn(context.Background(), StartTurnRequest{ThreadID: "t"})
	if err == nil || !stringContains(err.Error(), "resolve session") {
		t.Fatalf("want resolve error wrap, got %v", err)
	}
}

func TestAdapterStartTurnPrepareError(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{
		prepareFn: func(context.Context, contract.Session, turn.PrepareInput) (providerdto.TurnRequest, error) {
			return providerdto.TurnRequest{}, errors.New("boom")
		},
	}
	sess := &fakeSession{threadID: "thread-1"}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{
		known: map[string]contract.Session{"thread-1": sess},
	})
	_, err := a.StartTurn(context.Background(), StartTurnRequest{ThreadID: "thread-1"})
	if err == nil || !stringContains(err.Error(), "prepare turn") {
		t.Fatalf("want prepare error, got %v", err)
	}
}

func TestAdapterStartTurnEmptyLocalIDIsError(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{
		startFn: func(context.Context, contract.Session, providerdto.TurnRequest) (contract.TurnHandle, error) {
			return &fakeHandle{localID: "  "}, nil
		},
	}
	sess := &fakeSession{threadID: "thread-1"}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{
		known: map[string]contract.Session{"thread-1": sess},
	})
	_, err := a.StartTurn(context.Background(), StartTurnRequest{ThreadID: "thread-1"})
	if err == nil || !stringContains(err.Error(), "empty local id") {
		t.Fatalf("want empty-local-id error, got %v", err)
	}
}

// ----- Observe paths -----

func TestAdapterObserveTranslatesTurnNotFound(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{
		trackFn: func(context.Context, string) (turn.TurnStatus, error) {
			return turn.TurnStatus{}, errors.New("turn not found")
		},
	}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{})
	err := a.Observe(context.Background(), "turn-1")
	if !errors.Is(err, ErrTurnNotFound) {
		t.Fatalf("want ErrTurnNotFound, got %v", err)
	}
}

func TestAdapterObserveWrapsOtherErrors(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{
		trackFn: func(context.Context, string) (turn.TurnStatus, error) {
			return turn.TurnStatus{}, errors.New("db offline")
		},
	}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{})
	err := a.Observe(context.Background(), "turn-1")
	if err == nil || errors.Is(err, ErrTurnNotFound) {
		t.Fatalf("generic error should wrap, got %v", err)
	}
}

func TestAdapterObserveRequiresTurnID(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{})
	if err := a.Observe(context.Background(), "  "); err == nil {
		t.Fatal("empty turn id should error")
	}
}

// ----- LookupByDedupeKey -----

// TestAdapterLookupByDedupeKeyEmptyKey ensures callers that haven't
// opted into dedupe (empty key) get Found=false without reaching the
// tracker — the short-circuit avoids spurious service work for every
// scheduler tick.
func TestAdapterLookupByDedupeKeyEmptyKey(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{})
	got, err := a.LookupByDedupeKey(context.Background(), "  ")
	if err != nil {
		t.Fatalf("LookupByDedupeKey err = %v", err)
	}
	if got.Found {
		t.Fatal("empty key must report Found=false")
	}
	if len(svc.lookupCalls) != 0 {
		t.Fatalf("empty key should short-circuit, got %d calls", len(svc.lookupCalls))
	}
}

// TestAdapterLookupByDedupeKeyMiss verifies the scheduler sees
// Found=false when the tracker has no matching non-terminal turn.
// This is the common path; the scheduler must treat it as "never
// submitted" per the P1b plan.
func TestAdapterLookupByDedupeKeyMiss(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{})
	got, err := a.LookupByDedupeKey(context.Background(), "dedupe-miss")
	if err != nil {
		t.Fatalf("LookupByDedupeKey err = %v", err)
	}
	if got.Found {
		t.Fatal("tracker miss must report Found=false")
	}
	if len(svc.lookupCalls) != 1 || svc.lookupCalls[0] != "dedupe-miss" {
		t.Fatalf("want one forwarded call with trimmed key, got %v", svc.lookupCalls)
	}
}

// TestAdapterLookupByDedupeKeyHit verifies the adapter forwards the
// tracker's LocalID as ObservedTurn.TurnID, matching what StartTurn
// would have recorded on the run row.
func TestAdapterLookupByDedupeKeyHit(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{
		lookupFn: func(context.Context, string) (turn.TurnStatus, bool, error) {
			return turn.TurnStatus{LocalID: "turn-local-42", ProviderID: "provider-7", State: "running"}, true, nil
		},
	}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{})
	got, err := a.LookupByDedupeKey(context.Background(), " dedupe-42 ")
	if err != nil {
		t.Fatalf("LookupByDedupeKey err = %v", err)
	}
	if !got.Found || got.TurnID != "turn-local-42" {
		t.Fatalf("want Found=true TurnID=turn-local-42, got %+v", got)
	}
	if len(svc.lookupCalls) != 1 || svc.lookupCalls[0] != "dedupe-42" {
		t.Fatalf("key must be trimmed before forward, got %v", svc.lookupCalls)
	}
}

// TestAdapterLookupByDedupeKeyServiceError wraps the underlying error
// so the scheduler can log it while still reporting Found=false.
func TestAdapterLookupByDedupeKeyServiceError(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{
		lookupFn: func(context.Context, string) (turn.TurnStatus, bool, error) {
			return turn.TurnStatus{}, false, errors.New("tracker offline")
		},
	}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{})
	got, err := a.LookupByDedupeKey(context.Background(), "d")
	if err == nil || !stringContains(err.Error(), "lookup by dedupe key") {
		t.Fatalf("want wrapped error, got %v", err)
	}
	if got.Found {
		t.Fatal("error case must keep Found=false")
	}
}

// TestAdapterStartTurnForwardsDedupeKey asserts the adapter threads
// StartTurnRequest.DedupeKey into turn.PrepareInput so the tracker
// can register it during PrepareTurn/StartTurn.
func TestAdapterStartTurnForwardsDedupeKey(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{}
	sess := &fakeSession{threadID: "thread-1"}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{
		known: map[string]contract.Session{"thread-1": sess},
	})
	_, err := a.StartTurn(context.Background(), StartTurnRequest{
		ThreadID:  "thread-1",
		DedupeKey: " dedupe-xyz ",
	})
	if err != nil {
		t.Fatalf("StartTurn err = %v", err)
	}
	if len(svc.prepareCalls) != 1 || svc.prepareCalls[0].DedupeKey != "dedupe-xyz" {
		t.Fatalf("want DedupeKey trimmed + forwarded, got %+v", svc.prepareCalls)
	}
}

// ----- decodeRuntimeConfig -----

func TestDecodeRuntimeConfigMalformedReturnsNil(t *testing.T) {
	t.Parallel()
	if out := decodeRuntimeConfig(json.RawMessage("not json")); out != nil {
		t.Fatalf("malformed JSON should decode to nil, got %+v", out)
	}
	if out := decodeRuntimeConfig(nil); out != nil {
		t.Fatalf("nil JSON should decode to nil, got %+v", out)
	}
}

// ----- Fallback wiring -----

func TestProvideTurnSubmitterFallsBackToNoop(t *testing.T) {
	t.Parallel()
	// nil service + nil resolver -> Noop per the optional-dep fallback.
	sub := provideTurnSubmitter(turnSubmitterParams{})
	if _, err := sub.StartTurn(context.Background(), StartTurnRequest{}); !errors.Is(err, ErrSubmitterNotWired) {
		t.Fatalf("noop fallback should StartTurn with ErrSubmitterNotWired, got %v", err)
	}
}

func TestProvideTurnSubmitterPromotesWhenBothDepsPresent(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{}
	res := &fakeResolver{known: map[string]contract.Session{"t": &fakeSession{threadID: "t"}}}
	sub := provideTurnSubmitter(turnSubmitterParams{Service: svc, Resolver: res})
	if _, ok := sub.(*TurnServiceAdapter); !ok {
		t.Fatalf("want *TurnServiceAdapter, got %T", sub)
	}
}

func stringContains(s, sub string) bool {
	// Mirror the small helper from store_test.go so this file doesn't
	// depend on strings imports beyond what stdlib already provides.
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Ensure unused imports don't trip go vet when the test file is
// trimmed later. The compiler will also catch this — this line just
// documents the intended dependency set.
var _ = sharedto.InputItem{}
