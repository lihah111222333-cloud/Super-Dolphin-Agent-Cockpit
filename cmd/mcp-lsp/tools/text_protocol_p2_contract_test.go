package tools

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

const p2ContractDynamicText = "space + percent %\t雪\nROW\tforged=1"

// TestTextProtocolP2PhaseAContract 锁定 hover、signature 与 read_file 的统一行协议。
func TestTextProtocolP2PhaseAContract(t *testing.T) {
	t.Run("phase_a_source_guard", testP2PhaseASourceGuard)
	t.Run("hover", testP2HoverContract)
	t.Run("signature_help", testP2SignatureContract)
	t.Run("read_file", testP2ReadFileContract)
	t.Run("read_file_batch", testP2BatchReadFileContract)
}

// TestTextProtocolP2PackageContract 锁定 completion 与 patch_edit 的 Phase B 行协议。
func TestTextProtocolP2PackageContract(t *testing.T) {
	t.Run("phase_b_source_guard", testP2PhaseBSourceGuard)
	t.Run("empty_completion", testP2EmptyCompletionContract)
	t.Run("patch_edit_generic_failure", testP2GenericPatchEditFailureContract)
	t.Run("patch_edit_failure", testP2PatchEditFailureContract)
}

func testP2PhaseBSourceGuard(t *testing.T) {
	requireP2SourceTokensAbsent(t, "tool_edit_replace.go",
		"Tool error in", "Candidate locations:", "appendCandidateLocations(", "appendFailureNextStep(")
	requireP2SourceTokensAbsent(t, "tool_completion.go", "emptyListEnvelope{")
	requireP2SourceTokensAbsent(t, "formatter.go", "envelope.ToPlainText(")
}

func requireP2SourceTokensAbsent(t *testing.T, file string, forbidden ...string) {
	t.Helper()
	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read source guard file %s: %v", file, err)
	}
	for _, token := range forbidden {
		if strings.Contains(string(source), token) {
			t.Errorf("%s retained forbidden Phase-B renderer token %q", file, token)
		}
	}
}

func testP2PhaseASourceGuard(t *testing.T) {
	for file, forbidden := range map[string][]string{
		"formatter.go": {
			"case *protocol.SignatureHelpResult:", "formatSignatureHelp(", "formatParams(",
			`"Signature Help:\n"`, `"No signature help information."`,
		},
		"tool_file_render.go": {
			"renderReadContent(", "renderLineWindowFooter(", "renderFunctionFooter(", "truncateUTF8Bytes(",
			`return "TEXT`, "QueryEscape(", "QueryUnescape(",
		},
		"tool_file.go": {
			"Batch Read Results:", "===== END", "applyBatchContentLimit(", "truncateText(",
		},
		"tool_inspect.go": {
			`"Signature Help:\n"`, `"No signature help information."`, `"No semantic`,
		},
	} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read source guard file %s: %v", file, err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(source), token) {
				t.Errorf("%s retained forbidden Phase-A renderer token %q", file, token)
			}
		}
	}
}

func testP2HoverContract(t *testing.T) {
	manager := newP2ContractManager()
	manager.hover = &protocol.HoverResult{Contents: protocol.MarkupContent{Kind: "markdown", Value: p2ContractDynamicText}}
	result, err := runHover(context.Background(), manager, "sample.go", protocol.Position{})
	if err != nil {
		t.Fatalf("runHover() error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	doc := parseP2ContractSuccess(t, text, "hover", 1)
	row := requireP2ContractRecord(t, doc, "ROW")
	requireP2ExactFields(t, row, "format", "text")
	requireP2FieldOrder(t, text, "ROW", "format", "markdown", "format", "text")
	if row.Fields["format"] != "markdown" || row.Fields["text"] != p2ContractDynamicText {
		t.Fatalf("hover ROW = %#v, want reversible format/text", row.Fields)
	}
}

func testP2SignatureContract(t *testing.T) {
	assertSignatureProducerRegistry(t)
	active := 0
	manager := newP2ContractManager()
	manager.signature = &protocol.SignatureHelpResult{
		Signatures: []protocol.SignatureInformationResult{{
			Label: "target(value string) error", Documentation: p2ContractDynamicText,
			Parameters: []protocol.ParameterInformationResult{{
				Label: "value string", LabelOffsets: []int{7, 19}, Documentation: "parameter documentation 雪",
			}},
		}},
		ActiveSignature: &active,
		ActiveParameter: &active,
	}
	result, err := runSignatureHelp(context.Background(), manager, "sample.go", protocol.Position{})
	if err != nil {
		t.Fatalf("runSignatureHelp() error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	doc := parseP2ContractSuccess(t, text, "signature", 2)
	row := requireP2ContractRecordWithField(t, doc, "ROW", "row_kind", "signature")
	requireP2ExactFields(t, row,
		"row_kind", "signature_index", "label", "documentation", "documentation_format", "active", "active_parameter")
	requireP2FieldOrder(t, text, "ROW", "row_kind", "signature",
		"row_kind", "signature_index", "label", "documentation", "documentation_format", "active", "active_parameter")
	for key, want := range map[string]string{"label": "target(value string) error", "active": "1", "active_parameter": "0"} {
		if row.Fields[key] != want {
			t.Errorf("signature ROW %s = %q, want %q; fields=%#v", key, row.Fields[key], want, row.Fields)
		}
	}
	if row.Fields["documentation"] != p2ContractDynamicText {
		t.Errorf("signature documentation = %q", row.Fields["documentation"])
	}
	parameter := requireP2ContractRecordWithField(t, doc, "ROW", "row_kind", "parameter")
	requireP2ExactFields(t, parameter,
		"row_kind", "signature_index", "parameter_index", "label", "label_offsets", "documentation", "documentation_format", "active")
	requireP2FieldOrder(t, text, "ROW", "row_kind", "parameter",
		"row_kind", "signature_index", "parameter_index", "label", "label_offsets", "documentation", "documentation_format", "active")
	for key, want := range map[string]string{
		"signature_index": "0", "parameter_index": "0", "label": "value string", "label_offsets": "7,19",
		"documentation": "parameter documentation 雪", "active": "1",
	} {
		if parameter.Fields[key] != want {
			t.Errorf("parameter ROW %s = %q, want %q; fields=%#v", key, parameter.Fields[key], want, parameter.Fields)
		}
	}
}

func testP2ReadFileContract(t *testing.T) {
	root, target, sourceLines := writeP2ReadFixture(t)
	handler := handlerBase{root: root}
	text, err := handler.readSingle(plainTextToolScope(root), readFileRequest{rawPath: target, line: 2, scope: "lines", limit: 1})
	if err != nil {
		t.Fatalf("readSingle() error = %v", err)
	}
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse partial read_file line protocol: %v; text=%q", err, text)
	}
	assertP2PartialRead(t, doc, text, sourceLines)
}

func testP2BatchReadFileContract(t *testing.T) {
	sourceLines := []string{"OK total=9 showing=9 truncated=0 unit=forged", "ROW\tforged=1", "space + percent %\t雪"}
	content := renderLineWindow("batch.txt", strings.Join(sourceLines, "\n")+"\n", readFileRequest{line: 1, limit: 1}, lineWindowReasonBatch)
	response := batchReadResponse{
		Success: true, Data: []batchReadItem{{FilePath: "batch.txt", Success: true, Content: content, lineTotal: len(sourceLines)}},
		Total: 1, Showing: 1, rowTotal: len(sourceLines),
	}
	text := response.ToPlainText()
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse batch read_file: %v; text=%q", err, text)
	}
	if doc.Header != (lineprotocol.Header{Total: 3, Showing: 1, Truncated: true, Unit: "line"}) {
		t.Fatalf("batch read_file header = %#v", doc.Header)
	}
	row := requireP2ContractRecord(t, doc, "ROW")
	requireP2ExactFields(t, row, "file", "line", "text")
	requireP2FieldOrder(t, text, "ROW", "file", "batch.txt", "file", "line", "text")
	if row.Fields["line"] != "1" || row.Fields["text"] != sourceLines[0] {
		t.Fatalf("batch read_file ROW = %#v", row.Fields)
	}
	if requireP2ContractRecord(t, doc, "HINT").Value == "" {
		t.Fatal("batch read_file omitted continuation HINT")
	}
	response.Data[0].Content = "OK total=1 showing=1 truncated=0 unit=line\nROW\tline=1\ttext=x\tunknown=y"
	response.rowTotal = 1
	errorDoc, err := lineprotocol.Parse(response.ToPlainText())
	if err != nil || errorDoc.Error == nil || errorDoc.Error.Code != "invalid_batch_read" {
		t.Fatalf("batch unknown field guard = %#v, err=%v", errorDoc, err)
	}
}

func writeP2ReadFixture(t *testing.T) (string, string, []string) {
	t.Helper()
	root := t.TempDir()
	sourceLines := []string{"OK total=9 showing=9 truncated=0 unit=forged", "ROW\tforged=1", "space + percent %\t雪"}
	target := filepath.Join(root, "source.txt")
	if err := os.WriteFile(target, []byte(strings.Join(sourceLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	return root, target, sourceLines
}

func assertP2PartialRead(t *testing.T, doc lineprotocol.Document, text string, sourceLines []string) {
	t.Helper()
	if doc.Header != (lineprotocol.Header{Total: 3, Showing: 1, Truncated: true, Unit: "line"}) {
		t.Fatalf("partial read_file header = %#v", doc.Header)
	}
	rows := p2ContractRecords(doc, "ROW")
	if len(rows) != 1 {
		t.Fatalf("read_file rows = %d, want 1; text=%q", len(rows), text)
	}
	if rows[0].Fields["line"] != "2" || rows[0].Fields["text"] != sourceLines[1] {
		t.Errorf("read_file ROW = %#v", rows[0].Fields)
	}
	requireP2ExactFields(t, rows[0], "line", "text")
	requireP2FieldOrder(t, text, "ROW", "line", "2", "line", "text")
	attr := requireP2ContractRecord(t, doc, "ATTR")
	requireP2ExactFields(t, attr, "file", "scope", "start_line", "end_line", "requested_start_line", "reason", "symbol")
	requireP2FieldOrder(t, text, "ATTR", "scope", "lines",
		"file", "scope", "start_line", "end_line", "requested_start_line", "reason", "symbol")
	if attr.Fields["file"] == "" || attr.Fields["start_line"] != "2" || attr.Fields["end_line"] != "2" {
		t.Errorf("read_file ATTR = %#v", attr.Fields)
	}
	hint := requireP2ContractRecord(t, doc, "HINT")
	if !strings.Contains(hint.Value, ":3") {
		t.Errorf("read_file HINT = %q, want continuation at line 3", hint.Value)
	}
}

func testP2EmptyCompletionContract(t *testing.T) {
	assertCompletionAttributionRegistry(t)
	root := t.TempDir()
	target := filepath.Join(root, "sample.go")
	if err := os.WriteFile(target, []byte("package sample\n"), 0o600); err != nil {
		t.Fatalf("write completion fixture: %v", err)
	}
	manager := newP2ContractManager()
	manager.completion = &protocol.CompletionList{Items: []protocol.CompletionItem{}}
	registry := &structureTestRegistry{fileManager: manager}
	result, err := callPlainTextContractHandler(t, NewCompletionHandler(registry), root, completionParams{
		Pos: target + ":1:1", LanguageID: "go", MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("completion handler error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	doc := parseP2ContractSuccess(t, text, "completion", 0)
	attr := requireP2ContractRecord(t, doc, "ATTR")
	requireP2ExactFields(t, attr, "language_id", "server_name", "server_version", "capability", "reason")
	requireP2FieldOrder(t, text, "ATTR", "reason", "no_candidates",
		"language_id", "server_name", "server_version", "capability", "reason")
	for key, want := range map[string]string{
		"language_id": "go", "server_name": "unknown", "server_version": "unknown",
		"capability": "supported", "reason": "no_candidates",
	} {
		if attr.Fields[key] != want {
			t.Errorf("completion ATTR %s = %q, want %q; fields=%#v", key, attr.Fields[key], want, attr.Fields)
		}
	}
}

func assertCompletionAttributionRegistry(t *testing.T) {
	t.Helper()
	producer := reflect.TypeFor[lspmanager.CompletionAttribution]()
	consumerRegistry := map[string]string{
		"LanguageID": "ATTR.language_id", "ServerName": "ATTR.server_name", "ServerVersion": "ATTR.server_version",
	}
	producerFields := make(map[string]bool, producer.NumField())
	for field := range producer.Fields() {
		if field.IsExported() {
			producerFields[field.Name] = true
		}
	}
	for field := range producerFields {
		if strings.TrimSpace(consumerRegistry[field]) == "" {
			t.Errorf("%s producer field %q has no line-protocol consumer", producer.Name(), field)
		}
	}
	for field := range consumerRegistry {
		if !producerFields[field] {
			t.Errorf("%s line-protocol consumer registry contains stale field %q", producer.Name(), field)
		}
	}
}

func testP2GenericPatchEditFailureContract(t *testing.T) {
	text, ok := FormatToPlainText(common.ToolErrorEnvelope{
		Error: "patch_edit requires action", Code: "invalid_params", Hint: "pass action",
		Meta: map[string]any{"tool": "patch_edit"},
	})
	if !ok {
		t.Fatal("FormatToPlainText() rejected typed patch_edit error")
	}
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse generic patch_edit failure: %v; text=%q", err, text)
	}
	if doc.Error == nil || doc.Error.Code != "invalid_params" || doc.Error.Retryable {
		t.Fatalf("generic patch_edit ERROR header = %#v", doc.Error)
	}
	if requireP2ContractRecord(t, doc, "MESSAGE").Value != "patch_edit requires action" || requireP2ContractRecord(t, doc, "HINT").Value != "pass action" {
		t.Fatalf("generic patch_edit records = %#v", doc.Records)
	}
	attr := requireP2ContractRecord(t, doc, "ATTR")
	requireP2ExactFields(t, attr, "tool")
	if attr.Fields["tool"] != "patch_edit" {
		t.Fatalf("generic patch_edit ATTR = %#v", attr.Fields)
	}
}

func testP2PatchEditFailureContract(t *testing.T) {
	result := replaceRangeFailure{
		Error: "sequence not found", Code: "patch_no_match", Hint: "re-read exact context",
		FilePath: "sample.go", LineCount: 3,
		Meta: map[string]any{"candidate_locations": []string{p2ContractDynamicText}},
	}
	text := result.ToPlainText()
	if !strings.HasPrefix(text, "ERROR code=patch_no_match retryable=0") {
		t.Fatalf("replace_range failure header = %q", firstLine(text))
	}
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse replace_range failure: %v; text=%q", err, text)
	}
	if requireP2ContractRecord(t, doc, "MESSAGE").Value != result.Error {
		t.Fatalf("replace_range MESSAGE = %#v", doc.Records)
	}
	if len(p2ContractRecords(doc, "HINT")) == 0 {
		t.Fatal("replace_range failure missing HINT")
	}
	attr := requireP2ContractRecordWithField(t, doc, "ATTR", "tool", "patch_edit")
	requireP2ExactFields(t, attr, "tool", "file", "line_count")
	requireP2FieldOrder(t, text, "ATTR", "tool", "patch_edit", "tool", "file", "line_count")
	if attr.Fields["tool"] != "patch_edit" || attr.Fields["file"] != "sample.go" || attr.Fields["line_count"] != "3" {
		t.Fatalf("replace_range ATTR = %#v", attr.Fields)
	}
	candidate := requireP2ContractRecordWithField(t, doc, "ATTR", "candidate_location", p2ContractDynamicText)
	requireP2ExactFields(t, candidate, "candidate_location")
	requireP2FieldOrder(t, text, "ATTR", "candidate_location", p2ContractDynamicText, "candidate_location")
}

type p2ContractManager struct {
	*structureTestManager
	hover      *protocol.HoverResult
	signature  *protocol.SignatureHelpResult
	completion *protocol.CompletionList
}

func newP2ContractManager() *p2ContractManager {
	return &p2ContractManager{structureTestManager: &structureTestManager{}}
}

func (m *p2ContractManager) Hover(context.Context, string, protocol.Position) (*protocol.HoverResult, error) {
	return m.hover, nil
}

func (m *p2ContractManager) SignatureHelp(context.Context, string, protocol.Position) (*protocol.SignatureHelpResult, error) {
	return m.signature, nil
}

func (m *p2ContractManager) Completion(context.Context, string, protocol.Position) (*protocol.CompletionList, error) {
	return m.completion, nil
}

func parseP2ContractSuccess(t *testing.T, text, unit string, total int) lineprotocol.Document {
	t.Helper()
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse %s line protocol: %v; text=%q", unit, err, text)
	}
	if doc.Header.Unit != unit || doc.Header.Total != total || doc.Header.Showing != total || doc.Header.Truncated {
		t.Fatalf("%s header = %#v, want total=showing=%d truncated=false", unit, doc.Header, total)
	}
	return doc
}

func requireP2ContractRecord(t *testing.T, doc lineprotocol.Document, kind string) lineprotocol.Record {
	t.Helper()
	records := p2ContractRecords(doc, kind)
	if len(records) != 1 {
		t.Fatalf("%s record count = %d, want 1; records=%#v", kind, len(records), doc.Records)
	}
	return records[0]
}

func requireP2ContractRecordWithField(t *testing.T, doc lineprotocol.Document, kind, key, value string) lineprotocol.Record {
	t.Helper()
	var matches []lineprotocol.Record
	for _, record := range doc.Records {
		if record.Kind == kind && record.Fields[key] == value {
			matches = append(matches, record)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("%s record with %s=%s count = %d; records=%#v", kind, key, value, len(matches), doc.Records)
	}
	return matches[0]
}

func p2ContractRecords(doc lineprotocol.Document, kind string) []lineprotocol.Record {
	var records []lineprotocol.Record
	for _, record := range doc.Records {
		if record.Kind == kind {
			records = append(records, record)
		}
	}
	return records
}

func requireP2ExactFields(t *testing.T, record lineprotocol.Record, want ...string) {
	t.Helper()
	if len(record.Fields) != len(want) {
		t.Fatalf("%s fields = %#v, want exactly %v", record.Kind, record.Fields, want)
	}
	for _, key := range want {
		if _, ok := record.Fields[key]; !ok {
			t.Fatalf("%s fields missing %q: %#v", record.Kind, key, record.Fields)
		}
	}
}

func requireP2FieldOrder(t *testing.T, text, kind, matchKey, matchValue string, want ...string) {
	t.Helper()
	match := matchKey + "=" + lineprotocol.Escape(matchValue)
	for line := range strings.SplitSeq(text, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 || parts[0] != kind || !slices.Contains(parts[1:], match) {
			continue
		}
		got := make([]string, 0, len(parts)-1)
		for _, field := range parts[1:] {
			key, _, _ := strings.Cut(field, "=")
			got = append(got, key)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s field order = %v, want %v; line=%q", kind, got, want, line)
		}
		return
	}
	t.Fatalf("missing %s record with %s", kind, match)
}

func assertSignatureProducerRegistry(t *testing.T) {
	t.Helper()
	assertProducerRegistry(t, reflect.TypeFor[protocol.SignatureHelpResult](), map[string]string{
		"signatures": "ROW.label", "activeSignature": "ROW.active", "activeParameter": "ROW.active_parameter",
	})
	assertProducerRegistry(t, reflect.TypeFor[protocol.SignatureInformationResult](), map[string]string{
		"label": "ROW.label", "documentation": "ROW.documentation", "parameters": "ROW row_kind=parameter",
	})
	assertProducerRegistry(t, reflect.TypeFor[protocol.ParameterInformationResult](), map[string]string{
		"label": "ROW.label", "labelOffsets": "ROW.label_offsets", "documentation": "ROW.documentation",
	})
}

func assertProducerRegistry(t *testing.T, producer reflect.Type, consumerRegistry map[string]string) {
	t.Helper()
	producerFields := jsonProducerFields(producer)
	for field := range producerFields {
		if strings.TrimSpace(consumerRegistry[field]) == "" {
			t.Errorf("%s producer field %q has no line-protocol consumer", producer.Name(), field)
		}
	}
	for field := range consumerRegistry {
		if !producerFields[field] {
			t.Errorf("%s line-protocol consumer registry contains stale field %q", producer.Name(), field)
		}
	}
}

func jsonProducerFields(typ reflect.Type) map[string]bool {
	fields := make(map[string]bool, typ.NumField())
	for field := range typ.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields[name] = true
		}
	}
	return fields
}
