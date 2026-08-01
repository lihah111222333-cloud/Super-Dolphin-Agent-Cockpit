package main

import (
	"context"
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

// sqlDiagnosticClient delegates SQL navigation to the configured peer and
// publishes SQLite diagnostics from the production SQLite parser.
type sqlDiagnosticClient struct {
	multilsp.Client
	root    string
	handler protocol.NotificationHandler
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
		Client: inner, root: root, handler: handler,
	}, nil
}

// DidOpen 同步底层 peer 后，用真实 SQLite 引擎发布诊断快照。
func (c *sqlDiagnosticClient) DidOpen(
	ctx context.Context,
	uri string,
	languageID string,
	version int,
	text string,
) error {
	if err := c.Client.DidOpen(ctx, uri, languageID, version, text); err != nil {
		return err
	}
	if !c.isSQLiteURI(uri) {
		return nil
	}
	return c.publishSQLiteDiagnostics(ctx, uri, version, text)
}

// DidChange 要求全量文本同步，并刷新 SQLite 诊断快照。
func (c *sqlDiagnosticClient) DidChange(
	ctx context.Context,
	uri string,
	version int,
	changes []protocol.TextDocumentContentChangeEvent,
) error {
	if c.isSQLiteURI(uri) && (len(changes) != 1 || changes[0].Range != nil || changes[0].RangeLength != nil) {
		return fmt.Errorf("SQLite diagnostics require one full-document change for %s", uri)
	}
	if err := c.Client.DidChange(ctx, uri, version, changes); err != nil {
		return err
	}
	if !c.isSQLiteURI(uri) {
		return nil
	}
	text := changes[0].Text
	return c.publishSQLiteDiagnostics(ctx, uri, version, text)
}

// UnderlyingLSPClient 暴露真实 transport owner，使 wrapper 仍参与统一进程树与 RSS 生命周期管理。
func (c *sqlDiagnosticClient) UnderlyingLSPClient() multilsp.Client { return c.Client }

// Healthy 复用底层 peer 的健康状态。
func (c *sqlDiagnosticClient) Healthy() bool {
	healthy, ok := c.Client.(multilsp.HealthCheckedClient)
	return !ok || healthy.Healthy()
}

// ServerCapabilities 暴露底层 peer 的语义能力，避免组合层伪造能力。
func (c *sqlDiagnosticClient) ServerCapabilities() protocol.ServerCapabilities {
	capabilities, ok := c.Client.(multilsp.ServerCapabilitiesClient)
	if !ok {
		return protocol.ServerCapabilities{}
	}
	return capabilities.ServerCapabilities()
}

func (c *sqlDiagnosticClient) isSQLiteURI(uri string) bool {
	path, err := format.AbsolutePathFromURI(uri)
	return err == nil && isSQLiteDiagnosticsPath(c.root, path)
}

func (c *sqlDiagnosticClient) publishSQLiteDiagnostics(ctx context.Context, uri string, version int, text string) error {
	diagnostics, err := validateSQLiteDocument(ctx, c.root, uri, text)
	if err != nil {
		return fmt.Errorf("validate SQLite document: %w", err)
	}
	if err := c.handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: uri, Version: &version, Diagnostics: diagnostics,
	}); err != nil {
		return fmt.Errorf("publish SQLite diagnostics: %w", err)
	}
	return nil
}
