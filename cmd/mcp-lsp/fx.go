// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/tools"
	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformmetrics "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"go.uber.org/fx"
)

// bootstrapRunner 持有启动配置和控制面客户端，等待 stdio server 就绪后再连接 RPC。
type bootstrapRunner struct {
	cfg        bootstrap.Config
	client     *bootstrap.Client
	logRuntime *pkglogger.Runtime
	stdioReady <-chan struct{} // closed when stdio server is ready
}

// runtimeParams 聚合 fx 注入的 runner 列表和 shutdowner，供 bindRuntime 使用。
type runtimeParams struct {
	fx.In

	Runners    []platformrunner.Runner `group:"runners"`
	Shutdowner fx.Shutdowner
}

// registryToolProvider 基于工具定义列表实现 common.ToolProvider，支持按需过滤语义 LSP 工具。
type registryToolProvider struct {
	defs                   []toolDefinition
	semanticToolsAvailable func(context.Context) bool
	ensureReady            func() error
}

// run 组装并启动 mcp-lsp sidecar 自身的 fx 应用。
// 该进程只暴露 ctl 工具与 manifest 元数据，stdout 必须保留给 MCP stdio 协议通道。
func run(stdout *os.File, logRuntime *pkglogger.Runtime, loggerGate *sidecarFileLoggerGate) error {
	if loggerGate == nil {
		return errors.New("mcp-lsp file logger gate is required")
	}
	// MCP stdio 协议把 stdout 当作 JSON-RPC 通道；日志必须固定写 stderr。
	// 如果这里回到 stdout，客户端会把普通日志当作协议帧解析而失败。

	app := fx.New(
		fx.NopLogger,
		fx.Supply(stdout, logRuntime, loggerGate),
		fx.Provide(
			func(shutdowner fx.Shutdowner, handlers ToolHandlers, runtimeManager *Manager, metrics *platformmetrics.BootstrapMetrics, loggerGate *sidecarFileLoggerGate) bootstrap.Config {
				cfg := bootstrap.ReadBootConfig()
				cfg.AgentID = ""
				cfg.Metrics = metrics
				cfg.Capabilities = []string{"tools/lsp"}
				tp := registryToolProvider{defs: toolDefinitions(handlers), ensureReady: loggerGate.Ensure}
				cfg.OnToolsList = func(ctx context.Context) (any, error) {
					tools, err := tp.ListTools(ctx)
					if err != nil {
						return nil, err
					}
					return map[string]any{"tools": tools}, nil
				}
				cfg.OnToolsCall = func(ctx context.Context, params json.RawMessage) (any, error) {
					return handleScopedToolsCall(ctx, tp, mcp.ClientKindLSP, params)
				}
				cfg.FinalReport = func() *mcp.ReportRequest {
					return &mcp.ReportRequest{Report: mcp.ReportEnvelope{Type: mcp.ReportVariantCompletion, Completion: &mcp.CompletionReport{Status: "done", Report: "mcp-lsp shutdown"}}}
				}
				cfg.OnConfigChanged = func(notify mcp.ConfigChangedNotify) {
					fields := []any{"binary_name", cfg.BinaryName, "instance_id", cfg.InstanceID, "scope", notify.Scope, "config_version", notify.ConfigVersion, "selector", notify.Selector}
					fields = append(fields, platformshared.SafePayloadLogFields("payload", notify.Payload)...)
					pkglogger.Info("mcp-lsp config changed", fields...)
				}
				cfg.OnLSPReleaseScope = func(ctx context.Context, req mcp.LSPReleaseScopeRequest) (mcp.LSPReleaseScopeResult, error) {
					if runtimeManager == nil {
						return mcp.LSPReleaseScopeResult{}, nil
					}
					return runtimeManager.ReleaseScope(req)
				}
				cfg.OnShutdown = func(mcp.ShutdownRequest) {
					platformshared.LogIgnoredError(pkglogger.Get(), "mcp-lsp: OnShutdown", shutdowner.Shutdown())
				}
				return cfg
			},
			platformconfig.New,
			platformmetrics.NewBootstrapMetrics,
			bootstrap.New,
			newManager,
			newToolHandlers,
			newServer,
			fx.Annotate(newBootstrapRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newStdioRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newHTTPRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newOrphanWatchdogRunner, fx.ResultTags(`group:"runners"`)),
			// 每种语言 ManagerPool 的后台 recycler 由根运行组托管，构造函数只负责建模。
			// flatten 会把 runner 切片拆成独立成员，确保 fx 生命周期统一启动和停止。
			fx.Annotate(provideLSPBackgroundRunners, fx.ResultTags(`group:"runners,flatten"`)),
		),
		fx.Invoke(bindRuntime),
	)
	if err := app.Err(); err != nil {
		return err
	}
	startCtx, startCancel := platformconfig.WithRPCRequestTimeout(context.Background())
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		return err
	}
	<-app.Wait()
	stopCtx, stopCancel := platformconfig.WithRPCRequestTimeout(context.Background())
	defer stopCancel()
	return app.Stop(stopCtx)
}

// newServer 创建 stdio 传输层的 MCP server，使用受保护的 stdout 作为写端。
func newServer(stdout *os.File, handlers ToolHandlers, logRuntime *pkglogger.Runtime, loggerGate *sidecarFileLoggerGate) (*common.Server, error) {
	if stdout == nil {
		return nil, errors.New("mcp-lsp: stdout is nil; program assembly order is broken")
	}
	if loggerGate == nil {
		return nil, errors.New("mcp-lsp file logger gate is required")
	}
	transport := common.NewStdioTransport(os.Stdin, stdout)
	// ToolErrorClassifier 是共享传输契约：它只识别显式 typed Windows 错误，
	// 非 Windows 原生 errno/PathError 保持普通工具错误，不能改变其他平台的授权语义。
	return common.NewServer(binaryName, binaryVersion, transport, registryToolProvider{
		defs:        toolDefinitions(handlers),
		ensureReady: loggerGate.Ensure,
	}, common.WithLoggerRuntime(logRuntime), common.WithToolCallResultPolicy(lspToolCallResultPolicy()), common.WithToolErrorClassifier(tools.ToolErrorClassifier)), nil
}

// lspToolCallResultPolicy 返回 mcp-lsp 专属的严格纯文本结果策略。
func lspToolCallResultPolicy() common.ToolCallResultPolicy {
	return common.NewTextOnlyToolCallResultPolicy(tools.FormatToPlainText)
}

// newBootstrapRunner 构建 bootstrapRunner，等待 stdio server ready 信号后连接控制面。
func newBootstrapRunner(cfg bootstrap.Config, client *bootstrap.Client, logRuntime *pkglogger.Runtime, server *common.Server) (platformrunner.Runner, error) {
	if logRuntime == nil {
		return nil, errors.New("mcp-lsp logger runtime is required")
	}
	return bootstrapRunner{cfg: cfg, client: client, logRuntime: logRuntime, stdioReady: server.Ready()}, nil
}

// provideLSPBackgroundRunners 将语言 manager 的后台 runner 挂到根运行组。
// 后台 recycler 必须受 fx 生命周期管理，避免构造阶段隐式启动 goroutine。
func provideLSPBackgroundRunners(m *Manager) []platformrunner.Runner {
	return m.BackgroundRunners()
}

// ListTools 返回当前 peer 暴露的工具列表，语义 LSP server 不可用时过滤掉对应工具。
func (p registryToolProvider) ListTools(ctx context.Context) ([]mcp.MCPTool, error) {
	semanticAvailable, err := p.semanticLSPAvailable(ctx)
	if err != nil {
		return nil, err
	}
	toolsList := make([]mcp.MCPTool, 0, len(p.defs))
	for _, def := range p.defs {
		if isSemanticLSPToolName(def.Manifest.Name) && !semanticAvailable {
			continue
		}
		schema, err := marshalInputSchema(def.Manifest.Schema)
		if err != nil {
			return nil, err
		}
		tool := mcp.MCPTool{
			Name:        def.Manifest.Name,
			Description: def.Manifest.Description,
			InputSchema: schema,
		}
		if len(def.Manifest.OutputSchema) > 0 {
			outSchema, err := json.Marshal(def.Manifest.OutputSchema)
			if err != nil {
				return nil, err
			}
			tool.OutputSchema = outSchema
		}
		toolsList = append(toolsList, tool)
	}
	return toolsList, nil
}

// semanticLSPAvailable 检查当前环境是否有可用的语义 LSP server。
func (p registryToolProvider) semanticLSPAvailable(ctx context.Context) (bool, error) {
	if p.semanticToolsAvailable != nil {
		return p.semanticToolsAvailable(ctx), nil
	}
	return runtimeSemanticLSPToolsAvailable(ctx)
}

// isSemanticLSPToolName 判断工具名是否属于需要语义 LSP server 才能运行的工具。
func isSemanticLSPToolName(name string) bool {
	switch canonicalToolName(name) {
	case "inspect", "xref", "structure", "patch_edit", "completion":
		return true
	default:
		return false
	}
}

// runtimeSemanticLSPToolsAvailable 按共享优先级检查语义 LSP 能力：先读取显式 bundle，
// 再检查 PATH，最后交给带 build tag 的平台实现判断生产自动安装器是否可用。
// 平台实现只能补充本平台的可用性事实，不能改变其他平台的 bundle/PATH 语义。
func runtimeSemanticLSPToolsAvailable(ctx context.Context) (bool, error) {
	lspBundle, packaged, err := runtimeenv.LoadLSPBundleFromEnv()
	if packaged {
		if err != nil {
			return false, err
		}
		return len(lspBundle.SemanticLanguages()) > 0, nil
	}
	binaries, err := runtimeSemanticLSPServerBinaries()
	if err != nil {
		return false, err
	}
	for _, binary := range binaries {
		if _, err := exec.LookPath(binary); err == nil {
			return true, nil
		}
	}
	return runtimePlatformSemanticLSPToolsAvailable(ctx)
}

// runtimeSemanticLSPServerBinaries 返回支持的语义 LSP server 二进制名称列表。
func runtimeSemanticLSPServerBinaries() ([]string, error) {
	adapters := multilsp.NewDefaultLanguageAdapterRegistry()
	binaries := make([]string, 0, len(runtimePrimaryLanguageIDs()))
	seen := make(map[string]struct{}, len(runtimePrimaryLanguageIDs()))
	for _, languageID := range runtimePrimaryLanguageIDs() {
		adapter, ok := adapters.AdapterForLanguage(languageID)
		if !ok {
			return nil, errors.New("missing LSP language adapter: " + languageID)
		}
		if !adapter.CapabilityPolicy().RequiresLSPClient {
			continue
		}
		command, err := adapter.ServerCommand(context.Background(), multilsp.ResolvedLanguageScope{})
		if err != nil {
			return nil, err
		}
		binary := strings.TrimSpace(command.Executable)
		if binary == "" {
			return nil, errors.New("missing semantic LSP server command for language: " + languageID)
		}
		if _, ok := seen[binary]; ok {
			continue
		}
		seen[binary] = struct{}{}
		binaries = append(binaries, binary)
	}
	return binaries, nil
}

// CallTool 调用当前 peer 暴露的工具，先补全工作区作用域后分发到具体处理器。
func (p registryToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	if p.ensureReady != nil {
		if err := p.ensureReady(); err != nil {
			return nil, err
		}
	}
	var err error
	ctx, err = withRuntimeWorkspaceScopeFallback(ctx)
	if err != nil {
		return nil, err
	}
	return handleToolCall(ctx, p.defs, name, args)
}

// withRuntimeWorkspaceScopeFallback 将 sidecar 配置的运行时 roots 合并进工具作用域。
// 缺少 metadata roots 的调用仍会打 runtime fallback 标记，供 grep 等工具阻断 stale-root 搜索。
func withRuntimeWorkspaceScopeFallback(ctx context.Context) (context.Context, error) {
	scope, ok := common.ToolScopeFromContext(ctx)
	hadTrustedRoots := ok && len(scope.WorkspaceRoots) > 0
	if hadTrustedRoots {
		return ctx, nil
	}
	runtimeRoots, configured, err := runtimeWorkspaceRootsFromEnv()
	if err != nil {
		return ctx, err
	}
	if len(runtimeRoots) == 0 {
		if configured {
			return ctx, errors.New("runtime workspace roots env is explicitly configured but empty")
		}
		return ctx, errors.New("runtime workspace roots env is required")
	}
	if strings.TrimSpace(scope.CWD) == "" {
		scope.CWD = runtimeRoots[0]
	}
	scope.WorkspaceRoots = append(scope.WorkspaceRoots, runtimeRoots...)
	if strings.TrimSpace(scope.Family) == "" {
		scope.Family = mcp.ClientKindLSP
	}
	ctx = common.WithToolScope(ctx, scope)
	return common.WithRuntimeWorkspaceScopeFallback(ctx), nil
}

// shouldWarnLSPCWDTrace 判断该工具名是否需要记录工作区追踪日志。
func shouldWarnLSPCWDTrace(toolName string) bool {
	toolName = canonicalToolName(toolName)
	switch toolName {
	case "file", "inspect", "xref", "grep", "structure", "patch_edit", "completion":
		return true
	default:
		return false
	}
}

// warnLSPToolsCallScopeTrace 记录工具调用的作用域追踪日志，仅对需要追踪的工具生效。
func warnLSPToolsCallScopeTrace(toolName string, scope common.ToolScope) {
	if !shouldWarnLSPCWDTrace(toolName) {
		return
	}
	fields := []any{
		"tool", strings.TrimSpace(toolName),
		"agent_id", scope.AgentID,
		"thread_id", scope.ThreadID,
		"call_id", scope.CallID,
		"has_cwd", scope.CWD != "",
	}
	fields = append(fields, platformshared.SafePathLogFields("cwd", scope.CWD)...)
	pkglogger.Warn("mcp-lsp: tools/call scope trace", fields...)
}

// handleScopedToolsCall 解码工具调用参数，设置作用域后分发到具体工具处理器，panic 时包装为错误返回。
func handleScopedToolsCall(ctx context.Context, tp registryToolProvider, family string, params json.RawMessage) (result any, err error) {
	toolName := "tools/call"
	defer func() {
		if recovered := recover(); recovered != nil {
			result, err = wrapScopedToolResult(common.NewToolErrorEnvelopeWithClassifier(toolName, "", common.NewPanicToolError(recovered), nil, tools.ToolErrorClassifier))
		}
	}()
	req, err := common.DecodeToolCallParams(params)
	if err != nil {
		return nil, err
	}
	toolName = req.Name
	scope := req.Scope(family)
	warnLSPToolsCallScopeTrace(req.Name, scope)
	ctx = common.WithToolScope(ctx, scope)
	result, err = tp.CallTool(ctx, req.Name, req.Arguments)
	if err != nil {
		if result == nil {
			result = common.NewToolErrorEnvelopeWithClassifier(req.Name, "", err, nil, tools.ToolErrorClassifier)
		}
	}
	return wrapScopedToolResult(result)
}

// wrapScopedToolResult 使用与 direct/HTTP 相同的 common builder 生成纯文本 MCP 响应。
func wrapScopedToolResult(result any) (any, error) {
	return common.BuildToolCallResultWithPolicy(result, lspToolCallResultPolicy())
}

// marshalInputSchema 将工具输入 schema 序列化为 JSON，空 schema 返回 "{}"。
func marshalInputSchema(schema map[string]any) (json.RawMessage, error) {
	if len(schema) == 0 {
		return json.RawMessage("{}"), nil
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// compileToolDefinitions validates every manifest and compiles its input schema once during assembly.
// The resulting validator is retained on the definition so handler calls never decode a second, hand-written contract.
func compileToolDefinitions(manifests []ToolManifest, handlers ToolHandlers) ([]toolDefinition, error) {
	if len(manifests) == 0 {
		return nil, errors.New("no LSP tool manifests configured")
	}
	defs := make([]toolDefinition, 0, len(manifests))
	seenNames := make(map[string]struct{}, len(manifests))
	for _, manifest := range manifests {
		name := canonicalToolName(manifest.Name)
		if name == "" {
			return nil, errors.New("LSP tool manifest has empty name")
		}
		if _, exists := seenNames[name]; exists {
			return nil, fmt.Errorf("duplicate LSP tool manifest: %q", name)
		}
		seenNames[name] = struct{}{}
		if err := validateManifestActions(manifest); err != nil {
			return nil, err
		}
		validator, err := compileToolManifestSchema(manifest)
		if err != nil {
			return nil, err
		}
		handler := handlers[name]
		if handler == nil {
			handler = stubToolHandler
		}
		defs = append(defs, toolDefinition{Manifest: manifest, Handler: handler, validator: validator})
	}
	return defs, nil
}

// compileToolManifestSchema compiles the exact ToolManifest.Schema value used by tools/list.
func compileToolManifestSchema(manifest ToolManifest) (*jsonschema.Schema, error) {
	if len(manifest.Schema) == 0 {
		return nil, fmt.Errorf("LSP tool %q has empty input schema", manifest.Name)
	}
	raw, err := json.Marshal(manifest.Schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema for tool %q: %w", manifest.Name, err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("normalize schema for tool %q: %w", manifest.Name, err)
	}
	compiler := jsonschema.NewCompiler()
	const resource = "mcp-lsp-tool-schema.json"
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, fmt.Errorf("register schema for tool %q: %w", manifest.Name, err)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		return nil, fmt.Errorf("compile schema for tool %q: %w", manifest.Name, err)
	}
	return compiled, nil
}

// validateManifestActions dynamically checks every action enum exposed by every manifest.
// This prevents a missing or stale action declaration from reaching the runtime validator.
func validateManifestActions(manifest ToolManifest) error {
	properties, ok := manifest.Schema["properties"].(map[string]any)
	if !ok {
		if _, hasConditions := manifest.Schema["allOf"]; hasConditions {
			return fmt.Errorf("LSP tool %q has action conditions but no properties", manifest.Name)
		}
		return nil
	}
	property, exists := properties["action"]
	if !exists {
		if _, hasConditions := manifest.Schema["allOf"]; hasConditions {
			return fmt.Errorf("LSP tool %q has action conditions but no action property", manifest.Name)
		}
		return nil
	}
	actionSchema, ok := property.(map[string]any)
	if !ok {
		return fmt.Errorf("LSP tool %q action schema has invalid type %T", manifest.Name, property)
	}
	actions, err := schemaEnumStrings(actionSchema["enum"])
	if err != nil {
		return fmt.Errorf("LSP tool %q action enum: %w", manifest.Name, err)
	}
	seen := make(map[string]struct{}, len(actions))
	for _, action := range actions {
		action = strings.TrimSpace(action)
		if action == "" {
			return fmt.Errorf("LSP tool %q action enum contains an empty value", manifest.Name)
		}
		if _, exists := seen[action]; exists {
			return fmt.Errorf("LSP tool %q action enum contains duplicate %q", manifest.Name, action)
		}
		seen[action] = struct{}{}
	}
	if conditions, ok := manifest.Schema["allOf"].([]any); ok {
		conditionActions := make(map[string]struct{}, len(conditions))
		for _, rawCondition := range conditions {
			condition, ok := rawCondition.(map[string]any)
			if !ok {
				return fmt.Errorf("LSP tool %q action condition has invalid type %T", manifest.Name, rawCondition)
			}
			ifBlock, ok := condition["if"].(map[string]any)
			if !ok {
				return fmt.Errorf("LSP tool %q action condition is missing if block", manifest.Name)
			}
			conditionProperties, ok := ifBlock["properties"].(map[string]any)
			if !ok {
				return fmt.Errorf("LSP tool %q action condition has invalid properties", manifest.Name)
			}
			conditionAction, ok := conditionProperties["action"].(map[string]any)
			if !ok {
				return fmt.Errorf("LSP tool %q action condition is missing action const", manifest.Name)
			}
			name, ok := conditionAction["const"].(string)
			if !ok || strings.TrimSpace(name) == "" {
				return fmt.Errorf("LSP tool %q action condition has invalid const %v", manifest.Name, conditionAction["const"])
			}
			if _, exists := seen[name]; !exists {
				return fmt.Errorf("LSP tool %q action condition %q is missing from action enum", manifest.Name, name)
			}
			if _, duplicate := conditionActions[name]; duplicate {
				return fmt.Errorf("LSP tool %q action conditions contain duplicate %q", manifest.Name, name)
			}
			conditionActions[name] = struct{}{}
		}
		for action := range seen {
			if _, exists := conditionActions[action]; !exists {
				return fmt.Errorf("LSP tool %q action enum %q has no action condition", manifest.Name, action)
			}
		}
	}
	return nil
}

func schemaEnumStrings(value any) ([]string, error) {
	switch values := value.(type) {
	case []string:
		if len(values) == 0 {
			return nil, errors.New("enum is empty")
		}
		return append([]string(nil), values...), nil
	case []any:
		if len(values) == 0 {
			return nil, errors.New("enum is empty")
		}
		result := make([]string, 0, len(values))
		for _, value := range values {
			action, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("enum value has type %T, want string", value)
			}
			result = append(result, action)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("enum has type %T, want []string", value)
	}
}

func validateToolCallArguments(def toolDefinition, args json.RawMessage) error {
	validator := def.validator
	if validator == nil {
		// Hand-built definitions remain useful to low-level transport tests. Production definitions
		// always carry the validator compiled by compileToolDefinitions above.
		if len(def.Manifest.Schema) == 0 {
			return nil
		}
		compiled, err := compileToolManifestSchema(def.Manifest)
		if err != nil {
			return common.NewCodedToolError("invalid_params", err, false, "The tool manifest schema is invalid; inspect the sidecar assembly.")
		}
		validator = compiled
	}
	var value any
	if err := json.Unmarshal(args, &value); err != nil {
		return common.NewCodedToolError("invalid_params", fmt.Errorf("arguments are not valid JSON: %w", err), false, "Pass a JSON object matching the tool input schema.")
	}
	if unknown := firstUnknownManifestField(def.Manifest.Schema, value); unknown != "" {
		return common.NewCodedToolError("invalid_params", fmt.Errorf("unknown field %q", unknown), false, "Remove fields not declared by the tool input schema.")
	}
	if err := validator.Validate(value); err != nil {
		return common.NewCodedToolError("invalid_params", fmt.Errorf("tool %q arguments do not match its input schema: %w", def.Manifest.Name, err), false, "Fix the arguments to match the advertised tool schema.")
	}
	return nil
}

func firstUnknownManifestField(raw schema, value any) string {
	arguments, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	properties, ok := raw["properties"].(map[string]any)
	if !ok {
		return ""
	}
	unknown := make([]string, 0)
	for field := range arguments {
		if _, declared := properties[field]; !declared {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	sort.Strings(unknown)
	return unknown[0]
}

// handleToolCall 按工具名在定义列表中查找处理器并执行，未找到时返回错误。
func handleToolCall(ctx context.Context, defs []toolDefinition, name string, args json.RawMessage) (any, error) {
	trimmed := canonicalToolName(name)
	for _, def := range defs {
		if canonicalToolName(def.Manifest.Name) != trimmed {
			continue
		}
		if def.Handler == nil {
			return nil, errors.New("tool handler is not configured")
		}
		if err := validateToolCallArguments(def, args); err != nil {
			return nil, err
		}
		return def.Handler(ctx, args)
	}
	return nil, errors.New("unknown tool: " + strings.TrimSpace(name))
}

// Run 启动 LSP bootstrap 流程，等待 stdio server 就绪后按模式决定是否连接控制面 RPC。
func (r bootstrapRunner) Run(ctx context.Context) error {
	r.client.InstallLogRelay(r.logRuntime)
	// 双通道启动顺序：先等本地 stdio MCP server 就绪，再连接控制面 jrpc2。
	// 这样控制面开始派发请求时，工具执行通道已经存在。
	if r.stdioReady != nil {
		select {
		case <-r.stdioReady:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if strings.TrimSpace(r.cfg.RPCAddr) == "" {
		pkglogger.Warn("mcp-lsp bootstrap disabled: GO_AGENT_CTL_RPC_ADDR missing",
			"binary_name", r.cfg.BinaryName,
			"client_kind", r.cfg.ClientKind,
			"thread_id", r.cfg.ThreadID,
			"capabilities", r.cfg.Capabilities,
		)
		<-ctx.Done()
		return nil
	}
	// 只有独立 peer 模式才注册控制面；provider 拉起的 sidecar 避免和宿主 sweeper 抢占生命周期。
	if os.Getenv("GO_AGENT_PEER_MODE") != "1" {
		pkglogger.Info("mcp-lsp bootstrap skipped (sidecar mode)",
			"rpc_addr", r.cfg.RPCAddr,
			"binary_name", r.cfg.BinaryName,
		)
		<-ctx.Done()
		return nil
	}
	pkglogger.Info("mcp-lsp bootstrap starting (peer mode)",
		"binary_name", r.cfg.BinaryName,
		"rpc_addr", r.cfg.RPCAddr,
		"capabilities", r.cfg.Capabilities,
	)
	if err := r.client.Start(ctx); err != nil {
		pkglogger.Error("mcp-lsp bootstrap start failed",
			"binary_name", r.cfg.BinaryName,
			"rpc_addr", r.cfg.RPCAddr,
			"error", err,
		)
		return err
	}
	<-ctx.Done()
	return r.client.Close()
}

// bindRuntime 将运行组生命周期绑定到 fx，OnStart 启动 goroutine 运行所有 runner，
// OnStop 取消 ctx 并等待运行组退出，超时时返回 ctx 错误。
func bindRuntime(lc fx.Lifecycle, params runtimeParams) {
	log := pkglogger.Get()
	var (
		cancel       context.CancelFunc
		shutdownOnce sync.Once
	)
	done := make(chan error, 1)
	requestShutdown := func() {
		shutdownOnce.Do(func() {
			platformshared.LogIgnoredError(log, "mcp-lsp shutdown error", params.Shutdowner.Shutdown())
		})
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			// sidecar 是独立子进程，不能继承主应用 RootCtxProvider。
			// 父进程关闭、控制面 OnShutdown 或 RunGroup 自退出最终都会进入 OnStop，统一 cancel 并等待 done。
			runCtx, runCancel := context.WithCancel(context.Background())
			cancel = runCancel
			runtimesafe.SafeGo(runCtx, log, "mcp-lsp.runtime.runGroup", func(context.Context) {
				err := platformrunner.RunGroup(runCtx, params.Runners, platformrunner.GroupOptions{
					EnableSignals: false,
				})
				done <- err
				close(done)
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Error("mcp-lsp runtime exited", "error", err)
				}
				requestShutdown()
			})

			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}
			select {
			case err := <-done:
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
