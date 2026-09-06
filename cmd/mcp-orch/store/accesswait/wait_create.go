package accesswait

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// CreateWait 建立一个与授权快照绑定的 durable wait handle。
// 句柄只保存 selector/cursor 和事件引用，不保存正文，也不创建 command/outbox。
func (s *Store) CreateWait(ctx context.Context, request WaitRequest) (WaitHandle, error) {
	if err := s.ready(ctx); err != nil {
		return WaitHandle{}, err
	}
	if err := request.Validate(); err != nil {
		return WaitHandle{}, err
	}
	var result WaitHandle
	err := s.withWriteTx(ctx, func(_ *sqlc.Queries, txdb sqlc.DBTX) error {
		var err error
		result, err = s.CreateWaitTx(ctx, txdb, request)
		return err
	})
	return result, err
}

// CreateWaitTx 在调用方事务内创建或重放 durable wait，不创建嵌套事务。
func (s *Store) CreateWaitTx(ctx context.Context, dbtx sqlc.DBTX, request WaitRequest) (WaitHandle, error) {
	if err := s.ready(ctx); err != nil {
		return WaitHandle{}, err
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return WaitHandle{}, err
	}
	if err := request.Validate(); err != nil {
		return WaitHandle{}, err
	}
	q := sqlc.New(dbtx)
	grant, err := s.prepareWaitGrantTx(ctx, q, request)
	if err != nil {
		return WaitHandle{}, err
	}
	selectorJSON, eventTypesJSON, selectorDigest, err := selectorJSON(request.Selector)
	if err != nil {
		return WaitHandle{}, err
	}
	request, err = s.prepareWaitRequestTx(ctx, dbtx, q, request, grant, selectorDigest)
	if err != nil {
		return WaitHandle{}, err
	}
	return s.createWaitRow(ctx, q, request, grant, selectorJSON, eventTypesJSON, selectorDigest)
}

// prepareWaitGrantTx 先固定授权快照及其 source 绑定，后续 wait 写入事务会再次复核该快照。
func (s *Store) prepareWaitGrantTx(ctx context.Context, q *sqlc.Queries, request WaitRequest) (AccessGrant, error) {
	if request.DeadlineAt.Sub(s.now().UTC()) > maxWaitDuration {
		return AccessGrant{}, invalidWait("wait deadline exceeds 60 second bound")
	}
	row, err := q.GetOrchestrationAccessGrant(ctx, sqlc.GetOrchestrationAccessGrantParams{GrantID: request.GrantID})
	if err != nil {
		return AccessGrant{}, classifyDBError("get access grant for wait", err)
	}
	grant, err := grantFromRow(row)
	if err != nil {
		return AccessGrant{}, err
	}
	if !waitGrantMatchesRequest(request, grant) {
		return AccessGrant{}, ErrAccessDenied
	}
	if err := selectorSourceError(request.Selector, grant); err != nil {
		return AccessGrant{}, err
	}
	if request.DeadlineAt.After(grant.ExpiresAt) {
		return AccessGrant{}, ErrAccessExpired
	}
	return grant, nil
}

func waitGrantMatchesRequest(request WaitRequest, grant AccessGrant) bool {
	return request.GrantRevision == grant.Revision &&
		request.CallerPrincipalRef == grant.CallerPrincipalRef &&
		request.ScopeRef == grant.ScopeRef &&
		request.Selector.TargetAgentID == grant.Target.AgentID &&
		selectorSourceMatchesGrant(request.Selector, grant)
}

// prepareWaitRequestTx 维持授权/fence、精确重放、source admission、句柄生成的顺序，且全程复用调用方事务。
func (s *Store) prepareWaitRequestTx(ctx context.Context, dbtx sqlc.DBTX, q *sqlc.Queries, request WaitRequest, grant AccessGrant, selectorDigest string) (WaitRequest, error) {
	if err := s.validateWaitRequestTx(ctx, dbtx, request, grant); err != nil {
		return WaitRequest{}, err
	}
	replayed, ok, err := s.replayWaitRequestTx(ctx, q, request, grant, selectorDigest)
	if err != nil {
		return WaitRequest{}, err
	}
	if ok {
		return replayed, nil
	}
	if err := s.ensureSourceReadableTx(ctx, dbtx, request.Selector, request.AfterSequence); err != nil {
		return WaitRequest{}, err
	}
	if request.WaitHandleID == "" {
		request.WaitHandleID, err = newOpaqueID("wait")
		if err != nil {
			return WaitRequest{}, err
		}
		request.waitHandleIDGenerated = true
	}
	return request, nil
}

// validateWaitRequestTx 在写事务内校验 read capability 与两端 identity fence。
func (s *Store) validateWaitRequestTx(ctx context.Context, dbtx sqlc.DBTX, request WaitRequest, grant AccessGrant) error {
	access := AccessRequest{
		RequestID: request.WaitHandleID, GrantID: request.GrantID,
		CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
		Capability: "read", Source: grant.Source, Target: grant.Target,
		RequestDigest: request.RequestDigest, RequestedAt: s.now().UTC(),
	}
	if access.RequestID == "" {
		access.RequestID = "wait:replay-check"
	}
	if err := validateGrantAuthorization(grant, access, access.RequestedAt); err != nil {
		return err
	}
	if err := s.verifyRequestedFencesTx(ctx, dbtx, grant.Source, grant.Target); err != nil {
		return err
	}
	return nil
}

// replayWaitRequestTx 查询幂等键并逐字段比对，命中时返回持久化句柄而不触发 source admission。
func (s *Store) replayWaitRequestTx(ctx context.Context, q *sqlc.Queries, request WaitRequest, grant AccessGrant, selectorDigest string) (WaitRequest, bool, error) {
	row, err := q.GetOrchestrationEventWaitHandleByIdempotencyKey(ctx, sqlc.GetOrchestrationEventWaitHandleByIdempotencyKeyParams{
		IdempotencyNamespace: request.IdempotencyNamespace, IdempotencyKeyDigest: request.IdempotencyKeyDigest,
	})
	if err == nil {
		prior, mapErr := waitFromRow(row)
		if mapErr != nil {
			return WaitRequest{}, false, mapErr
		}
		replayRequest := request
		replayRequest.waitHandleIDGenerated = request.WaitHandleID == ""
		if !waitRequestMatchesHandle(replayRequest, prior, selectorDigest) || prior.SourceAgentID != grant.Source.AgentID || !equalFence(prior.Target, grant.Target) || !selectorSourceMatchesGrant(prior.Selector, grant) {
			return WaitRequest{}, false, ErrAccessConflict
		}
		return waitRequestFromHandle(prior), true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WaitRequest{}, false, classifyDBError("check replay event wait handle", err)
	}
	return WaitRequest{}, false, nil
}

func (s *Store) createWaitRow(ctx context.Context, q *sqlc.Queries, request WaitRequest, grant AccessGrant, selectorJSON, eventTypesJSON, selectorDigest string) (WaitHandle, error) {
	if err := s.recheckWaitGrant(ctx, q, grant); err != nil {
		return WaitHandle{}, err
	}
	row, err := q.CreateOrchestrationEventWaitHandle(ctx, newWaitHandleParams(request, grant, selectorJSON, eventTypesJSON, selectorDigest))
	if errors.Is(err, sql.ErrNoRows) {
		return s.replayWaitHandle(ctx, q, request, grant, selectorDigest)
	}
	if err != nil {
		return WaitHandle{}, classifyDBError("create event wait handle", err)
	}
	return waitFromRow(row)
}

// recheckWaitGrant 在 wait 插入事务内重读 grant 的 revision、状态和两端 fence。
func (s *Store) recheckWaitGrant(ctx context.Context, q *sqlc.Queries, grant AccessGrant) error {
	row, err := q.GetOrchestrationAccessGrant(ctx, sqlc.GetOrchestrationAccessGrantParams{GrantID: grant.GrantID})
	if err != nil {
		return classifyDBError("recheck access grant for wait", err)
	}
	current, err := grantFromRow(row)
	if err != nil {
		return err
	}
	if current.Revision != grant.Revision || current.Status != "active" {
		return ErrAccessDenied
	}
	if !canonicalSourceFenceMatches(current.Source) || !equalFence(current.Source, grant.Source) || !equalFence(current.Target, grant.Target) {
		return ErrAccessDenied
	}
	return nil
}

func newWaitHandleParams(request WaitRequest, grant AccessGrant, selectorJSON, eventTypesJSON, selectorDigest string) sqlc.CreateOrchestrationEventWaitHandleParams {
	return sqlc.CreateOrchestrationEventWaitHandleParams{
		WaitHandleID: request.WaitHandleID, GrantID: request.GrantID, GrantRevision: request.GrantRevision,
		CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
		SourceAgentID: grant.Source.AgentID, TargetAgentID: grant.Target.AgentID,
		TargetReservationID: grant.Target.ReservationID, TargetReservationGeneration: grant.Target.ReservationGeneration,
		TargetIncarnationID: grant.Target.IncarnationID, TargetIncarnationGeneration: grant.Target.IncarnationGeneration,
		TargetBindingVersion: grant.Target.BindingVersion, TargetBindingDigest: grant.Target.BindingDigest,
		TargetFenceRef: grant.Target.SourceFenceRef, SourceID: request.Selector.SourceID,
		SourceEpoch: request.Selector.SourceEpoch, Lane: string(request.Selector.Lane), SelectorVersion: request.Selector.Version,
		SelectorJson: selectorJSON, SelectorDigest: selectorDigest, EventTypesJson: eventTypesJSON,
		AfterSourceSequence: request.AfterSequence, AfterCursor: request.AfterCursor,
		DeadlineAt: utcText(request.DeadlineAt), IdempotencyNamespace: request.IdempotencyNamespace,
		IdempotencyKeyDigest: request.IdempotencyKeyDigest, RequestDigest: request.RequestDigest,
	}
}

// replayWaitHandle 读取同一幂等键的原句柄，并逐字段比较 selector 与 target fence。
func (s *Store) replayWaitHandle(ctx context.Context, q *sqlc.Queries, request WaitRequest, grant AccessGrant, selectorDigest string) (WaitHandle, error) {
	row, err := q.GetOrchestrationEventWaitHandleByIdempotencyKey(ctx, sqlc.GetOrchestrationEventWaitHandleByIdempotencyKeyParams{
		IdempotencyNamespace: request.IdempotencyNamespace, IdempotencyKeyDigest: request.IdempotencyKeyDigest,
	})
	if err != nil {
		return WaitHandle{}, classifyDBError("replay event wait handle", err)
	}
	prior, err := waitFromRow(row)
	if err != nil {
		return WaitHandle{}, err
	}
	if !waitRequestMatchesHandle(request, prior, selectorDigest) || prior.SourceAgentID != grant.Source.AgentID || !equalFence(prior.Target, grant.Target) || !selectorSourceMatchesGrant(prior.Selector, grant) {
		return WaitHandle{}, ErrAccessConflict
	}
	return prior, nil
}
