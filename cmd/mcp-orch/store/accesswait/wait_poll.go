package accesswait

import (
	"context"
	"database/sql"
	"errors"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// GetWait 读取句柄前重新认证 caller、scope、grant revision 和当前 identity fence。
func (s *Store) GetWait(ctx context.Context, request PollRequest) (WaitHandle, error) {
	if err := request.Validate(); err != nil {
		return WaitHandle{}, err
	}
	handle, err := s.getWaitByID(ctx, request.WaitHandleID)
	if err != nil {
		return WaitHandle{}, err
	}
	if err := s.authorizeWait(ctx, request, handle); err != nil {
		return WaitHandle{}, err
	}
	return handle, nil
}

// PollWait 查询并在首个匹配 canonical event 上执行单赢家 CAS。
func (s *Store) PollWait(ctx context.Context, request PollRequest) (WaitResult, error) {
	if err := request.Validate(); err != nil {
		return WaitResult{}, err
	}
	handle, err := s.getWaitByID(ctx, request.WaitHandleID)
	if err != nil {
		return WaitResult{}, err
	}
	if err := s.authorizeWait(ctx, request, handle); err != nil {
		return WaitResult{}, err
	}
	return s.pollWaitHandle(ctx, handle)
}

func (s *Store) pollWaitHandle(ctx context.Context, handle WaitHandle) (WaitResult, error) {
	if handle.Status != "pending" {
		return s.resultForTerminal(ctx, handle)
	}
	if !s.now().UTC().Before(handle.DeadlineAt) {
		return s.expireWait(ctx, handle)
	}
	return s.pollPendingTx(ctx, handle)
}

// pollPendingTx 在单一事务内读取事件并以句柄 revision 固化唯一满足结果。
func (s *Store) pollPendingTx(ctx context.Context, handle WaitHandle) (WaitResult, error) {
	var result WaitResult
	err := s.withWriteTx(ctx, func(q *storedb.Queries, txdb storedb.DBTX) error {
		if _, err := s.authorizeWaitTx(ctx, txdb, PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: handle.CallerPrincipalRef, ScopeRef: handle.ScopeRef, GrantRevision: handle.GrantRevision}, handle); err != nil {
			return err
		}
		if err := s.ensureSourceReadableTx(ctx, txdb, handle.Selector, handle.AfterSequence); err != nil {
			return err
		}
		matched, err := s.findWaitEventTx(ctx, txdb, handle)
		if err != nil {
			return err
		}
		if matched == nil {
			result = waitResult(handle, nil, "pending", false)
			return nil
		}
		updated, err := s.updateSatisfiedWaitTx(ctx, q, txdb, handle, *matched)
		if err != nil {
			return err
		}
		result = waitResult(updated, matched, "satisfied", false)
		return nil
	})
	if errors.Is(err, ErrWaitTerminal) {
		return s.resolveSatisfiedRace(ctx, handle)
	}
	if err != nil {
		return pollEventReadError(err)
	}
	return result, nil
}

func pollEventReadError(err error) (WaitResult, error) {
	return mapWaitEventError(err)
}

func (s *Store) getWaitByID(ctx context.Context, id string) (WaitHandle, error) {
	if err := validateText(ErrInvalidWait, "wait_handle_id", id, maxID, false); err != nil {
		return WaitHandle{}, err
	}
	if err := s.ready(ctx); err != nil {
		return WaitHandle{}, err
	}
	return getWaitByIDWithQueries(ctx, s.queries, id)
}

// GetWaitByKeyTx 在调用方事务内按完整幂等键读取 durable wait。
func (s *Store) GetWaitByKeyTx(ctx context.Context, dbtx storedb.DBTX, namespace, digest string) (WaitHandle, error) {
	if err := s.ready(ctx); err != nil {
		return WaitHandle{}, err
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return WaitHandle{}, err
	}
	if err := validateText(ErrInvalidWait, "idempotency_namespace", namespace, maxNamespace, false); err != nil {
		return WaitHandle{}, err
	}
	if err := validateDigest(ErrInvalidWait, "idempotency_key_digest", digest, true); err != nil {
		return WaitHandle{}, err
	}
	row, err := storedb.New(dbtx).GetOrchestrationEventWaitHandleByIdempotencyKey(ctx, storedb.GetOrchestrationEventWaitHandleByIdempotencyKeyParams{
		IdempotencyNamespace: namespace, IdempotencyKeyDigest: digest,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WaitHandle{}, ErrWaitNotFound
		}
		return WaitHandle{}, classifyDBError("get event wait handle by key in transaction", err)
	}
	return waitFromRow(row)
}

// GetWaitByIDTx 在调用方事务内按 opaque handle id 读取 durable wait，不执行授权或事件读取。
func (s *Store) GetWaitByIDTx(ctx context.Context, dbtx storedb.DBTX, id string) (WaitHandle, error) {
	if err := s.ready(ctx); err != nil {
		return WaitHandle{}, err
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return WaitHandle{}, err
	}
	if err := validateText(ErrInvalidWait, "wait_handle_id", id, maxID, false); err != nil {
		return WaitHandle{}, err
	}
	return getWaitByIDWithQueries(ctx, storedb.New(dbtx), id)
}

func getWaitByIDWithQueries(ctx context.Context, q *storedb.Queries, id string) (WaitHandle, error) {
	row, err := q.GetOrchestrationEventWaitHandle(ctx, storedb.GetOrchestrationEventWaitHandleParams{WaitHandleID: id})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WaitHandle{}, ErrWaitNotFound
		}
		return WaitHandle{}, classifyDBError("get event wait handle", err)
	}
	return waitFromRow(row)
}
