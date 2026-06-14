package common

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	toolPayloadLogDirEnv = "GO_AGENT_TOOL_PAYLOAD_LOG_DIR"
	logFallbackDirEnv    = "GO_AGENT_LOG_FALLBACK_DIR"
)

var toolPayloadLogSeq atomic.Uint64

type toolPayloadLogRef struct {
	Path  string
	Bytes int
	Err   error
}

type toolPayloadSnapshot struct {
	Version      int             `json:"version"`
	CreatedAt    string          `json:"created_at"`
	Stage        string          `json:"stage"`
	Transport    string          `json:"transport"`
	Deprecated   bool            `json:"deprecated,omitempty"`
	Server       string          `json:"server"`
	Tool         string          `json:"tool"`
	ReqID        string          `json:"req_id,omitempty"`
	AgentID      string          `json:"agent_id,omitempty"`
	ThreadID     string          `json:"thread_id,omitempty"`
	CallID       string          `json:"call_id,omitempty"`
	CWD          string          `json:"cwd,omitempty"`
	Arguments    json.RawMessage `json:"arguments,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        string          `json:"error,omitempty"`
	RawArgsLen   int             `json:"raw_args_len,omitempty"`
	RawResultLen int             `json:"raw_result_len,omitempty"`
}

func logToolCallRequestPayload(transport, server string, reqID json.RawMessage, params ToolCallParams, scope ToolScope) toolPayloadLogRef {
	rawArgs := cloneRawMessage(params.Arguments)
	return writeToolPayloadSnapshot(toolPayloadSnapshot{
		Version:    1,
		CreatedAt:  time.Now().Format(time.RFC3339Nano),
		Stage:      "request",
		Transport:  strings.TrimSpace(transport),
		Deprecated: isDeprecatedToolPayloadTransport(transport),
		Server:     strings.TrimSpace(server),
		Tool:       strings.TrimSpace(params.Name),
		ReqID:      trimJSONID(reqID),
		AgentID:    scope.AgentID,
		ThreadID:   scope.ThreadID,
		CallID:     scope.CallID,
		CWD:        scope.CWD,
		Arguments:  rawArgs,
		RawArgsLen: len(rawArgs),
	})
}

func logToolCallResultPayload(transport, server, tool string, reqID json.RawMessage, scope ToolScope, rawResult []byte, err error) toolPayloadLogRef {
	var errText string
	if err != nil {
		errText = err.Error()
	}
	raw := cloneRawMessage(rawResult)
	return writeToolPayloadSnapshot(toolPayloadSnapshot{
		Version:      1,
		CreatedAt:    time.Now().Format(time.RFC3339Nano),
		Stage:        "result",
		Transport:    strings.TrimSpace(transport),
		Deprecated:   isDeprecatedToolPayloadTransport(transport),
		Server:       strings.TrimSpace(server),
		Tool:         strings.TrimSpace(tool),
		ReqID:        trimJSONID(reqID),
		AgentID:      scope.AgentID,
		ThreadID:     scope.ThreadID,
		CallID:       scope.CallID,
		CWD:          scope.CWD,
		Result:       raw,
		Error:        errText,
		RawResultLen: len(raw),
	})
}

func writeToolPayloadSnapshot(snapshot toolPayloadSnapshot) toolPayloadLogRef {
	dir, err := toolPayloadLogDir()
	if err != nil {
		return toolPayloadLogRef{Err: err}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return toolPayloadLogRef{Err: err}
	}
	line, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return toolPayloadLogRef{Err: err}
	}
	line = append(line, '\n')
	path := filepath.Join(dir, toolPayloadSnapshotFileName(snapshot))
	if err := os.WriteFile(path, line, 0o600); err != nil {
		return toolPayloadLogRef{Err: err}
	}
	return toolPayloadLogRef{Path: path, Bytes: len(line)}
}

// toolPayloadLogDir 处理工具载荷日志目录。
func toolPayloadLogDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(toolPayloadLogDirEnv)); dir != "" {
		return filepath.Abs(dir)
	}
	if logPath := strings.TrimSpace(pkglogger.CurrentLogFilePath()); logPath != "" {
		return filepath.Join(filepath.Dir(logPath), "tool-payloads"), nil
	}
	if dir := strings.TrimSpace(os.Getenv(logFallbackDirEnv)); dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		return filepath.Join(abs, "tool-payloads"), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	logDir, _ := pkglogger.ResolveProjectLogDir(home, cwd)
	return filepath.Join(logDir, "peer-fallback", "tool-payloads"), nil
}

func toolPayloadSnapshotFileName(snapshot toolPayloadSnapshot) string {
	seq := toolPayloadLogSeq.Add(1)
	stamp := strings.ReplaceAll(snapshot.CreatedAt, ":", "")
	stamp = strings.ReplaceAll(stamp, "-", "")
	stamp = strings.ReplaceAll(stamp, ".", "")
	return fmt.Sprintf("%s-%06d-%s-%s-%s.json",
		sanitizeLogFileComponent(stamp),
		seq,
		sanitizeLogFileComponent(snapshot.Stage),
		sanitizeLogFileComponent(snapshot.Server),
		sanitizeLogFileComponent(snapshot.Tool),
	)
}

func toolPayloadAttrs(prefix string, ref toolPayloadLogRef) []any {
	if strings.TrimSpace(prefix) == "" {
		prefix = "tool_payload"
	}
	if ref.Err != nil {
		return []any{prefix + "_error", ref.Err}
	}
	if ref.Path == "" {
		return nil
	}
	return []any{
		prefix + "_path", ref.Path,
		prefix + "_bytes", ref.Bytes,
	}
}

func trimJSONID(id json.RawMessage) string {
	trimmed := strings.TrimSpace(string(id))
	return strings.Trim(trimmed, `"`)
}

func cloneRawMessage(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func sanitizeLogFileComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	out := strings.Trim(strings.Map(sanitizeLogFileRune, value), "-")
	if out == "" {
		return "unknown"
	}
	if len(out) > 80 {
		return out[:80]
	}
	return out
}

func sanitizeLogFileRune(r rune) rune {
	const safeLogFileChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	if strings.ContainsRune(safeLogFileChars, r) {
		return r
	}
	return '-'
}

func isDeprecatedToolPayloadTransport(transport string) bool {
	return strings.EqualFold(strings.TrimSpace(transport), "http")
}
