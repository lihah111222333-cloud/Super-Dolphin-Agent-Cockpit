package managedruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrSupervisorUnavailable 表示 candidate owner 未构造或合同依赖不可用。
	ErrSupervisorUnavailable = errors.New("managed runtime candidate supervisor is unavailable")
	// ErrSupervisorFenced 表示 owner 已进入 sticky query-only 状态。
	ErrSupervisorFenced = errors.New("managed runtime candidate supervisor is fenced")
	// ErrOutcomeUnknown 表示 mutation 的外部结果不能安全裁决。
	ErrOutcomeUnknown = errors.New("managed runtime mutation outcome is unknown")
	// ErrPreparedReceiptUnavailable 表示 candidate 尚未收到外部 owner 的 prepared receipt。
	ErrPreparedReceiptUnavailable = errors.New("managed runtime prepared receipt is unavailable")
	// ErrInvalidTransition 表示请求不能从当前 candidate 状态迁移。
	ErrInvalidTransition = errors.New("managed runtime candidate state transition is invalid")
	// ErrInvalidSupervisorContext 表示调用方未提供可检查的 context。
	ErrInvalidSupervisorContext = errors.New("managed runtime candidate supervisor requires context")
)

const (
	commitClosureEvidenceDomain = "orch.managed-runtime.candidate.commit-closure/v1"
	commitClosureEvidencePrefix = "candidate://managed-runtime/commit/"
	closedReceiptPrefix         = "candidate://managed-runtime/closed/"
)

// CandidateSupervisor 是只用于合同和 replay 负测的内存 owner。
//
// 它不创建或检查进程，不调用 provider，不连接 Fx、DB、registry、unified 或 Room。
// PREPARE 必须先由 SeedPrepared 注入一个已通过合同校验的外部 receipt；因此本类型
// 不会合成 PID、provider session 或 opaque handle，也不会把内存状态升级成 production truth。
type CandidateSupervisor struct {
	mu            sync.Mutex
	prerequisites ProductionPrerequisites
	staged        map[string]PreparedReceipt
	records       map[string]candidateRecord
	operations    map[string]candidateOperation
	queryOnly     bool
}

// MemorySupervisor 是兼容命名；它与 CandidateSupervisor 完全相同，仍保持 candidate-only。
type MemorySupervisor = CandidateSupervisor

type candidateRecord struct {
	request           PrepareRequest
	prepared          PreparedReceipt
	state             State
	closed            ClosedReceipt
	uncertaintyRef    string
	uncertaintyDigest string
}

type candidateOperation struct {
	digest  string
	kind    candidateOperationKind
	receipt ClosedReceipt
}

type candidateOperationKind uint8

const (
	candidatePrepareOperation candidateOperationKind = iota + 1
	candidateCommitOperation
	candidateAbortOperation
)

// NewCandidateSupervisor 构造一个仅用于本地候选验证的 owner。
//
// prerequisites 只验证四类 production evidence 的形状；它不验证 evidence 的真实性，
// 也不改变本 owner 的 candidate-only 身份。缺任一依赖时直接返回 ErrProductionNoGo。
func NewCandidateSupervisor(prerequisites ProductionPrerequisites) (*CandidateSupervisor, error) {
	if err := prerequisites.Validate(); err != nil {
		return nil, err
	}
	return &CandidateSupervisor{
		prerequisites: prerequisites,
		staged:        make(map[string]PreparedReceipt),
		records:       make(map[string]candidateRecord),
		operations:    make(map[string]candidateOperation),
	}, nil
}

// NewMemorySupervisor 保留显式的内存 owner 命名，不提供 production 构造快捷方式。
func NewMemorySupervisor(prerequisites ProductionPrerequisites) (*MemorySupervisor, error) {
	return NewCandidateSupervisor(prerequisites)
}

// SeedPrepared 将外部 owner 已验证的 receipt 放入 candidate 的待 PREPARE 区域。
//
// 这是测试/合同 seam，不是启动动作；同 operation+digest+receipt 重放成功，任何
// 字段漂移都返回冲突。调用方必须先完成自己的 process receipt 验真。
func (s *CandidateSupervisor) SeedPrepared(
	ctx context.Context,
	request PrepareRequest,
	receipt PreparedReceipt,
) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	if err := validateSeedPrepared(request, receipt); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seedPreparedLocked(request, receipt)
}

// validateSeedPrepared 校验 candidate seed 的原 request、receipt 和 operation 绑定。
func validateSeedPrepared(request PrepareRequest, receipt PreparedReceipt) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := receipt.Validate(); err != nil {
		return err
	}
	return MatchPrepare(request, receipt)
}

// seedPreparedLocked 在 owner 锁内执行 seed 的冲突判断和一次性保存。
func (s *CandidateSupervisor) seedPreparedLocked(request PrepareRequest, receipt PreparedReceipt) error {
	if s.queryOnly {
		return ErrSupervisorFenced
	}
	if previous, exists := s.operations[request.OperationID]; exists {
		if err := checkSeedOperation(previous, request, receipt, s.records, s.staged); err != nil {
			return err
		}
		if _, hasRecord := s.records[request.OperationID]; hasRecord {
			return nil
		}
		if staged, hasStaged := s.staged[request.OperationID]; hasStaged && staged == receipt {
			return nil
		}
	}
	s.operations[request.OperationID] = candidateOperation{digest: request.RequestDigest, kind: candidatePrepareOperation}
	s.staged[request.OperationID] = receipt
	return nil
}

// checkSeedOperation 保持已有 prepare seed 的 operation、receipt 和 replay 关系不变。
func checkSeedOperation(
	previous candidateOperation,
	request PrepareRequest,
	receipt PreparedReceipt,
	records map[string]candidateRecord,
	staged map[string]PreparedReceipt,
) error {
	if previous.kind != candidatePrepareOperation || previous.digest != request.RequestDigest {
		return ErrOperationConflict
	}
	if record, hasRecord := records[request.OperationID]; hasRecord && record.prepared != receipt {
		return ErrOperationConflict
	}
	if seeded, hasSeeded := staged[request.OperationID]; hasSeeded && seeded != receipt {
		return ErrOperationConflict
	}
	return nil
}

// Prepare 消费一个已注入的 prepared receipt，并对原 operation 严格幂等。
func (s *CandidateSupervisor) Prepare(ctx context.Context, request PrepareRequest) (PreparedReceipt, error) {
	if err := s.ready(ctx); err != nil {
		return PreparedReceipt{}, err
	}
	if err := request.Validate(); err != nil {
		return PreparedReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepareLocked(request)
}

// prepareLocked 在 owner 锁内消费已验证 seed，并创建 prepared record。
func (s *CandidateSupervisor) prepareLocked(request PrepareRequest) (PreparedReceipt, error) {
	if s.queryOnly {
		return PreparedReceipt{}, ErrSupervisorFenced
	}
	if record, exists := s.records[request.OperationID]; exists {
		if record.request.RequestDigest != request.RequestDigest {
			return PreparedReceipt{}, ErrOperationConflict
		}
		return record.prepared, nil
	}
	operation, exists := s.operations[request.OperationID]
	if exists && (operation.kind != candidatePrepareOperation || operation.digest != request.RequestDigest) {
		return PreparedReceipt{}, ErrOperationConflict
	}
	receipt, exists := s.staged[request.OperationID]
	if !exists {
		return PreparedReceipt{}, ErrPreparedReceiptUnavailable
	}
	if err := MatchPrepare(request, receipt); err != nil {
		return PreparedReceipt{}, err
	}
	s.records[request.OperationID] = candidateRecord{request: request, prepared: receipt, state: StatePrepared}
	delete(s.staged, request.OperationID)
	s.operations[request.OperationID] = candidateOperation{digest: request.RequestDigest, kind: candidatePrepareOperation}
	return receipt, nil
}

// Commit 以单赢家 CAS 语义激活 prepared receipt；candidate 不执行真实 activation。
func (s *CandidateSupervisor) Commit(ctx context.Context, request CommitRequest) (ClosedReceipt, error) {
	if err := s.ready(ctx); err != nil {
		return ClosedReceipt{}, err
	}
	if err := request.Validate(); err != nil {
		return ClosedReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queryOnly {
		return ClosedReceipt{}, ErrSupervisorFenced
	}
	if replay, handled, err := s.replayMutationLocked(request.OperationID, request.RequestDigest, candidateCommitOperation); handled {
		return replay, err
	}
	record, err := s.preparedRecordLocked(request.PreparedOperationID, request.PreparedRequestDigest)
	if err != nil {
		return ClosedReceipt{}, err
	}
	if err := matchCommitPrepared(record, request); err != nil {
		return ClosedReceipt{}, err
	}
	if record.state != StatePrepared {
		return ClosedReceipt{}, invalidTransition(record.state, StateCommitted)
	}
	closed, err := buildCommitReceipt(record, request)
	if err != nil {
		return ClosedReceipt{}, err
	}
	record.state, record.closed = StateCommitted, closed
	s.records[record.request.OperationID] = record
	s.operations[request.OperationID] = candidateOperation{
		digest: request.RequestDigest, kind: candidateCommitOperation, receipt: closed,
	}
	return closed, nil
}

// Abort 以单赢家 CAS 语义关闭 prepared receipt；cleanup evidence 由调用方显式提供。
func (s *CandidateSupervisor) Abort(ctx context.Context, request AbortRequest) (ClosedReceipt, error) {
	if err := s.ready(ctx); err != nil {
		return ClosedReceipt{}, err
	}
	if err := request.Validate(); err != nil {
		return ClosedReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queryOnly {
		return ClosedReceipt{}, ErrSupervisorFenced
	}
	if replay, handled, err := s.replayMutationLocked(request.OperationID, request.RequestDigest, candidateAbortOperation); handled {
		return replay, err
	}
	record, err := s.preparedRecordLocked(request.PreparedOperationID, request.PreparedRequestDigest)
	if err != nil {
		return ClosedReceipt{}, err
	}
	if err := matchAbortPrepared(record, request); err != nil {
		return ClosedReceipt{}, err
	}
	if record.state != StatePrepared {
		return ClosedReceipt{}, invalidTransition(record.state, StateAborted)
	}
	closed, err := buildAbortReceipt(record, request)
	if err != nil {
		return ClosedReceipt{}, err
	}
	record.state, record.closed = StateAborted, closed
	s.records[record.request.OperationID] = record
	s.operations[request.OperationID] = candidateOperation{
		digest: request.RequestDigest, kind: candidateAbortOperation, receipt: closed,
	}
	return closed, nil
}

// Query 返回原 PREPARE operation 的 one-hot 状态，且在 sticky fence 后仍可调用。
func (s *CandidateSupervisor) Query(ctx context.Context, request QueryRequest) (QueryResult, error) {
	if err := s.ready(ctx); err != nil {
		return QueryResult{}, err
	}
	if err := request.Validate(); err != nil {
		return QueryResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.records[request.OperationID]
	if !exists {
		if operation, found := s.operations[request.OperationID]; found {
			if operation.kind != candidatePrepareOperation {
				return QueryResult{}, ErrOperationNotFound
			}
			if operation.digest != request.OperationDigest {
				return QueryResult{}, ErrOperationConflict
			}
		}
		return finalizeCandidateQuery(QueryResult{
			SchemaVersion: SchemaVersion, OperationID: request.OperationID,
			OperationDigest: request.OperationDigest, State: StateAbsent,
		})
	}
	if record.request.RequestDigest != request.OperationDigest {
		return QueryResult{}, ErrOperationConflict
	}
	return finalizeCandidateQuery(queryResultForRecord(record))
}

// MarkOutcomeUnknown 模拟 commit 边界丢失结果，并安装不可清除的 query-only fence。
//
// 该方法仅用于 candidate fault injection；它不代表真实外部副作用。成功安装后返回
// ErrOutcomeUnknown，调用方必须只用原 QueryRequest 查询，任何 mutation 都会被拒绝。
func (s *CandidateSupervisor) MarkOutcomeUnknown(
	ctx context.Context,
	request QueryRequest,
	uncertaintyRef string,
	uncertaintyDigest string,
) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if err := validateCanonicalText(ErrInvalidQuery, textField{"uncertainty_receipt_ref", uncertaintyRef, 512}); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidQuery, digestField{"uncertainty_digest", uncertaintyDigest}); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queryOnly {
		return ErrSupervisorFenced
	}
	record, exists := s.records[request.OperationID]
	if !exists {
		return ErrOperationNotFound
	}
	if record.request.RequestDigest != request.OperationDigest {
		return ErrOperationConflict
	}
	if record.state != StatePrepared {
		return invalidTransition(record.state, StateOutcomeUnknown)
	}
	record.state = StateOutcomeUnknown
	record.uncertaintyRef, record.uncertaintyDigest = uncertaintyRef, uncertaintyDigest
	s.records[request.OperationID] = record
	s.queryOnly = true
	return ErrOutcomeUnknown
}

// IsFenced 返回当前 owner 是否已经转为 query-only；nil owner 按 fail-closed 处理。
func (s *CandidateSupervisor) IsFenced() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queryOnly
}

// ready 校验 receiver、context 和四类显式 production evidence，不执行任何副作用。
func (s *CandidateSupervisor) ready(ctx context.Context) error {
	if s == nil {
		return ErrSupervisorUnavailable
	}
	if ctx == nil {
		return ErrInvalidSupervisorContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.prerequisites.Validate()
}

// preparedRecordLocked 查找并绑定原 prepared operation 与 request digest。
func (s *CandidateSupervisor) preparedRecordLocked(operationID, requestDigest string) (candidateRecord, error) {
	record, exists := s.records[operationID]
	if !exists {
		return candidateRecord{}, ErrOperationNotFound
	}
	if record.request.RequestDigest != requestDigest {
		return candidateRecord{}, ErrOperationConflict
	}
	return record, nil
}

// replayMutationLocked 处理 mutation operation 的 exact replay 与 key 冲突。
func (s *CandidateSupervisor) replayMutationLocked(
	operationID string,
	digest string,
	kind candidateOperationKind,
) (ClosedReceipt, bool, error) {
	operation, exists := s.operations[operationID]
	if !exists {
		return ClosedReceipt{}, false, nil
	}
	if operation.kind != kind || operation.digest != digest {
		return ClosedReceipt{}, true, ErrOperationConflict
	}
	if operation.receipt.ReceiptDigest == "" {
		return ClosedReceipt{}, true, ErrSupervisorUnavailable
	}
	return operation.receipt, true, nil
}

// matchCommitPrepared 检查 commit 的 receipt ref/digest 与原 prepared 完全一致。
func matchCommitPrepared(record candidateRecord, request CommitRequest) error {
	prepared := record.prepared
	if request.ExpectedPreparedReceiptRef != prepared.ReceiptRef || request.ExpectedPreparedReceiptDigest != prepared.ReceiptDigest {
		return ErrOperationConflict
	}
	return nil
}

// matchAbortPrepared 检查 abort 的 receipt ref/digest 与原 prepared 完全一致。
func matchAbortPrepared(record candidateRecord, request AbortRequest) error {
	prepared := record.prepared
	if request.ExpectedPreparedReceiptRef != prepared.ReceiptRef || request.ExpectedPreparedReceiptDigest != prepared.ReceiptDigest {
		return ErrOperationConflict
	}
	return nil
}

// buildCommitReceipt 只生成 candidate closure receipt，不宣称真实 active binding。
func buildCommitReceipt(record candidateRecord, request CommitRequest) (ClosedReceipt, error) {
	evidenceDigest, err := digestCanonical(commitClosureEvidenceDomain, struct {
		ActiveBindingDigest string `json:"active_binding_digest"`
		CredentialDigest    string `json:"credential_digest"`
	}{request.ActiveBindingDigest, request.CredentialDigest})
	if err != nil {
		return ClosedReceipt{}, err
	}
	return finalizeClosedReceipt(ClosedReceipt{
		SchemaVersion: SchemaVersion, PreparedOperationID: record.request.OperationID,
		PreparedRequestDigest: record.request.RequestDigest, OperationID: request.OperationID,
		OperationDigest: request.RequestDigest, State: StateCommitted,
		PreparedReceiptRef: record.prepared.ReceiptRef, PreparedReceiptDigest: record.prepared.ReceiptDigest,
		ClosureEvidenceRef:    commitClosureEvidencePrefix + request.OperationID,
		ClosureEvidenceDigest: evidenceDigest, ReceiptRef: closedReceiptPrefix + request.OperationID,
	})
}

// buildAbortReceipt 将调用方提供的 cleanup evidence 原样绑定到 candidate receipt。
func buildAbortReceipt(record candidateRecord, request AbortRequest) (ClosedReceipt, error) {
	return finalizeClosedReceipt(ClosedReceipt{
		SchemaVersion: SchemaVersion, PreparedOperationID: record.request.OperationID,
		PreparedRequestDigest: record.request.RequestDigest, OperationID: request.OperationID,
		OperationDigest: request.RequestDigest, State: StateAborted,
		PreparedReceiptRef: record.prepared.ReceiptRef, PreparedReceiptDigest: record.prepared.ReceiptDigest,
		ClosureEvidenceRef: request.CleanupEvidenceRef, ClosureEvidenceDigest: request.CleanupEvidenceDigest,
		ReceiptRef: closedReceiptPrefix + request.OperationID,
	})
}

// finalizeClosedReceipt 统一完成 closed receipt 摘要和最终合同校验。
func finalizeClosedReceipt(receipt ClosedReceipt) (ClosedReceipt, error) {
	finalized, err := FinalizeClosedReceiptDigest(receipt)
	if err != nil {
		return ClosedReceipt{}, err
	}
	if err := finalized.Validate(); err != nil {
		return ClosedReceipt{}, err
	}
	return finalized, nil
}

// queryResultForRecord 将内部状态投影为严格 one-hot QueryResult。
func queryResultForRecord(record candidateRecord) QueryResult {
	result := QueryResult{
		SchemaVersion: SchemaVersion, OperationID: record.request.OperationID,
		OperationDigest: record.request.RequestDigest, State: record.state,
	}
	switch record.state {
	case StatePrepared:
		result.PreparedReceiptRef, result.PreparedReceiptDigest = record.prepared.ReceiptRef, record.prepared.ReceiptDigest
	case StateCommitted, StateAborted:
		result.PreparedReceiptRef, result.PreparedReceiptDigest = record.prepared.ReceiptRef, record.prepared.ReceiptDigest
		result.ClosedReceiptRef, result.ClosedReceiptDigest = record.closed.ReceiptRef, record.closed.ReceiptDigest
	case StateOutcomeUnknown:
		result.UncertaintyReceiptRef, result.UncertaintyDigest = record.uncertaintyRef, record.uncertaintyDigest
	}
	return result
}

// finalizeCandidateQuery 完成 query digest 与最终 one-hot 校验。
func finalizeCandidateQuery(result QueryResult) (QueryResult, error) {
	finalized, err := FinalizeQueryResultDigest(result)
	if err != nil {
		return QueryResult{}, err
	}
	if err := finalized.Validate(); err != nil {
		return QueryResult{}, err
	}
	return finalized, nil
}

// invalidTransition 将状态迁移错误固定为稳定 sentinel，避免暴露内部细节。
func invalidTransition(from, to State) error {
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
}
