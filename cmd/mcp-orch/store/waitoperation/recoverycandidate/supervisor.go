package recoverycandidate

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// ReceiptSchema is intentionally supplied by a candidate migration/fixture;
// Supervisor never executes DDL at runtime.
const ReceiptSchema = `CREATE TABLE orchestration_wait_operation_recovery_receipts (
 receipt_key TEXT PRIMARY KEY NOT NULL CHECK(length(receipt_key)=64 AND receipt_key NOT GLOB '*[^0-9a-f]*'),
 marker_key TEXT UNIQUE NOT NULL CHECK(length(marker_key)=64 AND marker_key NOT GLOB '*[^0-9a-f]*'),
 operation_key TEXT NOT NULL CHECK(length(trim(operation_key)) > 0 AND trim(operation_key)=operation_key),
 operation_digest TEXT NOT NULL CHECK(length(operation_digest)=64 AND operation_digest NOT GLOB '*[^0-9a-f]*'),
 target_key TEXT NOT NULL, mutation_kind TEXT NOT NULL,
 post_state_digest TEXT NOT NULL CHECK(length(post_state_digest)=64 AND post_state_digest NOT GLOB '*[^0-9a-f]*'),
 observation_digest TEXT NOT NULL CHECK(length(observation_digest)=64 AND observation_digest NOT GLOB '*[^0-9a-f]*'),
 recovered_at TEXT NOT NULL CHECK(length(trim(recovered_at)) > 0 AND trim(recovered_at)=recovered_at AND julianday(recovered_at) IS NOT NULL),
 schema_version INTEGER NOT NULL CHECK(schema_version=1)
) STRICT;
CREATE INDEX idx_wait_operation_recovery_receipts_marker ON orchestration_wait_operation_recovery_receipts(marker_key);
CREATE TRIGGER trg_wait_operation_recovery_receipt_immutable BEFORE UPDATE ON orchestration_wait_operation_recovery_receipts BEGIN SELECT RAISE(ABORT, 'recovery receipt is immutable'); END;
CREATE TRIGGER trg_wait_operation_recovery_receipt_no_delete BEFORE DELETE ON orchestration_wait_operation_recovery_receipts BEGIN SELECT RAISE(ABORT, 'recovery receipt cannot be deleted'); END;`

// Supervisor reconciles owner capabilities and writes only to the explicitly
// separate candidate receipt database.
type Supervisor struct {
	receiptDB *sql.DB
	productDB *sql.DB
	reader    MarkerReader
	resolver  MarkerResolver
	verifier  CanonicalPostStateVerifier
	clock     Clock
}

// New 创建仅写候选回执库、读取 owner 能力的恢复监督器。
func New(receiptDB, productDB *sql.DB, reader MarkerReader, resolver MarkerResolver, verifier CanonicalPostStateVerifier, clock Clock) (*Supervisor, error) {
	if receiptDB == nil || productDB == nil || receiptDB == productDB {
		return nil, ErrRecoveryUnavailable
	}
	if isNilCapability(reader) || isNilCapability(resolver) || isNilCapability(verifier) {
		return nil, ErrRecoveryUnavailable
	}
	if err := validateClock(clock); err != nil {
		return nil, err
	}
	return &Supervisor{receiptDB: receiptDB, productDB: productDB, reader: reader, resolver: resolver, verifier: verifier, clock: clock}, nil
}

// EnsureSchema 校验候选回执表由显式迁移或测试夹具安装，运行时禁止执行 DDL。
func (s *Supervisor) EnsureSchema(ctx context.Context) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	if err := s.verifySchemaObjects(ctx); err != nil {
		return err
	}
	return s.verifySchemaColumns(ctx)
}

// ReadMarker 通过 owner 签发的读取能力取得并校验单个提交标记。
func (s *Supervisor) ReadMarker(ctx context.Context, key string) (Marker, error) {
	if err := s.ready(ctx); err != nil {
		return Marker{}, err
	}
	marker, err := s.reader.ReadMarker(ctx, key)
	if err != nil {
		return Marker{}, err
	}
	if err := validateMarker(marker); err != nil {
		return Marker{}, err
	}
	return marker, nil
}

// ListPending 读取 owner 尚未解决的标记并按标记键稳定排序。
func (s *Supervisor) ListPending(ctx context.Context) ([]Marker, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	markers, err := s.reader.ListPendingMarkers(ctx)
	if err != nil {
		return nil, err
	}
	for _, marker := range markers {
		if err := validateMarker(marker); err != nil {
			return nil, err
		}
	}
	sort.Slice(markers, func(i, j int) bool { return markers[i].MarkerKey < markers[j].MarkerKey })
	return markers, nil
}

// RecoverPending 逐个证明并登记 owner 返回的全部待处理标记。
func (s *Supervisor) RecoverPending(ctx context.Context) ([]Observation, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	markers, err := s.ListPending(ctx)
	if err != nil {
		return nil, err
	}
	observations := make([]Observation, 0, len(markers))
	for _, marker := range markers {
		observation, recoverErr := s.RecoverMarker(ctx, marker.MarkerKey)
		if recoverErr != nil {
			return observations, recoverErr
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

// RecoverMarker 证明 owner 读取的后状态，要求 owner 解决标记，再原子选出一个回执赢家。
func (s *Supervisor) RecoverMarker(ctx context.Context, key string) (Observation, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return Observation{}, err
	}
	marker, err := s.readMarkerForRecovery(ctx, key)
	if err != nil {
		return Observation{}, err
	}
	if err := s.verifyPostState(ctx, marker); err != nil {
		return Observation{}, err
	}
	if err := s.resolveMarker(ctx, marker); err != nil {
		return Observation{}, err
	}
	receipt := s.newReceipt(marker)
	inserted, err := s.insertReceipt(ctx, receipt)
	if err != nil {
		return Observation{}, err
	}
	return Observation{Receipt: receipt, Winner: inserted, Authoritative: false}, nil
}

// verifySchemaObjects 校验回执表及其索引、不可变和禁止删除触发器均已存在。
func (s *Supervisor) verifySchemaObjects(ctx context.Context) error {
	var count int
	err := s.receiptDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE (type='table' AND name='orchestration_wait_operation_recovery_receipts') OR (type='index' AND name='idx_wait_operation_recovery_receipts_marker') OR (type='trigger' AND name IN ('trg_wait_operation_recovery_receipt_immutable','trg_wait_operation_recovery_receipt_no_delete'))`).Scan(&count)
	if err != nil || count != 4 {
		return errors.Join(ErrSchemaUnavailable, err)
	}
	return nil
}

// verifySchemaColumns 校验回执表包含契约要求的全部列。
func (s *Supervisor) verifySchemaColumns(ctx context.Context) error {
	rows, err := s.receiptDB.QueryContext(ctx, "PRAGMA table_info(orchestration_wait_operation_recovery_receipts)")
	if err != nil {
		return errors.Join(ErrSchemaUnavailable, err)
	}
	defer rows.Close()
	want := requiredReceiptColumns()
	for rows.Next() {
		if err := consumeReceiptColumn(rows, want); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil || len(want) != 0 {
		return errors.Join(ErrSchemaUnavailable, err)
	}
	return nil
}

// requiredReceiptColumns 返回回执表必须存在的列名集合。
func requiredReceiptColumns() map[string]bool {
	return map[string]bool{"receipt_key": true, "marker_key": true, "operation_key": true, "operation_digest": true, "target_key": true, "mutation_kind": true, "post_state_digest": true, "observation_digest": true, "recovered_at": true, "schema_version": true}
}

// consumeReceiptColumn 读取一行 PRAGMA 元数据并从缺失集合中移除列名。
func consumeReceiptColumn(rows *sql.Rows, want map[string]bool) error {
	var cid int
	var name, typ string
	var notNull, pk int
	var defaultValue any
	if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
		return errors.Join(ErrSchemaUnavailable, err)
	}
	delete(want, name)
	return nil
}

// readMarkerForRecovery 读取并校验恢复流程使用的提交标记。
func (s *Supervisor) readMarkerForRecovery(ctx context.Context, key string) (Marker, error) {
	return s.ReadMarker(ctx, key)
}

// verifyPostState 校验标记存在后状态，并让 owner verifier 提供同一规范摘要。
func (s *Supervisor) verifyPostState(ctx context.Context, marker Marker) error {
	if marker.PostStateDigest == "" {
		return ErrProofRequired
	}
	actual, err := s.verifier.VerifyPostState(ctx, marker)
	if err != nil {
		return errors.Join(ErrRecoveryUnavailable, err)
	}
	if !validDigest(actual) {
		return ErrProofRequired
	}
	if actual != marker.PostStateDigest {
		return ErrProofMismatch
	}
	return nil
}

// resolveMarker 要求 owner 以已证明摘要完成标记 CAS，并校验返回状态。
func (s *Supervisor) resolveMarker(ctx context.Context, marker Marker) error {
	resolved, err := s.resolver.ResolveMarker(ctx, marker.MarkerKey, marker.PostStateDigest)
	if err != nil {
		return errors.Join(ErrRecoveryUnavailable, err)
	}
	if err := validateMarker(resolved); err != nil || resolved.MarkerKey != marker.MarkerKey || resolved.PostStateDigest != marker.PostStateDigest || resolved.State != "resolved" {
		return ErrProofMismatch
	}
	return nil
}

// newReceipt 根据标记和显式时钟构造不可变候选回执。
func (s *Supervisor) newReceipt(marker Marker) RecoveryReceipt {
	receipt := RecoveryReceipt{ReceiptKey: receiptKey(marker), MarkerKey: marker.MarkerKey, OperationKey: marker.OperationKey, OperationDigest: marker.OperationDigest, TargetKey: marker.TargetKey, MutationKind: marker.MutationKind, PostStateDigest: marker.PostStateDigest, RecoveredAt: formatTime(s.clock()), SchemaVersion: schemaVersion}
	receipt.ObservationDigest = observationDigest(marker, receipt.ReceiptKey, receipt.RecoveredAt)
	return receipt
}

func (s *Supervisor) insertReceipt(ctx context.Context, receipt RecoveryReceipt) (bool, error) {
	inserted := false
	err := sqlctx.WithImmediateTx(ctx, s.receiptDB, storedb.New(s.receiptDB), func(_ *storedb.Queries, txdb storedb.DBTX) error {
		_, insertErr := txdb.ExecContext(ctx, `INSERT INTO orchestration_wait_operation_recovery_receipts (receipt_key, marker_key, operation_key, operation_digest, target_key, mutation_kind, post_state_digest, observation_digest, recovered_at, schema_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, receipt.ReceiptKey, receipt.MarkerKey, receipt.OperationKey, receipt.OperationDigest, receipt.TargetKey, receipt.MutationKind, receipt.PostStateDigest, receipt.ObservationDigest, receipt.RecoveredAt, receipt.SchemaVersion)
		if insertErr == nil {
			inserted = true
			return nil
		}
		existing, readErr := loadReceipt(ctx, txdb, receipt.MarkerKey)
		if readErr != nil {
			return errors.Join(ErrReceiptConflict, insertErr)
		}
		if validateReceipt(existing, Marker{MarkerKey: receipt.MarkerKey, OperationKey: receipt.OperationKey, OperationDigest: receipt.OperationDigest, TargetKey: receipt.TargetKey, MutationKind: receipt.MutationKind, PostStateDigest: receipt.PostStateDigest}) != nil {
			return errors.Join(ErrReceiptConflict, insertErr)
		}
		return nil
	})
	return inserted, err
}

type receiptQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadReceipt(ctx context.Context, tx receiptQuerier, key string) (RecoveryReceipt, error) {
	var receipt RecoveryReceipt
	err := tx.QueryRowContext(ctx, `SELECT receipt_key, marker_key, operation_key, operation_digest, target_key, mutation_kind, post_state_digest, observation_digest, recovered_at, schema_version FROM orchestration_wait_operation_recovery_receipts WHERE marker_key = ?`, key).Scan(&receipt.ReceiptKey, &receipt.MarkerKey, &receipt.OperationKey, &receipt.OperationDigest, &receipt.TargetKey, &receipt.MutationKind, &receipt.PostStateDigest, &receipt.ObservationDigest, &receipt.RecoveredAt, &receipt.SchemaVersion)
	return receipt, err
}

// validateReceipt 校验已有回执与同一标记完全一致且时间戳严格合法。
func validateReceipt(receipt RecoveryReceipt, marker Marker) error {
	if !receiptFieldsMatch(receipt, marker) || !receiptObservationMatches(receipt, marker) {
		return ErrReceiptConflict
	}
	if _, err := parseMarkerTime(receipt.RecoveredAt); err != nil {
		return ErrReceiptConflict
	}
	return nil
}

// receiptFieldsMatch 比较回执身份、操作和后状态字段。
func receiptFieldsMatch(receipt RecoveryReceipt, marker Marker) bool {
	return receipt.ReceiptKey == receiptKey(marker) && receipt.MarkerKey == marker.MarkerKey && receipt.OperationKey == marker.OperationKey && receipt.OperationDigest == marker.OperationDigest && receipt.TargetKey == marker.TargetKey && receipt.MutationKind == marker.MutationKind && receipt.PostStateDigest == marker.PostStateDigest && receipt.SchemaVersion == schemaVersion
}

// receiptObservationMatches 比较回执内容摘要，防止同标记写入不同观察结果。
func receiptObservationMatches(receipt RecoveryReceipt, marker Marker) bool {
	return receipt.ObservationDigest == observationDigest(marker, receipt.ReceiptKey, receipt.RecoveredAt)
}

// ready 检查恢复依赖和调用上下文，拒绝所有隐式降级。
func (s *Supervisor) ready(ctx context.Context) error {
	if s == nil || s.receiptDB == nil || s.productDB == nil || s.receiptDB == s.productDB || isNilCapability(s.reader) || isNilCapability(s.resolver) || isNilCapability(s.verifier) {
		return ErrRecoveryUnavailable
	}
	if ctx == nil {
		return errors.New("recovery candidate context is required")
	}
	return ctx.Err()
}
