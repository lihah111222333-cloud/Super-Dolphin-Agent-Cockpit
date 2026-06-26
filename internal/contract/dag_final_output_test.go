package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFinalOutputFileFromRunMetadata(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		metadata   json.RawMessage
		wantOK     bool
		wantPath   string
		wantSource string
	}{
		{
			name: "file path",
			metadata: json.RawMessage(`{
				"final_output": {
					"kind": "file",
					"path": "reports/daily-brief.pptx",
					"source_node_key": "report"
				}
			}`),
			wantOK:     true,
			wantPath:   "reports/daily-brief.pptx",
			wantSource: "report",
		},
		{
			name: "legacy sharedfile envelope",
			metadata: json.RawMessage(`{
				"final_output": {
					"role": "final_output",
					"sharedfile": {"path": "reports/final.md"}
				}
			}`),
			wantOK:   true,
			wantPath: "reports/final.md",
		},
		{
			name:     "text output is not a sharedfile",
			metadata: json.RawMessage(`{"final_output":{"kind":"text","text":"done"}}`),
			wantOK:   false,
		},
		{
			name:     "missing final output",
			metadata: json.RawMessage(`{"note":"none"}`),
			wantOK:   false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := FinalOutputFileFromRunMetadata(tc.metadata)
			if ok != tc.wantOK {
				t.Fatalf("FinalOutputFileFromRunMetadata() ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got.Path != tc.wantPath || got.SourceNodeKey != tc.wantSource {
				t.Fatalf("FinalOutputFileFromRunMetadata() = %#v", got)
			}
		})
	}
}

func TestFinalOutputFileFromRunMetadataStrictReportsMalformedInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		metadata json.RawMessage
		wantText string
	}{
		{
			name:     "malformed metadata",
			metadata: json.RawMessage(`{"final_output":`),
			wantText: "metadata",
		},
		{
			name:     "malformed final output shape",
			metadata: json.RawMessage(`{"final_output":{"sharedfile":"reports/final.md"}}`),
			wantText: "final_output",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok, err := FinalOutputFileFromRunMetadataStrict(tc.metadata)
			if err == nil {
				t.Fatalf("FinalOutputFileFromRunMetadataStrict() error = nil, want explicit parse error")
			}
			if ok {
				t.Fatalf("FinalOutputFileFromRunMetadataStrict() ok = true with %#v", got)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("FinalOutputFileFromRunMetadataStrict() error = %q, want %q", err.Error(), tc.wantText)
			}
		})
	}
}
