package content

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentobject"
)

// PutSinglePrepared 在reservation commit后密封/发布密文，并以短事务记录两份receipt。
func (s *Store) PutSinglePrepared(ctx context.Context, request PutSinglePreparedRequest) (PreparedObject, error) {
	if err := s.ready(ctx); err != nil {
		return PreparedObject{}, err
	}
	if err := request.Validate(); err != nil {
		return PreparedObject{}, err
	}
	reservation, err := s.GetIngestByTicket(ctx, request.TicketID)
	if err != nil {
		return PreparedObject{}, err
	}
	if int64(len(request.Plaintext)) != reservation.SizeBytes {
		return PreparedObject{}, ErrInvalidRequest
	}
	reservation, err = s.startPreparation(ctx, request.TicketID, request.OperationID)
	if err != nil {
		return PreparedObject{}, err
	}
	if reservation.State != "reserved" {
		return s.replayPrepared(ctx, reservation, request.OperationID)
	}
	sealed, objectReceipt, err := s.sealAndPut(ctx, reservation, request)
	if err != nil {
		return PreparedObject{}, err
	}
	return s.recordPrepared(ctx, reservation, sealed, objectReceipt)
}

// sealAndPut 只在preflight登记后调用crypto与private object两个幂等owner。
func (s *Store) sealAndPut(ctx context.Context, reservation IngestReservation, request PutSinglePreparedRequest) (contentcrypto.SealedPayload, contentobject.PutReceipt, error) {
	sealRequest := contentcrypto.SealRequest{
		OperationID: request.OperationID, TicketID: reservation.TicketID,
		ObjectID: reservation.ObjectID, ObjectVersion: reservation.ObjectVersion,
		SourceFenceRef: reservation.Source.FenceRef, MediaType: reservation.MediaType,
		AADProfile: contentcrypto.AADProfileV1, ContentIntentDigest: reservation.ContentIntentDigest,
		Plaintext: request.Plaintext, AAD: buildSingleAAD(reservation),
	}
	sealed, err := s.sealer.CreateAndSeal(ctx, sealRequest)
	if err != nil {
		return contentcrypto.SealedPayload{}, contentobject.PutReceipt{}, err
	}
	if err := validateSealedForReservation(sealRequest, sealed, reservation); err != nil {
		return contentcrypto.SealedPayload{}, contentobject.PutReceipt{}, err
	}
	put := contentobject.PutRequest{
		SchemaVersion: 1, OperationID: request.OperationID, ObjectID: reservation.ObjectID,
		ObjectVersion: reservation.ObjectVersion, CiphertextHash: sealed.CiphertextHash,
		CiphertextSize: sealed.CiphertextSize, Ciphertext: sealed.Ciphertext,
	}
	put, err = contentobject.FinalizePutRequest(put)
	if err != nil {
		return contentcrypto.SealedPayload{}, contentobject.PutReceipt{}, err
	}
	receipt, err := s.objects.Put(ctx, put)
	if err != nil {
		return contentcrypto.SealedPayload{}, contentobject.PutReceipt{}, err
	}
	if err := validateObjectReceipt(receipt, put); err != nil {
		return contentcrypto.SealedPayload{}, contentobject.PutReceipt{}, ErrReceiptMismatch
	}
	return sealed, receipt, nil
}

// recordPrepared 用短事务记录两份已核验receipt，不跨外部I/O持锁。
func (s *Store) recordPrepared(ctx context.Context, reservation IngestReservation, sealed contentcrypto.SealedPayload, receipt contentobject.PutReceipt) (PreparedObject, error) {
	var result PreparedObject
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(q *storedb.Queries, _ storedb.DBTX) error {
		var err error
		result, err = s.recordPreparedWithQueries(ctx, q, reservation, sealed, receipt)
		return err
	})
	return result, err
}

// recordPreparedWithQueries 在已持有写锁时完成prepared记录或exact replay协调。
func (s *Store) recordPreparedWithQueries(ctx context.Context, q *storedb.Queries, reservation IngestReservation, sealed contentcrypto.SealedPayload, receipt contentobject.PutReceipt) (PreparedObject, error) {
	current, err := loadReservationByTicket(ctx, q, reservation.TicketID)
	if err != nil {
		return PreparedObject{}, err
	}
	if current.State != "reserved" {
		return reconcilePreparedRecord(ctx, q, current, sealed, receipt)
	}
	if !reflect.DeepEqual(current, reservation) {
		return PreparedObject{}, ErrReplayConflict
	}
	objectRow, err := q.CreateContentPreparedObject(ctx, preparedObjectParams(current, sealed))
	if err != nil {
		return PreparedObject{}, err
	}
	payloadRow, err := q.CreateContentPrivatePayloadReceipt(ctx, payloadReceiptParams(current, receipt))
	if err != nil {
		return PreparedObject{}, err
	}
	affected, err := q.MarkContentIngestSealed(ctx, storedb.MarkContentIngestSealedParams{
		UpdatedAt: s.now().UTC().Format(time.RFC3339Nano), IngestReservationID: current.ReservationID,
	})
	if err != nil || affected != 1 {
		return PreparedObject{}, errors.Join(ErrInvalidTransition, err)
	}
	return preparedFromRows(objectRow, payloadRow)
}

func reconcilePreparedRecord(ctx context.Context, q *storedb.Queries, reservation IngestReservation, sealed contentcrypto.SealedPayload, receipt contentobject.PutReceipt) (PreparedObject, error) {
	result, err := readPreparedWithQueries(ctx, q, reservation.ReservationID)
	if err != nil {
		return PreparedObject{}, err
	}
	if !preparedMatchesExternal(result, sealed, receipt) {
		return PreparedObject{}, ErrReplayConflict
	}
	return result, nil
}

func (s *Store) replayPrepared(ctx context.Context, reservation IngestReservation, operationID string) (PreparedObject, error) {
	result, err := readPreparedWithQueries(ctx, s.queries, reservation.ReservationID)
	if err != nil {
		return PreparedObject{}, err
	}
	if result.ObjectPutReceipt.OperationID != operationID {
		return PreparedObject{}, ErrReplayConflict
	}
	return result, nil
}

func readPreparedWithQueries(ctx context.Context, q *storedb.Queries, reservationID string) (PreparedObject, error) {
	objectRow, err := q.GetContentObjectByReservation(ctx, storedb.GetContentObjectByReservationParams{IngestReservationID: reservationID})
	if err != nil {
		return PreparedObject{}, classifyContentReadError(err)
	}
	payloadRow, err := q.GetContentPrivatePayloadReceiptByReservation(ctx, storedb.GetContentPrivatePayloadReceiptByReservationParams{IngestReservationID: reservationID})
	if err != nil {
		return PreparedObject{}, classifyContentReadError(err)
	}
	return preparedFromRows(objectRow, payloadRow)
}

func preparedObjectParams(reservation IngestReservation, sealed contentcrypto.SealedPayload) storedb.CreateContentPreparedObjectParams {
	return storedb.CreateContentPreparedObjectParams{
		ObjectID: reservation.ObjectID, IngestReservationID: reservation.ReservationID,
		OwnerID: reservation.OwnerID, OwnerGeneration: reservation.OwnerGeneration,
		WriterFenceRef: reservation.WriterFenceRef, SecurityTimeGeneration: reservation.SecurityTimeGeneration,
		WorkspaceRef: reservation.WorkspaceRef, SourceAgentID: reservation.Source.AgentID,
		SourceFenceRef: reservation.Source.FenceRef, KeyWrapProfile: sealed.KeyWrapProfile,
		KeyHandleRef: sealed.KeyHandleRef, KeyReceiptRef: sealed.KeyReceiptRef,
		KeyReceiptDigest: sealed.KeyReceiptDigest, NonceHex: hex.EncodeToString(sealed.Nonce),
		AadDigest: sealed.AADDigest, MediaType: reservation.MediaType, SizeBytes: reservation.SizeBytes,
		CiphertextSha256: sealed.CiphertextHash, SealedPayloadSizeBytes: sealed.CiphertextSize,
		RetentionPolicyDigest: reservation.RetentionPolicyDigest,
	}
}

func payloadReceiptParams(reservation IngestReservation, receipt contentobject.PutReceipt) storedb.CreateContentPrivatePayloadReceiptParams {
	return storedb.CreateContentPrivatePayloadReceiptParams{
		ObjectID: reservation.ObjectID, IngestReservationID: reservation.ReservationID,
		ObjectStoreID: receipt.StoreID, PrivatePayloadRef: receipt.ObjectRef,
		SealedPayloadSizeBytes: receipt.CiphertextSize, CiphertextSha256: receipt.CiphertextHash,
		StoreOperationID: receipt.OperationID, StoreRequestDigest: receipt.RequestDigest,
		StoreReceiptRef: receipt.ReceiptRef, StoreReceiptDigest: receipt.ReceiptDigest,
		StoredAt: receipt.RecordedAt.Format(time.RFC3339Nano),
	}
}

func validateSealedForReservation(request contentcrypto.SealRequest, sealed contentcrypto.SealedPayload, reservation IngestReservation) error {
	if contentcrypto.ValidateSealedForRequest(request, sealed) != nil || sealed.KeyWrapProfile != reservation.KeyWrapProfile {
		return ErrReceiptMismatch
	}
	return nil
}

// validateObjectReceipt 逐字段绑定backend receipt与本次finalized put request。
func validateObjectReceipt(receipt contentobject.PutReceipt, request contentobject.PutRequest) error {
	if err := receipt.Validate(); err != nil {
		return ErrReceiptMismatch
	}
	if receipt.OperationID != request.OperationID || receipt.RequestDigest != request.RequestDigest ||
		receipt.ObjectID != request.ObjectID || receipt.ObjectVersion != request.ObjectVersion ||
		receipt.CiphertextHash != request.CiphertextHash || receipt.CiphertextSize != request.CiphertextSize {
		return ErrReceiptMismatch
	}
	return nil
}

// preparedMatchesExternal 校验crash replay的外部结果与原metadata逐字段相同。
func preparedMatchesExternal(prepared PreparedObject, sealed contentcrypto.SealedPayload, receipt contentobject.PutReceipt) bool {
	return prepared.ObjectID == receipt.ObjectID && prepared.ObjectVersion == receipt.ObjectVersion &&
		prepared.KeyHandleRef == sealed.KeyHandleRef && prepared.KeyReceiptRef == sealed.KeyReceiptRef &&
		prepared.KeyReceiptDigest == sealed.KeyReceiptDigest && prepared.CiphertextSize == sealed.CiphertextSize &&
		prepared.NonceHex == hex.EncodeToString(sealed.Nonce) && prepared.AADDigest == sealed.AADDigest &&
		prepared.CiphertextHash == sealed.CiphertextHash && reflect.DeepEqual(prepared.ObjectPutReceipt, receipt)
}
