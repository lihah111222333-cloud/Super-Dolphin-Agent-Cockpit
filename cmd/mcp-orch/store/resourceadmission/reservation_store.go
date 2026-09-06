package resourceadmission

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Reserve 原子重放或创建一个non-authorizing reserved snapshot。
func (s *Store) Reserve(ctx context.Context, request ReserveRequest) (ReservationSnapshot, error) {
	if err := s.ready(ctx); err != nil {
		return ReservationSnapshot{}, err
	}
	if err := request.Validate(); err != nil {
		return ReservationSnapshot{}, err
	}
	var winner ReservationSnapshot
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, _ storedb.DBTX) error {
		var err error
		winner, err = s.reserveTx(ctx, q, request)
		return err
	})
	return winner, err
}

// reserveTx 在写锁内先裁决幂等winner，再完成所有authority读取后执行唯一INSERT。
func (s *Store) reserveTx(
	ctx context.Context,
	q *storedb.Queries,
	request ReserveRequest,
) (ReservationSnapshot, error) {
	existing, found, err := getReservationByKey(ctx, q, request.IdempotencyNamespace, request.IdempotencyKeyDigest)
	if err != nil {
		return ReservationSnapshot{}, err
	}
	if found {
		if existing.ReservationRequestDigest != request.ReservationRequestDigest {
			return ReservationSnapshot{}, ErrReplayConflict
		}
		return existing, nil
	}
	authority, err := loadReservationAuthority(ctx, q, request, s.now().UTC())
	if err != nil {
		return ReservationSnapshot{}, err
	}
	candidate, err := newReservationSnapshot(request, authority)
	if err != nil {
		return ReservationSnapshot{}, err
	}
	row, err := q.CreateResourceReservation(ctx, reservationParams(candidate))
	if err != nil {
		return ReservationSnapshot{}, classifyReservationDBError("create resource reservation", err)
	}
	winner, err := reservationFromRow(ctx, q, row)
	if err != nil {
		return ReservationSnapshot{}, err
	}
	if winner.ResourceSnapshotDigest != candidate.ResourceSnapshotDigest {
		return ReservationSnapshot{}, ErrSnapshotCorrupt
	}
	return winner, nil
}

// Get 按reservation ID返回历史immutable snapshot，不把它重新解释为current proof。
func (s *Store) Get(ctx context.Context, reservationID string) (ReservationSnapshot, error) {
	if err := s.ready(ctx); err != nil {
		return ReservationSnapshot{}, err
	}
	if !validText(reservationID, 128) {
		return ReservationSnapshot{}, ErrInvalidRequest
	}
	row, err := s.queries.GetResourceReservationByID(ctx, storedb.GetResourceReservationByIDParams{
		ReservationID: reservationID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ReservationSnapshot{}, ErrReservationNotFound
	}
	if err != nil {
		return ReservationSnapshot{}, err
	}
	return reservationFromRow(ctx, s.queries, row)
}

// ResolveReservedSnapshot 按command proof key读取并重验current policy、ID和CONTENT。
func (s *Store) ResolveReservedSnapshot(
	ctx context.Context,
	request ResolveReservedSnapshotRequest,
) (ReservationSnapshot, error) {
	if err := s.ready(ctx); err != nil {
		return ReservationSnapshot{}, err
	}
	if err := request.Validate(); err != nil {
		return ReservationSnapshot{}, err
	}
	row, err := s.queries.GetResourceReservationByCommand(ctx, storedb.GetResourceReservationByCommandParams{
		CommandID: request.CommandID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ReservationSnapshot{}, ErrReservationNotFound
	}
	if err != nil {
		return ReservationSnapshot{}, err
	}
	snapshot, err := reservationFromRow(ctx, s.queries, row)
	if err != nil {
		return ReservationSnapshot{}, err
	}
	if !resolveRequestMatchesSnapshot(request, snapshot) {
		return ReservationSnapshot{}, ErrProfileMismatch
	}
	if err := s.validateCurrentReservation(ctx, snapshot); err != nil {
		return ReservationSnapshot{}, err
	}
	return snapshot, nil
}

func (s *Store) validateCurrentReservation(ctx context.Context, snapshot ReservationSnapshot) error {
	request := ReserveRequest{
		SchemaVersion: SchemaVersion, ReservationID: snapshot.ReservationID,
		ResourceSnapshotID: snapshot.ResourceSnapshotID, CommandID: snapshot.CommandID,
		CommandVersion: snapshot.CommandVersion, CommandProfileDigest: snapshot.CommandProfileDigest,
		IdentityFenceDigest:            snapshot.IdentityFenceDigest,
		ContentAdmissionSnapshotDigest: snapshot.Content.AdmissionSnapshotDigest,
		Requested:                      snapshot.Requested, IdempotencyNamespace: snapshot.IdempotencyNamespace,
		IdempotencyKeyDigest:       snapshot.IdempotencyKeyDigest,
		ReservationRequestDigest:   snapshot.ReservationRequestDigest,
		ReservationOperationID:     snapshot.ReservationOperationID,
		ReservationOperationDigest: snapshot.ReservationOperationDigest,
		ReservationReceiptRef:      snapshot.ReservationReceiptRef, AdmissionOwner: snapshot.AdmissionOwner,
	}
	authority, err := loadReservationAuthority(ctx, s.queries, request, s.now().UTC())
	if err != nil {
		return err
	}
	if !authorityMatchesReservation(authority, snapshot) {
		return ErrPolicyStale
	}
	return nil
}

func getReservationByKey(
	ctx context.Context,
	q *storedb.Queries,
	namespace string,
	keyDigest string,
) (ReservationSnapshot, bool, error) {
	row, err := q.GetResourceReservationByKey(ctx, storedb.GetResourceReservationByKeyParams{
		IdempotencyNamespace: namespace, IdempotencyKeyDigest: keyDigest,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ReservationSnapshot{}, false, nil
	}
	if err != nil {
		return ReservationSnapshot{}, false, err
	}
	snapshot, err := reservationFromRow(ctx, q, row)
	return snapshot, true, err
}

// newReservationSnapshot 从authoritative join与caller-stable请求构造reserved snapshot。
func newReservationSnapshot(
	request ReserveRequest,
	authority reservationAuthority,
) (ReservationSnapshot, error) {
	snapshot := ReservationSnapshot{
		SchemaVersion: SchemaVersion, ReservationID: request.ReservationID,
		ReservationVersion: ReservationVersion, ResourceSnapshotID: request.ResourceSnapshotID,
		ResourceStatus: ResourceStatusReserved, RequestedKind: RequestedKindSendMessage,
		CommandID: authority.command.CommandID, CommandVersion: authority.command.CommandVersion,
		CommandProfileDigest: authority.profile.CompleteIntentDigest,
		CallerPrincipalRef:   authority.command.CallerPrincipalRef, ScopeRef: authority.command.ScopeRef,
		Source: BindingAnchor{AgentID: authority.source.AgentID, BindingVersion: authority.source.BindingVersion,
			FenceRef: authority.source.SourceFenceRef},
		Target: BindingAnchor{AgentID: authority.target.AgentID, BindingVersion: authority.target.BindingVersion,
			FenceRef: authority.target.SourceFenceRef}, IdentityFenceDigest: request.IdentityFenceDigest,
		Content: ContentAnchor{ReferenceID: authority.content.ReferenceID, ContentRef: authority.content.CanonicalRef,
			ObjectID: authority.content.ObjectID, ObjectVersion: authority.content.ObjectVersion,
			RefVersion: authority.content.RefVersion, OwnerSnapshotDigest: authority.content.ProofSnapshotDigest,
			AdmissionSnapshotDigest: request.ContentAdmissionSnapshotDigest},
		PolicySnapshotID: authority.policy.SnapshotID, PolicyRevision: authority.policy.Revision,
		PolicySnapshotDigest:  authority.policy.PolicySnapshotDigest,
		PolicyCanonicalHash:   authority.policy.CanonicalPolicyHash,
		EffectiveLimits:       authority.policy.EffectiveLimits,
		EffectiveLimitsDigest: authority.policy.EffectiveLimitsDigest, Requested: request.Requested,
		UsageDigestDomain: EmptyUsageLedgerDomain, UsageWatermark: EmptyUsageWatermark,
		UsageDigest: EmptyUsageLedgerDigest, ReleaseConditionDigest: authority.policy.ReleaseConditionDigest,
		IdempotencyNamespace: request.IdempotencyNamespace, IdempotencyKeyDigest: request.IdempotencyKeyDigest,
		ReservationRequestDigest:   request.ReservationRequestDigest,
		ReservationOperationID:     request.ReservationOperationID,
		ReservationOperationDigest: request.ReservationOperationDigest,
		ReservationReceiptRef:      request.ReservationReceiptRef, AdmissionOwner: request.AdmissionOwner,
		ValidUntil: authority.expires, CreatedAt: authority.created,
	}
	return FinalizeReservationSnapshot(snapshot)
}

// resolveRequestMatchesSnapshot 比较proof-grade lookup的所有调用方字段。
func resolveRequestMatchesSnapshot(
	request ResolveReservedSnapshotRequest,
	snapshot ReservationSnapshot,
) bool {
	return request.SchemaVersion == snapshot.SchemaVersion && request.CommandID == snapshot.CommandID &&
		request.CommandVersion == snapshot.CommandVersion &&
		request.CommandProfileDigest == snapshot.CommandProfileDigest &&
		request.IdentityFenceDigest == snapshot.IdentityFenceDigest &&
		request.ContentAdmissionSnapshotDigest == snapshot.Content.AdmissionSnapshotDigest
}

// authorityMatchesReservation 校验current command/ID/CONTENT/policy仍等于历史snapshot。
func authorityMatchesReservation(authority reservationAuthority, snapshot ReservationSnapshot) bool {
	return authorityCommandAndBindingsMatch(authority, snapshot) &&
		authorityContentMatches(authority, snapshot) && authorityPolicyMatches(authority, snapshot)
}

// authorityCommandAndBindingsMatch 校验command digest与双端binding anchors。
func authorityCommandAndBindingsMatch(authority reservationAuthority, snapshot ReservationSnapshot) bool {
	return authority.command.CommandID == snapshot.CommandID &&
		authority.profile.CompleteIntentDigest == snapshot.CommandProfileDigest &&
		authority.source.AgentID == snapshot.Source.AgentID &&
		authority.source.BindingVersion == snapshot.Source.BindingVersion &&
		authority.source.SourceFenceRef == snapshot.Source.FenceRef &&
		authority.target.AgentID == snapshot.Target.AgentID &&
		authority.target.BindingVersion == snapshot.Target.BindingVersion &&
		authority.target.SourceFenceRef == snapshot.Target.FenceRef
}

func authorityContentMatches(authority reservationAuthority, snapshot ReservationSnapshot) bool {
	return authority.content.ReferenceID == snapshot.Content.ReferenceID &&
		authority.content.CanonicalRef == snapshot.Content.ContentRef &&
		authority.content.ProofSnapshotDigest == snapshot.Content.OwnerSnapshotDigest
}

// authorityPolicyMatches 校验current policy tuple、limits与release metadata。
func authorityPolicyMatches(authority reservationAuthority, snapshot ReservationSnapshot) bool {
	return authority.policy.SnapshotID == snapshot.PolicySnapshotID &&
		authority.policy.Revision == snapshot.PolicyRevision &&
		authority.policy.PolicySnapshotDigest == snapshot.PolicySnapshotDigest &&
		authority.policy.CanonicalPolicyHash == snapshot.PolicyCanonicalHash &&
		authority.policy.EffectiveLimitsDigest == snapshot.EffectiveLimitsDigest &&
		authority.policy.ReleaseConditionDigest == snapshot.ReleaseConditionDigest
}

func classifyReservationDBError(operation string, err error) error {
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return errors.Join(ErrReplayConflict, err)
		case sqlite3.SQLITE_CONSTRAINT_CHECK, sqlite3.SQLITE_CONSTRAINT_TRIGGER, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return errors.Join(ErrProfileMismatch, err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var _ StoreAPI = (*Store)(nil)
