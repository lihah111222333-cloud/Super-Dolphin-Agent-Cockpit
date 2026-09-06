package orchestrationcommand

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// SendProfileStore 在一个 IMMEDIATE transaction 中拥有 base intent 与 send_message_v1 profile 的原子创建。
type SendProfileStore struct {
	db       *sql.DB
	queries  *storedb.Queries
	verifier SendProfileDigestVerifier
	bindings SendProfileCurrentBindingReader
}

// SendProfileCurrentBindingReader 在调用方 IMMEDIATE transaction 内读取 ID owner 的最新 binding。
type SendProfileCurrentBindingReader interface {
	GetSendProfileCurrentBinding(context.Context, storedb.DBTX, string) (ActiveBindingSelector, error)
}

// NewSendProfileStore 创建专用 profile owner；无 digest verifier 时禁止构造。
func NewSendProfileStore(db *sql.DB, verifier SendProfileDigestVerifier, bindings SendProfileCurrentBindingReader) (*SendProfileStore, error) {
	if db == nil {
		return nil, errors.New("send profile store database is required")
	}
	if isNilProfileInterface(verifier) {
		return nil, errors.New("send profile digest verifier is required")
	}
	if isNilProfileInterface(bindings) {
		return nil, errors.New("send profile current binding reader is required")
	}
	return &SendProfileStore{db: db, queries: storedb.New(db), verifier: verifier, bindings: bindings}, nil
}

// Reserve 原子创建完整 send profile，或按原 key 返回首个完全相同的组合；不写 lifecycle pending。
func (s *SendProfileStore) Reserve(ctx context.Context, intent Intent, profile SendMessageProfile) (CompleteIntentRecord, error) {
	if err := s.validateRequest(ctx, intent, profile); err != nil {
		return CompleteIntentRecord{}, err
	}
	var result CompleteIntentRecord
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, txdb storedb.DBTX) error {
		resolved, err := reserveSendProfileTx(ctx, q, txdb, s.bindings, intent, profile)
		if err == nil {
			result = resolved
		}
		return err
	})
	return result, err
}

// Get 按 canonical command ID 读取完整 profile；base-only intent 明确返回 ErrIncompleteIntent。
func (s *SendProfileStore) Get(ctx context.Context, commandID string) (CompleteIntentRecord, error) {
	if err := validateText("command_id", commandID, 128); err != nil {
		return CompleteIntentRecord{}, err
	}
	if err := s.ready(ctx); err != nil {
		return CompleteIntentRecord{}, err
	}
	commandRow, err := s.queries.GetOrchestrationCommandIntent(ctx, storedb.GetOrchestrationCommandIntentParams{CommandID: commandID})
	if err != nil {
		return CompleteIntentRecord{}, classifyReadError(err)
	}
	return s.readCompleteIntent(ctx, commandRow)
}

// GetByKey 只按原幂等 namespace/digest 读取完整 profile，不补建 base-only 记录。
func (s *SendProfileStore) GetByKey(ctx context.Context, key Key) (CompleteIntentRecord, error) {
	if err := key.Validate(); err != nil {
		return CompleteIntentRecord{}, err
	}
	if err := s.ready(ctx); err != nil {
		return CompleteIntentRecord{}, err
	}
	commandRow, err := s.queries.GetOrchestrationCommandIntentByKey(ctx, storedb.GetOrchestrationCommandIntentByKeyParams{
		IdempotencyNamespace: key.Namespace, IdempotencyKeyDigest: key.Digest,
	})
	if err != nil {
		return CompleteIntentRecord{}, classifyReadError(err)
	}
	return s.readCompleteIntent(ctx, commandRow)
}

func (s *SendProfileStore) validateRequest(ctx context.Context, intent Intent, profile SendMessageProfile) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	if err := profile.Validate(intent); err != nil {
		return err
	}
	if err := s.verifier.VerifySendProfileDigest(ctx, intent, profile); err != nil {
		return fmt.Errorf("verify send profile digest: %w", err)
	}
	return nil
}

// ready 校验profile store及其digest/binding依赖仍完整。
func (s *SendProfileStore) ready(ctx context.Context) error {
	if s == nil || s.db == nil || s.queries == nil || isNilProfileInterface(s.verifier) || isNilProfileInterface(s.bindings) {
		return errors.New("send profile store is not initialized")
	}
	if ctx == nil {
		return ErrContextRequired
	}
	return ctx.Err()
}

// reserveSendProfileTx 在单一写事务内协调canonical replay或fresh profile+CONTENT fence创建。
func reserveSendProfileTx(ctx context.Context, q *storedb.Queries, txdb storedb.DBTX, bindings SendProfileCurrentBindingReader, intent Intent, profile SendMessageProfile) (CompleteIntentRecord, error) {
	existing, err := q.GetOrchestrationCommandIntentByKey(ctx, storedb.GetOrchestrationCommandIntentByKeyParams{
		IdempotencyNamespace: intent.IdempotencyNamespace, IdempotencyKeyDigest: intent.IdempotencyKeyDigest,
	})
	if err == nil {
		return replaySendProfile(ctx, q, intent, profile, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CompleteIntentRecord{}, classifyReadError(err)
	}
	commandRow, err := q.CreateOrchestrationCommandIntent(ctx, intentParams(intent))
	if err != nil {
		return CompleteIntentRecord{}, classifyInsertError(err)
	}
	if err := verifyCurrentProfileBindings(ctx, txdb, bindings, profile); err != nil {
		return CompleteIntentRecord{}, err
	}
	profileRow, err := q.CreateOrchestrationCommandIntentProfile(ctx, sendProfileParams(commandRow.CommandID, profile))
	if err != nil {
		return CompleteIntentRecord{}, classifyProfileInsertError(err)
	}
	complete, err := completeIntentFromRows(commandRow, profileRow)
	if err != nil {
		return CompleteIntentRecord{}, err
	}
	if err := requireCommandContentRelation(ctx, q, complete); err != nil {
		return CompleteIntentRecord{}, err
	}
	return complete, nil
}

func verifyCurrentProfileBindings(ctx context.Context, txdb storedb.DBTX, reader SendProfileCurrentBindingReader, profile SendMessageProfile) error {
	for _, selector := range []ActiveBindingSelector{profile.Source, profile.Target} {
		current, err := reader.GetSendProfileCurrentBinding(ctx, txdb, selector.AgentID)
		if err != nil {
			return fmt.Errorf("read current command profile binding: %w", err)
		}
		if current.State != "active" {
			return ErrBindingNotActive
		}
		if current != selector {
			return ErrBindingFence
		}
	}
	return nil
}

// replaySendProfile 只返回原始完整profile，并拒绝legacy或缺CONTENT关系记录。
func replaySendProfile(ctx context.Context, q *storedb.Queries, candidate Intent, profile SendMessageProfile, row storedb.OrchestrationCommandIntent) (CompleteIntentRecord, error) {
	existing, err := recordFromRow(row)
	if err != nil {
		return CompleteIntentRecord{}, err
	}
	matched, err := matchExistingIntent(candidate, existing)
	if err != nil {
		return CompleteIntentRecord{}, err
	}
	profileRow, err := q.GetOrchestrationCommandIntentProfile(ctx, storedb.GetOrchestrationCommandIntentProfileParams{CommandID: matched.Intent.CommandID})
	if errors.Is(err, sql.ErrNoRows) {
		return CompleteIntentRecord{}, ErrIncompleteIntent
	}
	if err != nil {
		return CompleteIntentRecord{}, fmt.Errorf("read command intent profile: %w", err)
	}
	complete, err := completeIntentFromRows(row, profileRow)
	if err != nil {
		return CompleteIntentRecord{}, err
	}
	if complete.Profile != profile {
		return CompleteIntentRecord{}, ErrProfileConflict
	}
	if err := requireCommandContentRelation(ctx, q, complete); err != nil {
		return CompleteIntentRecord{}, err
	}
	return complete, nil
}

// readCompleteIntent 重验digest以及完整CONTENT admission relation后才返回profile。
func (s *SendProfileStore) readCompleteIntent(ctx context.Context, commandRow storedb.OrchestrationCommandIntent) (CompleteIntentRecord, error) {
	profileRow, err := s.queries.GetOrchestrationCommandIntentProfile(ctx, storedb.GetOrchestrationCommandIntentProfileParams{CommandID: commandRow.CommandID})
	if errors.Is(err, sql.ErrNoRows) {
		return CompleteIntentRecord{}, ErrIncompleteIntent
	}
	if err != nil {
		return CompleteIntentRecord{}, fmt.Errorf("read command intent profile: %w", err)
	}
	complete, err := completeIntentFromRows(commandRow, profileRow)
	if err != nil {
		return CompleteIntentRecord{}, err
	}
	if err := s.verifier.VerifySendProfileDigest(ctx, complete.Command.Intent, complete.Profile); err != nil {
		return CompleteIntentRecord{}, fmt.Errorf("verify stored send profile digest: %w", err)
	}
	if err := requireCommandContentRelation(ctx, s.queries, complete); err != nil {
		return CompleteIntentRecord{}, err
	}
	return complete, nil
}

// requireCommandContentRelation 要求profile、held fence、live ref、active object和promoted reservation完整闭合。
func requireCommandContentRelation(ctx context.Context, q *storedb.Queries, complete CompleteIntentRecord) error {
	if err := rejectLegacyCommandContent(ctx, q, complete); err != nil {
		return err
	}
	valid, err := q.CheckCommandContentAdmissionRelation(ctx, storedb.CheckCommandContentAdmissionRelationParams{
		CommandID: complete.Command.Intent.CommandID, CommandProfileDigest: complete.Profile.CompleteIntentDigest,
		SourceFenceRef: complete.Profile.Source.FenceRef, TargetFenceRef: complete.Profile.Target.FenceRef,
		CanonicalRef:       complete.Command.Intent.RequestContentRef,
		CallerPrincipalRef: complete.Command.Intent.CallerPrincipalRef, ScopeRef: complete.Command.Intent.ScopeRef,
		SourceAgentID:          complete.Profile.Source.AgentID,
		AuthorizationDecidedAt: profileTime(complete.Profile.Authorization.DecidedAt),
		AuthorizationExpiresAt: profileTime(complete.Profile.Authorization.ExpiresAt),
		CallerAuthExpiresAt:    profileTime(complete.Profile.Caller.ExpiresAt),
	})
	if err != nil {
		return fmt.Errorf("read command content admission relation: %w", err)
	}
	if valid != 1 {
		return ErrIncompleteIntent
	}
	return nil
}

// rejectLegacyCommandContent 拒绝migration132隔离的旧profile，且绝不补建ref或fence。
func rejectLegacyCommandContent(ctx context.Context, q *storedb.Queries, complete CompleteIntentRecord) error {
	legacy, err := q.GetLegacyCommandContentQuarantine(ctx, storedb.GetLegacyCommandContentQuarantineParams{
		CommandID: complete.Command.Intent.CommandID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read legacy command content quarantine: %w", err)
	}
	if legacy.CommandID != complete.Command.Intent.CommandID ||
		legacy.CompleteIntentDigest != complete.Profile.CompleteIntentDigest ||
		legacy.Reason != "legacy_missing_content_owner_ref" || legacy.SourceSchemaVersion != 131 ||
		legacy.SchemaVersion != 1 {
		return ErrProfileRelation
	}
	if _, err := parseProfileTimestamp(legacy.CreatedAt); err != nil {
		return ErrProfileRelation
	}
	return errors.Join(ErrIncompleteIntent, ErrLegacyContentProofMissing)
}

// completeIntentFromRows 严格映射一对一base/profile行并复验profile字段关系。
func completeIntentFromRows(commandRow storedb.OrchestrationCommandIntent, profileRow storedb.OrchestrationCommandIntentProfile) (CompleteIntentRecord, error) {
	command, err := recordFromRow(commandRow)
	if err != nil {
		return CompleteIntentRecord{}, err
	}
	profile, recordedAt, err := sendProfileFromRow(profileRow)
	if err != nil {
		return CompleteIntentRecord{}, err
	}
	if profileRow.CommandID != command.Intent.CommandID || profileRow.SchemaVersion != 1 {
		return CompleteIntentRecord{}, ErrProfileRelation
	}
	if err := profile.Validate(command.Intent); err != nil {
		return CompleteIntentRecord{}, err
	}
	return CompleteIntentRecord{Command: command, Profile: profile, SchemaVersion: profileRow.SchemaVersion, RecordedAt: recordedAt}, nil
}

func classifyProfileInsertError(err error) error {
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT {
		return errors.Join(ErrProfileRelation, err)
	}
	return fmt.Errorf("record command intent profile: %w", err)
}

func isNilProfileInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func profileTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
