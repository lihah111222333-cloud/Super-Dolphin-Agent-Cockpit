package content

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Promote 在BEGIN IMMEDIATE事务内把prepared object、live ref和reservation原子推进到active。
func (s *Store) Promote(ctx context.Context, request PromoteRequest) (ActiveRef, error) {
	if err := s.ready(ctx); err != nil {
		return ActiveRef{}, err
	}
	if err := request.Validate(); err != nil {
		return ActiveRef{}, err
	}
	var result ActiveRef
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, _ storedb.DBTX) error {
		var err error
		result, err = s.promoteWithQueries(ctx, q, request)
		return err
	})
	if err != nil {
		return ActiveRef{}, classifyPromotionDBError("promote content", err)
	}
	return result, nil
}

// promoteWithQueries 在已持有写锁的连接内处理首次promotion或exact replay。
func (s *Store) promoteWithQueries(ctx context.Context, q *storedb.Queries, request PromoteRequest) (ActiveRef, error) {
	reservation, err := loadPromotionReservation(ctx, q, request.TicketID)
	if err != nil {
		return ActiveRef{}, err
	}
	if err := validateCurrentSourceBinding(ctx, q, reservation); err != nil {
		return ActiveRef{}, err
	}
	if reservation.State == "promoted" {
		result, err := readActiveRefByReservation(ctx, q, reservation)
		if err != nil {
			return ActiveRef{}, err
		}
		if !promoteRequestMatches(request, result) {
			return ActiveRef{}, ErrReplayConflict
		}
		return result, nil
	}
	if reservation.State != "sealed" {
		return ActiveRef{}, ErrInvalidTransition
	}
	now := s.now().UTC()
	if !request.ProofExpiresAt.After(now) {
		return ActiveRef{}, ErrContentUnavailable
	}
	if err := validatePromotionPreparedState(ctx, q, reservation); err != nil {
		return ActiveRef{}, err
	}
	return promotePreparedReservation(ctx, q, reservation, request, now)
}

// validateCurrentSourceBinding 要求reservation来源仍是ID owner的latest active exact binding。
func validateCurrentSourceBinding(ctx context.Context, q *storedb.Queries, reservation IngestReservation) error {
	current, err := q.GetOrchestrationCurrentBinding(ctx, storedb.GetOrchestrationCurrentBindingParams{
		AgentID: reservation.Source.AgentID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrContentUnavailable
	}
	if err != nil {
		return err
	}
	updatedAt, err := parseContentTime(current.UpdatedAt)
	if err != nil || !currentSourceBindingMatches(reservation.Source, current, updatedAt) {
		return ErrContentUnavailable
	}
	return nil
}

// currentSourceBindingMatches 对ID-1 current binding的所有持久化身份和fence字段做exact比较。
func currentSourceBindingMatches(source SourceBinding, current storedb.OrchestrationCurrentBinding, updatedAt time.Time) bool {
	currentSource := SourceBinding{
		AgentID: current.AgentID, IdentityKind: current.IdentityKind,
		IdentityReservationID: current.ReservationID, ReservationGeneration: current.ReservationGeneration,
		IncarnationID: current.IncarnationID, IncarnationGeneration: current.IncarnationGeneration,
		ExternalIdentityRef: current.ExternalIdentityRef, BindingVersion: current.BindingVersion,
		BindingDigest: current.BindingDigest, FenceRef: current.SourceFenceRef,
		ActiveProcessReceiptRef: current.ActiveProcessReceiptRef, BindingUpdatedAt: updatedAt,
	}
	return current.State == "active" && current.SchemaVersion == 1 && currentSource == source
}

// loadPromotionReservation 读取promotion ticket并区分不存在与损坏记录。
func loadPromotionReservation(ctx context.Context, q *storedb.Queries, ticketID string) (IngestReservation, error) {
	row, err := q.GetContentIngestReservationByTicket(ctx, storedb.GetContentIngestReservationByTicketParams{IngestTicketID: ticketID})
	if errors.Is(err, sql.ErrNoRows) {
		return IngestReservation{}, ErrIngestNotFound
	}
	if err != nil {
		return IngestReservation{}, err
	}
	return reservationFromRow(row)
}

// validatePromotionPreparedState 要求sealed reservation仍对应完整且一致的prepared object和payload receipt。
func validatePromotionPreparedState(ctx context.Context, q *storedb.Queries, reservation IngestReservation) error {
	prepared, err := readPreparedWithQueries(ctx, q, reservation.ReservationID)
	if errors.Is(err, ErrContentNotFound) {
		return ErrContentCorrupt
	}
	if err != nil {
		return err
	}
	if prepared.ReservationID != reservation.ReservationID || prepared.ObjectID != reservation.ObjectID ||
		prepared.ObjectVersion != reservation.ObjectVersion || prepared.State != "prepared" || prepared.KeyState != "active" {
		return ErrContentCorrupt
	}
	return nil
}

// promotePreparedReservation 严格按migration132触发器要求推进object、ref和reservation。
func promotePreparedReservation(
	ctx context.Context,
	q *storedb.Queries,
	reservation IngestReservation,
	request PromoteRequest,
	now time.Time,
) (ActiveRef, error) {
	timestamp := now.Format(time.RFC3339Nano)
	affected, err := q.PromoteContentObject(ctx, storedb.PromoteContentObjectParams{
		PromotedAt: timestamp, UpdatedAt: timestamp, IngestReservationID: reservation.ReservationID,
	})
	if err != nil {
		return ActiveRef{}, err
	}
	if affected != 1 {
		return ActiveRef{}, fmt.Errorf("%w: promote object affected %d rows", ErrInvalidTransition, affected)
	}

	digest, err := computeActiveRefDigest(activeRefCandidate(reservation, request))
	if err != nil {
		return ActiveRef{}, err
	}
	refRow, err := q.CreateContentActiveRef(ctx, activeRefParams(reservation, request, digest))
	if err != nil {
		return ActiveRef{}, err
	}
	affected, err = q.MarkContentIngestPromoted(ctx, storedb.MarkContentIngestPromotedParams{
		UpdatedAt: timestamp, IngestReservationID: reservation.ReservationID,
	})
	if err != nil {
		return ActiveRef{}, err
	}
	if affected != 1 {
		return ActiveRef{}, fmt.Errorf("%w: promote reservation affected %d rows", ErrInvalidTransition, affected)
	}
	return readPromotedRows(ctx, q, refRow, reservation.TicketID)
}

// activeRefCandidate 构造不含数据库created_at的canonical snapshot digest输入。
func activeRefCandidate(reservation IngestReservation, request PromoteRequest) ActiveRef {
	return ActiveRef{
		SchemaVersion: 1, ReferenceID: request.ReferenceID, CanonicalRef: request.CanonicalRef,
		ObjectID: reservation.ObjectID, ObjectVersion: reservation.ObjectVersion, RefVersion: 1,
		TicketID: reservation.TicketID, ContentState: "active", ReferenceState: "live", KeyState: "active",
		CallerPrincipalRef: reservation.CallerPrincipalRef, ScopeRef: reservation.ScopeRef,
		SourceAgentID: reservation.Source.AgentID, SourceFenceRef: reservation.Source.FenceRef,
		ProofRequestDigest: reservation.ProofRequestDigest, ProofID: request.ProofID,
		ProofRevision: request.ProofRevision, OwnerID: reservation.OwnerID,
		OwnerGeneration: reservation.OwnerGeneration, WriterFenceRef: reservation.WriterFenceRef,
		SecurityTimeGeneration: reservation.SecurityTimeGeneration, OwnerProofRef: request.OwnerProofRef,
		ValidUntil: request.ProofExpiresAt, MediaType: reservation.MediaType, SizeBytes: reservation.SizeBytes,
		RetentionPolicyDigest: reservation.RetentionPolicyDigest,
	}
}

// activeRefParams 显式映射active snapshot到sqlc插入参数。
func activeRefParams(reservation IngestReservation, request PromoteRequest, snapshotDigest string) storedb.CreateContentActiveRefParams {
	return storedb.CreateContentActiveRefParams{
		ReferenceID: request.ReferenceID, CanonicalRef: request.CanonicalRef, ObjectID: reservation.ObjectID,
		IngestReservationID: reservation.ReservationID, CallerPrincipalRef: reservation.CallerPrincipalRef,
		ScopeRef: reservation.ScopeRef, SourceAgentID: reservation.Source.AgentID,
		SourceFenceRef: reservation.Source.FenceRef, ProofRequestDigest: reservation.ProofRequestDigest,
		ProofID: request.ProofID, ProofRevision: request.ProofRevision, OwnerID: reservation.OwnerID,
		OwnerGeneration: reservation.OwnerGeneration, WriterFenceRef: reservation.WriterFenceRef,
		SecurityTimeGeneration: reservation.SecurityTimeGeneration, OwnerProofRef: request.OwnerProofRef,
		ProofSnapshotDigest: snapshotDigest, ProofExpiresAt: request.ProofExpiresAt.Format(time.RFC3339Nano),
		MediaType: reservation.MediaType, SizeBytes: reservation.SizeBytes,
		RetentionPolicyDigest: reservation.RetentionPolicyDigest,
	}
}

// readPromotedRows 从事务内最终状态重建active ref，拒绝依赖写入参数冒充持久化结果。
func readPromotedRows(
	ctx context.Context,
	q *storedb.Queries,
	refRow storedb.OrchestrationContentRef,
	ticketID string,
) (ActiveRef, error) {
	objectRow, err := q.GetContentObjectByReservation(ctx, storedb.GetContentObjectByReservationParams{IngestReservationID: refRow.IngestReservationID})
	if err != nil {
		return ActiveRef{}, promotionRelationReadError(err)
	}
	reservationRow, err := q.GetContentIngestReservationByTicket(ctx, storedb.GetContentIngestReservationByTicketParams{
		IngestTicketID: ticketID,
	})
	if err != nil {
		return ActiveRef{}, promotionRelationReadError(err)
	}
	reservation, err := reservationFromRow(reservationRow)
	if err != nil {
		return ActiveRef{}, err
	}
	return activeRefFromRows(refRow, objectRow, reservation)
}

// readActiveRefByReservation 严格重建已promoted reservation的原始ref用于exact replay。
func readActiveRefByReservation(ctx context.Context, q *storedb.Queries, reservation IngestReservation) (ActiveRef, error) {
	refRow, err := q.GetContentRefByReservation(ctx, storedb.GetContentRefByReservationParams{IngestReservationID: reservation.ReservationID})
	if err != nil {
		return ActiveRef{}, promotionRelationReadError(err)
	}
	objectRow, err := q.GetContentObjectByReservation(ctx, storedb.GetContentObjectByReservationParams{IngestReservationID: reservation.ReservationID})
	if err != nil {
		return ActiveRef{}, promotionRelationReadError(err)
	}
	return activeRefFromRows(refRow, objectRow, reservation)
}

// promoteRequestMatches 比较所有调用方提供字段，拒绝同ticket异参数重放。
func promoteRequestMatches(request PromoteRequest, prior ActiveRef) bool {
	return request.SchemaVersion == prior.SchemaVersion && request.TicketID == prior.TicketID &&
		request.ReferenceID == prior.ReferenceID && request.CanonicalRef == prior.CanonicalRef &&
		request.ProofID == prior.ProofID && request.ProofRevision == prior.ProofRevision &&
		request.OwnerProofRef == prior.OwnerProofRef && request.ProofExpiresAt.Equal(prior.ValidUntil)
}

// promotionRelationReadError 把已提交生命周期缺失关系归类为owner记录损坏。
func promotionRelationReadError(err error) error {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrContentNotFound) {
		return ErrContentCorrupt
	}
	return err
}

// classifyPromotionDBError 将SQLite约束冲突映射到稳定CONTENT领域错误。
func classifyPromotionDBError(operation string, err error) error {
	if errors.Is(err, ErrIngestNotFound) || errors.Is(err, ErrReplayConflict) ||
		errors.Is(err, ErrInvalidTransition) || errors.Is(err, ErrContentCorrupt) ||
		errors.Is(err, ErrContentUnavailable) {
		return err
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return errors.Join(ErrReplayConflict, err)
		case sqlite3.SQLITE_CONSTRAINT_CHECK, sqlite3.SQLITE_CONSTRAINT_TRIGGER, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return errors.Join(ErrInvalidTransition, err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
