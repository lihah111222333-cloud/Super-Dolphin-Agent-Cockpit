package orchestrationcommand

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// AttemptStore is the durable owner for command attempts. It consumes only a
// narrow IntentReader and never owns provider, custody or transport behavior.
type AttemptStore struct {
	db        *sql.DB
	queries   *storedb.Queries
	intents   IntentReader
	admission AttemptAdmission
}

// NewAttemptStore 在已有 SQLite 句柄上构造 attempt owner，并要求事务绑定依赖。
func NewAttemptStore(db *sql.DB, intents IntentReader, admission AttemptAdmission) (*AttemptStore, error) {
	if db == nil || isNilDependency(intents) || isNilDependency(admission) {
		return nil, errors.New("command attempt store requires database, intent reader, and admission verifier")
	}
	if !supportsTransactionalIntentReader(intents) || !supportsTransactionalAdmission(admission) {
		return nil, ErrAttemptTransaction
	}
	return &AttemptStore{db: db, queries: storedb.New(db), intents: intents, admission: admission}, nil
}

// NewCommandAttemptStore 是按完整领域命名的构造别名。
func NewCommandAttemptStore(db *sql.DB, intents IntentReader, admission AttemptAdmission) (*AttemptStore, error) {
	return NewAttemptStore(db, intents, admission)
}

// CommandAttemptGateway exposes the provider-boundary protocol without a
// provider dependency. The caller must invoke the provider only after Issue.
type CommandAttemptGateway struct{ store *AttemptStore }

// NewCommandAttemptGateway 创建绑定 attempt store 的 provider 边界网关。
func NewCommandAttemptGateway(store *AttemptStore) (*CommandAttemptGateway, error) {
	if store == nil || store.queries == nil || isNilDependency(store.intents) || isNilDependency(store.admission) {
		return nil, errors.New("command attempt gateway requires attempt store")
	}
	return &CommandAttemptGateway{store: store}, nil
}

// NewGateway 是按 gateway 命名的构造别名。
func NewGateway(store *AttemptStore) (*CommandAttemptGateway, error) {
	return NewCommandAttemptGateway(store)
}

// Issue 记录一次 attempt 并返回一次性 provider permit。
// Replays of pending or unknown attempts never return a permit, preventing a
// second provider call after a crash or an indeterminate response.
func (g *CommandAttemptGateway) Issue(ctx context.Context, req AttemptRequest) (DispatchPermit, error) {
	if g == nil || g.store == nil {
		return DispatchPermit{}, ErrAttemptNotFound
	}
	attempt, fresh, err := g.store.reserve(ctx, req)
	if err != nil {
		return DispatchPermit{}, err
	}
	if !fresh {
		if attempt.Phase == AttemptEffectPending || attempt.Phase == AttemptOutcomeUnknown {
			return DispatchPermit{}, ErrAttemptUnresolved
		}
		return DispatchPermit{}, ErrAttemptTerminal
	}
	return permitFromAttempt(attempt), nil
}

// PrepareDispatch 是 Issue 的语义别名。
func (g *CommandAttemptGateway) PrepareDispatch(ctx context.Context, req AttemptRequest) (DispatchPermit, error) {
	return g.Issue(ctx, req)
}

// IssueDispatch 是 dispatcher adapter 使用的 record-before-effect 别名。
func (g *CommandAttemptGateway) IssueDispatch(ctx context.Context, req AttemptRequest) (DispatchPermit, error) {
	return g.Issue(ctx, req)
}

// Consume 在 provider 调用前原子消费已发放的 permit；
// effect. The durable phase moves effect_pending/pending to
// outcome_unknown/unknown in one CAS, so a replay or concurrent caller can
// never obtain a second effect opportunity. The returned Attempt retains the
// original permit material; a caller that loses the result after this point
// may only use that same permit with ReconcileUnknown or QueryUnknown.
func (g *CommandAttemptGateway) Consume(ctx context.Context, permit DispatchPermit) (Attempt, error) {
	if g == nil || g.store == nil {
		return Attempt{}, ErrAttemptNotFound
	}
	return g.store.ConsumePermit(ctx, permit)
}

// ConsumePermit 是 Consume 的 provider-boundary 显式命名。
func (g *CommandAttemptGateway) ConsumePermit(ctx context.Context, permit DispatchPermit) (Attempt, error) {
	return g.Consume(ctx, permit)
}

// RecordBeforeEffect 为拆分 permit 发放与 dispatch 编排的 adapter 暴露持久记录操作。
func (s *AttemptStore) RecordBeforeEffect(ctx context.Context, req AttemptRequest) (Attempt, error) {
	attempt, fresh, err := s.reserve(ctx, req)
	if err == nil && !fresh {
		return Attempt{}, replayAttemptError(attempt)
	}
	return attempt, err
}

// RecordAttempt 持久化一次 record-before-effect attempt 并拒绝不可安全重放的记录。
func (s *AttemptStore) RecordAttempt(ctx context.Context, req AttemptRequest) (Attempt, error) {
	return s.RecordBeforeEffect(ctx, req)
}

// ConsumePermit 在外部 effect 前原子消费精确 permit；
// effect. migration135 has no consumed column, so the existing
// effect_pending/pending -> outcome_unknown/unknown transition is the durable
// one-shot marker. A second caller therefore sees a failed CAS and receives a
// sticky, query-only error; it never gets a replacement permit or attempt.
func (s *AttemptStore) ConsumePermit(ctx context.Context, permit DispatchPermit) (Attempt, error) {
	if err := validateDispatchPermit(permit); err != nil {
		return Attempt{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Attempt{}, err
	}
	var row storedb.OrchestrationCommandAttempt
	err := sqlctx.WithWriteRetry(ctx, func() error {
		var queryErr error
		row, queryErr = s.queries.ConsumeOrchestrationCommandAttemptPermit(ctx, storedb.ConsumeOrchestrationCommandAttemptPermitParams{
			CommandID: permit.CommandID, AttemptID: permit.AttemptID, WakeupID: permit.WakeupID,
			WakeupFence: permit.WakeupFence, RequestDigest: permit.RequestDigest,
			DispatchReceipt: permit.DispatchReceipt, Permit: permit.Permit, Nonce: permit.Nonce,
		})
		return queryErr
	})
	if err == nil {
		return attemptFromRow(row)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, fmt.Errorf("consume command attempt permit: %w", err)
	}
	return s.classifyConsumeMiss(ctx, permit)
}

// Consume 是 store 层 ConsumePermit 的简洁别名。
func (s *AttemptStore) Consume(ctx context.Context, permit DispatchPermit) (Attempt, error) {
	return s.ConsumePermit(ctx, permit)
}

// classifyConsumeMiss 将零行 CAS 映射为精确的 fail-closed 原因，
// after re-reading the immutable attempt identity and current phase. The read
// cannot grant a permit: it only explains why this consume did not win.
func (s *AttemptStore) classifyConsumeMiss(ctx context.Context, permit DispatchPermit) (Attempt, error) {
	current, err := s.GetByCommand(ctx, permit.CommandID)
	if err != nil {
		return Attempt{}, err
	}
	if !permitMatchesAttempt(current, permit) {
		return Attempt{}, ErrAttemptPermit
	}
	switch current.Phase {
	case AttemptOutcomeUnknown:
		return Attempt{}, errors.Join(ErrAttemptConsumed, ErrAttemptUnknownSticky, ErrAttemptUnresolved)
	case AttemptProviderAccepted, AttemptProviderFailed:
		return Attempt{}, ErrAttemptTerminal
	case AttemptEffectPending:
		return Attempt{}, fmt.Errorf("%w: consume CAS did not match pending outcome", ErrAttemptConflict)
	default:
		return Attempt{}, fmt.Errorf("%w: consume CAS observed unsupported phase %q", ErrAttemptOutcome, current.Phase)
	}
}

func (s *AttemptStore) reserve(ctx context.Context, req AttemptRequest) (Attempt, bool, error) {
	if err := validateAttemptRequest(req); err != nil {
		return Attempt{}, false, err
	}
	if err := s.ready(ctx); err != nil {
		return Attempt{}, false, err
	}
	attemptID := req.AttemptID
	if attemptID == "" {
		attemptID = deriveMaterial("attempt", req)
	}
	var out Attempt
	var fresh bool
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, txdb storedb.DBTX) error {
		var err error
		out, fresh, err = reserveInTx(ctx, txdb, q, s.intents, s.admission, req, attemptID)
		return err
	})
	return out, fresh, err
}

// reserveInTx 在同一 BEGIN IMMEDIATE 内完成 intent、admission 与 attempt 决策。
func reserveInTx(ctx context.Context, txdb storedb.DBTX, q *storedb.Queries, intents IntentReader, admission AttemptAdmission, req AttemptRequest, attemptID string) (Attempt, bool, error) {
	intent, err := readAttemptIntentTx(ctx, txdb, q, intents, req.CommandID)
	if err != nil {
		return Attempt{}, false, err
	}
	if intent.Intent.IdempotencyNamespace != req.IdempotencyNamespace || intent.Intent.IdempotencyKeyDigest != req.IdempotencyKeyDigest || intent.Intent.RequestDigest != req.RequestDigest {
		return Attempt{}, false, ErrAttemptConflict
	}
	verifier, ok := admission.(TransactionalAttemptAdmission)
	if !ok {
		return Attempt{}, false, ErrAttemptTransaction
	}
	if err := verifier.VerifyAttemptAdmissionTx(ctx, txdb, req, intent); err != nil {
		return Attempt{}, false, err
	}
	if existing, found, err := existingAttemptInTx(ctx, q, req); found || err != nil {
		return existing, false, err
	}
	return insertAttemptInTx(ctx, q, req, attemptID)
}

func existingAttemptInTx(ctx context.Context, q *storedb.Queries, req AttemptRequest) (Attempt, bool, error) {
	row, err := q.GetOrchestrationCommandAttemptByKey(ctx, storedb.GetOrchestrationCommandAttemptByKeyParams{IdempotencyNamespace: req.IdempotencyNamespace, IdempotencyKeyDigest: req.IdempotencyKeyDigest})
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, false, nil
	}
	if err != nil {
		return Attempt{}, false, fmt.Errorf("read command attempt: %w", err)
	}
	existing, err := attemptFromRow(row)
	if err != nil {
		return Attempt{}, true, err
	}
	if !attemptMatchesRequest(existing, req) {
		return Attempt{}, true, ErrAttemptConflict
	}
	return existing, true, nil
}

// insertAttemptInTx 在 owner 事务内生成并持久化唯一 provider material。
func insertAttemptInTx(ctx context.Context, q *storedb.Queries, req AttemptRequest, attemptID string) (Attempt, bool, error) {
	m, err := deriveMaterials(req, attemptID)
	if err != nil {
		return Attempt{}, false, err
	}
	row, err := q.CreateOrchestrationCommandAttempt(ctx, storedb.CreateOrchestrationCommandAttemptParams{CommandID: req.CommandID, AttemptID: attemptID, WakeupID: req.WakeupID, WakeupFence: req.WakeupFence, IdempotencyNamespace: req.IdempotencyNamespace, IdempotencyKeyDigest: req.IdempotencyKeyDigest, RequestDigest: req.RequestDigest, DispatchReceipt: m.dispatchReceipt, Permit: m.permit, Nonce: m.nonce})
	if err == nil {
		attempt, mapErr := attemptFromRow(row)
		return attempt, mapErr == nil, mapErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, false, classifyAttemptInsertError(err)
	}
	row, err = q.GetOrchestrationCommandAttemptByKey(ctx, storedb.GetOrchestrationCommandAttemptByKeyParams{IdempotencyNamespace: req.IdempotencyNamespace, IdempotencyKeyDigest: req.IdempotencyKeyDigest})
	if err != nil {
		return Attempt{}, false, fmt.Errorf("read command attempt after conflict: %w", err)
	}
	existing, err := attemptFromRow(row)
	if err != nil {
		return Attempt{}, false, err
	}
	if !attemptMatchesRequest(existing, req) {
		return Attempt{}, false, ErrAttemptConflict
	}
	return existing, false, nil
}

func supportsTransactionalIntentReader(reader IntentReader) bool {
	if _, ok := reader.(TransactionalAttemptIntentReader); ok {
		return true
	}
	_, ok := reader.(*Store)
	return ok
}

func supportsTransactionalAdmission(admission AttemptAdmission) bool {
	_, ok := admission.(TransactionalAttemptAdmission)
	return ok
}

func readAttemptIntentTx(ctx context.Context, txdb storedb.DBTX, q *storedb.Queries, reader IntentReader, commandID string) (Record, error) {
	if txReader, ok := reader.(TransactionalAttemptIntentReader); ok {
		return txReader.GetTx(ctx, txdb, commandID)
	}
	if _, ok := reader.(*Store); !ok {
		return Record{}, ErrAttemptTransaction
	}
	row, err := q.GetOrchestrationCommandIntent(ctx, storedb.GetOrchestrationCommandIntentParams{CommandID: commandID})
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("read command intent: %w", err)
	}
	return recordFromRow(row)
}

func replayAttemptError(attempt Attempt) error {
	if attempt.Phase == AttemptEffectPending || attempt.Phase == AttemptOutcomeUnknown {
		return ErrAttemptUnresolved
	}
	return ErrAttemptTerminal
}

// AttemptKey identifies the durable command attempt without creating a row.
// It aliases the intent Key so callers cannot accidentally use a different
// namespace/digest representation at the command/attempt boundary.
type AttemptKey = Key

// GetByKey 按幂等命名空间和摘要读取唯一 durable attempt。
func (s *AttemptStore) GetByKey(ctx context.Context, key AttemptKey) (Attempt, error) {
	if err := validateAttemptKey(key); err != nil {
		return Attempt{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Attempt{}, err
	}
	row, err := s.queries.GetOrchestrationCommandAttemptByKey(ctx, storedb.GetOrchestrationCommandAttemptByKeyParams{
		IdempotencyNamespace: key.Namespace, IdempotencyKeyDigest: key.Digest,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, ErrAttemptNotFound
	}
	if err != nil {
		return Attempt{}, fmt.Errorf("read command attempt: %w", err)
	}
	return attemptFromRow(row)
}

// GetByCommand 按 command ID 读取唯一 durable attempt。
func (s *AttemptStore) GetByCommand(ctx context.Context, commandID string) (Attempt, error) {
	if err := validateText("command_id", commandID, 128); err != nil {
		return Attempt{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Attempt{}, err
	}
	row, err := s.queries.GetOrchestrationCommandAttemptByCommand(ctx, storedb.GetOrchestrationCommandAttemptByCommandParams{CommandID: commandID})
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, ErrAttemptNotFound
	}
	if err != nil {
		return Attempt{}, fmt.Errorf("read command attempt: %w", err)
	}
	return attemptFromRow(row)
}

// MarkProviderResult 在外部调用后记录 provider 响应。
// Unknown is durable and blocks Issue until an explicit reconciliation query.
func (s *AttemptStore) MarkProviderResult(ctx context.Context, req ProviderResultRequest) (Attempt, error) {
	return s.markResult(ctx, req, false)
}

// MarkOutcome 记录 provider 返回结果并保持未知结果的粘滞围栏。
func (s *AttemptStore) MarkOutcome(ctx context.Context, req ProviderResultRequest) (Attempt, error) {
	return s.MarkProviderResult(ctx, req)
}

// ReconcileUnknown 只解析已进入 unknown 的 attempt，不调用 provider。
func (s *AttemptStore) ReconcileUnknown(ctx context.Context, req ProviderResultRequest) (Attempt, error) {
	return s.markResult(ctx, req, true)
}

// QueryUnknown 只读返回原始 unknown 记录，不制造新 attempt 或 permit。
func (s *AttemptStore) QueryUnknown(ctx context.Context, key AttemptKey) (Attempt, error) {
	attempt, err := s.GetByKey(ctx, key)
	if err != nil {
		return Attempt{}, err
	}
	if attempt.Phase != AttemptOutcomeUnknown {
		return Attempt{}, ErrAttemptUnresolved
	}
	return attempt, nil
}

// markResult 在 permit 全字段匹配后以 CAS 记录 provider 结果或 unknown 对账。
func (s *AttemptStore) markResult(ctx context.Context, req ProviderResultRequest, reconcile bool) (Attempt, error) {
	if err := validateProviderResultRequest(req); err != nil {
		return Attempt{}, err
	}
	if req.Result.Kind == ProviderResultPending {
		return Attempt{}, fmt.Errorf("%w: pending is not a provider outcome", ErrAttemptOutcome)
	}
	if err := s.ready(ctx); err != nil {
		return Attempt{}, err
	}
	current, err := s.GetByCommand(ctx, req.CommandID)
	if err != nil {
		return Attempt{}, err
	}
	if !permitMatchesAttempt(current, req.DispatchPermit) {
		return Attempt{}, ErrAttemptPermit
	}
	noOp, err := resultTransitionDecision(current, req.Result, reconcile)
	if err != nil {
		return Attempt{}, err
	}
	if noOp {
		return current, nil
	}
	nextPhase, nextOutcome := resultState(req.Result)
	return s.updateResult(ctx, req, current, nextPhase, nextOutcome)
}

// resultTransitionDecision 校验 provider 结果与当前 phase 的单调转换。
func resultTransitionDecision(current Attempt, result ProviderResult, reconcile bool) (bool, error) {
	if result.Kind == ProviderResultUnknown {
		if reconcile || current.Phase != AttemptEffectPending {
			return false, ErrAttemptUnknownSticky
		}
		return false, nil
	}
	if reconcile {
		if current.Phase != AttemptOutcomeUnknown {
			return false, ErrAttemptUnresolved
		}
		return false, nil
	}
	if current.Phase == AttemptEffectPending {
		return false, nil
	}
	if sameResult(current.Result, result) {
		return true, nil
	}
	return false, ErrAttemptOutcome
}

// updateResult 在 CAS 失败时只接受完全相同的已提交结果重放。
func (s *AttemptStore) updateResult(ctx context.Context, req ProviderResultRequest, current Attempt, nextPhase AttemptPhase, nextOutcome ProviderResultKind) (Attempt, error) {
	row, err := s.queries.UpdateOrchestrationCommandAttemptOutcome(ctx, storedb.UpdateOrchestrationCommandAttemptOutcomeParams{
		NextPhase: string(nextPhase), NextOutcome: string(nextOutcome),
		ProviderSubmissionID: req.Result.SubmissionID, ProviderReceiptID: req.Result.ReceiptID, FailureCode: req.Result.FailureCode,
		CommandID: req.CommandID, AttemptID: req.AttemptID, WakeupFence: req.WakeupFence, RequestDigest: req.RequestDigest,
		DispatchReceipt: req.DispatchReceipt, Permit: req.Permit, Nonce: req.Nonce,
		ExpectedPhase: string(current.Phase), ExpectedOutcome: string(current.ResultKind()),
	})
	if err == nil {
		return attemptFromRow(row)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, fmt.Errorf("record command attempt outcome: %w", err)
	}
	latest, latestErr := s.GetByCommand(ctx, req.CommandID)
	if latestErr != nil {
		return Attempt{}, latestErr
	}
	if !permitMatchesAttempt(latest, req.DispatchPermit) {
		return Attempt{}, ErrAttemptPermit
	}
	if sameResult(latest.Result, req.Result) {
		return latest, nil
	}
	return Attempt{}, ErrAttemptOutcome
}

// ready 校验 owner、事务句柄和调用上下文均可用。
func (s *AttemptStore) ready(ctx context.Context) error {
	if s == nil || s.db == nil || s.queries == nil || isNilDependency(s.intents) || isNilDependency(s.admission) {
		return errors.New("command attempt store dependencies are missing")
	}
	if ctx == nil {
		return ErrContextRequired
	}
	return ctx.Err()
}

// validateAttemptRequest 校验 attempt 请求的身份字段和摘要格式。
func validateAttemptRequest(req AttemptRequest) error {
	for name, value := range map[string]string{"command_id": req.CommandID, "wakeup_id": req.WakeupID, "wakeup_fence": req.WakeupFence, "idempotency_namespace": req.IdempotencyNamespace} {
		if err := validateText(name, value, 256); err != nil {
			return err
		}
	}
	if req.AttemptID != "" {
		if err := validateText("attempt_id", req.AttemptID, 128); err != nil {
			return err
		}
	}
	if err := validateDigest("idempotency_key_digest", req.IdempotencyKeyDigest); err != nil {
		return err
	}
	return validateDigest("request_digest", req.RequestDigest)
}

func validateAttemptKey(key AttemptKey) error {
	if err := validateText("idempotency_namespace", key.Namespace, 256); err != nil {
		return err
	}
	return validateDigest("idempotency_key_digest", key.Digest)
}

// validateDispatchPermit rejects incomplete or non-canonical provider
// material before any database lookup. A permit is an opaque binding, not an
// authorization decision; the durable CAS below still has to match every
// field exactly.
func validateDispatchPermit(permit DispatchPermit) error {
	if permit.AttemptNumber != 1 {
		return fmt.Errorf("%w: attempt number is invalid", ErrAttemptPermit)
	}
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{name: "command_id", value: permit.CommandID, limit: 128},
		{name: "attempt_id", value: permit.AttemptID, limit: 128},
		{name: "wakeup_id", value: permit.WakeupID, limit: 256},
		{name: "wakeup_fence", value: permit.WakeupFence, limit: 256},
		{name: "dispatch_receipt", value: permit.DispatchReceipt, limit: 256},
		{name: "permit", value: permit.Permit, limit: 256},
		{name: "nonce", value: permit.Nonce, limit: 256},
	} {
		if err := validateText(field.name, field.value, field.limit); err != nil {
			return fmt.Errorf("%w: %v", ErrAttemptPermit, err)
		}
	}
	if err := validateDigest("request_digest", permit.RequestDigest); err != nil {
		return fmt.Errorf("%w: %v", ErrAttemptPermit, err)
	}
	return nil
}

func validateProviderResultRequest(req ProviderResultRequest) error {
	if err := validateDispatchPermit(req.DispatchPermit); err != nil {
		return err
	}
	if err := validateAttemptRequest(AttemptRequest{CommandID: req.CommandID, AttemptID: req.AttemptID, WakeupID: req.WakeupID, WakeupFence: req.WakeupFence, RequestDigest: req.RequestDigest, IdempotencyNamespace: "x", IdempotencyKeyDigest: validDigest}); err != nil {
		return err
	}
	return req.Result.Validate()
}

const validDigest = "0000000000000000000000000000000000000000000000000000000000000000"

type attemptMaterials struct{ dispatchReceipt, permit, nonce string }

// deriveMaterials creates the provider-facing handles only for a fresh
// durable attempt. They are generated from the OS CSPRNG and persisted in the
// same insert as the attempt, so replay reads the original handles while a
// random-source failure prevents the provider boundary from opening.
func deriveMaterials(_ AttemptRequest, _ string) (attemptMaterials, error) {
	dispatchReceipt, err := randomMaterial("dispatch-receipt")
	if err != nil {
		return attemptMaterials{}, fmt.Errorf("generate dispatch receipt: %w", err)
	}
	permit, err := randomMaterial("dispatch-permit")
	if err != nil {
		return attemptMaterials{}, fmt.Errorf("generate dispatch permit: %w", err)
	}
	nonce, err := randomMaterial("dispatch-nonce")
	if err != nil {
		return attemptMaterials{}, fmt.Errorf("generate dispatch nonce: %w", err)
	}
	return attemptMaterials{dispatchReceipt: dispatchReceipt, permit: permit, nonce: nonce}, nil
}

func randomMaterial(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + ":" + hex.EncodeToString(raw), nil
}

// isNilDependency catches interfaces containing typed nil pointers. Calling a
// method through such an interface would otherwise panic after construction.
func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func deriveMaterial(domain string, req AttemptRequest, suffix ...string) string {
	raw := domain + "\x00" + req.CommandID + "\x00" + req.WakeupID + "\x00" + req.WakeupFence + "\x00" + req.IdempotencyNamespace + "\x00" + req.IdempotencyKeyDigest + "\x00" + req.RequestDigest
	for _, part := range suffix {
		raw += "\x00" + part
	}
	digest := sha256.Sum256([]byte("orch.command.attempt/v1\x00" + raw))
	return hex.EncodeToString(digest[:])
}

func permitFromAttempt(a Attempt) DispatchPermit {
	return DispatchPermit{CommandID: a.CommandID, AttemptID: a.AttemptID, WakeupID: a.WakeupID, WakeupFence: a.WakeupFence, RequestDigest: a.RequestDigest, AttemptNumber: a.AttemptNumber, DispatchReceipt: a.DispatchReceipt, Permit: a.Permit, Nonce: a.Nonce}
}

// permitMatchesAttempt 比较 permit 与持久 attempt 的全部不可变材料。
func permitMatchesAttempt(a Attempt, p DispatchPermit) bool {
	return a.CommandID == p.CommandID && a.AttemptID == p.AttemptID && a.WakeupID == p.WakeupID && a.WakeupFence == p.WakeupFence && a.RequestDigest == p.RequestDigest && a.AttemptNumber == p.AttemptNumber && a.DispatchReceipt == p.DispatchReceipt && a.Permit == p.Permit && a.Nonce == p.Nonce
}

// attemptMatchesRequest 比较重放请求与持久 attempt 的身份字段。
func attemptMatchesRequest(a Attempt, req AttemptRequest) bool {
	return a.CommandID == req.CommandID && a.WakeupID == req.WakeupID && a.WakeupFence == req.WakeupFence && a.IdempotencyNamespace == req.IdempotencyNamespace && a.IdempotencyKeyDigest == req.IdempotencyKeyDigest && a.RequestDigest == req.RequestDigest && (req.AttemptID == "" || a.AttemptID == req.AttemptID)
}

func resultState(result ProviderResult) (AttemptPhase, ProviderResultKind) {
	switch result.Kind {
	case ProviderResultUnknown:
		return AttemptOutcomeUnknown, ProviderResultUnknown
	case ProviderResultSubmission:
		return AttemptProviderAccepted, ProviderResultSubmission
	default:
		return AttemptProviderFailed, ProviderResultFailure
	}
}

// ResultKind 返回 attempt 当前可观察的 provider 结果类别。
func (a Attempt) ResultKind() ProviderResultKind {
	if a.Phase == AttemptEffectPending {
		return "pending"
	}
	return a.Result.Kind
}

func sameResult(a, b ProviderResult) bool {
	return a.Kind == b.Kind && a.SubmissionID == b.SubmissionID && a.ReceiptID == b.ReceiptID && a.FailureCode == b.FailureCode
}

// attemptFromRow 映射并校验数据库中的 attempt 记录。
func attemptFromRow(row storedb.OrchestrationCommandAttempt) (Attempt, error) {
	attempt := Attempt{CommandID: row.CommandID, AttemptID: row.AttemptID, WakeupID: row.WakeupID, WakeupFence: row.WakeupFence, IdempotencyNamespace: row.IdempotencyNamespace, IdempotencyKeyDigest: row.IdempotencyKeyDigest, RequestDigest: row.RequestDigest, AttemptNumber: row.AttemptNumber, DispatchReceipt: row.DispatchReceipt, Permit: row.Permit, Nonce: row.Nonce, Phase: AttemptPhase(row.Phase), Result: ProviderResult{Kind: ProviderResultKind(row.Outcome), SubmissionID: row.ProviderSubmissionID, ReceiptID: row.ProviderReceiptID, FailureCode: row.FailureCode}}
	if err := validateAttemptRequest(AttemptRequest{CommandID: attempt.CommandID, AttemptID: attempt.AttemptID, WakeupID: attempt.WakeupID, WakeupFence: attempt.WakeupFence, IdempotencyNamespace: attempt.IdempotencyNamespace, IdempotencyKeyDigest: attempt.IdempotencyKeyDigest, RequestDigest: attempt.RequestDigest}); err != nil {
		return Attempt{}, err
	}
	if attempt.AttemptNumber != 1 || !validAttemptPhase(attempt.Phase) || attempt.Result.Validate() != nil {
		return Attempt{}, errors.New("command attempt record is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, row.RecordedAt); err != nil {
		return Attempt{}, errors.New("command attempt record timestamp is invalid")
	}
	return attempt, nil
}

func validAttemptPhase(phase AttemptPhase) bool {
	return phase == AttemptEffectPending || phase == AttemptOutcomeUnknown || phase == AttemptProviderAccepted || phase == AttemptProviderFailed
}

func classifyAttemptInsertError(err error) error {
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) && (sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY || sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE) {
		return errors.Join(ErrAttemptConflict, err)
	}
	return fmt.Errorf("record command attempt: %w", err)
}
