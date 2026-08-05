package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// sqlDiagnosticNotificationHandler keeps the existing SQL peer for semantic
// features while replacing its dialect-incompatible diagnostics on SQLite files.
type sqlDiagnosticNotificationHandler struct {
	root string
	next protocol.NotificationHandler
}

// PublishDiagnostics 仅拦截 SQLite 文件的旧解析器诊断，其他通知原样转发。
func (h *sqlDiagnosticNotificationHandler) PublishDiagnostics(params protocol.PublishDiagnosticsParams) error {
	path, err := format.AbsolutePathFromURI(params.URI)
	if err != nil {
		return fmt.Errorf("resolve SQL diagnostics URI: %w", err)
	}
	if isSQLiteDiagnosticsPath(h.root, path) {
		return nil
	}
	return h.next.PublishDiagnostics(params)
}

// LogMessage 原样转发底层 SQL peer 的日志通知。
func (h *sqlDiagnosticNotificationHandler) LogMessage(params protocol.LogMessageParams) error {
	return h.next.LogMessage(params)
}

// sqlDiagnosticClientCapabilities isolates optional capability probes from the
// document/diagnostics orchestration while preserving promoted interfaces.
type sqlDiagnosticClientCapabilities struct {
	inner multilsp.Client
}

// Healthy 复用底层 peer 的健康状态。
func (c sqlDiagnosticClientCapabilities) Healthy() bool {
	healthy, ok := c.inner.(multilsp.HealthCheckedClient)
	return !ok || healthy.Healthy()
}

// ServerCapabilities 暴露底层 peer 的语义能力，避免组合层伪造能力。
func (c sqlDiagnosticClientCapabilities) ServerCapabilities() protocol.ServerCapabilities {
	capabilities, ok := c.inner.(multilsp.ServerCapabilitiesClient)
	if !ok {
		return protocol.ServerCapabilities{}
	}
	return capabilities.ServerCapabilities()
}

// sqlDiagnosticClient delegates SQL navigation to the configured peer and
// publishes SQLite diagnostics from the production SQLite parser.
type sqlDiagnosticClient struct {
	sqlDiagnosticClientCapabilities
	inner       multilsp.Client
	root        string
	handler     protocol.NotificationHandler
	diagnostics *sqliteDiagnosticsState
}

var _ multilsp.WrappedClient = (*sqlDiagnosticClient)(nil)

func newSQLDiagnosticClient(
	inner multilsp.Client,
	root string,
	handler protocol.NotificationHandler,
) (multilsp.Client, error) {
	if inner == nil {
		return nil, fmt.Errorf("SQL diagnostic client inner client is nil")
	}
	if handler == nil {
		return nil, protocol.ErrNotificationHandlerNil
	}
	return &sqlDiagnosticClient{
		sqlDiagnosticClientCapabilities: sqlDiagnosticClientCapabilities{inner: inner},
		inner:                           inner, root: root, handler: handler, diagnostics: newSQLiteDiagnosticsState(),
	}, nil
}

// Initialize 初始化底层 SQL peer，组合层不改变其语义能力声明。
func (c *sqlDiagnosticClient) Initialize(ctx context.Context, root string) error {
	return c.inner.Initialize(ctx, root)
}

// Shutdown 请求底层 SQL peer 完成协议关闭。
func (c *sqlDiagnosticClient) Shutdown(ctx context.Context) error { return c.inner.Shutdown(ctx) }

// Request 将语义请求原样交给底层 SQL peer。
func (c *sqlDiagnosticClient) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.inner.Request(ctx, method, params)
}

// Notify 将非文档通知原样交给底层 SQL peer。
func (c *sqlDiagnosticClient) Notify(ctx context.Context, method string, params any) error {
	return c.inner.Notify(ctx, method, params)
}

// DidOpen 同步底层 peer 后，用真实 SQLite 引擎发布诊断快照。
func (c *sqlDiagnosticClient) DidOpen(
	ctx context.Context,
	uri string,
	languageID string,
	version int,
	text string,
) error {
	if err := c.inner.DidOpen(ctx, uri, languageID, version, text); err != nil {
		return err
	}
	if !isSQLiteDiagnosticsURI(c.root, uri) {
		return nil
	}
	return publishSQLiteDiagnostics(ctx, c.root, c.diagnostics, c.handler, uri, version, text)
}

// DidChange 要求全量文本同步，并刷新 SQLite 诊断快照。
func (c *sqlDiagnosticClient) DidChange(
	ctx context.Context,
	uri string,
	version int,
	changes []protocol.TextDocumentContentChangeEvent,
) error {
	if isSQLiteDiagnosticsURI(c.root, uri) && (len(changes) != 1 || changes[0].Range != nil || changes[0].RangeLength != nil) {
		return fmt.Errorf("SQLite diagnostics require one full-document change for %s", uri)
	}
	if err := c.inner.DidChange(ctx, uri, version, changes); err != nil {
		return err
	}
	if !isSQLiteDiagnosticsURI(c.root, uri) {
		return nil
	}
	text := changes[0].Text
	return publishSQLiteDiagnostics(ctx, c.root, c.diagnostics, c.handler, uri, version, text)
}

// DidClose 关闭底层文档；组合层不保存文档副本。
func (c *sqlDiagnosticClient) DidClose(ctx context.Context, uri string) error {
	return c.inner.DidClose(ctx, uri)
}

// Close 释放底层 SQL peer 进程与传输资源。
func (c *sqlDiagnosticClient) Close() error { return c.inner.Close() }

// UnderlyingLSPClient 暴露真实 transport owner，使 wrapper 仍参与统一进程树与 RSS 生命周期管理。
func (c *sqlDiagnosticClient) UnderlyingLSPClient() multilsp.Client { return c.inner }

func isSQLiteDiagnosticsURI(root, uri string) bool {
	path, err := format.AbsolutePathFromURI(uri)
	return err == nil && isSQLiteDiagnosticsPath(root, path)
}

func publishSQLiteDiagnostics(
	ctx context.Context,
	root string,
	state *sqliteDiagnosticsState,
	handler protocol.NotificationHandler,
	uri string,
	version int,
	text string,
) error {
	diagnostics, err := state.validateSQLiteDocument(ctx, root, uri, text)
	if err != nil {
		return fmt.Errorf("validate SQLite document: %w", err)
	}
	if err := handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: uri, Version: &version, Diagnostics: diagnostics,
	}); err != nil {
		return fmt.Errorf("publish SQLite diagnostics: %w", err)
	}
	return nil
}
