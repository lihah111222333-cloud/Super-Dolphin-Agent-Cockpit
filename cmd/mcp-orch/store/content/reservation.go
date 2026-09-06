package content

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// ReserveIngest 原子创建或重放CONTENT-1 ticket；commit前不调用crypto/object backend。
func (s *Store) ReserveIngest(ctx context.Context, request ReserveIngestRequest) (IngestReservation, error) {
	if err := s.ready(ctx); err != nil {
		return IngestReservation{}, err
	}
	if err := request.Validate(); err != nil {
		return IngestReservation{}, err
	}
	var result IngestReservation
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, _ storedb.DBTX) error {
		row, err := q.GetContentIngestReservationByKey(ctx, storedb.GetContentIngestReservationByKeyParams{
			IdempotencyNamespace: request.IdempotencyNamespace, IdempotencyKeyDigest: request.IdempotencyKeyDigest,
		})
		if err == nil {
			prior, mapErr := reservationFromRow(row)
			if mapErr != nil {
				return mapErr
			}
			if !reserveRequestMatches(request, prior) {
				return ErrReplayConflict
			}
			result = prior
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !request.AssemblyDeadline.After(s.now().UTC()) {
			return ErrIngestExpired
		}
		created, err := q.CreateContentIngestReservation(ctx, reservationParams(request))
		if err != nil {
			return err
		}
		result, err = reservationFromRow(created)
		return err
	})
	return result, err
}

// GetIngestByTicket 读取严格校验后的原reservation，不创建新ticket。
func (s *Store) GetIngestByTicket(ctx context.Context, ticketID string) (IngestReservation, error) {
	if err := s.ready(ctx); err != nil {
		return IngestReservation{}, err
	}
	if !validContentText(ticketID, 128) {
		return IngestReservation{}, ErrInvalidRequest
	}
	row, err := s.queries.GetContentIngestReservationByTicket(ctx, storedb.GetContentIngestReservationByTicketParams{IngestTicketID: ticketID})
	if err != nil {
		return IngestReservation{}, classifyContentReadError(err)
	}
	return reservationFromRow(row)
}

func reservationParams(request ReserveIngestRequest) storedb.CreateContentIngestReservationParams {
	return storedb.CreateContentIngestReservationParams{
		IngestReservationID: request.ReservationID, IngestTicketID: request.TicketID, ObjectID: request.ObjectID,
		OwnerID: request.OwnerID, OwnerGeneration: request.OwnerGeneration, WriterFenceRef: request.WriterFenceRef,
		SecurityTimeGeneration: request.SecurityTimeGeneration, WorkspaceRef: request.WorkspaceRef,
		CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
		SourceAgentID: request.Source.AgentID, SourceIdentityKind: request.Source.IdentityKind,
		SourceIdentityReservationID: request.Source.IdentityReservationID,
		SourceReservationGeneration: request.Source.ReservationGeneration, SourceIncarnationID: request.Source.IncarnationID,
		SourceIncarnationGeneration: request.Source.IncarnationGeneration,
		SourceExternalIdentityRef:   request.Source.ExternalIdentityRef, SourceBindingVersion: request.Source.BindingVersion,
		SourceBindingDigest: request.Source.BindingDigest, SourceFenceRef: request.Source.FenceRef,
		SourceActiveProcessReceiptRef: request.Source.ActiveProcessReceiptRef,
		SourceBindingUpdatedAt:        request.Source.BindingUpdatedAt.Format(time.RFC3339Nano),
		IdempotencyNamespace:          request.IdempotencyNamespace, IdempotencyKeyDigest: request.IdempotencyKeyDigest,
		ProofRequestDigest: request.ProofRequestDigest, CandidateRefDigest: request.CandidateRefDigest,
		ContentIntentDigest: request.ContentIntentDigest, DigestKeyID: request.DigestKeyID,
		DigestKeyVersion: request.DigestKeyVersion, SizeBytes: request.SizeBytes, MediaType: request.MediaType,
		KeyWrapProfile: request.KeyWrapProfile, QuotaSnapshotRef: request.QuotaSnapshotRef,
		QuotaSnapshotDigest: request.QuotaSnapshotDigest, RetentionPolicyID: request.RetentionPolicyID,
		RetentionPolicyVersion: request.RetentionPolicyVersion, RetentionPolicyDigest: request.RetentionPolicyDigest,
		AssemblyDeadlineAt: request.AssemblyDeadline.Format(time.RFC3339Nano),
	}
}

func reserveRequestMatches(candidate ReserveIngestRequest, prior IngestReservation) bool {
	candidate.ReservationID = prior.ReservationID
	candidate.TicketID = prior.TicketID
	candidate.ObjectID = prior.ObjectID
	return reflect.DeepEqual(candidate, prior.ReserveIngestRequest)
}
