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

func TestDecodeDiagnosticsContentText(t *testing.T) {
	text := "OK total=1 showing=1 truncated=0 unit=diagnostic\n" +
		"MESSAGE\tChecked+file%3A+main.go\n" +
		"ROW\tfile=main.go\tline=3\tcol=7\tseverity=warning\tmessage=unused value\tsource=gopls\tcode=U1000"
	payload := decodeDiagnosticsContentText(t, text)
	if payload.Total != 1 || !payload.HasFile("main.go") {
		t.Fatalf("diagnostics payload = %#v, want one row for main.go", payload)
	}
	if got := payload.FirstMessageForFile(t, "main.go"); got != "unused value" {
		t.Fatalf("diagnostics message = %q, want unused value", got)
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

func decodeDiagnosticsContentText(t *testing.T, text string) diagnosticsPayload {
	t.Helper()
	doc := parseDiagnosticsContentDocument(t, text)
	payload := diagnosticsPayload{Success: true, Total: doc.Header.Total}
	tables := make(map[string]int)
	for _, record := range doc.Records {
		appendDiagnosticContentRecord(t, &payload, tables, record, text)
	}
	return payload
}

func parseDiagnosticsContentDocument(t *testing.T, text string) lineprotocol.Document {
	t.Helper()
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse diagnostics line protocol: %v; text=%q", err, text)
	}
	if doc.Error != nil || doc.Header.Unit != "diagnostic" {
		t.Fatalf("diagnostics content is not a successful diagnostic document: error=%#v header=%#v text=%q", doc.Error, doc.Header, text)
	}
	return doc
}

func appendDiagnosticContentRecord(t *testing.T, payload *diagnosticsPayload, tables map[string]int, record lineprotocol.Record, text string) {
	t.Helper()
	switch record.Kind {
	case "MESSAGE":
		appendDiagnosticsMessage(payload, record.Value)
	case "ROW":
		appendDiagnosticsRow(t, payload, tables, record, text)
	}
}

func appendDiagnosticsMessage(payload *diagnosticsPayload, message string) {
	if payload.Meta.Message == "" {
		payload.Meta.Message = message
		return
	}
	payload.Meta.Message += " " + message
}

func appendDiagnosticsRow(t *testing.T, payload *diagnosticsPayload, tables map[string]int, record lineprotocol.Record, text string) {
	t.Helper()
	file := record.Fields["file"]
	if strings.TrimSpace(file) == "" {
		t.Fatalf("diagnostic ROW lacks file field: %q", text)
	}
	index := diagnosticsTableIndex(payload, tables, file)
	row := diagnosticsContentRow(record)
	payload.Data[index].Rows = append(payload.Data[index].Rows, row)
}

func diagnosticsTableIndex(payload *diagnosticsPayload, tables map[string]int, file string) int {
	if index, ok := tables[file]; ok {
		return index
	}
	index := len(payload.Data)
	tables[file] = index
	payload.Data = append(payload.Data, diagnosticsTablePayload{File: file})
	return index
}

func diagnosticsContentRow(record lineprotocol.Record) []any {
	row := []any{
		record.Fields["line"],
		record.Fields["col"],
		record.Fields["severity"],
		record.Fields["message"],
	}
	if source, code := record.Fields["source"], record.Fields["code"]; source != "" || code != "" {
		row = append(row, source, code)
	}
	return row
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
