package content

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func testContentReservationParamsFieldMappingGuard(t *testing.T) {
	request := contentReserveRequest(SourceBinding{
		AgentID: "mapping-agent", IdentityKind: "native_subagent",
		IdentityReservationID: "mapping-identity-reservation", ReservationGeneration: 13,
		IncarnationID: "mapping-incarnation", IncarnationGeneration: 17,
		ExternalIdentityRef: "mapping-external", BindingVersion: 19,
		BindingDigest: contentDigest("mapping-binding"), FenceRef: "mapping-fence",
		ActiveProcessReceiptRef: "mapping-process-receipt",
		BindingUpdatedAt:        time.Date(2029, 8, 7, 6, 5, 4, 3, time.UTC),
	}, "mapping", 23)
	params := reservationParams(request)

	producer := contentFlattenProducerFields(t, request)
	consumer := map[string]any{
		"ReservationID":                  requestMapped(params.IngestReservationID),
		"TicketID":                       requestMapped(params.IngestTicketID),
		"ObjectID":                       requestMapped(params.ObjectID),
		"OwnerID":                        requestMapped(params.OwnerID),
		"OwnerGeneration":                requestMapped(params.OwnerGeneration),
		"WriterFenceRef":                 requestMapped(params.WriterFenceRef),
		"SecurityTimeGeneration":         requestMapped(params.SecurityTimeGeneration),
		"WorkspaceRef":                   requestMapped(params.WorkspaceRef),
		"CallerPrincipalRef":             requestMapped(params.CallerPrincipalRef),
		"ScopeRef":                       requestMapped(params.ScopeRef),
		"Source.AgentID":                 requestMapped(params.SourceAgentID),
		"Source.IdentityKind":            requestMapped(params.SourceIdentityKind),
		"Source.IdentityReservationID":   requestMapped(params.SourceIdentityReservationID),
		"Source.ReservationGeneration":   requestMapped(params.SourceReservationGeneration),
		"Source.IncarnationID":           requestMapped(params.SourceIncarnationID),
		"Source.IncarnationGeneration":   requestMapped(params.SourceIncarnationGeneration),
		"Source.ExternalIdentityRef":     requestMapped(params.SourceExternalIdentityRef),
		"Source.BindingVersion":          requestMapped(params.SourceBindingVersion),
		"Source.BindingDigest":           requestMapped(params.SourceBindingDigest),
		"Source.FenceRef":                requestMapped(params.SourceFenceRef),
		"Source.ActiveProcessReceiptRef": requestMapped(params.SourceActiveProcessReceiptRef),
		"Source.BindingUpdatedAt":        requestMapped(params.SourceBindingUpdatedAt),
		"IdempotencyNamespace":           requestMapped(params.IdempotencyNamespace),
		"IdempotencyKeyDigest":           requestMapped(params.IdempotencyKeyDigest),
		"ProofRequestDigest":             requestMapped(params.ProofRequestDigest),
		"CandidateRefDigest":             requestMapped(params.CandidateRefDigest),
		"ContentIntentDigest":            requestMapped(params.ContentIntentDigest),
		"DigestKeyID":                    requestMapped(params.DigestKeyID),
		"DigestKeyVersion":               requestMapped(params.DigestKeyVersion),
		"SizeBytes":                      requestMapped(params.SizeBytes),
		"MediaType":                      requestMapped(params.MediaType),
		"KeyWrapProfile":                 requestMapped(params.KeyWrapProfile),
		"QuotaSnapshotRef":               requestMapped(params.QuotaSnapshotRef),
		"QuotaSnapshotDigest":            requestMapped(params.QuotaSnapshotDigest),
		"RetentionPolicyID":              requestMapped(params.RetentionPolicyID),
		"RetentionPolicyVersion":         requestMapped(params.RetentionPolicyVersion),
		"RetentionPolicyDigest":          requestMapped(params.RetentionPolicyDigest),
		"AssemblyDeadline":               requestMapped(params.AssemblyDeadlineAt),
	}
	exemptions := map[string]string{
		"SchemaVersion": "CreateContentIngestReservation SQL writes the literal schema_version=1",
	}
	contentAssertFieldDifferenceZero(t, producer, consumer, exemptions)

	for path, got := range consumer {
		want := producer[path]
		if timestamp, ok := want.(time.Time); ok {
			want = timestamp.Format(time.RFC3339Nano)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("reservation field %s mapped value = %#v, want %#v", path, got, want)
		}
	}
	if request.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion exemption value = %d, want SQL literal 1", request.SchemaVersion)
	}
}

func testContentPreparedMappingCopiesEveryReceiptField(t *testing.T) {
	fixture := newContentTestFixture(t, "prepared-mapping")
	plaintext := []byte("prepared-field-mapping-sensitive-plaintext")
	reservation := contentReserve(t, fixture, "prepared-mapping", int64(len(plaintext)))
	prepared, err := fixture.store.PutSinglePrepared(t.Context(), PutSinglePreparedRequest{
		SchemaVersion: 1, TicketID: reservation.TicketID,
		OperationID: contentPutOperationID(t, reservation.TicketID), Plaintext: plaintext,
	})
	contentNoError(t, err)

	objectRow, err := fixture.store.queries.GetContentObjectByReservation(t.Context(),
		storedb.GetContentObjectByReservationParams{IngestReservationID: reservation.ReservationID})
	contentNoError(t, err)
	payloadRow, err := fixture.store.queries.GetContentPrivatePayloadReceiptByReservation(t.Context(),
		storedb.GetContentPrivatePayloadReceiptByReservationParams{IngestReservationID: reservation.ReservationID})
	contentNoError(t, err)
	mapped, err := preparedFromRows(objectRow, payloadRow)
	contentNoError(t, err)
	if !reflect.DeepEqual(mapped, prepared) {
		t.Fatalf("preparedFromRows() = %#v, want store result %#v", mapped, prepared)
	}

	wantReceipt := map[string]any{
		"SchemaVersion": payloadRow.SchemaVersion, "StoreID": payloadRow.ObjectStoreID,
		"OperationID": payloadRow.StoreOperationID, "RequestDigest": payloadRow.StoreRequestDigest,
		"ObjectID": payloadRow.ObjectID, "ObjectVersion": payloadRow.ObjectVersion,
		"ObjectRef": payloadRow.PrivatePayloadRef, "ReceiptRef": payloadRow.StoreReceiptRef,
		"CiphertextHash": payloadRow.CiphertextSha256, "CiphertextSize": payloadRow.SealedPayloadSizeBytes,
		"ReceiptDigest": payloadRow.StoreReceiptDigest,
	}
	receipt := reflect.ValueOf(mapped.ObjectPutReceipt)
	receiptType := receipt.Type()
	for index := 0; index < receiptType.NumField(); index++ {
		field := receiptType.Field(index)
		if field.Name == "RecordedAt" {
			continue
		}
		want, ok := wantReceipt[field.Name]
		if !ok {
			t.Fatalf("contentobject.PutReceipt field %s lacks row-mapping registry entry", field.Name)
		}
		if got := receipt.Field(index).Interface(); !reflect.DeepEqual(got, want) {
			t.Errorf("receipt field %s = %#v, want row value %#v", field.Name, got, want)
		}
	}
	if len(wantReceipt) != receiptType.NumField()-1 {
		t.Fatalf("receipt row-mapping registry has %d entries, want %d", len(wantReceipt), receiptType.NumField()-1)
	}
}

func testContentPlaintextFieldIsNeverJSONSerializable(t *testing.T) {
	sentinel := []byte("content-json-plaintext-sentinel")
	payload, err := json.Marshal(PutSinglePreparedRequest{
		SchemaVersion: 1, TicketID: "ticket-json", OperationID: "operation-json", Plaintext: sentinel,
	})
	contentNoError(t, err)
	if strings.Contains(string(payload), string(sentinel)) || strings.Contains(string(payload), "plaintext") {
		t.Fatalf("PutSinglePreparedRequest JSON leaked plaintext field: %s", payload)
	}
	var fields map[string]json.RawMessage
	contentNoError(t, json.Unmarshal(payload, &fields))
	if len(fields) != 3 {
		t.Fatalf("PutSinglePreparedRequest JSON fields = %v, want only schema/ticket/operation", fields)
	}
}

func contentFlattenProducerFields(t *testing.T, request ReserveIngestRequest) map[string]any {
	t.Helper()
	fields := make(map[string]any)
	contentFlattenValue(t, reflect.ValueOf(request), "", fields)
	return fields
}

func contentFlattenValue(t *testing.T, value reflect.Value, prefix string, fields map[string]any) {
	t.Helper()
	typeOfValue := value.Type()
	for index := 0; index < typeOfValue.NumField(); index++ {
		field := typeOfValue.Field(index)
		path := field.Name
		if prefix != "" {
			path = prefix + "." + path
		}
		fieldValue := value.Field(index)
		if field.Type == reflect.TypeOf(SourceBinding{}) {
			contentFlattenValue(t, fieldValue, path, fields)
			continue
		}
		if _, exists := fields[path]; exists {
			t.Fatalf("duplicate producer field path %q", path)
		}
		fields[path] = fieldValue.Interface()
	}
}

func contentAssertFieldDifferenceZero(t *testing.T, producer, consumer map[string]any, exemptions map[string]string) {
	t.Helper()
	missing := contentMissingFields(producer, consumer, exemptions)
	stale := contentStaleFields(producer, consumer)
	invalidExemptions := contentInvalidExemptions(producer, exemptions)
	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(invalidExemptions)
	if len(missing) != 0 || len(stale) != 0 || len(invalidExemptions) != 0 {
		t.Fatalf("content reservation field difference: missing=%v stale=%v invalid_exemptions=%v",
			missing, stale, invalidExemptions)
	}
}

func contentMissingFields(producer, consumer map[string]any, exemptions map[string]string) []string {
	var missing []string
	for field := range producer {
		if _, mapped := consumer[field]; mapped {
			continue
		}
		if reason := exemptions[field]; strings.TrimSpace(reason) == "" {
			missing = append(missing, field)
		}
	}
	return missing
}

func contentStaleFields(producer, consumer map[string]any) []string {
	var stale []string
	for field := range consumer {
		if _, exists := producer[field]; !exists {
			stale = append(stale, field)
		}
	}
	return stale
}

func contentInvalidExemptions(producer map[string]any, exemptions map[string]string) []string {
	var invalid []string
	for field, reason := range exemptions {
		if _, exists := producer[field]; !exists || strings.TrimSpace(reason) == "" {
			invalid = append(invalid, field)
		}
	}
	return invalid
}

func requestMapped(value any) any { return value }
