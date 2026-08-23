package heterocireceipt_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocireceipt"
)

func TestDecodeAndVerifyValidDarlingReceipt(t *testing.T) {
	receipt := validReceipt()
	now := time.UnixMilli(1500)
	verifier, err := heterocireceipt.New(validConfig(receipt, now))
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	decoded, err := verifier.Decode(strings.NewReader(marshalReceipt(t, receipt)))
	if err != nil {
		t.Fatalf("decode and verify receipt: %v", err)
	}
	if decoded != receipt {
		t.Fatalf("decoded receipt mismatch: got=%+v want=%+v", decoded, receipt)
	}
}

func TestDecodeRejectsMalformedJSONEnvelope(t *testing.T) {
	receipt := validReceipt()
	validJSON := marshalReceipt(t, receipt)
	tests := []struct {
		name string
		data string
	}{
		{name: "unknown field", data: addBeforeObjectEnd(validJSON, `,"unknown":true`)},
		{name: "authority wire field", data: addBeforeObjectEnd(validJSON, `,"authority":"LOCAL_NON_AUTHORITATIVE"`)},
		{name: "status wire field", data: addBeforeObjectEnd(validJSON, `,"status":"NOT_VERIFIED"`)},
		{name: "null field", data: strings.Replace(validJSON, `"node_id":"node-1"`, `"node_id":null`, 1)},
		{name: "duplicate field", data: addBeforeObjectEnd(validJSON, `,"node_id":"node-other"`)},
		{name: "trailing value", data: validJSON + ` {}`},
		{name: "root array", data: `[]`},
		{name: "missing field", data: removeField(t, validJSON, "digest")},
		{name: "schema version", data: strings.Replace(validJSON, heteroci.CapabilityQualificationSchemaVersion, "heteroci-capability-qualification/v0", 1)},
		{name: "canonical digest", data: strings.Replace(validJSON, receipt.Digest, "sha256:"+strings.Repeat("b", 64), 1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := heterocireceipt.Decode(strings.NewReader(test.data)); err == nil {
				t.Fatalf("Decode(%s) unexpectedly succeeded", test.name)
			}
		})
	}
}

func TestVerifierRejectsReceiptIdentityAndDigestMutations(t *testing.T) {
	base := validReceipt()
	now := time.UnixMilli(1500)
	verifier, err := heterocireceipt.New(validConfig(base, now))
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*heteroci.CapabilityQualificationReceipt)
	}{
		{name: "schema version", mutate: func(value *heteroci.CapabilityQualificationReceipt) {
			value.SchemaVersion = "heteroci-capability-qualification/v0"
		}},
		{name: "node identity", mutate: func(value *heteroci.CapabilityQualificationReceipt) { value.NodeID = "node-other" }},
		{name: "runtime", mutate: func(value *heteroci.CapabilityQualificationReceipt) {
			value.Runtime = heteroci.RuntimeKind("retired_runtime")
		}},
		{name: "probe identity", mutate: func(value *heteroci.CapabilityQualificationReceipt) { value.ProbeIdentity = "probe-other" }},
		{name: "probe boot identity", mutate: func(value *heteroci.CapabilityQualificationReceipt) { value.ProbeBootID = "boot-other" }},
		{name: "image digest", mutate: func(value *heteroci.CapabilityQualificationReceipt) {
			value.ImageDigest = "sha256:" + strings.Repeat("b", 64)
		}},
		{name: "source revision", mutate: func(value *heteroci.CapabilityQualificationReceipt) {
			value.SourceRevision = "https://example.invalid/wine@other"
		}},
		{name: "qualification suite", mutate: func(value *heteroci.CapabilityQualificationReceipt) { value.QualificationSuite = "suite-other" }},
		{name: "immutable identity", mutate: func(value *heteroci.CapabilityQualificationReceipt) { value.ImmutableIdentity = "identity-other" }},
		{name: "completion time", mutate: func(value *heteroci.CapabilityQualificationReceipt) { value.CompletedAtUnixMilli = 0 }},
		{name: "canonical digest", mutate: func(value *heteroci.CapabilityQualificationReceipt) {
			value.Digest = "sha256:" + strings.Repeat("b", 64)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if err := verifier.Verify(candidate); err == nil {
				t.Fatalf("Verify(%s) unexpectedly succeeded", test.name)
			}
		})
	}
}

func TestVerifierRejectsAuthorityStatusAndExpiryViolations(t *testing.T) {
	receipt := validReceipt()
	now := time.UnixMilli(1500)

	config := validConfig(receipt, now)
	config.Authority = "REMOTE_AUTHORITATIVE"
	if _, err := heterocireceipt.New(config); err == nil {
		t.Fatal("New unexpectedly accepted remote authority")
	}

	config = validConfig(receipt, now)
	config.Status = "PASS"
	if _, err := heterocireceipt.New(config); err == nil {
		t.Fatal("New unexpectedly accepted a verified status")
	}

	config = validConfig(receipt, now)
	config.ExpiresAtUnixMilli = 1500
	verifier, err := heterocireceipt.New(config)
	if err != nil {
		t.Fatalf("new verifier with boundary expiry: %v", err)
	}
	if err := verifier.Verify(receipt); err == nil {
		t.Fatal("Verify unexpectedly accepted an expired receipt")
	}

	config = validConfig(receipt, now)
	config.Now = func() time.Time { return time.UnixMilli(900) }
	verifier, err = heterocireceipt.New(config)
	if err != nil {
		t.Fatalf("new verifier with early clock: %v", err)
	}
	if err := verifier.Verify(receipt); err == nil {
		t.Fatal("Verify unexpectedly accepted a future-dated receipt")
	}
}

func TestVerifierRequiresExplicitDependencies(t *testing.T) {
	receipt := validReceipt()
	config := validConfig(receipt, time.UnixMilli(1500))

	tests := []struct {
		name   string
		mutate func(*heterocireceipt.Config)
	}{
		{name: "clock", mutate: func(value *heterocireceipt.Config) { value.Now = nil }},
		{name: "expiry", mutate: func(value *heterocireceipt.Config) { value.ExpiresAtUnixMilli = 0 }},
		{name: "source expectation", mutate: func(value *heterocireceipt.Config) { value.Expectations.SourceRevision = nil }},
		{name: "immutable expectation", mutate: func(value *heterocireceipt.Config) { value.Expectations.ImmutableIdentity = nil }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := config
			test.mutate(&candidate)
			if _, err := heterocireceipt.New(candidate); err == nil {
				t.Fatalf("New unexpectedly accepted missing %s", test.name)
			}
		})
	}
}

func validReceipt() heteroci.CapabilityQualificationReceipt {
	receipt := heteroci.CapabilityQualificationReceipt{
		SchemaVersion:        heteroci.CapabilityQualificationSchemaVersion,
		NodeID:               "node-1",
		Runtime:              heteroci.RuntimeUbuntuWineContainer,
		ProbeIdentity:        "probe-1",
		ProbeBootID:          "boot-1",
		ImageDigest:          "sha256:" + strings.Repeat("a", 64),
		SourceRevision:       "https://github.com/wine-mirror/wine@" + strings.Repeat("c", 40),
		QualificationSuite:   "wine-windows-amd64/v1",
		ImmutableIdentity:    "",
		CompletedAtUnixMilli: 1000,
	}
	receipt.Digest = receipt.CanonicalDigest()
	return receipt
}

func validConfig(receipt heteroci.CapabilityQualificationReceipt, now time.Time) heterocireceipt.Config {
	sourceRevision := receipt.SourceRevision
	immutableIdentity := receipt.ImmutableIdentity
	return heterocireceipt.Config{
		Expectations: heterocireceipt.Expectations{
			NodeID:             receipt.NodeID,
			Runtime:            receipt.Runtime,
			ProbeIdentity:      receipt.ProbeIdentity,
			ProbeBootID:        receipt.ProbeBootID,
			ImageDigest:        receipt.ImageDigest,
			SourceRevision:     &sourceRevision,
			QualificationSuite: receipt.QualificationSuite,
			ImmutableIdentity:  &immutableIdentity,
		},
		Authority:          heteroci.AuthorityLocalNonAuthoritative,
		Status:             heterocireceipt.StatusNotVerified,
		ExpiresAtUnixMilli: 2000,
		Now:                func() time.Time { return now },
	}
}

func marshalReceipt(t *testing.T, receipt heteroci.CapabilityQualificationReceipt) string {
	t.Helper()
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	return string(data)
}

func addBeforeObjectEnd(data, suffix string) string {
	return strings.TrimSuffix(data, "}") + suffix + "}"
}

func removeField(t *testing.T, data, field string) string {
	t.Helper()
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &values); err != nil {
		t.Fatalf("unmarshal receipt fixture: %v", err)
	}
	delete(values, field)
	result, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal receipt fixture: %v", err)
	}
	return string(result)
}
