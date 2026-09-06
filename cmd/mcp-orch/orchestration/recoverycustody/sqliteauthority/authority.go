package sqliteauthority

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	platformsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

// Authority 持有独立 SQLite authority 与恢复域外 evidence 依赖。
type Authority struct {
	db                *sql.DB
	config            Config
	lease             AuthorityLease
	scope             AuthorityScope
	identityMu        sync.Mutex
	identity          authorityFileIdentity
	pathIdentity      authorityPathIdentity
	writerFence       WriterFence
	finalizer         EvidenceFinalizer
	verifier          recoverycustody.ReceiptVerifier
	transactionRunner transactionRunner
	queryOnly         atomic.Bool
}

type transactionRunner func(context.Context, *sql.DB, WriterFence, func(*sql.Conn) error) error

// resolutionPreparation 汇总事务内固化的候选、创建标记和 terminal 观察结果。
type resolutionPreparation struct {
	expected ExpectedResolution
	created  bool
	terminal bool
}

// OpenAuthority 打开已显式 Bootstrap 的独立 authority，并精确校验 lease、schema、meta 与 head。
func OpenAuthority(
	ctx context.Context,
	config Config,
	lease AuthorityLease,
	finalizer EvidenceFinalizer,
	verifier recoverycustody.ReceiptVerifier,
) (*Authority, error) {
	config, scope, err := validateAuthorityInputs(ctx, config, lease)
	if err != nil {
		return nil, err
	}
	if dependencyIsNil(finalizer) || dependencyIsNil(verifier) {
		return nil, fmt.Errorf("%w: evidence dependencies are required", ErrInvalidConfig)
	}
	var authority *Authority
	err = withValidatedAuthorityScope(ctx, lease, scope, config.Path, func() error {
		var openErr error
		authority, openErr = openAuthorityInScope(ctx, config, scope, lease, finalizer, verifier)
		return openErr
	})
	if err != nil {
		if authority != nil {
			_ = authority.Close()
		}
		return nil, err
	}
	return authority, nil
}

// openAuthorityInScope 在已验证的 authority scope 内打开数据库并完成
// schema、身份和单连接约束校验；失败路径始终关闭数据库句柄。
func openAuthorityInScope(
	ctx context.Context,
	config Config,
	scope AuthorityScope,
	lease AuthorityLease,
	finalizer EvidenceFinalizer,
	verifier recoverycustody.ReceiptVerifier,
) (*Authority, error) {
	if err := requireExistingAuthorityDatabase(config.Path); err != nil {
		return nil, err
	}
	db, err := platformsqlite.Open(ctx, platformsqlite.OpenOptions{Path: config.Path})
	if err != nil {
		return nil, safeAuthorityError("open authority", err)
	}
	if db.Stats().MaxOpenConnections != 1 {
		_ = db.Close()
		return nil, fmt.Errorf("%w: authority must use one SQLite connection", ErrSchemaMismatch)
	}
	fence, err := verifyAuthoritySchema(ctx, db, config, scope)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	identity, err := captureAuthorityPathIdentity(config.Path)
	if err != nil || !identity.main.present {
		_ = db.Close()
		if err != nil {
			return nil, err
		}
		return nil, ErrLeaseRejected
	}
	return &Authority{db: db, config: config, lease: lease, scope: scope, identity: identity.main,
		pathIdentity: identity,
		finalizer:    finalizer, verifier: verifier, writerFence: fence, transactionRunner: withWriterFenceImmediate}, nil
}

// requireExistingAuthorityDatabase 要求 Open 只接管已由 Bootstrap 创建的普通非符号链接数据库。
func requireExistingAuthorityDatabase(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: authority database was not bootstrapped", ErrSchemaMismatch)
	}
	if err != nil {
		return fmt.Errorf("%w: inspect authority database", ErrInvalidConfig)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: authority database must be a regular non-symlink file", ErrInvalidConfig)
	}
	return nil
}

// Close 关闭 authority 数据库；关闭后实例不可复用。
func (a *Authority) Close() error {
	if a == nil || a.db == nil {
		return nil
	}
	if err := a.db.Close(); err != nil {
		return safeAuthorityError("close authority", err)
	}
	return nil
}

// Submit 在 BEGIN IMMEDIATE 中建立 pending submission；精确重放返回原结果，异请求冲突。
func (a *Authority) Submit(ctx context.Context, request recoverycustody.SubmissionRequest) (recoverycustody.Outcome, error) {
	handle, err := a.validateSubmission(ctx, request)
	if err != nil {
		return recoverycustody.Outcome{}, err
	}
	if a.queryOnly.Load() {
		return a.queryAfterWriteRefusal(ctx, handle, ErrAuthorityReadOnly)
	}
	var outcome recoverycustody.Outcome
	err = a.writeTransaction(ctx, func(conn *sql.Conn) error {
		var writeErr error
		outcome, writeErr = a.submitOnConnection(ctx, conn, request, handle)
		return writeErr
	})
	if errors.Is(err, ErrCommitOutcomeUnknown) {
		return a.queryAfterWriteRefusal(ctx, handle, err)
	}
	if err != nil {
		return recoverycustody.Outcome{}, err
	}
	if outcome.Status != recoverycustody.StatusPending {
		return a.Query(ctx, handle)
	}
	return outcome, outcome.Validate()
}

// validateSubmission 按上下文、请求内容和 authority scope 顺序校验提交并派生句柄。
func (a *Authority) validateSubmission(
	ctx context.Context,
	request recoverycustody.SubmissionRequest,
) (recoverycustody.SubmissionHandle, error) {
	if err := ctx.Err(); err != nil {
		return recoverycustody.SubmissionHandle{}, err
	}
	if err := request.Validate(); err != nil {
		return recoverycustody.SubmissionHandle{}, err
	}
	if request.SecurityDomainID != a.config.SecurityDomainID || request.AuthorityProfile != a.config.AuthorityProfile {
		return recoverycustody.SubmissionHandle{}, recoverycustody.ErrCustodyConflict
	}
	return recoverycustody.NewSubmissionHandle(request)
}

// submitOnConnection 在既有写事务连接上精确重放或插入 pending submission。
func (a *Authority) submitOnConnection(
	ctx context.Context,
	conn *sql.Conn,
	request recoverycustody.SubmissionRequest,
	handle recoverycustody.SubmissionHandle,
) (recoverycustody.Outcome, error) {
	storedRequest, storedOutcome, err := scanSubmission(conn.QueryRowContext(ctx, selectSubmissionSQL, request.OperationID))
	if err == nil {
		return matchStoredSubmission(request, handle, storedRequest, storedOutcome)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return recoverycustody.Outcome{}, safeAuthorityError("read submission", err)
	}
	args, err := submissionInsertArgs(request, handle, canonicalTime(time.Now().UTC()))
	if err != nil {
		return recoverycustody.Outcome{}, err
	}
	result, err := conn.ExecContext(ctx, insertSubmissionSQL, args...)
	if err != nil {
		return recoverycustody.Outcome{}, safeAuthorityError("insert submission", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return recoverycustody.Outcome{}, safeAuthorityError("inspect submission insert", err)
	}
	if rows != 1 {
		return recoverycustody.Outcome{}, recoverycustody.ErrCustodyConflict
	}
	return recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusPending}, nil
}

// matchStoredSubmission 双向核对持久化提交与本次请求，避免同 operation 异请求重放。
func matchStoredSubmission(
	request recoverycustody.SubmissionRequest,
	handle recoverycustody.SubmissionHandle,
	storedRequest recoverycustody.SubmissionRequest,
	storedOutcome recoverycustody.Outcome,
) (recoverycustody.Outcome, error) {
	if err := recoverycustody.MatchSubmission(storedOutcome.Handle, request); err != nil {
		return recoverycustody.Outcome{}, err
	}
	if err := recoverycustody.MatchSubmission(handle, storedRequest); err != nil {
		return recoverycustody.Outcome{}, err
	}
	return storedOutcome, nil
}

// Query 只读取原 operation，并在返回 terminal 前重新执行外部 ReceiptVerifier。
func (a *Authority) Query(ctx context.Context, handle recoverycustody.SubmissionHandle) (recoverycustody.Outcome, error) {
	if err := a.validateQueryHandle(ctx, handle); err != nil {
		return recoverycustody.Outcome{}, err
	}
	var outcome recoverycustody.Outcome
	err := a.withAuthorityReadPath(ctx, func() error {
		var err error
		outcome, err = a.readMatchedSubmission(ctx, handle)
		if err != nil {
			return err
		}
		if outcome.Status == recoverycustody.StatusPending {
			outcome, err = a.queryPendingSubmission(ctx, handle, outcome)
			return err
		}
		pending := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusPending}
		outcome, err = loadTerminalOutcome(ctx, a.db, handle.OperationID, pending, a.config, a.verifier)
		return err
	})
	if err != nil {
		return recoverycustody.Outcome{}, err
	}
	return outcome, nil
}

// validateQueryHandle 按上下文、句柄内容和 authority scope 顺序校验查询输入。
func (a *Authority) validateQueryHandle(ctx context.Context, handle recoverycustody.SubmissionHandle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := handle.Validate(); err != nil {
		return err
	}
	if handle.SecurityDomainID != a.config.SecurityDomainID || handle.AuthorityProfile != a.config.AuthorityProfile {
		return recoverycustody.ErrCustodyConflict
	}
	return nil
}

// readMatchedSubmission 读取原 operation，并同时核对请求句柄与持久化 outcome 身份。
func (a *Authority) readMatchedSubmission(
	ctx context.Context,
	handle recoverycustody.SubmissionHandle,
) (recoverycustody.Outcome, error) {
	request, outcome, err := scanSubmission(a.db.QueryRowContext(ctx, selectSubmissionSQL, handle.OperationID))
	if errors.Is(err, sql.ErrNoRows) {
		return recoverycustody.Outcome{}, ErrResolutionNotFound
	}
	if err != nil {
		return recoverycustody.Outcome{}, safeAuthorityError("query submission", err)
	}
	if err := recoverycustody.MatchSubmission(handle, request); err != nil {
		return recoverycustody.Outcome{}, recoverycustody.ErrCustodyConflict
	}
	if outcome.Handle != handle {
		return recoverycustody.Outcome{}, recoverycustody.ErrCustodyConflict
	}
	return outcome, nil
}

// queryPendingSubmission 区分未解析 pending 与已被唯一 candidate 占用的活动解析。
func (a *Authority) queryPendingSubmission(
	ctx context.Context,
	handle recoverycustody.SubmissionHandle,
	pending recoverycustody.Outcome,
) (recoverycustody.Outcome, error) {
	stored, err := a.readResolution(ctx, handle.OperationID)
	if errors.Is(err, sql.ErrNoRows) {
		return pending, pending.Validate()
	}
	if err != nil {
		return recoverycustody.Outcome{}, safeAuthorityError("query active resolution", err)
	}
	if stored.State != resolutionStateActive {
		return recoverycustody.Outcome{}, ErrSchemaMismatch
	}
	indeterminate := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusIndeterminate,
		ReasonCode: "recovery_custody_resolution_active"}
	return indeterminate, indeterminate.Validate()
}

// Resolve 持久化唯一候选后统一进入 reconciliation，不在 tx1 返回路径直接调用 Finalize。
func (a *Authority) Resolve(ctx context.Context, request ResolutionRequest) (recoverycustody.Outcome, error) {
	if err := request.Validate(); err != nil {
		return recoverycustody.Outcome{}, err
	}
	if a.queryOnly.Load() {
		return a.queryAfterWriteRefusal(ctx, request.Handle, ErrAuthorityReadOnly)
	}
	_, _, terminal, err := a.prepareResolution(ctx, request)
	if errors.Is(err, ErrCommitOutcomeUnknown) {
		return a.queryAfterWriteRefusal(ctx, request.Handle, err)
	}
	if err != nil {
		return recoverycustody.Outcome{}, err
	}
	if terminal {
		return a.Query(ctx, request.Handle)
	}
	return a.ReconcileResolution(ctx, request.Handle)
}

// ReconcileResolution 仅恢复已持久化且仍持有本地 head claim 的精确 active resolution。
func (a *Authority) ReconcileResolution(
	ctx context.Context,
	handle recoverycustody.SubmissionHandle,
) (recoverycustody.Outcome, error) {
	if err := a.validateQueryHandle(ctx, handle); err != nil {
		return recoverycustody.Outcome{}, err
	}
	if a.queryOnly.Load() {
		return a.queryAfterWriteRefusal(ctx, handle, ErrAuthorityReadOnly)
	}
	expected, terminal, err := a.loadReconcileExpected(ctx, handle)
	if err != nil {
		return recoverycustody.Outcome{}, err
	}
	if terminal {
		return a.Query(ctx, handle)
	}
	return a.reconcileExpected(ctx, expected)
}

// loadReconcileExpected 读取并核对只属于同句柄的 active resolution；terminal 只返回终态标记。
func (a *Authority) loadReconcileExpected(
	ctx context.Context,
	handle recoverycustody.SubmissionHandle,
) (ExpectedResolution, bool, error) {
	stored, err := a.readResolution(ctx, handle.OperationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ExpectedResolution{}, false, ErrResolutionNotFound
	}
	if err != nil {
		return ExpectedResolution{}, false, safeAuthorityError("read resolution", err)
	}
	if stored.Expected.Handle != handle {
		return ExpectedResolution{}, false, recoverycustody.ErrCustodyConflict
	}
	if stored.State == resolutionStateTerminal {
		return stored.Expected, true, nil
	}
	if stored.State != resolutionStateActive {
		return ExpectedResolution{}, false, ErrSchemaMismatch
	}
	return stored.Expected, false, nil
}

// reconcileExpected 查询原 finalization operation，并只应用经过严格验证的 observation。
func (a *Authority) reconcileExpected(
	ctx context.Context,
	expected ExpectedResolution,
) (recoverycustody.Outcome, error) {
	finalization, err := BindFinalizationRequest(expected)
	if err != nil {
		return a.pendingWithError(ctx, expected.Handle, err)
	}
	observation, err := a.finalizer.Query(ctx, finalization)
	if err != nil {
		return a.pendingWithError(ctx, expected.Handle, ErrInvalidEvidence)
	}
	if err := observation.Validate(finalization); err != nil {
		return a.pendingWithError(ctx, expected.Handle, err)
	}
	return a.applyFinalizationObservation(ctx, finalization, observation)
}

// applyFinalizationObservation 按封闭查询状态提交 evidence、等待或执行同键原子 create-or-get。
func (a *Authority) applyFinalizationObservation(
	ctx context.Context,
	finalization FinalizationRequest,
	observation FinalizationObservation,
) (recoverycustody.Outcome, error) {
	handle := finalization.Handle
	switch observation.State {
	case FinalizationQueryFinalized:
		return a.commitEvidence(ctx, finalization, *observation.Evidence)
	case FinalizationQueryInflight:
		return finalizationInflightOutcome(handle)
	case FinalizationQueryAbsent:
		if err := a.confirmActiveResolutionClaim(ctx, finalization.ExpectedResolution); err != nil {
			return a.pendingWithError(ctx, handle, err)
		}
		evidence, err := a.finalizer.Finalize(ctx, finalization)
		if err != nil {
			return a.pendingWithError(ctx, handle, ErrInvalidEvidence)
		}
		return a.commitEvidence(ctx, finalization, evidence)
	default:
		return a.pendingWithError(ctx, handle, ErrInvalidEvidence)
	}
}

// ResumeResolution 是 ReconcileResolution 的兼容别名。
func (a *Authority) ResumeResolution(
	ctx context.Context,
	handle recoverycustody.SubmissionHandle,
) (recoverycustody.Outcome, error) {
	return a.ReconcileResolution(ctx, handle)
}

// prepareResolution 在一个写事务中读取 submission、固化唯一候选并占用 authority head。
func (a *Authority) prepareResolution(ctx context.Context, request ResolutionRequest) (ExpectedResolution, bool, bool, error) {
	var prepared resolutionPreparation
	err := a.writeTransaction(ctx, func(conn *sql.Conn) error {
		var prepareErr error
		prepared, prepareErr = a.prepareResolutionOnConnection(ctx, conn, request)
		return prepareErr
	})
	return prepared.expected, prepared.created, prepared.terminal, err
}

// prepareResolutionOnConnection 保持 submission、现有 candidate、head、插入和 claim 的读取写入顺序。
func (a *Authority) prepareResolutionOnConnection(
	ctx context.Context,
	conn *sql.Conn,
	request ResolutionRequest,
) (resolutionPreparation, error) {
	storedRequest, outcome, err := scanSubmission(conn.QueryRowContext(ctx, selectSubmissionSQL, request.Handle.OperationID))
	if err != nil {
		return resolutionPreparation{}, safeAuthorityError("read resolution submission", err)
	}
	if err := recoverycustody.MatchSubmission(request.Handle, storedRequest); err != nil {
		return resolutionPreparation{}, err
	}
	stored, err := scanStoredResolution(conn.QueryRowContext(ctx, selectResolutionSQL, request.Handle.OperationID))
	if err == nil {
		return prepareExistingResolution(request, outcome, stored)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return resolutionPreparation{}, safeAuthorityError("read existing resolution", err)
	}
	head, err := a.readClaimableHead(ctx, conn, outcome)
	if err != nil {
		return resolutionPreparation{}, err
	}
	expected, err := expectedResolutionFromHead(request, head)
	if err != nil {
		return resolutionPreparation{}, err
	}
	if err := a.insertAndClaimResolution(ctx, conn, expected, head); err != nil {
		return resolutionPreparation{}, err
	}
	return resolutionPreparation{expected: expected, created: true}, nil
}

// prepareExistingResolution 校验同 operation 的候选请求，并复用已持久化的 expected snapshot。
func prepareExistingResolution(
	request ResolutionRequest,
	outcome recoverycustody.Outcome,
	stored storedResolution,
) (resolutionPreparation, error) {
	if stored.Expected.ResolutionRequest != request {
		return resolutionPreparation{}, recoverycustody.ErrResolutionConflict
	}
	terminal := outcome.Status != recoverycustody.StatusPending || stored.State == resolutionStateTerminal
	return resolutionPreparation{expected: stored.Expected, terminal: terminal}, nil
}

// readClaimableHead 要求 submission 仍为 pending，并读取尚未被其他 candidate 占用的 head。
func (a *Authority) readClaimableHead(
	ctx context.Context,
	conn *sql.Conn,
	outcome recoverycustody.Outcome,
) (authorityHead, error) {
	if outcome.Status != recoverycustody.StatusPending {
		return authorityHead{}, ErrSchemaMismatch
	}
	head, err := scanAuthorityHead(conn.QueryRowContext(
		ctx,
		selectAuthorityHeadSQL,
		a.config.SecurityDomainID,
		a.config.AuthorityProfile,
	))
	if err != nil {
		return authorityHead{}, safeAuthorityError("read authority head", err)
	}
	if head.ActiveResolutionID.Valid {
		return authorityHead{}, recoverycustody.ErrResolutionConflict
	}
	return head, nil
}

// expectedResolutionFromHead 冻结 candidate 提交时观察到的 head 与 security-time CAS 条件。
func expectedResolutionFromHead(request ResolutionRequest, head authorityHead) (ExpectedResolution, error) {
	expected := ExpectedResolution{ResolutionRequest: request, HeadID: head.HeadID, ExpectedHeadRevision: head.Revision,
		ExpectedHeadRootDigest: head.RootDigest, ExpectedAntiRollbackGeneration: head.AntiRollbackGeneration,
		ExpectedAnchorDigest: head.AntiRollbackAnchorDigest, SecurityTimeLineageDigest: head.SecurityTimeLineageDigest,
		ExpectedSecurityTimeGeneration: head.SecurityTimeGeneration, ExpectedSecurityTimeRevision: head.SecurityTimeRevision}
	return BindExpectedResolutionDigest(expected)
}

// insertAndClaimResolution 先写入 candidate，再用完整 head snapshot 执行单赢家 claim。
func (a *Authority) insertAndClaimResolution(
	ctx context.Context,
	conn *sql.Conn,
	expected ExpectedResolution,
	head authorityHead,
) error {
	args, err := resolutionInsertArgs(expected, canonicalTime(time.Now().UTC()))
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, insertResolutionSQL, args...); err != nil {
		return safeAuthorityError("insert resolution", err)
	}
	request := expected.ResolutionRequest
	result, err := conn.ExecContext(ctx, claimHeadSQL, request.ResolutionID, a.config.SecurityDomainID,
		a.config.AuthorityProfile, head.HeadID, head.Revision, head.RootDigest, head.SecurityTimeLineageDigest,
		head.SecurityTimeGeneration, head.SecurityTimeRevision, head.AntiRollbackGeneration, head.AntiRollbackAnchorDigest)
	if err != nil {
		return safeAuthorityError("claim authority head", err)
	}
	return requireOneRow(result, "claim authority head")
}

// finalizationInflightOutcome 返回同句柄且不携带 receipt 的稳定处理中结果。
func finalizationInflightOutcome(handle recoverycustody.SubmissionHandle) (recoverycustody.Outcome, error) {
	outcome := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusIndeterminate,
		ReasonCode: "recovery_custody_finalization_inflight"}
	return outcome, outcome.Validate()
}

// confirmActiveResolutionClaim 在租约保护的单一 SQLite 事务中重读 active resolution 与完整 head claim。
func (a *Authority) confirmActiveResolutionClaim(ctx context.Context, expected ExpectedResolution) error {
	return a.writeTransaction(ctx, func(conn *sql.Conn) error {
		if err := validateActiveResolution(ctx, conn, expected); err != nil {
			return err
		}
		head, err := scanAuthorityHead(conn.QueryRowContext(
			ctx,
			selectAuthorityHeadSQL,
			a.config.SecurityDomainID,
			a.config.AuthorityProfile,
		))
		if err != nil {
			return safeAuthorityError("reconfirm authority head", err)
		}
		if !activeHeadClaimMatches(head, expected) {
			return recoverycustody.ErrResolutionConflict
		}
		return nil
	})
}

// activeHeadClaimMatches 要求 current head 仍是 tx1 固化前驱并由同一 resolution 占用。
func activeHeadClaimMatches(head authorityHead, expected ExpectedResolution) bool {
	return head.HeadID == expected.HeadID &&
		head.Revision == expected.ExpectedHeadRevision &&
		head.RootDigest == expected.ExpectedHeadRootDigest &&
		head.SecurityTimeLineageDigest == expected.SecurityTimeLineageDigest &&
		head.SecurityTimeGeneration == expected.ExpectedSecurityTimeGeneration &&
		head.SecurityTimeRevision == expected.ExpectedSecurityTimeRevision &&
		head.AntiRollbackGeneration == expected.ExpectedAntiRollbackGeneration &&
		head.AntiRollbackAnchorDigest == expected.ExpectedAnchorDigest &&
		head.ActiveResolutionID.Valid && head.ActiveResolutionID.String == expected.ResolutionID
}

// commitEvidence 先验证 evidence 身份与 receipt，再在独立写事务中执行 terminal CAS。
func (a *Authority) commitEvidence(ctx context.Context, request FinalizationRequest, evidence FinalizedEvidence) (recoverycustody.Outcome, error) {
	expected := request.ExpectedResolution
	if err := validateEvidenceIdentity(a.config, request, evidence); err != nil {
		return a.pendingWithError(ctx, request.Handle, err)
	}
	pending := recoverycustody.Outcome{Handle: expected.Handle, Status: recoverycustody.StatusPending}
	if _, err := recoverycustody.ApplyVerifiedReceipt(ctx, pending, evidence.Receipt, a.verifier); err != nil {
		return a.pendingWithError(ctx, expected.Handle, err)
	}
	if a.queryOnly.Load() {
		return a.queryAfterWriteRefusal(ctx, expected.Handle, ErrAuthorityReadOnly)
	}
	err := a.writeTransaction(ctx, func(conn *sql.Conn) error { return a.writeEvidence(ctx, conn, expected, evidence) })
	if errors.Is(err, ErrCommitOutcomeUnknown) {
		return a.queryAfterWriteRefusal(ctx, expected.Handle, err)
	}
	if err != nil {
		return a.pendingWithError(ctx, expected.Handle, err)
	}
	return a.Query(ctx, expected.Handle)
}

// writeEvidence 按 anchor、checkpoint、receipt、head、submission、resolution 的固定顺序提交证据。
func (a *Authority) writeEvidence(ctx context.Context, conn *sql.Conn, expected ExpectedResolution, evidence FinalizedEvidence) error {
	if err := validateActiveResolution(ctx, conn, expected); err != nil {
		return err
	}
	now := canonicalTime(time.Now().UTC())
	if err := anchorExpectedResolution(ctx, conn, expected, evidence.Checkpoint, now); err != nil {
		return err
	}
	if err := a.insertCheckpoint(ctx, conn, expected, evidence.Checkpoint); err != nil {
		return err
	}
	if err := insertTerminalReceipt(ctx, conn, expected, evidence, now); err != nil {
		return err
	}
	if err := a.advanceHead(ctx, conn, expected, evidence); err != nil {
		return err
	}
	if err := terminalizeSubmission(ctx, conn, evidence.Receipt, now); err != nil {
		return err
	}
	return terminalizeResolution(ctx, conn, expected, now)
}

// validateActiveResolution 要求当前活动 candidate 及其 expected snapshot 与提交证据完全一致。
func validateActiveResolution(ctx context.Context, conn *sql.Conn, expected ExpectedResolution) error {
	stored, err := scanStoredResolution(conn.QueryRowContext(ctx, selectResolutionSQL, expected.Handle.OperationID))
	if err != nil || stored.State != resolutionStateActive || stored.Expected != expected {
		return recoverycustody.ErrResolutionConflict
	}
	return nil
}

// anchorExpectedResolution 用完整 candidate 与 security-time snapshot 对 resolution 执行 anchor CAS。
func anchorExpectedResolution(
	ctx context.Context,
	conn *sql.Conn,
	expected ExpectedResolution,
	checkpoint FinalizedCheckpoint,
	now string,
) error {
	result, err := conn.ExecContext(ctx, anchorResolutionSQL, checkpoint.Generation, checkpoint.AnchorDigest,
		checkpoint.ResultHeadRootDigest, checkpoint.FinalizationDigest, canonicalTime(checkpoint.AnchoredAt), now,
		expected.Handle.OperationID, expected.ResolutionID, expected.ResolutionRequestDigest,
		expected.FinalizationOperationID, expected.ExpectedResolutionDigest, checkpoint.FinalizationDigest,
		expected.HeadID, expected.ExpectedHeadRevision,
		expected.ExpectedHeadRootDigest, expected.ExpectedAntiRollbackGeneration, expected.ExpectedAnchorDigest,
		expected.SecurityTimeLineageDigest, expected.ExpectedSecurityTimeGeneration, expected.ExpectedSecurityTimeRevision)
	if err != nil {
		return safeAuthorityError("anchor resolution", err)
	}
	return requireOneRow(result, "anchor resolution")
}

// insertCheckpoint 记录本次 resolution 的 anti-rollback checkpoint，保持其位于 receipt 之前。
func (a *Authority) insertCheckpoint(
	ctx context.Context,
	conn *sql.Conn,
	expected ExpectedResolution,
	checkpoint FinalizedCheckpoint,
) error {
	if _, err := conn.ExecContext(ctx, insertCheckpointSQL, a.config.SecurityDomainID, a.config.AuthorityProfile,
		checkpoint.Generation, checkpoint.PredecessorGeneration, checkpoint.PredecessorAnchorDigest, checkpoint.AnchorDigest,
		checkpoint.ResultHeadRootDigest, checkpoint.AnchorRef, checkpoint.FinalizationDigest, checkpoint.FinalizationRef,
		canonicalTime(checkpoint.AnchoredAt), expected.ResolutionID); err != nil {
		return safeAuthorityError("insert checkpoint", err)
	}
	return nil
}

// insertTerminalReceipt 在 checkpoint 成功后序列化并插入经验证的 terminal receipt。
func insertTerminalReceipt(
	ctx context.Context,
	conn *sql.Conn,
	expected ExpectedResolution,
	evidence FinalizedEvidence,
	now string,
) error {
	receiptJSONBytes, err := json.Marshal(evidence.Receipt)
	if err != nil {
		return fmt.Errorf("encode terminal receipt: %w", err)
	}
	if _, err := conn.ExecContext(ctx, insertTerminalReceiptSQL,
		terminalReceiptInsertArgs(expected, evidence, string(receiptJSONBytes), now)...); err != nil {
		return safeAuthorityError("insert terminal receipt", err)
	}
	return nil
}

// advanceHead 用 expected 中的完整 head 与 security-time 条件推进 authority head。
func (a *Authority) advanceHead(
	ctx context.Context,
	conn *sql.Conn,
	expected ExpectedResolution,
	evidence FinalizedEvidence,
) error {
	r := evidence.Receipt
	checkpoint := evidence.Checkpoint
	result, err := conn.ExecContext(ctx, advanceHeadSQL, r.HeadRevision, r.HeadRootDigest, r.SecurityTimeGeneration,
		r.SecurityTimeRevision, r.AntiRollbackGeneration, r.AntiRollbackAnchorDigest, checkpoint.AnchorRef,
		a.config.SecurityDomainID, a.config.AuthorityProfile, expected.HeadID, expected.ExpectedHeadRevision,
		expected.ExpectedHeadRootDigest, expected.SecurityTimeLineageDigest, expected.ExpectedSecurityTimeGeneration,
		expected.ExpectedSecurityTimeRevision, expected.ExpectedAntiRollbackGeneration, expected.ExpectedAnchorDigest,
		expected.ResolutionID)
	if err != nil {
		return safeAuthorityError("advance authority head", err)
	}
	return requireOneRow(result, "advance authority head")
}

// terminalizeSubmission 按 receipt kind 选择唯一 terminal 状态并 CAS 更新 submission。
func terminalizeSubmission(ctx context.Context, conn *sql.Conn, receipt recoverycustody.ExternalReceipt, now string) error {
	status := recoverycustody.StatusAvailable
	if receipt.Kind == recoverycustody.ReceiptAbort {
		status = recoverycustody.StatusAborted
	}
	result, err := conn.ExecContext(ctx, terminalizeSubmissionSQL, status, receipt.ReceiptDigest, now, receipt.OperationID)
	if err != nil {
		return safeAuthorityError("terminalize submission", err)
	}
	return requireOneRow(result, "terminalize submission")
}

// terminalizeResolution 最后关闭活动 resolution，确保前序所有权威写入已成功。
func terminalizeResolution(ctx context.Context, conn *sql.Conn, expected ExpectedResolution, now string) error {
	result, err := conn.ExecContext(ctx, terminalizeResolutionSQL, now, expected.Handle.OperationID, expected.ResolutionID)
	if err != nil {
		return safeAuthorityError("terminalize resolution", err)
	}
	return requireOneRow(result, "terminalize resolution")
}

// readResolution 按 operation ID 读取并解码持久化 resolution。
func (a *Authority) readResolution(ctx context.Context, operationID string) (storedResolution, error) {
	if err := a.validateDatabaseIdentity(); err != nil {
		return storedResolution{}, err
	}
	return scanStoredResolution(a.db.QueryRowContext(ctx, selectResolutionSQL, operationID))
}

// pendingWithError 在 evidence 不可用时重新查询 authority，并把未终态结果收敛为 indeterminate。
func (a *Authority) pendingWithError(ctx context.Context, handle recoverycustody.SubmissionHandle, cause error) (recoverycustody.Outcome, error) {
	outcome, err := a.Query(ctx, handle)
	if err != nil {
		indeterminate := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusIndeterminate,
			ReasonCode: "recovery_custody_evidence_unavailable"}
		return indeterminate, errors.Join(cause, err)
	}
	if outcome.Status == recoverycustody.StatusPending || outcome.Status == recoverycustody.StatusIndeterminate {
		outcome = recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusIndeterminate,
			ReasonCode: "recovery_custody_evidence_unavailable"}
	}
	return outcome, cause
}

// queryAfterWriteRefusal 在只读熔断后只查询 authority，并返回稳定的 query-only indeterminate 原因。
func (a *Authority) queryAfterWriteRefusal(ctx context.Context, handle recoverycustody.SubmissionHandle, cause error) (recoverycustody.Outcome, error) {
	outcome, err := a.Query(ctx, handle)
	if err != nil {
		indeterminate := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusIndeterminate,
			ReasonCode: "recovery_custody_authority_query_only"}
		return indeterminate, errors.Join(cause, err)
	}
	if outcome.Status == recoverycustody.StatusPending || outcome.Status == recoverycustody.StatusIndeterminate {
		outcome = recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusIndeterminate,
			ReasonCode: "recovery_custody_authority_query_only"}
	}
	return outcome, cause
}

// validateAuthorityInputs 校验配置、独立路径与 lease 依赖并构造完整作用域。
func validateAuthorityInputs(ctx context.Context, config Config, lease AuthorityLease) (Config, AuthorityScope, error) {
	if err := ctx.Err(); err != nil {
		return Config{}, AuthorityScope{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, AuthorityScope{}, err
	}
	if dependencyIsNil(lease) {
		return Config{}, AuthorityScope{}, fmt.Errorf("%w: lease is required", ErrInvalidConfig)
	}
	physicalAuthorityPath, err := validateIndependentPaths(config)
	if err != nil {
		return Config{}, AuthorityScope{}, err
	}
	// Keep every subsequent require/open/bootstrap operation on the same
	// canonical physical path that was validated above. This prevents an
	// intermediate symlink alias from being swapped after validation.
	config.Path = physicalAuthorityPath
	scope, err := authorityScopeFromConfig(config, physicalAuthorityPath)
	if err != nil {
		return Config{}, AuthorityScope{}, err
	}
	return config, scope, nil
}

// authorityScopeFromConfig 把配置中的全部 authority 身份绑定到已解析物理状态路径。
func authorityScopeFromConfig(config Config, physicalAuthorityPath string) (AuthorityScope, error) {
	statePathDigest, err := digestValue(StatePathDigestDomain, physicalAuthorityPath)
	if err != nil {
		return AuthorityScope{}, err
	}
	return AuthorityScope{
		StoreID:                    config.StoreID,
		StatePathDigest:            statePathDigest,
		AuthorityID:                config.AuthorityID,
		AuthorityClusterID:         config.AuthorityClusterID,
		AuthorityGeneration:        config.AuthorityGeneration,
		OwnerID:                    config.OwnerID,
		OwnerGeneration:            config.OwnerGeneration,
		OwnerFenceDigest:           config.OwnerFenceDigest,
		AuthorityProfile:           config.AuthorityProfile,
		AuthorityProfileGeneration: config.AuthorityProfileGeneration,
		AuthorityProfileDigest:     config.AuthorityProfileDigest,
		SecurityDomainID:           config.SecurityDomainID,
		RetentionPolicyDigest:      config.RetentionPolicyDigest,
	}, nil
}

// safeAuthorityError 隐去底层 SQLite 细节，只暴露稳定的 custody unavailable 分类。
func safeAuthorityError(action string, _ error) error {
	return fmt.Errorf("%s: %w", action, recoverycustody.ErrCustodyUnavailable)
}

var _ recoverycustody.Client = (*Authority)(nil)
