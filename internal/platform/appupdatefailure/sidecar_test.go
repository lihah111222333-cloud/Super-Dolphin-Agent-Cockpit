package appupdatefailure

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const validFailureJSON = `{"version":2,"generation":"00112233445566778899aabbccddeeff","state":"failure","code":"UPDATE_INTEGRITY_INVALID","retryable":false,"action":"preserve_state_export_diagnostics","transaction_id":""}`

func TestSidecarRejectsEveryDuplicateJSONField(t *testing.T) {
	fields := []string{"version", "generation", "state", "code", "retryable", "action", "transaction_id"}
	for _, field := range fields {
		for _, conflict := range []bool{false, true} {
			name := field + "/same"
			if conflict {
				name = field + "/conflict"
			}
			t.Run(name, func(t *testing.T) {
				raw := duplicateFieldJSON(t, validFailureJSON, field, conflict)
				assertMalformedSidecar(t, raw)
			})
		}
	}
}

func TestSidecarRejectsMalformedExactSchema(t *testing.T) {
	tests := map[string]string{
		"unknown":  strings.Replace(validFailureJSON, `"transaction_id":""`, `"transaction_id":"","raw":"secret"`, 1),
		"missing":  strings.Replace(validFailureJSON, `"retryable":false,`, "", 1),
		"trailing": validFailureJSON + `{}`,
		"nested":   strings.Replace(validFailureJSON, `"code":"UPDATE_INTEGRITY_INVALID"`, `"code":{"value":"UPDATE_INTEGRITY_INVALID"}`, 1),
		"version":  strings.Replace(validFailureJSON, `"version":2`, `"version":1`, 1),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) { assertMalformedSidecar(t, raw) })
	}
}

func TestPendingRecordRequiresEmptyRecoveryFields(t *testing.T) {
	raw := strings.Replace(validFailureJSON, `"state":"failure"`, `"state":"pending"`, 1)
	assertMalformedSidecar(t, raw)
}

func TestGenerationValidationFailsClosed(t *testing.T) {
	for _, generation := range []string{"", "short", "../../escape", "00112233445566778899AABBCCDDEEFF"} {
		if err := ValidateGeneration(generation); err == nil {
			t.Fatalf("ValidateGeneration(%q) error = nil, want invalid generation rejection", generation)
		}
	}
	if err := ValidateGeneration("00112233445566778899aabbccddeeff"); err != nil {
		t.Fatalf("ValidateGeneration() error = %v, want valid", err)
	}
}

func TestCanonicalPath(t *testing.T) {
	if _, err := CanonicalPath(""); err == nil {
		t.Fatal("CanonicalPath(\"\") error = nil")
	}
	if _, err := CanonicalPath("/"); err == nil {
		t.Fatal("CanonicalPath(\"/\") error = nil")
	}
	if _, err := CanonicalPath("relative/path"); err == nil {
		t.Fatal("CanonicalPath(relative) error = nil")
	}

	validDir := filepath.Join(t.TempDir(), "stage")
	path, err := CanonicalPath(validDir)
	if err != nil {
		t.Fatalf("CanonicalPath(%q) error = %v", validDir, err)
	}
	if filepath.Base(path) != Filename {
		t.Fatalf("CanonicalPath() base = %q, want %q", filepath.Base(path), Filename)
	}
}

func TestNewErrorAndValidateFailure(t *testing.T) {
	sigFailure, ok := contract.RecoveryFailureForCode("UPDATE_SIGNATURE_INVALID", "")
	if !ok {
		t.Fatal("RecoveryFailureForCode(UPDATE_SIGNATURE_INVALID) = false")
	}
	errObj, err := NewError(sigFailure)
	if err != nil {
		t.Fatalf("NewError() error = %v", err)
	}
	if errObj.RecoveryFailure() != sigFailure {
		t.Fatalf("RecoveryFailure() = %+v, want %+v", errObj.RecoveryFailure(), sigFailure)
	}
	if errObj.Error() != "app update pre-journal recovery action is required" {
		t.Fatalf("Error() = %q", errObj.Error())
	}

	// Non-empty TransactionID must fail validation
	invalidFailure := sigFailure
	invalidFailure.TransactionID = "tx-123"
	if _, err := NewError(invalidFailure); err == nil {
		t.Fatal("NewError(with TransactionID) error = nil, want validation error")
	}

	// Unknown recovery code must fail
	unknownFailure := contract.RecoveryFailure{Code: "UNKNOWN_CODE"}
	if _, err := NewError(unknownFailure); err == nil {
		t.Fatal("NewError(unknown code) error = nil, want validation error")
	}
}

func TestRecordEncodeDecodeRoundTrip(t *testing.T) {
	gen := "00112233445566778899aabbccddeeff"
	pending := pendingRecord(gen)
	rawPending, err := encodeRecord(pending)
	if err != nil {
		t.Fatalf("encodeRecord(pending) error = %v", err)
	}
	decodedPending, err := decodeRecord(rawPending)
	if err != nil {
		t.Fatalf("decodeRecord(pending) error = %v", err)
	}
	if decodedPending != pending {
		t.Fatalf("decodedPending = %+v, want %+v", decodedPending, pending)
	}

	failure, ok := contract.RecoveryFailureForCode("UPDATE_SIGNATURE_INVALID", "")
	if !ok {
		t.Fatal("RecoveryFailureForCode = false")
	}
	failRec := failureRecord(gen, failure)
	rawFail, err := encodeRecord(failRec)
	if err != nil {
		t.Fatalf("encodeRecord(failure) error = %v", err)
	}
	decodedFail, err := decodeRecord(rawFail)
	if err != nil {
		t.Fatalf("decodeRecord(failure) error = %v", err)
	}
	if decodedFail != failRec {
		t.Fatalf("decodedFail = %+v, want %+v", decodedFail, failRec)
	}
}

func TestValidateRecord(t *testing.T) {
	gen := "00112233445566778899aabbccddeeff"
	failure, _ := contract.RecoveryFailureForCode("UPDATE_SIGNATURE_INVALID", "")

	// Valid records
	if err := validateRecord(pendingRecord(gen)); err != nil {
		t.Fatalf("validateRecord(pending) error = %v", err)
	}
	if err := validateRecord(failureRecord(gen, failure)); err != nil {
		t.Fatalf("validateRecord(failure) error = %v", err)
	}

	// Invalid version
	rec := pendingRecord(gen)
	rec.Version = 99
	if err := validateRecord(rec); err == nil {
		t.Fatal("validateRecord(bad version) error = nil")
	}

	// Invalid generation
	rec = pendingRecord("invalid-gen")
	if err := validateRecord(rec); err == nil {
		t.Fatal("validateRecord(bad gen) error = nil")
	}

	// Non-empty TransactionID
	rec = failureRecord(gen, failure)
	rec.TransactionID = "tx-123"
	if err := validateRecord(rec); err == nil {
		t.Fatal("validateRecord(non-empty tx) error = nil")
	}

	// Unsupported state
	rec = pendingRecord(gen)
	rec.State = "running"
	if err := validateRecord(rec); err == nil {
		t.Fatal("validateRecord(unsupported state) error = nil")
	}
}

func TestFailCodeUnknown(t *testing.T) {
	if err := FailCode("/unused", "00112233445566778899aabbccddeeff", "NON_EXISTENT_CODE"); err == nil {
		t.Fatal("FailCode(unknown code) error = nil, want unregistered code error")
	}
}

func duplicateFieldJSON(t *testing.T, raw string, field string, conflict bool) string {
	t.Helper()
	needle, _ := jsonField(t, raw, field)
	duplicate := needle
	if conflict {
		switch field {
		case "version":
			duplicate = `"version":99`
		case "retryable":
			duplicate = `"retryable":true`
		default:
			duplicate = fmt.Sprintf(`%q:%q`, field, "conflict")
		}
	}
	return strings.Replace(raw, needle, needle+","+duplicate, 1)
}

func jsonField(t *testing.T, raw string, field string) (string, string) {
	t.Helper()
	prefix := fmt.Sprintf(`%q:`, field)
	start := strings.Index(raw, prefix)
	if start < 0 {
		t.Fatalf("field %q missing", field)
	}
	rest := raw[start+len(prefix):]
	end := strings.IndexByte(rest, ',')
	if end < 0 {
		end = strings.IndexByte(rest, '}')
	}
	value := rest[:end]
	return prefix + value, value
}

func assertMalformedSidecar(t *testing.T, raw string) {
	t.Helper()
	if _, err := decodeRecord([]byte(raw)); err == nil {
		t.Fatal("decodeRecord() error = nil, want malformed rejection")
	}
}
