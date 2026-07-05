package cron

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// fakeSession 是只携带 ThreadID 的 contract.Session 假实现。
// 这些测试把 turn 执行交给 fake CronTurnExecutor，因此不触发真实 StartTurn/Interrupt。
type fakeSession struct {
	fakeSessionInfo
	fakeSessionControl

	threadID string
}

type fakeSessionInfo struct{}

func (fakeSessionInfo) RolloutPath() string                     { return "" }
func (fakeSessionInfo) Capabilities() providerdto.CapabilitySet { return nil }

type fakeSessionControl struct{}

func (f *fakeSession) ThreadID() string { return f.threadID }
func (fakeSessionControl) StartTurn(context.Context, providerdto.TurnRequest) (contract.TurnHandle, error) {
	return nil, errors.New("fakeSession.StartTurn not used")
}
func (fakeSessionControl) Interrupt(context.Context, providerdto.InterruptRequest) error {
	return nil
}
func (fakeSessionControl) ForceComplete(context.Context, providerdto.ForceCompleteRequest) error {
	return nil
}
func (fakeSessionControl) ListThreads(context.Context) ([]providerdto.ThreadRef, error) {
	return nil, nil
}
func (fakeSessionControl) ForkThread(context.Context, providerdto.ForkRequest) (providerdto.ForkResult, error) {
	return providerdto.ForkResult{}, nil
}
func (fakeSessionControl) ReadHistory(context.Context, string, int) ([]providerdto.Message, error) {
	return nil, nil
}
func (fakeSessionControl) Configure(context.Context, providerdto.ThreadConfigPatch) error { return nil }
func (fakeSessionControl) Close(context.Context) error                                    { return nil }
func (fakeSessionControl) ForceStop() error                                               { return nil }

// fakeResolver 按 threadID 返回测试 session，未知 ID 用错误模拟解析失败。
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

// fakeTurnService 记录 cron turn 调用，并允许每个步骤注入成功或失败结果。
type fakeTurnService struct {
	prepareFn func(context.Context, contract.Session, contract.CronPrepareInput) (providerdto.TurnRequest, error)
	startFn   func(context.Context, contract.Session, providerdto.TurnRequest) (contract.TurnHandle, error)
	trackFn   func(context.Context, string) (contract.CronTurnStatus, error)
	lookupFn  func(context.Context, string) (contract.CronTurnStatus, bool, error)

	prepareCalls []contract.CronPrepareInput
	lookupCalls  []string
}

var _ contract.CronTurnExecutor = (*fakeTurnService)(nil)

func (f *fakeTurnService) CronPrepareTurn(ctx context.Context, s contract.Session, in contract.CronPrepareInput) (providerdto.TurnRequest, error) {
	f.prepareCalls = append(f.prepareCalls, in)
	if f.prepareFn != nil {
		return f.prepareFn(ctx, s, in)
	}
	return providerdto.TurnRequest{LocalID: "turn-local-1", ThreadID: s.ThreadID()}, nil
}
func (f *fakeTurnService) CronStartTurn(ctx context.Context, s contract.Session, req providerdto.TurnRequest) (contract.TurnHandle, error) {
	if f.startFn != nil {
		return f.startFn(ctx, s, req)
	}
	return &fakeHandle{localID: req.LocalID}, nil
}
func (f *fakeTurnService) CronTrackTurn(ctx context.Context, localID string) (contract.CronTurnStatus, error) {
	if f.trackFn != nil {
		return f.trackFn(ctx, localID)
	}
	return contract.CronTurnStatus{LocalID: localID, State: "running"}, nil
}
func (f *fakeTurnService) CronLookupByDedupeKey(ctx context.Context, key string) (contract.CronTurnStatus, bool, error) {
	f.lookupCalls = append(f.lookupCalls, key)
	if f.lookupFn != nil {
		return f.lookupFn(ctx, key)
	}
	return contract.CronTurnStatus{}, false, nil
}

type fakeHandle struct {
	localID    string
	providerID string
}

func (h *fakeHandle) LocalID() string       { return h.localID }
func (h *fakeHandle) ProviderID() string    { return h.providerID }
func (h *fakeHandle) Done() <-chan struct{} { ch := make(chan struct{}); close(ch); return ch }
func (h *fakeHandle) Err() error            { return nil }

// ----- StartTurn 路径 -----

func TestAdapterStartTurnNotWired(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		adapter *TurnServiceAdapter
	}{
		{name: "nil adapter", adapter: nil},
		{name: "nil service", adapter: &TurnServiceAdapter{resolver: &fakeResolver{}}},
		{name: "nil resolver", adapter: &TurnServiceAdapter{svc: &fakeTurnService{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.adapter.StartTurn(context.Background(), StartTurnRequest{ThreadID: "thread-1"})
			if err == nil || err.Error() != "cron: turn adapter not wired" {
				t.Fatalf("want not-wired error, got %v", err)
			}
		})
	}
}

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
	assertStartTurnHappyResult(t, res)
	got := requireSinglePrepareCall(t, svc)
	assertPreparedStartTurnInput(t, got)
	assertPreparedStartTurnSkills(t, got)
	assertPreparedRuntimeConfig(t, got)
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
		prepareFn: func(context.Context, contract.Session, contract.CronPrepareInput) (providerdto.TurnRequest, error) {
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

func TestAdapterStartTurnStartError(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{
		startFn: func(context.Context, contract.Session, providerdto.TurnRequest) (contract.TurnHandle, error) {
			return nil, errors.New("queue offline")
		},
	}
	sess := &fakeSession{threadID: "thread-1"}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{
		known: map[string]contract.Session{"thread-1": sess},
	})
	_, err := a.StartTurn(context.Background(), StartTurnRequest{ThreadID: "thread-1"})
	if err == nil || !stringContains(err.Error(), "start turn") {
		t.Fatalf("want start-turn error, got %v", err)
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

func TestAdapterStartTurnRejectsMalformedRuntimeConfig(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{
		prepareFn: func(context.Context, contract.Session, contract.CronPrepareInput) (providerdto.TurnRequest, error) {
			t.Fatal("CronPrepareTurn must not receive malformed runtime config")
			return providerdto.TurnRequest{}, nil
		},
	}
	sess := &fakeSession{threadID: "thread-1"}
	a := NewTurnServiceAdapter(slog.Default(), svc, &fakeResolver{
		known: map[string]contract.Session{"thread-1": sess},
	})
	_, err := a.StartTurn(context.Background(), StartTurnRequest{
		ThreadID: "thread-1",
		Config:   json.RawMessage(`{"bad":`),
	})
	if err == nil || !stringContains(err.Error(), "runtime config") {
		t.Fatalf("StartTurn error = %v, want runtime config validation error", err)
	}
}

// ----- Observe 路径 -----

func TestAdapterObserveTranslatesTurnNotFound(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{
		trackFn: func(context.Context, string) (contract.CronTurnStatus, error) {
			return contract.CronTurnStatus{}, errors.New("turn not found")
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
		trackFn: func(context.Context, string) (contract.CronTurnStatus, error) {
			return contract.CronTurnStatus{}, errors.New("db offline")
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

// ----- LookupByDedupeKey 路径 -----

// TestAdapterLookupByDedupeKeyEmptyKey 验证空 dedupe key 会直接返回 Found=false。
// 该短路避免未启用去重的调度 tick 反复触发 tracker 查询。
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

// TestAdapterLookupByDedupeKeyMiss 验证 tracker 无非终态 turn 时返回 Found=false。
// 调度器必须把该路径视为从未提交过任务，不能把 miss 当作已占用。
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

// TestAdapterLookupByDedupeKeyHit 验证 adapter 将 tracker 的 LocalID 转为
// ObservedTurn.TurnID，与 StartTurn 写入 run row 的 ID 保持一致。
func TestAdapterLookupByDedupeKeyHit(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{
		lookupFn: func(context.Context, string) (contract.CronTurnStatus, bool, error) {
			return contract.CronTurnStatus{LocalID: "turn-local-42", ProviderID: "provider-7", State: "running"}, true, nil
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

// TestAdapterLookupByDedupeKeyServiceError 验证底层错误会被包装返回，
// 同时保持 Found=false，便于调度器记录失败而不误判命中。
func TestAdapterLookupByDedupeKeyServiceError(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{
		lookupFn: func(context.Context, string) (contract.CronTurnStatus, bool, error) {
			return contract.CronTurnStatus{}, false, errors.New("tracker offline")
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

// TestAdapterStartTurnForwardsDedupeKey 验证 StartTurnRequest.DedupeKey 会进入
// CronPrepareInput，后续 tracker 才能在 prepare/start 阶段登记。
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

// ----- decodeRuntimeConfig 路径 -----

func TestDecodeRuntimeConfigMalformedReturnsError(t *testing.T) {
	t.Parallel()
	out, err := decodeRuntimeConfig(json.RawMessage("not json"))
	if err == nil || !stringContains(err.Error(), "runtime config") {
		t.Fatalf("malformed JSON error = %v, want runtime config validation error", err)
	}
	if out != nil {
		t.Fatalf("malformed JSON output = %+v, want nil", out)
	}
	out, err = decodeRuntimeConfig(nil)
	if err != nil {
		t.Fatalf("nil JSON error = %v, want nil", err)
	}
	if out != nil {
		t.Fatalf("nil JSON should decode to nil, got %+v", out)
	}
}

// ----- 可选依赖 wiring 路径 -----

func TestProvideTurnSubmitterFallsBackToNoop(t *testing.T) {
	t.Parallel()
	// service 和 resolver 都缺失时只能返回 Noop submitter，避免启动时隐式装配假依赖。
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
	// 复用本文件内的小 helper，避免为了少量断言扩大 imports 面。
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

// ----- Bootstrap 路径 -----

type recordingBootstrapper struct {
	calls  []BootstrapRequest
	result BootstrapResult
	err    error
}

func (r *recordingBootstrapper) BootstrapThread(_ context.Context, req BootstrapRequest) (BootstrapResult, error) {
	r.calls = append(r.calls, req)
	if r.err != nil {
		return BootstrapResult{}, r.err
	}
	return r.result, nil
}

func TestAdapterStartTurnBootstrapsOnEmptyThreadID(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{}
	sess := &fakeSession{threadID: "thread-fresh"}
	resolver := &fakeResolver{known: map[string]contract.Session{"thread-fresh": sess}}
	bs := &recordingBootstrapper{result: BootstrapResult{ThreadID: " thread-fresh ", AgentID: " agent-fresh "}}
	a := NewTurnServiceAdapter(slog.Default(), svc, resolver).WithBootstrapper(bs)

	res, err := a.StartTurn(context.Background(), StartTurnRequest{
		JobID:    "job-42",
		Provider: "codex",
		Model:    "gpt-5",
		CWD:      "/repo",
		Config:   json.RawMessage(`{"codexHome":"/tmp/home"}`),
		Prompt:   "scheduled run",
	})
	if err != nil {
		t.Fatalf("StartTurn err = %v", err)
	}
	assertBootstrapStartTurnResult(t, res)
	got := requireSingleBootstrapCall(t, bs)
	assertBootstrapRequestProjection(t, got)
	assertBootstrapConfigForwarded(t, got)
}

func TestAdapterStartTurnBootstrapKeepsRequestAgentWhenBootstrapAgentMissing(t *testing.T) {
	t.Parallel()
	svc := &fakeTurnService{}
	sess := &fakeSession{threadID: "thread-fresh"}
	resolver := &fakeResolver{known: map[string]contract.Session{"thread-fresh": sess}}
	bs := &recordingBootstrapper{result: BootstrapResult{ThreadID: "thread-fresh"}}
	a := NewTurnServiceAdapter(slog.Default(), svc, resolver).WithBootstrapper(bs)

	res, err := a.StartTurn(context.Background(), StartTurnRequest{
		AgentID: " agent-request ",
		JobID:   "job-42",
	})
	if err != nil {
		t.Fatalf("StartTurn err = %v", err)
	}
	if res.AgentID != "agent-request" || res.ThreadID != "thread-fresh" || res.TurnID == "" {
		t.Fatalf("bootstrap request-agent fallback wrong: %+v", res)
	}
}

func TestAdapterStartTurnBootstrapperErrorFails(t *testing.T) {
	t.Parallel()
	bs := &recordingBootstrapper{err: errors.New("thread start boom")}
	a := NewTurnServiceAdapter(slog.Default(), &fakeTurnService{}, &fakeResolver{}).WithBootstrapper(bs)

	_, err := a.StartTurn(context.Background(), StartTurnRequest{JobID: "j"})
	if err == nil || !stringContains(err.Error(), "bootstrap thread") {
		t.Fatalf("want wrapped bootstrap error, got %v", err)
	}
}

func TestAdapterStartTurnBootstrapperEmptyThreadIDRejects(t *testing.T) {
	t.Parallel()
	bs := &recordingBootstrapper{result: BootstrapResult{ThreadID: "  "}}
	a := NewTurnServiceAdapter(slog.Default(), &fakeTurnService{}, &fakeResolver{}).WithBootstrapper(bs)
	_, err := a.StartTurn(context.Background(), StartTurnRequest{JobID: "j"})
	if err == nil || !stringContains(err.Error(), "empty thread id") {
		t.Fatalf("want empty-thread-id rejection, got %v", err)
	}
}

func TestAdapterStartTurnFallsBackToNotBootstrappedWhenNoSeam(t *testing.T) {
	t.Parallel()
	// 故意不调用 WithBootstrapper：adapter 必须继续返回 ErrJobNotBootstrapped，
	// 让调度器按预算重试，而不是静默创建线程。
	a := NewTurnServiceAdapter(slog.Default(), &fakeTurnService{}, &fakeResolver{})
	_, err := a.StartTurn(context.Background(), StartTurnRequest{JobID: "j"})
	if !errors.Is(err, ErrJobNotBootstrapped) {
		t.Fatalf("want ErrJobNotBootstrapped, got %v", err)
	}
}

func TestNoopThreadBootstrapperSignals(t *testing.T) {
	t.Parallel()
	_, err := NoopThreadBootstrapper{}.BootstrapThread(context.Background(), BootstrapRequest{})
	if !errors.Is(err, ErrBootstrapperNotWired) {
		t.Fatalf("noop should return ErrBootstrapperNotWired, got %v", err)
	}
}

// ----- ThreadServiceBootstrapper 路径 -----

type fakeThreadStarter struct {
	startFn func(context.Context, contract.CronStartThreadRequest) (contract.CronStartThreadResult, error)
	calls   []contract.CronStartThreadRequest
}

var _ contract.CronThreadStarter = (*fakeThreadStarter)(nil)

func (f *fakeThreadStarter) CronStartThread(ctx context.Context, req contract.CronStartThreadRequest) (contract.CronStartThreadResult, error) {
	f.calls = append(f.calls, req)
	if f.startFn != nil {
		return f.startFn(ctx, req)
	}
	return contract.CronStartThreadResult{ThreadID: "thread-new", AgentID: "agent-new"}, nil
}

func TestThreadServiceBootstrapperHappyPath(t *testing.T) {
	t.Parallel()
	ts := &fakeThreadStarter{}
	b := NewThreadServiceBootstrapper(slog.Default(), ts)
	res, err := b.BootstrapThread(context.Background(), BootstrapRequest{
		JobID:    "job-1",
		Provider: "codex",
		Model:    "gpt-5",
		CWD:      "/repo",
		Name:     "nightly",
		Config:   json.RawMessage(`{"codexHome":"/tmp/home","codexInstanceKey":"glm"}`),
	})
	if err != nil {
		t.Fatalf("BootstrapThread err = %v", err)
	}
	assertThreadBootstrapResult(t, res)
	got := requireSingleThreadStartCall(t, ts)
	assertThreadStartRequestProjection(t, got)
	assertThreadStartConfig(t, got)
}

func TestThreadServiceBootstrapperPropagatesStartError(t *testing.T) {
	t.Parallel()
	ts := &fakeThreadStarter{
		startFn: func(context.Context, contract.CronStartThreadRequest) (contract.CronStartThreadResult, error) {
			return contract.CronStartThreadResult{}, errors.New("thread start offline")
		},
	}
	b := NewThreadServiceBootstrapper(slog.Default(), ts)
	_, err := b.BootstrapThread(context.Background(), BootstrapRequest{JobID: "j"})
	if err == nil || !stringContains(err.Error(), "thread start offline") {
		t.Fatalf("want underlying start error, got %v", err)
	}
}

func TestThreadServiceBootstrapperMalformedConfigRejected(t *testing.T) {
	t.Parallel()
	b := NewThreadServiceBootstrapper(slog.Default(), &fakeThreadStarter{})
	_, err := b.BootstrapThread(context.Background(), BootstrapRequest{
		JobID:  "j",
		Config: json.RawMessage("not-json"),
	})
	if err == nil || !stringContains(err.Error(), "bootstrap config") {
		t.Fatalf("want malformed-config rejection, got %v", err)
	}
}

func TestThreadServiceBootstrapperNilServiceSignals(t *testing.T) {
	t.Parallel()
	var b *ThreadServiceBootstrapper
	_, err := b.BootstrapThread(context.Background(), BootstrapRequest{})
	if !errors.Is(err, ErrBootstrapperNotWired) {
		t.Fatalf("nil bootstrapper should surface ErrBootstrapperNotWired, got %v", err)
	}
}
