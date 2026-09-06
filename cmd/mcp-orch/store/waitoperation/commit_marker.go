package waitoperation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

const commitMarkerSchemaVersion int64 = 1

var (
	ErrCommitMarkerPending            = errors.New("wait operation commit marker is pending")
	ErrCommitMarkerResolved           = errors.New("wait operation commit marker is already resolved")
	ErrCommitMarkerConflict           = errors.New("wait operation commit marker conflicts")
	ErrCommitMarkerProofRequired      = errors.New("wait operation commit marker post-state proof is required")
	ErrCommitMarkerProofMismatch      = errors.New("wait operation commit marker post-state proof mismatches")
	ErrCommitMarkerResolutionRequired = errors.New("wait operation commit marker resolution is required")
)

// commitAfterCallbackFaultContextKey scopes the candidate-only commit fault
// injection to one context; no process-global mutable hook is used.
type commitAfterCallbackFaultContextKey struct{}

type commitAfterCallbackFault struct {
	fired atomic.Bool
}

// withCommitAfterCallbackFault arms a one-shot candidate test seam. The
// fault is consumed only after the guarded transaction has committed.
func withCommitAfterCallbackFault(ctx context.Context) context.Context {
	return context.WithValue(ctx, commitAfterCallbackFaultContextKey{}, &commitAfterCallbackFault{})
}

func consumeCommitAfterCallbackFault(ctx context.Context) bool {
	hook, ok := ctx.Value(commitAfterCallbackFaultContextKey{}).(*commitAfterCallbackFault)
	return ok && hook != nil && hook.fired.CompareAndSwap(false, true)
}

// MutationKind identifies the owner mutation guarded by a commit marker.
type MutationKind string

const (
	MutationPlan            MutationKind = "plan"
	MutationTargetAppend    MutationKind = "target_append"
	MutationStateAdvance    MutationKind = "state_advance"
	MutationTargetProvision MutationKind = "target_provision"
	MutationTargetCleanup   MutationKind = "target_cleanup"
	MutationTargetResume    MutationKind = "target_resume"
)

// CommitMarkerDescriptor is the immutable identity of one guarded mutation.
// PostStateDigest is filled by the owner callback before the transaction commit.
type CommitMarkerDescriptor struct {
	MutationKind    MutationKind
	OperationKey    string
	OperationDigest string
	TargetKey       string
	RequestDigest   string
	PreStateDigest  string
	PostStateDigest string
}

// MutationDescriptor is an additive alias for owner-local callers.
type MutationDescriptor = CommitMarkerDescriptor

// CommitMarker is the durable unresolved/ resolved transaction marker.
type CommitMarker struct {
	MarkerKey       string
	OperationKey    string
	OperationDigest string
	TargetKey       string
	MutationKind    MutationKind
	RequestDigest   string
	PreStateDigest  string
	PostStateDigest string
	State           string
	CreatedAt       time.Time
	ResolvedAt      *time.Time
	SchemaVersion   int64
}

// CommitMarkerStore owns marker rows and never owns operation/provider effects.
type CommitMarkerStore struct {
	db    *sql.DB
	clock Clock
}

// NewCommitMarkerStore 构造显式的 marker owner，并拒绝缺少数据库或时钟的实例。
func NewCommitMarkerStore(db *sql.DB, clock Clock) (*CommitMarkerStore, error) {
	if db == nil {
		return nil, errors.New("commit marker store requires database")
	}
	if clock == nil {
		return nil, errors.New("commit marker store requires explicit clock")
	}
	return &CommitMarkerStore{db: db, clock: clock}, nil
}

// Validate 在开启事务前校验 marker 描述子的身份、状态摘要和 mutation kind。
func (d CommitMarkerDescriptor) Validate() error {
	if err := validateDescriptorIdentity(d); err != nil {
		return err
	}
	if err := validateDescriptorPreState(d); err != nil {
		return err
	}
	if err := validateOptionalDigest("post_state_digest", d.PostStateDigest); err != nil {
		return err
	}
	return validateMarkerKind(d.MutationKind, d.TargetKey)
}

func validateDescriptorIdentity(d CommitMarkerDescriptor) error {
	if err := validateText("operation_key", d.OperationKey, MaxOperationKey, false); err != nil {
		return err
	}
	if err := validateDigest("operation_digest", d.OperationDigest); err != nil {
		return err
	}
	return validateDigest("request_digest", d.RequestDigest)
}

// validateDescriptorPreState 强制 plan 与其他 mutation 遵守各自的前置状态摘要约束。
func validateDescriptorPreState(d CommitMarkerDescriptor) error {
	if err := validateOptionalDigest("pre_state_digest", d.PreStateDigest); err != nil {
		return err
	}
	if d.MutationKind != MutationPlan && d.PreStateDigest == "" {
		return invalidOperation("pre_state_digest is required for %s", d.MutationKind)
	}
	if d.MutationKind == MutationPlan && d.PreStateDigest != "" {
		return invalidOperation("plan marker must not carry pre_state_digest")
	}
	return nil
}

func validateMarkerKind(kind MutationKind, targetKey string) error {
	switch kind {
	case MutationPlan, MutationStateAdvance:
		if targetKey != "" {
			return invalidOperation("marker target_key must be empty for %s", kind)
		}
		return nil
	case MutationTargetAppend, MutationTargetProvision, MutationTargetCleanup, MutationTargetResume:
		return validateText("target_key", targetKey, MaxTargetKey, false)
	default:
		return invalidOperation("marker mutation kind is unsupported")
	}
}

// MarkerKey 根据完整 mutation 元组生成稳定身份，供幂等重放匹配使用。
func (d CommitMarkerDescriptor) MarkerKey() string {
	return digest("wait-operation/commit-marker/v1", []byte(string(d.MutationKind)+"\x00"+d.OperationKey+"\x00"+d.OperationDigest+"\x00"+d.TargetKey+"\x00"+d.RequestDigest+"\x00"+d.PreStateDigest))
}

// NewMutationDescriptor 规范化请求摘要，并在 owner 调用前返回已校验的描述子。
func NewMutationDescriptor(kind MutationKind, operationKey, operationDigest, targetKey string, request any, preStateDigest string) (CommitMarkerDescriptor, error) {
	requestDigest, err := mutationRequestDigest(kind, request)
	if err != nil {
		return CommitMarkerDescriptor{}, err
	}
	descriptor := CommitMarkerDescriptor{
		MutationKind: kind, OperationKey: operationKey, OperationDigest: operationDigest,
		TargetKey: targetKey, RequestDigest: requestDigest,
		PreStateDigest: preStateDigest,
	}
	if err := descriptor.Validate(); err != nil {
		return CommitMarkerDescriptor{}, err
	}
	return descriptor, nil
}

func (m *CommitMarkerStore) listMutation(ctx context.Context, operationKey, operationDigest string, kind MutationKind, targetKey, requestDigest string) ([]CommitMarker, error) {
	if err := validateMutationList(m, ctx, operationKey, operationDigest, kind, targetKey, requestDigest); err != nil {
		return nil, err
	}
	rows, err := m.queryMutation(ctx, operationKey, operationDigest, kind, targetKey, requestDigest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectMutationMarkers(rows)
}

// validateMutationList 校验 marker 查询所需的存储、上下文及完整 mutation 元组。
func validateMutationList(m *CommitMarkerStore, ctx context.Context, operationKey, operationDigest string, kind MutationKind, targetKey, requestDigest string) error {
	if m == nil || m.db == nil {
		return ErrStoreUninitialized
	}
	if ctx == nil {
		return errors.Join(ErrStoreUninitialized, errors.New("commit marker context is nil"))
	}
	if err := validateText("operation_key", operationKey, MaxOperationKey, false); err != nil {
		return err
	}
	if err := validateDigest("operation_digest", operationDigest); err != nil {
		return err
	}
	if err := validateMarkerKind(kind, targetKey); err != nil {
		return err
	}
	return validateDigest("request_digest", requestDigest)
}

func (m *CommitMarkerStore) queryMutation(ctx context.Context, operationKey, operationDigest string, kind MutationKind, targetKey, requestDigest string) (*sql.Rows, error) {
	return m.db.QueryContext(ctx, `SELECT marker_key, operation_key, operation_digest, target_key, mutation_kind,
request_digest, pre_state_digest, post_state_digest, state, created_at, resolved_at, schema_version
FROM orchestration_wait_operation_commit_markers
WHERE operation_key = ? AND operation_digest = ? AND mutation_kind = ? AND target_key = ? AND request_digest = ?
ORDER BY marker_key`, operationKey, operationDigest, string(kind), targetKey, requestDigest)
}

func collectMutationMarkers(rows *sql.Rows) ([]CommitMarker, error) {
	var markers []CommitMarker
	for rows.Next() {
		marker, err := scanCommitMarkerRows(rows)
		if err != nil {
			return nil, err
		}
		markers = append(markers, marker)
	}
	return markers, rows.Err()
}

func scanCommitMarkerRows(rows *sql.Rows) (CommitMarker, error) {
	var marker CommitMarker
	var created, resolved string
	if err := rows.Scan(&marker.MarkerKey, &marker.OperationKey, &marker.OperationDigest, &marker.TargetKey, &marker.MutationKind, &marker.RequestDigest, &marker.PreStateDigest, &marker.PostStateDigest, &marker.State, &created, &resolved, &marker.SchemaVersion); err != nil {
		return CommitMarker{}, err
	}
	if err := decodeCommitMarker(&marker, created, resolved); err != nil {
		return CommitMarker{}, err
	}
	return marker, nil
}

// armTx 在业务事务内占用唯一 pending marker，并拒绝冲突或重复未完成 mutation。
func (m *CommitMarkerStore) armTx(ctx context.Context, txdb storedb.DBTX, descriptor CommitMarkerDescriptor, now time.Time) (CommitMarker, error) {
	if err := sqlctx.RequireTransactionDBTX(txdb); err != nil {
		return CommitMarker{}, err
	}
	if err := descriptor.Validate(); err != nil {
		return CommitMarker{}, err
	}
	var pending CommitMarker
	err := scanCommitMarker(txdb.QueryRowContext(ctx, selectPendingCommitMarkerSQL, descriptor.OperationKey, descriptor.OperationDigest), &pending)
	if err == nil {
		return CommitMarker{}, fmt.Errorf("%w: %s", ErrCommitMarkerPending, pending.MarkerKey)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommitMarker{}, err
	}
	var existing CommitMarker
	err = scanCommitMarker(txdb.QueryRowContext(ctx, selectCommitMarkerSQL, descriptor.MarkerKey()), &existing)
	if err == nil {
		if !markerMatchesDescriptor(existing, descriptor) {
			return CommitMarker{}, ErrCommitMarkerConflict
		}
		if existing.State == "pending" {
			return CommitMarker{}, ErrCommitMarkerPending
		}
		return existing, ErrCommitMarkerResolved
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommitMarker{}, err
	}
	if _, err := txdb.ExecContext(ctx, insertCommitMarkerSQL, descriptor.MarkerKey(), descriptor.OperationKey, descriptor.OperationDigest, descriptor.TargetKey, string(descriptor.MutationKind), descriptor.RequestDigest, descriptor.PreStateDigest, formatUTC(now)); err != nil {
		return CommitMarker{}, classifyDBError("insert wait operation commit marker", err, ErrCommitMarkerConflict)
	}
	return markerFromDescriptor(descriptor, now), nil
}

func markerFromDescriptor(descriptor CommitMarkerDescriptor, now time.Time) CommitMarker {
	return CommitMarker{MarkerKey: descriptor.MarkerKey(), OperationKey: descriptor.OperationKey, OperationDigest: descriptor.OperationDigest, TargetKey: descriptor.TargetKey, MutationKind: descriptor.MutationKind, RequestDigest: descriptor.RequestDigest, PreStateDigest: descriptor.PreStateDigest, State: "pending", CreatedAt: now.UTC(), SchemaVersion: commitMarkerSchemaVersion}
}

// markerMatchesDescriptor 比较 marker 的不可变身份，防止重放误认其他 mutation。
func markerMatchesDescriptor(marker CommitMarker, descriptor CommitMarkerDescriptor) bool {
	return marker.MarkerKey == descriptor.MarkerKey() && marker.OperationKey == descriptor.OperationKey && marker.OperationDigest == descriptor.OperationDigest && marker.TargetKey == descriptor.TargetKey && marker.MutationKind == descriptor.MutationKind && marker.RequestDigest == descriptor.RequestDigest && marker.PreStateDigest == descriptor.PreStateDigest
}

func (m *CommitMarkerStore) recordPostTx(ctx context.Context, txdb storedb.DBTX, markerKey, postDigest string) error {
	if err := validateDigest("post_state_digest", postDigest); err != nil {
		return errors.Join(ErrCommitMarkerProofRequired, err)
	}
	result, err := txdb.ExecContext(ctx, setCommitMarkerPostSQL, postDigest, markerKey)
	if err != nil {
		return classifyDBError("record wait operation commit marker post-state", err, ErrCommitMarkerConflict)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrCommitMarkerConflict
	}
	return nil
}

// resolveTx 仅凭已记录的 post-state proof 将 pending marker 原子转为 resolved。
func (m *CommitMarkerStore) resolveTx(ctx context.Context, txdb storedb.DBTX, marker CommitMarker, now time.Time) (CommitMarker, error) {
	if marker.State == "resolved" {
		return marker, nil
	}
	if marker.PostStateDigest == "" {
		return CommitMarker{}, ErrCommitMarkerProofRequired
	}
	result, err := txdb.ExecContext(ctx, resolveCommitMarkerSQL, formatUTC(now), marker.MarkerKey, marker.PostStateDigest)
	if err != nil {
		return CommitMarker{}, classifyDBError("resolve wait operation commit marker", err, ErrCommitMarkerConflict)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return CommitMarker{}, err
	}
	if rows != 1 {
		return CommitMarker{}, ErrCommitMarkerConflict
	}
	resolved := now.UTC()
	marker.State, marker.ResolvedAt = "resolved", &resolved
	return marker, nil
}

func scanCommitMarker(row *sql.Row, marker *CommitMarker) error {
	var created, resolved string
	err := row.Scan(&marker.MarkerKey, &marker.OperationKey, &marker.OperationDigest, &marker.TargetKey, &marker.MutationKind, &marker.RequestDigest, &marker.PreStateDigest, &marker.PostStateDigest, &marker.State, &created, &resolved, &marker.SchemaVersion)
	if err != nil {
		return err
	}
	return decodeCommitMarker(marker, created, resolved)
}

func decodeCommitMarker(marker *CommitMarker, created, resolved string) error {
	if err := validateDecodedMarkerFields(marker); err != nil {
		return err
	}
	if err := decodeMarkerTimes(marker, created, resolved); err != nil {
		return err
	}
	if err := validateMarkerLifecycle(marker); err != nil {
		return err
	}
	if err := validateMarkerPreState(marker); err != nil {
		return err
	}
	return validateMarkerCanonicalKey(marker)
}

// validateDecodedMarkerFields 校验数据库解码出的 marker 身份、摘要和目标字段。
func validateDecodedMarkerFields(marker *CommitMarker) error {
	if err := validateDigest("marker_key", marker.MarkerKey); err != nil {
		return err
	}
	if err := validateText("operation_key", marker.OperationKey, MaxOperationKey, false); err != nil {
		return err
	}
	if err := validateDigest("operation_digest", marker.OperationDigest); err != nil {
		return err
	}
	if err := validateDigest("request_digest", marker.RequestDigest); err != nil {
		return err
	}
	if err := validateOptionalDigest("pre_state_digest", marker.PreStateDigest); err != nil {
		return err
	}
	if err := validateOptionalDigest("post_state_digest", marker.PostStateDigest); err != nil {
		return err
	}
	if err := validateMarkerKind(marker.MutationKind, marker.TargetKey); err != nil {
		return err
	}
	return nil
}

func decodeMarkerTimes(marker *CommitMarker, created, resolved string) error {
	createdAt, err := parseUTC("commit marker created_at", created)
	if err != nil {
		return err
	}
	marker.CreatedAt = createdAt
	if resolved != "" {
		parsed, parseErr := parseUTC("commit marker resolved_at", resolved)
		if parseErr != nil {
			return parseErr
		}
		marker.ResolvedAt = &parsed
	}
	return nil
}

// validateMarkerLifecycle 强制 schema、状态、时间和 post-state proof 的生命周期不变量。
func validateMarkerLifecycle(marker *CommitMarker) error {
	if marker.SchemaVersion != commitMarkerSchemaVersion || marker.State != "pending" && marker.State != "resolved" {
		return ErrInvalidOperation
	}
	if marker.State == "pending" && marker.ResolvedAt != nil {
		return ErrInvalidOperation
	}
	if marker.State == "resolved" && (marker.PostStateDigest == "" || marker.ResolvedAt == nil || marker.ResolvedAt.Before(marker.CreatedAt)) {
		return ErrInvalidOperation
	}
	return nil
}

func validateMarkerPreState(marker *CommitMarker) error {
	if marker.MutationKind != MutationPlan && marker.PreStateDigest == "" {
		return ErrInvalidOperation
	}
	if marker.MutationKind == MutationPlan && marker.PreStateDigest != "" {
		return ErrInvalidOperation
	}
	return nil
}

func validateMarkerCanonicalKey(marker *CommitMarker) error {
	canonicalKey := (CommitMarkerDescriptor{
		MutationKind: marker.MutationKind, OperationKey: marker.OperationKey,
		OperationDigest: marker.OperationDigest, TargetKey: marker.TargetKey,
		RequestDigest: marker.RequestDigest, PreStateDigest: marker.PreStateDigest,
	}).MarkerKey()
	if marker.MarkerKey != canonicalKey {
		return ErrInvalidOperation
	}
	return nil
}

// load 按 marker key 读取并完整解码一个持久化 marker，缺失时返回明确错误。
func (m *CommitMarkerStore) load(ctx context.Context, markerKey string) (CommitMarker, error) {
	if m == nil || m.db == nil {
		return CommitMarker{}, ErrStoreUninitialized
	}
	if ctx == nil {
		return CommitMarker{}, errors.Join(ErrStoreUninitialized, errors.New("commit marker context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return CommitMarker{}, err
	}
	if err := validateDigest("marker_key", markerKey); err != nil {
		return CommitMarker{}, err
	}
	var marker CommitMarker
	if err := scanCommitMarker(m.db.QueryRowContext(ctx, selectCommitMarkerSQL, markerKey), &marker); errors.Is(err, sql.ErrNoRows) {
		return CommitMarker{}, ErrOperationNotFound
	} else if err != nil {
		return CommitMarker{}, err
	}
	return marker, nil
}

// ListPending 返回所有未解决 marker，供进程重启后的 reconciliation 使用。
func (m *CommitMarkerStore) ListPending(ctx context.Context) ([]CommitMarker, error) {
	if m == nil || m.db == nil {
		return nil, ErrStoreUninitialized
	}
	if ctx == nil {
		return nil, errors.Join(ErrStoreUninitialized, errors.New("commit marker context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := m.db.QueryContext(ctx, `SELECT marker_key, operation_key, operation_digest, target_key, mutation_kind,
request_digest, pre_state_digest, post_state_digest, state, created_at, resolved_at, schema_version
FROM orchestration_wait_operation_commit_markers WHERE state = 'pending' ORDER BY operation_key, marker_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var markers []CommitMarker
	for rows.Next() {
		marker, err := scanCommitMarkerRows(rows)
		if err != nil {
			return nil, err
		}
		markers = append(markers, marker)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return markers, nil
}

func (s *Store) markerPostStateDigest(ctx context.Context, q *storedb.Queries, marker CommitMarker) (string, error) {
	switch marker.MutationKind {
	case MutationPlan, MutationStateAdvance:
		return s.operationMarkerPostStateDigest(ctx, q, marker)
	case MutationTargetAppend, MutationTargetProvision, MutationTargetCleanup, MutationTargetResume:
		return s.targetMarkerPostStateDigest(ctx, q, marker)
	default:
		return "", ErrCommitMarkerConflict
	}
}

func (s *Store) operationMarkerPostStateDigest(ctx context.Context, q *storedb.Queries, marker CommitMarker) (string, error) {
	row, err := q.GetOrchestrationWaitOperation(ctx, storedb.GetOrchestrationWaitOperationParams{OperationKey: marker.OperationKey})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrCommitMarkerProofMismatch
		}
		return "", err
	}
	if row.OperationDigest != marker.OperationDigest {
		return "", ErrCommitMarkerProofMismatch
	}
	operation, err := operationFromRow(row)
	if err != nil {
		return "", err
	}
	return operationStateDigest(operation), nil
}

func (s *Store) targetMarkerPostStateDigest(ctx context.Context, q *storedb.Queries, marker CommitMarker) (string, error) {
	row, err := q.GetOrchestrationWaitTargetHead(ctx, storedb.GetOrchestrationWaitTargetHeadParams{OperationKey: marker.OperationKey, TargetKey: marker.TargetKey})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrCommitMarkerProofMismatch
		}
		return "", err
	}
	if row.OperationDigest != marker.OperationDigest {
		return "", ErrCommitMarkerProofMismatch
	}
	target, err := targetFromRow(row)
	if err != nil {
		return "", err
	}
	return targetStateDigest(target), nil
}

// loadResolvedMarker 在幂等返回前重新证明 resolved marker 仍描述当前持久化 post-state。
func (s *Store) loadResolvedMarker(ctx context.Context, descriptor CommitMarkerDescriptor) (CommitMarker, error) {
	if s == nil || s.commitMarkers == nil {
		return CommitMarker{}, ErrCommitMarkerConflict
	}
	marker, err := s.commitMarkers.load(ctx, descriptor.MarkerKey())
	if err != nil {
		return CommitMarker{}, err
	}
	if !markerMatchesDescriptor(marker, descriptor) {
		return CommitMarker{}, ErrCommitMarkerConflict
	}
	if marker.State != "resolved" {
		return CommitMarker{}, ErrCommitMarkerPending
	}
	actual, err := s.markerPostStateDigest(ctx, s.queries, marker)
	if err != nil {
		s.fenceProcess()
		return CommitMarker{}, err
	}
	if actual != marker.PostStateDigest {
		s.fenceProcess()
		return CommitMarker{}, ErrCommitMarkerProofMismatch
	}
	return marker, nil
}

// replayResolvedTargetMarker 返回已证明的 target successor，不重复 owner effect 或 journal row。
func (s *Store) replayResolvedTargetMarker(ctx context.Context, descriptor CommitMarkerDescriptor, result *TargetState) error {
	marker, err := s.loadResolvedMarker(ctx, descriptor)
	if err != nil {
		return err
	}
	row, err := s.queries.GetOrchestrationWaitTargetHead(ctx, storedb.GetOrchestrationWaitTargetHeadParams{OperationKey: marker.OperationKey, TargetKey: marker.TargetKey})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.fenceProcess()
			return ErrCommitMarkerProofMismatch
		}
		return err
	}
	if row.OperationDigest != marker.OperationDigest {
		s.fenceProcess()
		return ErrCommitMarkerProofMismatch
	}
	target, err := targetFromRow(row)
	if err != nil {
		return err
	}
	if targetStateDigest(target) != marker.PostStateDigest {
		s.fenceProcess()
		return ErrCommitMarkerProofMismatch
	}
	if result != nil {
		*result = target
	}
	return nil
}

// clearDurableFence 仅在持久化层确认没有 pending marker 时解除可恢复的
// marker fence；processFenced 是进程级粘滞闸门，必须重建 owner（显式重启）后才会清零。
func (s *Store) clearDurableFence(ctx context.Context) {
	if s == nil || s.commitMarkers == nil || s.commitMarkers.db == nil || s.processFenced.Load() {
		return
	}
	var count int64
	if err := s.commitMarkers.db.QueryRowContext(ctx, countPendingCommitMarkersSQL).Scan(&count); err != nil {
		s.fenceProcess()
		return
	}
	if count == 0 {
		s.fenced.Store(false)
	}
}

// reconcileReplayMarkers 重新计算持久化 post-state 后，仅协调完全匹配的 replay marker。
func (s *Store) reconcileReplayMarkers(ctx context.Context, operationKey, operationDigest string, kind MutationKind, targetKey, requestDigest, postDigest string) error {
	if s == nil || s.commitMarkers == nil {
		return nil
	}
	if err := validateDigest("post_state_digest", postDigest); err != nil {
		return errors.Join(ErrCommitMarkerProofRequired, err)
	}
	markers, err := s.commitMarkers.listMutation(ctx, operationKey, operationDigest, kind, targetKey, requestDigest)
	if err != nil {
		s.fenceProcess()
		return err
	}
	for _, marker := range markers {
		if err := s.reconcileReplayMarker(ctx, marker, postDigest); err != nil {
			s.fenceProcess()
			return err
		}
	}
	return nil
}

// reconcileReplayMarker 校验当前 post-state，并把仍 pending 的 replay marker 标记为 resolved。
func (s *Store) reconcileReplayMarker(ctx context.Context, marker CommitMarker, postDigest string) error {
	if marker.PostStateDigest == "" || marker.PostStateDigest != postDigest {
		return ErrCommitMarkerProofMismatch
	}
	actual, err := s.markerPostStateDigest(ctx, s.queries, marker)
	if err != nil {
		return err
	}
	if actual != marker.PostStateDigest {
		return ErrCommitMarkerProofMismatch
	}
	if marker.State != "pending" {
		return nil
	}
	_, err = s.ResolveCommitMarker(ctx, marker.MarkerKey)
	return err
}

// ResolveCommitMarker 在事务中证明当前持久化 post-state 后解决一个 marker。
func (s *Store) ResolveCommitMarker(ctx context.Context, markerKey string) (CommitMarker, error) {
	if err := s.prepareCommitMarkerResolution(ctx); err != nil {
		return CommitMarker{}, err
	}
	var result CommitMarker
	err := s.withImmediateTx(ctx, func(q *storedb.Queries, txdb storedb.DBTX) error {
		return s.resolveCommitMarkerTx(ctx, q, txdb, markerKey, &result)
	})
	if err != nil {
		s.fenceProcess()
		return result, err
	}
	s.refreshCommitFence(ctx)
	s.clearDurableFence(ctx)
	return result, nil
}

func (s *Store) prepareCommitMarkerResolution(ctx context.Context) error {
	if err := s.ready(ctx, false); err != nil {
		return err
	}
	if s.commitMarkers == nil {
		s.fenceProcess()
		return ErrCommitMarkerConflict
	}
	if err := s.commitMarkers.CheckSchema(ctx); err != nil {
		s.fenceProcess()
		return err
	}
	return nil
}

// resolveCommitMarkerTx 在同一事务内幂等校验 resolved marker，或证明 pending marker 后解决它。
func (s *Store) resolveCommitMarkerTx(ctx context.Context, q *storedb.Queries, txdb storedb.DBTX, markerKey string, result *CommitMarker) error {
	marker, err := s.commitMarkers.loadTx(ctx, txdb, markerKey)
	if err != nil {
		return err
	}
	if marker.State == "resolved" {
		actual, err := s.markerPostStateDigest(ctx, q, marker)
		if err != nil {
			return err
		}
		if actual != marker.PostStateDigest {
			return ErrCommitMarkerProofMismatch
		}
		*result = marker
		return nil
	}
	if marker.PostStateDigest == "" {
		return ErrCommitMarkerProofRequired
	}
	actual, err := s.markerPostStateDigest(ctx, q, marker)
	if err != nil {
		return err
	}
	if actual != marker.PostStateDigest {
		return ErrCommitMarkerProofMismatch
	}
	now, err := s.nowUTC()
	if err != nil {
		return err
	}
	*result, err = s.commitMarkers.resolveTx(ctx, txdb, marker, now)
	return err
}

func (m *CommitMarkerStore) loadTx(ctx context.Context, txdb storedb.DBTX, markerKey string) (CommitMarker, error) {
	if err := sqlctx.RequireTransactionDBTX(txdb); err != nil {
		return CommitMarker{}, err
	}
	if err := validateDigest("marker_key", markerKey); err != nil {
		return CommitMarker{}, err
	}
	var marker CommitMarker
	err := scanCommitMarker(txdb.QueryRowContext(ctx, selectCommitMarkerSQL, markerKey), &marker)
	if errors.Is(err, sql.ErrNoRows) {
		return CommitMarker{}, ErrOperationNotFound
	}
	return marker, err
}

// refreshCommitFence 根据数据库中的 pending marker 数量刷新进程 fence 状态。
func (s *Store) refreshCommitFence(ctx context.Context) {
	if s == nil || s.commitMarkers == nil || s.commitMarkers.db == nil {
		return
	}
	var count int64
	if err := s.commitMarkers.db.QueryRowContext(ctx, countPendingCommitMarkersSQL).Scan(&count); err != nil {
		s.fenceProcess()
		return
	}
	if count > 0 {
		s.fenced.Store(true)
	}
}

// refreshCommitFenceAtConstruction 构造 Store 时检查遗留 pending marker 并恢复 fence。
func (s *Store) refreshCommitFenceAtConstruction(ctx context.Context) {
	if s == nil || s.commitMarkers == nil || s.commitMarkers.db == nil || ctx == nil {
		return
	}
	var count int64
	if err := s.commitMarkers.db.QueryRowContext(ctx, countPendingCommitMarkersSQL).Scan(&count); err == nil && count > 0 {
		s.fenced.Store(true)
	}
}

// withImmediateMutation 用 durable marker 包裹业务事务，并要求提交后 proof 完成解决。
func (s *Store) withImmediateMutation(ctx context.Context, descriptor *CommitMarkerDescriptor, callback func(*storedb.Queries, storedb.DBTX) error) error {
	if descriptor == nil {
		return ErrCommitMarkerConflict
	}
	if err := descriptor.Validate(); err != nil {
		return err
	}
	if s.commitMarkers == nil {
		return s.withImmediateTx(ctx, callback)
	}
	if err := s.commitMarkers.CheckSchema(ctx); err != nil {
		return err
	}
	now, err := s.nowUTC()
	if err != nil {
		return err
	}
	err = s.withImmediateTx(ctx, func(q *storedb.Queries, txdb storedb.DBTX) error {
		return s.runMarkedMutationTx(ctx, q, txdb, descriptor, now, callback)
	})
	if err != nil {
		if errors.Is(err, ErrCommitMarkerPending) {
			s.fenced.Store(true)
		}
		return err
	}
	if consumeCommitAfterCallbackFault(ctx) {
		// The transaction has committed, including its post-state proof, but
		// the caller observes an unknown outcome before marker resolution.
		s.fenced.Store(true)
		return errors.Join(ErrCommitUnknown, ErrFenced)
	}
	if err := s.resolveMarkedMutation(ctx, descriptor.MarkerKey()); err != nil {
		s.fenceProcess()
		return errors.Join(ErrCommitMarkerResolutionRequired, ErrFenced, err)
	}
	return nil
}

func (s *Store) runMarkedMutationTx(ctx context.Context, q *storedb.Queries, txdb storedb.DBTX, descriptor *CommitMarkerDescriptor, now time.Time, callback func(*storedb.Queries, storedb.DBTX) error) error {
	marker, err := s.commitMarkers.armTx(ctx, txdb, *descriptor, now)
	if err != nil {
		return err
	}
	if err := callback(q, txdb); err != nil {
		return err
	}
	if descriptor.PostStateDigest == "" {
		return ErrCommitMarkerProofRequired
	}
	return s.commitMarkers.recordPostTx(ctx, txdb, marker.MarkerKey, descriptor.PostStateDigest)
}

func (s *Store) resolveMarkedMutation(ctx context.Context, markerKey string) error {
	if _, err := s.ResolveCommitMarker(ctx, markerKey); err != nil {
		return err
	}
	return nil
}
