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
	"strconv"
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

// Options 描述启动 LSP client 时传给 transport、initialize 和回调处理器的配置。
type Options struct {
	Binary              string
	Args                []string
	Dir                 string
	Env                 []string
	ProcessID           int
	InitOptions         map[string]any
	NotificationHandler protocol.NotificationHandler
	RequestHandler      ServerRequestHandler
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
	mu                      sync.RWMutex
	diagnosticRegistrations map[string]struct{}
}

func newDynamicRegistrationTracker() *dynamicRegistrationTracker {
	return &dynamicRegistrationTracker{diagnosticRegistrations: map[string]struct{}{}}
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
			ID     string `json:"id"`
			Method string `json:"method"`
		} `json:"registrations"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for index, registration := range request.Registrations {
		if registration.Method != protocol.MethodTextDocumentDiagnostic {
			continue
		}
		t.diagnosticRegistrations[dynamicRegistrationKey(registration.ID, registration.Method, index)] = struct{}{}
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
	t.mu.Lock()
	defer t.mu.Unlock()
	for index, registration := range request.Unregisterations {
		if registration.Method != protocol.MethodTextDocumentDiagnostic {
			continue
		}
		key := dynamicRegistrationKey(registration.ID, registration.Method, index)
		if strings.TrimSpace(registration.ID) == "" {
			clear(t.diagnosticRegistrations)
			continue
		}
		delete(t.diagnosticRegistrations, key)
	}
	return nil
}

func (t *dynamicRegistrationTracker) serverCapabilities(capabilities protocol.ServerCapabilities) protocol.ServerCapabilities {
	if t == nil {
		return capabilities
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.diagnosticRegistrations) > 0 {
		capabilities.DiagnosticProvider = true
	}
	return capabilities
}

func dynamicRegistrationKey(id, method string, index int) string {
	id = strings.TrimSpace(id)
	if id != "" {
		return id
	}
	return strings.TrimSpace(method) + "#" + strconv.Itoa(index)
}

type limitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
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
		Binary:              binary,
		Args:                defaultArgs(options.Args),
		Dir:                 options.Dir,
		Env:                 append([]string(nil), options.Env...),
		NotificationHandler: options.NotificationHandler,
		RequestHandler:      dynamicRegistrationRequestHandler(dynamicRegistrations, requestHandler),
	})
	if err != nil {
		return nil, err
	}
	return &client{
		transport:            transport,
		processID:            normalizeProcessID(options.ProcessID),
		initOptions:          options.InitOptions,
		dynamicRegistrations: dynamicRegistrations,
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
	capabilities, err := decodeInitializeResult(result)
	if err != nil {
		return err
	}
	c.stateMu.Lock()
	c.rootURI = rootURI
	c.initialized = true
	c.capabilities = capabilities
	c.stateMu.Unlock()
	return nil
}

// Shutdown 按 LSP 协议发送 shutdown 和 exit。
// 未初始化的 client 只更新本地关闭状态，transport 关闭错误会透传给调用方。
func (c *client) Shutdown(ctx context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.isShutdown() {
		return nil
	}
	if !c.isInitialized() {
		c.markShutdown()
		return nil
	}
	if _, err := c.transport.request(ctx, "shutdown", nil); err != nil && !errors.Is(err, ErrTransportClosed) {
		return fmt.Errorf("LSP shutdown request: %w", err)
	}
	if err := c.transport.notify(ctx, methodExit, nil); err != nil && !errors.Is(err, ErrTransportClosed) {
		return fmt.Errorf("LSP exit notification: %w", err)
	}
	// 给整棵服务进程树一个固定的正常退出与缓存落盘窗口；之后由 Close 强制回收残留后代。
	waitForGracefulProcessTreeExit(ctx, gracefulProcessExitTimeout)
	c.markShutdown()
	return nil
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
	c.stateMu.RUnlock()
	return c.dynamicRegistrations.serverCapabilities(capabilities)
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

func decodeInitializeResult(result json.RawMessage) (protocol.ServerCapabilities, error) {
	if len(result) == 0 {
		return protocol.ServerCapabilities{}, nil
	}
	var decoded protocol.InitializeResult
	if err := json.Unmarshal(result, &decoded); err != nil {
		return protocol.ServerCapabilities{}, fmt.Errorf("decode initialize result: %w", err)
	}
	return decoded.Capabilities, nil
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
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Error 把 JSON-RPC 错误码和消息组合成人类可读文本。
// Data 保留在结构体上供上层需要时再做细分处理。
func (e *responseError) Error() string {
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

func normalizeID(id json.RawMessage) string {
	return string(bytes.TrimSpace(id))
}
