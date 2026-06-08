package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestNewClientFromFactoryRejectsEnvWithLegacyFactory(t *testing.T) {
	_, err := newClientFromFactory(legacyClientFactory{}, workspaceConfig{rootPath: t.TempDir(), env: []string{"GOWORK=/repo/go.work"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "env") {
		t.Fatalf("newClientFromFactory() error = %v, want env unsupported", err)
	}
}

func TestEffectiveLSPLogMessageTypeDowngradesGoplsWarningText(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   protocol.LogMessageParams
		want protocol.LogMessageType
	}{
		{
			name: "gopls orphaned shutdown warning",
			in: protocol.LogMessageParams{
				Type:    protocol.LogMessageError,
				Message: "2026/06/08 05:39:23 warning: while diagnosing orphaned files: session is shut down",
			},
			want: protocol.LogMessageWarning,
		},
		{
			name: "plain warning prefix",
			in: protocol.LogMessageParams{
				Type:    protocol.LogMessageError,
				Message: "warning: while diagnosing orphaned files: session is shut down",
			},
			want: protocol.LogMessageWarning,
		},
		{
			name: "real error stays error",
			in: protocol.LogMessageParams{
				Type:    protocol.LogMessageError,
				Message: "failed to load workspace: package metadata unavailable",
			},
			want: protocol.LogMessageError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveLSPLogMessageType(tc.in); got != tc.want {
				t.Fatalf("effectiveLSPLogMessageType() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestEnsureClientForLanguageReturnsBootstrapDidOpenError(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"bootstrap-error"}`)
	writeGenericTestFile(t, filepath.Join(root, "app.js"), "const value = 1\n")
	mgr := NewManager(Config{
		WorkspaceRoot:    root,
		ClientFactory:    bootstrapErrorFactory{err: errors.New("did open boom")},
		LanguageAdapters: NewDefaultLanguageAdapterRegistry(),
	}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.EnsureClient(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), "", "javascript")
	if err == nil || !strings.Contains(err.Error(), "did open boom") {
		t.Fatalf("EnsureClient() error = %v, want bootstrap DidOpen failure", err)
	}
}

func TestEnsureClientSkipsInitialWorkspaceBootstrapWhenDisabled(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"lazy-bootstrap"}`)
	writeGenericTestFile(t, filepath.Join(root, "app.js"), "const value = 1\n")
	factory := &recordingClientFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    factory,
		LanguageAdapters:                 NewDefaultLanguageAdapterRegistry(),
		DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.EnsureClient(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), "", "javascript")
	if err != nil {
		t.Fatalf("EnsureClient() error = %v", err)
	}
	client := requireRecordingClient(t, factory)
	if got := client.openCount(); got != 0 {
		t.Fatalf("EnsureClient() opened %d documents during initial bootstrap, want 0", got)
	}
}

func TestEnsureClientBootstrapCanBeReEnabled(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"eager-bootstrap"}`)
	writeGenericTestFile(t, filepath.Join(root, "app.js"), "const value = 1\n")
	factory := &recordingClientFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    factory,
		LanguageAdapters:                 NewDefaultLanguageAdapterRegistry(),
		DisableInitialWorkspaceBootstrap: false,
	}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.EnsureClient(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), "", "javascript")
	if err != nil {
		t.Fatalf("EnsureClient() error = %v", err)
	}
	client := requireRecordingClient(t, factory)
	if got := client.openCount(); got != 1 {
		t.Fatalf("EnsureClient() opened %d documents during enabled bootstrap, want 1", got)
	}
}

type legacyClientFactory struct{}

func (legacyClientFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	return noopClient{}, nil
}

type bootstrapErrorFactory struct {
	err error
}

func (f bootstrapErrorFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	return bootstrapErrorClient{err: f.err}, nil
}

type bootstrapErrorClient struct {
	err error
	noopClient
}

func (c bootstrapErrorClient) DidOpen(context.Context, string, string, int, string) error {
	return c.err
}

type noopClient struct{}

func (noopClient) Initialize(context.Context, string) error { return nil }
func (noopClient) Shutdown(context.Context) error           { return nil }
func (noopClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("null"), nil
}
func (noopClient) Notify(context.Context, string, any) error { return nil }
func (noopClient) DidOpen(context.Context, string, string, int, string) error {
	return nil
}
func (noopClient) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}
func (noopClient) DidClose(context.Context, string) error { return nil }
func (noopClient) Close() error                           { return nil }
