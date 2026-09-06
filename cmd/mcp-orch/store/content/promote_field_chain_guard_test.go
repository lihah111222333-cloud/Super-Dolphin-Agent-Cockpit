package content

import (
	"reflect"
	"testing"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func testContentPromotionFieldsReachCandidateAndSQLParams(t *testing.T) {
	fixture := newContentTestFixture(t, "promotion-field-chain")
	plaintext := []byte("promotion-field-chain-plaintext")
	reservation := contentReserve(t, fixture, "promotion-field-chain", int64(len(plaintext)))
	_, err := fixture.store.PutSinglePrepared(t.Context(), PutSinglePreparedRequest{
		SchemaVersion: 1, TicketID: reservation.TicketID,
		OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: plaintext,
	})
	contentNoError(t, err)
	sealedReservation, err := fixture.store.GetIngestByTicket(t.Context(), reservation.TicketID)
	contentNoError(t, err)
	request := PromoteRequest{SchemaVersion: 1, TicketID: reservation.TicketID,
		ReferenceID: "reference-promotion-field-chain", CanonicalRef: "content://v1/promotion-field-chain",
		ProofID: "proof-promotion-field-chain", ProofRevision: 7,
		OwnerProofRef: "owner-proof/promotion-field-chain", ProofExpiresAt: contentTestClock().Add(45 * time.Minute)}
	candidate := activeRefCandidate(sealedReservation, request)
	digest, err := computeActiveRefDigest(candidate)
	contentNoError(t, err)
	params := activeRefParams(sealedReservation, request, digest)

	contentAssertPromotionInputToCandidate(t, sealedReservation, request, candidate)
	contentAssertCandidateToParams(t, candidate, params)
	contentAssertActiveRefDigestOneHot(t, candidate)
	active, err := fixture.store.Promote(t.Context(), request)
	contentNoError(t, err)
	if active.SnapshotDigest != digest {
		t.Fatalf("Promote().SnapshotDigest = %q, want candidate digest %q", active.SnapshotDigest, digest)
	}
}

func contentAssertPromotionInputToCandidate(
	t *testing.T,
	reservation IngestReservation,
	request PromoteRequest,
	candidate ActiveRef,
) {
	t.Helper()
	producers := contentMergeFieldSets(t, contentStructFields(t, "reservation", reservation), contentStructFields(t, "promote", request))
	consumers := contentStructFields(t, "candidate", candidate)
	edges := contentPromotionCandidateEdges()
	contentAssertFieldChain(t, contentFieldChain{producers: producers, consumers: consumers, edges: edges,
		producerExemptions: contentPromotionInputExemptions(), consumerExemptions: contentCandidateExemptions()})
	contentAssertDirectEdges(t, producers, consumers, edges)
}

func contentPromotionCandidateEdges() map[string][]string {
	return map[string][]string{
		"reservation.ObjectID": {"candidate.ObjectID"}, "reservation.ObjectVersion": {"candidate.ObjectVersion"},
		"reservation.TicketID":           {"candidate.TicketID"},
		"reservation.CallerPrincipalRef": {"candidate.CallerPrincipalRef"},
		"reservation.ScopeRef":           {"candidate.ScopeRef"}, "reservation.Source.AgentID": {"candidate.SourceAgentID"},
		"reservation.Source.FenceRef":    {"candidate.SourceFenceRef"},
		"reservation.ProofRequestDigest": {"candidate.ProofRequestDigest"},
		"reservation.OwnerID":            {"candidate.OwnerID"}, "reservation.OwnerGeneration": {"candidate.OwnerGeneration"},
		"reservation.WriterFenceRef":         {"candidate.WriterFenceRef"},
		"reservation.SecurityTimeGeneration": {"candidate.SecurityTimeGeneration"},
		"reservation.MediaType":              {"candidate.MediaType"}, "reservation.SizeBytes": {"candidate.SizeBytes"},
		"reservation.RetentionPolicyDigest": {"candidate.RetentionPolicyDigest"},
		"promote.ReferenceID":               {"candidate.ReferenceID"}, "promote.CanonicalRef": {"candidate.CanonicalRef"},
		"promote.ProofID": {"candidate.ProofID"}, "promote.ProofRevision": {"candidate.ProofRevision"},
		"promote.OwnerProofRef": {"candidate.OwnerProofRef"}, "promote.ProofExpiresAt": {"candidate.ValidUntil"},
	}
}

func contentPromotionInputExemptions() map[string]string {
	return map[string]string{
		"promote.SchemaVersion":                      "promotion boundary: request schema is validated before the v1 candidate is constructed",
		"promote.TicketID":                           "promotion lookup: ticket identity selects the committed reservation used by the candidate",
		"reservation.SchemaVersion":                  "SQL owner: active reference schema version is fixed by the migration-132 literal",
		"reservation.ReservationID":                  "SQL relation: reservation identity reaches CreateContentActiveRefParams outside the candidate DTO",
		"reservation.WorkspaceRef":                   "reservation owner: workspace identity remains prepared-object metadata outside the active ref",
		"reservation.Source.IdentityKind":            "identity owner: identity kind is rechecked against the current-binding relation before promotion",
		"reservation.Source.IdentityReservationID":   "identity owner: reservation identity is rechecked against the current-binding relation",
		"reservation.Source.ReservationGeneration":   "identity owner: reservation generation is rechecked against the current-binding relation",
		"reservation.Source.IncarnationID":           "identity owner: incarnation identity is rechecked against the current-binding relation",
		"reservation.Source.IncarnationGeneration":   "identity owner: incarnation generation is rechecked against the current-binding relation",
		"reservation.Source.ExternalIdentityRef":     "identity owner: external identity is rechecked against the current-binding relation",
		"reservation.Source.BindingVersion":          "identity owner: binding version is rechecked against the current-binding relation",
		"reservation.Source.BindingDigest":           "identity owner: binding digest is rechecked against the current-binding relation",
		"reservation.Source.ActiveProcessReceiptRef": "identity owner: process receipt is rechecked against the current-binding relation",
		"reservation.Source.BindingUpdatedAt":        "identity owner: binding timestamp is rechecked against the current-binding relation",
		"reservation.IdempotencyNamespace":           "reservation owner: replay namespace terminates at reservation replay handling",
		"reservation.IdempotencyKeyDigest":           "reservation owner: replay key digest terminates at reservation replay handling",
		"reservation.CandidateRefDigest":             "reservation owner: candidate admission digest remains durable pre-promotion evidence",
		"reservation.ContentIntentDigest":            "crypto owner: content intent digest terminates at the verified sealed payload",
		"reservation.DigestKeyID":                    "reservation owner: digest key identity remains durable audit metadata",
		"reservation.DigestKeyVersion":               "reservation owner: digest key version remains durable audit metadata",
		"reservation.KeyWrapProfile":                 "crypto owner: key-wrap profile remains prepared-object encryption metadata",
		"reservation.QuotaSnapshotRef":               "quota owner: quota snapshot reference remains durable reservation evidence",
		"reservation.QuotaSnapshotDigest":            "quota owner: quota snapshot digest remains durable reservation evidence",
		"reservation.RetentionPolicyID":              "retention owner: policy identity remains durable reservation metadata",
		"reservation.RetentionPolicyVersion":         "retention owner: policy version remains durable reservation metadata",
		"reservation.AssemblyDeadline":               "preflight owner: assembly deadline terminates at sealing admission checks",
		"reservation.PrepareOperationID":             "preflight owner: prepare operation identity remains crash-replay metadata",
		"reservation.PrepareStartedAt":               "preflight owner: prepare start time remains crash-replay audit metadata",
		"reservation.State":                          "lifecycle owner: sealed state gates promotion before candidate construction",
		"reservation.Revision":                       "lifecycle owner: revision gates the atomic promotion transition",
		"reservation.CreatedAt":                      "database owner: reservation creation time remains audit metadata",
		"reservation.UpdatedAt":                      "database owner: reservation update time remains audit metadata",
	}
}

func contentCandidateExemptions() map[string]string {
	return map[string]string{
		"candidate.SchemaVersion":  "CONTENT contract: active candidate schema is the fixed version-one literal",
		"candidate.RefVersion":     "CONTENT contract: first active reference uses the fixed version-one literal",
		"candidate.ContentState":   "lifecycle contract: candidate content state is the fixed active literal",
		"candidate.ReferenceState": "lifecycle contract: candidate reference state is the fixed live literal",
		"candidate.KeyState":       "lifecycle contract: candidate key state is the fixed active literal",
		"candidate.SnapshotDigest": "digest owner: self-digest output is intentionally empty in the digest candidate",
		"candidate.CreatedAt":      "database owner: created_at is generated only after the digest candidate is persisted",
	}
}

func contentAssertCandidateToParams(t *testing.T, candidate ActiveRef, params storedb.CreateContentActiveRefParams) {
	t.Helper()
	producers := contentStructFields(t, "candidate", candidate)
	consumers := contentStructFields(t, "params", params)
	edges := contentCandidateParamEdges()
	contentAssertFieldChain(t, contentFieldChain{producers: producers, consumers: consumers, edges: edges,
		producerExemptions: contentCandidateParamProducerExemptions(), consumerExemptions: map[string]string{
			"params.IngestReservationID": "SQL relation: reservation identity bypasses candidate because ActiveRef exposes ticket identity",
			"params.ProofSnapshotDigest": "digest owner: value is computed from every non-exempt candidate field before insertion",
		}})
	direct := contentCandidateParamEdges()
	delete(direct, "candidate.ValidUntil")
	contentAssertDirectEdges(t, producers, consumers, direct)
	if params.ProofExpiresAt != candidate.ValidUntil.Format(time.RFC3339Nano) {
		t.Fatalf("candidate.ValidUntil mapped to %q, want RFC3339Nano", params.ProofExpiresAt)
	}
}

func contentCandidateParamEdges() map[string][]string {
	return map[string][]string{
		"candidate.ReferenceID": {"params.ReferenceID"}, "candidate.CanonicalRef": {"params.CanonicalRef"},
		"candidate.ObjectID": {"params.ObjectID"}, "candidate.CallerPrincipalRef": {"params.CallerPrincipalRef"},
		"candidate.ScopeRef": {"params.ScopeRef"}, "candidate.SourceAgentID": {"params.SourceAgentID"},
		"candidate.SourceFenceRef":     {"params.SourceFenceRef"},
		"candidate.ProofRequestDigest": {"params.ProofRequestDigest"}, "candidate.ProofID": {"params.ProofID"},
		"candidate.ProofRevision": {"params.ProofRevision"}, "candidate.OwnerID": {"params.OwnerID"},
		"candidate.OwnerGeneration":        {"params.OwnerGeneration"},
		"candidate.WriterFenceRef":         {"params.WriterFenceRef"},
		"candidate.SecurityTimeGeneration": {"params.SecurityTimeGeneration"},
		"candidate.OwnerProofRef":          {"params.OwnerProofRef"}, "candidate.ValidUntil": {"params.ProofExpiresAt"},
		"candidate.MediaType": {"params.MediaType"}, "candidate.SizeBytes": {"params.SizeBytes"},
		"candidate.RetentionPolicyDigest": {"params.RetentionPolicyDigest"},
	}
}

func contentCandidateParamProducerExemptions() map[string]string {
	return map[string]string{
		"candidate.SchemaVersion":  "SQL owner: schema version is the migration-132 literal in CreateContentActiveRef",
		"candidate.ObjectVersion":  "SQL owner: object version is the migration-132 literal in CreateContentActiveRef",
		"candidate.RefVersion":     "SQL owner: reference version is the migration-132 literal in CreateContentActiveRef",
		"candidate.TicketID":       "relation owner: ticket identity selects the reservation and is not duplicated in the ref row",
		"candidate.ContentState":   "lifecycle owner: object active state is written by PromoteContentObject before ref insertion",
		"candidate.ReferenceState": "SQL owner: live reference state is the migration-132 literal in CreateContentActiveRef",
		"candidate.KeyState":       "lifecycle owner: active key state is verified on the prepared object relation",
		"candidate.SnapshotDigest": "digest owner: candidate self-digest output is supplied separately as ProofSnapshotDigest",
		"candidate.CreatedAt":      "database owner: created_at is generated by SQLite after insertion",
	}
}

func contentAssertActiveRefDigestOneHot(t *testing.T, candidate ActiveRef) {
	t.Helper()
	baseDigest, err := computeActiveRefDigest(candidate)
	contentNoError(t, err)
	exemptions := map[string]string{
		"SnapshotDigest": "digest owner: self-digest output is cleared before canonical JSON encoding",
		"CreatedAt":      "database owner: created_at is cleared because SQLite generates it after digesting",
	}
	typeOfCandidate := reflect.TypeFor[ActiveRef]()
	for field := range typeOfCandidate.Fields() {
		t.Run(field.Name, func(t *testing.T) {
			mutated := candidate
			contentMutateActiveRefField(t, &mutated, field.Name)
			got, digestErr := computeActiveRefDigest(mutated)
			contentNoError(t, digestErr)
			reason, exempt := exemptions[field.Name]
			if exempt && (!contentSpecificExemptionReason(reason) || got != baseDigest) {
				t.Fatalf("digest exemption %s is invalid or affected digest", field.Name)
			}
			if !exempt && got == baseDigest {
				t.Fatalf("computeActiveRefDigest ignored one-hot mutation of %s", field.Name)
			}
		})
	}
	if len(exemptions) != 2 {
		t.Fatalf("active-ref digest exemption registry drifted: %v", exemptions)
	}
}

func contentMutateActiveRefField(t *testing.T, candidate *ActiveRef, fieldName string) {
	t.Helper()
	field := reflect.ValueOf(candidate).Elem().FieldByName(fieldName)
	if field.Type() == reflect.TypeFor[time.Time]() {
		field.Set(reflect.ValueOf(field.Interface().(time.Time).Add(time.Second)))
		return
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString(field.String() + "-one-hot")
	case reflect.Int64:
		field.SetInt(field.Int() + 1)
	default:
		t.Fatalf("ActiveRef.%s has unsupported digest mutation kind %s", fieldName, field.Kind())
	}
}
