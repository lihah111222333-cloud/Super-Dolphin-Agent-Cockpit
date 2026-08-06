package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

const (
	// ErrorPreviewField 是所有 trace/log 预览错误文本使用的统一字段名。
	ErrorPreviewField = "error_preview"
	// ErrorCodeField 是错误分类使用的统一字段名。
	ErrorCodeField = "error_code"
	// ProviderExitCodeField 是 provider 进程退出码使用的统一字段名。
	ProviderExitCodeField = "provider_exit_code"
	// PeerIDField 是 MCP peer 标识使用的统一字段名。
	PeerIDField = "peer_id"
)

const defaultErrorPreviewBytes = 512

// SafePreviewResult 描述可写入日志或 trace metadata 的安全预览。
// 超过上限的内容不返回局部正文，只保留原始字节数和哈希，避免长 payload 泄密。
type SafePreviewResult struct {
	Preview   string `json:"preview,omitempty"`
	Truncated bool   `json:"truncated"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256,omitempty"`
}

// ProviderErrorSummary 是 provider error 日志和事件中允许暴露的安全错误形态。
type ProviderErrorSummary struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// MCPServerSummary 是 MCP launch 日志允许输出的 server 摘要。
type MCPServerSummary struct {
	Name            string   `json:"name,omitempty"`
	Type            string   `json:"type"`
	Command         string   `json:"command,omitempty"`
	CommandExists   bool     `json:"command_exists"`
	ArgCount        int      `json:"arg_count,omitempty"`
	ArgSHA256       string   `json:"arg_sha256,omitempty"`
	HasURL          bool     `json:"has_url,omitempty"`
	URLScheme       string   `json:"url_scheme,omitempty"`
	URLHost         string   `json:"url_host,omitempty"`
	URLPort         string   `json:"url_port,omitempty"`
	URLPath         string   `json:"url_path,omitempty"`
	URLPathSegments int      `json:"url_path_segments,omitempty"`
	URLPathSHA256   string   `json:"url_path_sha256,omitempty"`
	URLParseError   bool     `json:"url_parse_error,omitempty"`
	EnvKeys         []string `json:"env_keys,omitempty"`
	HasRPCAddr      bool     `json:"has_rpc_addr,omitempty"`
	HasBootstrap    bool     `json:"has_bootstrap,omitempty"`
}

// BusEventSummary 是 bus 结构化日志允许输出的事件摘要。
type BusEventSummary struct {
	Type      string `json:"type"`
	ThreadID  string `json:"thread_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	Stream    string `json:"stream,omitempty"`
	InputType string `json:"input_type,omitempty"`
	Success   *bool  `json:"success,omitempty"`
}

// SafePreview 将任意值转换为单行脱敏预览；超过 maxBytes 时只返回大小和哈希。
func SafePreview(value any, maxBytes int) SafePreviewResult {
	raw := safePreviewBytes(value)
	result := SafePreviewResult{Bytes: int64(len(raw))}
	if maxBytes <= 0 || len(raw) > maxBytes {
		result.Truncated = true
		result.SHA256 = safePreviewHash(raw)
		return result
	}
	sanitizer := safePreviewSanitizer(maxBytes)
	if preview, ok := safePreviewJSON(raw, sanitizer); ok {
		result.Preview = sanitizer.String(preview)
		return result
	}
	result.Preview = sanitizer.String(string(raw))
	return result
}

// SafeErrorPreview 返回适合 error_preview 字段的短错误摘要。
func SafeErrorPreview(err error) string {
	if err == nil {
		return ""
	}
	return SafePreview(err.Error(), defaultErrorPreviewBytes).Preview
}

// SafeErrorMetadata 返回统一错误字段；没有错误时返回 nil，便于调用方按需合入 metadata。
func SafeErrorMetadata(err error, code string) map[string]any {
	if err == nil {
		return nil
	}
	out := map[string]any{
		ErrorPreviewField: SafeErrorPreview(err),
	}
	if code != "" {
		out[ErrorCodeField] = code
	}
	return out
}

// SafeMetadataValue 按 metadata 键名和值类型返回可安全写入日志或 trace 的字段值。
func SafeMetadataValue(key string, value any, maxBytes int) (any, bool) {
	return safePreviewSanitizer(maxBytes).metadataValueForKey(key, value)
}

// SafeProviderErrorReason 将 provider 原始错误折叠为可写入日志/事件的安全摘要。
func SafeProviderErrorReason(reason string) ProviderErrorSummary {
	reason = strings.TrimSpace(reason)
	if code := classifyProviderAuthError(reason); code != "" {
		return ProviderErrorSummary{Code: code, Message: "provider authentication failed: " + code}
	}
	if reason == "" {
		return ProviderErrorSummary{Code: "provider_connection_lost", Message: "provider connection lost"}
	}
	preview := SafePreview(reason, defaultErrorPreviewBytes).Preview
	if strings.TrimSpace(preview) == "" {
		preview = "provider connection failed"
	}
	return ProviderErrorSummary{Code: "provider_connection_failed", Message: preview}
}

// SafeMCPServerSummaries 返回 MCP manifest 可安全写入 launch 日志的 server 摘要。
func SafeMCPServerSummaries(manifest dto.MCPManifest) []MCPServerSummary {
	servers := make([]MCPServerSummary, 0, len(manifest.Binaries))
	for _, bin := range manifest.Binaries {
		serverType := strings.TrimSpace(bin.Type)
		if serverType == "" {
			serverType = "stdio"
		}
		summary := MCPServerSummary{
			Name:         strings.TrimSpace(bin.Name),
			Type:         serverType,
			ArgCount:     safeCommandArgCount(bin.Command),
			ArgSHA256:    safeCommandArgHash(bin.Command),
			HasURL:       strings.TrimSpace(bin.URL) != "",
			EnvKeys:      safeSortedEnvKeys(bin.Env),
			HasRPCAddr:   strings.TrimSpace(bin.Env["GO_AGENT_CTL_RPC_ADDR"]) != "",
			HasBootstrap: strings.TrimSpace(bin.Env["GO_AGENT_CTL_BOOTSTRAP_JSON"]) != "",
		}
		applySafeURLSummary(&summary, bin.URL)
		if len(bin.Command) > 0 {
			rawCommand := strings.TrimSpace(bin.Command[0])
			summary.Command = safeCommandBasename(rawCommand)
			if rawCommand != "" {
				_, statErr := os.Stat(rawCommand)
				summary.CommandExists = statErr == nil
			}
		}
		servers = append(servers, summary)
	}
	return servers
}

// SafeBusEventSummary 提取 bus 事件的 allowlist 元数据，避免日志持久化完整 DTO。
func SafeBusEventSummary(ev any) BusEventSummary {
	summary := BusEventSummary{Type: safeEventTypeName(ev)}
	collectBusEventSummary(reflect.ValueOf(ev), &summary)
	return summary
}

// safePreviewBytes 把任意值转换为后续脱敏和哈希使用的字节串。
// 无法 JSON 编码时退回 fmt.Sprint，但仍会经过 SafePreview 的脱敏流程。
func safePreviewBytes(value any) []byte {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		return append([]byte(nil), typed...)
	case string:
		return []byte(typed)
	case json.RawMessage:
		return append([]byte(nil), typed...)
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return fmt.Append(nil, typed)
		}
		return raw
	}
}

// classifyProviderAuthError 把 provider 返回的认证失败文本归一成稳定 code。
// 这里不能回传原始 reason，外部 CLI/API 常把 token 或请求 URL 拼在错误字符串里。
func classifyProviderAuthError(reason string) string {
	text := strings.ToLower(strings.TrimSpace(reason))
	if text == "" {
		return ""
	}
	if strings.Contains(text, "invalid_api_key") || strings.Contains(text, "incorrect api key") {
		return "invalid_api_key"
	}
	if strings.Contains(text, "api key") && (strings.Contains(text, "invalid") ||
		strings.Contains(text, "incorrect") ||
		strings.Contains(text, "unauthorized")) {
		return "invalid_api_key"
	}
	if strings.Contains(text, "401") && strings.Contains(text, "unauthorized") {
		return "invalid_api_key"
	}
	return ""
}

func safeCommandArgCount(command []string) int {
	if len(command) <= 1 {
		return 0
	}
	return len(command) - 1
}

func safeCommandArgHash(command []string) string {
	if len(command) <= 1 {
		return ""
	}
	raw, err := json.Marshal(command[1:])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func safeCommandBasename(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	command = strings.ReplaceAll(command, "\\", "/")
	return path.Base(command)
}

func applySafeURLSummary(summary *MCPServerSummary, rawURL string) {
	rawURL = strings.TrimSpace(rawURL)
	if summary == nil || rawURL == "" {
		return
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		summary.URLParseError = true
		return
	}
	summary.URLScheme = strings.ToLower(strings.TrimSpace(parsed.Scheme))
	summary.URLHost = strings.TrimSpace(parsed.Hostname())
	summary.URLPort = strings.TrimSpace(parsed.Port())
	applySafeURLPathSummary(summary, parsed.EscapedPath())
}

func applySafeURLPathSummary(summary *MCPServerSummary, escapedPath string) {
	escapedPath = strings.TrimSpace(escapedPath)
	if summary == nil || escapedPath == "" {
		return
	}
	if escapedPath == "/mcp" {
		summary.URLPath = "/mcp"
		return
	}
	segments := safeURLPathSegments(escapedPath)
	if segments > 0 {
		summary.URLPathSegments = segments
	}
	sum := sha256.Sum256([]byte(escapedPath))
	summary.URLPathSHA256 = hex.EncodeToString(sum[:])
}

func safeURLPathSegments(escapedPath string) int {
	trimmed := strings.Trim(strings.TrimSpace(escapedPath), "/")
	if trimmed == "" {
		return 0
	}
	count := 0
	for segment := range strings.SplitSeq(trimmed, "/") {
		if segment != "" {
			count++
		}
	}
	return count
}

func safeSortedEnvKeys(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		key = strings.TrimSpace(key)
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func safeEventTypeName(ev any) string {
	if ev == nil {
		return "<nil>"
	}
	typ := reflect.TypeOf(ev)
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ.String()
}

// collectBusEventSummary 只沿导出字段提取 allowlist 摘要字段。
// 禁止在这里恢复 JSON preview，否则 prompt/delta/cwd 会重新进入生产日志。
func collectBusEventSummary(value reflect.Value, summary *BusEventSummary) {
	value, ok := busSummaryStructValue(value)
	if !ok || summary == nil {
		return
	}
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		collectBusSummaryField(typ.Field(i), value.Field(i), summary)
	}
}

func busSummaryStructValue(value reflect.Value) (reflect.Value, bool) {
	if !value.IsValid() {
		return reflect.Value{}, false
	}
	value, ok := indirectBusSummaryValue(value)
	if !ok || value.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	return value, !isTimeType(value.Type())
}

func indirectBusSummaryValue(value reflect.Value) (reflect.Value, bool) {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		value = value.Elem()
	}
	return value, true
}

func isTimeType(typ reflect.Type) bool {
	return typ.PkgPath() == "time" && typ.Name() == "Time"
}

func collectBusSummaryField(field reflect.StructField, value reflect.Value, summary *BusEventSummary) {
	if field.PkgPath != "" {
		return
	}
	if setBusSummaryField(field.Name, value, summary) {
		return
	}
	if field.Anonymous || value.Kind() == reflect.Struct {
		collectBusEventSummary(value, summary)
	}
}

func setBusSummaryField(name string, value reflect.Value, summary *BusEventSummary) bool {
	switch name {
	case "ThreadID":
		summary.ThreadID = safeSummaryString(value)
	case "AgentID":
		summary.AgentID = safeSummaryString(value)
	case "SessionID":
		summary.SessionID = safeSummaryString(value)
	case "TurnID":
		summary.TurnID = safeSummaryString(value)
	case "CallID":
		summary.CallID = safeSummaryString(value)
	case "ToolName":
		summary.ToolName = safeSummaryString(value)
	case "Provider":
		summary.Provider = safeSummaryString(value)
	case "Model":
		summary.Model = safeSummaryString(value)
	case "Stream":
		summary.Stream = safeSummaryString(value)
	case "InputType":
		summary.InputType = safeSummaryString(value)
	case "Success":
		if value.Kind() == reflect.Bool {
			success := value.Bool()
			summary.Success = &success
		}
	default:
		return false
	}
	return true
}

func safeSummaryString(value reflect.Value) string {
	if value.Kind() != reflect.String {
		return ""
	}
	return strings.TrimSpace(value.String())
}

func safePreviewSanitizer(maxBytes int) Sanitizer {
	return NewSanitizer(Config{StringMaxBytes: maxBytes, MetadataMaxBytes: maxBytes})
}

// safePreviewJSON 对对象或数组 JSON 做键名感知脱敏，避免 quoted password/token 绕过文本正则。
func safePreviewJSON(raw []byte, sanitizer Sanitizer) (string, bool) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", false
	}
	switch decoded.(type) {
	case map[string]any, []any:
	default:
		return "", false
	}
	encoded, err := json.Marshal(safePreviewJSONValue(decoded, "", sanitizer))
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

// safePreviewJSONValue 递归复制 JSON 值，敏感键下的值整体替换，普通字符串继续走通用脱敏。
func safePreviewJSONValue(value any, key string, sanitizer Sanitizer) any {
	if key != "" && sanitizer.secretLikeKey(key) {
		return redacted
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			out[sanitizer.String(childKey)] = safePreviewJSONValue(childValue, childKey, sanitizer)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, childValue := range typed {
			out = append(out, safePreviewJSONValue(childValue, key, sanitizer))
		}
		return out
	case string:
		return sanitizer.String(typed)
	default:
		return typed
	}
}

func safePreviewHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
