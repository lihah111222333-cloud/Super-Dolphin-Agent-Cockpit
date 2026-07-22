package wails

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	// defaultGroup 是未显式分组的新窗口使用的默认窗口组。
	defaultGroup            = "default"
	frontendReadinessMethod = "ui/frontend/readiness"
	frontendReadinessProbe  = "probe"
	frontendReadinessCommit = "commit"
)

// App 是暴露给 Wails 前端的后端绑定对象。
// 它同时持有 RPC dispatch、窗口状态和拖拽文件登记表，跨 goroutine 字段必须通过各自 mutex 访问。
type App struct {
	dispatch         func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) // 前端 callAPI 的后端 RPC 分发入口
	emitter          func(event string, data any)                                                              // Wails 原生事件发送器
	pushRuntimeEvent func(ctx context.Context, event string, data any)                                         // WebSocket runtime 事件推送入口
	wailsApp         *application.App                                                                          // Wails 应用实例，原生 dialog 和窗口操作依赖它
	windowTitle      string                                                                                    // 新窗口标题
	debug            bool                                                                                      // 是否启用 devtools 和调试窗口行为
	observability    *observability.Service                                                                    // callAPI trace 写入服务

	frontendReadinessMu sync.RWMutex         // 保护当前 App 绑定的 readiness 与 lifecycle owner
	frontendReadiness   *ActivationReadiness // 当前 Wails application 的 activation readiness
	frontendLifecycle   *WailsLifecycle      // 当前 Wails application 的 frontend lifecycle owner

	group string // 当前窗口组

	windowStateMu         sync.Mutex                // 保护窗口启动快照和窗口组映射
	windowBootstrap       map[string]any            // 兼容旧入口的一次性启动快照
	windowBootstrapByName map[string]map[string]any // 按窗口名登记的一次性启动快照
	windowGroups          map[string]string         // 窗口名到窗口组的映射

	droppedFilesMu sync.Mutex                   // 保护近期拖拽文件登记表
	droppedFiles   map[string]droppedFileRecord // 路径到可读取窗口和过期时间的映射

	openNewWindowInvoker func(group string, n int, uiBootstrap, cwd string) (string, error)   // 测试可替换的新窗口打开函数
	saveDirectoryInvoker func(defaultPath string) (string, error)                             // 测试可替换的目录选择函数
	selectFileInvoker    func(defaultPath string, filters []selectFileFilter) (string, error) // 测试可替换的单文件选择函数
	currentWindowNameFn  func() string                                                        // 测试可替换的当前窗口名函数

	datasourceImportPickerTokens *datasourceImportPickerTokens // datasource 本地文件导入的短期 capability 状态
}

type frontendReadinessRequest struct {
	Phase string `json:"phase"`
	Epoch uint64 `json:"epoch,omitempty"`
}

type frontendReadinessResponse struct {
	Epoch uint64 `json:"epoch"`
}

// CallAPI 处理前端统一 RPC 调用，并围绕 dispatch 记录脱敏 trace。
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
	var result json.RawMessage
	if method == frontendReadinessMethod {
		result, err = a.handleFrontendReadiness(params)
	} else {
		result, err = a.dispatch(ctx, method, params)
	}
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

// bindFrontendReadiness 绑定此 App 实例所属的就绪状态，禁止跨 application 复用。
func (a *App) bindFrontendReadiness(readiness *ActivationReadiness, lifecycle *WailsLifecycle) error {
	if a == nil {
		return errors.New("wails frontend readiness: app binding is required")
	}
	if readiness == nil || lifecycle == nil {
		return errors.New("wails frontend readiness: owners are required")
	}
	a.frontendReadinessMu.Lock()
	defer a.frontendReadinessMu.Unlock()
	if a.frontendReadiness != nil && a.frontendReadiness != readiness {
		return errors.New("wails frontend readiness: app is already bound to another activation")
	}
	if a.frontendLifecycle != nil && a.frontendLifecycle != lifecycle {
		return errors.New("wails frontend readiness: app is already bound to another lifecycle")
	}
	a.frontendReadiness = readiness
	a.frontendLifecycle = lifecycle
	return nil
}

// frontendReadinessOwners 返回当前 App 实例绑定的 activation 与 lifecycle owner。
func (a *App) frontendReadinessOwners() (*ActivationReadiness, *WailsLifecycle, error) {
	if a == nil {
		return nil, nil, errors.New("wails frontend readiness: app binding is required")
	}
	a.frontendReadinessMu.RLock()
	readiness := a.frontendReadiness
	lifecycle := a.frontendLifecycle
	a.frontendReadinessMu.RUnlock()
	if readiness == nil || lifecycle == nil {
		return nil, nil, errors.New("wails frontend readiness: owners are not bound")
	}
	return readiness, lifecycle, nil
}

// handleFrontendReadiness 处理经过真实 Wails CallAPI 边界到达的前端 probe/commit。
func (a *App) handleFrontendReadiness(params json.RawMessage) (json.RawMessage, error) {
	request, err := decodeFrontendReadinessRequest(params)
	if err != nil {
		return nil, err
	}
	readiness, lifecycle, err := a.frontendReadinessOwners()
	if err != nil {
		return nil, err
	}
	epoch, err := readiness.CurrentEpoch()
	if err != nil {
		return nil, err
	}
	switch request.Phase {
	case frontendReadinessProbe:
		if request.Epoch != 0 {
			return nil, errors.New("wails frontend readiness: probe must not include an epoch")
		}
	case frontendReadinessCommit:
		if request.Epoch != epoch {
			return nil, errors.New("wails frontend readiness: epoch does not match current activation")
		}
		// 先更新 lifecycle，再释放 ACK gate，避免 ACK 后仍观察到未 ready 的生命周期状态。
		lifecycle.MarkFrontendReady()
		if err := readiness.MarkFrontendReady(epoch); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("wails frontend readiness: phase must be probe or commit")
	}
	result, err := json.Marshal(frontendReadinessResponse{Epoch: epoch})
	if err != nil {
		return nil, fmt.Errorf("wails frontend readiness: encode response: %w", err)
	}
	return result, nil
}

// decodeFrontendReadinessRequest 严格解析握手载荷，拒绝未知字段和尾随 JSON。
func decodeFrontendReadinessRequest(params json.RawMessage) (frontendReadinessRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(params))
	decoder.DisallowUnknownFields()
	var request frontendReadinessRequest
	if err := decoder.Decode(&request); err != nil {
		return frontendReadinessRequest{}, fmt.Errorf("wails frontend readiness: decode request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return frontendReadinessRequest{}, errors.New("wails frontend readiness: request must contain one JSON object")
	}
	request.Phase = strings.TrimSpace(request.Phase)
	if request.Phase == "" {
		return frontendReadinessRequest{}, errors.New("wails frontend readiness: phase is required")
	}
	return request, nil
}

// prepareCallAPIRequest 校验前端 RPC 请求并建立 trace 上下文。
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

// stripCallAPITraceParams 在进入严格 RPC handler 前剥离前端 trace/meta 字段。
func stripCallAPITraceParams(method string, params json.RawMessage) json.RawMessage {
	if method == "ui/log" {
		return stripFrontendTraceMeta(params)
	}
	return stripFrontendMeta(params)
}

// contextWithObservabilityTraceFromLogger 把 logger trace 注入 observability 上下文。
func contextWithObservabilityTraceFromLogger(ctx context.Context) context.Context {
	traceID := pkglogger.TraceIDFromContext(ctx)
	spanID := pkglogger.SpanIDFromContext(ctx)
	if traceID == "" || spanID == "" {
		return ctx
	}
	return observability.ContextWithSpan(ctx, traceID, spanID, pkglogger.ParentSpanIDFromContext(ctx))
}

// recordCallAPITrace 记录 callAPI 生命周期事件；只写参数大小和键名，不落原始参数。
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

// wailsParamKeys 提取 JSON object 顶层键名，用于脱敏 trace 元数据。
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

// wailsTraceStatus 根据耗时把 callAPI trace 标为 OK 或慢调用。
func wailsTraceStatus(method string, duration time.Duration) observability.Status {
	if duration > wailsSlowThreshold(method) {
		return observability.StatusSlow
	}
	return observability.StatusOK
}

// wailsSlowThreshold 返回不同前端 RPC 方法的慢调用阈值。
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

// LaunchAgent 保留旧桌面绑定入口，并转发到 typed thread/start RPC。
// name 暂不入参传递，避免在正式线程命名流程恢复前写入不一致的展示名。
func (a *App) LaunchAgent(name, prompt, cwd string) (any, error) {
	_ = name
	return a.callAPIObject("thread/start", map[string]string{
		"cwd":              strings.TrimSpace(cwd),
		"baseInstructions": prompt,
	})
}

// StopAgent 保留旧桌面绑定方法名，并把停止请求委托给 thread/stop。
func (a *App) StopAgent(threadID string) error {
	_, err := a.callAPIObject("thread/stop", map[string]string{
		"threadId": strings.TrimSpace(threadID),
	})
	return err
}

// ListAgents 通过统一 RPC 返回 agent 列表，保持旧 Wails 方法兼容。
func (a *App) ListAgents() (any, error) {
	return a.callAPIObject("agent/list", struct{}{})
}

// GetBuildInfo 返回桌面前端展示用的构建信息。
func (a *App) GetBuildInfo() map[string]string {
	return currentBuildInfo()
}

// GetGroup 返回当前窗口所属分组，空值时由窗口状态逻辑回到默认组。
func (a *App) GetGroup() string {
	return a.currentWindowGroup()
}

// OpenNewWindow 打开同组或指定组的新窗口，并保持旧 Wails 绑定签名。
func (a *App) OpenNewWindow(group string, n int, uiBootstrap, cwd string) error {
	_, err := a.openNewWindow(group, n, uiBootstrap, cwd)
	return err
}

// openNewWindow 创建新窗口并登记一次性启动快照，测试可替换 invoker。
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
	// 前端从 window.location.search 读取 ao_ui_bootstrap/ao_window_cwd。
	name := buildWindowName(group, n)
	window := createWindow(app, title, a.debug, name, uiBootstrap, cwd, a)
	if window == nil {
		return "", errors.New("wails binding: failed to create window")
	}
	a.registerWindowState(name, group, snapshot)
	return fmt.Sprintf("%d", window.ID()), nil
}

// bindRuntime 绑定 Wails runtime，让后端事件可以发往当前窗口。
func (a *App) bindRuntime(wailsApp *application.App) {
	a.wailsApp = wailsApp
	a.emitter = func(event string, data any) {
		if wailsApp == nil || wailsApp.Event == nil {
			return
		}
		wailsApp.Event.Emit(event, data)
	}
}

// emitRuntimeEvent 向当前窗口发送 runtime 事件，并在可用时同步推送 WebSocket。
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

// callContext 返回前端调用使用的基础 context。
func (a *App) callContext() context.Context {
	if a != nil && a.wailsApp != nil {
		if ctx := a.wailsApp.Context(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

// callAPIObject 用 Go 对象参数调用内部 RPC 并返回解码后的结果。
func (a *App) callAPIObject(method string, params any) (any, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return a.CallAPI(method, json.RawMessage(data))
}

// stripFrontendMeta 移除已知前端 meta 字段；未知 _ao 字段保留给 strict handler 拒绝。
func stripFrontendMeta(raw json.RawMessage) json.RawMessage {
	return stripJSONFields(raw, func(key string) bool {
		return isFrontendMetaField(key)
	})
}

// stripFrontendTraceMeta 移除 request/trace meta，保留 ui/log 需要的客户端来源字段。
func stripFrontendTraceMeta(raw json.RawMessage) json.RawMessage {
	return stripJSONFields(raw, func(key string) bool {
		return key == "_aoRequestId" || key == "_aoTraceparent" || key == "_aoTraceId" || key == "_aoSpanId"
	})
}

func isFrontendMetaField(key string) bool {
	switch key {
	case "_aoClientKind", "_aoClientRoute", "_aoRequestId", "_aoTraceparent", "_aoTraceId", "_aoSpanId":
		return true
	default:
		return false
	}
}

// stripJSONFields 按谓词删除 JSON object 顶层字段，非 object 或重编码失败时保留原载荷。
func stripJSONFields(raw json.RawMessage, shouldStrip func(string) bool) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw // 非 object 载荷直接交给下游 strict handler 判断。
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

// frontendTraceContext 从前端 meta 中恢复 traceparent，元数据不一致时 fail-fast。
func frontendTraceContext(ctx context.Context, raw json.RawMessage) (context.Context, error) {
	if !isJSONObject(raw) {
		return ctx, nil
	}
	obj, err := decodeFrontendMetaObject(raw)
	if err != nil {
		return nil, err
	}
	trace, ok, err := pkglogger.ExtractAOTraceCarrierJSON(obj)
	if err != nil {
		return nil, fmt.Errorf("wails binding: %w", err)
	}
	if !ok {
		return ctx, nil
	}
	return pkglogger.WithTraceContext(ctx, trace.TraceID, trace.SpanID, ""), nil
}

// isJSONObject 快速判断 RawMessage 是否为 JSON object。
func isJSONObject(raw json.RawMessage) bool {
	return strings.HasPrefix(strings.TrimSpace(string(raw)), "{")
}

// decodeFrontendMetaObject 解码前端 meta object，空载荷按空 object 处理。
func decodeFrontendMetaObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("wails binding: decode frontend metadata: %w", err)
	}
	return obj, nil
}

// decodeAPIResult 把 RPC JSON 结果还原为前端可消费的 Go 值。
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

// currentBuildInfo 汇总运行平台、版本和 VCS 构建信息供前端展示。
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

// applyBuildSetting 将 Go build setting 中的 VCS 字段写入构建信息。
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

// shortCommit 截短构建注入的 commit hash，便于 UI 展示。
func shortCommit(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) <= 12 {
		return raw
	}
	return raw[:12]
}
