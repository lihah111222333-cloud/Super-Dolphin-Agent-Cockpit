package orchestrationcommand

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

var (
	ErrDurableSubmissionConflict   = errors.New("durable command submission identity conflict")
	ErrDurableSubmissionNotFound   = errors.New("durable command submission not found")
	ErrDurableSubmissionUnresolved = errors.New("durable command submission is unresolved")
	ErrDurableSubmissionTerminal   = errors.New("durable command submission is terminal")
	ErrDurableSubmissionAuthority  = errors.New("durable command submission authority rejected")
	ErrDurableSubmissionSchema     = errors.New("durable command submission schema is unavailable")
)

// AcceptResolution 是 submission fence 的三态 accept-resolution。
type AcceptResolution string

const (
	AcceptPending  AcceptResolution = "pending"
	AcceptAccepted AcceptResolution = "accepted"
	AcceptAborted  AcceptResolution = "aborted"
)

// ResolutionKind 是 wait/reconcile 只读投影的终态分类；aborted 对外表现为 rejected。
type ResolutionKind string

const (
	ResolutionPending  ResolutionKind = "pending"
	ResolutionAccepted ResolutionKind = "accepted"
	ResolutionRejected ResolutionKind = "rejected"
)

// CommandFence 是 accepted/rejected resolution 的权威、不可授权投影。
// 它只携带可逐字段回验的引用，不代表 execution terminal。
type CommandFence struct {
	Ref             string
	Digest          string
	Cursor          string
	CommandID       string
	CommandVersion  int64
	SubjectSelector string
}

// DurableSubmissionRequest 标识不可授权的 accept-resolution submission fence。
type DurableSubmissionRequest struct {
	CommandID            string
	CommandVersion       int64
	SubmissionID         string
	SubjectSelector      string
	CallerPrincipalRef   string
	ScopeRef             string
	SourceFenceRef       string
	TargetFenceRef       string
	IdempotencyNamespace string
	IdempotencyKeyDigest string
	RequestDigest        string
	PendingCursor        string
	PendingCursorDigest  string
}

// DurableSubmission 保存 submission 的不可变身份和 accept-resolution。
type DurableSubmission struct {
	Request                                                                                                                 DurableSubmissionRequest
	Resolution                                                                                                              AcceptResolution
	ResolutionKind                                                                                                          ResolutionKind
	AcceptedCommandFence, RejectedCommandFence                                                                              CommandFence
	AcceptReceiptRef, AcceptReceiptDigest, RejectReceiptRef, RejectReceiptDigest, RecoveryReceiptRef, RecoveryReceiptDigest string
	Revision                                                                                                                int64
	RecordedAt, UpdatedAt                                                                                                   string
}

// SubmissionResolution 携带 authority verifier 已验证的终结回执。
type SubmissionResolution struct {
	Request                                                                                                                 DurableSubmissionRequest
	Resolution                                                                                                              AcceptResolution
	AcceptedCommandFence, RejectedCommandFence                                                                              CommandFence
	AcceptReceiptRef, AcceptReceiptDigest, RejectReceiptRef, RejectReceiptDigest, RecoveryReceiptRef, RecoveryReceiptDigest string
	ExpectedRevision                                                                                                        int64
}

// SubmissionAuthorityVerifier 在同一 transaction-bound DBTX 内验证 accepted/aborted 回执，不执行 provider。
type SubmissionAuthorityVerifier interface {
	Verify(context.Context, storedb.DBTX, DurableSubmission, SubmissionResolution) error
}

// AcceptanceReceiptVerifier 是 SubmissionAuthorityVerifier 的兼容别名。
type AcceptanceReceiptVerifier = SubmissionAuthorityVerifier

// DurableSubmissionStore 持有候选 durable submission 的数据库访问和权威校验器。
type DurableSubmissionStore struct {
	db       *sql.DB
	q        *storedb.Queries
	verifier SubmissionAuthorityVerifier
}

// NewDurableSubmissionStore 创建隔离的 candidate submission owner。
func NewDurableSubmissionStore(db *sql.DB, verifier SubmissionAuthorityVerifier) (*DurableSubmissionStore, error) {
	if db == nil {
		return nil, errors.New("durable command submission database is required")
	}
	if verifier == nil || isNilSubmissionVerifier(verifier) {
		return nil, errors.New("durable command submission authority verifier is required")
	}
	return &DurableSubmissionStore{db: db, q: storedb.New(db), verifier: verifier}, nil
}

// NewCommandSubmissionDurableStore 是按领域命名的构造别名。
func NewCommandSubmissionDurableStore(db *sql.DB, v SubmissionAuthorityVerifier) (*DurableSubmissionStore, error) {
	return NewDurableSubmissionStore(db, v)
}
func isNilSubmissionVerifier(v SubmissionAuthorityVerifier) bool {
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return r.IsNil()
	}
	return false
}

// Prepare 通过 owner 事务创建或精确重放 pending submission。
func (s *DurableSubmissionStore) Prepare(c context.Context, r DurableSubmissionRequest) (DurableSubmission, error) {
	if s == nil {
		return DurableSubmission{}, ErrDurableSubmissionNotFound
	}
	return s.PrepareTx(c, s.db, r)
}

// PrepareTx 在 transaction-bound DBTX 中幂等创建 pending submission。
func (s *DurableSubmissionStore) PrepareTx(c context.Context, dbtx storedb.DBTX, r DurableSubmissionRequest) (DurableSubmission, error) {
	if s == nil || s.q == nil {
		return DurableSubmission{}, ErrDurableSubmissionNotFound
	}
	if c == nil {
		return DurableSubmission{}, ErrContextRequired
	}
	if err := validateSubmissionRequestDurable(r); err != nil {
		return DurableSubmission{}, err
	}
	if err := s.checkSchemaForDBTX(c, dbtx); err != nil {
		return DurableSubmission{}, err
	}
	if r.SubmissionID == "" {
		r.SubmissionID = deriveSubmissionIDDurable(r)
	}
	var out DurableSubmission
	err := sqlctx.WithImmediateTxOrReuse(c, dbtx, s.q, func(q *storedb.Queries, _ storedb.DBTX) error {
		var e error
		out, e = prepareDurableInTx(c, q, r)
		return e
	})
	return out, err
}

// prepareDurableInTx 在调用方事务内执行插入或原始幂等键重放。
func prepareDurableInTx(c context.Context, q *storedb.Queries, r DurableSubmissionRequest) (DurableSubmission, error) {
	bound, err := bindDurableIntent(c, q, r)
	if err != nil {
		return DurableSubmission{}, err
	}
	r = bound
	if err := validateSubmissionRequestDurable(r); err != nil {
		return DurableSubmission{}, err
	}
	row, err := q.CreateOrchestrationCommandSubmission(c, storedb.CreateOrchestrationCommandSubmissionParams{CommandID: r.CommandID, SubmissionID: r.SubmissionID, SubjectSelector: r.SubjectSelector, CallerPrincipalRef: r.CallerPrincipalRef, ScopeRef: r.ScopeRef, SourceFenceRef: r.SourceFenceRef, TargetFenceRef: r.TargetFenceRef, IdempotencyNamespace: r.IdempotencyNamespace, IdempotencyKeyDigest: r.IdempotencyKeyDigest, RequestDigest: r.RequestDigest, PendingCursor: r.PendingCursor, PendingCursorDigest: r.PendingCursorDigest})
	if err == nil {
		return mapDurableSubmission(row)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DurableSubmission{}, classifyDurableWrite(err)
	}
	row, err = q.GetOrchestrationCommandSubmissionByKey(c, storedb.GetOrchestrationCommandSubmissionByKeyParams{IdempotencyNamespace: r.IdempotencyNamespace, IdempotencyKeyDigest: r.IdempotencyKeyDigest})
	if err != nil {
		return DurableSubmission{}, classifyDurableRead(err)
	}
	got, err := mapDurableSubmission(row)
	if err != nil {
		return DurableSubmission{}, err
	}
	if !sameSubmissionRequest(got.Request, r) {
		return DurableSubmission{}, ErrDurableSubmissionConflict
	}
	return got, nil
}

// verifyDurableIntentBinding 在候选事务内重读 command intent，防止关闭 FK 后出现孤儿 submission。
func bindDurableIntent(c context.Context, q *storedb.Queries, r DurableSubmissionRequest) (DurableSubmissionRequest, error) {
	intent, err := q.GetOrchestrationCommandIntent(c, storedb.GetOrchestrationCommandIntentParams{CommandID: r.CommandID})
	if errors.Is(err, sql.ErrNoRows) {
		return DurableSubmissionRequest{}, ErrDurableSubmissionNotFound
	}
	if err != nil {
		return DurableSubmissionRequest{}, err
	}
	if intent.CommandVersion != r.CommandVersion || intent.IdempotencyNamespace != r.IdempotencyNamespace || intent.IdempotencyKeyDigest != r.IdempotencyKeyDigest || intent.RequestDigest != r.RequestDigest {
		return DurableSubmissionRequest{}, ErrDurableSubmissionConflict
	}
	for _, pair := range []struct{ got, want string }{
		{r.CallerPrincipalRef, intent.CallerPrincipalRef}, {r.ScopeRef, intent.ScopeRef},
		{r.SourceFenceRef, intent.SourceFenceRef}, {r.TargetFenceRef, intent.TargetFenceRef},
	} {
		if pair.got != pair.want {
			return DurableSubmissionRequest{}, ErrDurableSubmissionConflict
		}
	}
	return r, nil
}

// verifyDurableIntentBinding 保留旧候选调用形状，同时强制读取同一 tx 的完整 intent。
func verifyDurableIntentBinding(c context.Context, q *storedb.Queries, r DurableSubmissionRequest) error {
	_, err := bindDurableIntent(c, q, r)
	return err
}

// Get 按 command/submission identity 只读 durable submission。
func (s *DurableSubmissionStore) Get(c context.Context, k DurableSubmissionKey) (DurableSubmission, error) {
	if s == nil || s.q == nil || c == nil {
		return DurableSubmission{}, ErrDurableSubmissionNotFound
	}
	if err := validateDurableKey(k); err != nil {
		return DurableSubmission{}, err
	}
	if err := s.CheckSchema(c); err != nil {
		return DurableSubmission{}, err
	}
	tx, err := s.db.BeginTx(c, nil)
	if err != nil {
		return DurableSubmission{}, err
	}
	defer tx.Rollback()
	got, err := s.GetTx(c, tx, k)
	if err != nil {
		return DurableSubmission{}, err
	}
	if err := tx.Commit(); err != nil {
		return DurableSubmission{}, err
	}
	return got, nil
}

// GetTx 在调用方事务内读取 submission；禁止拆成独立连接以免 reconcile 观察到不一致 intent。
func (s *DurableSubmissionStore) GetTx(c context.Context, dbtx storedb.DBTX, k DurableSubmissionKey) (DurableSubmission, error) {
	if err := validateSubmissionTxRead(s, c, dbtx); err != nil {
		return DurableSubmission{}, err
	}
	if err := validateDurableKey(k); err != nil {
		return DurableSubmission{}, err
	}
	row, err := storedb.New(dbtx).GetOrchestrationCommandSubmissionByID(c, storedb.GetOrchestrationCommandSubmissionByIDParams{CommandID: k.CommandID, SubmissionID: k.SubmissionID})
	if err != nil {
		return DurableSubmission{}, classifyDurableRead(err)
	}
	got, err := mapDurableSubmission(row)
	if err != nil {
		return DurableSubmission{}, err
	}
	if err := matchDurableOptionalKeyFields(got, k); err != nil {
		return DurableSubmission{}, ErrDurableSubmissionConflict
	}
	intent, err := s.GetIntentTx(c, dbtx, k.CommandID)
	if err != nil {
		return DurableSubmission{}, err
	}
	if !submissionIntentFieldsMatch(got.Request, intent.Intent) {
		return DurableSubmission{}, ErrDurableSubmissionConflict
	}
	return got, nil
}

// submissionIntentFieldsMatch 比较 submission 与 intent 的全部授权边界字段。
func submissionIntentFieldsMatch(request DurableSubmissionRequest, intent Intent) bool {
	return request.CommandID == intent.CommandID && request.CommandVersion == 1 && request.CallerPrincipalRef == intent.CallerPrincipalRef && request.ScopeRef == intent.ScopeRef && request.SourceFenceRef == intent.SourceFenceRef && request.TargetFenceRef == intent.TargetFenceRef && request.IdempotencyNamespace == intent.IdempotencyNamespace && request.IdempotencyKeyDigest == intent.IdempotencyKeyDigest && request.RequestDigest == intent.RequestDigest
}

// GetIntentTx 在同一 transaction-bound DBTX 重读原始 intent，供 wait/reconcile 做逐字段绑定。
func (s *DurableSubmissionStore) GetIntentTx(c context.Context, dbtx storedb.DBTX, commandID string) (Record, error) {
	if err := validateSubmissionTxRead(s, c, dbtx); err != nil {
		return Record{}, err
	}
	row, err := storedb.New(dbtx).GetOrchestrationCommandIntent(c, storedb.GetOrchestrationCommandIntentParams{CommandID: commandID})
	if err != nil {
		return Record{}, classifyReadError(err)
	}
	return recordFromRow(row)
}

func validateSubmissionTxRead(s *DurableSubmissionStore, c context.Context, dbtx storedb.DBTX) error {
	if s == nil || dbtx == nil || c == nil {
		return ErrDurableSubmissionNotFound
	}
	return sqlctx.RequireTransactionDBTX(dbtx)
}

// GetByKey 按 namespace/key digest 只读原 submission。
func (s *DurableSubmissionStore) GetByKey(c context.Context, k DurableSubmissionKey) (DurableSubmission, error) {
	if s == nil || s.q == nil || c == nil {
		return DurableSubmission{}, ErrDurableSubmissionNotFound
	}
	if err := validateDurableKeyNamespace(k); err != nil {
		return DurableSubmission{}, ErrDurableSubmissionConflict
	}
	if err := s.CheckSchema(c); err != nil {
		return DurableSubmission{}, err
	}
	tx, err := s.db.BeginTx(c, nil)
	if err != nil {
		return DurableSubmission{}, err
	}
	defer tx.Rollback()
	got, err := s.getByKeyTx(c, tx, k)
	if err != nil {
		return DurableSubmission{}, err
	}
	if err := tx.Commit(); err != nil {
		return DurableSubmission{}, err
	}
	return got, nil
}

// getByKeyTx 在同一事务内按幂等键读取 submission 并回验 intent 边界。
func (s *DurableSubmissionStore) getByKeyTx(c context.Context, tx *sql.Tx, k DurableSubmissionKey) (DurableSubmission, error) {
	row, err := storedb.New(tx).GetOrchestrationCommandSubmissionByKey(c, storedb.GetOrchestrationCommandSubmissionByKeyParams{IdempotencyNamespace: k.IdempotencyNamespace, IdempotencyKeyDigest: k.IdempotencyKeyDigest})
	if err != nil {
		return DurableSubmission{}, classifyDurableRead(err)
	}
	got, err := mapDurableSubmission(row)
	if err != nil {
		return DurableSubmission{}, err
	}
	if err := matchDurableOptionalKeyFields(got, k); err != nil {
		return DurableSubmission{}, ErrDurableSubmissionConflict
	}
	intent, err := s.GetIntentTx(c, tx, got.Request.CommandID)
	if err != nil {
		return DurableSubmission{}, err
	}
	if !submissionIntentFieldsMatch(got.Request, intent.Intent) {
		return DurableSubmission{}, ErrDurableSubmissionConflict
	}
	return got, nil
}

// DurableSubmissionKey 描述 durable submission 的只读查询身份。
type DurableSubmissionKey struct{ CommandID, SubmissionID, IdempotencyNamespace, IdempotencyKeyDigest string }

// Resolve 以 BEGIN IMMEDIATE CAS 终结 pending submission。
func (s *DurableSubmissionStore) Resolve(c context.Context, r SubmissionResolution) (DurableSubmission, error) {
	if s == nil {
		return DurableSubmission{}, ErrDurableSubmissionNotFound
	}
	return s.ResolveTx(c, s.db, r)
}

// ResolveTx 在调用方 transaction-bound DBTX 内执行 resolution CAS。
func (s *DurableSubmissionStore) ResolveTx(c context.Context, dbtx storedb.DBTX, r SubmissionResolution) (DurableSubmission, error) {
	if s == nil || s.q == nil || s.verifier == nil {
		return DurableSubmission{}, ErrDurableSubmissionAuthority
	}
	if err := validateResolveCall(c, s, dbtx, r); err != nil {
		return DurableSubmission{}, err
	}
	if r.Request.SubmissionID == "" {
		r.Request.SubmissionID = deriveSubmissionIDDurable(r.Request)
	}
	var out DurableSubmission
	err := sqlctx.WithImmediateTxOrReuse(c, dbtx, s.q, func(q *storedb.Queries, txdb storedb.DBTX) error {
		var e error
		out, e = resolveDurableInTx(c, q, txdb, s.verifier, r)
		return e
	})
	return out, err
}

// validateResolveCall 校验 resolution 的上下文、schema、版本和请求身份。
func validateResolveCall(c context.Context, s *DurableSubmissionStore, dbtx storedb.DBTX, r SubmissionResolution) error {
	if c == nil {
		return ErrContextRequired
	}
	if r.Resolution != AcceptAccepted && r.Resolution != AcceptAborted || r.ExpectedRevision <= 0 {
		return ErrDurableSubmissionConflict
	}
	if err := validateSubmissionRequestDurable(r.Request); err != nil {
		return err
	}
	if err := validateResolutionFence(r); err != nil {
		return err
	}
	return s.checkSchemaForDBTX(c, dbtx)
}

// validateResolutionFence 强制 accepted/rejected 只能携带对应权威 command fence。
func validateResolutionFence(r SubmissionResolution) error {
	if r.Resolution == AcceptAccepted {
		if err := r.AcceptedCommandFence.validate(r.Request); err != nil {
			return err
		}
		if r.RejectedCommandFence != (CommandFence{}) {
			return ErrDurableSubmissionConflict
		}
		return nil
	}
	if err := r.RejectedCommandFence.validate(r.Request); err != nil {
		return err
	}
	if r.AcceptedCommandFence != (CommandFence{}) {
		return ErrDurableSubmissionConflict
	}
	return nil
}

// validate 校验 command fence 与 submission identity 逐字段相等且摘要规范。
func (f CommandFence) validate(r DurableSubmissionRequest) error {
	if f.CommandID != r.CommandID || f.CommandVersion != r.CommandVersion || f.SubjectSelector != r.SubjectSelector {
		return ErrDurableSubmissionConflict
	}
	if validateCommandFenceText(f.Ref, 512) != nil || validateCommandFenceText(f.Cursor, 256) != nil || !durableDigest(f.Digest) {
		return ErrDurableSubmissionConflict
	}
	return nil
}

// validateCommandFenceText 校验 command fence 引用的规范文本边界。
func validateCommandFenceText(value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
		return ErrDurableSubmissionConflict
	}
	return nil
}

// resolveDurableInTx 在单一事务内验证 authority 并执行状态 CAS。
// resolveDurableInTx 在单一事务内读取、验证并提交 resolution。
func resolveDurableInTx(c context.Context, q *storedb.Queries, txdb storedb.DBTX, verifier SubmissionAuthorityVerifier, r SubmissionResolution) (DurableSubmission, error) {
	row, err := q.GetOrchestrationCommandSubmissionByID(c, storedb.GetOrchestrationCommandSubmissionByIDParams{CommandID: r.Request.CommandID, SubmissionID: r.Request.SubmissionID})
	if err != nil {
		return DurableSubmission{}, classifyDurableRead(err)
	}
	cur, err := mapDurableSubmission(row)
	if err != nil {
		return DurableSubmission{}, err
	}
	if !sameSubmissionRequest(cur.Request, r.Request) {
		return DurableSubmission{}, ErrDurableSubmissionConflict
	}
	if bound, bindErr := bindDurableIntent(c, q, cur.Request); bindErr != nil || !sameSubmissionRequest(bound, cur.Request) {
		return DurableSubmission{}, ErrDurableSubmissionConflict
	}
	if cur.Resolution != AcceptPending {
		return replayDurableResolution(cur, r)
	}
	if err := verifier.Verify(c, txdb, cur, r); err != nil {
		return DurableSubmission{}, errors.Join(ErrDurableSubmissionAuthority, err)
	}
	row, err = q.ResolveOrchestrationCommandSubmission(c, storedb.ResolveOrchestrationCommandSubmissionParams{CommandID: r.Request.CommandID, SubmissionID: r.Request.SubmissionID, CommandVersion: r.Request.CommandVersion, PendingCursor: r.Request.PendingCursor, PendingCursorDigest: r.Request.PendingCursorDigest, ExpectedRevision: r.ExpectedRevision, AcceptResolution: string(r.Resolution), ResolutionKind: string(resolutionKindFor(r.Resolution)), AcceptReceiptRef: r.AcceptReceiptRef, AcceptReceiptDigest: r.AcceptReceiptDigest, RejectReceiptRef: r.RejectReceiptRef, RejectReceiptDigest: r.RejectReceiptDigest, RecoveryReceiptRef: r.RecoveryReceiptRef, RecoveryReceiptDigest: r.RecoveryReceiptDigest, AcceptedCommandFenceRef: r.AcceptedCommandFence.Ref, AcceptedCommandFenceDigest: r.AcceptedCommandFence.Digest, AcceptedCommandFenceCursor: r.AcceptedCommandFence.Cursor, RejectedCommandFenceRef: r.RejectedCommandFence.Ref, RejectedCommandFenceDigest: r.RejectedCommandFence.Digest, RejectedCommandFenceCursor: r.RejectedCommandFence.Cursor})
	if errors.Is(err, sql.ErrNoRows) {
		return DurableSubmission{}, ErrDurableSubmissionConflict
	}
	if err != nil {
		return DurableSubmission{}, err
	}
	return mapDurableSubmission(row)
}

// Query 是 Get 的只读别名，不会创建或授权 submission。
func (s *DurableSubmissionStore) Query(c context.Context, k DurableSubmissionKey) (DurableSubmission, error) {
	return s.Get(c, k)
}

// CheckSchema 确认 candidate 表已由显式安装步骤创建，不触发运行时迁移。
func (s *DurableSubmissionStore) CheckSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrDurableSubmissionSchema
	}
	if ctx == nil {
		return ErrContextRequired
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='orchestration_command_submissions'").Scan(&count); err != nil || count != 1 {
		return errors.Join(ErrDurableSubmissionSchema, err)
	}
	return nil
}

// checkSchemaForDBTX 在调用方已持有事务时复用同一 DBTX，避免另开连接造成死锁。
func (s *DurableSubmissionStore) checkSchemaForDBTX(ctx context.Context, dbtx storedb.DBTX) error {
	if dbtx == nil {
		return s.CheckSchema(ctx)
	}
	if _, ok := dbtx.(*sql.DB); ok {
		return s.CheckSchema(ctx)
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return err
	}
	var count int
	if err := dbtx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='orchestration_command_submissions'").Scan(&count); err != nil || count != 1 {
		return errors.Join(ErrDurableSubmissionSchema, err)
	}
	return nil
}
func deriveSubmissionIDDurable(r DurableSubmissionRequest) string {
	x := sha256.Sum256([]byte("orch.command.submission/v1\n" + r.CommandID + "\n" + r.IdempotencyNamespace + "\n" + r.IdempotencyKeyDigest))
	return hex.EncodeToString(x[:])
}

// validateSubmissionRequestDurable 校验 submission 的文本、版本和摘要字段。
func validateSubmissionRequestDurable(r DurableSubmissionRequest) error {
	if err := validateDurableRequestText(r); err != nil {
		return err
	}
	return validateDurableRequestDigests(r)
}

// validateDurableRequestText 校验 submission 请求中的有界文本字段。
func validateDurableRequestText(r DurableSubmissionRequest) error {
	for _, v := range []struct {
		name, value string
		max         int
	}{{"command_id", r.CommandID, 128}, {"submission_id", r.SubmissionID, 128}, {"subject_selector", r.SubjectSelector, 512}, {"caller_principal_ref", r.CallerPrincipalRef, 512}, {"scope_ref", r.ScopeRef, 512}, {"source_fence_ref", r.SourceFenceRef, 512}, {"target_fence_ref", r.TargetFenceRef, 512}, {"idempotency_namespace", r.IdempotencyNamespace, 256}, {"pending_cursor", r.PendingCursor, 256}} {
		if err := validateDurableRequestTextField(v.name, v.value, v.max); err != nil {
			return ErrDurableSubmissionConflict
		}
	}
	return nil
}

// validateDurableRequestTextField 校验请求文本并拒绝缺失绑定字段。
func validateDurableRequestTextField(name, value string, max int) error {
	if value == "" && name != "submission_id" {
		return ErrDurableSubmissionConflict
	}
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value || len(value) > max || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return ErrDurableSubmissionConflict
	}
	return nil
}

// validateDurableRequestDigests 校验 submission 请求中的版本和摘要字段。
func validateDurableRequestDigests(r DurableSubmissionRequest) error {
	if r.CommandVersion != 1 || !durableDigest(r.IdempotencyKeyDigest) || !durableDigest(r.RequestDigest) || !durableDigest(r.PendingCursorDigest) {
		return ErrDurableSubmissionConflict
	}
	return nil
}
func validateDurableKey(k DurableSubmissionKey) error {
	if k.CommandID == "" || k.SubmissionID == "" || strings.TrimSpace(k.CommandID) != k.CommandID || strings.TrimSpace(k.SubmissionID) != k.SubmissionID {
		return ErrDurableSubmissionConflict
	}
	return nil
}

// validateDurableKeyNamespace 校验 namespace lookup 的必填字段。
func validateDurableKeyNamespace(k DurableSubmissionKey) error {
	if k.IdempotencyNamespace == "" || strings.TrimSpace(k.IdempotencyNamespace) != k.IdempotencyNamespace || len(k.IdempotencyNamespace) > 256 || !utf8.ValidString(k.IdempotencyNamespace) || strings.ContainsRune(k.IdempotencyNamespace, 0) || !durableDigest(k.IdempotencyKeyDigest) {
		return ErrDurableSubmissionConflict
	}
	return nil
}

// matchDurableOptionalKeyFields 拒绝 lookup 附带的 command/submission 身份漂移。
func matchDurableOptionalKeyFields(got DurableSubmission, key DurableSubmissionKey) error {
	if key.CommandID != "" && got.Request.CommandID != key.CommandID {
		return ErrDurableSubmissionConflict
	}
	if key.SubmissionID != "" && got.Request.SubmissionID != key.SubmissionID {
		return ErrDurableSubmissionConflict
	}
	return nil
}

// replayDurableResolution 只返回完全相同的终态重放，拒绝回执漂移。
func replayDurableResolution(current DurableSubmission, request SubmissionResolution) (DurableSubmission, error) {
	if current.Resolution != request.Resolution || current.AcceptReceiptRef != request.AcceptReceiptRef || current.AcceptReceiptDigest != request.AcceptReceiptDigest || current.RejectReceiptRef != request.RejectReceiptRef || current.RejectReceiptDigest != request.RejectReceiptDigest || current.RecoveryReceiptRef != request.RecoveryReceiptRef || current.RecoveryReceiptDigest != request.RecoveryReceiptDigest || current.AcceptedCommandFence != request.AcceptedCommandFence || current.RejectedCommandFence != request.RejectedCommandFence {
		return DurableSubmission{}, ErrDurableSubmissionConflict
	}
	return current, nil
}

func resolutionKindFor(resolution AcceptResolution) ResolutionKind {
	if resolution == AcceptAccepted {
		return ResolutionAccepted
	}
	return ResolutionRejected
}
func durableDigest(v string) bool {
	if len(v) != 64 || strings.ToLower(v) != v {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}
func sameSubmissionRequest(a, b DurableSubmissionRequest) bool {
	if b.SubmissionID == "" {
		b.SubmissionID = a.SubmissionID
	}
	return a == b
}
func classifyDurableRead(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return ErrDurableSubmissionNotFound
	}
	return e
}

func classifyDurableWrite(e error) error {
	if strings.Contains(strings.ToLower(e.Error()), "constraint") {
		return errors.Join(ErrDurableSubmissionConflict, e)
	}
	return e
}

// mapDurableSubmission 将 SQLC 行映射为严格校验的 durable submission。
func mapDurableSubmission(r storedb.OrchestrationCommandSubmission) (DurableSubmission, error) {
	if err := validateDurableRowIdentity(r); err != nil {
		return DurableSubmission{}, fmt.Errorf("durable submission row identity: %w", err)
	}
	if err := validateDurableRowTimes(r); err != nil {
		return DurableSubmission{}, fmt.Errorf("durable submission row time: %w", err)
	}
	if err := validateDurableRowResolution(r); err != nil {
		return DurableSubmission{}, fmt.Errorf("durable submission row resolution: %w", err)
	}
	return durableSubmissionFromRow(r), nil
}

// validateDurableRowIdentity 校验持久化行的固定身份、文本和摘要。
func validateDurableRowIdentity(r storedb.OrchestrationCommandSubmission) error {
	if err := validateDurableRowVersionAndDigests(r); err != nil {
		return ErrDurableSubmissionConflict
	}
	return validateDurableRowText(r)
}

// validateDurableRowVersionAndDigests 校验持久化行的版本和摘要字段。
func validateDurableRowVersionAndDigests(r storedb.OrchestrationCommandSubmission) error {
	if r.CommandVersion != 1 || r.Revision <= 0 || !durableDigest(r.IdempotencyKeyDigest) || !durableDigest(r.RequestDigest) || !durableDigest(r.PendingCursorDigest) {
		return ErrDurableSubmissionConflict
	}
	return nil
}

// validateDurableRowText 校验持久化行的必填文本边界和编码。
func validateDurableRowText(r storedb.OrchestrationCommandSubmission) error {
	for _, field := range []struct {
		name, value string
		max         int
	}{{"command_id", r.CommandID, 128}, {"submission_id", r.SubmissionID, 128}, {"subject_selector", r.SubjectSelector, 512}, {"caller_principal_ref", r.CallerPrincipalRef, 512}, {"scope_ref", r.ScopeRef, 512}, {"source_fence_ref", r.SourceFenceRef, 512}, {"target_fence_ref", r.TargetFenceRef, 512}, {"idempotency_namespace", r.IdempotencyNamespace, 256}, {"pending_cursor", r.PendingCursor, 256}} {
		if field.value == "" || strings.TrimSpace(field.value) != field.value || len(field.value) > field.max || !utf8.ValidString(field.value) || strings.ContainsRune(field.value, 0) {
			return ErrDurableSubmissionConflict
		}
	}
	return nil
}

// validateDurableRowTimes 校验记录时间的严格 UTC 格式和顺序。
func validateDurableRowTimes(r storedb.OrchestrationCommandSubmission) error {
	recorded, err := parseDurableUTC(r.RecordedAt)
	if err != nil {
		return fmt.Errorf("recorded_at=%q: %w", r.RecordedAt, err)
	}
	updated, err := parseDurableUTC(r.UpdatedAt)
	if err != nil || updated.Before(recorded) {
		if err != nil {
			return fmt.Errorf("updated_at=%q: %w", r.UpdatedAt, err)
		}
		return fmt.Errorf("updated_at precedes recorded_at: %w", ErrDurableSubmissionConflict)
	}
	return nil
}

// validateDurableRowResolution 校验 pending/accepted/aborted 的 one-hot 回执形状。
func validateDurableRowResolution(r storedb.OrchestrationCommandSubmission) error {
	switch AcceptResolution(r.AcceptResolution) {
	case AcceptPending:
		if r.ResolutionKind != string(ResolutionPending) {
			return ErrDurableSubmissionConflict
		}
		return emptyDurableReceipts(r)
	case AcceptAccepted:
		if r.ResolutionKind != string(ResolutionAccepted) {
			return ErrDurableSubmissionConflict
		}
		return acceptedDurableReceipts(r)
	case AcceptAborted:
		if r.ResolutionKind != string(ResolutionRejected) {
			return ErrDurableSubmissionConflict
		}
		return abortedDurableReceipts(r)
	default:
		return ErrDurableSubmissionConflict
	}
}

// emptyDurableReceipts 要求 pending 行不携带任何终结回执。
func emptyDurableReceipts(r storedb.OrchestrationCommandSubmission) error {
	values := []string{r.AcceptReceiptRef, r.AcceptReceiptDigest, r.RejectReceiptRef, r.RejectReceiptDigest, r.RecoveryReceiptRef, r.RecoveryReceiptDigest, r.AcceptedCommandFenceRef, r.AcceptedCommandFenceDigest, r.AcceptedCommandFenceCursor, r.RejectedCommandFenceRef, r.RejectedCommandFenceDigest, r.RejectedCommandFenceCursor}
	for _, value := range values {
		if value != "" {
			return ErrDurableSubmissionConflict
		}
	}
	return nil
}

// acceptedDurableReceipts 校验 accepted 行的 accept/recovery 回执配对。
func acceptedDurableReceipts(r storedb.OrchestrationCommandSubmission) error {
	if r.AcceptReceiptRef == "" || !durableDigest(r.AcceptReceiptDigest) || r.RejectReceiptRef != "" || r.RejectReceiptDigest != "" {
		return ErrDurableSubmissionConflict
	}
	if err := validateFenceColumns(r.AcceptedCommandFenceRef, r.AcceptedCommandFenceDigest, r.AcceptedCommandFenceCursor); err != nil {
		return err
	}
	if r.RejectedCommandFenceRef != "" || r.RejectedCommandFenceDigest != "" || r.RejectedCommandFenceCursor != "" {
		return ErrDurableSubmissionConflict
	}
	return validateRecoveryReceipt(r)
}

// abortedDurableReceipts 校验 aborted 行的 reject/recovery 回执配对。
func abortedDurableReceipts(r storedb.OrchestrationCommandSubmission) error {
	if r.RejectReceiptRef == "" || !durableDigest(r.RejectReceiptDigest) || r.AcceptReceiptRef != "" || r.AcceptReceiptDigest != "" {
		return ErrDurableSubmissionConflict
	}
	if err := validateFenceColumns(r.RejectedCommandFenceRef, r.RejectedCommandFenceDigest, r.RejectedCommandFenceCursor); err != nil {
		return err
	}
	if r.AcceptedCommandFenceRef != "" || r.AcceptedCommandFenceDigest != "" || r.AcceptedCommandFenceCursor != "" {
		return ErrDurableSubmissionConflict
	}
	return validateRecoveryReceipt(r)
}

func validateFenceColumns(ref, digest, cursor string) error {
	if validateCommandFenceText(ref, 512) != nil || validateCommandFenceText(cursor, 256) != nil || !durableDigest(digest) {
		return ErrDurableSubmissionConflict
	}
	return nil
}

// validateRecoveryReceipt 校验 recovery receipt 引用与摘要必须成对出现。
func validateRecoveryReceipt(r storedb.OrchestrationCommandSubmission) error {
	if r.RecoveryReceiptRef == "" || !durableDigest(r.RecoveryReceiptDigest) {
		return ErrDurableSubmissionConflict
	}
	for _, value := range []string{r.AcceptReceiptRef, r.RejectReceiptRef, r.RecoveryReceiptRef} {
		if value != "" && (strings.TrimSpace(value) != value || len(value) > 512 || !utf8.ValidString(value) || strings.ContainsRune(value, 0)) {
			return ErrDurableSubmissionConflict
		}
	}
	return nil
}

// durableSubmissionFromRow 构造已完成完整校验的领域对象。
func durableSubmissionFromRow(r storedb.OrchestrationCommandSubmission) DurableSubmission {
	acceptedFence := CommandFence{Ref: r.AcceptedCommandFenceRef, Digest: r.AcceptedCommandFenceDigest, Cursor: r.AcceptedCommandFenceCursor, CommandID: r.CommandID, CommandVersion: r.CommandVersion, SubjectSelector: r.SubjectSelector}
	rejectedFence := CommandFence{Ref: r.RejectedCommandFenceRef, Digest: r.RejectedCommandFenceDigest, Cursor: r.RejectedCommandFenceCursor, CommandID: r.CommandID, CommandVersion: r.CommandVersion, SubjectSelector: r.SubjectSelector}
	if acceptedFence.Ref == "" {
		acceptedFence = CommandFence{}
	}
	if rejectedFence.Ref == "" {
		rejectedFence = CommandFence{}
	}
	return DurableSubmission{Request: DurableSubmissionRequest{CommandID: r.CommandID, CommandVersion: r.CommandVersion, SubmissionID: r.SubmissionID, SubjectSelector: r.SubjectSelector, CallerPrincipalRef: r.CallerPrincipalRef, ScopeRef: r.ScopeRef, SourceFenceRef: r.SourceFenceRef, TargetFenceRef: r.TargetFenceRef, IdempotencyNamespace: r.IdempotencyNamespace, IdempotencyKeyDigest: r.IdempotencyKeyDigest, RequestDigest: r.RequestDigest, PendingCursor: r.PendingCursor, PendingCursorDigest: r.PendingCursorDigest}, Resolution: AcceptResolution(r.AcceptResolution), ResolutionKind: ResolutionKind(r.ResolutionKind), AcceptedCommandFence: acceptedFence, RejectedCommandFence: rejectedFence, AcceptReceiptRef: r.AcceptReceiptRef, AcceptReceiptDigest: r.AcceptReceiptDigest, RejectReceiptRef: r.RejectReceiptRef, RejectReceiptDigest: r.RejectReceiptDigest, RecoveryReceiptRef: r.RecoveryReceiptRef, RecoveryReceiptDigest: r.RecoveryReceiptDigest, Revision: r.Revision, RecordedAt: r.RecordedAt, UpdatedAt: r.UpdatedAt}
}

// parseDurableUTC 解析规范 RFC3339Nano UTC 时间戳。
func parseDurableUTC(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || !durableTimestampEncodingMatches(parsed, value) {
		return time.Time{}, fmt.Errorf("%w: timestamp is not canonical UTC", ErrDurableSubmissionConflict)
	}
	return parsed, nil
}

// durableTimestampEncodingMatches accepts RFC3339Nano and SQLite's fixed-millisecond UTC form.
func durableTimestampEncodingMatches(parsed time.Time, value string) bool {
	utc := parsed.UTC()
	return value == utc.Format(time.RFC3339Nano) || value == utc.Format("2006-01-02T15:04:05.000Z")
}

var _ = strings.TrimSpace
