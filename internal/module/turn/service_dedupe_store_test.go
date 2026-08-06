package turn

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// fakeDedupeStore 是内存版 DedupeStore。
// 服务层测试用它覆盖镜像写入和读取回退分支，不需要连接真实持久化后端。
type fakeDedupeStore struct {
	mu       sync.Mutex
	rows     map[string]DedupeEntry
	upsertFn func(context.Context, DedupeUpsertParams) error
	bindFn   func(context.Context, DedupeBindProviderTurnIDParams) error
	getFn    func(context.Context, string) (DedupeEntry, error)
	termFn   func(context.Context, string) error

	upsertCalls   int
	terminalCalls int
	bindCalls     int
}

func newFakeDedupeStore() *fakeDedupeStore {
	return &fakeDedupeStore{rows: map[string]DedupeEntry{}}
}

func (f *fakeDedupeStore) Upsert(ctx context.Context, p DedupeUpsertParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalls++
	if f.upsertFn != nil {
		return f.upsertFn(ctx, p)
	}
	e, ok := f.rows[p.DedupeKey]
	if !ok {
		e = DedupeEntry{DedupeKey: p.DedupeKey, CreatedAt: p.Now}
	}
	e.LocalTurnID = p.LocalTurnID
	if p.ThreadID != "" {
		e.ThreadID = p.ThreadID
	}
	e.UpdatedAt = p.Now
	e.TerminalAt = time.Time{}
	f.rows[p.DedupeKey] = e
	return nil
}

func (f *fakeDedupeStore) BindProviderTurnID(ctx context.Context, p DedupeBindProviderTurnIDParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindCalls++
	if f.bindFn != nil {
		return f.bindFn(ctx, p)
	}
	if e, ok := f.rows[p.DedupeKey]; ok {
		e.ProviderTurnID = p.ProviderTurnID
		e.UpdatedAt = p.Now
		f.rows[p.DedupeKey] = e
	}
	return nil
}

func (f *fakeDedupeStore) MarkTerminal(ctx context.Context, key string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminalCalls++
	if f.termFn != nil {
		return f.termFn(ctx, key)
	}
	if e, ok := f.rows[key]; ok {
		if e.TerminalAt.IsZero() {
			e.TerminalAt = now
			e.UpdatedAt = now
			f.rows[key] = e
		}
	}
	return nil
}

func (f *fakeDedupeStore) GetLive(ctx context.Context, key string) (DedupeEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getFn != nil {
		return f.getFn(ctx, key)
	}
	e, ok := f.rows[key]
	if !ok || !e.TerminalAt.IsZero() {
		return DedupeEntry{}, ErrDedupeNotFound
	}
	return e, nil
}

func (f *fakeDedupeStore) Sweep(_ context.Context, _ time.Time) error { return nil }

// serviceWithStore 构造默认 turn service 并注入测试用 dedupe store。
// 这样可以直接覆盖镜像写入路径，而不需要通过 fx 装配完整依赖图。
func serviceWithStore(store DedupeStore) *service {
	return newService(silentLogger(), &stubPromptAssemblyService{}, nil, nil, nil, store, nil, nil, NewToolResultRuntime()).(*service)
}

func TestServiceStartTurnMirrorsToStore(t *testing.T) {
	t.Parallel()
	store := newFakeDedupeStore()
	svc := serviceWithStore(store)

	sess := &stubSession{
		threadID: "thread-1",
		startTurn: func(_ context.Context, _ dto.TurnRequest) (contract.TurnHandle, error) {
			return &stubTurnHandle{localID: "turn-1", providerID: "p-1", done: make(chan struct{})}, nil
		},
	}
	req, err := svc.PrepareTurn(context.Background(), sess, PrepareInput{
		Prompt:    "do the thing",
		DedupeKey: "dk-1",
	})
	if err != nil {
		t.Fatalf("PrepareTurn err = %v", err)
	}
	if _, err := svc.StartTurn(context.Background(), sess, req); err != nil {
		t.Fatalf("StartTurn err = %v", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.upsertCalls != 1 {
		t.Fatalf("want 1 upsert, got %d", store.upsertCalls)
	}
	if store.bindCalls != 1 {
		t.Fatalf("want 1 provider bind, got %d", store.bindCalls)
	}
	e := store.rows["dk-1"]
	if e.LocalTurnID == "" || e.ThreadID != "thread-1" || e.ProviderTurnID != "p-1" {
		t.Fatalf("mirror row = %+v", e)
	}
}

func TestServiceStartTurnDedupeUpsertErrorAbortsProviderStart(t *testing.T) {
	t.Parallel()
	want := errors.New("dedupe upsert failed")
	store := newFakeDedupeStore()
	store.upsertFn = func(context.Context, DedupeUpsertParams) error {
		return want
	}
	svc := serviceWithStore(store)
	var providerStarted bool
	sess := &stubSession{
		threadID: "thread-upsert",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			providerStarted = true
			return &stubTurnHandle{localID: "turn-upsert", providerID: "p-upsert", done: make(chan struct{})}, nil
		},
	}
	req, err := svc.PrepareTurn(context.Background(), sess, PrepareInput{Prompt: "run", DedupeKey: "dk-upsert"})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	_, err = svc.StartTurn(context.Background(), sess, req)
	if !errors.Is(err, want) {
		t.Fatalf("StartTurn() error = %v, want %v", err, want)
	}
	if providerStarted {
		t.Fatal("provider StartTurn must not run after dedupe upsert fails")
	}
}

func TestServiceStartTurnDedupeProviderIDErrorSurfaces(t *testing.T) {
	t.Parallel()
	want := errors.New("provider id bind failed")
	store := newFakeDedupeStore()
	store.bindFn = func(context.Context, DedupeBindProviderTurnIDParams) error {
		return want
	}
	svc := serviceWithStore(store)
	sess := &stubSession{
		threadID: "thread-bind",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return &stubTurnHandle{localID: "turn-bind", providerID: "p-bind", done: make(chan struct{})}, nil
		},
	}
	req, err := svc.PrepareTurn(context.Background(), sess, PrepareInput{Prompt: "run", DedupeKey: "dk-bind"})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	_, err = svc.StartTurn(context.Background(), sess, req)
	if !errors.Is(err, want) {
		t.Fatalf("StartTurn() error = %v, want %v", err, want)
	}
	if sess.lastInterrupt.ThreadID != "thread-bind" || sess.lastInterrupt.TurnID != "p-bind" {
		t.Fatalf("interrupt request = %#v, want thread-bind/p-bind cleanup", sess.lastInterrupt)
	}
}

func TestServiceStartTurnErrorMarksTerminal(t *testing.T) {
	t.Parallel()
	store := newFakeDedupeStore()
	svc := serviceWithStore(store)
	sess := &stubSession{
		threadID: "thread-err",
		startTurn: func(_ context.Context, _ dto.TurnRequest) (contract.TurnHandle, error) {
			return nil, errors.New("provider boom")
		},
	}
	req, _ := svc.PrepareTurn(context.Background(), sess, PrepareInput{DedupeKey: "dk-err"})
	if _, err := svc.StartTurn(context.Background(), sess, req); err == nil {
		t.Fatal("StartTurn should have failed")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.terminalCalls != 1 {
		t.Fatalf("want 1 terminal mark, got %d", store.terminalCalls)
	}
	if e := store.rows["dk-err"]; e.TerminalAt.IsZero() {
		t.Fatalf("terminal_at not set on row: %+v", e)
	}
}

func TestServiceLookupByDedupeKeyFallsBackToStore(t *testing.T) {
	t.Parallel()
	store := newFakeDedupeStore()
	svc := serviceWithStore(store)
	// 模拟上一个进程已写入 store，但当前实例的内存 tracker 从未登记该 key。
	now := time.Now()
	store.rows["dk-recover"] = DedupeEntry{
		DedupeKey:   "dk-recover",
		LocalTurnID: "turn-recover",
		ThreadID:    "thread-x",
		UpdatedAt:   now,
		CreatedAt:   now,
	}
	status, ok, err := svc.LookupByDedupeKey(context.Background(), "dk-recover")
	if err != nil {
		t.Fatalf("LookupByDedupeKey err = %v", err)
	}
	if !ok {
		t.Fatal("store row should have been hit")
	}
	if status.LocalID != "turn-recover" || status.State != "running" {
		t.Fatalf("status = %+v", status)
	}
}

func TestServiceLookupByDedupeKeyNoStoreStaysTrackerOnly(t *testing.T) {
	t.Parallel()
	// 未注入 store 时必须保持纯 tracker 行为：未命中返回 ok=false 且不报错。
	svc := newService(silentLogger(), &stubPromptAssemblyService{}, nil, nil, nil, nil, nil, nil, NewToolResultRuntime()).(*service)
	if _, ok, err := svc.LookupByDedupeKey(context.Background(), "dk-none"); ok || err != nil {
		t.Fatalf("want (false, nil), got ok=%v err=%v", ok, err)
	}
}

func TestServiceLookupByDedupeKeyStoreMissIsNeverSubmitted(t *testing.T) {
	t.Parallel()
	store := newFakeDedupeStore()
	svc := serviceWithStore(store)

	status, ok, err := svc.LookupByDedupeKey(context.Background(), "dk-missing")
	if err != nil {
		t.Fatalf("LookupByDedupeKey() err = %v, want nil for ErrNotFound domain miss", err)
	}
	if ok {
		t.Fatalf("LookupByDedupeKey() ok = true, want false for never-submitted key")
	}
	if status != (TurnStatus{}) {
		t.Fatalf("LookupByDedupeKey() status = %#v, want zero status", status)
	}
}

func TestServiceLookupByDedupeKeyStoreErrorSurfaces(t *testing.T) {
	t.Parallel()
	store := newFakeDedupeStore()
	store.getFn = func(_ context.Context, _ string) (DedupeEntry, error) {
		return DedupeEntry{}, errors.New("db offline")
	}
	svc := serviceWithStore(store)
	_, _, err := svc.LookupByDedupeKey(context.Background(), "dk")
	if err == nil {
		t.Fatal("want the underlying store error to surface")
	}
}

func TestServiceRecordDedupeUpsertEmptyKeyIsNoop(t *testing.T) {
	t.Parallel()
	store := newFakeDedupeStore()
	svc := serviceWithStore(store)
	svc.recordDedupeUpsert(context.Background(), "  ", "turn-x", "thread-x")
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.upsertCalls != 0 {
		t.Fatalf("empty key must not reach store, got %d calls", store.upsertCalls)
	}
}
