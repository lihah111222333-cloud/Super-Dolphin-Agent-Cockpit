package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge"
)

type stubToolCatalogLister struct {
	tools []toolbridge.ToolCatalogEntry
	cwd   string
	calls int
	err   error
}

func (s *stubToolCatalogLister) ListToolCatalog(_ context.Context, cwd string) ([]toolbridge.ToolCatalogEntry, error) {
	s.cwd = cwd
	s.calls++
	return append([]toolbridge.ToolCatalogEntry(nil), s.tools...), s.err
}

func TestToolCatalogRPCDispatchesStrictWorkspaceRequest(t *testing.T) {
	lister := &stubToolCatalogLister{tools: []toolbridge.ToolCatalogEntry{{
		ServerName: "lsp", ToolName: "grep", DisplayName: "grep",
		Description: "Search source", Enabled: true,
	}}}
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(toolCatalogHandlers(lister).Handlers)

	raw, err := server.Dispatch(t.Context(), "toolbridge/tools/list", json.RawMessage(`{"cwd":"/repo/app"}`))
	if err != nil {
		t.Fatalf("Dispatch toolbridge/tools/list: %v", err)
	}
	if lister.cwd != "/repo/app" || !bytes.Contains(raw, []byte(`"toolName":"grep"`)) {
		t.Fatalf("cwd=%q raw=%s", lister.cwd, raw)
	}
}

func TestToolCatalogRPCRejectsInvalidPayloadBeforeLister(t *testing.T) {
	for _, raw := range []string{`{}`, `{"cwd":" "}`, `{"cwd":"/repo","extra":true}`} {
		t.Run(raw, func(t *testing.T) {
			lister := &stubToolCatalogLister{}
			server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
			server.Register(toolCatalogHandlers(lister).Handlers)
			if _, err := server.Dispatch(t.Context(), "toolbridge/tools/list", json.RawMessage(raw)); err == nil {
				t.Fatalf("Dispatch(%s) error = nil", raw)
			}
			if lister.calls != 0 {
				t.Fatalf("lister calls = %d, want 0", lister.calls)
			}
		})
	}
}
