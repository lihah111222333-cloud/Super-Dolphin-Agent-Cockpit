package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

type xrefReferencesManager struct {
	structureTestManager
	gotIncludeDeclaration bool
	gotPosition           protocol.Position
	gotFilePath           string
}

func (m *xrefReferencesManager) References(_ context.Context, filePath string, position protocol.Position, includeDeclaration bool) ([]protocol.LocationResult, error) {
	m.gotIncludeDeclaration = includeDeclaration
	m.gotPosition = position
	m.gotFilePath = filePath
	return nil, nil
}

func TestReferencesDefaultIncludesDeclaration(t *testing.T) {
	dir, filePath := writeXRefFixture(t)
	manager := &xrefReferencesManager{}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(xrefParams{
		Action: "references",
		Pos:    filePath + ":1:1",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input); err != nil {
		t.Fatalf("references returned error: %v", err)
	}
	if !manager.gotIncludeDeclaration {
		t.Fatalf("includeDeclaration = false, want default true")
	}
}

func TestReferencesCanDisableDeclaration(t *testing.T) {
	dir, filePath := writeXRefFixture(t)
	manager := &xrefReferencesManager{gotIncludeDeclaration: true}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	includeDeclaration := false
	input, err := json.Marshal(xrefParams{
		Action:             "references",
		Pos:                filePath + ":1:1",
		IncludeDeclaration: &includeDeclaration,
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input); err != nil {
		t.Fatalf("references returned error: %v", err)
	}
	if manager.gotIncludeDeclaration {
		t.Fatalf("includeDeclaration = true, want explicit false")
	}
}

func TestCallHierarchyEmptyTypeScriptResultExplainsBootstrapOrCursor(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "app.ts")
	if err := os.WriteFile(filePath, []byte("export function greet(name: string) { return name }\n"), 0o644); err != nil {
		t.Fatalf("write xref fixture: %v", err)
	}
	manager := &structureTestManager{}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(xrefParams{
		Action:     "call_hierarchy",
		Pos:        filePath + ":1:17",
		LanguageID: "typescript",
		Direction:  "outgoing",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input)
	if err != nil {
		t.Fatalf("call_hierarchy returned error: %v", err)
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var decoded struct {
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode result: %v; raw=%s", err, payload)
	}
	hint := strings.ToLower(decoded.Hint)
	for _, want := range []string{"js/ts", "bootstrap", "cursor"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("empty call hierarchy hint = %q, want %q; raw=%s", decoded.Hint, want, payload)
		}
	}
}

func writeXRefFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(filePath, []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write xref fixture: %v", err)
	}
	return dir, filePath
}

func TestXRefReferencesAutoSnapTwoPartPos(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "app.go")
	if err := os.WriteFile(filePath, []byte("package app\nfunc CalculateSum() int { return 0 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &xrefReferencesManager{}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(xrefParams{
		Action: "references",
		Pos:    filePath + ":2",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input); err != nil {
		t.Fatalf("references with two-part pos error: %v", err)
	}
	// "func CalculateSum() int" -> "CalculateSum" starts at char 5
	want := protocol.Position{Line: 1, Character: 5}
	if manager.gotPosition != want {
		t.Fatalf("manager position = %#v, want %#v (auto-snapped from two-part pos)", manager.gotPosition, want)
	}
}

func TestXRefReferencesAutoSnapIndentationAndKeywords(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "service.py")
	if err := os.WriteFile(filePath, []byte("    def process_order(order_id):\n        pass\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &xrefReferencesManager{}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})

	// Pass pos pointing to line 1 col 1 (leading indentation)
	input, err := json.Marshal(xrefParams{
		Action: "references",
		Pos:    filePath + ":1:1",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input); err != nil {
		t.Fatalf("references error: %v", err)
	}
	// "    def process_order" -> "process_order" starts at char 8
	want := protocol.Position{Line: 0, Character: 8}
	if manager.gotPosition != want {
		t.Fatalf("manager position = %#v, want %#v (auto-snapped from indentation)", manager.gotPosition, want)
	}
}

func TestXRefReferencesTruncationHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "app.go")
	if err := os.WriteFile(filePath, []byte("package app\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	results := make([]protocol.LocationResult, 20)
	for i := range 20 {
		results[i] = protocol.LocationResult{
			Location: &protocol.Location{
				URI: "file://" + filePath,
				Range: protocol.Range{
					Start: protocol.Position{Line: i, Character: 0},
					End:   protocol.Position{Line: i, Character: 5},
				},
			},
		}
	}
	manager := &xrefImportTopologyManager{
		refResults: results,
	}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})

	input, err := json.Marshal(xrefParams{
		Action:     "references",
		Pos:        filePath + ":2:6",
		MaxResults: 15, // truncate at 15
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input)
	if err != nil {
		t.Fatalf("references returned error: %v", err)
	}
	assertTruncatedReferences(t, got, filePath)
}

func assertTruncatedReferences(t *testing.T, got any, filePath string) {
	t.Helper()
	grouped := got.(protocol.GroupedLocationResult)
	if !grouped.Truncated {
		t.Fatalf("expected truncated results")
	}
	assertTruncatedReferenceHint(t, grouped)
	assertReferenceContinuation(t, grouped, filePath)
	if strings.Contains(grouped.Hint, "external_sampling") {
		t.Fatalf("ordinary max_results hint = %q, must not report external_sampling", grouped.Hint)
	}
}

func assertTruncatedReferenceHint(t *testing.T, grouped protocol.GroupedLocationResult) {
	t.Helper()
	if !strings.Contains(grouped.Hint, "next: increase max_results or narrow the target with pos=<file>:<line>:<col>") {
		t.Fatalf("truncated hint = %q, want supported pos narrowing guidance", grouped.Hint)
	}
	if !strings.Contains(grouped.Hint, "(+ 5 more references in this file, total 20 across 1 files)") {
		t.Fatalf("truncated hint = %q, want summary hint (+ 5 more references in this file, total 20 across 1 files)", grouped.Hint)
	}
	for _, want := range []string{
		"truncation: reason=max_results",
		"max_results=15",
		"per_file=[file=app.go,total=20,showing=15]",
	} {
		if !strings.Contains(grouped.Hint, want) {
			t.Fatalf("truncated hint = %q, want metadata %q", grouped.Hint, want)
		}
	}
}

func assertReferenceContinuation(t *testing.T, grouped protocol.GroupedLocationResult, filePath string) {
	t.Helper()
	if grouped.Continuation == nil {
		t.Fatal("truncated references continuation = nil, want stable continuation")
	}
	if grouped.Continuation.Token == "" || grouped.Continuation.Reason != "max_results" {
		t.Fatalf("references continuation = %#v, want stable token and max_results reason", grouped.Continuation)
	}
	if got := grouped.Continuation.NextArgs["action"]; got != "references" {
		t.Fatalf("references continuation action = %#v, want references", got)
	}
	if got := grouped.Continuation.NextArgs["pos"]; got != filePath+":2:6" {
		t.Fatalf("references continuation pos = %#v, want original position", got)
	}
	plainText, handled := FormatToPlainText(grouped)
	if !handled || !strings.Contains(plainText, "CONTINUATION") || !strings.Contains(plainText, grouped.Continuation.Token) {
		t.Fatalf("references continuation missing from plain text: handled=%t text=%q", handled, plainText)
	}
}

func TestXRefReferencesExternalConsumptionSamplingAndSummaryHint(t *testing.T) {
	dir := t.TempDir()
	currFile := filepath.Join(dir, "app.go")
	extFile1 := filepath.Join(dir, "consumer1.go")
	extFile2 := filepath.Join(dir, "consumer2.go")

	if err := os.WriteFile(currFile, []byte("package main\nfunc Target() {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	results := externalReferenceResults(currFile, extFile1, extFile2)

	manager := &xrefImportTopologyManager{refResults: results}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})

	input, err := json.Marshal(xrefParams{
		Action:     "references",
		Pos:        currFile + ":2:6",
		MaxResults: 30,
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input)
	if err != nil {
		t.Fatalf("references returned error: %v", err)
	}
	grouped := got.(protocol.GroupedLocationResult)
	assertExternalConsumption(t, grouped, dir, currFile, extFile1, extFile2)
}

func externalReferenceResults(current, firstExternal, secondExternal string) []protocol.LocationResult {
	results := make([]protocol.LocationResult, 0, 20)
	results = appendReferenceResults(results, current, 8)
	results = appendReferenceResults(results, firstExternal, 9)
	return appendReferenceResults(results, secondExternal, 3)
}

func appendReferenceResults(results []protocol.LocationResult, filePath string, count int) []protocol.LocationResult {
	for index := 0; index < count; index++ {
		results = append(results, protocol.LocationResult{Location: &protocol.Location{
			URI: "file://" + filePath,
			Range: protocol.Range{
				Start: protocol.Position{Line: index + 1, Character: 0},
				End:   protocol.Position{Line: index + 1, Character: 6},
			},
		}})
	}
	return results
}

func assertExternalConsumption(t *testing.T, grouped protocol.GroupedLocationResult, dir, current, firstExternal, secondExternal string) {
	t.Helper()
	assertReferenceCount(t, grouped, dir, firstExternal, 5, "external file 1")
	assertReferenceCount(t, grouped, dir, secondExternal, 3, "external file 2")
	assertReferenceCount(t, grouped, dir, current, 8, "current file")
	if grouped.Total != 20 || !strings.Contains(grouped.Hint, "total 20 across 3 files") || !grouped.Truncated {
		t.Fatalf("external sampling result total/truncated/hint = %d/%t/%q", grouped.Total, grouped.Truncated, grouped.Hint)
	}
	if len(grouped.SampledFiles) != 1 || grouped.SampledFiles[0] != "consumer1.go" {
		t.Fatalf("sampled files = %#v, want [consumer1.go]", grouped.SampledFiles)
	}
	for _, want := range []string{
		"truncation: reason=external_sampling",
		"max_results=30",
		"per_file=[file=app.go,total=8,showing=8;file=consumer1.go,total=9,showing=5,sampled=external_sampling;file=consumer2.go,total=3,showing=3]",
		"sampled_files=[consumer1.go]",
		"narrow the target with pos=<file>:<line>:<col>",
		"external_sampling cannot be recovered by increasing max_results",
	} {
		if !strings.Contains(grouped.Hint, want) {
			t.Fatalf("external sampling hint = %q, want metadata/action %q", grouped.Hint, want)
		}
	}
	if grouped.Continuation == nil {
		t.Fatal("external sampling continuation = nil, want stable continuation")
	}
	if grouped.Continuation.Reason != "external_sampling" {
		t.Fatalf("external sampling continuation reason = %q, want external_sampling", grouped.Continuation.Reason)
	}
	if got := grouped.Continuation.NextArgs["action"]; got != "references" {
		t.Fatalf("external sampling continuation action = %#v, want references", got)
	}
}

func assertReferenceCount(t *testing.T, grouped protocol.GroupedLocationResult, dir, filePath string, want int, label string) {
	t.Helper()
	relative, err := filepath.Rel(dir, filePath)
	if err != nil {
		t.Fatalf("%s relative path: %v", label, err)
	}
	relative = filepath.ToSlash(relative)
	if len(grouped.Data[relative]) != want {
		t.Fatalf("%s rows = %d, want %d; Data=%v", label, len(grouped.Data[relative]), want, grouped.Data)
	}
}
