//go:build e2e

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

func TestRealToolsContentOnlyResultValidation(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{
			name: "plain line protocol",
			raw:  `{"result":{"content":[{"type":"text","text":"OK total=1 showing=1 truncated=0 unit=reference\nROW\tfile=main.go"}],"isError":false}}`,
		},
		{
			name:    "deprecated structured content",
			raw:     `{"result":{"content":[{"type":"text","text":"OK total=0 showing=0 truncated=0 unit=reference"}],"structuredContent":{"total":0},"isError":false}}`,
			wantErr: "structuredContent",
		},
		{
			name:    "json content text",
			raw:     `{"result":{"content":[{"type":"text","text":"{\"total\":1}"}],"isError":false}}`,
			wantErr: "JSON",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var response mcpLSPBinaryResponse
			if err := json.Unmarshal([]byte(tc.raw), &response); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}
			err := validateMCPToolSuccessResult(response)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("validate content-only result: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("validation error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestDiagnosticsContentOnlyZeroContract(t *testing.T) {
	var response mcpLSPBinaryResponse
	raw := `{"result":{"content":[{"type":"text","text":"OK total=0 showing=0 truncated=0 unit=diagnostic\nMESSAGE\tChecked+file%3A+main.go"}],"isError":false}}`
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("unmarshal diagnostics fixture: %v", err)
	}
	if err := validateZeroDiagnosticsResult(response); err != nil {
		t.Fatalf("validate zero diagnostics: %v", err)
	}
}

func validateMCPToolSuccessResult(response mcpLSPBinaryResponse) error {
	if response.Result.IsError {
		return fmt.Errorf("tools/call result has isError=true")
	}
	if strings.TrimSpace(string(response.Result.StructuredContent)) != "" {
		return fmt.Errorf("tools/call result contains deprecated structuredContent")
	}
	text := strings.TrimSpace(response.Result.ContentText())
	if text == "" {
		return fmt.Errorf("tools/call result has empty content text")
	}
	if json.Valid([]byte(text)) {
		return fmt.Errorf("tools/call content text must not be JSON")
	}
	return nil
}

func validateZeroDiagnosticsResult(response mcpLSPBinaryResponse) error {
	if err := validateMCPToolSuccessResult(response); err != nil {
		return err
	}
	doc, err := lineprotocol.Parse(response.Result.ContentText())
	if err != nil {
		return fmt.Errorf("parse diagnostics line protocol: %w", err)
	}
	if doc.Header.Unit != "diagnostic" || doc.Header.Total != 0 || doc.Header.Showing != 0 || doc.Header.Truncated {
		return fmt.Errorf("diagnostics header is not zero: %+v", doc.Header)
	}
	for _, record := range doc.Records {
		if record.Kind == "ROW" {
			return fmt.Errorf("zero diagnostics result contains ROW")
		}
	}
	return nil
}

func requireGroupedLocationTextTotal(t *testing.T, response mcpLSPBinaryResponse, minimum int, label string) {
	t.Helper()
	doc, err := lineprotocol.Parse(response.Result.ContentText())
	if err != nil {
		t.Fatalf("parse %s line protocol: %v; text=%q", label, err, response.Result.ContentText())
	}
	if doc.Header.Total < minimum {
		t.Fatalf("%s total = %d, want at least %d; text=%q", label, doc.Header.Total, minimum, response.Result.ContentText())
	}
	for _, record := range doc.Records {
		if record.Kind == "ROW" && strings.TrimSpace(record.Fields["file"]) != "" {
			return
		}
	}
	t.Fatalf("%s returned total %d but no location ROW; text=%q", label, doc.Header.Total, response.Result.ContentText())
}

func completionLabelsFromContent(t *testing.T, response mcpLSPBinaryResponse) []string {
	t.Helper()
	doc, err := lineprotocol.Parse(response.Result.ContentText())
	if err != nil {
		t.Fatalf("parse completion line protocol: %v; text=%q", err, response.Result.ContentText())
	}
	labels := make([]string, 0, doc.Header.Showing)
	for _, record := range doc.Records {
		if record.Kind == "ROW" && record.Fields["label"] != "" {
			labels = append(labels, record.Fields["label"])
		}
	}
	return labels
}
