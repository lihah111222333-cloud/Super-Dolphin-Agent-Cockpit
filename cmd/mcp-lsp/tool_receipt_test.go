package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestNewLSPToolReceiptSinkRequiresExplicitConfiguration(t *testing.T) {
	t.Setenv(mcpLSPToolReceiptLogPathEnv, "")
	t.Setenv(mcpLSPToolReceiptKeyPathEnv, "")
	_, err := newLSPToolReceiptSinkFromEnv()
	if err == nil || !strings.Contains(err.Error(), mcpLSPToolReceiptLogPathEnv) || !strings.Contains(err.Error(), mcpLSPToolReceiptKeyPathEnv) {
		t.Fatalf("newLSPToolReceiptSinkFromEnv() error = %v, want both explicit path names", err)
	}
}

func TestLSPToolReceiptSinkSignsAndChainsAppendOnlyRecords(t *testing.T) {
	root := t.TempDir()
	keyPath, publicKey := writeLSPToolReceiptTestKey(t, root)
	logPath := filepath.Join(root, "receipts", "mcp-lsp.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	sink, err := newLSPToolReceiptSink(logPath, keyPath)
	if err != nil {
		t.Fatalf("newLSPToolReceiptSink() error = %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		AgentID: "agent-1", ThreadID: "thread-1", TurnID: "turn-1", CallID: "call-1", CWD: "/secret/workspace",
		WorkspaceRoots: []string{"/secret/workspace"}, Family: "lsp",
	})
	started := time.Unix(10, 0)
	completed := started.Add(12 * time.Millisecond)
	appendLSPToolReceiptSamples(t, sink, ctx, started, completed)
	if err := sink.Close(); err != nil {
		t.Fatalf("close receipt sink: %v", err)
	}
	lines := readReceiptLines(t, logPath)
	if len(lines) != 2 {
		t.Fatalf("receipt line count = %d, want 2", len(lines))
	}
	assertLSPToolReceiptChain(t, lines, publicKey)
	reopened, err := newLSPToolReceiptSink(logPath, keyPath)
	if err != nil {
		t.Fatalf("reopen valid append-only receipt log: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened receipt sink: %v", err)
	}
}

func TestLSPToolReceiptSinkCanonicalIOModeIsExplicitAndSigned(t *testing.T) {
	root := t.TempDir()
	keyPath, publicKey := writeLSPToolReceiptTestKey(t, root)
	logPath := filepath.Join(root, "canonical.jsonl")
	sink, err := newLSPToolReceiptSink(logPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	sink.includeCanonicalIO = true
	args := json.RawMessage(`{"action":"document_symbol","file_path":"src/a.go"}`)
	result := map[string]any{"rows": []any{map[string]any{"path": "src/a.go", "line": 3}}}
	if err := sink.Append(context.Background(), "structure", args, result, nil, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	var record lspToolReceiptRecord
	if err := decodeReceiptJSON(readReceiptLines(t, logPath)[0], &record); err != nil {
		t.Fatal(err)
	}
	if record.Schema != mcpLSPCanonicalReceiptSchema || len(record.Payload.Arguments) == 0 || len(record.Payload.Result) == 0 {
		t.Fatalf("canonical record = %+v", record)
	}
	if err := validateLSPToolReceiptRecord(record, 1, "", publicKey); err != nil {
		t.Fatal(err)
	}
}

func appendLSPToolReceiptSamples(t *testing.T, sink *lspToolReceiptSink, ctx context.Context, started, completed time.Time) {
	t.Helper()
	if err := sink.Append(ctx, "structure", json.RawMessage(`{"action":"document_symbol","file_path":"/secret/source.go"}`), map[string]any{"private_result": "/secret/result"}, nil, started, completed); err != nil {
		t.Fatalf("append success receipt: %v", err)
	}
	if err := sink.Append(ctx, "xref", json.RawMessage(`{"action":"references","pos":"/secret/source.go:1:1"}`), nil, errors.New("private error /secret/error"), started, completed); err != nil {
		t.Fatalf("append error receipt: %v", err)
	}
}

func assertLSPToolReceiptChain(t *testing.T, lines [][]byte, publicKey ed25519.PublicKey) {
	t.Helper()
	previous := ""
	for index, line := range lines {
		previous = assertLSPToolReceiptLine(t, line, publicKey, uint64(index+1), previous)
	}
}

func assertLSPToolReceiptLine(t *testing.T, line []byte, publicKey ed25519.PublicKey, sequence uint64, previous string) string {
	t.Helper()
	var record lspToolReceiptRecord
	if err := decodeReceiptJSON(line, &record); err != nil {
		t.Fatalf("decode receipt line %d: %v", sequence, err)
	}
	if err := validateLSPToolReceiptRecord(record, sequence, previous, publicKey); err != nil {
		t.Fatalf("validate receipt line %d: %v", sequence, err)
	}
	if strings.Contains(string(line), "/secret/") || strings.Contains(string(line), "private_result") || strings.Contains(string(line), "private error") {
		t.Fatalf("receipt line %d leaks raw payload: %s", sequence, line)
	}
	if record.Payload.AgentID != "agent-1" || record.Payload.CWDHash == "" {
		t.Fatalf("receipt payload scope = %+v, want identity and hashed cwd", record.Payload)
	}
	return record.EventDigest
}

func TestLSPToolReceiptSinkRejectsTamperedExistingRecord(t *testing.T) {
	root := t.TempDir()
	keyPath, _ := writeLSPToolReceiptTestKey(t, root)
	logPath := filepath.Join(root, "receipt.jsonl")
	sink, err := newLSPToolReceiptSink(logPath, keyPath)
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	if err := sink.Append(context.Background(), "diagnostics", json.RawMessage(`{"action":"diagnostics"}`), "result", nil, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatalf("append receipt: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("close sink: %v", err)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.Replace(string(contents), "diagnostics", "structure", 1))
	if err := os.WriteFile(logPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newLSPToolReceiptSink(logPath, keyPath); err == nil || (!strings.Contains(err.Error(), "signature") && !strings.Contains(err.Error(), "event digest")) {
		t.Fatalf("reopen tampered receipt error = %v, want signed-content validation failure", err)
	}
}

func TestRegistryToolProviderWritesSemanticReceiptOutOfBand(t *testing.T) {
	root := t.TempDir()
	keyPath, _ := writeLSPToolReceiptTestKey(t, root)
	logPath := filepath.Join(root, "receipt.jsonl")
	sink, err := newLSPToolReceiptSink(logPath, keyPath)
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}
	provider := registryToolProvider{
		defs: []toolDefinition{{Manifest: ToolManifest{Name: "structure"}, Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"result": "visible only through return value"}, nil
		}}},
		receiptSink: sink,
	}
	result, err := provider.CallTool(common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}}), "structure", json.RawMessage(`{"action":"document_symbol"}`))
	if err != nil {
		t.Fatalf("provider CallTool() error = %v", err)
	}
	if result == nil {
		t.Fatal("provider CallTool() result = nil, want handler result")
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("close sink: %v", err)
	}
	line := readReceiptLines(t, logPath)[0]
	if strings.Contains(string(line), "visible only through return value") {
		t.Fatalf("receipt contains raw tool result: %s", line)
	}
}

func writeLSPToolReceiptTestKey(t *testing.T, root string) (string, ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal Ed25519 PKCS8 key: %v", err)
	}
	path := filepath.Join(root, "receipt-key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write receipt key: %v", err)
	}
	return path, publicKey
}

func readReceiptLines(t *testing.T, path string) [][]byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open receipt log: %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var lines [][]byte
	for scanner.Scan() {
		lines = append(lines, append([]byte(nil), scanner.Bytes()...))
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan receipt log: %v", err)
	}
	return lines
}
