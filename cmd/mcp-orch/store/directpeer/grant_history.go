package directpeer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

// grantByKeyWithQueries 在同一事务快照中核对可变当前记录与不可变授权事件，拒绝旧key生成新授权。
func grantByKeyWithQueries(ctx context.Context, q *storedb.Queries, workspace, key, digest string) (Grant, bool, error) {
	row, err := q.GetDirectPeerGrantByKey(ctx, storedb.GetDirectPeerGrantByKeyParams{WorkspaceRoot: workspace, IdempotencyKey: key})
	if err == nil {
		if row.RequestDigest != digest {
			return Grant{}, true, ErrIdempotencyConflict
		}
		return grantFromRow(row), true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Grant{}, false, err
	}
	event, err := q.GetDirectPeerGrantEventByKey(ctx, storedb.GetDirectPeerGrantEventByKeyParams{WorkspaceRoot: workspace, PayloadJson: key})
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, false, nil
	}
	if err != nil {
		return Grant{}, false, err
	}
	return historicalGrantByKey(ctx, q, event, workspace, key, digest)
}

// historicalGrantByKey 以历史操作摘要校验重放，只返回当前授权状态。
func historicalGrantByKey(ctx context.Context, q *storedb.Queries, event storedb.GetDirectPeerGrantEventByKeyRow, workspace, key, digest string) (Grant, bool, error) {
	var original Grant
	if err := json.Unmarshal([]byte(event.PayloadJson), &original); err != nil {
		return Grant{}, true, err
	}
	if err := validateHistoricalGrant(original, workspace, key); err != nil {
		return Grant{}, true, err
	}
	action := "grant"
	if event.EventType == eventGrantRevoked {
		action = "revoke"
	}
	if grantRequestDigest(action, workspace, original.SenderAgentID, original.ReceiverAgentID, original.CanSend, original.CanRead) != digest {
		return Grant{}, true, ErrIdempotencyConflict
	}
	current, err := q.GetDirectPeerGrant(ctx, storedb.GetDirectPeerGrantParams{WorkspaceRoot: workspace, SenderAgentID: original.SenderAgentID, ReceiverAgentID: original.ReceiverAgentID})
	if err != nil {
		return Grant{}, true, err
	}
	if current.Revision < original.Revision {
		return Grant{}, true, errors.New("direct peer grant history conflicts with current revision")
	}
	return grantFromRow(current), true, nil
}

// validateHistoricalGrant 拒绝不完整历史记录，历史事件只证明操作key，绝不作为当前授权owner。
func validateHistoricalGrant(grant Grant, workspace, key string) error {
	if grant.WorkspaceRoot != workspace || grant.IdempotencyKey != key || grant.GrantID == "" || grant.SenderAgentID == "" || grant.ReceiverAgentID == "" || grant.Revision < 1 {
		return fmt.Errorf("direct peer grant history identity is invalid")
	}
	return nil
}

// resolveGrantWriteError 事务冲突后只读确认同key结果；不同意图不得重试覆盖活跃授权。
func (s *Store) resolveGrantWriteError(ctx context.Context, workspace, key, digest string, cause error) (Grant, error) {
	if existing, found, err := s.grantByKey(ctx, workspace, key, digest); found || err != nil {
		return existing, err
	}
	if platformdb.IsConflict(cause) || errors.Is(cause, sql.ErrNoRows) {
		return Grant{}, ErrIdempotencyConflict
	}
	return Grant{}, cause
}
