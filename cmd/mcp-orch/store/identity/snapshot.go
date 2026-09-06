package identity

import (
	"context"
	"errors"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// ActiveSnapshot 是同一 SQLite 事务内读取的完整 active identity 快照。
//
// 该快照只证明 reservation/incarnation/current binding 三者彼此一致；它
// 不把后续 EventStore.Append 纳入同一事务，因此不能被视为 identity/event
// 原子提交证据。runtime event candidate 仍必须显式保持 production disabled。
type ActiveSnapshot struct {
	Reservation Reservation
	Incarnation Incarnation
	Binding     CurrentBinding
}

// GetActiveSnapshot 在一个 BEGIN IMMEDIATE 事务中读取三条身份记录。
// 缺失记录、非 active 状态或跨表 fence 漂移均 fail-fast，不猜测默认身份。
func (s *Store) GetActiveSnapshot(ctx context.Context, agentID string) (ActiveSnapshot, error) {
	if ctx == nil {
		return ActiveSnapshot{}, errors.New("identity snapshot context is required")
	}
	if err := validateText("agent_id", agentID, 128, false); err != nil {
		return ActiveSnapshot{}, err
	}
	if err := s.ready(ctx); err != nil {
		return ActiveSnapshot{}, err
	}
	var result ActiveSnapshot
	err := s.withWrite(ctx, func(q *storedb.Queries) error {
		var err error
		result, err = readActiveSnapshotTx(ctx, q, agentID)
		return err
	})
	if err != nil {
		return ActiveSnapshot{}, err
	}
	return result, nil
}

func readActiveSnapshotTx(ctx context.Context, q *storedb.Queries, agentID string) (ActiveSnapshot, error) {
	binding, err := readSnapshotBindingTx(ctx, q, agentID)
	if err != nil {
		return ActiveSnapshot{}, err
	}
	reservation, err := readSnapshotReservationTx(ctx, q, binding.ReservationID)
	if err != nil {
		return ActiveSnapshot{}, err
	}
	incarnation, err := readSnapshotIncarnationTx(ctx, q, binding.IncarnationID)
	if err != nil {
		return ActiveSnapshot{}, err
	}
	if !snapshotRelationsMatch(reservation, incarnation, binding) {
		return ActiveSnapshot{}, ErrIdentityFence
	}
	return ActiveSnapshot{Reservation: reservation, Incarnation: incarnation, Binding: binding}, nil
}

func readSnapshotBindingTx(ctx context.Context, q *storedb.Queries, agentID string) (CurrentBinding, error) {
	row, err := q.GetOrchestrationCurrentBinding(ctx, storedb.GetOrchestrationCurrentBindingParams{AgentID: agentID})
	if err != nil {
		return CurrentBinding{}, classifyIdentityDBError("get active identity binding", err)
	}
	binding, err := currentBindingFromRow(row)
	if err != nil {
		return CurrentBinding{}, err
	}
	if binding.State != "active" {
		return CurrentBinding{}, ErrIdentityFence
	}
	return binding, nil
}

func readSnapshotReservationTx(ctx context.Context, q *storedb.Queries, reservationID string) (Reservation, error) {
	row, err := q.GetOrchestrationIdentityReservation(ctx, storedb.GetOrchestrationIdentityReservationParams{ReservationID: reservationID})
	if err != nil {
		return Reservation{}, classifyIdentityDBError("get active identity reservation", err)
	}
	return reservationFromRow(row)
}

func readSnapshotIncarnationTx(ctx context.Context, q *storedb.Queries, incarnationID string) (Incarnation, error) {
	row, err := q.GetOrchestrationExecutionIncarnation(ctx, storedb.GetOrchestrationExecutionIncarnationParams{IncarnationID: incarnationID})
	if err != nil {
		return Incarnation{}, classifyIdentityDBError("get active identity incarnation", err)
	}
	return incarnationFromRow(row)
}

func snapshotRelationsMatch(reservation Reservation, incarnation Incarnation, binding CurrentBinding) bool {
	return snapshotReservationMatches(reservation, binding) && snapshotIncarnationMatches(reservation, incarnation, binding)
}

func snapshotReservationMatches(reservation Reservation, binding CurrentBinding) bool {
	return reservation.State == "activated" && reservation.AgentID == binding.AgentID &&
		reservation.ReservationID == binding.ReservationID && reservation.ReservationGeneration == binding.ReservationGeneration &&
		reservation.SourceFenceRef == binding.SourceFenceRef
}

func snapshotIncarnationMatches(reservation Reservation, incarnation Incarnation, binding CurrentBinding) bool {
	return incarnation.ReservationID == reservation.ReservationID && incarnation.AgentID == binding.AgentID &&
		incarnation.IncarnationID == binding.IncarnationID && incarnation.IncarnationGeneration == binding.IncarnationGeneration &&
		incarnation.IdentityKind == binding.IdentityKind && incarnation.ExternalIdentityRef == binding.ExternalIdentityRef &&
		incarnation.SourceFenceRef == binding.SourceFenceRef
}
