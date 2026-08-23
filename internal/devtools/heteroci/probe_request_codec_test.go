package heteroci

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeProbeRequestAcceptsOnlyCanonicalCoreBytes(t *testing.T) {
	request := ProbeRequest{
		SchemaVersion: ProbeRequestSchemaVersion, NodeID: "node-a",
		ProbeIdentity: "sha256:" + strings.Repeat("a", 64), OwnerEpoch: 7, CapturedAtUnixMilli: 1_701_000_000_000,
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeProbeRequest(encoded)
	if err != nil {
		t.Fatalf("DecodeProbeRequest() error = %v", err)
	}
	if decoded != request {
		t.Fatalf("decoded = %#v, want %#v", decoded, request)
	}
	invalid := map[string][]byte{
		"unknown":    append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"extra":true}`)...),
		"duplicate":  append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"node_id":"node-a"}`)...),
		"null":       []byte(`{"schema_version":"heteroci-probe-request/v2","node_id":null,"probe_identity":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","owner_epoch":7,"captured_at_unix_milli":1701000000000}`),
		"whitespace": append([]byte(" "), encoded...),
		"trailing":   append(append([]byte(nil), encoded...), []byte(`{}`)...),
	}
	for name, data := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeProbeRequest(data); err == nil {
				t.Fatal("DecodeProbeRequest() accepted invalid input")
			}
		})
	}
}

func TestDecodeProbeRequestRejectsIdentityAndEpochDrift(t *testing.T) {
	for name, request := range map[string]ProbeRequest{
		"schema":   {SchemaVersion: "heteroci-probe-request/v0", NodeID: "node-a", ProbeIdentity: "sha256:" + strings.Repeat("a", 64), OwnerEpoch: 1, CapturedAtUnixMilli: 1},
		"node":     {SchemaVersion: ProbeRequestSchemaVersion, NodeID: "", ProbeIdentity: "sha256:" + strings.Repeat("a", 64), OwnerEpoch: 1, CapturedAtUnixMilli: 1},
		"probe":    {SchemaVersion: ProbeRequestSchemaVersion, NodeID: "node-a", ProbeIdentity: "bad", OwnerEpoch: 1, CapturedAtUnixMilli: 1},
		"epoch":    {SchemaVersion: ProbeRequestSchemaVersion, NodeID: "node-a", ProbeIdentity: "sha256:" + strings.Repeat("a", 64), OwnerEpoch: 0, CapturedAtUnixMilli: 1},
		"captured": {SchemaVersion: ProbeRequestSchemaVersion, NodeID: "node-a", ProbeIdentity: "sha256:" + strings.Repeat("a", 64), OwnerEpoch: 1},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeProbeRequest(encoded); err == nil {
				t.Fatal("DecodeProbeRequest() accepted drift")
			}
		})
	}
}
