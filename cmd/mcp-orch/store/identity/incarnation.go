package identity

import (
	"context"
	"database/sql"
	"errors"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// CreateIncarnation 记录不可变 external execution incarnation，并推进 reservation 到 identity_pending。
func (s *Store) CreateIncarnation(ctx context.Context, req IncarnationRequest) (Incarnation, error) {
	if err := req.Validate(); err != nil {
		return Incarnation{}, err
	}
	var result Incarnation
	err := s.withWrite(ctx, func(q *storedb.Queries) error {
		reservation, err := loadReservationForIncarnation(ctx, q, req)
		if err != nil {
			return err
		}
		if prior, found, lookupErr := getIncarnationByReservation(ctx, q, req.ReservationID); lookupErr != nil {
			return lookupErr
		} else if found {
			return replayIncarnation(prior, req, &result)
		}
		if err := validateIncarnationReservation(reservation, req); err != nil {
			return err
		}
		row, createErr := q.CreateOrchestrationExecutionIncarnation(ctx, incarnationParams(req))
		if createErr == nil {
			result, createErr = createIncarnationAndAdvance(ctx, q, row, reservation)
			return createErr
		}
		if !errors.Is(createErr, sql.ErrNoRows) {
			return classifyIdentityDBError("create execution incarnation", createErr)
		}
		prior, found, lookupErr := getIncarnationByReservation(ctx, q, req.ReservationID)
		if lookupErr != nil {
			return lookupErr
		}
		if !found {
			return ErrIdentityConflict
		}
		return replayIncarnation(prior, req, &result)
	})
	return result, err
}

func getIncarnationByReservation(ctx context.Context, q *storedb.Queries, reservationID string) (Incarnation, bool, error) {
	row, err := q.GetOrchestrationExecutionIncarnationByReservation(ctx, storedb.GetOrchestrationExecutionIncarnationByReservationParams{ReservationID: reservationID})
	if errors.Is(err, sql.ErrNoRows) {
		return Incarnation{}, false, nil
	}
	if err != nil {
		return Incarnation{}, false, classifyIdentityDBError("look up execution incarnation", err)
	}
	result, err := incarnationFromRow(row)
	return result, true, err
}

func replayIncarnation(prior Incarnation, req IncarnationRequest, result *Incarnation) error {
	if err := matchIncarnation(req, prior); err != nil {
		*result = Incarnation{}
		return err
	}
	*result = prior
	return nil
}

func createIncarnationAndAdvance(ctx context.Context, q *storedb.Queries, row storedb.OrchestrationExecutionIncarnation, reservation Reservation) (Incarnation, error) {
	if !incarnationReservationStateAllowed(reservation.State) {
		return Incarnation{}, ErrIdentityFence
	}
	result, err := incarnationFromRow(row)
	if err != nil {
		return Incarnation{}, err
	}
	if err := advanceReservationState(ctx, q, reservation, "identity_pending"); err != nil {
		return Incarnation{}, err
	}
	return result, nil
}

// GetIncarnation 按不可变 incarnation ID 读取完整身份。
func (s *Store) GetIncarnation(ctx context.Context, incarnationID string) (Incarnation, error) {
	if err := validateText("incarnation_id", incarnationID, 128, false); err != nil {
		return Incarnation{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Incarnation{}, err
	}
	row, err := s.queries.GetOrchestrationExecutionIncarnation(ctx, storedb.GetOrchestrationExecutionIncarnationParams{IncarnationID: incarnationID})
	if err != nil {
		return Incarnation{}, classifyIdentityDBError("get execution incarnation", err)
	}
	return incarnationFromRow(row)
}

func loadReservationForIncarnation(ctx context.Context, q *storedb.Queries, req IncarnationRequest) (Reservation, error) {
	row, err := q.GetOrchestrationIdentityReservation(ctx, storedb.GetOrchestrationIdentityReservationParams{ReservationID: req.ReservationID})
	if err != nil {
		return Reservation{}, classifyIdentityDBError("load incarnation reservation", err)
	}
	return reservationFromRow(row)
}

// validateIncarnationReservation 确认 incarnation 与 reservation 的身份和来源一致；两者代际独立递增。
func validateIncarnationReservation(reservation Reservation, req IncarnationRequest) error {
	if reservation.AgentID != req.AgentID || reservation.IdentityKind != req.IdentityKind || reservation.SourceFenceRef != req.SourceFenceRef ||
		reservation.ReservationGeneration <= 0 || req.IncarnationGeneration <= 0 {
		return ErrIdentityFence
	}
	return nil
}

func incarnationReservationStateAllowed(state string) bool {
	switch state {
	case "reserved", "claimed", "identity_pending":
		return true
	default:
		return false
	}
}

func incarnationParams(req IncarnationRequest) storedb.CreateOrchestrationExecutionIncarnationParams {
	return storedb.CreateOrchestrationExecutionIncarnationParams{
		IncarnationID: req.IncarnationID, ReservationID: req.ReservationID, AgentID: req.AgentID,
		IdentityKind: req.IdentityKind, ExternalIdentityRef: req.ExternalIdentityRef, ExternalEpoch: req.ExternalEpoch,
		SourceFenceRef: req.SourceFenceRef, IdentityDigest: req.IdentityDigest,
		IncarnationGeneration: req.IncarnationGeneration,
	}
}

// matchIncarnation 比较重放请求的全部不可变 external identity 字段。
func matchIncarnation(req IncarnationRequest, prior Incarnation) error {
	if req.IncarnationGeneration != prior.IncarnationGeneration {
		return ErrIdentityFence
	}
	if req.IncarnationID != prior.IncarnationID || req.ReservationID != prior.ReservationID || req.AgentID != prior.AgentID ||
		req.IdentityKind != prior.IdentityKind || req.ExternalIdentityRef != prior.ExternalIdentityRef || req.ExternalEpoch != prior.ExternalEpoch ||
		req.SourceFenceRef != prior.SourceFenceRef || req.IdentityDigest != prior.IdentityDigest {
		return ErrIdentityConflict
	}
	return nil
}
