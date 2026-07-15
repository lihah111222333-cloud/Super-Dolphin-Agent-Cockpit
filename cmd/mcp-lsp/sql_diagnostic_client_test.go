package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestSQLDiagnosticClientReplacesSQLitePeerDiagnostics(t *testing.T) {
	root := sqliteDiagnosticsRepoRoot(t)
	path := filepath.Join(root, "cmd", "mcp-orch", "sql", "queries", "command_card.sql")
	text, err := os.ReadFile(path)
	require.NoError(t, err)
	uri := sqliteDiagnosticsFileURI(path)

	outer := &capturingSQLNotificationHandler{}
	filtered := &sqlDiagnosticNotificationHandler{root: root, next: outer}
	inner := &fakeSQLDiagnosticClient{handler: filtered, peerDiagnostics: []protocol.Diagnostic{{Message: "old parser error"}}}
	client, err := newSQLDiagnosticClient(inner, root, outer)
	require.NoError(t, err)

	require.NoError(t, client.DidOpen(context.Background(), uri, "sql", 1, string(text)))
	require.Len(t, outer.published, 1)
	require.Empty(t, outer.published[0].Diagnostics)
	require.NotNil(t, outer.published[0].Version)
	require.Equal(t, 1, *outer.published[0].Version)
}

func TestSQLDiagnosticClientPublishesSQLiteSyntaxError(t *testing.T) {
	root := sqliteDiagnosticsRepoRoot(t)
	uri := sqliteDiagnosticsFileURI(filepath.Join(root, "sql", "queries", "agent_status.sql"))
	outer := &capturingSQLNotificationHandler{}
	client, err := newSQLDiagnosticClient(&fakeSQLDiagnosticClient{}, root, outer)
	require.NoError(t, err)

	require.NoError(t, client.DidOpen(context.Background(), uri, "sql", 1, "-- name: Broken :one\nSELECT FROM agent_status;"))
	require.Len(t, outer.published, 1)
	require.NotEmpty(t, outer.published[0].Diagnostics)
	require.Equal(t, sqliteDiagnosticsSource, outer.published[0].Diagnostics[0].Source)
	require.NotNil(t, outer.published[0].Version)
	require.Equal(t, 1, *outer.published[0].Version)
}

func TestSQLDiagnosticClientRejectsIncrementalSQLiteChangeBeforeForwarding(t *testing.T) {
	root := sqliteDiagnosticsRepoRoot(t)
	uri := sqliteDiagnosticsFileURI(filepath.Join(root, "sql", "queries", "agent_status.sql"))
	inner := &fakeSQLDiagnosticClient{}
	client, err := newSQLDiagnosticClient(inner, root, &capturingSQLNotificationHandler{})
	require.NoError(t, err)

	err = client.DidChange(context.Background(), uri, 2, []protocol.TextDocumentContentChangeEvent{{
		Range: &protocol.Range{}, Text: "SELECT 1;",
	}})
	require.ErrorContains(t, err, "one full-document change")
	require.Equal(t, 0, inner.didChangeCalls)
}

func TestSQLDiagnosticNotificationHandlerPreservesDiagnosticsOutsideSQLiteOwners(t *testing.T) {
	root := sqliteDiagnosticsRepoRoot(t)
	outer := &capturingSQLNotificationHandler{}
	handler := &sqlDiagnosticNotificationHandler{root: root, next: outer}
	params := protocol.PublishDiagnosticsParams{
		URI:         sqliteDiagnosticsFileURI(filepath.Join(root, "fixtures", "schema.sql")),
		Diagnostics: []protocol.Diagnostic{{Message: "external diagnostic"}},
	}
	require.NoError(t, handler.PublishDiagnostics(params))
	require.Equal(t, []protocol.PublishDiagnosticsParams{params}, outer.published)
}

type capturingSQLNotificationHandler struct {
	published []protocol.PublishDiagnosticsParams
}

func (h *capturingSQLNotificationHandler) PublishDiagnostics(params protocol.PublishDiagnosticsParams) error {
	h.published = append(h.published, params)
	return nil
}

func (*capturingSQLNotificationHandler) LogMessage(protocol.LogMessageParams) error { return nil }

type fakeSQLDiagnosticClient struct {
	handler         protocol.NotificationHandler
	peerDiagnostics []protocol.Diagnostic
	didChangeCalls  int
}

func (*fakeSQLDiagnosticClient) Initialize(context.Context, string) error { return nil }
func (*fakeSQLDiagnosticClient) Shutdown(context.Context) error           { return nil }
func (*fakeSQLDiagnosticClient) Close() error                             { return nil }
func (*fakeSQLDiagnosticClient) Healthy() bool                            { return true }
func (*fakeSQLDiagnosticClient) ServerCapabilities() protocol.ServerCapabilities {
	return protocol.ServerCapabilities{}
}
func (*fakeSQLDiagnosticClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage(`null`), nil
}
func (*fakeSQLDiagnosticClient) Notify(context.Context, string, any) error { return nil }
func (c *fakeSQLDiagnosticClient) DidOpen(_ context.Context, uri, _ string, _ int, _ string) error {
	if c.handler == nil {
		return nil
	}
	return c.handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: c.peerDiagnostics})
}

func (c *fakeSQLDiagnosticClient) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	c.didChangeCalls++
	return nil
}
func (*fakeSQLDiagnosticClient) DidClose(context.Context, string) error { return nil }

var _ multilsp.Client = (*fakeSQLDiagnosticClient)(nil)
