package accesswait

import (
	"context"
	"fmt"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// grantRequestMatches 比较幂等重放的全部不可变授权字段，而不只依赖调用方摘要。
func grantRequestMatches(request AccessGrantRequest, existing storedb.OrchestrationAccessGrant) bool {
	grant, err := grantFromRow(existing)
	if err != nil {
		return false
	}
	return grantRequestIdentityMatches(request, grant) && grantRequestFenceMatches(request, grant) && grantRequestWindowMatches(request, grant)
}

// grantRequestIdentityMatches 比较授权主体、方向和幂等命名空间。
func grantRequestIdentityMatches(request AccessGrantRequest, grant AccessGrant) bool {
	return request.GrantID == grant.GrantID && request.CallerPrincipalRef == grant.CallerPrincipalRef && request.ScopeRef == grant.ScopeRef && request.CanRead == grant.CanRead && request.CanSend == grant.CanSend && request.IdempotencyNamespace == grant.IdempotencyNamespace && request.IdempotencyKeyDigest == grant.IdempotencyKeyDigest && request.RequestDigest == grant.RequestDigest
}

func grantRequestFenceMatches(request AccessGrantRequest, grant AccessGrant) bool {
	return equalFence(request.Source, grant.Source) && equalFence(request.Target, grant.Target)
}

func grantRequestWindowMatches(request AccessGrantRequest, grant AccessGrant) bool {
	return request.IssuedAt.Equal(grant.IssuedAt) && request.ExpiresAt.Equal(grant.ExpiresAt)
}

// revokeGrantInTx 在同一写事务中执行授权撤回及其 wait handle 收敛。
func (s *Store) revokeGrantInTx(ctx context.Context, q *storedb.Queries, request RevokeGrantRequest) (AccessGrant, error) {
	grant, err := loadGrantForRevoke(ctx, q, request.GrantID)
	if err != nil {
		return AccessGrant{}, err
	}
	if err := validateRevokeGrant(grant, request); err != nil {
		return AccessGrant{}, err
	}
	if err := persistGrantRevocation(ctx, q, request); err != nil {
		return AccessGrant{}, err
	}
	if err := revokePendingGrantWaits(ctx, q, request); err != nil {
		return AccessGrant{}, err
	}
	row, err := q.GetOrchestrationAccessGrant(ctx, storedb.GetOrchestrationAccessGrantParams{GrantID: request.GrantID})
	if err != nil {
		return AccessGrant{}, classifyDBError("read revoked access grant", err)
	}
	return grantFromRow(row)
}

func loadGrantForRevoke(ctx context.Context, q *storedb.Queries, grantID string) (AccessGrant, error) {
	row, err := q.GetOrchestrationAccessGrant(ctx, storedb.GetOrchestrationAccessGrantParams{GrantID: grantID})
	if err != nil {
		return AccessGrant{}, classifyDBError("load grant for revoke", err)
	}
	return grantFromRow(row)
}

// validateRevokeGrant 检查撤销 caller、operation digest、状态和 expected revision。
func validateRevokeGrant(grant AccessGrant, request RevokeGrantRequest) error {
	if grant.CallerPrincipalRef != request.CallerPrincipalRef || grant.ScopeRef != request.ScopeRef || grant.RequestDigest != request.RequestDigest {
		return ErrAccessDenied
	}
	switch grant.Status {
	case "revoked":
		return ErrAccessRevoked
	case "expired":
		return ErrAccessExpired
	}
	if grant.Revision != request.GrantRevision {
		return ErrAccessConflict
	}
	return nil
}

func persistGrantRevocation(ctx context.Context, q *storedb.Queries, request RevokeGrantRequest) error {
	affected, err := q.RevokeOrchestrationAccessGrant(ctx, storedb.RevokeOrchestrationAccessGrantParams{GrantID: request.GrantID, ExpectedRevision: request.GrantRevision})
	if err != nil {
		return classifyDBError("revoke access grant", err)
	}
	return requireAffected(ErrAccessConflict, "revoke access grant", affected)
}

// revokePendingGrantWaits 将撤权原子传播到仍 pending 的 wait 句柄。
func revokePendingGrantWaits(ctx context.Context, q *storedb.Queries, request RevokeGrantRequest) error {
	waits, err := q.ListOrchestrationEventWaitHandlesByGrant(ctx, storedb.ListOrchestrationEventWaitHandlesByGrantParams{GrantID: request.GrantID})
	if err != nil {
		return classifyDBError("list grant waits", err)
	}
	resolved := utcText(request.RevokedAt)
	for _, wait := range waits {
		if wait.Status != "pending" {
			continue
		}
		count, updateErr := q.RevokeOrchestrationEventWaitHandle(ctx, storedb.RevokeOrchestrationEventWaitHandleParams{ResolvedAt: resolved, WaitHandleID: wait.WaitHandleID, GrantID: request.GrantID, ExpectedRevision: wait.Revision})
		if updateErr != nil {
			return classifyDBError("revoke grant wait", updateErr)
		}
		if count != 1 {
			return fmt.Errorf("%w: revoke wait %s affected %d rows", ErrAccessConflict, wait.WaitHandleID, count)
		}
	}
	return nil
}

func validateGrantAuthorization(grant AccessGrant, request AccessRequest, now time.Time) error {
	if err := validateGrantIdentity(grant, request); err != nil {
		return err
	}
	if err := validateGrantLifecycle(grant, now); err != nil {
		return err
	}
	if err := validateGrantCapability(grant, request.Capability); err != nil {
		return err
	}
	return validateGrantFences(grant, request)
}

func validateGrantIdentity(grant AccessGrant, request AccessRequest) error {
	if grant.CallerPrincipalRef != request.CallerPrincipalRef || grant.ScopeRef != request.ScopeRef || grant.Source.AgentID != request.Source.AgentID || grant.Target.AgentID != request.Target.AgentID {
		return ErrAccessDenied
	}
	return nil
}

func validateGrantLifecycle(grant AccessGrant, now time.Time) error {
	if grant.Status == "revoked" {
		return ErrAccessRevoked
	}
	if grant.Status == "expired" || !now.Before(grant.ExpiresAt) {
		return ErrAccessExpired
	}
	if now.Before(grant.IssuedAt) {
		return ErrAccessDenied
	}
	return nil
}

func validateGrantCapability(grant AccessGrant, capability string) error {
	if capability == "read" && !grant.CanRead || capability == "send" && !grant.CanSend {
		return ErrAccessDenied
	}
	return nil
}

func validateGrantFences(grant AccessGrant, request AccessRequest) error {
	if !equalFence(request.Source, grant.Source) || !equalFence(request.Target, grant.Target) {
		return ErrAccessDenied
	}
	return nil
}
