package mcpwire

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNegotiateProtocolVersionDoesNotEchoUnsupportedVersion(t *testing.T) {
	got, err := NegotiateProtocolVersion("2099-01-01")
	if err != nil {
		t.Fatalf("NegotiateProtocolVersion() error = %v", err)
	}
	if got != LatestProtocolVersion {
		t.Fatalf("NegotiateProtocolVersion() = %q, want %q", got, LatestProtocolVersion)
	}
}

func TestNegotiateProtocolVersionRejectsUnsafeValue(t *testing.T) {
	for _, value := range []string{"", " " + LatestProtocolVersion, LatestProtocolVersion + "\r\nX-Evil: yes"} {
		t.Run(strings.ReplaceAll(value, "\n", `\n`), func(t *testing.T) {
			if _, err := NegotiateProtocolVersion(value); err == nil {
				t.Fatalf("NegotiateProtocolVersion(%q) error = nil", value)
			}
		})
	}
}

func TestDecodeInitializeProtocolVersionStrict(t *testing.T) {
	tests := []struct {
		name    string
		raw     json.RawMessage
		want    string
		wantErr bool
	}{
		{name: "latest", raw: json.RawMessage(`{"protocolVersion":"2025-11-25"}`), want: LatestProtocolVersion},
		{name: "legacy", raw: json.RawMessage(`{"protocolVersion":"2024-11-05"}`), want: LegacyProtocolVersion},
		{name: "missing", raw: json.RawMessage(`{"capabilities":{}}`), wantErr: true},
		{name: "wrong type", raw: json.RawMessage(`{"protocolVersion":1}`), wantErr: true},
		{name: "unsupported", raw: json.RawMessage(`{"protocolVersion":"2099-01-01"}`), wantErr: true},
		{name: "control", raw: json.RawMessage(`{"protocolVersion":"2025-11-25\r\nX-Evil: yes"}`), wantErr: true},
		{name: "array", raw: json.RawMessage(`[]`), wantErr: true},
		{name: "null", raw: json.RawMessage(`null`), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeInitializeProtocolVersion(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("DecodeInitializeProtocolVersion(%s) error = nil", tt.raw)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("DecodeInitializeProtocolVersion(%s) = %q, %v; want %q", tt.raw, got, err, tt.want)
			}
		})
	}
}

func TestDefaultToolErrorClassifiersReturnFreshSlice(t *testing.T) {
	first := defaultToolErrorClassifiers()
	if len(first) == 0 {
		t.Fatal("defaultToolErrorClassifiers() returned no classifiers")
	}
	first[0].code = "mutated"
	if got := defaultToolErrorClassifiers()[0].code; got == "mutated" {
		t.Fatal("defaultToolErrorClassifiers() leaked caller mutation")
	}
}

func TestClassifyToolErrorKeepsDefaultAndCallerPrecedence(t *testing.T) {
	code, retryable, hint, meta := ClassifyToolError("patch_edit", errors.New("ambiguous match"))
	if code != "patch_ambiguous" || retryable || hint == "" || meta != nil {
		t.Fatalf("default classification = (%q, %v, %q, %#v), want patch_ambiguous non-retryable with hint and nil meta", code, retryable, hint, meta)
	}
	code, retryable, hint, meta = ClassifyToolErrorWithClassifier("patch_edit", errors.New("ambiguous match"), func(string, error) (ToolErrorClassification, bool) {
		return ToolErrorClassification{Code: "caller_override", Retryable: true, Hint: "caller hint", Meta: map[string]any{"source": "caller"}}, true
	})
	if code != "caller_override" || !retryable || hint != "caller hint" || meta["source"] != "caller" {
		t.Fatalf("caller classification = (%q, %v, %q, %#v), want caller override", code, retryable, hint, meta)
	}
}

func TestScopeTooBroadHintUsesWorkspaceSymbolSchemaFields(t *testing.T) {
	code, retryable, hint, _ := ClassifyToolError("structure", errors.New("project walk entry limit exceeded"))
	if code != "scope_too_broad" || !retryable {
		t.Fatalf("classification = (%q, %v), want scope_too_broad retryable", code, retryable)
	}
	if strings.Contains(hint, "file_path=") || !strings.Contains(hint, "work_dir=") {
		t.Fatalf("hint = %q, want schema-valid work_dir narrowing without file_path", hint)
	}
}

func TestClassifyToolErrorTreatsForeignPlatformPathAsOutsideWorkspace(t *testing.T) {
	code, retryable, hint, meta := ClassifyToolError("file", errors.New(`path "C:\\repo\\main.go" belongs to a foreign platform path family`))
	if code != "path_outside_workspace" || retryable || hint == "" || meta != nil {
		t.Fatalf("foreign path classification = (%q, %v, %q, %#v), want path_outside_workspace non-retryable with hint", code, retryable, hint, meta)
	}
}

func TestDefaultToolErrorClassifiersKeepOriginalPrecedenceOrder(t *testing.T) {
	want := []string{
		"patch_no_match", "patch_ambiguous", "database_schema_missing", "internal_panic",
		"capability_unsupported", "language_unsupported", "schema_invalid", "cwd_required",
		"cwd_invalid", "provider_required", "provider_invalid", "launch_request_invalid",
		"invalid_input", "lsp_unavailable", "dependency_missing", "identifier_not_found",
		"file_not_found", "scope_too_broad", "path_invalid", "path_outside_workspace", "lsp_timeout",
		"position_invalid", "lsp_client_closed", "scope_ambiguous",
	}
	got := defaultToolErrorClassifiers()
	if len(got) != len(want) {
		t.Fatalf("default classifier count = %d, want %d", len(got), len(want))
	}
	for index, code := range want {
		if got[index].code != code {
			t.Fatalf("classifier[%d] = %q, want %q", index, got[index].code, code)
		}
	}
}
