package accesswait

import (
	"context"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// AuthorizeTx 在调用方已持有的真实事务中读取 grant 并复核当前 identity fence。
// 这是 additive candidate seam；Authorize 保持既有 API 与授权判定语义。
func (s *Store) AuthorizeTx(ctx context.Context, dbtx storedb.DBTX, request AccessRequest) (Authorization, error) {
	if err := request.Validate(); err != nil {
		return Authorization{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Authorization{}, err
	}
	if err := enforceContextCaller(ctx, request.CallerPrincipalRef, request.ScopeRef); err != nil {
		return Authorization{}, err
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return Authorization{}, err
	}
	row, err := storedb.New(dbtx).GetOrchestrationAccessGrant(ctx, storedb.GetOrchestrationAccessGrantParams{GrantID: request.GrantID})
	if err != nil {
		return Authorization{}, classifyDBError("get access grant in transaction", err)
	}
	grant, err := grantFromRow(row)
	if err != nil {
		return Authorization{}, err
	}
	now := s.now().UTC()
	if err := validateGrantAuthorization(grant, request, now); err != nil {
		return Authorization{}, err
	}
	if err := s.verifyRequestedFencesTx(ctx, dbtx, grant.Source, grant.Target); err != nil {
		return Authorization{}, err
	}
	return Authorization{RequestID: request.RequestID, GrantID: grant.GrantID, Capability: request.Capability, GrantRevision: grant.Revision, AuthorizedAt: now, ExpiresAt: grant.ExpiresAt, Grant: grant}, nil
}
