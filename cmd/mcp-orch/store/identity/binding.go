package identity

import (
	"context"
	"database/sql"
	"errors"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// GetCurrent 读取 logical agent 的最新绑定历史行；撤销行也必须可查。
func (s *Store) GetCurrent(ctx context.Context, agentID string) (CurrentBinding, error) {
	if err := validateText("agent_id", agentID, 128, false); err != nil {
		return CurrentBinding{}, err
	}
	if err := s.ready(ctx); err != nil {
		return CurrentBinding{}, err
	}
	row, err := s.queries.GetOrchestrationCurrentBinding(ctx, storedb.GetOrchestrationCurrentBindingParams{AgentID: agentID})
	if err != nil {
		return CurrentBinding{}, classifyIdentityDBError("get current binding", err)
	}
	return currentBindingFromRow(row)
}

// FinalizeBinding 以 preparation、incarnation 和当前版本 fence 原子建立 active 历史行。
func (s *Store) FinalizeBinding(ctx context.Context, req FinalizeBindingRequest) (CurrentBinding, error) {
	if err := req.Validate(); err != nil {
		return CurrentBinding{}, err
	}
	var result CurrentBinding
	err := s.withWrite(ctx, func(q *storedb.Queries) error {
		var err error
		result, err = finalizeBindingTx(ctx, q, req)
		return err
	})
	return result, err
}

// finalizeBindingTx 在单个写事务中处理准备记录的重放或 finalize。
func finalizeBindingTx(ctx context.Context, q *storedb.Queries, req FinalizeBindingRequest) (CurrentBinding, error) {
	prep, err := getPreparationTx(ctx, q, req.PreparationID)
	if err != nil {
		return CurrentBinding{}, err
	}
	if prep.State == "finalized" {
		return replayFinalizedBinding(ctx, q, prep, req)
	}
	if prep.State != "prepared_non_authorizing" {
		return CurrentBinding{}, ErrIdentityAlreadyResolved
	}
	return finalizePreparedBinding(ctx, q, prep, req)
}

// finalizePreparedBinding 完成 receipt、current binding 与 reservation 的同事务推进。
func finalizePreparedBinding(ctx context.Context, q *storedb.Queries, prep BindingPreparation, req FinalizeBindingRequest) (CurrentBinding, error) {
	reservation, incarnation, err := loadFinalizeIdentities(ctx, q, prep)
	if err != nil {
		return CurrentBinding{}, err
	}
	if err := validateFinalizeFence(reservation, incarnation, prep, req); err != nil {
		return CurrentBinding{}, err
	}
	current, found, err := getCurrentOptional(ctx, q, req.AgentID)
	if err != nil {
		return CurrentBinding{}, err
	}
	if err := validateFinalizeCurrent(current, found, req, incarnation.IncarnationGeneration); err != nil {
		return CurrentBinding{}, err
	}
	row, err := q.AppendOrchestrationCurrentBinding(ctx, currentBindingParams(req, prep, reservation, incarnation, nextBindingVersion(current, found)))
	if err != nil {
		return CurrentBinding{}, classifyIdentityDBError("append active binding", err)
	}
	if err := finalizePreparationTx(ctx, q, prep, req); err != nil {
		return CurrentBinding{}, err
	}
	if err := advanceReservationState(ctx, q, reservation, "activated"); err != nil {
		return CurrentBinding{}, err
	}
	return currentBindingFromRow(row)
}

func getPreparationTx(ctx context.Context, q *storedb.Queries, preparationID string) (BindingPreparation, error) {
	row, err := q.GetOrchestrationBindingPreparation(ctx, storedb.GetOrchestrationBindingPreparationParams{PreparationID: preparationID})
	if err != nil {
		return BindingPreparation{}, classifyIdentityDBError("load binding preparation", err)
	}
	return preparationFromRow(row)
}

func loadFinalizeIdentities(ctx context.Context, q *storedb.Queries, prep BindingPreparation) (Reservation, Incarnation, error) {
	reservationRow, err := q.GetOrchestrationIdentityReservation(ctx, storedb.GetOrchestrationIdentityReservationParams{ReservationID: prep.ReservationID})
	if err != nil {
		return Reservation{}, Incarnation{}, classifyIdentityDBError("load finalize reservation", err)
	}
	incarnationRow, err := q.GetOrchestrationExecutionIncarnation(ctx, storedb.GetOrchestrationExecutionIncarnationParams{IncarnationID: prep.IncarnationID})
	if err != nil {
		return Reservation{}, Incarnation{}, classifyIdentityDBError("load finalize incarnation", err)
	}
	reservation, err := reservationFromRow(reservationRow)
	if err != nil {
		return Reservation{}, Incarnation{}, err
	}
	incarnation, err := incarnationFromRow(incarnationRow)
	return reservation, incarnation, err
}

// validateFinalizeFence 校验 finalize 使用的 reservation、preparation 和 incarnation fence。
func validateFinalizeFence(reservation Reservation, incarnation Incarnation, prep BindingPreparation, req FinalizeBindingRequest) error {
	if !finalizeReservationMatches(reservation, req) || !finalizePreparationMatches(prep, req) || !finalizeIncarnationMatches(incarnation, prep, req) {
		return ErrIdentityFence
	}
	return nil
}

func finalizeReservationMatches(reservation Reservation, req FinalizeBindingRequest) bool {
	return reservation.State == "activation_pending" && reservation.AgentID == req.AgentID && reservation.IdentityKind == req.IdentityKind
}

// finalizePreparationMatches 比较准备记录携带的版本、摘要和来源 fence。
func finalizePreparationMatches(prep BindingPreparation, req FinalizeBindingRequest) bool {
	return prep.AgentID == req.AgentID && prep.IncarnationID == req.IncarnationID && prep.IdentityKind == req.IdentityKind &&
		prep.ExpectedBindingVersion == req.ExpectedBindingVersion && prep.PreparationGeneration == req.ExpectedPreparationGeneration &&
		prep.BindingDigest == req.BindingDigest && prep.SourceFenceRef == req.SourceFenceRef
}

func finalizeIncarnationMatches(incarnation Incarnation, prep BindingPreparation, req FinalizeBindingRequest) bool {
	return incarnation.AgentID == req.AgentID && incarnation.IdentityKind == req.IdentityKind &&
		incarnation.ExternalIdentityRef == req.ExternalIdentityRef && incarnation.SourceFenceRef == req.SourceFenceRef &&
		incarnation.IncarnationGeneration == prep.ExpectedIncarnationGeneration
}

func getCurrentOptional(ctx context.Context, q *storedb.Queries, agentID string) (CurrentBinding, bool, error) {
	row, err := q.GetOrchestrationCurrentBinding(ctx, storedb.GetOrchestrationCurrentBindingParams{AgentID: agentID})
	if errors.Is(err, sql.ErrNoRows) {
		return CurrentBinding{}, false, nil
	}
	if err != nil {
		return CurrentBinding{}, false, classifyIdentityDBError("load current binding", err)
	}
	current, err := currentBindingFromRow(row)
	return current, true, err
}

// validateFinalizeCurrent 确认初次绑定或 revoked 后 replacement 的版本条件。
func validateFinalizeCurrent(current CurrentBinding, found bool, req FinalizeBindingRequest, nextGeneration int64) error {
	if req.ExpectedBindingVersion == 0 {
		if found {
			return ErrIdentityFence
		}
		return nil
	}
	if !found || current.BindingVersion != req.ExpectedBindingVersion || current.State != "revoked" || current.IncarnationGeneration >= nextGeneration {
		return ErrIdentityFence
	}
	return nil
}

func nextBindingVersion(current CurrentBinding, found bool) int64 {
	if !found {
		return 1
	}
	return current.BindingVersion + 1
}

func currentBindingParams(req FinalizeBindingRequest, prep BindingPreparation, reservation Reservation, incarnation Incarnation, version int64) storedb.AppendOrchestrationCurrentBindingParams {
	return storedb.AppendOrchestrationCurrentBindingParams{
		AgentID: req.AgentID, BindingVersion: version, ReservationID: prep.ReservationID, IncarnationID: req.IncarnationID,
		IdentityKind: req.IdentityKind, ReservationGeneration: reservation.ReservationGeneration, IncarnationGeneration: incarnation.IncarnationGeneration,
		ExternalIdentityRef: req.ExternalIdentityRef, SourceFenceRef: req.SourceFenceRef,
		ActiveProcessReceiptRef: req.ActiveProcessReceiptRef, BindingDigest: req.BindingDigest,
	}
}

func finalizePreparationTx(ctx context.Context, q *storedb.Queries, prep BindingPreparation, req FinalizeBindingRequest) error {
	affected, err := q.UpdateOrchestrationBindingPreparationState(ctx, storedb.UpdateOrchestrationBindingPreparationStateParams{
		NextState: "finalized", ActiveProcessReceiptRef: req.ActiveProcessReceiptRef, ResolvedAt: nowUTC(),
		PreparationID: prep.PreparationID, AgentID: prep.AgentID,
		ExpectedPreparationGeneration: prep.PreparationGeneration, ExpectedState: prep.State,
	})
	if err != nil {
		return classifyIdentityDBError("finalize binding preparation", err)
	}
	return rowsAffectedOrFence("finalize binding preparation", affected)
}

// replayFinalizedBinding 读取已完成 preparation 的原 active binding，不重复追加历史。
func replayFinalizedBinding(ctx context.Context, q *storedb.Queries, prep BindingPreparation, req FinalizeBindingRequest) (CurrentBinding, error) {
	if !finalizeReplayMatches(prep, req) {
		return CurrentBinding{}, ErrIdentityFence
	}
	row, err := q.GetOrchestrationCurrentBindingVersion(ctx, storedb.GetOrchestrationCurrentBindingVersionParams{
		AgentID: req.AgentID, BindingVersion: req.ExpectedBindingVersion + 1,
	})
	if err != nil {
		return CurrentBinding{}, classifyIdentityDBError("replay finalized binding", err)
	}
	current, err := currentBindingFromRow(row)
	if err != nil {
		return CurrentBinding{}, err
	}
	if !finalizedCurrentMatches(current, req) {
		return CurrentBinding{}, ErrIdentityFence
	}
	return current, nil
}

func finalizedCurrentMatches(current CurrentBinding, req FinalizeBindingRequest) bool {
	return current.IncarnationID == req.IncarnationID && current.IdentityKind == req.IdentityKind &&
		current.ExternalIdentityRef == req.ExternalIdentityRef && current.SourceFenceRef == req.SourceFenceRef &&
		current.State == "active" && current.BindingDigest == req.BindingDigest && current.ActiveProcessReceiptRef == req.ActiveProcessReceiptRef
}

// finalizeReplayMatches 校验 finalize 重放的完整字段，阻止旧请求伪装成新的 CAS。
func finalizeReplayMatches(prep BindingPreparation, req FinalizeBindingRequest) bool {
	return prep.PreparationID == req.PreparationID && prep.AgentID == req.AgentID &&
		prep.PreparationGeneration == req.ExpectedPreparationGeneration &&
		prep.ExpectedBindingVersion == req.ExpectedBindingVersion &&
		prep.IncarnationID == req.IncarnationID && prep.IdentityKind == req.IdentityKind &&
		prep.SourceFenceRef == req.SourceFenceRef && prep.BindingDigest == req.BindingDigest &&
		prep.ActiveProcessReceiptRef == req.ActiveProcessReceiptRef
}

// AbortBinding 仅在 preparation 未完成时写入 aborted；ReasonRef 由上游审计 owner 保存。
func (s *Store) AbortBinding(ctx context.Context, req AbortBindingRequest) (BindingPreparation, error) {
	if err := req.Validate(); err != nil {
		return BindingPreparation{}, err
	}
	var result BindingPreparation
	err := s.withWrite(ctx, func(q *storedb.Queries) error {
		prep, err := getPreparationTx(ctx, q, req.PreparationID)
		if err != nil {
			return err
		}
		if prep.AgentID != req.AgentID || prep.PreparationGeneration != req.ExpectedPreparationGeneration {
			return ErrIdentityFence
		}
		if prep.State == "aborted" {
			result = prep
			return nil
		}
		if prep.State != "prepared_non_authorizing" {
			return ErrIdentityAlreadyResolved
		}
		affected, updateErr := q.UpdateOrchestrationBindingPreparationState(ctx, storedb.UpdateOrchestrationBindingPreparationStateParams{
			NextState: "aborted", ActiveProcessReceiptRef: prep.ActiveProcessReceiptRef, ResolvedAt: nowUTC(),
			PreparationID: prep.PreparationID, AgentID: prep.AgentID,
			ExpectedPreparationGeneration: prep.PreparationGeneration, ExpectedState: prep.State,
		})
		if updateErr != nil {
			return classifyIdentityDBError("abort binding preparation", updateErr)
		}
		if err := rowsAffectedOrFence("abort binding preparation", affected); err != nil {
			return err
		}
		if err := rejectReservationAfterAbort(ctx, q, prep.ReservationID); err != nil {
			return err
		}
		result, err = getPreparationTx(ctx, q, prep.PreparationID)
		return err
	})
	return result, err
}

// rejectReservationAfterAbort 将 abort 结果投影到同一 reservation 状态机。
func rejectReservationAfterAbort(ctx context.Context, q *storedb.Queries, reservationID string) error {
	row, err := q.GetOrchestrationIdentityReservation(ctx, storedb.GetOrchestrationIdentityReservationParams{ReservationID: reservationID})
	if err != nil {
		return classifyIdentityDBError("load aborted reservation", err)
	}
	reservation, err := reservationFromRow(row)
	if err != nil {
		return err
	}
	if reservation.State == "rejected" {
		return nil
	}
	if reservation.State != "activation_pending" && reservation.State != "outcome_unknown" {
		return ErrIdentityFence
	}
	return advanceReservationState(ctx, q, reservation, "rejected")
}

// RevokeBinding 通过追加 revoked 历史行关闭当前 incarnation，不删除旧 fence。
func (s *Store) RevokeBinding(ctx context.Context, req RevokeBindingRequest) (CurrentBinding, error) {
	if err := req.Validate(); err != nil {
		return CurrentBinding{}, err
	}
	var result CurrentBinding
	err := s.withWrite(ctx, func(q *storedb.Queries) error {
		current, found, err := getCurrentOptional(ctx, q, req.AgentID)
		if err != nil {
			return err
		}
		if !found {
			return ErrIdentityNotFound
		}
		if current.State == "revoked" && current.BindingVersion == req.ExpectedBindingVersion {
			if current.IncarnationID != req.IncarnationID || current.SourceFenceRef != req.SourceFenceRef {
				return ErrIdentityFence
			}
			result = current
			return nil
		}
		if !revokeFenceMatches(current, req) {
			return ErrIdentityFence
		}
		row, appendErr := q.AppendOrchestrationCurrentBindingRevoked(ctx, revokedBindingParams(current))
		if appendErr != nil {
			return classifyIdentityDBError("revoke current binding", appendErr)
		}
		result, err = currentBindingFromRow(row)
		return err
	})
	return result, err
}

// CloseBinding 使用生命周期语义名称执行带 fence 的 revoke 追加操作。
func (s *Store) CloseBinding(ctx context.Context, req CloseBindingRequest) (CurrentBinding, error) {
	return s.RevokeBinding(ctx, req)
}

func revokeFenceMatches(current CurrentBinding, req RevokeBindingRequest) bool {
	return current.BindingVersion == req.ExpectedBindingVersion && current.IncarnationID == req.IncarnationID &&
		current.SourceFenceRef == req.SourceFenceRef && (current.State == "active" || current.State == "revoking")
}

func revokedBindingParams(current CurrentBinding) storedb.AppendOrchestrationCurrentBindingRevokedParams {
	return storedb.AppendOrchestrationCurrentBindingRevokedParams{
		AgentID: current.AgentID, BindingVersion: current.BindingVersion + 1,
		ReservationID: current.ReservationID, IncarnationID: current.IncarnationID,
		IdentityKind: current.IdentityKind, ReservationGeneration: current.ReservationGeneration,
		IncarnationGeneration: current.IncarnationGeneration, ExternalIdentityRef: current.ExternalIdentityRef,
		SourceFenceRef: current.SourceFenceRef, ActiveProcessReceiptRef: current.ActiveProcessReceiptRef,
		BindingDigest: current.BindingDigest, SchemaVersion: current.SchemaVersion,
	}
}
