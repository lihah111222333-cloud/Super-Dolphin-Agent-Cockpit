package content

import (
	"context"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
)

type contentSealRequestProbe struct {
	delegate contentcrypto.KeyCreatorSealer
	request  contentcrypto.SealRequest
	sealed   contentcrypto.SealedPayload
}

func (p *contentSealRequestProbe) Validate() error { return p.delegate.Validate() }

func (p *contentSealRequestProbe) CreateAndSeal(
	ctx context.Context,
	request contentcrypto.SealRequest,
) (contentcrypto.SealedPayload, error) {
	p.request = request
	sealed, err := p.delegate.CreateAndSeal(ctx, request)
	if err == nil {
		p.sealed = sealed
	}
	return sealed, err
}

func testContentReservationAndPutFieldsReachSealRequest(t *testing.T) {
	fixture := newContentTestFixture(t, "seal-field-chain")
	plaintext := []byte("seal-field-chain-plaintext")
	reservation := contentReserve(t, fixture, "seal-field-chain", int64(len(plaintext)))
	put := PutSinglePreparedRequest{SchemaVersion: 1, TicketID: reservation.TicketID,
		OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: plaintext}
	probe := &contentSealRequestProbe{delegate: fixture.store.sealer}
	fixture.store.sealer = probe
	_, err := fixture.store.PutSinglePrepared(t.Context(), put)
	contentNoError(t, err)

	producers := contentMergeFieldSets(t, contentStructFields(t, "reservation", reservation), contentStructFields(t, "put", put))
	consumers := contentStructFields(t, "seal", probe.request)
	edges := contentSealRequestEdges()
	contentAssertFieldChain(t, contentFieldChain{producers: producers, consumers: consumers, edges: edges,
		producerExemptions: contentSealProducerExemptions(), consumerExemptions: map[string]string{
			"seal.AADProfile": "crypto contract: CONTENT single sealing fixes the canonical AAD profile v1 literal",
		}})
	contentAssertDirectEdges(t, producers, consumers, contentSealDirectEdges())
	if !reflect.DeepEqual(probe.request.AAD, buildSingleAAD(reservation)) {
		t.Fatalf("seal.AAD = %x, want canonical reservation AAD %x", probe.request.AAD, buildSingleAAD(reservation))
	}
}

func contentSealRequestEdges() map[string][]string {
	return map[string][]string{
		"put.OperationID": {"seal.OperationID"}, "put.TicketID": {"seal.TicketID"},
		"put.Plaintext":             {"seal.Plaintext"},
		"reservation.TicketID":      {"seal.TicketID", "seal.AAD"},
		"reservation.ObjectID":      {"seal.ObjectID", "seal.AAD"},
		"reservation.ObjectVersion": {"seal.ObjectVersion", "seal.AAD"},
		"reservation.WorkspaceRef":  {"seal.AAD"}, "reservation.OwnerID": {"seal.AAD"},
		"reservation.OwnerGeneration": {"seal.AAD"}, "reservation.Source.AgentID": {"seal.AAD"},
		"reservation.Source.FenceRef":     {"seal.SourceFenceRef", "seal.AAD"},
		"reservation.MediaType":           {"seal.MediaType", "seal.AAD"},
		"reservation.ContentIntentDigest": {"seal.ContentIntentDigest", "seal.AAD"},
		"reservation.SizeBytes":           {"seal.AAD"},
	}
}

func contentSealDirectEdges() map[string][]string {
	return map[string][]string{
		"put.OperationID": {"seal.OperationID"}, "put.TicketID": {"seal.TicketID"},
		"put.Plaintext": {"seal.Plaintext"}, "reservation.TicketID": {"seal.TicketID"},
		"reservation.ObjectID": {"seal.ObjectID"}, "reservation.ObjectVersion": {"seal.ObjectVersion"},
		"reservation.Source.FenceRef":     {"seal.SourceFenceRef"},
		"reservation.MediaType":           {"seal.MediaType"},
		"reservation.ContentIntentDigest": {"seal.ContentIntentDigest"},
	}
}

func contentSealProducerExemptions() map[string]string {
	return map[string]string{
		"put.SchemaVersion":                          "put boundary: request version is validated before the versioned crypto primitive is called",
		"reservation.SchemaVersion":                  "reservation owner: schema version remains durable lifecycle metadata outside the seal DTO",
		"reservation.ReservationID":                  "reservation owner: internal reservation identity remains a SQLite relation key",
		"reservation.WriterFenceRef":                 "reservation owner: writer fence remains durable promotion authority metadata",
		"reservation.SecurityTimeGeneration":         "reservation owner: security generation remains durable promotion authority metadata",
		"reservation.CallerPrincipalRef":             "reservation owner: caller principal remains durable access-control metadata",
		"reservation.ScopeRef":                       "reservation owner: scope remains durable access-control metadata",
		"reservation.Source.IdentityKind":            "identity owner: identity kind is checked through the persisted current-binding relation",
		"reservation.Source.IdentityReservationID":   "identity owner: reservation identity is checked through the current-binding relation",
		"reservation.Source.ReservationGeneration":   "identity owner: reservation generation is checked through the current-binding relation",
		"reservation.Source.IncarnationID":           "identity owner: incarnation identity is checked through the current-binding relation",
		"reservation.Source.IncarnationGeneration":   "identity owner: incarnation generation is checked through the current-binding relation",
		"reservation.Source.ExternalIdentityRef":     "identity owner: external identity is checked through the current-binding relation",
		"reservation.Source.BindingVersion":          "identity owner: binding version is checked through the current-binding relation",
		"reservation.Source.BindingDigest":           "identity owner: binding digest is checked through the current-binding relation",
		"reservation.Source.ActiveProcessReceiptRef": "identity owner: process receipt is checked through the current-binding relation",
		"reservation.Source.BindingUpdatedAt":        "identity owner: binding timestamp is checked through the current-binding relation",
		"reservation.IdempotencyNamespace":           "reservation owner: replay namespace is consumed only by reservation creation and replay",
		"reservation.IdempotencyKeyDigest":           "reservation owner: replay key digest is consumed only by reservation creation and replay",
		"reservation.ProofRequestDigest":             "promotion owner: proof request digest is bound when the active reference is issued",
		"reservation.CandidateRefDigest":             "reservation owner: candidate digest remains durable admission metadata",
		"reservation.DigestKeyID":                    "reservation owner: digest key identity remains durable audit metadata",
		"reservation.DigestKeyVersion":               "reservation owner: digest key version remains durable audit metadata",
		"reservation.KeyWrapProfile":                 "crypto receipt: key-wrap profile is verified against the returned sealed payload",
		"reservation.QuotaSnapshotRef":               "reservation owner: quota snapshot reference remains durable quota evidence",
		"reservation.QuotaSnapshotDigest":            "reservation owner: quota snapshot digest remains durable quota evidence",
		"reservation.RetentionPolicyID":              "reservation owner: retention policy identity remains durable lifecycle metadata",
		"reservation.RetentionPolicyVersion":         "reservation owner: retention policy version remains durable lifecycle metadata",
		"reservation.RetentionPolicyDigest":          "promotion owner: retention digest is bound into the active reference snapshot",
		"reservation.AssemblyDeadline":               "preflight owner: assembly deadline is enforced before any crypto or object I/O",
		"reservation.PrepareOperationID":             "preflight owner: stable operation identity is represented by put.OperationID in SealRequest",
		"reservation.PrepareStartedAt":               "preflight owner: start time is durable crash-replay metadata and not crypto input",
		"reservation.State":                          "lifecycle owner: reserved state gates sealing but is not part of the crypto request",
		"reservation.Revision":                       "lifecycle owner: reservation revision gates transitions but is not crypto input",
		"reservation.CreatedAt":                      "database owner: reservation creation time is audit metadata outside sealing",
		"reservation.UpdatedAt":                      "database owner: reservation update time is audit metadata outside sealing",
	}
}

func testContentSealedAndReceiptFieldsReachPreparedObject(t *testing.T) {
	fixture := newContentTestFixture(t, "prepared-field-chain")
	plaintext := []byte("prepared-field-chain-plaintext")
	reservation := contentReserve(t, fixture, "prepared-field-chain", int64(len(plaintext)))
	probe := &contentSealRequestProbe{delegate: fixture.store.sealer}
	fixture.store.sealer = probe
	prepared, err := fixture.store.PutSinglePrepared(t.Context(), PutSinglePreparedRequest{
		SchemaVersion: 1, TicketID: reservation.TicketID,
		OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: plaintext,
	})
	contentNoError(t, err)
	receipt := prepared.ObjectPutReceipt
	objectParams := preparedObjectParams(reservation, probe.sealed)
	receiptParams := payloadReceiptParams(reservation, receipt)

	producers := contentMergeFieldSets(t, contentStructFields(t, "sealed", probe.sealed), contentStructFields(t, "receipt", receipt))
	consumers := contentMergeFieldSets(t, contentStructFields(t, "objectParams", objectParams),
		contentStructFields(t, "receiptParams", receiptParams), contentStructFields(t, "prepared", prepared))
	contentAssertFieldChain(t, contentFieldChain{producers: producers, consumers: consumers,
		edges: contentPreparedEdges(), producerExemptions: contentPreparedProducerExemptions(),
		consumerExemptions: contentPreparedConsumerExemptions()})
	contentAssertDirectEdges(t, producers, consumers, contentPreparedDirectEdges())
	if objectParams.NonceHex != hex.EncodeToString(probe.sealed.Nonce) || prepared.NonceHex != hex.EncodeToString(probe.sealed.Nonce) {
		t.Fatal("sealed.Nonce did not reach prepared object parameters and PreparedObject as canonical hex")
	}
	if receiptParams.StoredAt != receipt.RecordedAt.Format(time.RFC3339Nano) {
		t.Fatalf("receipt.RecordedAt mapped to %q, want RFC3339Nano", receiptParams.StoredAt)
	}
}

func contentPreparedEdges() map[string][]string {
	return map[string][]string{
		"sealed.KeyWrapProfile":   {"objectParams.KeyWrapProfile"},
		"sealed.KeyHandleRef":     {"objectParams.KeyHandleRef", "prepared.KeyHandleRef"},
		"sealed.KeyReceiptRef":    {"objectParams.KeyReceiptRef", "prepared.KeyReceiptRef"},
		"sealed.KeyReceiptDigest": {"objectParams.KeyReceiptDigest", "prepared.KeyReceiptDigest"},
		"sealed.Nonce":            {"objectParams.NonceHex", "prepared.NonceHex"},
		"sealed.AADDigest":        {"objectParams.AadDigest", "prepared.AADDigest"},
		"sealed.CiphertextHash":   {"objectParams.CiphertextSha256", "prepared.CiphertextHash"},
		"sealed.CiphertextSize":   {"objectParams.SealedPayloadSizeBytes", "prepared.CiphertextSize"},
		"receipt.SchemaVersion":   {"prepared.ObjectPutReceipt.SchemaVersion"},
		"receipt.StoreID":         {"receiptParams.ObjectStoreID", "prepared.ObjectPutReceipt.StoreID"},
		"receipt.OperationID":     {"receiptParams.StoreOperationID", "prepared.ObjectPutReceipt.OperationID"},
		"receipt.RequestDigest":   {"receiptParams.StoreRequestDigest", "prepared.ObjectPutReceipt.RequestDigest"},
		"receipt.ObjectID":        {"prepared.ObjectPutReceipt.ObjectID"},
		"receipt.ObjectVersion":   {"prepared.ObjectPutReceipt.ObjectVersion"},
		"receipt.ObjectRef":       {"receiptParams.PrivatePayloadRef", "prepared.ObjectPutReceipt.ObjectRef"},
		"receipt.ReceiptRef":      {"receiptParams.StoreReceiptRef", "prepared.ObjectPutReceipt.ReceiptRef"},
		"receipt.CiphertextHash":  {"receiptParams.CiphertextSha256", "prepared.ObjectPutReceipt.CiphertextHash"},
		"receipt.CiphertextSize":  {"receiptParams.SealedPayloadSizeBytes", "prepared.ObjectPutReceipt.CiphertextSize"},
		"receipt.ReceiptDigest":   {"receiptParams.StoreReceiptDigest", "prepared.ObjectPutReceipt.ReceiptDigest"},
		"receipt.RecordedAt":      {"receiptParams.StoredAt", "prepared.ObjectPutReceipt.RecordedAt"},
	}
}

func contentPreparedDirectEdges() map[string][]string {
	edges := contentPreparedEdges()
	delete(edges, "sealed.Nonce")
	delete(edges, "receipt.RecordedAt")
	return edges
}

func contentPreparedProducerExemptions() map[string]string {
	return map[string]string{
		"sealed.OperationID":   "seal validator: operation identity is checked against PutSinglePreparedRequest before persistence",
		"sealed.RequestDigest": "seal validator: request digest is verified before the object receipt becomes durable evidence",
		"sealed.Algorithm":     "crypto contract: AES-256-GCM is validated and persisted by the SQL literal aead_algorithm",
		"sealed.Ciphertext":    "object store owner: ciphertext bytes are written only to the private object backend, never SQLite",
		"sealed.PlaintextSize": "reservation owner: plaintext size is validated against reservation.SizeBytes before sealing",
	}
}

func contentPreparedConsumerExemptions() map[string]string {
	return map[string]string{
		"objectParams.ObjectID":               "reservation owner: canonical object identity comes from the committed reservation after receipt validation",
		"objectParams.IngestReservationID":    "reservation owner: prepared metadata remains joined to the committed reservation identity",
		"objectParams.OwnerID":                "reservation owner: owner identity is durable reservation authority metadata",
		"objectParams.OwnerGeneration":        "reservation owner: owner generation is durable reservation authority metadata",
		"objectParams.WriterFenceRef":         "reservation owner: writer fence is durable reservation authority metadata",
		"objectParams.SecurityTimeGeneration": "reservation owner: security generation is durable reservation authority metadata",
		"objectParams.WorkspaceRef":           "reservation owner: workspace identity is durable reservation metadata",
		"objectParams.SourceAgentID":          "identity owner: source agent identity is copied from the committed reservation",
		"objectParams.SourceFenceRef":         "identity owner: source fence is copied from the committed reservation",
		"objectParams.MediaType":              "reservation owner: media type is copied from the committed reservation",
		"objectParams.SizeBytes":              "reservation owner: plaintext size is copied from the committed reservation",
		"objectParams.RetentionPolicyDigest":  "reservation owner: retention policy digest is copied from the committed reservation",
		"receiptParams.ObjectID":              "reservation owner: canonical object identity is used after exact receipt validation",
		"receiptParams.IngestReservationID":   "reservation owner: payload receipt remains joined to the committed reservation identity",
		"prepared.SchemaVersion":              "SQL owner: prepared schema version is the migration-132 literal and returned row value",
		"prepared.ReservationID":              "reservation owner: PreparedObject relation identity comes from the returned object row",
		"prepared.ObjectID":                   "reservation owner: PreparedObject identity comes from the validated returned object row",
		"prepared.ObjectVersion":              "SQL owner: object version is the migration-132 literal and returned row value",
		"prepared.State":                      "SQL owner: prepared content state is the migration-132 literal and returned row value",
		"prepared.KeyState":                   "SQL owner: active key state is the migration-132 literal and returned row value",
		"prepared.PreparedAt":                 "database owner: prepared audit time is generated by SQLite and parsed from the returned row",
	}
}
