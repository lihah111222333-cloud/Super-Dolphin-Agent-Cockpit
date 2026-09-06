package identity

import (
	"context"
	"database/sql"
	"errors"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// PrepareBinding 持久化不具授权性的绑定候选，不改变旧 binding 投影。
func (s *Store) PrepareBinding(ctx context.Context, req BindingPreparationRequest) (BindingPreparation, error) {
	if err := req.Validate(); err != nil {
		return BindingPreparation{}, err
	}
	var result BindingPreparation
	err := s.withWrite(ctx, func(q *storedb.Queries) error {
		var err error
		result, err = prepareBindingTx(ctx, q, req)
		return err
	})
	return result, err
}

// prepareBindingTx 在单个写事务中校验独立代际并创建或重放准备记录。
func prepareBindingTx(ctx context.Context, q *storedb.Queries, req BindingPreparationRequest) (BindingPreparation, error) {
	reservation, incarnation, err := loadPreparationIdentities(ctx, q, req)
	if err != nil {
		return BindingPreparation{}, err
	}
	if existing, found, lookupErr := getPreparationByGeneration(ctx, q, req); lookupErr != nil {
		return BindingPreparation{}, lookupErr
	} else if found {
		return replayPreparation(existing, req)
	}
	if err := validatePreparationFence(reservation, incarnation, req); err != nil {
		return BindingPreparation{}, err
	}
	if err := validateExpectedBindingVersion(ctx, q, req.AgentID, req.ExpectedBindingVersion); err != nil {
		return BindingPreparation{}, err
	}
	row, createErr := q.CreateOrchestrationBindingPreparation(ctx, preparationParams(req))
	if createErr == nil {
		result, mapErr := preparationFromRow(row)
		if mapErr != nil {
			return BindingPreparation{}, mapErr
		}
		if err := advanceReservationState(ctx, q, reservation, "activation_pending"); err != nil {
			return BindingPreparation{}, err
		}
		return result, nil
	}
	if !errors.Is(createErr, sql.ErrNoRows) {
		return BindingPreparation{}, classifyIdentityDBError("create binding preparation", createErr)
	}
	return replayPreparationTx(ctx, q, req)
}

func getPreparationByGeneration(ctx context.Context, q *storedb.Queries, req BindingPreparationRequest) (BindingPreparation, bool, error) {
	row, err := q.GetOrchestrationBindingPreparationByReservationGeneration(ctx, storedb.GetOrchestrationBindingPreparationByReservationGenerationParams{
		ReservationID: req.ReservationID, PreparationGeneration: req.PreparationGeneration,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return BindingPreparation{}, false, nil
	}
	if err != nil {
		return BindingPreparation{}, false, classifyIdentityDBError("look up binding preparation", err)
	}
	result, err := preparationFromRow(row)
	return result, true, err
}

func replayPreparation(prior BindingPreparation, req BindingPreparationRequest) (BindingPreparation, error) {
	if err := matchPreparation(req, prior); err != nil {
		return BindingPreparation{}, err
	}
	return prior, nil
}

func replayPreparationTx(ctx context.Context, q *storedb.Queries, req BindingPreparationRequest) (BindingPreparation, error) {
	row, err := q.GetOrchestrationBindingPreparationByReservationGeneration(ctx, storedb.GetOrchestrationBindingPreparationByReservationGenerationParams{
		ReservationID: req.ReservationID, PreparationGeneration: req.PreparationGeneration,
	})
	if err != nil {
		return BindingPreparation{}, classifyIdentityDBError("replay binding preparation", err)
	}
	result, err := preparationFromRow(row)
	if err != nil {
		return BindingPreparation{}, err
	}
	return replayPreparation(result, req)
}

// GetPreparation 按准备 ID 读取候选；读取不到不会猜测 pending。
func (s *Store) GetPreparation(ctx context.Context, preparationID string) (BindingPreparation, error) {
	if err := validateText("preparation_id", preparationID, 128, false); err != nil {
		return BindingPreparation{}, err
	}
	if err := s.ready(ctx); err != nil {
		return BindingPreparation{}, err
	}
	row, err := s.queries.GetOrchestrationBindingPreparation(ctx, storedb.GetOrchestrationBindingPreparationParams{PreparationID: preparationID})
	if err != nil {
		return BindingPreparation{}, classifyIdentityDBError("get binding preparation", err)
	}
	return preparationFromRow(row)
}

func loadPreparationIdentities(ctx context.Context, q *storedb.Queries, req BindingPreparationRequest) (Reservation, Incarnation, error) {
	reservationRow, err := q.GetOrchestrationIdentityReservation(ctx, storedb.GetOrchestrationIdentityReservationParams{ReservationID: req.ReservationID})
	if err != nil {
		return Reservation{}, Incarnation{}, classifyIdentityDBError("load preparation reservation", err)
	}
	incarnationRow, err := q.GetOrchestrationExecutionIncarnation(ctx, storedb.GetOrchestrationExecutionIncarnationParams{IncarnationID: req.IncarnationID})
	if err != nil {
		return Reservation{}, Incarnation{}, classifyIdentityDBError("load preparation incarnation", err)
	}
	reservation, err := reservationFromRow(reservationRow)
	if err != nil {
		return Reservation{}, Incarnation{}, err
	}
	incarnation, err := incarnationFromRow(incarnationRow)
	return reservation, incarnation, err
}

// validatePreparationFence 校验 reservation、incarnation 与 preparation 的完整独立代际绑定。
func validatePreparationFence(reservation Reservation, incarnation Incarnation, req BindingPreparationRequest) error {
	if !preparationIdentityMatches(reservation, incarnation, req) || !preparationStateAllowed(reservation.State) {
		return ErrIdentityFence
	}
	return nil
}

// preparationIdentityMatches 比较 reservation、incarnation 和请求中的身份 tuple。
func preparationIdentityMatches(reservation Reservation, incarnation Incarnation, req BindingPreparationRequest) bool {
	return reservation.AgentID == req.AgentID && reservation.IdentityKind == req.IdentityKind &&
		reservation.ReservationGeneration > 0 && incarnation.IncarnationGeneration > 0 && incarnation.ReservationID == req.ReservationID &&
		incarnation.AgentID == req.AgentID && incarnation.IdentityKind == req.IdentityKind && incarnation.SourceFenceRef == req.SourceFenceRef &&
		req.ExpectedIncarnationGeneration > 0 && req.ExpectedIncarnationGeneration == incarnation.IncarnationGeneration
}

func preparationStateAllowed(state string) bool {
	return state == "identity_pending" || state == "activation_pending"
}

// validateExpectedBindingVersion 确认新 preparation 只接续缺失或已撤销的当前 binding。
func validateExpectedBindingVersion(ctx context.Context, q *storedb.Queries, agentID string, expected int64) error {
	row, err := q.GetOrchestrationCurrentBinding(ctx, storedb.GetOrchestrationCurrentBindingParams{AgentID: agentID})
	if errors.Is(err, sql.ErrNoRows) {
		if expected == 0 {
			return nil
		}
		return ErrIdentityFence
	}
	if err != nil {
		return classifyIdentityDBError("check current binding fence", err)
	}
	current, err := currentBindingFromRow(row)
	if err != nil {
		return err
	}
	if current.BindingVersion != expected || current.State != "revoked" {
		return ErrIdentityFence
	}
	return nil
}

func preparationParams(req BindingPreparationRequest) storedb.CreateOrchestrationBindingPreparationParams {
	return storedb.CreateOrchestrationBindingPreparationParams{
		PreparationID: req.PreparationID, ReservationID: req.ReservationID, IncarnationID: req.IncarnationID,
		AgentID: req.AgentID, IdentityKind: req.IdentityKind, PreparationGeneration: req.PreparationGeneration,
		ExpectedBindingVersion: req.ExpectedBindingVersion, ExpectedIncarnationGeneration: req.ExpectedIncarnationGeneration,
		BindingDigest: req.BindingDigest, SourceFenceRef: req.SourceFenceRef,
		ActiveProcessReceiptRef: req.ActiveProcessReceiptRef,
	}
}

// matchPreparation 比较重放候选的全部不可变 preparation 字段。
func matchPreparation(req BindingPreparationRequest, prior BindingPreparation) error {
	if !preparationIDsMatch(req, prior) || !preparationFenceMatch(req, prior) {
		return ErrIdentityConflict
	}
	return nil
}

// preparationIDsMatch 比较 preparation 的标识与代际字段。
func preparationIDsMatch(req BindingPreparationRequest, prior BindingPreparation) bool {
	return req.PreparationID == prior.PreparationID && req.ReservationID == prior.ReservationID && req.IncarnationID == prior.IncarnationID &&
		req.AgentID == prior.AgentID && req.IdentityKind == prior.IdentityKind && req.PreparationGeneration == prior.PreparationGeneration
}

func preparationFenceMatch(req BindingPreparationRequest, prior BindingPreparation) bool {
	return req.ExpectedBindingVersion == prior.ExpectedBindingVersion && req.ExpectedIncarnationGeneration == prior.ExpectedIncarnationGeneration &&
		req.BindingDigest == prior.BindingDigest && req.SourceFenceRef == prior.SourceFenceRef &&
		preparationReceiptMatches(req.ActiveProcessReceiptRef, prior.ActiveProcessReceiptRef)
}

func preparationReceiptMatches(requested, stored string) bool {
	return requested == "" || stored == "" || requested == stored
}
