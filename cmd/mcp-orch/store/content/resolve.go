package content

import (
	"context"
	"database/sql"
	"errors"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// ResolveActiveRef 按owner-issued canonical ref重建active/live snapshot，不访问crypto或object backend。
func (s *Store) ResolveActiveRef(ctx context.Context, request ResolveActiveRefRequest) (ActiveRef, error) {
	if err := s.ready(ctx); err != nil {
		return ActiveRef{}, err
	}
	if !validContentText(request.CanonicalRef, 512) {
		return ActiveRef{}, ErrInvalidRequest
	}
	refRow, err := s.queries.GetContentRefByCanonical(ctx, storedb.GetContentRefByCanonicalParams{CanonicalRef: request.CanonicalRef})
	if errors.Is(err, sql.ErrNoRows) {
		return ActiveRef{}, ErrContentNotFound
	}
	if err != nil {
		return ActiveRef{}, err
	}
	objectRow, err := s.queries.GetContentObjectByReservation(ctx, storedb.GetContentObjectByReservationParams{
		IngestReservationID: refRow.IngestReservationID,
	})
	if err != nil {
		return ActiveRef{}, promotionRelationReadError(err)
	}
	reservation, err := loadResolvedReservation(ctx, s.queries, refRow)
	if err != nil {
		return ActiveRef{}, err
	}
	if err := validateCurrentSourceBinding(ctx, s.queries, reservation); err != nil {
		return ActiveRef{}, err
	}
	result, err := activeRefFromRows(refRow, objectRow, reservation)
	if err != nil {
		return ActiveRef{}, err
	}
	if !result.ValidUntil.After(s.now().UTC()) {
		return ActiveRef{}, ErrContentUnavailable
	}
	return result, nil
}

// loadResolvedReservation 用ref关系定位并验证promoted reservation。
func loadResolvedReservation(ctx context.Context, q *storedb.Queries, refRow storedb.OrchestrationContentRef) (IngestReservation, error) {
	reservationRow, err := q.GetContentIngestReservationByID(ctx, storedb.GetContentIngestReservationByIDParams{
		IngestReservationID: refRow.IngestReservationID,
	})
	if err != nil {
		return IngestReservation{}, promotionRelationReadError(err)
	}
	reservation, err := reservationFromRow(reservationRow)
	if err != nil {
		return IngestReservation{}, err
	}
	if reservation.ReservationID != refRow.IngestReservationID {
		return IngestReservation{}, ErrContentCorrupt
	}
	return reservation, nil
}
