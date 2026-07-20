//go:build darwin

package appupdatefailure

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	stageDir := privateStageDir(t)
	if err := os.WriteFile(filepath.Join(stageDir, Filename), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFailure(stageDir); err == nil {
		t.Fatal("ReadFailure() error = nil, want malformed rejection")
	}
}
