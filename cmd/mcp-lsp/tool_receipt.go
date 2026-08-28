package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

const (
	mcpLSPToolReceiptSchema       = "super-dolphin/mcp-lsp-tool-receipt/v1"
	mcpLSPCanonicalReceiptSchema  = "super-dolphin/mcp-lsp-tool-receipt/v2"
	mcpLSPCanonicalReceiptEnv     = "MCP_LSP_RECEIPT_INCLUDE_CANONICAL_IO"
	mcpLSPToolReceiptLogPathEnv   = "MCP_LSP_RECEIPT_LOG_PATH"
	mcpLSPToolReceiptKeyPathEnv   = "MCP_LSP_RECEIPT_SIGNING_KEY_PATH"
	mcpLSPToolReceiptMaxLineBytes = 1 << 20
)

// lspToolReceiptSink 是 mcp-lsp 进程级的 append-only、Ed25519 签名回执写入器。
// 它只写显式配置的文件，不接触 MCP stdout，也不把参数和结果正文写入回执。
type lspToolReceiptSink struct {
	mu                 sync.Mutex
	file               *os.File
	privateKey         ed25519.PrivateKey
	path               string
	nextSequence       uint64
	previousDigest     string
	closed             bool
	poisoned           error
	includeCanonicalIO bool
}

// lspToolReceiptPayload 是单次 structure/xref/diagnostics 完成事件的脱敏内容。
type lspToolReceiptPayload struct {
	ReceiptID          string          `json:"receipt_id"`
	Tool               string          `json:"tool"`
	Action             string          `json:"action,omitempty"`
	Status             string          `json:"status"`
	ErrorCode          string          `json:"error_code,omitempty"`
	ErrorRetryable     bool            `json:"error_retryable,omitempty"`
	StartedAt          string          `json:"started_at"`
	CompletedAt        string          `json:"completed_at"`
	DurationMS         int64           `json:"duration_ms"`
	ArgumentsBytes     int             `json:"arguments_bytes"`
	ArgumentsSHA256    string          `json:"arguments_sha256"`
	Arguments          json.RawMessage `json:"arguments,omitempty"`
	ResultBytes        int             `json:"result_bytes"`
	ResultSHA256       string          `json:"result_sha256"`
	Result             json.RawMessage `json:"result,omitempty"`
	ErrorSHA256        string          `json:"error_sha256,omitempty"`
	ResultType         string          `json:"result_type,omitempty"`
	AgentID            string          `json:"agent_id,omitempty"`
	ThreadID           string          `json:"thread_id,omitempty"`
	TurnID             string          `json:"turn_id,omitempty"`
	CallID             string          `json:"call_id,omitempty"`
	CWDHash            string          `json:"cwd_sha256,omitempty"`
	WorkspaceRootCount int             `json:"workspace_root_count,omitempty"`
}

// lspToolReceiptRecord 是 JSONL 中的链式签名记录。
type lspToolReceiptRecord struct {
	Schema                string                `json:"schema"`
	Sequence              uint64                `json:"sequence"`
	PreviousDigest        string                `json:"previous_digest,omitempty"`
	Payload               lspToolReceiptPayload `json:"payload"`
	SignerPublicKeySHA256 string                `json:"signer_public_key_sha256"`
	EventDigest           string                `json:"event_digest"`
	Signature             string                `json:"signature"`
}

// lspToolReceiptUnsignedRecord 是签名和 event_digest 的稳定输入。
type lspToolReceiptUnsignedRecord struct {
	Schema                string                `json:"schema"`
	Sequence              uint64                `json:"sequence"`
	PreviousDigest        string                `json:"previous_digest,omitempty"`
	Payload               lspToolReceiptPayload `json:"payload"`
	SignerPublicKeySHA256 string                `json:"signer_public_key_sha256"`
}

// newLSPToolReceiptSinkFromEnv 从显式环境变量构造唯一生产回执 sink。
// 任一路径缺失都阻断启动，禁止默认目录或临时密钥伪造权威回执。
func newLSPToolReceiptSinkFromEnv() (*lspToolReceiptSink, error) {
	logPath := strings.TrimSpace(os.Getenv(mcpLSPToolReceiptLogPathEnv))
	keyPath := strings.TrimSpace(os.Getenv(mcpLSPToolReceiptKeyPathEnv))
	if logPath == "" || keyPath == "" {
		return nil, fmt.Errorf("mcp-lsp signed receipt requires explicit %s and %s", mcpLSPToolReceiptLogPathEnv, mcpLSPToolReceiptKeyPathEnv)
	}
	sink, err := newLSPToolReceiptSink(logPath, keyPath)
	if err != nil {
		return nil, err
	}
	canonical := strings.TrimSpace(os.Getenv(mcpLSPCanonicalReceiptEnv))
	if canonical != "" && canonical != "1" {
		_ = sink.file.Close()
		return nil, fmt.Errorf("%s must be exactly 1 when set", mcpLSPCanonicalReceiptEnv)
	}
	sink.includeCanonicalIO = canonical == "1"
	return sink, nil
}

// newLSPToolReceiptSink 打开并校验显式日志/密钥路径，恢复既有链的下一个序号。
func newLSPToolReceiptSink(logPath, keyPath string) (*lspToolReceiptSink, error) {
	logPath, err := requireAbsoluteReceiptPath("log", logPath)
	if err != nil {
		return nil, err
	}
	keyPath, err = requireAbsoluteReceiptPath("signing key", keyPath)
	if err != nil {
		return nil, err
	}
	privateKey, err := readLSPToolReceiptPrivateKey(keyPath)
	if err != nil {
		return nil, err
	}
	if err := ensureReceiptLogParent(logPath); err != nil {
		return nil, err
	}
	if err := validateReceiptLogPathBeforeOpen(logPath); err != nil {
		return nil, err
	}
	state, err := inspectLSPToolReceiptLog(logPath, privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open mcp-lsp signed receipt log: %w", err)
	}
	if err := requirePrivateReceiptMode(logPath, file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &lspToolReceiptSink{
		file:           file,
		privateKey:     privateKey,
		path:           logPath,
		nextSequence:   state.nextSequence,
		previousDigest: state.previousDigest,
	}, nil
}

func validateReceiptLogPathBeforeOpen(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat mcp-lsp signed receipt log: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("mcp-lsp signed receipt log must be a regular non-symlink file: %s", path)
	}
	return nil
}

func requireAbsoluteReceiptPath(kind, raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", fmt.Errorf("mcp-lsp signed receipt %s path is required", kind)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("mcp-lsp signed receipt %s path must be absolute: %q", kind, path)
	}
	return filepath.Clean(path), nil
}

func ensureReceiptLogParent(logPath string) error {
	parent := filepath.Dir(logPath)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("stat mcp-lsp signed receipt log directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("mcp-lsp signed receipt log parent is not a directory: %s", parent)
	}
	return nil
}

// readLSPToolReceiptPrivateKey 读取严格权限保护的 Ed25519 私钥文件。
func readLSPToolReceiptPrivateKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat mcp-lsp receipt signing key: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("mcp-lsp receipt signing key must be a regular non-symlink file: %s", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("mcp-lsp receipt signing key is group/world accessible: %s", path)
	}
	contents, err := readReceiptKeyContents(path)
	if err != nil {
		return nil, err
	}
	if len(contents) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(contents), nil
	}
	return parseLSPToolReceiptPEMKey(contents)
}

func readReceiptKeyContents(path string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mcp-lsp receipt signing key: %w", err)
	}
	return contents, nil
}

func parseLSPToolReceiptPEMKey(contents []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(contents)
	if block == nil {
		return nil, errors.New("mcp-lsp receipt signing key must be raw Ed25519 bytes or PKCS8 PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse mcp-lsp receipt signing key PKCS8: %w", err)
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("mcp-lsp receipt signing key PKCS8 key is not Ed25519")
	}
	return privateKey, nil
}

func requirePrivateReceiptMode(path string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat mcp-lsp signed receipt log: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("mcp-lsp signed receipt log must be a regular non-symlink file: %s", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("mcp-lsp signed receipt log is group/world accessible: %s", path)
	}
	return nil
}

type lspToolReceiptLogState struct {
	nextSequence   uint64
	previousDigest string
}

// inspectLSPToolReceiptLog 验证既有 JSONL 链并返回下一条记录的身份。
func inspectLSPToolReceiptLog(path string, publicKey ed25519.PublicKey) (lspToolReceiptLogState, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return lspToolReceiptLogState{nextSequence: 1}, nil
	}
	if err != nil {
		return lspToolReceiptLogState{}, fmt.Errorf("open mcp-lsp signed receipt log for validation: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return lspToolReceiptLogState{}, fmt.Errorf("stat mcp-lsp signed receipt log for validation: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return lspToolReceiptLogState{}, fmt.Errorf("mcp-lsp signed receipt log must be a regular non-symlink file: %s", path)
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), mcpLSPToolReceiptMaxLineBytes)
	state := lspToolReceiptLogState{nextSequence: 1}
	for scanner.Scan() {
		state, err = validateLSPToolReceiptLine(scanner.Bytes(), state, publicKey)
		if err != nil {
			return lspToolReceiptLogState{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return lspToolReceiptLogState{}, fmt.Errorf("scan mcp-lsp signed receipt log: %w", err)
	}
	return state, nil
}

func validateLSPToolReceiptLine(raw []byte, state lspToolReceiptLogState, publicKey ed25519.PublicKey) (lspToolReceiptLogState, error) {
	if len(bytesTrimSpace(raw)) == 0 {
		return lspToolReceiptLogState{}, errors.New("mcp-lsp signed receipt log contains an empty line")
	}
	var record lspToolReceiptRecord
	if err := decodeReceiptJSON(raw, &record); err != nil {
		return lspToolReceiptLogState{}, fmt.Errorf("decode mcp-lsp signed receipt log: %w", err)
	}
	if err := validateLSPToolReceiptRecord(record, state.nextSequence, state.previousDigest, publicKey); err != nil {
		return lspToolReceiptLogState{}, fmt.Errorf("validate mcp-lsp signed receipt log: %w", err)
	}
	state.nextSequence++
	state.previousDigest = record.EventDigest
	return state, nil
}

func bytesTrimSpace(value []byte) []byte {
	return []byte(strings.TrimSpace(string(value)))
}

func decodeReceiptJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// validateLSPToolReceiptRecord 校验 schema、链序号、摘要和 Ed25519 签名。
func validateLSPToolReceiptRecord(record lspToolReceiptRecord, expectedSequence uint64, previousDigest string, publicKey ed25519.PublicKey) error {
	if record.Schema != mcpLSPToolReceiptSchema && record.Schema != mcpLSPCanonicalReceiptSchema {
		return fmt.Errorf("unsupported receipt schema %q", record.Schema)
	}
	if record.Schema == mcpLSPCanonicalReceiptSchema &&
		(len(record.Payload.Arguments) == 0 || len(record.Payload.Result) == 0) {
		return errors.New("canonical receipt is missing arguments or result")
	}
	if record.Schema == mcpLSPToolReceiptSchema &&
		(len(record.Payload.Arguments) != 0 || len(record.Payload.Result) != 0) {
		return errors.New("metadata-only receipt contains canonical IO")
	}
	if record.Sequence != expectedSequence {
		return fmt.Errorf("sequence=%d, want %d", record.Sequence, expectedSequence)
	}
	if record.PreviousDigest != previousDigest {
		return errors.New("previous digest does not match append-only chain")
	}
	if record.SignerPublicKeySHA256 != receiptDigest(publicKey) {
		return errors.New("signer public key digest does not match configured key")
	}
	unsigned := lspToolReceiptUnsignedRecord{
		Schema:                record.Schema,
		Sequence:              record.Sequence,
		PreviousDigest:        record.PreviousDigest,
		Payload:               record.Payload,
		SignerPublicKeySHA256: record.SignerPublicKeySHA256,
	}
	raw, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("marshal unsigned receipt: %w", err)
	}
	if receiptDigest(raw) != record.EventDigest {
		return errors.New("event digest does not match signed content")
	}
	signature, err := base64.StdEncoding.DecodeString(record.Signature)
	if err != nil || !ed25519.Verify(publicKey, raw, signature) {
		return errors.New("receipt signature is invalid")
	}
	return nil
}

// Append 签发一条完成回执；sink 错误会阻断调用方，避免工具结果没有权威证据。
func (s *lspToolReceiptSink) Append(ctx context.Context, toolName string, args json.RawMessage, result any, callErr error, started, completed time.Time) error {
	if s == nil {
		return errors.New("mcp-lsp signed receipt sink is nil")
	}
	if !isReceiptToolName(toolName) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("mcp-lsp signed receipt sink is closed")
	}
	if s.poisoned != nil {
		return s.poisoned
	}
	payload, err := buildLSPToolReceiptPayload(ctx, toolName, args, result, callErr, started, completed)
	if err != nil {
		return err
	}
	if s.includeCanonicalIO {
		if len(payload.Arguments)+len(payload.Result) > mcpLSPToolReceiptMaxLineBytes/2 {
			return fmt.Errorf(
				"mcp-lsp canonical receipt payload exceeds limit: arguments=%d result=%d",
				len(payload.Arguments), len(payload.Result),
			)
		}
	} else {
		payload.Arguments = nil
		payload.Result = nil
	}
	record, line, err := s.signLSPToolReceipt(payload)
	if err != nil {
		return err
	}
	if _, err := s.file.Write(append(line, '\n')); err != nil {
		s.poisoned = fmt.Errorf("append mcp-lsp signed receipt: %w", err)
		return s.poisoned
	}
	if err := s.file.Sync(); err != nil {
		s.poisoned = fmt.Errorf("sync mcp-lsp signed receipt: %w", err)
		return s.poisoned
	}
	s.nextSequence = record.Sequence + 1
	s.previousDigest = record.EventDigest
	return nil
}

func isReceiptToolName(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "structure", "xref", "diagnostics":
		return true
	default:
		return false
	}
}

// buildLSPToolReceiptPayload 生成包含受签名 canonical 参数与结果的单次工具完成回执。
func buildLSPToolReceiptPayload(ctx context.Context, toolName string, args json.RawMessage, result any, callErr error, started, completed time.Time) (lspToolReceiptPayload, error) {
	if !json.Valid(args) {
		return lspToolReceiptPayload{}, errors.New("mcp-lsp receipt arguments are not valid JSON")
	}
	argsDigest := receiptDigest(args)
	resultRaw, err := json.Marshal(result)
	if err != nil {
		return lspToolReceiptPayload{}, fmt.Errorf("marshal mcp-lsp receipt result digest: %w", err)
	}
	receiptIDBytes := make([]byte, 16)
	if _, err := cryptorand.Read(receiptIDBytes); err != nil {
		return lspToolReceiptPayload{}, fmt.Errorf("generate mcp-lsp receipt id: %w", err)
	}
	status := "success"
	payload := lspToolReceiptPayload{
		ReceiptID:       hex.EncodeToString(receiptIDBytes),
		Tool:            strings.TrimSpace(toolName),
		Action:          receiptAction(args),
		Status:          status,
		StartedAt:       started.UTC().Format(time.RFC3339Nano),
		CompletedAt:     completed.UTC().Format(time.RFC3339Nano),
		DurationMS:      completed.Sub(started).Milliseconds(),
		ArgumentsBytes:  len(args),
		ArgumentsSHA256: argsDigest,
		Arguments:       append(json.RawMessage(nil), args...),
		ResultBytes:     len(resultRaw),
		ResultSHA256:    receiptDigest(resultRaw),
		Result:          append(json.RawMessage(nil), resultRaw...),
		ResultType:      fmt.Sprintf("%T", result),
	}
	if scope, ok := common.ToolScopeFromContext(ctx); ok {
		payload.AgentID = scope.AgentID
		payload.ThreadID = scope.ThreadID
		payload.TurnID = scope.TurnID
		payload.CallID = scope.CallID
		payload.CWDHash = digestReceiptString(scope.CWD)
		payload.WorkspaceRootCount = len(scope.WorkspaceRoots)
	}
	if callErr != nil {
		payload.Status = "error"
		payload.ErrorSHA256 = digestReceiptString(callErr.Error())
		payload.ErrorCode, payload.ErrorRetryable = receiptErrorClassification(toolName, callErr)
	}
	return payload, nil
}

func receiptAction(args json.RawMessage) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(args, &object); err != nil {
		return ""
	}
	var action string
	if err := json.Unmarshal(object["action"], &action); err != nil {
		return ""
	}
	return strings.TrimSpace(action)
}

func digestReceiptString(value string) string {
	return receiptDigest([]byte(value))
}

func receiptDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func receiptErrorClassification(toolName string, err error) (string, bool) {
	code, retryable, _, _ := common.ClassifyToolErrorWithClassifier(toolName, err, tools.ToolErrorClassifier)
	return code, retryable
}

func (s *lspToolReceiptSink) signLSPToolReceipt(payload lspToolReceiptPayload) (lspToolReceiptRecord, []byte, error) {
	schema := mcpLSPToolReceiptSchema
	if s.includeCanonicalIO {
		schema = mcpLSPCanonicalReceiptSchema
	}
	record := lspToolReceiptRecord{
		Schema:                schema,
		Sequence:              s.nextSequence,
		PreviousDigest:        s.previousDigest,
		Payload:               payload,
		SignerPublicKeySHA256: receiptDigest(s.privateKey.Public().(ed25519.PublicKey)),
	}
	unsigned := lspToolReceiptUnsignedRecord{
		Schema:                record.Schema,
		Sequence:              record.Sequence,
		PreviousDigest:        record.PreviousDigest,
		Payload:               record.Payload,
		SignerPublicKeySHA256: record.SignerPublicKeySHA256,
	}
	raw, err := json.Marshal(unsigned)
	if err != nil {
		return lspToolReceiptRecord{}, nil, fmt.Errorf("marshal mcp-lsp unsigned receipt: %w", err)
	}
	record.EventDigest = receiptDigest(raw)
	record.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(s.privateKey, raw))
	line, err := json.Marshal(record)
	if err != nil {
		return lspToolReceiptRecord{}, nil, fmt.Errorf("marshal mcp-lsp signed receipt: %w", err)
	}
	return record, line, nil
}

// Close 刷新并关闭回执文件；关闭后的 sink 不可再次写入。
func (s *lspToolReceiptSink) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.file == nil {
		return nil
	}
	if err := s.file.Sync(); err != nil {
		_ = s.file.Close()
		return fmt.Errorf("sync mcp-lsp signed receipt log on close: %w", err)
	}
	return s.file.Close()
}
