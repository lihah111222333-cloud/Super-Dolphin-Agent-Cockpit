package identity

import (
	"context"
	"errors"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// GetCurrentWithDBTX 在调用方已持有的 SQLite 写连接上读取 current binding，
// 避免 max-open-conns=1 时为 fence 重读再申请第二条连接。
func (s *Store) GetCurrentWithDBTX(ctx context.Context, dbtx storedb.DBTX, agentID string) (CurrentBinding, error) {
	if s == nil || dbtx == nil {
		return CurrentBinding{}, errors.New("identity transaction reader dependencies are missing")
	}
	if ctx == nil {
		return CurrentBinding{}, errors.New("identity transaction reader context is required")
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return CurrentBinding{}, err
	}
	if err := validateText("agent_id", agentID, 128, false); err != nil {
		return CurrentBinding{}, err
	}
	row, err := storedb.New(dbtx).GetOrchestrationCurrentBinding(ctx, storedb.GetOrchestrationCurrentBindingParams{AgentID: agentID})
	if err != nil {
		return CurrentBinding{}, classifyIdentityDBError("get current binding in transaction", err)
	}
	return currentBindingFromRow(row)
}

// GetWithDBTX 在调用方持有的真实事务中读取 reservation，避免身份栅栏校验跨连接观察混合代际。
func (s *Store) GetWithDBTX(ctx context.Context, dbtx storedb.DBTX, reservationID string) (Reservation, error) {
	if s == nil || dbtx == nil {
		return Reservation{}, errors.New("identity transaction reader dependencies are missing")
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return Reservation{}, err
	}
	if ctx == nil {
		return Reservation{}, errors.New("identity transaction reader context is required")
	}
	if err := validateText("reservation_id", reservationID, 128, false); err != nil {
		return Reservation{}, err
	}
	row, err := storedb.New(dbtx).GetOrchestrationIdentityReservation(ctx, storedb.GetOrchestrationIdentityReservationParams{ReservationID: reservationID})
	if err != nil {
		return Reservation{}, classifyIdentityDBError("get identity reservation in transaction", err)
	}
	return reservationFromRow(row)
}

// GetIncarnationWithDBTX 在调用方持有的真实事务中读取不可变 incarnation。
func (s *Store) GetIncarnationWithDBTX(ctx context.Context, dbtx storedb.DBTX, incarnationID string) (Incarnation, error) {
	if s == nil || dbtx == nil {
		return Incarnation{}, errors.New("identity transaction reader dependencies are missing")
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return Incarnation{}, err
	}
	if ctx == nil {
		return Incarnation{}, errors.New("identity transaction reader context is required")
	}
	if err := validateText("incarnation_id", incarnationID, 128, false); err != nil {
		return Incarnation{}, err
	}
	row, err := storedb.New(dbtx).GetOrchestrationExecutionIncarnation(ctx, storedb.GetOrchestrationExecutionIncarnationParams{IncarnationID: incarnationID})
	if err != nil {
		return Incarnation{}, classifyIdentityDBError("get execution incarnation in transaction", err)
	}
	return incarnationFromRow(row)
}

// GetActiveSnapshotTx 在调用方持有的事务中读取完整 active identity 链。
// 它不创建嵌套事务，供 identity/event 原子候选复用同一 DBTX。
func (s *Store) GetActiveSnapshotTx(ctx context.Context, dbtx storedb.DBTX, agentID string) (ActiveSnapshot, error) {
	if s == nil || s.db == nil || s.queries == nil {
		return ActiveSnapshot{}, errors.New("identity transaction reader dependencies are missing")
	}
	if ctx == nil {
		return ActiveSnapshot{}, errors.New("identity transaction reader context is required")
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return ActiveSnapshot{}, err
	}
	if err := validateText("agent_id", agentID, 128, false); err != nil {
		return ActiveSnapshot{}, err
	}
	return readActiveSnapshotTx(ctx, storedb.New(dbtx), agentID)
}
