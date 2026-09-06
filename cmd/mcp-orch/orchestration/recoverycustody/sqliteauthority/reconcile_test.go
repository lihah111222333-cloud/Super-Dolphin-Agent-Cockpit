package sqliteauthority

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type fakeFinalizationRecord struct {
	digest   string
	evidence FinalizedEvidence
}

// observationFinalizerFake 模拟线性化 Query 与同键原子 create-or-get，不代表生产外部 evidence 服务。
type observationFinalizerFake struct {
	mu              sync.Mutex
	config          Config
	state           FinalizationQueryState
	queryErr        error
	finalizeErr     error
	queryCalls      int
	finalizeCalls   int
	physicalEffects int
	records         map[string]fakeFinalizationRecord
	queryEntered    chan<- struct{}
	queryRelease    <-chan struct{}
}

// Query 返回带完整 fence 的封闭 observation，并拒绝同 operation 异 digest 查询。
func (f *observationFinalizerFake) Query(
	ctx context.Context,
	request FinalizationRequest,
) (FinalizationObservation, error) {
	f.mu.Lock()
	f.queryCalls++
	var observation FinalizationObservation
	var queryErr error
	if f.queryErr != nil {
		queryErr = f.queryErr
	} else if record, ok := f.records[request.FinalizationOperationID]; ok {
		if record.digest != request.FinalizationRequestDigest {
			queryErr = recoverycustody.ErrResolutionConflict
		} else {
			evidence := record.evidence
			observation = finalizationObservationFixture(request, FinalizationQueryFinalized, &evidence)
		}
	} else {
		observation, queryErr = f.unrecordedObservation(request)
	}
	// 释放互斥锁后再发布已线性化观察；并发测试等待两个观察均发布后才放行
	// Query 返回，从而确定性地让两个调用都看到 absent。
	f.mu.Unlock()
	if err := signalAndWaitQuery(ctx, f.queryEntered, f.queryRelease); err != nil {
		return FinalizationObservation{}, err
	}
	return observation, queryErr
}

// unrecordedObservation 按测试模式生成 absent、inflight 或预置 finalized 观察。
func (f *observationFinalizerFake) unrecordedObservation(
	request FinalizationRequest,
) (FinalizationObservation, error) {
	if f.state != FinalizationQueryFinalized {
		return finalizationObservationFixture(request, f.state, nil), nil
	}
	evidence, err := finalizedEvidenceFixture(f.config, request.ExpectedResolution)
	if err != nil {
		return FinalizationObservation{}, err
	}
	return finalizationObservationFixture(request, f.state, &evidence), nil
}

// Finalize 对 operation ID 与 digest 执行线程安全 create-or-get，异 digest 必须冲突。
func (f *observationFinalizerFake) Finalize(
	_ context.Context,
	request FinalizationRequest,
) (FinalizedEvidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finalizeCalls++
	if f.finalizeErr != nil {
		return FinalizedEvidence{}, f.finalizeErr
	}
	if record, ok := f.records[request.FinalizationOperationID]; ok {
		if record.digest != request.FinalizationRequestDigest {
			return FinalizedEvidence{}, recoverycustody.ErrResolutionConflict
		}
		return record.evidence, nil
	}
	evidence, err := finalizedEvidenceFixture(f.config, request.ExpectedResolution)
	if err != nil {
		return FinalizedEvidence{}, err
	}
	if f.records == nil {
		f.records = make(map[string]fakeFinalizationRecord)
	}
	f.records[request.FinalizationOperationID] = fakeFinalizationRecord{
		digest: request.FinalizationRequestDigest, evidence: evidence,
	}
	f.physicalEffects++
	return evidence, nil
}

// signalAndWaitQuery 仅用于并发测试把两个 Query 固定在线性化观察之后再返回。
func signalAndWaitQuery(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	if entered != nil {
		select {
		case entered <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// counts 返回 finalizer API 调用与物理 effect 的竞态安全快照。
func (f *observationFinalizerFake) counts() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queryCalls, f.finalizeCalls, f.physicalEffects
}

// TestReconcileResolutionObservationMatrix 覆盖 inflight 与 finalized 两个非 absent 分支。
func TestReconcileResolutionObservationMatrix(t *testing.T) {
	t.Run("inflight", testReconcileInflight)
	t.Run("finalized", testReconcileFinalized)
}

// testReconcileInflight 验证处理中 observation 保持同句柄 indeterminate 且不调用 Finalize。
func testReconcileInflight(t *testing.T) {
	finalizer := &observationFinalizerFake{state: FinalizationQueryInflight}
	authority, pending := prepareActiveReconcileAuthority(t, "inflight", finalizer)
	outcome, err := authority.ReconcileResolution(t.Context(), pending.Handle)
	if err != nil || outcome.Handle != pending.Handle || outcome.Status != recoverycustody.StatusIndeterminate ||
		outcome.ReasonCode != "recovery_custody_finalization_inflight" || outcome.Receipt != nil {
		t.Fatalf("ReconcileResolution(inflight) = (%+v, %v)", outcome, err)
	}
	assertReconcileFinalizerCounts(t, finalizer, 1, 0, 0)
}

// testReconcileFinalized 验证 finalized observation 直接提交 evidence 且不调用 Finalize。
func testReconcileFinalized(t *testing.T) {
	finalizer := &observationFinalizerFake{state: FinalizationQueryFinalized}
	authority, pending := prepareActiveReconcileAuthority(t, "finalized", finalizer)
	outcome, err := authority.ReconcileResolution(t.Context(), pending.Handle)
	if err != nil || outcome.Status != recoverycustody.StatusAvailable || outcome.Receipt == nil {
		t.Fatalf("ReconcileResolution(finalized) = (%+v, %v)", outcome, err)
	}
	assertReconcileFinalizerCounts(t, finalizer, 1, 0, 0)
	assertOutcomeRowCount(t, authority.db, "terminal_receipts", 1)
}

// TestReconcileResolutionRestartAbsentFinalizesSameKey 模拟 tx1 后崩溃，再由新实例补齐同键 terminal。
func TestReconcileResolutionRestartAbsentFinalizesSameKey(t *testing.T) {
	config := authorityTestConfig(t, "restart-absent")
	finalizer := &observationFinalizerFake{config: config, state: FinalizationQueryAbsent}
	first, pending := prepareActiveReconcileAuthorityWithConfig(t, config, finalizer)
	if err := first.Close(); err != nil {
		t.Fatalf("close pre-crash authority: %v", err)
	}

	restarted := openOutcomeAuthority(t, config, finalizer)
	t.Cleanup(func() { closeOutcomeAuthority(t, restarted) })
	outcome, err := restarted.ReconcileResolution(t.Context(), pending.Handle)
	if err != nil || outcome.Status != recoverycustody.StatusAvailable || outcome.Receipt == nil {
		t.Fatalf("restart ReconcileResolution() = (%+v, %v)", outcome, err)
	}
	assertReconcileFinalizerCounts(t, finalizer, 1, 1, 1)
	assertOutcomeRowCount(t, restarted.db, "terminal_receipts", 1)
}

// TestAuthorityClientQueryNeverCallsFinalizer 通过公共 Client 接口重复纯读 active outcome。
func TestAuthorityClientQueryNeverCallsFinalizer(t *testing.T) {
	finalizer := &observationFinalizerFake{state: FinalizationQueryAbsent}
	authority, pending := prepareActiveReconcileAuthority(t, "pure-query", finalizer)
	var client recoverycustody.Client = authority
	for range 3 {
		outcome, err := client.Query(t.Context(), pending.Handle)
		if err != nil || outcome.Status != recoverycustody.StatusIndeterminate {
			t.Fatalf("Client.Query() = (%+v, %v)", outcome, err)
		}
	}
	assertReconcileFinalizerCounts(t, finalizer, 0, 0, 0)
}

// TestConcurrentReconcileResolutionCreatesOnePhysicalEffect 验证并发同键调用
// 允许同键 create-or-get 的重复 API 调用，但最终只产生一份物理 effect。
func TestConcurrentReconcileResolutionCreatesOnePhysicalEffect(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	finalizer := &observationFinalizerFake{state: FinalizationQueryAbsent, queryEntered: entered, queryRelease: release}
	authority, pending := prepareActiveReconcileAuthority(t, "concurrent", finalizer)
	results := make(chan reconcileResult, 2)
	for range 2 {
		runtimesafe.SafeGo(t.Context(), nil, "sqliteauthority.test.concurrent-reconcile", func(runCtx context.Context) {
			outcome, err := authority.ReconcileResolution(runCtx, pending.Handle)
			results <- reconcileResult{outcome: outcome, err: err}
		})
	}
	<-entered
	<-entered
	close(release)
	assertConcurrentReconcileResults(t, results)
	// Both queries are forced to observe absent, but the second claim check may
	// run after the first caller has committed the terminal resolution. In that
	// valid schedule it returns the already-available outcome without calling
	// Finalize. The contract is one physical effect; Finalize may be invoked once
	// or twice depending on where the SQLite transaction linearizes.
	queryCalls, finalizeCalls, physicalEffects := finalizer.counts()
	if queryCalls != 2 || finalizeCalls < 1 || finalizeCalls > 2 || physicalEffects != 1 {
		t.Fatalf("concurrent finalizer counts = query:%d finalize:%d effects:%d, want query:2 finalize:1..2 effects:1",
			queryCalls, finalizeCalls, physicalEffects)
	}
	assertOutcomeRowCount(t, authority.db, "terminal_receipts", 1)
}

func assertConcurrentReconcileResults(t *testing.T, results <-chan reconcileResult) {
	t.Helper()
	for range 2 {
		result := <-results
		if result.outcome.Status != recoverycustody.StatusAvailable || result.outcome.Receipt == nil {
			t.Fatalf("concurrent ReconcileResolution() = (%+v, %v)", result.outcome, result.err)
		}
		assertConcurrentReconcileError(t, result.err)
	}
}

func assertConcurrentReconcileError(t *testing.T, err error) {
	t.Helper()
	if err != nil && !errors.Is(err, recoverycustody.ErrResolutionConflict) {
		t.Fatalf("concurrent ReconcileResolution() error = %v", err)
	}
}

type reconcileResult struct {
	outcome recoverycustody.Outcome
	err     error
}

// TestFinalizerSameOperationDifferentDigestConflicts 锁定外部 create-or-get 的异 digest 冲突合同。
func TestFinalizerSameOperationDifferentDigestConflicts(t *testing.T) {
	_, expected, first := threeLayerDigestFixture(t)
	config := authorityTestConfig(t, "same-operation-conflict")
	finalizer := &observationFinalizerFake{config: config, state: FinalizationQueryAbsent}
	if _, err := finalizer.Finalize(t.Context(), first); err != nil {
		t.Fatalf("first Finalize() error = %v", err)
	}
	second := conflictingFinalizationRequest(t, expected)
	if _, err := finalizer.Finalize(t.Context(), second); !errors.Is(err, recoverycustody.ErrResolutionConflict) {
		t.Fatalf("conflicting Finalize() error = %v, want ErrResolutionConflict", err)
	}
	assertReconcileFinalizerCounts(t, finalizer, 0, 2, 1)
}

// conflictingFinalizationRequest 保留 operation ID，但重新绑定不同 resolution/digest。
func conflictingFinalizationRequest(t *testing.T, expected ExpectedResolution) FinalizationRequest {
	t.Helper()
	resolution := expected.ResolutionRequest
	resolution.ResolutionID += "-conflict"
	resolution.ResolutionRequestDigest = ""
	resolution, err := FinalizeResolutionRequestDigest(resolution)
	if err != nil {
		t.Fatalf("FinalizeResolutionRequestDigest(conflict) error = %v", err)
	}
	expected.ResolutionRequest = resolution
	expected.ExpectedResolutionDigest = ""
	expected, err = BindExpectedResolutionDigest(expected)
	if err != nil {
		t.Fatalf("BindExpectedResolutionDigest(conflict) error = %v", err)
	}
	finalization, err := BindFinalizationRequest(expected)
	if err != nil {
		t.Fatalf("BindFinalizationRequest(conflict) error = %v", err)
	}
	return finalization
}

// prepareActiveReconcileAuthority 创建已持久化 active resolution，但不调用 finalizer。
func prepareActiveReconcileAuthority(
	t *testing.T,
	name string,
	finalizer *observationFinalizerFake,
) (*Authority, recoverycustody.Outcome) {
	t.Helper()
	config := authorityTestConfig(t, "reconcile-"+name)
	finalizer.config = config
	return prepareActiveReconcileAuthorityWithConfig(t, config, finalizer)
}

// prepareActiveReconcileAuthorityWithConfig 用显式配置构造 crash-gap 测试夹具。
func prepareActiveReconcileAuthorityWithConfig(
	t *testing.T,
	config Config,
	finalizer *observationFinalizerFake,
) (*Authority, recoverycustody.Outcome) {
	t.Helper()
	authority := bootstrapAndOpenOutcomeAuthority(t, config, finalizer)
	pending, err := authority.Submit(t.Context(), schemaTestRequest(t))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	resolution := outcomeResolutionRequest(t, pending.Handle, "reconcile")
	_, created, terminal, err := authority.prepareResolution(t.Context(), resolution)
	if err != nil || !created || terminal {
		t.Fatalf("prepareResolution() = (created:%v terminal:%v err:%v)", created, terminal, err)
	}
	return authority, pending
}

// assertReconcileFinalizerCounts 核对 API 调用次数与实际外部 effect 数量。
func assertReconcileFinalizerCounts(
	t *testing.T,
	finalizer *observationFinalizerFake,
	wantQuery int,
	wantFinalize int,
	wantEffects int,
) {
	t.Helper()
	queryCalls, finalizeCalls, physicalEffects := finalizer.counts()
	if queryCalls != wantQuery || finalizeCalls != wantFinalize || physicalEffects != wantEffects {
		t.Fatalf("finalizer counts = query:%d finalize:%d effects:%d, want %d/%d/%d",
			queryCalls, finalizeCalls, physicalEffects, wantQuery, wantFinalize, wantEffects)
	}
}
