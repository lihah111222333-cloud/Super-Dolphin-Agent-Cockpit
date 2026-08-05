package processobserve

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
)

func TestDurableRecordFieldGuardRoundTripAndUnknownFieldFailFast(t *testing.T) {
	record := fieldGuardDurableRecord()
	raw, err := encodeDurableRecord(record)
	if err != nil {
		t.Fatalf("encodeDurableRecord() error = %v", err)
	}
	assertDurableJSONTags(t, raw)
	decoded, err := decodeDurableRecord(raw)
	if err != nil {
		t.Fatalf("decodeDurableRecord() error = %v", err)
	}
	if !reflect.DeepEqual(decoded, record) {
		t.Fatalf("durable round-trip changed record: got=%#v want=%#v", decoded, record)
	}
	unknown := addUnknownDurableField(t, raw)
	if _, err := decodeDurableRecord(unknown); err == nil {
		t.Fatal("decodeDurableRecord() accepted an unknown field")
	}
}

func fieldGuardDurableRecord() durableRecord {
	eventID := strings.Repeat("a", 32)
	operationID := strings.Repeat("b", 32)
	digest := strings.Repeat("c", 64)
	now := time.Unix(1_700_000_000, 123).UTC()
	return durableRecord{
		SchemaVersion: durableSchemaVersion,
		EventID:       eventID, OperationID: operationID,
		LifecycleKey: "lifecycle", DedupKey: digest, BucketKey: digest,
		Reason: string(processprobe.ReasonNoAuthoritativeOwner), Status: string(DecisionPersisted),
		FirstSeen: now, LastSeen: now, SeenCount: 1, MissingFields: []string{"receipt"}, EvidenceHash: digest,
		Candidate: durableProjection{ID: eventID + "|candidate", EventID: eventID, OperationID: operationID, Kind: string(ProjectionCandidate), Event: "lsp_ghost_candidate_observed", Reason: string(processprobe.ReasonNoAuthoritativeOwner), Acked: true},
		Blocked:   durableProjection{ID: eventID + "|blocked", EventID: eventID, OperationID: operationID, Kind: string(ProjectionBlocked), Event: "lsp_reclaim_blocked", Reason: string(processprobe.ReasonNoAuthoritativeOwner), Acked: true},
	}
}

func assertDurableJSONTags(t *testing.T, raw []byte) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	typ := reflect.TypeOf(durableRecord{})
	for index := 0; index < typ.NumField(); index++ {
		name := strings.Split(typ.Field(index).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("durable field %s has no JSON contract", typ.Field(index).Name)
		}
		if _, ok := fields[name]; !ok {
			t.Fatalf("durable field %s (%s) missing from encoded record", typ.Field(index).Name, name)
		}
	}
}

func addUnknownDurableField(t *testing.T, raw []byte) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	fields["unexpected_field"] = json.RawMessage(`true`)
	mutated, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return mutated
}
