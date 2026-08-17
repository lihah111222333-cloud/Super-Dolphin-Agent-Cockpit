package multilsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

const (
	methodExit                 = "exit"
	gracefulProcessExitTimeout = 500 * time.Millisecond
)

var (
	ErrClientClosed       = errors.New("LSP client closed")
	ErrMethodNotSupported = errors.New("LSP server request not supported")
	ErrTransportClosed    = errors.New("LSP transport closed")
	ErrBinaryRequired     = errors.New("LSP binary is required")
	// ErrIdleReleaseOwnerUnavailable 表示 idle recycler 无法取得共享 daemon
	// forwarder 的唯一 ReleaseForIdle owner；调用方必须保留 CleanupPending。
	ErrIdleReleaseOwnerUnavailable = errors.New("LSP idle release owner unavailable")
)

// Client 定义 multilsp manager 与底层 LSP transport 交互的最小生命周期接口。
// 实现必须显式处理 initialize、文档事件、请求通知和资源关闭。
type Client interface {
	Initialize(context.Context, string) error
	Shutdown(context.Context) error
	Request(context.Context, string, any) (json.RawMessage, error)
	Notify(context.Context, string, any) error
	DidOpen(context.Context, string, string, int, string) error
	DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error
	DidClose(context.Context, string) error
	Close() error
}

// ServerCapabilitiesClient 暴露 initialize 返回的服务端能力，供可选能力路径按需探测。
type ServerCapabilitiesClient interface {
	ServerCapabilities() protocol.ServerCapabilities
}

// ServerProfileJDTLS160 是 Windows product-owned JDK/JDTLS 1.60.0 的精确 profile。
// 只有 runtime resolver 明确传入该值时才会关闭已知非法 typeDefinition 响应。
const ServerProfileJDTLS160 = "jdk-jdtls@1.60.0"

// HealthCheckedClient 在 Client 基础上暴露 pool 复用前的健康状态。
type HealthCheckedClient interface {
	Client
	Healthy() bool
}

// WrappedClient 允许装饰器暴露真实 transport owner，供进程树、RSS 与跨 worktree 总账统一管理。
type WrappedClient interface {
	Client
	UnderlyingLSPClient() Client
}

// IdleReleasableClient 把 idle recycler 的关闭绑定到共享资源 owner。
// gopls root cohort client 必须实现 ReleaseForIdle；recycler 不得在该路径
// 退回裸 Client.Close，从而绕过 root lease/fence 与 forwarder drain。
type IdleReleasableClient interface {
	Client
	ReleaseForIdle() error
}

// IdleReleaseRequiredClient 标记一个 client 必须走 IdleReleasableClient。
// 标记存在但未实现 ReleaseForIdle 时，recycler fail-closed 并保留 cleanup owner。
type IdleReleaseRequiredClient interface {
	Client
	RequiresIdleRelease() bool
}

// Options 描述启动 LSP client 时传给 transport、initialize 和回调处理器的配置。
type Options struct {
	Binary                    string
	Args                      []string
	Dir                       string
	Env                       []string
	ProcessID                 int
	InitOptions               map[string]any
	NotificationHandler       protocol.NotificationHandler
	RequestHandler            ServerRequestHandler
	ServerNotificationHandler ServerNotificationHandler
	// ServerProfile 是生产 resolver 传入的精确服务端身份；空值表示未知，不能据此禁用能力。
	ServerProfile string
}

type client struct {
	transport            *transport
	processID            int
	initOptions          map[string]any
	workspaceFolders     []protocol.WorkspaceFolder
	dynamicRegistrations *dynamicRegistrationTracker

	lifecycleMu                 sync.Mutex
	resourceCleanupMu           sync.Mutex
	stateMu                     sync.RWMutex
	rootURI                     string
	initialized                 bool
	shutdown                    bool
	resourceReportsReleased     bool
	resourceCohortLeaseReleased bool
	capabilities                protocol.ServerCapabilities
	semanticTokensLegendBroken  bool
	serverName                  string
	serverVersion               string
	initializeResponseKnown     bool
	knownJDTLSTypeDefinitionOff bool
	serverProfile               string
}

// concreteClient 穿透有限层包装器，定位拥有 transport 与进程资源的真实 client。
func concreteClient(current Client) (*client, bool) {
	for range 16 {
		if typed, ok := current.(*client); ok {
			return typed, true
		}
		wrapped, ok := current.(WrappedClient)
		if !ok {
			return nil, false
		}
		next := wrapped.UnderlyingLSPClient()
		if next == nil || next == current {
			return nil, false
		}
		current = next
	}
	return nil, false
}

type dynamicRegistrationTracker struct {
	mu            sync.RWMutex
	registrations map[string]map[string]dynamicRegistration
}

// invalidSemanticTokensProvider 是初始化阶段识别出的协议畸形标记；它只存在于
// 能力合并内部，向调用方暴露前会归一化为 nil，避免动态 full 注册重新打开坏能力。
type invalidSemanticTokensProvider struct{}

type dynamicRegistration struct {
	options json.RawMessage
}

func newDynamicRegistrationTracker() *dynamicRegistrationTracker {
	return &dynamicRegistrationTracker{
		registrations: map[string]map[string]dynamicRegistration{},
	}
}

// dynamicRegistrationRequestHandler 记录服务端动态注册能力，并把未处理请求交给调用方配置处理器。
func dynamicRegistrationRequestHandler(tracker *dynamicRegistrationTracker, next ServerRequestHandler) ServerRequestHandler {
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if tracker != nil {
			handled, err := tracker.handleServerRequest(method, params)
			if err != nil {
				return nil, err
			}
			if handled {
				return struct{}{}, nil
			}
		}
		if next != nil {
			return next(ctx, method, params)
		}
		return nil, ErrMethodNotSupported
	}
}

func (t *dynamicRegistrationTracker) handleServerRequest(method string, params json.RawMessage) (bool, error) {
	switch method {
	case LSPCompatMethodClientRegisterCapability:
		return true, t.register(params)
	case LSPCompatMethodClientUnregisterCapability:
		return true, t.unregister(params)
	default:
		return false, nil
	}
}

func (t *dynamicRegistrationTracker) register(params json.RawMessage) error {
	var request struct {
		Registrations []struct {
			ID              string          `json:"id"`
			Method          string          `json:"method"`
			RegisterOptions json.RawMessage `json:"registerOptions"`
		} `json:"registrations"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return err
	}
	for _, registration := range request.Registrations {
		if strings.TrimSpace(registration.ID) == "" {
			return fmt.Errorf("dynamic registration id is required for %s", strings.TrimSpace(registration.Method))
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, registration := range request.Registrations {
		registrations, ok := t.registrationsForMethod(registration.Method)
		if !ok {
			continue
		}
		registrations[strings.TrimSpace(registration.ID)] = dynamicRegistration{
			options: append(json.RawMessage(nil), registration.RegisterOptions...),
		}
	}
	return nil
}

func (t *dynamicRegistrationTracker) unregister(params json.RawMessage) error {
	var request struct {
		Unregisterations []struct {
			ID     string `json:"id"`
			Method string `json:"method"`
		} `json:"unregisterations"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return err
	}
	for _, registration := range request.Unregisterations {
		if strings.TrimSpace(registration.ID) == "" {
			return fmt.Errorf("dynamic unregistration id is required for %s", strings.TrimSpace(registration.Method))
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, registration := range request.Unregisterations {
		registrations, ok := t.registrationsForMethod(registration.Method)
		if !ok {
			continue
		}
		delete(registrations, strings.TrimSpace(registration.ID))
	}
	return nil
}

// registrationsForMethod 返回需要合并进静态能力快照的动态注册集合。
// 这是跨平台共享的 LSP 协议账本：只按方法名记录能力，不读取宿主系统或进程架构。
func (t *dynamicRegistrationTracker) registrationsForMethod(method string) (map[string]dynamicRegistration, bool) {
	method = strings.TrimSpace(method)
	if !tracksDynamicRegistrationMethod(method) {
		return nil, false
	}
	registrations := t.registrations[method]
	if registrations == nil {
		registrations = map[string]dynamicRegistration{}
		t.registrations[method] = registrations
	}
	return registrations, true
}

// tracksDynamicRegistrationMethod 限定 sidecar 已声明并能路由的动态注册方法。
// 公共协议方法在所有平台使用同一集合，平台专用进程启动逻辑不得进入这里。
func tracksDynamicRegistrationMethod(method string) bool {
	switch method {
	case protocol.MethodTextDocumentDiagnostic,
		protocol.MethodCompletion,
		protocol.MethodDocumentSymbol,
		protocol.MethodDefinition,
		protocol.MethodImplementation,
		protocol.MethodTypeDefinition,
		protocol.MethodReferences,
		protocol.MethodPrepareCallHierarchy,
		protocol.MethodPrepareTypeHierarchy,
		protocol.MethodCodeAction,
		protocol.MethodSignatureHelp,
		protocol.MethodFormatting,
		protocol.MethodFoldingRange,
		protocol.MethodSemanticTokens:
		return true
	default:
		return false
	}
}

// serverCapabilities 把动态注册账本合并到 initialize 静态能力快照。
// 合并规则属于公共 LSP 语义；Windows、Darwin、Linux 等平台必须得到完全相同的能力裁决。
func (t *dynamicRegistrationTracker) serverCapabilities(capabilities protocol.ServerCapabilities) protocol.ServerCapabilities {
	if t == nil {
		return capabilities
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	semanticTokensLegendBroken := false
	if _, broken := capabilities.SemanticTokensProvider.(invalidSemanticTokensProvider); broken {
		semanticTokensLegendBroken = true
		capabilities.SemanticTokensProvider = nil
	}
	if len(t.registrations[protocol.MethodTextDocumentDiagnostic]) > 0 {
		capabilities.DiagnosticProvider = true
	}
	if len(t.registrations[protocol.MethodCompletion]) > 0 {
		capabilities.CompletionProvider = true
	}
	if len(t.registrations[protocol.MethodDocumentSymbol]) > 0 {
		capabilities.DocumentSymbolProvider = true
	}
	if len(t.registrations[protocol.MethodDefinition]) > 0 {
		capabilities.DefinitionProvider = true
	}
	if len(t.registrations[protocol.MethodImplementation]) > 0 {
		capabilities.ImplementationProvider = true
	}
	if len(t.registrations[protocol.MethodTypeDefinition]) > 0 {
		capabilities.TypeDefinitionProvider = true
	}
	if len(t.registrations[protocol.MethodReferences]) > 0 {
		capabilities.ReferencesProvider = true
	}
	if len(t.registrations[protocol.MethodPrepareCallHierarchy]) > 0 {
		capabilities.CallHierarchyProvider = true
	}
	if len(t.registrations[protocol.MethodPrepareTypeHierarchy]) > 0 {
		capabilities.TypeHierarchyProvider = true
	}
	if len(t.registrations[protocol.MethodCodeAction]) > 0 {
		capabilities.CodeActionProvider = true
	}
	if len(t.registrations[protocol.MethodSignatureHelp]) > 0 {
		capabilities.SignatureHelpProvider = true
	}
	if len(t.registrations[protocol.MethodFormatting]) > 0 {
		capabilities.DocumentFormattingProvider = true
	}
	if len(t.registrations[protocol.MethodFoldingRange]) > 0 {
		capabilities.FoldingRangeProvider = true
	}
	semanticRegistrations := t.registrations[protocol.MethodSemanticTokens]
	if len(semanticRegistrations) > 0 &&
		!semanticTokensLegendBroken &&
		dynamicSemanticTokensFullCapabilityAvailable(semanticRegistrations) &&
		!semanticTokensFullCapabilityAvailable(capabilities.SemanticTokensProvider) {
		capabilities.SemanticTokensProvider = semanticTokensProviderWithFull(capabilities.SemanticTokensProvider)
	}
	return capabilities
}

func dynamicSemanticTokensFullCapabilityAvailable(registrations map[string]dynamicRegistration) bool {
	for _, registration := range registrations {
		if semanticTokensFullCapabilityAvailable(registration.options) {
			return true
		}
	}
	return false
}

// semanticTokensProviderWithFull 合并动态 full 子能力，同时保留静态 provider 的
// range、legend 等字段；这是所有平台共享的协议合并，不能因 Windows 安装路径而改写。
func semanticTokensProviderWithFull(provider any) any {
	switch typed := provider.(type) {
	case map[string]any:
		merged := make(map[string]any, len(typed)+1)
		for key, value := range typed {
			merged[key] = value
		}
		merged["full"] = true
		return merged
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(typed, &decoded); err == nil {
			return semanticTokensProviderWithFull(decoded)
		}
	}
	return map[string]any{"full": true}
}

type limitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
	total int64
}

type responseError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

// NewClient 用默认参数启动一个 LSP client。
// 只暴露二进制和通知处理器的轻量入口，复杂调用方应使用 NewClientWithOptions。
func NewClient(binary string, handler protocol.NotificationHandler) (Client, error) {
	return NewClientWithOptions(Options{
		Binary:              binary,
		NotificationHandler: handler,
	})
}

// NewClientWithOptions 根据 Options 启动 LSP transport 并封装生命周期状态。
// binary 为空会立即报错，启动参数、环境和 request handler 都在这里完成复制或默认化。
func NewClientWithOptions(options Options) (Client, error) {
	binary := strings.TrimSpace(options.Binary)
	if binary == "" {
		return nil, ErrBinaryRequired
	}
	requestHandler := options.RequestHandler
	if requestHandler == nil {
		requestHandler = configurationRequestHandlerFromInitOptions(options.InitOptions)
	}
	dynamicRegistrations := newDynamicRegistrationTracker()
	transport, err := newTransport(transportOptions{
		Binary:                    binary,
		Args:                      defaultArgs(options.Args),
		Dir:                       options.Dir,
		Env:                       append([]string(nil), options.Env...),
		NotificationHandler:       options.NotificationHandler,
		RequestHandler:            dynamicRegistrationRequestHandler(dynamicRegistrations, requestHandler),
		ServerNotificationHandler: options.ServerNotificationHandler,
	})
	if err != nil {
		return nil, err
	}
	return &client{
		transport:            transport,
		processID:            normalizeProcessID(options.ProcessID),
		initOptions:          options.InitOptions,
		dynamicRegistrations: dynamicRegistrations,
		serverProfile:        strings.TrimSpace(options.ServerProfile),
	}, nil
}

// Initialize 发送 initialize/initialized 握手并记录 rootURI。
// 同一个 client 只能初始化一次；失败会标记 transport 健康状态供 pool 淘汰。
func (c *client) Initialize(ctx context.Context, rootURI string) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if err := c.canInitialize(rootURI); err != nil {
		return err
	}
	initOpts := c.initOptions
	if initOpts == nil {
		initOpts = map[string]any{"semanticTokens": true}
	}
	params := protocol.InitializeParams{
		ProcessID:             c.processID,
		RootURI:               rootURI,
		Capabilities:          clientCapabilities(),
		WorkspaceFolders:      c.workspaceFoldersForInitialize(rootURI),
		InitializationOptions: initOpts,
	}
	result, err := c.transport.request(ctx, protocol.MethodInitialize, params)
	if err != nil {
		c.markDeadIfClientFailure(err)
		return fmt.Errorf("LSP initialize request: %w", err)
	}
	if err := c.transport.notify(ctx, protocol.MethodInitialized, struct{}{}); err != nil {
		c.markDeadIfClientFailure(err)
		return fmt.Errorf("LSP initialized notification: %w", err)
	}
	capabilities, serverInfo, err := decodeInitializeResultAttribution(result)
	if err != nil {
		return err
	}
	semanticTokensLegendBroken := false
	if _, broken := capabilities.SemanticTokensProvider.(invalidSemanticTokensProvider); broken {
		semanticTokensLegendBroken = true
		capabilities.SemanticTokensProvider = nil
	}
	capabilities = normalizeServerProfileCapabilities(capabilities, c.serverProfile)
	c.stateMu.Lock()
	c.rootURI = rootURI
	c.initialized = true
	c.capabilities = capabilities
	c.semanticTokensLegendBroken = semanticTokensLegendBroken
	c.serverName = serverInfo.name
	c.serverVersion = serverInfo.version
	c.knownJDTLSTypeDefinitionOff = c.serverProfile == ServerProfileJDTLS160
	c.initializeResponseKnown = true
	c.stateMu.Unlock()
	return nil
}

// Shutdown 按 LSP 协议发送 shutdown 和 exit。
// 进程树 preparation 失败时只尝试非破坏性的 shutdown request，不发送 exit，保留 exact owner 供 Close 或重试收敛。
func (c *client) Shutdown(ctx context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.isShutdown() {
		return nil
	}
	var shutdownErrors []error
	var preparationErr error
	if c.transport != nil {
		if err := c.transport.prepareProcessTreeShutdown(); err != nil {
			preparationErr = fmt.Errorf("prepare LSP process-tree shutdown: %w", err)
			shutdownErrors = append(shutdownErrors, preparationErr)
		}
	}
	if !c.isInitialized() {
		c.markShutdown()
		return errors.Join(shutdownErrors...)
	}
	if c.transport == nil {
		c.markShutdown()
		return errors.Join(append(shutdownErrors, ErrTransportClosed)...)
	}
	if _, err := c.transport.request(ctx, "shutdown", nil); err != nil {
		if errors.Is(err, ErrTransportClosed) {
			c.transport.logShutdownStage("protocol_shutdown", "skipped", err)
		} else {
			wrapped := fmt.Errorf("LSP shutdown request: %w", err)
			shutdownErrors = append(shutdownErrors, wrapped)
			c.transport.logShutdownStage("protocol_shutdown", "failed", err)
		}
	} else {
		c.transport.logShutdownStage("protocol_shutdown", "completed", nil)
	}
	return c.finishProtocolExit(ctx, preparationErr, shutdownErrors)
}

// finishProtocolExit 仅在 exact owner preparation 成功后发送 exit，并完成本地关闭状态。
func (c *client) finishProtocolExit(ctx context.Context, preparationErr error, shutdownErrors []error) error {
	if preparationErr != nil {
		c.transport.logShutdownStage("protocol_exit", "skipped", preparationErr)
		return errors.Join(shutdownErrors...)
	}
	if err := c.transport.notify(ctx, methodExit, nil); err != nil {
		if errors.Is(err, ErrTransportClosed) {
			c.transport.logShutdownStage("protocol_exit", "skipped", err)
		} else {
			wrapped := fmt.Errorf("LSP exit notification: %w", err)
			shutdownErrors = append(shutdownErrors, wrapped)
			c.transport.logShutdownStage("protocol_exit", "failed", err)
		}
	} else {
		c.transport.logShutdownStage("protocol_exit", "completed", nil)
	}
	// 给整棵服务进程树一个固定的正常退出与缓存落盘窗口；之后由 Close 强制回收残留后代。
	waitForGracefulProcessTreeExit(ctx, gracefulProcessExitTimeout)
	c.markShutdown()
	return errors.Join(shutdownErrors...)
}

func waitForGracefulProcessTreeExit(ctx context.Context, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// Request 在 client 仍开放时发送 LSP request 并等待响应。
// transport 级失败会标记 client 不健康，避免后续请求继续复用坏连接。
func (c *client) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := c.ensureOpen(); err != nil {
		return nil, err
	}
	result, err := requestMessage(ctx, method, params, c.transport.request)
	if err != nil {
		c.markDeadIfClientFailure(err)
		return nil, fmt.Errorf("LSP request %s: %w", method, err)
	}
	return result, nil
}

// Notify 在 client 仍开放时发送 LSP notification。
// 通知失败同样会标记 client 不健康，因为底层通道已经不可再信任。
func (c *client) Notify(ctx context.Context, method string, params any) error {
	if err := c.ensureOpen(); err != nil {
		return err
	}
	if err := notifyMessage(ctx, method, params, c.transport.notify); err != nil {
		c.markDeadIfClientFailure(err)
		return fmt.Errorf("LSP notify %s: %w", method, err)
	}
	return nil
}

// DidOpen 把文档打开事件转成 LSP textDocument/didOpen。
// version 和完整文本由调用方提供，client 不在这里维护文档副本。
func (c *client) DidOpen(ctx context.Context, uri, languageID string, version int, text string) error {
	params := protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri,
			LanguageID: languageID,
			Version:    version,
			Text:       text,
		},
	}
	return c.notifyTextDocument(ctx, protocol.MethodDidOpen, params)
}

// DidChange 把调用方准备好的增量或全量变更发送给 LSP。
// changes 会复制一份，避免 transport 异步编码时读到外部修改。
func (c *client) DidChange(ctx context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	params := protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			URI:     uri,
			Version: version,
		},
		ContentChanges: append([]protocol.TextDocumentContentChangeEvent(nil), changes...),
	}
	return c.notifyTextDocument(ctx, protocol.MethodDidChange, params)
}

// DidClose 通知 LSP 释放指定文档状态。
// 关闭不存在的文档由 server 自行处理，client 只保证消息格式正确。
func (c *client) DidClose(ctx context.Context, uri string) error {
	params := protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	}
	return c.notifyTextDocument(ctx, protocol.MethodDidClose, params)
}

// Close 标记 client 已关闭并释放底层 transport。
// 这是本地资源收尾入口，不会再发送 LSP shutdown 请求。
func (c *client) Close() error {
	c.markShutdown()
	return errors.Join(
		c.transport.Close(),
		removeOwnedResourceCohortMember(c),
	)
}

// 平台策略通过外层 hook 选择是否调用，普通 client 不改变既有重建路径。

// Healthy 报告 client 是否还能被 pool 复用。
// nil、已 shutdown 或 transport 已关闭都会返回 false。
func (c *client) Healthy() bool {
	if c == nil {
		return false
	}
	c.stateMu.RLock()
	shutdown := c.shutdown
	c.stateMu.RUnlock()
	if shutdown {
		return false
	}
	return c.transport != nil && !c.transport.closed.Load()
}

// ServerCapabilities 返回 initialize 阶段记录的服务端能力。
func (c *client) ServerCapabilities() protocol.ServerCapabilities {
	c.stateMu.RLock()
	capabilities := c.capabilities
	semanticTokensLegendBroken := c.semanticTokensLegendBroken
	knownJDTLSTypeDefinitionOff := c.knownJDTLSTypeDefinitionOff
	c.stateMu.RUnlock()
	// semantic tokens 的坏 legend 只禁用该能力；其他动态 registration 仍须合并。
	// 这是跨平台 LSP 能力裁决，不因某个 server 的单项协议畸形而吞掉 completion 等能力。
	if semanticTokensLegendBroken {
		capabilities.SemanticTokensProvider = nil
	}
	capabilities = c.dynamicRegistrations.serverCapabilities(capabilities)
	if semanticTokensLegendBroken {
		// tracker 可能合并动态 semantic registration，但坏 legend 不能被任何动态声明绕过。
		capabilities.SemanticTokensProvider = nil
	}
	if knownJDTLSTypeDefinitionOff {
		// 已确认的 JDTLS 1.60.0 即使错误地动态注册该方法，也不能重新打开非法响应能力。
		capabilities.TypeDefinitionProvider = nil
	}
	return capabilities
}

type initializeServerInfo struct {
	name    string
	version string
}

type initializeResultAttribution struct {
	Capabilities protocol.ServerCapabilities
	ServerInfo   struct {
		Name    string
		Version string
	}
}

// lspClientAttribution 提取服务端能力和进程身份的有限观测字段，排除路径、参数和请求正文。
func lspClientAttribution(current Client) map[string]any {
	attrs := map[string]any{}
	concrete, concreteOK := concreteClient(current)
	if concreteOK {
		concrete.stateMu.RLock()
		known := concrete.initializeResponseKnown
		name := concrete.serverName
		version := concrete.serverVersion
		capabilities := concrete.capabilities
		profile := concrete.serverProfile
		profileDisabled := concrete.knownJDTLSTypeDefinitionOff
		concrete.stateMu.RUnlock()
		if known {
			attrs["capabilities_known"] = true
			attrs["capability_snapshot"] = serverCapabilitySnapshot(capabilities)
		}
		if name != "" {
			attrs["server_name"] = name
		}
		if version != "" {
			attrs["server_version"] = version
		}
		if profile = strings.TrimSpace(profile); profile != "" {
			attrs["server_profile"] = profile
			attrs["type_definition_profile_disabled"] = profileDisabled
		}
		if executable := concrete.serverExecutable(); executable != "" {
			attrs["server_executable"] = executable
		}
		if pid := concrete.serverProcessID(); pid > 0 {
			attrs["server_pid"] = pid
		}
		return attrs
	}
	if capabilityClient, ok := current.(ServerCapabilitiesClient); ok {
		attrs["capabilities_known"] = true
		attrs["capability_snapshot"] = serverCapabilitySnapshot(capabilityClient.ServerCapabilities())
	}
	return attrs
}

// serverExecutable 返回子进程可执行文件的 basename，避免日志记录原始路径。
func (c *client) serverExecutable() string {
	if c == nil || c.transport == nil || c.transport.cmd == nil {
		return ""
	}
	command := strings.TrimSpace(c.transport.cmd.Path)
	if command == "" && len(c.transport.cmd.Args) > 0 {
		command = strings.TrimSpace(c.transport.cmd.Args[0])
	}
	if command == "" {
		return ""
	}
	executable := path.Base(command)
	if executable == "." || executable == "/" || executable == "" {
		return ""
	}
	return executable
}

func (c *client) serverProcessID() int {
	if c == nil || c.transport == nil || c.transport.cmd == nil || c.transport.cmd.Process == nil {
		return 0
	}
	return c.transport.cmd.Process.Pid
}

func serverCapabilitySnapshot(capabilities protocol.ServerCapabilities) string {
	values := []string{
		"call_hierarchy=" + boolString(serverCapabilityAvailable(capabilities.CallHierarchyProvider)),
		"code_action=" + boolString(serverCapabilityAvailable(capabilities.CodeActionProvider)),
		"completion=" + boolString(serverCapabilityAvailable(capabilities.CompletionProvider)),
		"definition=" + boolString(serverCapabilityAvailable(capabilities.DefinitionProvider)),
		"diagnostic=" + boolString(serverCapabilityAvailable(capabilities.DiagnosticProvider)),
		"document_formatting=" + boolString(serverCapabilityAvailable(capabilities.DocumentFormattingProvider)),
		"document_symbol=" + boolString(serverCapabilityAvailable(capabilities.DocumentSymbolProvider)),
		"folding_range=" + boolString(serverCapabilityAvailable(capabilities.FoldingRangeProvider)),
		"hover=" + boolString(serverCapabilityAvailable(capabilities.HoverProvider)),
		"implementation=" + boolString(serverCapabilityAvailable(capabilities.ImplementationProvider)),
		"references=" + boolString(serverCapabilityAvailable(capabilities.ReferencesProvider)),
		"rename=" + boolString(serverCapabilityAvailable(capabilities.RenameProvider)),
		"semantic_tokens=" + boolString(serverCapabilityAvailable(capabilities.SemanticTokensProvider)),
		"signature_help=" + boolString(serverCapabilityAvailable(capabilities.SignatureHelpProvider)),
		"text_document_sync=" + boolString(serverCapabilityAvailable(capabilities.TextDocumentSync)),
		"type_definition=" + boolString(serverCapabilityAvailable(capabilities.TypeDefinitionProvider)),
		"type_hierarchy=" + boolString(serverCapabilityAvailable(capabilities.TypeHierarchyProvider)),
		"workspace_symbol=" + boolString(serverCapabilityAvailable(capabilities.WorkspaceSymbolProvider)),
	}
	return strings.Join(values, ",")
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func (c *client) canInitialize(rootURI string) error {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if c.shutdown {
		return ErrClientClosed
	}
	if !c.initialized {
		return nil
	}
	if c.rootURI == rootURI {
		return nil
	}
	return fmt.Errorf("LSP client already initialized for %q", c.rootURI)
}

func (c *client) ensureOpen() error {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if c.shutdown {
		return ErrClientClosed
	}
	return nil
}

func (c *client) isInitialized() bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.initialized
}

func (c *client) isShutdown() bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.shutdown
}

func (c *client) markShutdown() {
	c.stateMu.Lock()
	c.shutdown = true
	c.stateMu.Unlock()
}

func (c *client) markDeadIfClientFailure(err error) {
	if isClientDeadError(err) {
		c.markShutdown()
	}
}

func decodeInitializeResultAttribution(result json.RawMessage) (protocol.ServerCapabilities, initializeServerInfo, error) {
	if len(result) == 0 {
		return protocol.ServerCapabilities{}, initializeServerInfo{}, nil
	}
	var decoded initializeResultAttribution
	if err := json.Unmarshal(result, &decoded); err != nil {
		return protocol.ServerCapabilities{}, initializeServerInfo{}, fmt.Errorf("decode initialize result: %w", err)
	}
	decoded.Capabilities.SemanticTokensProvider = normalizeSemanticTokensProvider(decoded.Capabilities.SemanticTokensProvider)
	return decoded.Capabilities, initializeServerInfo{
		name:    strings.TrimSpace(decoded.ServerInfo.Name),
		version: strings.TrimSpace(decoded.ServerInfo.Version),
	}, nil
}

// normalizeServerProfileCapabilities 应用 resolver 传入的精确 product profile；未知 profile 保持原能力。
func normalizeServerProfileCapabilities(capabilities protocol.ServerCapabilities, profile string) protocol.ServerCapabilities {
	if strings.TrimSpace(profile) == ServerProfileJDTLS160 {
		capabilities.TypeDefinitionProvider = nil
	}
	return capabilities
}

// normalizeSemanticTokensProvider 只保留带完整 legend 的 semanticTokensProvider。
// LSP 服务端若只报 full=true 或缺少 tokenTypes，能力实际上不可用；初始化阶段将其
// 明确归一化为未声明，使上层返回 typed capability_unsupported，而不是伪造 token 类型
// 或把服务端协议错误降级成合法空结果。该规则是协议公共语义，与平台无关。
func normalizeSemanticTokensProvider(provider any) any {
	if provider == nil {
		return nil
	}
	if _, _, err := semanticTokensLegendFromProvider(provider); err != nil {
		return invalidSemanticTokensProvider{}
	}
	return provider
}

// clientCapabilities 声明本 sidecar 支持的 LSP 能力集合。
// 这些能力决定 server 初始化后的返回形态，变更时要同步检查 format/render 层。
func clientCapabilities() protocol.ClientCapabilities {
	return protocol.ClientCapabilities{
		Workspace: &protocol.WorkspaceClientCapability{
			WorkspaceFolders: true,
		},
		TextDocument: &protocol.TextDocumentClientCapabilities{
			PublishDiagnostics: &protocol.PublishDiagnosticsCapability{
				RelatedInformation: true,
			},
			Diagnostic: &protocol.DiagnosticClientCapability{
				DynamicRegistration: true,
			},
			Hover: &protocol.HoverCapability{
				ContentFormat: []string{"markdown", "plaintext"},
			},
			Completion: &protocol.CompletionClientCapability{
				DynamicRegistration: true,
			},
			Rename: &protocol.RenameClientCapability{
				PrepareSupport: true,
			},
			DocumentSymbol: &protocol.DocumentSymbolCapability{
				DynamicRegistration:               true,
				HierarchicalDocumentSymbolSupport: true,
			},
			Definition: &protocol.DynamicRegistrationCapability{
				DynamicRegistration: true,
			},
			Implementation: &protocol.DynamicRegistrationCapability{
				DynamicRegistration: true,
			},
			TypeDefinition: &protocol.DynamicRegistrationCapability{
				DynamicRegistration: true,
			},
			References: &protocol.DynamicRegistrationCapability{
				DynamicRegistration: true,
			},
			CallHierarchy: &protocol.CallHierarchyCapability{
				DynamicRegistration: true,
			},
			TypeHierarchy: &protocol.TypeHierarchyCapability{
				DynamicRegistration: true,
			},
			CodeAction: &protocol.CodeActionCapability{
				DynamicRegistration: true,
			},
			SignatureHelp: &protocol.SignatureHelpCapability{
				DynamicRegistration: true,
			},
			Formatting: &protocol.FormattingCapability{
				DynamicRegistration: true,
			},
			FoldingRange: &protocol.FoldingRangeCapability{
				DynamicRegistration: true,
			},
			SemanticTokens: &protocol.SemanticTokensCapability{
				DynamicRegistration: true,
				Requests: &protocol.SemanticTokensRequestsCapability{
					Range: true,
					Full: protocol.SemanticTokensFullRequestsCapability{
						Delta: true,
					},
				},
				TokenTypes: []string{
					"namespace", "type", "class", "enum", "interface",
					"struct", "typeParameter", "parameter", "variable",
					"property", "enumMember", "event", "function", "method",
					"macro", "keyword", "modifier", "comment", "string",
					"number", "regexp", "operator", "decorator",
				},
				TokenModifiers: []string{
					"declaration", "definition", "readonly", "static",
					"deprecated", "abstract", "async", "modification",
					"documentation", "defaultLibrary",
				},
				Formats: []string{"relative"},
			},
		},
	}
}

func (c *client) setWorkspaceFolders(folders []protocol.WorkspaceFolder) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.workspaceFolders = cloneWorkspaceFolders(folders)
}

func (c *client) workspaceFoldersForInitialize(rootURI string) []protocol.WorkspaceFolder {
	c.stateMu.RLock()
	folders := cloneWorkspaceFolders(c.workspaceFolders)
	c.stateMu.RUnlock()
	if len(folders) != 0 {
		return folders
	}
	return workspaceFoldersFromRootURI(rootURI)
}

func workspaceFoldersFromRootURI(rootURI string) []protocol.WorkspaceFolder {
	rootURI = strings.TrimSpace(rootURI)
	if rootURI == "" {
		return nil
	}
	return []protocol.WorkspaceFolder{{
		URI:  rootURI,
		Name: workspaceName(rootURI),
	}}
}

func cloneWorkspaceFolders(folders []protocol.WorkspaceFolder) []protocol.WorkspaceFolder {
	if len(folders) == 0 {
		return nil
	}
	return append([]protocol.WorkspaceFolder(nil), folders...)
}

func workspaceName(rootURI string) string {
	parsed, err := url.Parse(rootURI)
	if err != nil || parsed.Path == "" {
		return path.Base(rootURI)
	}
	name := path.Base(parsed.Path)
	if name == "." || name == "/" {
		return parsed.Path
	}
	return name
}

func defaultArgs(args []string) []string {
	if len(args) != 0 {
		return append([]string(nil), args...)
	}
	return nil
}

func normalizeProcessID(processID int) int {
	if processID > 0 {
		return processID
	}
	return os.Getpid()
}

func (t *transport) joinWaitError(err error) error {
	if waitErr := t.waitErr(); waitErr != nil {
		return errors.Join(err, waitErr)
	}
	return err
}

// Write 记录 LSP stderr/stdout 片段并保持固定容量。
// 超出 limit 时只保留尾部内容，避免失败诊断无限占用内存。
func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	b.total += int64(n)
	if b.buf.Len() <= b.limit {
		return n, err
	}
	raw := append([]byte(nil), b.buf.Bytes()...)
	b.buf.Reset()
	b.buf.Write(raw[len(raw)-b.limit:])
	return n, err
}

// String 返回当前缓冲的尾部日志快照。
// 读取时持锁，避免与 transport 写入并发访问 bytes.Buffer。
func (b *limitedBuffer) String() string {
	text, _, _ := b.Snapshot()
	return text
}

// Snapshot 返回当前尾部、累计字节数和是否发生截断，供脱敏生命周期日志使用。
func (b *limitedBuffer) Snapshot() (string, int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String(), b.total, b.total > int64(b.buf.Len())
}

// Error 把 JSON-RPC 错误码和消息组合成人类可读文本。
// Data 保留在结构体上供上层需要时再做细分处理。
func (e *responseError) Error() string {
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

// JSONRPCErrorCode 暴露服务端错误码，供上层按协议语义分类且不依赖错误文本。
func (e *responseError) JSONRPCErrorCode() int {
	if e == nil {
		return 0
	}
	return e.Code
}

func normalizeID(id json.RawMessage) string {
	return string(bytes.TrimSpace(id))
}
