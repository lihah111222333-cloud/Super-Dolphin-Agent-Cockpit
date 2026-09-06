package recoverycustody

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReceiptDigestContract(t *testing.T) {
	handle := validHandle(t, validRequest(t))
	receipt := validReceipt(t, handle, ReceiptAvailability, "golden")
	got, err := ComputeReceiptDigest(receipt)
	assertNoError(t, err)
	const want = "228d9bcd17f69f8a675e1d0c274cc01ad76fb8ea0f2b601c732c609eb1f7cfc4"
	if got != want {
		t.Fatalf("receipt digest golden = %q, want %q", got, want)
	}
	if receipt.ReceiptDigest != got {
		t.Fatalf("finalized receipt digest = %q, want %q", receipt.ReceiptDigest, got)
	}
	tampered := receipt
	tampered.HeadRootDigest = testDigest(991)
	assertErrorIs(t, tampered.Validate(), ErrInvalidReceipt)
}

func TestReceiptDigestRejectsProofMetadataTampering(t *testing.T) {
	handle := validHandle(t, validRequest(t))
	base := validReceipt(t, handle, ReceiptAvailability, "proof-tamper")
	for _, test := range []struct {
		name   string
		mutate func(*ExternalReceipt)
	}{
		{name: "quorum_digest", mutate: func(receipt *ExternalReceipt) {
			receipt.QuorumAttestationDigest = testDigest(701)
		}},
		{name: "quorum_ref", mutate: func(receipt *ExternalReceipt) {
			receipt.QuorumAttestationRef = "quorum://external/tampered/" + receipt.QuorumAttestationDigest
		}},
		{name: "signature_digest", mutate: func(receipt *ExternalReceipt) {
			receipt.SignatureDigest = testDigest(702)
		}},
		{name: "signature_ref", mutate: func(receipt *ExternalReceipt) {
			receipt.SignatureRef = "signature://external/tampered/" + receipt.SignatureDigest
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidReceipt) {
				t.Fatalf("Validate() error = %v, want ErrInvalidReceipt", err)
			}
		})
	}
}

func TestReceiptDigestV1IsRejectedWithoutCompatibilityFallback(t *testing.T) {
	handle := validHandle(t, validRequest(t))
	// This is the pre-fix v1 golden generated while proof metadata was omitted.
	receipt := validReceipt(t, handle, ReceiptAvailability, "golden")
	receipt.ReceiptDigest = "ffe7d25552241fea7e35bb5f8eeefec111b50738dd9cfa57402447cfef3d12fc"
	if err := receipt.Validate(); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("Validate() with pre-v2 domain digest error = %v, want ErrInvalidReceipt", err)
	}
}

func TestExternalReceiptJSONRoundTrip(t *testing.T) {
	handle := validHandle(t, validRequest(t))
	want := validReceipt(t, handle, ReceiptAbort, "json-roundtrip")
	encoded, err := json.Marshal(want)
	assertNoError(t, err)
	var got ExternalReceipt
	assertNoError(t, json.Unmarshal(encoded, &got))
	assertNoError(t, got.Validate())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExternalReceipt JSON roundtrip = %+v, want %+v", got, want)
	}
}

func TestExternalReceiptRejectsInvalidEvidenceAndRetention(t *testing.T) {
	handle := validHandle(t, validRequest(t))
	base := validReceipt(t, handle, ReceiptAvailability, "invalid-shape")
	tests := []struct {
		name   string
		mutate func(*ExternalReceipt)
	}{
		{"arbitrary_receipt_digest", func(value *ExternalReceipt) { value.ReceiptDigest = testDigest(999) }},
		{"uppercase_quorum_digest", func(value *ExternalReceipt) {
			value.QuorumAttestationDigest = strings.Repeat("A", 64)
		}},
		{"wrong_quorum_scheme", func(value *ExternalReceipt) {
			value.QuorumAttestationRef = "file://external/" + value.QuorumAttestationDigest
		}},
		{"quorum_ref_digest_mismatch", func(value *ExternalReceipt) {
			value.QuorumAttestationRef = "quorum://external/" + testDigest(998)
		}},
		{"signature_ref_query", func(value *ExternalReceipt) { value.SignatureRef += "?mutable=true" }},
		{"zero_retention", func(value *ExternalReceipt) { value.RetentionUntil = time.Time{} }},
		{"retention_before_authority", func(value *ExternalReceipt) {
			value.RetentionUntil = value.AuthorityTime.Add(-time.Second)
		}},
		{"gc_before_anchor", func(value *ExternalReceipt) {
			value.GCNotBeforeAnchorGeneration = value.AntiRollbackGeneration - 1
		}},
		{"missing_owner_generation", func(value *ExternalReceipt) { value.OwnerGeneration = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidReceipt) {
				t.Fatalf("Validate() error = %v, want ErrInvalidReceipt", err)
			}
		})
	}
}
