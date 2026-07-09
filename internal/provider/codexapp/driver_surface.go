package codexapp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// codexNativeToolDescriptors 返回 Codex 原生工具的默认治理描述表。
func codexNativeToolDescriptors() []contract.NativeToolDescriptor {
	return []contract.NativeToolDescriptor{
		{ID: contract.CodexNativeToolReadFile, Label: "直接读项目文件", Description: "绕过项目文件工具直接读取文件。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolWriteNewFile, Label: "直接新建文件", Description: "绕过项目文件编辑链路直接创建文件。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolApplyPatch, Label: "直接改文件", Description: "绕过项目文件编辑链路直接修改文件。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolShell, Label: "直接执行命令", Description: "绕过项目命令治理直接执行本地命令。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolListDir, Label: "直接列目录", Description: "绕过项目文件工具直接读取目录。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolMultiAgent, Label: "自行编排子任务", Description: "让 Codex 自己创建和管理子任务；本项目已有任务编排。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolMultiToolParallel, Label: "同时使用多个工具", Description: "让 Codex 一次使用多个内置工具；本项目已有工具调度。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolSpawnAgent, Label: "创建子任务", Description: "让 Codex 自己创建子任务；本项目已有任务编排。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolSendInput, Label: "给子任务发消息", Description: "绕过项目任务消息流直接给子任务发送输入。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolResumeAgent, Label: "恢复子任务", Description: "绕过项目任务生命周期直接恢复子任务。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolWaitAgent, Label: "等待子任务", Description: "绕过项目任务状态流直接等待子任务。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolCloseAgent, Label: "关闭子任务", Description: "绕过项目任务生命周期直接关闭子任务。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolToolSearch, Label: "自行发现工具", Description: "绕过项目工具清单自行发现可用工具。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolWebSearch, Label: "网页搜索", Description: "让模型自行搜索网页。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolImageGen, Label: "生成图片", Description: "让模型自行生成图片。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolViewImage, Label: "查看图片", Description: "让模型自行查看本地图片。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolRequestInput, Label: "向用户提问", Description: "绕过项目对话流直接向用户发起提问。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolRequestPerms, Label: "请求放行权限", Description: "绕过项目审批入口直接请求放行。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolPluginInstall, Label: "请求安装插件", Description: "绕过项目插件管理入口请求安装插件。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolListMCPResources, Label: "列出外部资源", Description: "绕过项目工具面直接读取外部资源列表。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolListMCPResourceTemplates, Label: "列出资源模板", Description: "绕过项目工具面直接读取外部资源模板。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolReadMCPResource, Label: "读取外部资源", Description: "绕过项目工具面直接读取外部资源内容。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolBrowserUse, Label: "操作内置浏览器", Description: "让模型自行操作内置浏览器。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolBrowserUseExternal, Label: "操作外部浏览器", Description: "让模型自行操作外部浏览器。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolComputerUse, Label: "操作本机应用", Description: "让模型自行操作本机应用。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolWorkspaceDeps, Label: "读取运行环境", Description: "绕过项目环境入口直接读取工作区运行环境。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolApps, Label: "使用连接器", Description: "绕过项目连接器管理直接使用连接器。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolPlugins, Label: "使用插件", Description: "绕过项目插件管理直接使用插件。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolGoals, Label: "自行管理目标", Description: "绕过项目任务视图自行管理目标。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		{ID: contract.CodexNativeToolUpdatePlan, Label: "自行更新计划", Description: "绕过项目计划和任务视图自行更新计划。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
	}
}

// SetListTools 更新动态工具列表回调。
// 回调在锁内替换，后续创建的 driver 才会读取新值，已启动会话不被原地改写。
func (f *DriverFactory) SetListTools(fn func(context.Context) ([]codexprotocol.DynamicToolSchema, error)) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listTools = fn
}

// SetPrepareTools 更新会话级动态工具准备回调。
// 该回调参与 Start/Resume 的工具面绑定，nil 表示当前运行环境不提供动态工具。
func (f *DriverFactory) SetPrepareTools(
	fn func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error),
) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepareTools = fn
}

// SetReleaseTools 更新工具面释放回调。
// session 关闭路径会调用它归还作用域资源，替换动作需持锁防止与 Create 竞态。
func (f *DriverFactory) SetReleaseTools(fn func(contract.CodexToolSurfaceScope) error) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseTools = fn
}

// SetBindTools 更新恢复路径的工具面绑定回调。
// Resume 会用它把既有 thread scope 重新绑定到新 session。
func (f *DriverFactory) SetBindTools(fn func(contract.CodexToolSurfaceScope) error) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindTools = fn
}

func (f *DriverFactory) currentListTools() func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.listTools
}

func (f *DriverFactory) currentPrepareTools() func(
	context.Context,
	contract.CodexToolSurfaceScope,
) ([]codexprotocol.DynamicToolSchema, error) {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.prepareTools
}

func (f *DriverFactory) currentBindTools() func(contract.CodexToolSurfaceScope) error {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.bindTools
}

func (f *DriverFactory) currentReleaseTools() func(contract.CodexToolSurfaceScope) error {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.releaseTools
}

// newDriver 创建单个 Codex driver 实例。
// 环境变量里的 app-server URL 优先；否则只在 legacy ServerManager 已运行时复用共享地址。
func newDriver(
	logger *slog.Logger,
	eventDispatcher *unified.EventDispatcher,
	approvals *rpc.ApprovalManager,
	reporter contract.RuntimeReporter,
	manager *ServerManager,
	pool *ServerPool,
	mirror contract.SkillMirrorReconciler,
	recovery contract.SessionRecoveryReporter,
	listTools ...func(context.Context) ([]codexprotocol.DynamicToolSchema, error),
) contract.Driver {
	if logger == nil {
		logger = pkglogger.Get()
	}
	serverURL := strings.TrimSpace(os.Getenv("CODEX_APP_SERVER_URL"))
	if serverURL == "" && manager != nil && manager.Running() {
		serverURL = manager.ServerURL()
	}
	var listToolsFn func(context.Context) ([]codexprotocol.DynamicToolSchema, error)
	if len(listTools) != 0 {
		listToolsFn = listTools[0]
	}
	return &driver{
		logger:          logger,
		serverURL:       serverURL,
		eventDispatcher: eventDispatcher,
		approvals:       approvals,
		reporter:        reporter,
		manager:         manager,
		pool:            pool,
		listTools:       listToolsFn,
		mirror:          mirror,
		recovery:        recovery,
	}
}

// codexSandboxWireJSON 将 Codex sandbox wire 值规整成 app-server 接受的 JSON。
// 字符串和对象两种历史形态都兼容；无法识别时原样返回，让下游按真实输入报错。
func codexSandboxWireJSON(raw json.RawMessage) json.RawMessage {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if mode := canonicalCodexSandboxMode(text); mode != "" {
			return mustJSON(mode)
		}
		return raw
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	if mode := sandboxModeObjectValue(obj); mode != "" {
		return mustJSON(mode)
	}
	if len(obj) == 1 {
		for key := range obj {
			if mode := canonicalCodexSandboxMode(key); mode != "" {
				return mustJSON(mode)
			}
		}
	}
	return raw
}

// codexSandboxPolicyWireJSON 将历史 sandbox 配置转成 turn/start 接受的 sandboxPolicy 对象。
// thread/start 仍只发送 mode string；writable roots/networkAccess 必须随 turn/start 传给 app-server。
func codexSandboxPolicyWireJSON(raw json.RawMessage) json.RawMessage {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return codexSandboxPolicyFromMode(canonicalCodexSandboxMode(text), nil)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	if mode := sandboxModeObjectValue(obj); mode != "" {
		return codexSandboxPolicyFromMode(mode, obj)
	}
	if len(obj) == 1 {
		for key := range obj {
			if mode := canonicalCodexSandboxMode(key); mode != "" {
				return codexSandboxPolicyFromMode(mode, obj)
			}
		}
	}
	return raw
}

func codexSandboxPolicyFromMode(mode string, obj map[string]any) json.RawMessage {
	var policy map[string]any
	switch mode {
	case "read-only":
		policy = map[string]any{"type": "readOnly"}
		copySandboxReadOnlyAccessField(policy, obj)
		copySandboxBoolField(policy, obj, "networkAccess", "network_access")
	case "workspace-write":
		policy = map[string]any{"type": "workspaceWrite"}
		if roots := sandboxStringSliceField(obj, "writableRoots", "writable_roots"); len(roots) > 0 {
			policy["writableRoots"] = roots
		}
		copySandboxBoolField(policy, obj, "networkAccess", "network_access")
		copySandboxBoolField(policy, obj, "excludeSlashTmp", "exclude_slash_tmp")
		copySandboxBoolField(policy, obj, "excludeTmpdirEnvVar", "exclude_tmpdir_env_var")
	case "danger-full-access":
		policy = map[string]any{"type": "dangerFullAccess"}
	default:
		return nil
	}
	return mustJSON(policy)
}

func copySandboxReadOnlyAccessField(policy map[string]any, obj map[string]any) {
	access, ok := sandboxMapField(obj, "access")
	if !ok {
		return
	}
	if restricted := restrictedReadOnlyAccessValue(access); restricted != nil {
		policy["access"] = restricted
	}
}

func copySandboxBoolField(policy map[string]any, obj map[string]any, camelKey, snakeKey string) {
	if value, ok := sandboxBoolField(obj, camelKey, snakeKey); ok {
		policy[camelKey] = value
	}
}

func sandboxBoolField(obj map[string]any, keys ...string) (bool, bool) {
	if len(obj) == 0 {
		return false, false
	}
	for _, key := range keys {
		value, ok := obj[key]
		if !ok {
			continue
		}
		typed, ok := value.(bool)
		return typed, ok
	}
	return false, false
}

func sandboxMapField(obj map[string]any, key string) (map[string]any, bool) {
	if len(obj) == 0 {
		return nil, false
	}
	value, ok := obj[key]
	if !ok {
		return nil, false
	}
	typed, ok := value.(map[string]any)
	return typed, ok
}

func restrictedReadOnlyAccessValue(access map[string]any) map[string]any {
	if len(access) == 0 {
		return nil
	}
	accessType, _ := access["type"].(string)
	if !strings.EqualFold(strings.TrimSpace(accessType), "restricted") {
		return nil
	}
	roots := sandboxStringSliceField(access, "readableRoots", "readable_roots")
	if len(roots) == 0 {
		return nil
	}
	out := map[string]any{
		"type":          "restricted",
		"readableRoots": roots,
	}
	copySandboxBoolField(out, access, "includePlatformDefaults", "include_platform_defaults")
	return out
}

func codexReadOnlySandboxPolicyValueFromRaw(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return codexReadOnlySandboxPolicyValue()
	}
	var policy map[string]any
	if err := json.Unmarshal(raw, &policy); err != nil {
		return codexReadOnlySandboxPolicyValue()
	}
	return codexReadOnlySandboxPolicyValueFromMap(policy)
}

func codexReadOnlySandboxPolicyValueFromAny(raw any) map[string]any {
	switch typed := raw.(type) {
	case map[string]any:
		return codexReadOnlySandboxPolicyValueFromMap(typed)
	case json.RawMessage:
		return codexReadOnlySandboxPolicyValueFromRaw(typed)
	case []byte:
		return codexReadOnlySandboxPolicyValueFromRaw(json.RawMessage(typed))
	case string:
		return codexReadOnlySandboxPolicyValueFromRaw(json.RawMessage(typed))
	default:
		return codexReadOnlySandboxPolicyValue()
	}
}

func codexReadOnlySandboxPolicyValueFromMap(policy map[string]any) map[string]any {
	out := codexReadOnlySandboxPolicyValue()
	if len(policy) == 0 {
		return out
	}
	policyType, _ := policy["type"].(string)
	if !strings.EqualFold(strings.TrimSpace(policyType), "readOnly") {
		return out
	}
	if access, ok := sandboxMapField(policy, "access"); ok {
		if restricted := restrictedReadOnlyAccessValue(access); restricted != nil {
			out["access"] = restricted
		}
	}
	copySandboxBoolField(out, policy, "networkAccess", "network_access")
	return out
}

// sandboxStringSliceField 从 sandbox 对象读取 camelCase 或 snake_case 列表字段。
// 前端和历史配置可能使用不同命名，转换时只保留非空字符串，避免把非法元素传给 app-server。
func sandboxStringSliceField(obj map[string]any, keys ...string) []string {
	if len(obj) == 0 {
		return nil
	}
	for _, key := range keys {
		value, ok := obj[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []string:
			return append([]string(nil), typed...)
		case []any:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, text)
				}
			}
			return out
		default:
			return nil
		}
	}
	return nil
}

func sandboxModeObjectValue(obj map[string]any) string {
	for _, key := range []string{"mode", "type"} {
		value, _ := obj[key].(string)
		if mode := canonicalCodexSandboxMode(value); mode != "" {
			return mode
		}
	}
	return ""
}

func canonicalCodexSandboxMode(value string) string {
	key := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch key {
	case "readonly":
		return "read-only"
	case "workspacewrite":
		return "workspace-write"
	case "dangerfullaccess":
		return "danger-full-access"
	default:
		return ""
	}
}

func hasAnyConfigKey(cfg map[string]any, keys ...string) bool {
	return hasAnyKey(cfg, keys...)
}
