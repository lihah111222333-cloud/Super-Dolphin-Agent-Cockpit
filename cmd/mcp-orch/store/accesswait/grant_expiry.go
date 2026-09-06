package accesswait

import (
	"context"
	"errors"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// ExpireGrant 由显式安全时间 owner 以 expected revision 标记已到期授权。
// 普通调用方不能用该方法把尚未到期或已撤销 grant 强行改写。
func (s *Store) ExpireGrant(ctx context.Context, request ExpireGrantRequest) (AccessGrant, error) {
	if err := request.Validate(); err != nil {
		return AccessGrant{}, err
	}
	var result AccessGrant
	err := s.withWrite(ctx, func(q *storedb.Queries) error {
		grant, err := loadGrantForExpiry(ctx, q, request.GrantID)
		if err != nil {
			return err
		}
		if grant.Status == "expired" && grant.Revision == request.ExpectedRevision {
			result = grant
			return nil
		}
		if err := validateExpiryCandidate(grant, request, s.now().UTC()); err != nil {
			return err
		}
		if err := markGrantExpired(ctx, q, request); err != nil {
			return err
		}
		if err := expirePendingGrantWaits(ctx, q, request); err != nil {
			return err
		}
		row, err := q.GetOrchestrationAccessGrant(ctx, storedb.GetOrchestrationAccessGrantParams{GrantID: request.GrantID})
		if err != nil {
			return classifyDBError("read expired access grant", err)
		}
		result, err = grantFromRow(row)
		return err
	})
	return result, err
}

func loadGrantForExpiry(ctx context.Context, q *storedb.Queries, grantID string) (AccessGrant, error) {
	row, err := q.GetOrchestrationAccessGrant(ctx, storedb.GetOrchestrationAccessGrantParams{GrantID: grantID})
	if err != nil {
		return AccessGrant{}, classifyDBError("load grant for expiry", err)
	}
	return grantFromRow(row)
}

// validateExpiryCandidate 确认 grant 已到期、revision 匹配且时间来自可信 clock。
func validateExpiryCandidate(grant AccessGrant, request ExpireGrantRequest, now time.Time) error {
	if grant.Status == "revoked" {
		return ErrAccessRevoked
	}
	if grant.Status == "expired" {
		return ErrAccessExpired
	}
	if grant.Revision != request.ExpectedRevision || request.ExpiredAt.Before(grant.ExpiresAt) || request.ExpiredAt.After(now) {
		return ErrAccessConflict
	}
	return nil
}

func markGrantExpired(ctx context.Context, q *storedb.Queries, request ExpireGrantRequest) error {
	affected, err := q.ExpireOrchestrationAccessGrant(ctx, storedb.ExpireOrchestrationAccessGrantParams{GrantID: request.GrantID, ExpectedRevision: request.ExpectedRevision, NowAt: utcText(request.ExpiredAt)})
	if err != nil {
		return classifyDBError("expire access grant", err)
	}
	return requireAffected(ErrAccessConflict, "expire access grant", affected)
}

// expirePendingGrantWaits 将到期事实传播到该 grant 下仍 pending 的 wait。
func expirePendingGrantWaits(ctx context.Context, q *storedb.Queries, request ExpireGrantRequest) error {
	waits, err := q.ListOrchestrationEventWaitHandlesByGrant(ctx, storedb.ListOrchestrationEventWaitHandlesByGrantParams{GrantID: request.GrantID})
	if err != nil {
		return classifyDBError("list expiry waits", err)
	}
	for _, wait := range waits {
		if wait.Status != "pending" {
			continue
		}
		count, updateErr := q.ExpireOrchestrationEventWaitHandleByGrant(ctx, storedb.ExpireOrchestrationEventWaitHandleByGrantParams{ResolvedAt: utcText(request.ExpiredAt), WaitHandleID: wait.WaitHandleID, GrantID: request.GrantID, ExpectedRevision: wait.Revision})
		if updateErr != nil {
			return classifyDBError("expire grant wait", updateErr)
		}
		if count != 1 {
			return errors.Join(ErrAccessConflict, errors.New("expiry wait CAS lost"))
		}
	}
	return nil
}
