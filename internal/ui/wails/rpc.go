package wails

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// codeLocateLimit 限制一次 ui/code/locate 返回的候选数量。
const codeLocateLimit = 24

const frontendLogRedactedValue = "[REDACTED]"

var frontendLogSecretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{
		pattern:     regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;&]+`),
		replacement: `${1}` + frontendLogRedactedValue,
	},
	{
		pattern:     regexp.MustCompile(`(?i)((?:api[_-]?key|secret[_-]?key|access[_-]?token|token|password|cookie)\s*[:=]\s*)[^\s,;&]+`),
		replacement: `${1}` + frontendLogRedactedValue,
	},
}

var frontendLogSecretKeyMarkers = []string{
	"token",
	"password",
	"secret",
	"database_url",
	"postgres_connection_string",
	"sqlite_path",
	"sqlite_db_path",
	"authorization",
	"api_key",
	"apikey",
	"cookie",
}

var frontendLogKeyReplacer = strings.NewReplacer("-", "_", ".", "_", " ", "_")

// clientMetaParams 承载前端客户端来源元数据。
type clientMetaParams struct {
	ClientKind  string `json:"_aoClientKind,omitempty"`
	ClientRoute string `json:"_aoClientRoute,omitempty"`
}

// scopeParams 承载代码和路径类 RPC 的项目范围选择。
type scopeParams struct {
	Project  string   `json:"project,omitempty"`
	Projects []string `json:"projects,omitempty"`
	clientMetaParams
}

// codeSaveParams 是 ui/code/save 的请求参数。
type codeSaveParams struct {
	FilePath  string  `json:"filePath"`
	Content   *string `json:"content"`
	CreateNew bool    `json:"createNew,omitempty"`
	scopeParams
}

// codeLocateParams 是 ui/code/locate 的请求参数。
type codeLocateParams struct {
	FilePath string `json:"filePath"`
	scopeParams
}

// codeOpenParams 是 ui/code/open 的请求参数。
type codeOpenParams struct {
	FilePath string `json:"filePath"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	scopeParams
}

// pathOpenParams 是 ui/path/open 的请求参数。
type pathOpenParams struct {
	FilePath string `json:"filePath"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	scopeParams
}

// copyTextParams 是 ui/copyText 的请求参数。
type copyTextParams struct {
	Text string `json:"text"`
	clientMetaParams
}

// selectProjectDirParams 是项目目录选择 RPC 的请求参数。
type selectProjectDirParams struct {
	DefaultPath string `json:"defaultPath,omitempty"`
	clientMetaParams
}

// selectFilesParams 是文件选择 RPC 的请求参数。
type selectFilesParams struct {
	DefaultPath string             `json:"defaultPath,omitempty"`
	Filters     []selectFileFilter `json:"filters,omitempty"`
	clientMetaParams
}

// selectFileFilter 描述前端传给原生文件选择器的可选过滤器。
// DisplayName/Pattern 保持桌面 RPC wire 字段，具体合法性由 dialog 归一化阶段处理。
type selectFileFilter struct {
	DisplayName string `json:"displayName"`
	Pattern     string `json:"pattern"`
}

// saveClipboardImageParams 是保存剪贴板图片 RPC 的请求参数。
type saveClipboardImageParams struct {
	Base64Payload string `json:"base64Payload"`
	clientMetaParams
}

// saveTextFileParams 是导出文本文件 RPC 的请求参数。
type saveTextFileParams struct {
	DefaultPath     string `json:"defaultPath,omitempty"`
	DefaultFilename string `json:"defaultFilename"`
	Content         string `json:"content"`
	clientMetaParams
}

// openNewWindowParams 是打开新桌面窗口 RPC 的请求参数。
type openNewWindowParams struct {
	Group       string         `json:"group,omitempty"`
	N           int            `json:"n,omitempty"`
	UIBootstrap string         `json:"uiBootstrap,omitempty"`
	Cwd         string         `json:"cwd,omitempty"`
	Snapshot    map[string]any `json:"snapshot,omitempty"`
	clientMetaParams
}

// windowBootstrapGetParams 是获取窗口启动快照 RPC 的请求参数。
type windowBootstrapGetParams struct {
	clientMetaParams
}

// NewRPCHandlers 注册桌面专用 UI helper RPC。
// uiState 只依赖 contract.UIProjectStateFacade，避免 ui/wails 反向依赖 uistate 服务实现。
func NewRPCHandlers(app *App, cfg *config.Config, uiState contract.UIProjectStateFacade) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"ui/code/save": rpc.StrictHandler(func(ctx context.Context, p codeSaveParams) (any, error) {
			return handleCodeSave(ctx, cfg, uiState, p)
		}),
		"ui/code/locate": rpc.StrictHandler(func(ctx context.Context, p codeLocateParams) (any, error) {
			return handleCodeLocate(ctx, cfg, uiState, p)
		}),
		"ui/code/open": rpc.StrictHandler(func(ctx context.Context, p codeOpenParams) (any, error) {
			return handleCodeOpen(ctx, cfg, uiState, p)
		}),
		"ui/path/open": rpc.StrictHandler(func(ctx context.Context, p pathOpenParams) (any, error) {
			return handlePathOpen(ctx, cfg, uiState, p)
		}),
		"ui/copyText": rpc.StrictHandler(func(ctx context.Context, p copyTextParams) (any, error) {
			return handleCopyText(app, p.Text)
		}),
		"ui/buildInfo": rpc.StrictHandler(func(ctx context.Context, _ clientMetaParams) (any, error) {
			return currentBuildInfo(), nil
		}),
		"ui/saveClipboardImage": rpc.StrictHandler(func(ctx context.Context, p saveClipboardImageParams) (any, error) {
			path, err := app.SaveClipboardImage(p.Base64Payload)
			if err != nil {
				return nil, err
			}
			return map[string]string{"path": path}, nil
		}),
		"ui/saveTextFile": rpc.StrictHandler(func(ctx context.Context, p saveTextFileParams) (any, error) {
			path, err := app.saveTextFile(p.DefaultPath, p.DefaultFilename, p.Content)
			if err != nil {
				return nil, err
			}
			return map[string]string{"path": path}, nil
		}),
		"ui/sharedFile/open": rpc.StrictHandler(func(ctx context.Context, p openSharedFileParams) (any, error) {
			return handleOpenSharedFile(ctx, app, cfg, p)
		}),
		"ui/log": rpc.StrictHandler(func(ctx context.Context, p map[string]any) (any, error) {
			return handleUILog(ctx, app, p)
		}),
		"ui/selectProjectDir": rpc.StrictHandler(func(ctx context.Context, p selectProjectDirParams) (any, error) {
			path, err := app.selectProjectDir(p.DefaultPath)
			if err != nil {
				return nil, err
			}
			return map[string]string{"path": path}, nil
		}),
		"ui/selectProjectDirs": rpc.StrictHandler(func(ctx context.Context, p selectProjectDirParams) (any, error) {
			paths, err := app.SelectProjectDirs(p.DefaultPath)
			if err != nil {
				return nil, err
			}
			return map[string][]string{"paths": paths}, nil
		}),
		"ui/selectFiles": rpc.StrictHandler(func(ctx context.Context, p selectFilesParams) (any, error) {
			paths, err := app.selectFilesWithFilters(p.DefaultPath, p.Filters)
			if err != nil {
				return nil, err
			}
			return map[string][]string{"paths": paths}, nil
		}),
		"ui/readDroppedTextFiles": rpc.StrictHandler(func(ctx context.Context, p readDroppedTextFilesParams) (any, error) {
			return readDroppedTextFiles(app, p)
		}),
		"ui/windowBootstrap/get": rpc.StrictHandler(func(ctx context.Context, _ windowBootstrapGetParams) (any, error) {
			return handleUIWindowBootstrapGet(app), nil
		}),
		"ui/openNewWindow": rpc.StrictHandler(func(ctx context.Context, p openNewWindowParams) (any, error) {
			return handleUIOpenNewWindow(app, p)
		}),
	}}
}

// handleCodeSave 先按前端传入的 project/projects 解析允许的项目根，再保存范围内文件。
// 保存只允许落在这些根目录内；项目范围解析、越界路径、缺失目标或写入失败都会直接返回错误。
func handleCodeSave(
	ctx context.Context,
	cfg *config.Config,
	uiState contract.UIProjectStateFacade,
	p codeSaveParams,
) (codeSaveResult, error) {
	if p.Content == nil {
		return codeSaveResult{}, errors.New("ui/code/save: content must be a string")
	}
	roots, err := requestScopeRoots(ctx, cfg, uiState, p.Project, p.Projects)
	if err != nil {
		return codeSaveResult{}, err
	}
	return saveScopedFile(p.FilePath, *p.Content, roots, p.CreateNew)
}

// handleCodeLocate 先解析项目范围，再在允许根目录内定位候选文件。
// 定位只返回轻量路径元数据并受 codeLocateLimit 截断；项目状态读取、范围解析或搜索错误会返回给 RPC 调用方。
func handleCodeLocate(
	ctx context.Context,
	cfg *config.Config,
	uiState contract.UIProjectStateFacade,
	p codeLocateParams,
) (codeLocateResult, error) {
	roots, err := requestScopeRoots(ctx, cfg, uiState, p.Project, p.Projects)
	if err != nil {
		return codeLocateResult{}, err
	}
	return locateScopedFile(ctx, p.FilePath, roots, codeLocateLimit)
}

// handleCodeOpen 先解析项目范围，再构建代码预览并尝试打开本地编辑器。
// 预览只读取允许根目录内的目标文件；越界、缺失、二进制/超大文件或系统打开失败按下层错误返回。
func handleCodeOpen(
	ctx context.Context,
	cfg *config.Config,
	uiState contract.UIProjectStateFacade,
	p codeOpenParams,
) (codeOpenResult, error) {
	roots, err := requestScopeRoots(ctx, cfg, uiState, p.Project, p.Projects)
	if err != nil {
		return codeOpenResult{}, err
	}
	return openScopedFile(ctx, p.FilePath, p.Line, p.Column, roots)
}

// handlePathOpen 先解析项目范围，再用系统默认程序打开范围内文件或目录。
// 该 RPC 不读取文件内容；空路径、越界路径、目标不存在或系统打开失败都会阻断并返回错误。
func handlePathOpen(
	ctx context.Context,
	cfg *config.Config,
	uiState contract.UIProjectStateFacade,
	p pathOpenParams,
) (pathOpenResult, error) {
	roots, err := requestScopeRoots(ctx, cfg, uiState, p.Project, p.Projects)
	if err != nil {
		return pathOpenResult{}, err
	}
	return openScopedPath(ctx, p.FilePath, p.Line, p.Column, roots)
}

// handleCopyText 处理剪贴板写入；空文本直接拒绝，headless 模式返回软失败给前端。
func handleCopyText(app *App, text string) (map[string]any, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("clipboard text is empty")
	}
	if app == nil || app.wailsApp == nil {
		return map[string]any{
			"ok":    false,
			"error": "clipboard not available in headless mode",
		}, nil
	}
	ok, err := app.CopyText(text)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": ok}, nil
}

// handleUILog 接收前端日志批次，写入后端日志并记录脱敏 observability 事件。
func handleUILog(ctx context.Context, app *App, params map[string]any) (map[string]any, error) {
	clientKind := extractFirstString(params, "_aoClientKind")
	clientRoute := extractFirstString(params, "_aoClientRoute")
	ingested := 0

	for _, entry := range frontendLogEntries(params) {
		if entry == nil {
			continue
		}
		level, scope, event, timestamp := frontendLogMetadata(entry)
		level = normalizeFrontendLogLevel(level, scope, event)
		threadID, agentID := frontendLogIDs(entry)
		msg := fmt.Sprintf("frontend: %s.%s", scope, event)
		args := buildFrontendLogArgs(entry, clientKind, clientRoute, level, scope, event, timestamp, threadID, agentID)
		emitFrontendLog(ctx, level, msg, args)
		ingested++
	}

	if app != nil && app.observability != nil && app.observability.Enabled() {
		event := observability.TraceEvent{
			Timestamp:    time.Now(),
			TraceID:      pkglogger.TraceIDFromContext(ctx),
			SpanID:       pkglogger.SpanIDFromContext(ctx),
			ParentSpanID: pkglogger.ParentSpanIDFromContext(ctx),
			Kind:         "frontend.log.ingested",
			Phase:        "ingested",
			Method:       "ui/log",
			Status:       observability.StatusOK,
			ClientKind:   clientKind,
			ClientRoute:  clientRoute,
			Code:         observability.NewCodeAnchor("internal/ui/wails/rpc.go", "handleUILog", 212),
			Metadata: map[string]any{
				"ingested": ingested,
			},
		}
		if err := app.observability.Record(ctx, event); err != nil {
			pkglogger.FromContext(ctx).Warn("frontend log trace record failed", "method", "ui/log", "error", err)
		}
	}

	return map[string]any{"ok": true, "ingested": ingested}, nil
}

// normalizeFrontendLogLevel 降低高频成功探针的日志等级，避免正常轮询刷屏。
func normalizeFrontendLogLevel(level, scope, event string) string {
	// 已加载的旧前端包可能把成功探针报成 warn，这里按诊断日志处理。
	if scope == "scroll" {
		return "debug"
	}
	if scope == "thread" {
		switch event {
		case "sidebar.refresh.pending_join",
			"sidebar.refresh.start",
			"sidebar.refresh.api_call_start",
			"sidebar.refresh.api_call_done":
			return "debug"
		}
	}
	return level
}

// frontendLogEntries 规范化单条或批量前端日志参数。
func frontendLogEntries(params map[string]any) []map[string]any {
	entries := make([]map[string]any, 0, 1)
	switch typed := params["entries"].(type) {
	case []any:
		for _, item := range typed {
			entry, ok := item.(map[string]any)
			if ok {
				entries = append(entries, entry)
			}
		}
	case []map[string]any:
		entries = append(entries, typed...)
	}
	if len(entries) != 0 {
		return entries
	}
	return []map[string]any{params}
}

// frontendLogMetadata 提取前端日志的等级、范围、事件和时间戳。
func frontendLogMetadata(entry map[string]any) (string, string, string, string) {
	level := strings.ToLower(strings.TrimSpace(extractFirstString(entry, "level")))
	scope := strings.TrimSpace(extractFirstString(entry, "scope"))
	event := strings.TrimSpace(extractFirstString(entry, "event"))
	timestamp := strings.TrimSpace(extractFirstString(entry, "ts", "timestamp"))
	return level, scope, event, timestamp
}

// frontendLogIDs 从日志顶层或 fields 中提取 thread/agent ID。
func frontendLogIDs(entry map[string]any) (string, string) {
	threadID := strings.TrimSpace(extractFirstString(entry, "thread_id", "threadId"))
	agentID := strings.TrimSpace(extractFirstString(entry, "agent_id", "agentId"))
	fields, _ := entry["fields"].(map[string]any)
	if fields == nil {
		return threadID, agentID
	}
	if threadID == "" {
		threadID = strings.TrimSpace(extractFirstString(fields, "thread_id", "threadId"))
	}
	if agentID == "" {
		agentID = strings.TrimSpace(extractFirstString(fields, "agent_id", "agentId"))
	}
	return threadID, agentID
}

// buildFrontendLogArgs 构建前端日志字段，保留客户端来源和关联线程信息。
func buildFrontendLogArgs(
	entry map[string]any,
	clientKind string,
	clientRoute string,
	level string,
	scope string,
	event string,
	timestamp string,
	threadID string,
	agentID string,
) []any {
	args := []any{
		"source", "ui",
		"component", "frontend_log",
		"frontend_scope", scope,
		"frontend_event", event,
		"frontend_level", level,
		"frontend_seq", entry["seq"],
		"frontend_ts", timestamp,
		"frontend_fields", sanitizeFrontendLogFields(entry["fields"]),
	}
	if clientKind != "" {
		args = append(args, "client_kind", clientKind)
	}
	if clientRoute != "" {
		args = append(args, "client_route", clientRoute)
	}
	if threadID != "" {
		args = append(args, "thread_id", threadID)
	}
	if agentID != "" {
		args = append(args, "agent_id", agentID)
	}
	return args
}

// sanitizeFrontendLogFields 递归清洗前端 fields，防止 ui/log 把任意嵌套对象原样写入日志。
// 这里只保留诊断结构，命中敏感 key 或字符串模式时替换具体值。
func sanitizeFrontendLogFields(value any) any {
	return sanitizeFrontendLogValue("", value)
}

// sanitizeFrontendLogValue 按当前字段名清洗单个值，并递归进入 JSON map/slice。
// 命中敏感字段名时整值替换，普通字符串只替换其中的密钥片段。
func sanitizeFrontendLogValue(key string, value any) any {
	if secretLikeFrontendLogKey(key) {
		return frontendLogRedactedValue
	}
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return redactFrontendLogString(typed)
	case map[string]any:
		return sanitizeFrontendLogMap(typed)
	case map[string]string:
		return sanitizeFrontendLogStringMap(typed)
	case []any:
		return sanitizeFrontendLogSlice(typed)
	case []string:
		return sanitizeFrontendLogStringSlice(typed)
	default:
		return typed
	}
}

func sanitizeFrontendLogMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for childKey, childValue := range values {
		out[childKey] = sanitizeFrontendLogValue(childKey, childValue)
	}
	return out
}

func sanitizeFrontendLogStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for childKey, childValue := range values {
		if secretLikeFrontendLogKey(childKey) {
			out[childKey] = frontendLogRedactedValue
			continue
		}
		out[childKey] = redactFrontendLogString(childValue)
	}
	return out
}

func sanitizeFrontendLogSlice(values []any) []any {
	out := make([]any, len(values))
	for i, item := range values {
		out[i] = sanitizeFrontendLogValue("", item)
	}
	return out
}

func sanitizeFrontendLogStringSlice(values []string) []string {
	out := make([]string, len(values))
	for i, item := range values {
		out[i] = redactFrontendLogString(item)
	}
	return out
}

func redactFrontendLogString(value string) string {
	for _, current := range frontendLogSecretPatterns {
		value = current.pattern.ReplaceAllString(value, current.replacement)
	}
	return value
}

func secretLikeFrontendLogKey(key string) bool {
	normalized := frontendLogKeyReplacer.Replace(strings.ToLower(strings.TrimSpace(key)))
	for _, marker := range frontendLogSecretKeyMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// emitFrontendLog 按前端传入的等级写入后端结构化日志。
func emitFrontendLog(ctx context.Context, level string, msg string, args []any) {
	logger := pkglogger.Get()
	switch level {
	case "debug":
		logger.DebugContext(ctx, msg, args...)
	case "warn", "warning":
		logger.WarnContext(ctx, msg, args...)
	case "error":
		logger.ErrorContext(ctx, msg, args...)
	default:
		logger.InfoContext(ctx, msg, args...)
	}
}

// extractFirstString 按优先级提取第一个非空字符串字段。
func extractFirstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := values[key].(string)
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// handleUIWindowBootstrapGet 消费当前窗口的一次性启动快照。
func handleUIWindowBootstrapGet(app *App) map[string]map[string]any {
	return map[string]map[string]any{
		"snapshot": app.consumeWindowBootstrapSnapshot(),
	}
}

// handleUIOpenNewWindow 创建同组或指定组的新桌面窗口。
func handleUIOpenNewWindow(app *App, p openNewWindowParams) (map[string]any, error) {
	if p.N < 0 {
		return nil, fmt.Errorf("wails binding: n must be a non-negative integer")
	}

	group := normalizeWindowGroup(p.Group, app.currentWindowGroup())
	uiBootstrap, err := resolveUIBootstrap(p.UIBootstrap, p.Snapshot)
	if err != nil {
		return nil, err
	}
	windowID, err := app.openNewWindow(group, p.N, uiBootstrap, p.Cwd)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":       true,
		"windowId": windowID,
		"cwd":      strings.TrimSpace(p.Cwd),
	}, nil
}

// resolveUIBootstrap 校验或编码新窗口启动快照。
func resolveUIBootstrap(raw string, snapshot map[string]any) (string, error) {
	if len(snapshot) != 0 {
		return encodeWindowBootstrapSnapshot(snapshot)
	}
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	if _, err := decodeWindowBootstrapSnapshot(raw); err != nil {
		return "", fmt.Errorf("wails binding: invalid uiBootstrap payload: %w", err)
	}
	return strings.TrimSpace(raw), nil
}
