package gopls

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

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

const (
	defaultBinary = "gopls"
	methodExit    = "exit"
)

var (
	ErrClientClosed       = errors.New("gopls: client closed")
	ErrMethodNotSupported = errors.New("gopls: server request not supported")
	ErrTransportClosed    = errors.New("gopls: transport closed")
)

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

type Options struct {
	Binary              string
	Args                []string
	Dir                 string
	Env                 []string
	ProcessID           int
	NotificationHandler protocol.NotificationHandler
	RequestHandler      ServerRequestHandler
}

type client struct {
	transport *transport
	processID int

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

func NewClient(handler protocol.NotificationHandler) (Client, error) {
	return NewClientWithOptions(Options{NotificationHandler: handler})
}

func NewClientWithOptions(options Options) (Client, error) {
	transport, err := newTransport(transportOptions{
		Binary:              coalesceString(options.Binary, defaultBinary),
		Args:                defaultArgs(options.Args),
		Dir:                 options.Dir,
		Env:                 append([]string(nil), options.Env...),
		NotificationHandler: options.NotificationHandler,
		RequestHandler:      options.RequestHandler,
	})
	if err != nil {
		return nil, err
	}
	return &client{
		transport: transport,
		processID: normalizeProcessID(options.ProcessID),
	}, nil
}

func (c *client) Initialize(ctx context.Context, rootURI string) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if err := c.canInitialize(rootURI); err != nil {
		return err
	}
	params := protocol.InitializeParams{
		ProcessID:        c.processID,
		RootURI:          rootURI,
		Capabilities:     clientCapabilities(),
		WorkspaceFolders: workspaceFolders(rootURI),
	}
	result, err := c.transport.request(ctx, protocol.MethodInitialize, params)
	if err != nil {
		return fmt.Errorf("gopls initialize request: %w", err)
	}
	if err := c.transport.notify(ctx, protocol.MethodInitialized, struct{}{}); err != nil {
		return fmt.Errorf("gopls initialized notification: %w", err)
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
		return fmt.Errorf("gopls shutdown request: %w", err)
	}
	if err := c.transport.notify(ctx, methodExit, nil); err != nil && !errors.Is(err, ErrTransportClosed) {
		return fmt.Errorf("gopls exit notification: %w", err)
	}
	c.markShutdown()
	return nil
}

func (c *client) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := c.ensureOpen(); err != nil {
		return nil, err
	}
	result, err := requestMessage(ctx, method, params, c.transport.request)
	if err != nil {
		return nil, fmt.Errorf("gopls request %s: %w", method, err)
	}
	return result, nil
}

func (c *client) Notify(ctx context.Context, method string, params any) error {
	if err := c.ensureOpen(); err != nil {
		return err
	}
	if err := notifyMessage(ctx, method, params, c.transport.notify); err != nil {
		return fmt.Errorf("gopls notify %s: %w", method, err)
	}
	return nil
}

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

func (c *client) DidClose(ctx context.Context, uri string) error {
	params := protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	}
	return c.notifyTextDocument(ctx, protocol.MethodDidClose, params)
}

func (c *client) Close() error {
	c.markShutdown()
	return c.transport.Close()
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
	return fmt.Errorf("gopls already initialized for %q", c.rootURI)
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

func clientCapabilities() protocol.ClientCapabilities {
	return protocol.ClientCapabilities{
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
				Formats: []string{"relative"},
			},
		},
		Workspace: &protocol.WorkspaceClientCapability{
			WorkspaceFolders: true,
		},
	}
}

func workspaceFolders(rootURI string) []protocol.WorkspaceFolder {
	rootURI = strings.TrimSpace(rootURI)
	if rootURI == "" {
		return nil
	}
	return []protocol.WorkspaceFolder{{
		URI:  rootURI,
		Name: workspaceName(rootURI),
	}}
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
	return []string{"serve"}
}

func normalizeProcessID(processID int) int {
	if processID > 0 {
		return processID
	}
	return os.Getpid()
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func coalesceString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (t *transport) joinWaitError(err error) error {
	if waitErr := t.waitErr(); waitErr != nil {
		return errors.Join(err, waitErr)
	}
	return err
}

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

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (e *responseError) Error() string {
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

func normalizeID(id json.RawMessage) string {
	return string(bytes.TrimSpace(id))
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
