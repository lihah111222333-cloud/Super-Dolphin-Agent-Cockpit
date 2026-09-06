package accesswait

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// CreateGrant 持久化不可变授权快照；同幂等键同摘要返回既有记录。
func (s *Store) CreateGrant(ctx context.Context, request AccessGrantRequest) (AccessGrant, error) {
	if err := s.ready(ctx); err != nil {
		return AccessGrant{}, err
	}
	if err := request.Validate(); err != nil {
		return AccessGrant{}, err
	}
	if err := enforceContextCaller(ctx, request.CallerPrincipalRef, request.ScopeRef); err != nil {
		return AccessGrant{}, err
	}
	if err := s.verifyRequestedFences(ctx, request.Source, request.Target); err != nil {
		return AccessGrant{}, err
	}
	var result AccessGrant
	err := s.withWriteTx(ctx, func(_ *storedb.Queries, txdb storedb.DBTX) error {
		var err error
		result, err = s.CreateGrantTx(ctx, txdb, request)
		return err
	})
	return result, err
}

// CreateGrantTx 在调用方事务内执行授权插入或幂等重放，不创建嵌套事务。
func (s *Store) CreateGrantTx(ctx context.Context, dbtx storedb.DBTX, request AccessGrantRequest) (AccessGrant, error) {
	if err := s.ready(ctx); err != nil {
		return AccessGrant{}, err
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return AccessGrant{}, err
	}
	if err := request.Validate(); err != nil {
		return AccessGrant{}, err
	}
	if err := enforceContextCaller(ctx, request.CallerPrincipalRef, request.ScopeRef); err != nil {
		return AccessGrant{}, err
	}
	q := storedb.New(dbtx)
	return s.createGrantInTx(ctx, q, dbtx, request)
}

func (s *Store) createGrantInTx(ctx context.Context, q *storedb.Queries, dbtx storedb.DBTX, request AccessGrantRequest) (AccessGrant, error) {
	var result AccessGrant
	if err := s.verifyRequestedFencesTx(ctx, dbtx, request.Source, request.Target); err != nil {
		return AccessGrant{}, err
	}
	row, err := q.CreateOrchestrationAccessGrant(ctx, storedb.CreateOrchestrationAccessGrantParams{
		GrantID: request.GrantID, CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
		SourceAgentID: request.Source.AgentID, TargetAgentID: request.Target.AgentID,
		SourceReservationID: request.Source.ReservationID, SourceReservationGeneration: request.Source.ReservationGeneration,
		SourceIncarnationID: request.Source.IncarnationID, SourceIncarnationGeneration: request.Source.IncarnationGeneration,
		SourceBindingVersion: request.Source.BindingVersion, SourceBindingDigest: request.Source.BindingDigest, SourceFenceRef: request.Source.SourceFenceRef,
		TargetReservationID: request.Target.ReservationID, TargetReservationGeneration: request.Target.ReservationGeneration,
		TargetIncarnationID: request.Target.IncarnationID, TargetIncarnationGeneration: request.Target.IncarnationGeneration,
		TargetBindingVersion: request.Target.BindingVersion, TargetBindingDigest: request.Target.BindingDigest, TargetFenceRef: request.Target.SourceFenceRef,
		CanRead: boolInt(request.CanRead), CanSend: boolInt(request.CanSend), IdempotencyNamespace: request.IdempotencyNamespace,
		IdempotencyKeyDigest: request.IdempotencyKeyDigest, RequestDigest: request.RequestDigest,
		IssuedAt: utcText(request.IssuedAt), ExpiresAt: utcText(request.ExpiresAt),
	})
	if errors.Is(err, sql.ErrNoRows) {
		existing, getErr := q.GetOrchestrationAccessGrantByIdempotencyKey(ctx, storedb.GetOrchestrationAccessGrantByIdempotencyKeyParams{IdempotencyNamespace: request.IdempotencyNamespace, IdempotencyKeyDigest: request.IdempotencyKeyDigest})
		if getErr != nil {
			return AccessGrant{}, classifyDBError("replay access grant", getErr)
		}
		if !grantRequestMatches(request, existing) {
			return AccessGrant{}, ErrAccessConflict
		}
		result, getErr = grantFromRow(existing)
		return result, getErr
	}
	if err != nil {
		return AccessGrant{}, classifyDBError("create access grant", err)
	}
	result, err = grantFromRow(row)
	return result, err
}

// GetGrant 读取授权快照；不会因句柄或 agent_id 猜测授权。
func (s *Store) GetGrant(ctx context.Context, grantID string) (AccessGrant, error) {
	if err := validateText(ErrInvalidAccess, "grant_id", grantID, maxID, false); err != nil {
		return AccessGrant{}, err
	}
	if err := s.ready(ctx); err != nil {
		return AccessGrant{}, err
	}
	return s.getGrantWithQueries(ctx, s.queries, grantID)
}

// GetGrantTx 在调用方事务内读取授权快照，不开启或提交事务。
func (s *Store) GetGrantTx(ctx context.Context, dbtx storedb.DBTX, grantID string) (AccessGrant, error) {
	if err := s.ready(ctx); err != nil {
		return AccessGrant{}, err
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return AccessGrant{}, err
	}
	if err := validateText(ErrInvalidAccess, "grant_id", grantID, maxID, false); err != nil {
		return AccessGrant{}, err
	}
	return s.getGrantWithQueries(ctx, storedb.New(dbtx), grantID)
}

func (s *Store) getGrantWithQueries(ctx context.Context, q *storedb.Queries, grantID string) (AccessGrant, error) {
	row, err := q.GetOrchestrationAccessGrant(ctx, storedb.GetOrchestrationAccessGrantParams{GrantID: grantID})
	if err != nil {
		return AccessGrant{}, classifyDBError("get access grant", err)
	}
	return grantFromRow(row)
}

// GetGrantByKey 查询幂等键对应的授权，不把 key 解释为权限。
func (s *Store) GetGrantByKey(ctx context.Context, namespace, digest string) (AccessGrant, error) {
	if err := validateText(ErrInvalidAccess, "idempotency_namespace", namespace, maxNamespace, false); err != nil {
		return AccessGrant{}, err
	}
	if err := validateDigest(ErrInvalidAccess, "idempotency_key_digest", digest, true); err != nil {
		return AccessGrant{}, err
	}
	if err := s.ready(ctx); err != nil {
		return AccessGrant{}, err
	}
	row, err := s.queries.GetOrchestrationAccessGrantByIdempotencyKey(ctx, storedb.GetOrchestrationAccessGrantByIdempotencyKeyParams{IdempotencyNamespace: namespace, IdempotencyKeyDigest: digest})
	if err != nil {
		return AccessGrant{}, classifyDBError("get access grant by key", err)
	}
	return grantFromRow(row)
}

// RevokeGrant 以 expected revision 撤回授权，并同步关闭其 pending wait handles。
func (s *Store) RevokeGrant(ctx context.Context, request RevokeGrantRequest) (AccessGrant, error) {
	if err := request.Validate(); err != nil {
		return AccessGrant{}, err
	}
	if err := s.ready(ctx); err != nil {
		return AccessGrant{}, err
	}
	if err := enforceContextCaller(ctx, request.CallerPrincipalRef, request.ScopeRef); err != nil {
		return AccessGrant{}, err
	}
	// The database owns the lifecycle timestamp. The caller-provided value is
	// an input fence only; never let it forge an audit time or future-dated wait.
	request.RevokedAt = s.now().UTC()
	var result AccessGrant
	err := s.withWriteTx(ctx, func(_ *storedb.Queries, txdb storedb.DBTX) error {
		var err error
		result, err = s.RevokeGrantTx(ctx, txdb, request)
		return err
	})
	return result, err
}

// RevokeGrantTx 在调用方事务内撤销授权并收敛 pending wait，不创建嵌套事务。
func (s *Store) RevokeGrantTx(ctx context.Context, dbtx storedb.DBTX, request RevokeGrantRequest) (AccessGrant, error) {
	if err := s.ready(ctx); err != nil {
		return AccessGrant{}, err
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return AccessGrant{}, err
	}
	if err := request.Validate(); err != nil {
		return AccessGrant{}, err
	}
	if err := enforceContextCaller(ctx, request.CallerPrincipalRef, request.ScopeRef); err != nil {
		return AccessGrant{}, err
	}
	request.RevokedAt = s.now().UTC()
	return s.revokeGrantInTx(ctx, storedb.New(dbtx), request)
}

// Authorize 在每次受保护操作前重读 grant 与两端当前 identity fence。
func (s *Store) Authorize(ctx context.Context, request AccessRequest) (Authorization, error) {
	if err := request.Validate(); err != nil {
		return Authorization{}, err
	}
	if err := enforceContextCaller(ctx, request.CallerPrincipalRef, request.ScopeRef); err != nil {
		return Authorization{}, err
	}
	grant, err := s.GetGrant(ctx, request.GrantID)
	if err != nil {
		return Authorization{}, err
	}
	now := s.now().UTC()
	if err := validateGrantAuthorization(grant, request, now); err != nil {
		return Authorization{}, err
	}
	if err := s.verifyRequestedFences(ctx, grant.Source, grant.Target); err != nil {
		return Authorization{}, err
	}
	return Authorization{RequestID: request.RequestID, GrantID: grant.GrantID, Capability: request.Capability, GrantRevision: grant.Revision, AuthorizedAt: now, ExpiresAt: grant.ExpiresAt, Grant: grant}, nil
}

// enforceContextCaller binds caller assertions to the server-authenticated
// principal when one is present. Legacy/internal callers without a transport
// principal still rely on the durable grant match; request strings never mint
// or elevate authority on their own.
func enforceContextCaller(ctx context.Context, principalRef, scopeRef string) error {
	principal, err := common.RequireCallerPrincipal(ctx)
	if errors.Is(err, common.ErrMissingCallerPrincipal) {
		return nil
	}
	if err != nil || principal.PrincipalRef != principalRef || principal.ScopeRef != scopeRef || isOperatorCaller(principal) {
		return ErrAccessDenied
	}
	return nil
}

func isOperatorCaller(principal common.CallerPrincipal) bool {
	profile := strings.ToLower(principal.Profile)
	ref := strings.ToLower(principal.PrincipalRef)
	return profile == "operator" || profile == "standalone_http_local_v1" || strings.HasPrefix(profile, "operator/") || strings.HasPrefix(ref, "operator/")
}

// verifyRequestedFences 重新读取两端当前 active binding，拒绝旧 incarnation。
func (s *Store) verifyRequestedFences(ctx context.Context, source, target FenceSnapshot) error {
	if source.IncarnationID == "" || target.IncarnationID == "" {
		return ErrAccessDenied
	}
	for _, snapshot := range []FenceSnapshot{source, target} {
		binding, err := s.identities.GetCurrent(ctx, snapshot.AgentID)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return errors.Join(ErrAccessDenied, err)
		}
		if binding.State != "active" || !equalFence(snapshot, fenceFromBinding(binding)) {
			return ErrAccessDenied
		}
	}
	return nil
}

func fenceFromBinding(binding identity.CurrentBinding) FenceSnapshot {
	return FenceSnapshot{AgentID: binding.AgentID, ReservationID: binding.ReservationID, ReservationGeneration: binding.ReservationGeneration, IncarnationID: binding.IncarnationID, IncarnationGeneration: binding.IncarnationGeneration, BindingVersion: binding.BindingVersion, BindingDigest: binding.BindingDigest, SourceFenceRef: binding.SourceFenceRef}
}

// equalFence 比较授权相关的完整身份 fence，不比较展示名称或路径。
func equalFence(left, right FenceSnapshot) bool {
	return left.AgentID == right.AgentID && left.ReservationID == right.ReservationID && left.ReservationGeneration == right.ReservationGeneration && left.IncarnationID == right.IncarnationID && left.IncarnationGeneration == right.IncarnationGeneration && left.BindingVersion == right.BindingVersion && left.BindingDigest == right.BindingDigest && left.SourceFenceRef == right.SourceFenceRef
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
