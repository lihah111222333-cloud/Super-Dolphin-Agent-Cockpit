package accesswait

import (
	"context"
	"errors"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// CancelWait 以句柄 revision CAS 结束 pending wait；不能复活 terminal 句柄。
func (s *Store) CancelWait(ctx context.Context, request CancelRequest) (WaitResult, error) {
	if err := request.Validate(); err != nil {
		return WaitResult{}, err
	}
	if err := s.ready(ctx); err != nil {
		return WaitResult{}, err
	}
	var result WaitResult
	err := s.withWriteTx(ctx, func(_ *storedb.Queries, txdb storedb.DBTX) error {
		var err error
		result, err = s.CancelWaitTx(ctx, txdb, request)
		return err
	})
	return result, err
}

// CancelWaitTx 在调用方事务内取消 pending wait 或重放既有终态。
func (s *Store) CancelWaitTx(ctx context.Context, dbtx storedb.DBTX, request CancelRequest) (WaitResult, error) {
	if err := s.ready(ctx); err != nil {
		return WaitResult{}, err
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return WaitResult{}, err
	}
	if err := request.Validate(); err != nil {
		return WaitResult{}, err
	}
	request.CancelledAt = s.now().UTC()
	q := storedb.New(dbtx)
	handle, poll, err := s.loadAuthorizedCancelHandleTx(ctx, dbtx, q, request)
	if err != nil {
		return WaitResult{}, err
	}
	if handle.Status != "pending" {
		return s.resultForTerminalTx(ctx, dbtx, handle)
	}
	updated, err := s.cancelPendingWaitTx(ctx, q, dbtx, request, handle)
	if err == nil {
		return waitResult(updated, nil, updated.Status, false), nil
	}
	if !errors.Is(err, ErrWaitTerminal) {
		return WaitResult{}, err
	}
	return s.resolveCancelledRaceTx(ctx, dbtx, q, poll, handle)
}

// loadAuthorizedCancelHandleTx 在同一事务读取并授权待取消句柄。
func (s *Store) loadAuthorizedCancelHandleTx(ctx context.Context, dbtx storedb.DBTX, q *storedb.Queries, request CancelRequest) (WaitHandle, PollRequest, error) {
	handle, err := getWaitByIDWithQueries(ctx, q, request.WaitHandleID)
	if err != nil {
		return WaitHandle{}, PollRequest{}, err
	}
	poll := PollRequest{WaitHandleID: request.WaitHandleID, CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef, GrantRevision: request.GrantRevision}
	if _, err := s.authorizeWaitTx(ctx, dbtx, poll, handle); err != nil {
		return WaitHandle{}, PollRequest{}, err
	}
	return handle, poll, nil
}

// resolveCancelledRaceTx 处理取消 CAS 失败后的终态重读与再次授权。
func (s *Store) resolveCancelledRaceTx(ctx context.Context, dbtx storedb.DBTX, q *storedb.Queries, poll PollRequest, handle WaitHandle) (WaitResult, error) {
	latest, err := getWaitByIDWithQueries(ctx, q, handle.WaitHandleID)
	if err != nil {
		return WaitResult{}, err
	}
	if _, err := s.authorizeWaitTx(ctx, dbtx, poll, latest); err != nil {
		return WaitResult{}, err
	}
	if !isTerminalWaitStatus(latest.Status) {
		return WaitResult{}, ErrWaitTerminal
	}
	return s.resultForTerminalTx(ctx, dbtx, latest)
}

func (s *Store) cancelPendingWaitTx(ctx context.Context, q *storedb.Queries, dbtx storedb.DBTX, request CancelRequest, handle WaitHandle) (WaitHandle, error) {
	grant, err := s.loadActiveGrantForWait(ctx, q, handle)
	if err != nil {
		return WaitHandle{}, err
	}
	if err := s.verifyRequestedFencesTx(ctx, dbtx, grant.Source, grant.Target); err != nil {
		return WaitHandle{}, err
	}
	return s.cancelWaitRow(ctx, q, request, handle)
}

func (s *Store) cancelPendingWait(ctx context.Context, request CancelRequest, handle WaitHandle) (WaitResult, error) {
	var updated WaitHandle
	err := s.withWriteTx(ctx, func(q *storedb.Queries, dbtx storedb.DBTX) error {
		var err error
		updated, err = s.cancelPendingWaitTx(ctx, q, dbtx, request, handle)
		return err
	})
	if !errors.Is(err, ErrWaitTerminal) {
		if err != nil {
			return WaitResult{}, err
		}
		return waitResult(updated, nil, updated.Status, false), nil
	}
	latest, readErr := s.getWaitByID(ctx, handle.WaitHandleID)
	if readErr != nil {
		return WaitResult{}, readErr
	}
	if authErr := s.authorizeTerminalHandle(ctx, latest); authErr != nil {
		return WaitResult{}, authErr
	}
	return s.resultForTerminal(ctx, latest)
}

func (s *Store) cancelWaitRow(ctx context.Context, q *storedb.Queries, request CancelRequest, handle WaitHandle) (WaitHandle, error) {
	affected, err := q.CancelOrchestrationEventWaitHandle(ctx, storedb.CancelOrchestrationEventWaitHandleParams{
		ResolvedAt: utcText(request.CancelledAt), WaitHandleID: handle.WaitHandleID, ExpectedRevision: handle.Revision,
	})
	if err != nil {
		return WaitHandle{}, classifyDBError("cancel event wait handle", err)
	}
	if affected != 1 {
		return WaitHandle{}, ErrWaitTerminal
	}
	row, err := q.GetOrchestrationEventWaitHandle(ctx, storedb.GetOrchestrationEventWaitHandleParams{WaitHandleID: handle.WaitHandleID})
	if err != nil {
		return WaitHandle{}, classifyDBError("read cancelled event wait handle", err)
	}
	return waitFromRow(row)
}
