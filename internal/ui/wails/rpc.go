package wails

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const codeLocateLimit = 24

type clientMetaParams struct {
	ClientKind  string `json:"_aoClientKind,omitempty"`
	ClientRoute string `json:"_aoClientRoute,omitempty"`
}

type scopeParams struct {
	Project  string   `json:"project,omitempty"`
	Projects []string `json:"projects,omitempty"`
	clientMetaParams
}

type codeSaveParams struct {
	FilePath  string `json:"filePath"`
	Content   string `json:"content"`
	CreateNew bool   `json:"createNew,omitempty"`
	scopeParams
}

type codeLocateParams struct {
	FilePath string `json:"filePath"`
	scopeParams
}

type codeOpenParams struct {
	FilePath string `json:"filePath"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	scopeParams
}

type pathOpenParams struct {
	FilePath string `json:"filePath"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	scopeParams
}

type copyTextParams struct {
	Text string `json:"text"`
	clientMetaParams
}

type selectProjectDirParams struct {
	DefaultPath string `json:"defaultPath,omitempty"`
	clientMetaParams
}

type selectFilesParams struct {
	DefaultPath string             `json:"defaultPath,omitempty"`
	Filters     []selectFileFilter `json:"filters,omitempty"`
	clientMetaParams
}

type selectFileFilter struct {
	DisplayName string `json:"displayName"`
	Pattern     string `json:"pattern"`
}

type saveClipboardImageParams struct {
	Base64Payload string `json:"base64Payload"`
	clientMetaParams
}

type saveTextFileParams struct {
	DefaultPath     string `json:"defaultPath,omitempty"`
	DefaultFilename string `json:"defaultFilename"`
	Content         string `json:"content"`
	clientMetaParams
}

type openNewWindowParams struct {
	Group       string         `json:"group,omitempty"`
	N           int            `json:"n,omitempty"`
	UIBootstrap string         `json:"uiBootstrap,omitempty"`
	Cwd         string         `json:"cwd,omitempty"`
	Snapshot    map[string]any `json:"snapshot,omitempty"`
	clientMetaParams
}

type windowBootstrapGetParams struct {
	clientMetaParams
}

// NewRPCHandlers registers desktop-only UI helpers behind the generic RPC
// bridge. P22 P4 S1b: the uiState parameter is the narrow
// contract.UIProjectStateFacade — ui/wails no longer depends on
// internal/module/uistate's fat Service interface.
// NewRPCHandlers 创建RPC处理器。
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
			return handleCopyText(app, strings.TrimSpace(p.Text))
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

func handleCodeSave(
	ctx context.Context,
	cfg *config.Config,
	uiState contract.UIProjectStateFacade,
	p codeSaveParams,
) (codeSaveResult, error) {
	roots, err := requestScopeRoots(ctx, cfg, uiState, p.Project, p.Projects)
	if err != nil {
		return codeSaveResult{}, err
	}
	return saveScopedFile(p.FilePath, p.Content, roots, p.CreateNew)
}

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

func handleCopyText(app *App, text string) (map[string]any, error) {
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

// handleUILog 处理UI日志。
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

func normalizeFrontendLogLevel(level, scope, event string) string {
	// High-frequency successful lifecycle probes are diagnostics, even when an
	// already-loaded frontend bundle reports them as warn.
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

// frontendLogEntries 处理前端日志条目。
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

func frontendLogMetadata(entry map[string]any) (string, string, string, string) {
	level := strings.ToLower(strings.TrimSpace(extractFirstString(entry, "level")))
	scope := strings.TrimSpace(extractFirstString(entry, "scope"))
	event := strings.TrimSpace(extractFirstString(entry, "event"))
	timestamp := strings.TrimSpace(extractFirstString(entry, "ts", "timestamp"))
	return level, scope, event, timestamp
}

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

// buildFrontendLogArgs 构建前端日志args。
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
		"frontend_fields", entry["fields"],
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

func extractFirstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := values[key].(string)
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func handleUIWindowBootstrapGet(app *App) map[string]map[string]any {
	return map[string]map[string]any{
		"snapshot": app.consumeWindowBootstrapSnapshot(),
	}
}

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
