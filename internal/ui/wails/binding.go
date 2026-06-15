package wails

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	defaultGroup = "default"
)

type App struct {
	dispatch         func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
	emitter          func(event string, data any)
	pushRuntimeEvent func(ctx context.Context, event string, data any)
	wailsApp         *application.App
	windowTitle      string
	debug            bool
	observability    *observability.Service

	group string

	windowStateMu         sync.Mutex
	windowBootstrap       map[string]any
	windowBootstrapByName map[string]map[string]any
	windowGroups          map[string]string

	droppedFilesMu sync.Mutex
	droppedFiles   map[string]droppedFileRecord

	openNewWindowInvoker func(group string, n int, uiBootstrap, cwd string) (string, error)
	saveDirectoryInvoker func(defaultPath string) (string, error)
	currentWindowNameFn  func() string
}

// CallAPI 调用API。
func (a *App) CallAPI(method string, params json.RawMessage) (any, error) {
	method, params, ctx, err := a.prepareCallAPIRequest(method, params)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	if err := a.recordCallAPITrace(ctx, method, params, startedAt, "wails.call_api.start", "start", 0, observability.StatusOK, nil); err != nil {
		pkglogger.FromContext(ctx).Warn("wails call api trace record failed", "phase", "start", "method", method, "error", err)
	}
	params = stripCallAPITraceParams(method, params)
	result, err := a.dispatch(ctx, method, params)
	if err != nil {
		if recordErr := a.recordCallAPITrace(ctx, method, params, startedAt, "wails.call_api.failed", "failed", time.Since(startedAt), observability.StatusError, err); recordErr != nil {
			pkglogger.FromContext(ctx).Warn("wails call api trace record failed", "phase", "failed", "method", method, "error", recordErr)
		}
		return nil, err
	}
	duration := time.Since(startedAt)
	if err := a.recordCallAPITrace(ctx, method, params, startedAt, "wails.call_api.done", "done", duration, wailsTraceStatus(method, duration), nil); err != nil {
		pkglogger.FromContext(ctx).Warn("wails call api trace record failed", "phase", "done", "method", method, "error", err)
	}
	return decodeAPIResult(result)
}

// prepareCallAPIRequest 准备callAPI请求。
func (a *App) prepareCallAPIRequest(method string, params json.RawMessage) (string, json.RawMessage, context.Context, error) {
	if a == nil || a.dispatch == nil {
		return "", nil, nil, errors.New("wails binding: dispatch is not configured")
	}
	method = strings.TrimSpace(method)
	if method == "" {
		return "", nil, nil, errors.New("wails binding: method is required")
	}
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	ctx, err := frontendTraceContext(a.callContext(), params)
	if err != nil {
		return "", nil, nil, err
	}
	ctx, _, err = pkglogger.WithChildTraceSpan(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	ctx = contextWithObservabilityTraceFromLogger(ctx)
	return method, params, ctx, nil
}

func stripCallAPITraceParams(method string, params json.RawMessage) json.RawMessage {
	if method == "ui/log" {
		return stripFrontendTraceMeta(params)
	}
	return stripFrontendMeta(params)
}

func contextWithObservabilityTraceFromLogger(ctx context.Context) context.Context {
	traceID := pkglogger.TraceIDFromContext(ctx)
	spanID := pkglogger.SpanIDFromContext(ctx)
	if traceID == "" || spanID == "" {
		return ctx
	}
	return observability.ContextWithSpan(ctx, traceID, spanID, pkglogger.ParentSpanIDFromContext(ctx))
}

// recordCallAPITrace 记录callAPItrace。
func (a *App) recordCallAPITrace(ctx context.Context, method string, params json.RawMessage, startedAt time.Time, kind string, phase string, duration time.Duration, status observability.Status, callErr error) error {
	if a == nil || a.observability == nil || !a.observability.Enabled() {
		return nil
	}
	// 误判防护：recordCallAPITrace 只记录 param_bytes/param_keys，不记录 raw params。
	metadata := map[string]any{"param_bytes": len(params)}
	if keys := wailsParamKeys(params); len(keys) > 0 {
		metadata["param_keys"] = keys
	}
	event := observability.TraceEvent{
		Timestamp:    startedAt,
		TraceID:      pkglogger.TraceIDFromContext(ctx),
		SpanID:       pkglogger.SpanIDFromContext(ctx),
		ParentSpanID: pkglogger.ParentSpanIDFromContext(ctx),
		Kind:         kind,
		Phase:        phase,
		Method:       strings.TrimSpace(method),
		DurationMS:   duration.Milliseconds(),
		Status:       status,
		Code:         observability.NewCodeAnchor("internal/ui/wails/binding.go", "(*App).CallAPI", 46),
		Metadata:     metadata,
	}
	if callErr != nil {
		event.Error = strings.TrimSpace(callErr.Error())
	}
	return a.observability.Record(ctx, event)
}

func wailsParamKeys(raw json.RawMessage) []string {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, strings.TrimSpace(key))
	}
	sort.Strings(keys)
	return keys
}

func wailsTraceStatus(method string, duration time.Duration) observability.Status {
	if duration > wailsSlowThreshold(method) {
		return observability.StatusSlow
	}
	return observability.StatusOK
}

func wailsSlowThreshold(method string) time.Duration {
	switch {
	case strings.TrimSpace(method) == "thread/start":
		return 1000 * time.Millisecond
	case strings.HasPrefix(strings.TrimSpace(method), "ui/"):
		return 300 * time.Millisecond
	default:
		return 500 * time.Millisecond
	}
}

// LaunchAgent preserves the legacy desktop entrypoint while routing creation
// through the typed V3 thread/start RPC using the V2 baseInstructions field.
// The legacy name is deferred until a first-class thread naming flow is restored.
// LaunchAgent 启动代理。
func (a *App) LaunchAgent(name, prompt, cwd string) (any, error) {
	_ = name
	return a.callAPIObject("thread/start", map[string]string{
		"cwd":              strings.TrimSpace(cwd),
		"baseInstructions": prompt,
	})
}

// StopAgent keeps the V2 method name while delegating execution to thread/stop.
// StopAgent 停止代理。
func (a *App) StopAgent(threadID string) error {
	_, err := a.callAPIObject("thread/stop", map[string]string{
		"threadId": strings.TrimSpace(threadID),
	})
	return err
}

// ListAgents 列出代理。
func (a *App) ListAgents() (any, error) {
	return a.callAPIObject("agent/list", struct{}{})
}

// GetBuildInfo 读取buildinfo。
func (a *App) GetBuildInfo() map[string]string {
	return currentBuildInfo()
}

// GetGroup 读取group。
func (a *App) GetGroup() string {
	return a.currentWindowGroup()
}

// OpenNewWindow 打开newwindow。
func (a *App) OpenNewWindow(group string, n int, uiBootstrap, cwd string) error {
	_, err := a.openNewWindow(group, n, uiBootstrap, cwd)
	return err
}

// openNewWindow 打开newwindow。
func (a *App) openNewWindow(group string, n int, uiBootstrap, cwd string) (string, error) {
	if a != nil && a.openNewWindowInvoker != nil {
		return a.openNewWindowInvoker(group, n, uiBootstrap, cwd)
	}
	app, err := a.requireWailsApp()
	if err != nil {
		return "", err
	}
	snapshot, err := decodeWindowBootstrapSnapshot(uiBootstrap)
	if err != nil {
		return "", fmt.Errorf("wails binding: decode ui bootstrap: %w", err)
	}
	group = normalizeWindowGroup(group, a.currentWindowGroup())
	title := strings.TrimSpace(a.windowTitle)
	if title == "" {
		title = applicationTitle()
	}
	// Backend propagates bootstrap values into the window URL; frontend
	// consumers read ao_ui_bootstrap/ao_window_cwd from window.location.search.
	name := buildWindowName(group, n)
	window := createWindow(app, title, a.debug, name, uiBootstrap, cwd, a)
	if window == nil {
		return "", errors.New("wails binding: failed to create window")
	}
	a.registerWindowState(name, group, snapshot)
	return fmt.Sprintf("%d", window.ID()), nil
}

func (a *App) bindRuntime(wailsApp *application.App) {
	a.wailsApp = wailsApp
	a.emitter = func(event string, data any) {
		if wailsApp == nil || wailsApp.Event == nil {
			return
		}
		wailsApp.Event.Emit(event, data)
	}
}

func (a *App) emitRuntimeEvent(event string, data any) {
	event = strings.TrimSpace(event)
	if a == nil || event == "" {
		return
	}
	if a.emitter != nil {
		a.emitter(event, data)
	}
	if a.pushRuntimeEvent != nil {
		a.pushRuntimeEvent(a.callContext(), event, data)
	}
}

func (a *App) callContext() context.Context {
	if a != nil && a.wailsApp != nil {
		if ctx := a.wailsApp.Context(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

func (a *App) callAPIObject(method string, params any) (any, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return a.CallAPI(method, json.RawMessage(data))
}

// stripFrontendMeta removes _ao-prefixed metadata fields that the frontend
// injects into every CallAPI payload (e.g. _aoClientKind, _aoClientRoute).
// These are useful for logging but must not reach StrictHandler RPC endpoints
// which reject unknown fields.
func stripFrontendMeta(raw json.RawMessage) json.RawMessage {
	return stripJSONFields(raw, func(key string) bool {
		return strings.HasPrefix(key, "_ao")
	})
}

func stripFrontendTraceMeta(raw json.RawMessage) json.RawMessage {
	return stripJSONFields(raw, func(key string) bool {
		return key == "_aoTraceparent" || key == "_aoTraceId" || key == "_aoSpanId"
	})
}

// stripJSONFields 处理stripJSON字段。
func stripJSONFields(raw json.RawMessage, shouldStrip func(string) bool) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw // not an object — pass through as-is
	}
	changed := false
	for key := range obj {
		if shouldStrip(key) {
			delete(obj, key)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	cleaned, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return cleaned
}

// frontendTraceContext 处理前端trace上下文。
func frontendTraceContext(ctx context.Context, raw json.RawMessage) (context.Context, error) {
	if !isJSONObject(raw) {
		return ctx, nil
	}
	obj, err := decodeFrontendMetaObject(raw)
	if err != nil {
		return nil, err
	}
	traceparent, ok, err := frontendStringField(obj, "_aoTraceparent")
	if err != nil {
		return nil, err
	}
	if !ok {
		return ctx, nil
	}
	traceID, spanID, err := parseFrontendTraceparent(traceparent)
	if err != nil {
		return nil, fmt.Errorf("wails binding: invalid _aoTraceparent: %w", err)
	}
	if err := validateFrontendTraceMetadata(obj, traceID, spanID); err != nil {
		return nil, err
	}
	return pkglogger.WithTraceContext(ctx, traceID, spanID, ""), nil
}

func isJSONObject(raw json.RawMessage) bool {
	return strings.HasPrefix(strings.TrimSpace(string(raw)), "{")
}

func decodeFrontendMetaObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("wails binding: decode frontend metadata: %w", err)
	}
	return obj, nil
}

// validateFrontendTraceMetadata 校验前端trace元数据。
func validateFrontendTraceMetadata(obj map[string]json.RawMessage, traceID, spanID string) error {
	if metadataTraceID, ok, err := frontendStringField(obj, "_aoTraceId"); err != nil {
		return err
	} else if ok && metadataTraceID != traceID {
		return fmt.Errorf("wails binding: mismatched _aoTraceId")
	}
	if metadataSpanID, ok, err := frontendStringField(obj, "_aoSpanId"); err != nil {
		return err
	} else if ok && metadataSpanID != spanID {
		return fmt.Errorf("wails binding: mismatched _aoSpanId")
	}
	return nil
}

func frontendStringField(obj map[string]json.RawMessage, key string) (string, bool, error) {
	raw, ok := obj[key]
	if !ok {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, fmt.Errorf("wails binding: %s must be a string", key)
	}
	return value, true, nil
}

// parseFrontendTraceparent 解析前端traceparent。
func parseFrontendTraceparent(value string) (string, string, error) {
	parts := strings.Split(value, "-")
	if len(parts) != 4 {
		return "", "", fmt.Errorf("expected 4 dash-separated fields")
	}
	if parts[0] != "00" {
		return "", "", fmt.Errorf("unsupported version %q", parts[0])
	}
	traceID, spanID, flags := parts[1], parts[2], parts[3]
	if err := validateTraceID(traceID); err != nil {
		return "", "", err
	}
	if err := validateSpanID(spanID); err != nil {
		return "", "", err
	}
	if err := validateTraceFlags(flags); err != nil {
		return "", "", err
	}
	return traceID, spanID, nil
}

func validateTraceID(value string) error {
	if len(value) != 32 || !isLowerHex(value) || allZeroHex(value) {
		return fmt.Errorf("invalid trace id")
	}
	return nil
}

func validateSpanID(value string) error {
	if len(value) != 16 || !isLowerHex(value) || allZeroHex(value) {
		return fmt.Errorf("invalid span id")
	}
	return nil
}

func validateTraceFlags(value string) error {
	if len(value) != 2 || !isLowerHex(value) {
		return fmt.Errorf("invalid flags")
	}
	return nil
}

// isLowerHex 判断lowerhex是否可用。
func isLowerHex(value string) bool {
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func allZeroHex(value string) bool {
	for _, ch := range value {
		if ch != '0' {
			return false
		}
	}
	return true
}

func decodeAPIResult(result json.RawMessage) (any, error) {
	if len(result) == 0 || string(result) == "null" {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(result, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// currentBuildInfo 处理当前buildinfo。
func currentBuildInfo() map[string]string {
	info := map[string]string{
		"version": "dev",
		"commit":  "unknown",
		"runtime": runtime.GOOS + "/" + runtime.GOARCH,
	}
	if appVersion := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_UPDATE_VERSION")); appVersion != "" {
		info["appVersion"] = appVersion
	}
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if version := strings.TrimSpace(buildInfo.Main.Version); version != "" && version != "(devel)" {
		info["version"] = version
	}
	for _, setting := range buildInfo.Settings {
		applyBuildSetting(info, setting.Key, setting.Value)
	}
	return info
}

// applyBuildSetting 应用buildsetting。
func applyBuildSetting(info map[string]string, key, value string) {
	switch key {
	case "vcs.revision":
		if commit := shortCommit(value); commit != "" {
			info["commit"] = commit
		}
	case "vcs.time":
		if value = strings.TrimSpace(value); value != "" {
			info["buildTime"] = value
		}
	case "vcs.modified":
		if strings.EqualFold(strings.TrimSpace(value), "true") {
			info["dirty"] = "true"
		}
	}
}

func shortCommit(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) <= 12 {
		return raw
	}
	return raw[:12]
}
