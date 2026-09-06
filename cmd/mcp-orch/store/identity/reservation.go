package identity

import (
	"context"
	"database/sql"
	"errors"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// Reserve 原子记录 logical agent reservation；同稳定键只重放原记录。
func (s *Store) Reserve(ctx context.Context, req ReservationRequest) (Reservation, error) {
	if err := req.Validate(); err != nil {
		return Reservation{}, err
	}
	var result Reservation
	err := s.withWrite(ctx, func(q *storedb.Queries) error {
		if req.CommandID != "" {
			if err := verifyCommandReservationMatch(ctx, q, req); err != nil {
				return err
			}
		}
		row, err := q.CreateOrchestrationIdentityReservation(ctx, reservationParams(req))
		if err == nil {
			result, err = reservationFromRow(row)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return classifyIdentityDBError("reserve identity", err)
		}
		prior, lookupErr := q.GetOrchestrationIdentityReservationByKey(ctx, storedb.GetOrchestrationIdentityReservationByKeyParams{
			IdempotencyNamespace: req.IdempotencyNamespace, IdempotencyKeyDigest: req.IdempotencyKeyDigest,
		})
		if lookupErr != nil {
			return classifyIdentityDBError("replay identity reservation", lookupErr)
		}
		result, err = reservationFromRow(prior)
		if err != nil {
			return err
		}
		if err := matchReservation(req, result); err != nil {
			result = Reservation{}
			return err
		}
		return nil
	})
	return result, err
}

// verifyCommandReservationMatch binds command-backed identity reservations to
// the immutable command intent observed on the same SQLite transaction. A
// caller cannot reserve an identity under a different principal, scope, or
// source/target fence than the command that names it.
func verifyCommandReservationMatch(ctx context.Context, q *storedb.Queries, req ReservationRequest) error {
	row, err := q.GetOrchestrationCommandIntent(ctx, storedb.GetOrchestrationCommandIntentParams{CommandID: req.CommandID})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIdentityNotFound
	}
	if err != nil {
		return classifyIdentityDBError("read command intent for identity reservation", err)
	}
	if row.CallerPrincipalRef != req.CallerPrincipalRef || row.ScopeRef != req.ScopeRef ||
		row.SourceFenceRef != req.SourceFenceRef || row.TargetFenceRef != req.TargetFenceRef {
		return ErrIdentityConflict
	}
	return nil
}

// Get 按 reservation ID 读取严格校验后的身份 reservation。
func (s *Store) Get(ctx context.Context, reservationID string) (Reservation, error) {
	if err := validateText("reservation_id", reservationID, 128, false); err != nil {
		return Reservation{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Reservation{}, err
	}
	row, err := s.queries.GetOrchestrationIdentityReservation(ctx, storedb.GetOrchestrationIdentityReservationParams{ReservationID: reservationID})
	if err != nil {
		return Reservation{}, classifyIdentityDBError("get identity reservation", err)
	}
	return reservationFromRow(row)
}

// GetByKey 按稳定幂等键读取原 reservation，不创建新身份。
func (s *Store) GetByKey(ctx context.Context, namespace, digest string) (Reservation, error) {
	if err := validateText("idempotency_namespace", namespace, 256, false); err != nil {
		return Reservation{}, err
	}
	if err := validateDigest("idempotency_key_digest", digest); err != nil {
		return Reservation{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Reservation{}, err
	}
	row, err := s.queries.GetOrchestrationIdentityReservationByKey(ctx, storedb.GetOrchestrationIdentityReservationByKeyParams{
		IdempotencyNamespace: namespace, IdempotencyKeyDigest: digest,
	})
	if err != nil {
		return Reservation{}, classifyIdentityDBError("get identity reservation by key", err)
	}
	return reservationFromRow(row)
}

func reservationParams(req ReservationRequest) storedb.CreateOrchestrationIdentityReservationParams {
	return storedb.CreateOrchestrationIdentityReservationParams{
		ReservationID: req.ReservationID, CommandID: reservationCommandID(req.CommandID), AgentID: req.AgentID,
		IdentityKind: req.IdentityKind, SourceKind: req.SourceKind, CallerPrincipalRef: req.CallerPrincipalRef,
		ScopeRef: req.ScopeRef, ParentAgentID: req.ParentAgentID, IdempotencyNamespace: req.IdempotencyNamespace,
		IdempotencyKeyDigest: req.IdempotencyKeyDigest, RequestDigest: req.RequestDigest,
		SourceFenceRef: req.SourceFenceRef, TargetFenceRef: req.TargetFenceRef,
		ExpectedBindingVersion: req.ExpectedBindingVersion, ReservationGeneration: req.ReservationGeneration,
	}
}

// matchReservation 比较重放候选的全部不可变字段，允许只复用已有 row 的状态和时间。
func matchReservation(req ReservationRequest, prior Reservation) error {
	if !reservationIdentityEqual(req, prior) || !reservationFenceEqual(req, prior) {
		return ErrIdentityConflict
	}
	return nil
}

// reservationIdentityEqual 比较命令来源和幂等字段，不比较重放时可变化的状态时间。
func reservationIdentityEqual(req ReservationRequest, prior Reservation) bool {
	return req.CommandID == reservationCommandValue(prior.CommandID) && req.AgentID == prior.AgentID &&
		req.IdentityKind == prior.IdentityKind && req.SourceKind == prior.SourceKind && req.CallerPrincipalRef == prior.CallerPrincipalRef &&
		req.ScopeRef == prior.ScopeRef && req.ParentAgentID == prior.ParentAgentID &&
		req.IdempotencyNamespace == prior.IdempotencyNamespace && req.IdempotencyKeyDigest == prior.IdempotencyKeyDigest &&
		req.RequestDigest == prior.RequestDigest
}

func reservationFenceEqual(req ReservationRequest, prior Reservation) bool {
	return req.SourceFenceRef == prior.SourceFenceRef && req.TargetFenceRef == prior.TargetFenceRef &&
		req.ExpectedBindingVersion == prior.ExpectedBindingVersion && req.ReservationGeneration == prior.ReservationGeneration
}

func reservationCommandValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func advanceReservationState(ctx context.Context, q *storedb.Queries, reservation Reservation, next string) error {
	if reservation.State == next {
		return nil
	}
	affected, err := q.UpdateOrchestrationIdentityReservationState(ctx, storedb.UpdateOrchestrationIdentityReservationStateParams{
		NextState: next, ReservationID: reservation.ReservationID, ExpectedState: reservation.State,
	})
	if err != nil {
		return classifyIdentityDBError("advance identity reservation", err)
	}
	return rowsAffectedOrFence("advance identity reservation", affected)
}
