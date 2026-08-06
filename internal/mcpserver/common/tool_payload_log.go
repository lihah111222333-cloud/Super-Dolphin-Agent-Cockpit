package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// 工具载荷日志环境变量名。
const (
	toolPayloadLogDirEnv   = "GO_AGENT_TOOL_PAYLOAD_LOG_DIR"
	toolPayloadLogDebugEnv = "GO_AGENT_TOOL_PAYLOAD_LOG_DEBUG"
	logFallbackDirEnv      = "GO_AGENT_LOG_FALLBACK_DIR"

	maxToolPayloadSnapshotCreateAttempts = 64
)

// toolPayloadLogger 为单个 MCP server 持有载荷快照序号，避免跨 server 共享可变状态。
type toolPayloadLogger struct {
	sequence atomic.Uint64
}

var (
	tokenLikePayloadPattern     = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{8,}|gh[pousr]_[a-z0-9_]{8,}|xox[baprs]-[a-z0-9-]{8,}|[a-z0-9_-]{20,}\.[a-z0-9_-]{10,}\.[a-z0-9_-]{10,})`)
	bearerPayloadPattern        = regexp.MustCompile(`(?i)\bbearer\s+[^\s,;]+`)
	authorizationPayloadPattern = regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*[^\r\n,;]+`)
	credentialAssignmentPattern = regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|password)\s*([:=])\s*[^\s,;]+`)
	connectionUserinfoPattern   = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^:/\s@]+:[^@/\s]+@`)
)

// UnmarshalJSON 严格解码 tools/call 顶层参数，同时兼容 MCP 标准和历史 metadata。
// _meta/sessionId/session_id 只作为不可信 metadata 被接收或忽略；其他未知字段仍会直接报错。
func (p *ToolCallParams) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Name                    string          `json:"name"`
		Arguments               json.RawMessage `json:"arguments,omitempty"`
		Meta                    json.RawMessage `json:"_meta,omitempty"`
		MetaAgentID             string          `json:"_agentId,omitempty"`
		MetaThreadID            string          `json:"_threadId,omitempty"`
		MetaCallID              string          `json:"_callId,omitempty"`
		MetaCWD                 string          `json:"_cwd,omitempty"`
		MetaWorkspaceRoots      []string        `json:"_workspaceRoots,omitempty"`
		MetaWorkspaceRootsSnake []string        `json:"_workspace_roots,omitempty"`
		LegacySessionID         json.RawMessage `json:"sessionId,omitempty"`
		LegacySessionIDSnake    json.RawMessage `json:"session_id,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("tool call params: trailing JSON payload")
	}
	*p = ToolCallParams{
		Name:                    wire.Name,
		Arguments:               wire.Arguments,
		Meta:                    wire.Meta,
		MetaAgentID:             wire.MetaAgentID,
		MetaThreadID:            wire.MetaThreadID,
		MetaCallID:              wire.MetaCallID,
		MetaCWD:                 wire.MetaCWD,
		MetaWorkspaceRoots:      wire.MetaWorkspaceRoots,
		MetaWorkspaceRootsSnake: wire.MetaWorkspaceRootsSnake,
	}
	return nil
}

// toolPayloadLogRef 记录工具载荷快照写入结果，供日志属性附加使用。
type toolPayloadLogRef struct {
	Path  string
	Bytes int
	Err   error
}

// toolPayloadSnapshot 是写入磁盘的工具调用载荷快照结构。
type toolPayloadSnapshot struct {
	Version         int             `json:"version"`
	CreatedAt       string          `json:"created_at"`
	Stage           string          `json:"stage"`
	Transport       string          `json:"transport"`
	Deprecated      bool            `json:"deprecated,omitempty"`
	Server          string          `json:"server"`
	Tool            string          `json:"tool"`
	ReqID           string          `json:"req_id,omitempty"`
	AgentID         string          `json:"agent_id,omitempty"`
	ThreadID        string          `json:"thread_id,omitempty"`
	CallID          string          `json:"call_id,omitempty"`
	CWD             string          `json:"cwd,omitempty"`
	Status          string          `json:"status,omitempty"`
	DurationMS      int64           `json:"duration_ms"`
	PayloadRedacted bool            `json:"payload_redacted"`
	Arguments       json.RawMessage `json:"arguments,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           string          `json:"error,omitempty"`
	RawArgsLen      int             `json:"raw_args_len,omitempty"`
	RawResultLen    int             `json:"raw_result_len,omitempty"`
}

// logRequest 记录工具调用请求载荷快照并返回引用。
func (l *toolPayloadLogger) logRequest(transport, server string, reqID json.RawMessage, params ToolCallParams, scope ToolScope) toolPayloadLogRef {
	rawArgs := cloneRawMessage(params.Arguments)
	args, redacted := snapshotPayload(rawArgs)
	return l.write(toolPayloadSnapshot{
		Version:         1,
		CreatedAt:       time.Now().Format(time.RFC3339Nano),
		Stage:           "request",
		Transport:       strings.TrimSpace(transport),
		Deprecated:      isDeprecatedToolPayloadTransport(transport),
		Server:          strings.TrimSpace(server),
		Tool:            strings.TrimSpace(params.Name),
		ReqID:           trimJSONID(reqID),
		AgentID:         scope.AgentID,
		ThreadID:        scope.ThreadID,
		CallID:          scope.CallID,
		CWD:             scope.CWD,
		Status:          "request",
		PayloadRedacted: redacted,
		Arguments:       args,
		RawArgsLen:      len(rawArgs),
	})
}

// logResult 记录工具调用结果载荷快照并返回引用。
func (l *toolPayloadLogger) logResult(transport, server, tool string, reqID json.RawMessage, scope ToolScope, rawResult []byte, err error) toolPayloadLogRef {
	var errText string
	if err != nil {
		errText = redactSensitiveString(err.Error())
	}
	raw := cloneRawMessage(rawResult)
	result, redacted := snapshotPayload(raw)
	return l.write(toolPayloadSnapshot{
		Version:         1,
		CreatedAt:       time.Now().Format(time.RFC3339Nano),
		Stage:           "result",
		Transport:       strings.TrimSpace(transport),
		Deprecated:      isDeprecatedToolPayloadTransport(transport),
		Server:          strings.TrimSpace(server),
		Tool:            strings.TrimSpace(tool),
		ReqID:           trimJSONID(reqID),
		AgentID:         scope.AgentID,
		ThreadID:        scope.ThreadID,
		CallID:          scope.CallID,
		CWD:             scope.CWD,
		Status:          toolPayloadResultStatus(err),
		PayloadRedacted: redacted || errText != "",
		Result:          result,
		Error:           errText,
		RawResultLen:    len(raw),
	})
}

// snapshotPayload 默认只保留长度等元数据；调试模式下返回经过敏感信息脱敏的 JSON body。
func snapshotPayload(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	if !toolPayloadDebugEnabled() {
		return nil, true
	}
	return redactJSONPayload(raw), true
}

// toolPayloadDebugEnabled 判断是否允许载荷快照写入经过脱敏的 body。
func toolPayloadDebugEnabled() bool {
	value := strings.TrimSpace(os.Getenv(toolPayloadLogDebugEnv))
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// toolPayloadResultStatus 将结果快照状态压缩为稳定枚举，避免客户端解析错误字符串。
func toolPayloadResultStatus(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

// redactJSONPayload 对 JSON payload 递归脱敏；非法 JSON 也会以脱敏字符串保留调试线索。
func redactJSONPayload(raw json.RawMessage) json.RawMessage {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		encoded, _ := json.Marshal(redactSensitiveString(string(raw)))
		return encoded
	}
	encoded, err := json.Marshal(redactJSONValue("", value))
	if err != nil {
		return json.RawMessage(`"[REDACTED]"`)
	}
	return encoded
}

// redactJSONValue 递归处理对象、数组和字符串，敏感 key 的值整段替换。
func redactJSONValue(key string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			out[childKey] = redactJSONValue(childKey, childValue)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactJSONValue("", item)
		}
		return out
	case string:
		if isSensitivePayloadKey(key) {
			return "[REDACTED]"
		}
		return redactSensitiveString(typed)
	default:
		if isSensitivePayloadKey(key) && typed != nil {
			return "[REDACTED]"
		}
		return typed
	}
}

// redactSensitiveString 替换常见凭据载体，保留不含凭据的调试上下文。
func redactSensitiveString(value string) string {
	value = tokenLikePayloadPattern.ReplaceAllString(value, "[REDACTED]")
	value = bearerPayloadPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = authorizationPayloadPattern.ReplaceAllString(value, "authorization=[REDACTED]")
	value = credentialAssignmentPattern.ReplaceAllString(value, "$1$2[REDACTED]")
	return connectionUserinfoPattern.ReplaceAllString(value, "$1[REDACTED]@")
}

// isSensitivePayloadKey 判断 JSON key 是否常用于认证密钥或口令。
func isSensitivePayloadKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(strings.TrimSpace(key)))
	return strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "authorization")
}

// write 将快照 JSON 写入磁盘，返回文件路径和字节数。
func (l *toolPayloadLogger) write(snapshot toolPayloadSnapshot) toolPayloadLogRef {
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
	for attempt := 0; attempt < maxToolPayloadSnapshotCreateAttempts; attempt++ {
		path := filepath.Join(dir, l.snapshotFileName(snapshot))
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return toolPayloadLogRef{Err: err}
		}
		if _, err := file.Write(line); err != nil {
			closeErr := file.Close()
			removeErr := os.Remove(path)
			return toolPayloadLogRef{Err: errors.Join(err, closeErr, removeErr)}
		}
		if err := file.Close(); err != nil {
			return toolPayloadLogRef{Err: err}
		}
		return toolPayloadLogRef{Path: path, Bytes: len(line)}
	}
	return toolPayloadLogRef{Err: fmt.Errorf("create unique tool payload snapshot after %d attempts", maxToolPayloadSnapshotCreateAttempts)}
}

// toolPayloadLogDir 解析工具载荷快照目录，优先使用显式环境变量，其次跟随当前日志文件。
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

// snapshotFileName 根据快照内容生成唯一且安全的文件名。
func (l *toolPayloadLogger) snapshotFileName(snapshot toolPayloadSnapshot) string {
	seq := l.sequence.Add(1)
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

// toolPayloadAttrs 将 toolPayloadLogRef 展开为 slog 属性键值对列表。
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

// trimJSONID 去掉 JSON-RPC ID 字段两端的空白和引号，得到可读字符串。
func trimJSONID(id json.RawMessage) string {
	trimmed := strings.TrimSpace(string(id))
	return strings.Trim(trimmed, `"`)
}

// cloneRawMessage 深拷贝 json.RawMessage，nil/空输入返回 nil。
func cloneRawMessage(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

// sanitizeLogFileComponent 将字符串中不安全字符替换为连字符，限制最大长度。
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

// sanitizeLogFileRune 将不安全字符映射为连字符，安全字符原样保留。
func sanitizeLogFileRune(r rune) rune {
	const safeLogFileChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	if strings.ContainsRune(safeLogFileChars, r) {
		return r
	}
	return '-'
}

// isDeprecatedToolPayloadTransport 判断 transport 是否属于已废弃的 HTTP 传输。
func isDeprecatedToolPayloadTransport(transport string) bool {
	return strings.EqualFold(strings.TrimSpace(transport), "http")
}
