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

	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/protocol"
)

const methodExit = "exit"

var (
	ErrClientClosed       = errors.New("LSP client closed")
	ErrMethodNotSupported = errors.New("LSP server request not supported")
	ErrTransportClosed    = errors.New("LSP transport closed")
	ErrBinaryRequired     = errors.New("LSP binary is required")
)

// Client describes a multilsp API type.
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

// HealthCheckedClient describes a multilsp API type.
type HealthCheckedClient interface {
	Client
	Healthy() bool
}

// Options configures multilsp behavior.
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
	transport        *transport
	processID        int
	initOptions      map[string]any
	workspaceFolders []protocol.WorkspaceFolder

	lifecycleMu sync.Mutex
	stateMu     sync.RWMutex
	rootURI     string
	initialized bool
	shutdown    bool
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

// NewClient 创建客户端。
func NewClient(binary string, handler protocol.NotificationHandler) (Client, error) {
	return NewClientWithOptions(Options{
		Binary:              binary,
		NotificationHandler: handler,
	})
}

// NewClientWithOptions 创建带选项的客户端。
func NewClientWithOptions(options Options) (Client, error) {
	binary := strings.TrimSpace(options.Binary)
	if binary == "" {
		return nil, ErrBinaryRequired
	}
	requestHandler := options.RequestHandler
	if requestHandler == nil {
		requestHandler = configurationRequestHandlerFromInitOptions(options.InitOptions)
	}
	transport, err := newTransport(transportOptions{
		Binary:              binary,
		Args:                defaultArgs(options.Args),
		Dir:                 options.Dir,
		Env:                 append([]string(nil), options.Env...),
		NotificationHandler: options.NotificationHandler,
		RequestHandler:      requestHandler,
	})
	if err != nil {
		return nil, err
	}
	return &client{
		transport:   transport,
		processID:   normalizeProcessID(options.ProcessID),
		initOptions: options.InitOptions,
	}, nil
}

// Initialize 发送 LSP 初始化请求。
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
	if err := decodeInitializeResult(result); err != nil {
		return err
	}
	c.stateMu.Lock()
	c.rootURI = rootURI
	c.initialized = true
	c.stateMu.Unlock()
	return nil
}

// Shutdown 发送 LSP 关闭请求。
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
	c.markShutdown()
	return nil
}

// Request 发送 LSP 请求并等待响应。
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

// Notify 发送通知消息。
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

// DidOpen 把文档打开事件转给 LSP。
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

// DidChange 把文档变更事件转给 LSP。
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

// DidClose 把文档关闭事件转给 LSP。
func (c *client) DidClose(ctx context.Context, uri string) error {
	params := protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	}
	return c.notifyTextDocument(ctx, protocol.MethodDidClose, params)
}

// Close 关闭 LSP 管理器资源。
func (c *client) Close() error {
	c.markShutdown()
	return c.transport.Close()
}

// Healthy 处理healthy。
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

func decodeInitializeResult(result json.RawMessage) error {
	if len(result) == 0 {
		return nil
	}
	var decoded protocol.InitializeResult
	if err := json.Unmarshal(result, &decoded); err != nil {
		return fmt.Errorf("decode initialize result: %w", err)
	}
	return nil
}

// clientCapabilities 处理客户端capabilities。
func clientCapabilities() protocol.ClientCapabilities {
	return protocol.ClientCapabilities{
		Workspace: &protocol.WorkspaceClientCapability{
			WorkspaceFolders: true,
		},
		TextDocument: &protocol.TextDocumentClientCapabilities{
			PublishDiagnostics: &protocol.PublishDiagnosticsCapability{
				RelatedInformation: true,
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

// Write 写入LSP。
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

// String 返回字符串表示。
func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Error 返回错误文本。
func (e *responseError) Error() string {
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

func normalizeID(id json.RawMessage) string {
	return string(bytes.TrimSpace(id))
}
