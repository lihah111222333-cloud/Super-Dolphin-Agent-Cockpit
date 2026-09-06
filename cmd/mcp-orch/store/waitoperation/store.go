package waitoperation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Store is the sole persistence owner for migration 136 wait-operation plans.
// Once a commit result is unknown the owner remains fenced and permits only
// exact query/reconciliation; no write is retried or guessed.
type Store struct {
	db            *sql.DB
	queries       *storedb.Queries
	clock         Clock
	access        *accesswait.Store
	commitMarkers *CommitMarkerStore
	fenced        atomic.Bool
	processFenced atomic.Bool
	txMu          sync.Mutex
}

type targetHistory struct {
	rows   []storedb.OrchestrationWaitOperationTargetJournal
	states []TargetState
}

// NewStore constructs an owner with an explicit UTC clock seam.
// NewStore 构造仅支持计划读写的 wait-operation owner。
func NewStore(db *sql.DB, clock Clock) (*Store, error) {
	if db == nil {
		return nil, errors.New("wait operation store requires database")
	}
	if clock == nil {
		return nil, errors.New("wait operation store requires explicit clock")
	}
	return &Store{db: db, queries: storedb.New(db), clock: clock}, nil
}

// NewStoreWithCommitMarkers constructs a wait-operation owner with an
// optional durable commit-marker owner. The marker owner must use the exact
// same *sql.DB handle so marker and business writes share one database.
// NewStore remains the plan-only constructor for callers that do not opt in.
// NewStoreWithCommitMarkers 构造共享同一数据库的 commit-marker wait-operation owner。
func NewStoreWithCommitMarkers(db *sql.DB, clock Clock, commitMarkers *CommitMarkerStore) (*Store, error) {
	store, err := NewStore(db, clock)
	if err != nil {
		return nil, err
	}
	if err := validateCommitMarkerStore(db, commitMarkers); err != nil {
		return nil, err
	}
	store.commitMarkers = commitMarkers
	store.refreshCommitFenceAtConstruction(context.Background())
	return store, nil
}

// NewStoreWithAccess constructs a wait-operation owner with the explicit
// access/wait owner required for durable target provisioning. NewStore remains
// plan-only and cannot mint grants or wait handles.
// NewStoreWithAccess 构造具备显式 access/wait owner 的 provisioning store。
func NewStoreWithAccess(db *sql.DB, clock Clock, access *accesswait.Store) (*Store, error) {
	if access == nil {
		return nil, errors.New("wait operation store requires explicit access wait owner")
	}
	store, err := NewStore(db, clock)
	if err != nil {
		return nil, err
	}
	store.access = access
	return store, nil
}

// NewStoreWithAccessAndCommitMarkers constructs a wait-operation owner with
// both durable access/wait provisioning and optional commit markers. Both
// dependencies remain explicit; marker and business writes must share db.
// NewStoreWithAccessAndCommitMarkers 构造带 access/wait 与 commit-marker owner 的 provisioning store。
func NewStoreWithAccessAndCommitMarkers(db *sql.DB, clock Clock, access *accesswait.Store, commitMarkers *CommitMarkerStore) (*Store, error) {
	store, err := NewStoreWithAccess(db, clock, access)
	if err != nil {
		return nil, err
	}
	if err := validateCommitMarkerStore(db, commitMarkers); err != nil {
		return nil, err
	}
	store.commitMarkers = commitMarkers
	store.refreshCommitFenceAtConstruction(context.Background())
	return store, nil
}

// validateCommitMarkerStore 校验 marker owner 与业务数据库及依赖时钟的一致性。
func validateCommitMarkerStore(db *sql.DB, commitMarkers *CommitMarkerStore) error {
	if commitMarkers == nil {
		return errors.New("wait operation store requires explicit commit marker owner")
	}
	if commitMarkers.db == nil || commitMarkers.clock == nil {
		return errors.New("wait operation commit marker owner is not initialized")
	}
	if db == nil || commitMarkers.db != db {
		return errors.New("wait operation commit marker owner must use the same database")
	}
	return nil
}

// CheckSchema proves that migration 136 is applied and both owner tables are
// queryable. It is read-only and remains available after a write fence.
// CheckSchema 验证 migration 136 及 wait-operation owner 表可查询。
func (s *Store) CheckSchema(ctx context.Context) error {
	if err := s.ready(ctx, false); err != nil {
		return err
	}
	if err := s.checkCommitMarkerSchema(ctx); err != nil {
		return err
	}
	if err := s.checkWaitOperationSchema(ctx); err != nil {
		return err
	}
	if s.commitMarkers != nil {
		s.refreshCommitFence(ctx)
	}
	return nil
}

func (s *Store) checkCommitMarkerSchema(ctx context.Context) error {
	if s.commitMarkers == nil {
		return nil
	}
	if err := validateCommitMarkerStore(s.db, s.commitMarkers); err != nil {
		return errors.Join(ErrSchemaUnavailable, err)
	}
	return s.commitMarkers.CheckSchema(ctx)
}

func (s *Store) checkWaitOperationSchema(ctx context.Context) error {
	version, err := s.queries.CheckOrchestrationWaitOperationSchema(ctx)
	if err != nil {
		return errors.Join(ErrSchemaUnavailable, err)
	}
	if version < 136 {
		return fmt.Errorf("%w: migration version %d is below 136", ErrSchemaUnavailable, version)
	}
	return s.checkWaitOperationTables(ctx)
}

func (s *Store) checkWaitOperationTables(ctx context.Context) error {
	if _, err := s.queries.CountOrchestrationWaitOperations(ctx); err != nil {
		return errors.Join(ErrSchemaUnavailable, err)
	}
	if _, err := s.queries.CountOrchestrationWaitOperationTargets(ctx); err != nil {
		return errors.Join(ErrSchemaUnavailable, err)
	}
	return nil
}

// Plan atomically inserts the operation and every initial target plan. The
// aggregate remains provisioning until a later owner appends real target
// progress; a replay is accepted only when every immutable field and target
// candidate is byte-for-byte identical.
// Plan 在事务中写入操作计划并支持不可变字段精确重放。
func (s *Store) Plan(ctx context.Context, request PlanRequest) (Operation, error) {
	prepared, err := s.preparePlan(ctx, request)
	if err != nil {
		return Operation{}, err
	}
	var result Operation
	err = s.withImmediateMutation(ctx, &prepared.descriptor, func(q *storedb.Queries, _ storedb.DBTX) error {
		if err := s.planTx(ctx, q, prepared.request, prepared.derivedTargets, prepared.targetSetDigest, prepared.issuedAt, &result); err != nil {
			return err
		}
		prepared.descriptor.PostStateDigest = operationStateDigest(result)
		return nil
	})
	err = s.resolvePlanCommit(ctx, prepared, err, &result)
	if errors.Is(err, ErrCommitUnknown) {
		return result, err
	}
	return result, err
}

type preparedPlan struct {
	request         PlanRequest
	derivedTargets  []storedb.CreateOrchestrationWaitTargetPlanParams
	targetSetDigest string
	issuedAt        time.Time
	descriptor      CommitMarkerDescriptor
}

// preparePlan 统一完成计划校验、目标派生、schema 检查和 marker 描述子构造。
func (s *Store) preparePlan(ctx context.Context, request PlanRequest) (preparedPlan, error) {
	if err := s.ready(ctx, false); err != nil {
		return preparedPlan{}, err
	}
	if err := request.validateShape(); err != nil {
		return preparedPlan{}, err
	}
	derivedTargets, targetSetDigest, err := deriveTargets(request)
	if err != nil {
		return preparedPlan{}, err
	}
	if request.TargetSetDigest != "" && request.TargetSetDigest != targetSetDigest {
		return preparedPlan{}, errors.Join(ErrReplayConflict, invalidOperation("target_set_digest does not match canonical targets"))
	}
	issuedAt, err := s.nowUTC()
	if err != nil {
		return preparedPlan{}, err
	}
	if err := s.CheckSchema(ctx); err != nil {
		return preparedPlan{}, err
	}
	for index := range derivedTargets {
		derivedTargets[index].RecordedAt = formatUTC(issuedAt)
	}
	request.TargetSetDigest = targetSetDigest
	descriptor, err := NewMutationDescriptor(MutationPlan, request.OperationKey, request.OperationDigest, "", canonicalPlanMarkerRequest(request, derivedTargets), "")
	if err != nil {
		return preparedPlan{}, err
	}
	return preparedPlan{request: request, derivedTargets: derivedTargets, targetSetDigest: targetSetDigest, issuedAt: issuedAt, descriptor: descriptor}, nil
}

func (s *Store) resolvePlanCommit(ctx context.Context, plan preparedPlan, err error, result *Operation) error {
	if !errors.Is(err, ErrCommitMarkerResolved) {
		return err
	}
	existing, lookupErr := s.queries.GetOrchestrationWaitOperation(ctx, storedb.GetOrchestrationWaitOperationParams{OperationKey: plan.request.OperationKey})
	if errors.Is(lookupErr, sql.ErrNoRows) {
		return ErrOperationNotFound
	}
	if lookupErr != nil {
		return classifyDBError("read resolved wait operation replay", lookupErr, ErrReplayConflict)
	}
	return s.replayPlanTx(ctx, s.queries, plan.request, plan.derivedTargets, result, existing)
}

// planTx 执行计划的存在性判定、插入或重放。
func (s *Store) planTx(ctx context.Context, q *storedb.Queries, request PlanRequest, derived []storedb.CreateOrchestrationWaitTargetPlanParams, targetSetDigest string, issuedAt time.Time, result *Operation) error {
	existing, lookupErr := q.GetOrchestrationWaitOperation(ctx, storedb.GetOrchestrationWaitOperationParams{OperationKey: request.OperationKey})
	if lookupErr == nil {
		return s.replayPlanTx(ctx, q, request, derived, result, existing)
	}
	if !errors.Is(lookupErr, sql.ErrNoRows) {
		return classifyDBError("check existing wait operation", lookupErr, ErrReplayConflict)
	}
	if err := s.validatePlanInsert(request, issuedAt); err != nil {
		return err
	}
	row, insertErr := q.CreateOrchestrationWaitOperation(ctx, createOperationParams(request, targetSetDigest, issuedAt, int64(len(derived))))
	if errors.Is(insertErr, sql.ErrNoRows) {
		return s.replayPlanTx(ctx, q, request, derived, result, storedb.OrchestrationWaitOperation{})
	}
	if insertErr != nil {
		return classifyDBError("create wait operation", insertErr, ErrReplayConflict)
	}
	for _, target := range derived {
		if _, err := q.CreateOrchestrationWaitTargetPlan(ctx, target); err != nil {
			return classifyDBError("create wait operation target plan", err, ErrReplayConflict)
		}
	}
	return s.operationWithHeadsTx(ctx, q, row, result)
}

func (s *Store) validatePlanInsert(request PlanRequest, issuedAt time.Time) error {
	if s.fenced.Load() {
		return ErrFenced
	}
	if !request.DeadlineAt.After(issuedAt) || request.DeadlineAt.Sub(issuedAt) > maxPlanLifetime {
		return invalidOperation("deadline_at must be within 60 seconds after issued_at")
	}
	return nil
}

func createOperationParams(request PlanRequest, targetSetDigest string, issuedAt time.Time, targetCount int64) storedb.CreateOrchestrationWaitOperationParams {
	stamp := formatUTC(issuedAt)
	return storedb.CreateOrchestrationWaitOperationParams{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetSetDigest: targetSetDigest,
		CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef, SourceAgentID: request.Source.AgentID,
		SourceReservationID: request.Source.ReservationID, SourceReservationGeneration: request.Source.ReservationGeneration,
		SourceIncarnationID: request.Source.IncarnationID, SourceIncarnationGeneration: request.Source.IncarnationGeneration,
		SourceBindingVersion: request.Source.BindingVersion, SourceBindingDigest: request.Source.BindingDigest, SourceFenceRef: request.Source.SourceFenceRef,
		IssuedAt: stamp, DeadlineAt: formatUTC(request.DeadlineAt), TargetCount: targetCount, CreatedAt: stamp, UpdatedAt: stamp,
	}
}

// Get reads an exact operation and all latest target heads without writing.
// Get 读取精确操作及其全部最新目标头。
func (s *Store) Get(ctx context.Context, lookup OperationLookup) (Operation, error) {
	if err := s.ready(ctx, false); err != nil {
		return Operation{}, err
	}
	if err := validateLookup(lookup); err != nil {
		return Operation{}, err
	}
	if err := s.CheckSchema(ctx); err != nil {
		return Operation{}, err
	}
	row, err := s.queries.GetOrchestrationWaitOperationByDigest(ctx, storedb.GetOrchestrationWaitOperationByDigestParams{OperationKey: lookup.Key, OperationDigest: lookup.Digest})
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, ErrOperationNotFound
	}
	if err != nil {
		return Operation{}, classifyDBError("get wait operation", err, ErrOperationNotFound)
	}
	result, err := operationFromRow(row)
	if err != nil {
		return Operation{}, err
	}
	if err := s.populateHeads(ctx, s.queries, &result); err != nil {
		return Operation{}, err
	}
	return result, nil
}

// List returns all latest target heads for an exact operation lookup.
// List 返回精确操作的全部最新目标状态。
func (s *Store) List(ctx context.Context, lookup OperationLookup) ([]TargetState, error) {
	operation, err := s.Get(ctx, lookup)
	if err != nil {
		return nil, err
	}
	return operation.Targets, nil
}

// AppendTarget appends one CAS-guarded target transition. It never updates a
// journal row in place and never performs a write after the owner is fenced.
// AppendTarget 追加一次 CAS 保护的目标生命周期转换。
func (s *Store) AppendTarget(ctx context.Context, request TargetTransitionRequest) (TargetState, error) {
	if err := s.ready(ctx, false); err != nil {
		return TargetState{}, err
	}
	if err := request.validateShape(); err != nil {
		return TargetState{}, err
	}
	if err := s.CheckSchema(ctx); err != nil {
		return TargetState{}, err
	}
	recordedAt, err := s.nowUTC()
	if err != nil {
		return TargetState{}, err
	}
	var result TargetState
	var descriptor CommitMarkerDescriptor
	if s.commitMarkers == nil {
		err = s.appendTargetWithoutMarker(ctx, request, recordedAt, &result)
	} else {
		descriptor, result, err = s.appendTargetWithMarker(ctx, request, recordedAt)
	}
	if errors.Is(err, ErrCommitMarkerResolved) {
		// The marker proves that this exact request already committed; return
		// only the proven durable successor and never append another row.
		err = s.replayResolvedTargetMarker(ctx, descriptor, &result)
	}
	if errors.Is(err, ErrCommitUnknown) {
		return result, err
	}
	return result, err
}

func (s *Store) appendTargetWithoutMarker(ctx context.Context, request TargetTransitionRequest, recordedAt time.Time, result *TargetState) error {
	return s.withImmediate(ctx, func(q *storedb.Queries) error {
		return s.appendTargetOperation(ctx, q, request, recordedAt, result)
	})
}

// appendTargetWithMarker 以 durable marker 保护目标追加并处理精确重放。
func (s *Store) appendTargetWithMarker(ctx context.Context, request TargetTransitionRequest, recordedAt time.Time) (CommitMarkerDescriptor, TargetState, error) {
	_, replay, err := loadAppendTarget(ctx, s.queries, request)
	if err != nil {
		return CommitMarkerDescriptor{}, TargetState{}, err
	}
	predecessor, err := loadAppendPredecessor(ctx, s.queries, request)
	if err != nil {
		return CommitMarkerDescriptor{}, TargetState{}, err
	}
	descriptor, err := NewMutationDescriptor(MutationTargetAppend, request.OperationKey, request.OperationDigest, request.TargetKey, request, targetStateDigest(predecessor))
	if err != nil {
		return CommitMarkerDescriptor{}, TargetState{}, err
	}
	if replay != nil {
		if err := s.reconcileReplayMarkers(ctx, request.OperationKey, request.OperationDigest, MutationTargetAppend, request.TargetKey, descriptor.RequestDigest, targetStateDigest(*replay)); err != nil {
			return CommitMarkerDescriptor{}, TargetState{}, err
		}
		return descriptor, *replay, nil
	}
	var result TargetState
	err = s.withImmediateMutation(ctx, &descriptor, func(q *storedb.Queries, _ storedb.DBTX) error {
		return s.appendTargetMarkerTx(ctx, q, request, recordedAt, &descriptor, &result)
	})
	return descriptor, result, err
}

// appendTargetMarkerTx 在 marker 事务内校验 predecessor 摘要并追加目标记录。
func (s *Store) appendTargetMarkerTx(ctx context.Context, q *storedb.Queries, request TargetTransitionRequest, recordedAt time.Time, descriptor *CommitMarkerDescriptor, result *TargetState) error {
	current, err := q.GetOrchestrationWaitTargetHead(ctx, storedb.GetOrchestrationWaitTargetHeadParams{OperationKey: request.OperationKey, TargetKey: request.TargetKey})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTargetNotFound
	}
	if err != nil {
		return classifyDBError("read wait operation target commit marker pre-state", err, ErrTargetConflict)
	}
	currentState, err := targetFromRow(current)
	if err != nil {
		return err
	}
	if targetStateDigest(currentState) != descriptor.PreStateDigest {
		return ErrTargetConflict
	}
	if err := s.appendTargetOperation(ctx, q, request, recordedAt, result); err != nil {
		return err
	}
	descriptor.PostStateDigest = targetStateDigest(*result)
	return nil
}

// replayPlanTx 比较已有操作及所有目标候选以确认精确重放。
func (s *Store) replayPlanTx(ctx context.Context, q *storedb.Queries, request PlanRequest, derived []storedb.CreateOrchestrationWaitTargetPlanParams, result *Operation, existing storedb.OrchestrationWaitOperation) error {
	row, err := replayOperationRow(ctx, q, request, existing)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOperationNotFound
	}
	if err != nil {
		return classifyDBError("read replayed wait operation", err, ErrReplayConflict)
	}
	if !replayOperationMatches(row, request, len(derived)) {
		return ErrReplayConflict
	}
	operation, err := operationFromRow(row)
	if err != nil {
		return err
	}
	headRows, err := q.ListOrchestrationWaitTargetHeads(ctx, storedb.ListOrchestrationWaitTargetHeadsParams{OperationKey: request.OperationKey})
	if err != nil {
		return classifyDBError("read replayed wait targets", err, ErrReplayConflict)
	}
	if err := validateReplayTargets(headRows, derived); err != nil {
		return err
	}
	if err := s.populateHeads(ctx, q, &operation); err != nil {
		return err
	}
	*result = operation
	return nil
}

func replayOperationRow(ctx context.Context, q *storedb.Queries, request PlanRequest, existing storedb.OrchestrationWaitOperation) (storedb.OrchestrationWaitOperation, error) {
	if existing.OperationKey != "" {
		return existing, nil
	}
	return q.GetOrchestrationWaitOperation(ctx, storedb.GetOrchestrationWaitOperationParams{OperationKey: request.OperationKey})
}

func validateReplayTargets(rows []storedb.OrchestrationWaitOperationTargetJournal, derived []storedb.CreateOrchestrationWaitTargetPlanParams) error {
	if len(rows) != len(derived) {
		return ErrReplayConflict
	}
	for index, row := range rows {
		if !targetPlanMatchesRow(derived[index], row) {
			return ErrReplayConflict
		}
	}
	return nil
}

// replayOperationMatches 比较操作重放所需的全部不可变字段。
func replayOperationMatches(row storedb.OrchestrationWaitOperation, request PlanRequest, targetCount int) bool {
	return row.OperationDigest == request.OperationDigest && row.TargetSetDigest == request.TargetSetDigest && row.CallerPrincipalRef == request.CallerPrincipalRef && row.ScopeRef == request.ScopeRef && sameSource(row, request.Source) && row.TargetCount == int64(targetCount) && row.DeadlineAt == formatUTC(request.DeadlineAt)
}

func (s *Store) operationWithHeadsTx(ctx context.Context, q *storedb.Queries, row storedb.OrchestrationWaitOperation, result *Operation) error {
	operation, err := operationFromRow(row)
	if err != nil {
		return err
	}
	if err := s.populateHeads(ctx, q, &operation); err != nil {
		return err
	}
	*result = operation
	return nil
}

// populateHeads 读取并验证目标日志链后填充操作头。
func (s *Store) populateHeads(ctx context.Context, q *storedb.Queries, operation *Operation) error {
	rows, err := q.ListOrchestrationWaitTargetJournal(ctx, storedb.ListOrchestrationWaitTargetJournalParams{OperationKey: operation.OperationKey})
	if err != nil {
		return classifyDBError("list wait operation target heads", err, ErrOperationNotFound)
	}
	byKey := make(map[string]*targetHistory, len(rows))
	for _, row := range rows {
		if err := appendTargetHistory(byKey, row, operation); err != nil {
			return err
		}
	}
	if err := validateTargetHeadCount(byKey, operation.TargetCount); err != nil {
		return err
	}
	latest := make([]TargetState, 0, len(byKey))
	for key, history := range byKey {
		if err := validateTargetHistory(key, history); err != nil {
			return err
		}
		latest = append(latest, history.states[len(history.states)-1])
	}
	if err := finalizeTargetHeads(latest, operation.TargetSetDigest); err != nil {
		return err
	}
	operation.Targets = latest
	return nil
}

func finalizeTargetHeads(latest []TargetState, targetSetDigest string) error {
	sort.Slice(latest, func(left, right int) bool { return latest[left].TargetIndex < latest[right].TargetIndex })
	for index, target := range latest {
		if target.TargetIndex != int64(index+1) {
			return errors.Join(ErrInvalidOperation, errors.New("wait operation target indices are not dense"))
		}
	}
	computedSetDigest, err := targetSetDigestFromStates(latest)
	if err != nil {
		return err
	}
	if computedSetDigest != targetSetDigest {
		return errors.Join(ErrInvalidOperation, errors.New("wait operation target_set_digest is not canonical"))
	}
	return nil
}

func validateTargetHeadCount(byKey map[string]*targetHistory, targetCount int64) error {
	if int64(len(byKey)) != targetCount {
		return errors.Join(ErrInvalidOperation, errors.New("wait operation target head count does not match operation target_count"))
	}
	return nil
}

func appendTargetHistory(byKey map[string]*targetHistory, row storedb.OrchestrationWaitOperationTargetJournal, operation *Operation) error {
	if row.OperationDigest != operation.OperationDigest {
		return errors.Join(ErrInvalidOperation, errors.New("target head operation digest mismatch"))
	}
	target, err := targetFromRow(row)
	if err != nil {
		return err
	}
	if !targetSelectorMatchesOperation(target, operation) {
		return errors.Join(ErrInvalidOperation, errors.New("target head selector fence mismatch"))
	}
	history := byKey[row.TargetKey]
	if history == nil {
		history = &targetHistory{}
		byKey[row.TargetKey] = history
	}
	history.rows = append(history.rows, row)
	history.states = append(history.states, target)
	return nil
}

func targetSelectorMatchesOperation(target TargetState, operation *Operation) bool {
	sourceMatch := target.Selector.SourceID == "task/"+operation.Source.AgentID && target.Selector.SourceEpoch == operation.Source.IncarnationID
	targetMatch := target.Selector.SourceID == "task/"+target.Target.AgentID && target.Selector.SourceEpoch == target.Target.IncarnationID
	return (sourceMatch || targetMatch) && target.Selector.TargetAgentID == target.Target.AgentID
}

// validateTargetHistory 校验目标日志版本连续、前驱摘要和转换摘要。
func validateTargetHistory(key string, history *targetHistory) error {
	if len(history.rows) == 0 {
		return errors.Join(ErrInvalidOperation, errors.New("wait operation target history is empty"))
	}
	for index, row := range history.rows {
		if row.Version != int64(index+1) {
			return errors.Join(ErrInvalidOperation, fmt.Errorf("target %s journal versions are not contiguous", key))
		}
		if index == 0 {
			continue
		}
		previous := history.rows[index-1]
		if row.PreviousRecordDigest != previous.RecordDigest {
			return errors.Join(ErrInvalidOperation, fmt.Errorf("target %s predecessor digest mismatch", key))
		}
		if err := validateReadTargetTransition(history.states[index-1], history.states[index]); err != nil {
			return fmt.Errorf("target %s transition is invalid: %w", key, err)
		}
		request := transitionRequestFromRow(row)
		expectedDigest, err := computeTargetRecordDigest(previous, request, row.RecordedAt, row.ResolvedAt)
		if err != nil {
			return err
		}
		if row.RecordDigest != expectedDigest {
			return errors.Join(ErrInvalidOperation, fmt.Errorf("target %s transition record digest is not canonical", key))
		}
	}
	return nil
}

// classifyTargetCAS 在并发追加失败后区分精确重放与真正冲突。
func classifyTargetCAS(ctx context.Context, q *storedb.Queries, request TargetTransitionRequest, result *TargetState) error {
	row, err := q.GetOrchestrationWaitTargetHead(ctx, storedb.GetOrchestrationWaitTargetHeadParams{OperationKey: request.OperationKey, TargetKey: request.TargetKey})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTargetNotFound
	}
	if err != nil {
		return classifyDBError("read wait operation target CAS conflict", err, ErrTargetConflict)
	}
	if row.OperationDigest != request.OperationDigest {
		return ErrReplayConflict
	}
	if row.Version > request.ExpectedVersion && row.RecordDigest == request.RecordDigest {
		matches, historyErr := appendReplayPredecessorMatches(ctx, q, request)
		if historyErr != nil {
			return historyErr
		}
		if !matches {
			return ErrTargetConflict
		}
		mapped, mapErr := targetFromRow(row)
		if mapErr != nil {
			return mapErr
		}
		*result = mapped
		return nil
	}
	return ErrTargetConflict
}

// sameSource 比较操作行中的完整源 fence 快照。
func sameSource(row storedb.OrchestrationWaitOperation, source accesswait.FenceSnapshot) bool {
	return row.SourceAgentID == source.AgentID && row.SourceReservationID == source.ReservationID && row.SourceReservationGeneration == source.ReservationGeneration && row.SourceIncarnationID == source.IncarnationID && row.SourceIncarnationGeneration == source.IncarnationGeneration && row.SourceBindingVersion == source.BindingVersion && row.SourceBindingDigest == source.BindingDigest && row.SourceFenceRef == source.SourceFenceRef
}

func validateLookup(lookup OperationLookup) error {
	if err := validateText("operation_key", lookup.Key, MaxOperationKey, false); err != nil {
		return err
	}
	return validateDigest("operation_digest", lookup.Digest)
}

func (s *Store) nowUTC() (time.Time, error) {
	now := s.clock()
	if now.IsZero() {
		return time.Time{}, invalidOperation("clock returned zero time")
	}
	if now.Location() != time.UTC {
		return time.Time{}, invalidOperation("clock must return UTC")
	}
	return now.UTC(), nil
}

func formatUTC(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func targetPhaseResolved(phase string) bool {
	return phase == "satisfied" || phase == "timed_out" || phase == "cancelled" || phase == "failed" || phase == "cleanup_cleared"
}

// isFreshProvisioningAdvance 判断是否为受 deadline 约束的 provisioning 前进。
func isFreshProvisioningAdvance(previousPhase, nextPhase string) bool {
	if nextPhase == "outcome_unknown" {
		switch previousPhase {
		case "planned", "grant_pending", "wait_pending":
			return true
		}
	}
	switch {
	case previousPhase == "planned" && nextPhase == "grant_pending":
		return true
	case previousPhase == "grant_pending" && nextPhase == "wait_pending":
		return true
	case previousPhase == "wait_pending" && nextPhase == "waiting":
		return true
	default:
		return false
	}
}

// ready 检查 owner 初始化、上下文和写 fence 状态。
func (s *Store) ready(ctx context.Context, write bool) error {
	if s == nil || s.db == nil || s.queries == nil || s.clock == nil {
		return ErrStoreUninitialized
	}
	if ctx == nil {
		return errors.Join(ErrStoreUninitialized, errors.New("wait operation context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if write && s.fenced.Load() {
		return ErrFenced
	}
	return nil
}

func (s *Store) fenceProcess() {
	if s == nil {
		return
	}
	s.processFenced.Store(true)
	s.fenced.Store(true)
}

func classifyDBError(operation string, err error, conflict error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return conflict
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return errors.Join(conflict, err)
		case sqlite3.SQLITE_CONSTRAINT_CHECK, sqlite3.SQLITE_CONSTRAINT_TRIGGER, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return errors.Join(ErrInvalidOperation, err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// withImmediate executes one callback in a SQLite BEGIN IMMEDIATE transaction.
// If COMMIT cannot be proven, it marks the owner query-only and never reruns
// the callback during the bounded busy retry loop.
func (s *Store) withImmediate(ctx context.Context, callback func(*storedb.Queries) error) error {
	if callback == nil {
		return errors.New("wait operation transaction callback is required")
	}
	return s.withImmediateTx(ctx, func(q *storedb.Queries, _ storedb.DBTX) error { return callback(q) })
}

// withImmediateTx 执行一次不可重放的 BEGIN IMMEDIATE 事务并处理 commit-unknown。
func (s *Store) withImmediateTx(ctx context.Context, callback func(*storedb.Queries, storedb.DBTX) error) error {
	if s == nil {
		return ErrStoreUninitialized
	}
	if callback == nil {
		return errors.New("wait operation transaction callback is required")
	}
	s.txMu.Lock()
	defer s.txMu.Unlock()
	var callbackSucceeded, callbackCompleted bool
	callbackReplay := errors.New("wait operation transaction callback replay fenced")
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, txdb storedb.DBTX) error {
		if callbackSucceeded {
			return callbackReplay
		}
		callbackErr := callback(q, txdb)
		callbackCompleted = true
		callbackSucceeded = callbackErr == nil
		return callbackErr
	})
	if err == nil {
		return nil
	}
	if callbackSucceeded || (callbackCompleted && err == callbackReplay) {
		s.fenceProcess()
		return errors.Join(ErrCommitUnknown, ErrFenced, err)
	}
	return err
}

// appendTargetTx appends one target transition in a caller-owned transaction.
// It is intentionally unexported so provisioning cannot bypass the single
// outer transaction or the owner-controlled timestamp/CAS checks.
// appendTargetTx 在调用方事务中追加规范目标转换日志。
func (s *Store) appendTargetTx(ctx context.Context, q *storedb.Queries, request TargetTransitionRequest, recordedAt time.Time) (TargetState, error) {
	current, replay, err := loadAppendTarget(ctx, q, request)
	if err != nil {
		return TargetState{}, err
	}
	if replay != nil {
		return *replay, nil
	}
	if err := validateAppendTarget(current, request, recordedAt); err != nil {
		return TargetState{}, err
	}
	resolvedAt := resolvedTargetTimestamp(request.NextPhase, recordedAt)
	computed, err := computeTargetRecordDigest(current, request, formatUTC(recordedAt), resolvedAt)
	if err != nil {
		return TargetState{}, err
	}
	if request.RecordDigest != "" && request.RecordDigest != computed {
		return TargetState{}, errors.Join(ErrReplayConflict, invalidOperation("record_digest does not match canonical transition"))
	}
	request.RecordDigest = computed
	row, err := q.AppendOrchestrationWaitTargetJournal(ctx, storedb.AppendOrchestrationWaitTargetJournalParams{
		RecordDigest: request.RecordDigest, ResolvedGrantID: cloneString(request.ResolvedGrantID), GrantRevision: request.GrantRevision,
		GrantStatus: request.GrantStatus, ResolvedWaitHandleID: cloneString(request.ResolvedWaitHandleID), WaitRevision: request.WaitRevision,
		WaitStatus: request.WaitStatus, GrantCleanupState: request.GrantCleanupState, WaitCleanupState: request.WaitCleanupState,
		NextPhase: request.NextPhase, UncertainPhase: request.UncertainPhase, ErrorCode: request.ErrorCode, ErrorRef: request.ErrorRef,
		ResolvedAt: resolvedAt, RecordedAt: formatUTC(recordedAt), OperationKey: request.OperationKey, OperationDigest: request.OperationDigest,
		TargetKey: request.TargetKey, ExpectedVersion: request.ExpectedVersion, ExpectedPhase: request.ExpectedPhase,
		ExpectedRecordDigest: request.ExpectedRecordDigest,
	})
	if errors.Is(err, sql.ErrNoRows) {
		var result TargetState
		return result, classifyTargetCAS(ctx, q, request, &result)
	}
	if err != nil {
		return TargetState{}, classifyDBError("append wait operation target", err, ErrTargetConflict)
	}
	result, err := targetFromRow(row)
	return result, err
}

// loadAppendTarget 读取目标头并识别 CAS 精确重放。
func loadAppendTarget(ctx context.Context, q *storedb.Queries, request TargetTransitionRequest) (storedb.OrchestrationWaitOperationTargetJournal, *TargetState, error) {
	row, err := q.GetOrchestrationWaitTargetHead(ctx, storedb.GetOrchestrationWaitTargetHeadParams{OperationKey: request.OperationKey, TargetKey: request.TargetKey})
	if errors.Is(err, sql.ErrNoRows) {
		return row, nil, ErrTargetNotFound
	}
	if err != nil {
		return row, nil, classifyDBError("read wait operation target", err, ErrTargetConflict)
	}
	if row.OperationDigest != request.OperationDigest {
		return row, nil, ErrReplayConflict
	}
	if row.Version > request.ExpectedVersion && request.RecordDigest != "" && row.RecordDigest == request.RecordDigest {
		matches, historyErr := appendReplayPredecessorMatches(ctx, q, request)
		if historyErr != nil {
			return row, nil, historyErr
		}
		if !matches {
			return row, nil, ErrTargetConflict
		}
		mapped, mapErr := targetFromRow(row)
		return row, &mapped, mapErr
	}
	return row, nil, nil
}

// loadAppendPredecessor 读取目标 predecessor，并区分 CAS、重放和历史证明。
func loadAppendPredecessor(ctx context.Context, q *storedb.Queries, request TargetTransitionRequest) (TargetState, error) {
	row, err := q.GetOrchestrationWaitTargetHead(ctx, storedb.GetOrchestrationWaitTargetHeadParams{OperationKey: request.OperationKey, TargetKey: request.TargetKey})
	if errors.Is(err, sql.ErrNoRows) {
		return TargetState{}, ErrTargetNotFound
	}
	if err != nil {
		return TargetState{}, classifyDBError("read append target predecessor", err, ErrTargetConflict)
	}
	if row.OperationDigest != request.OperationDigest {
		return TargetState{}, ErrReplayConflict
	}
	if row.Version == request.ExpectedVersion {
		if row.Phase != request.ExpectedPhase || row.RecordDigest != request.ExpectedRecordDigest {
			return TargetState{}, ErrTargetConflict
		}
		return targetFromRow(row)
	}
	if row.Version < request.ExpectedVersion {
		return TargetState{}, ErrTargetConflict
	}
	return findAppendPredecessorHistory(ctx, q, request)
}

func findAppendPredecessorHistory(ctx context.Context, q *storedb.Queries, request TargetTransitionRequest) (TargetState, error) {
	rows, err := q.ListOrchestrationWaitTargetJournal(ctx, storedb.ListOrchestrationWaitTargetJournalParams{OperationKey: request.OperationKey})
	if err != nil {
		return TargetState{}, classifyDBError("read append target predecessor history", err, ErrTargetConflict)
	}
	for _, historical := range rows {
		if appendPredecessorMatches(historical, request) {
			return targetFromRow(historical)
		}
	}
	return TargetState{}, ErrTargetConflict
}

func appendPredecessorMatches(row storedb.OrchestrationWaitOperationTargetJournal, request TargetTransitionRequest) bool {
	return row.OperationDigest == request.OperationDigest && row.TargetKey == request.TargetKey && row.Version == request.ExpectedVersion && row.Phase == request.ExpectedPhase && row.RecordDigest == request.ExpectedRecordDigest
}

// validateAppendTarget 校验目标转换前置和 provisioning deadline。
func validateAppendTarget(current storedb.OrchestrationWaitOperationTargetJournal, request TargetTransitionRequest, recordedAt time.Time) error {
	if current.Version != request.ExpectedVersion || current.Phase != request.ExpectedPhase || current.RecordDigest != request.ExpectedRecordDigest {
		return ErrTargetConflict
	}
	if !isFreshProvisioningAdvance(current.Phase, request.NextPhase) {
		return nil
	}
	deadline, err := parseUTC("target deadline_at", current.DeadlineAt)
	if err != nil {
		return err
	}
	if !recordedAt.Before(deadline) {
		return ErrDeadlineExpired
	}
	return nil
}

func resolvedTargetTimestamp(nextPhase string, recordedAt time.Time) string {
	if targetPhaseResolved(nextPhase) {
		return formatUTC(recordedAt)
	}
	return ""
}
