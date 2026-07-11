package turn

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/discovery"
)

func TestManifestUsesStdioWhenPeerDiscoveryStale(t *testing.T) {
	addr := closedManifestTCPAddr(t)
	if err := discovery.WriteDiscoveryFile("mcp-lsp", os.Getpid(), addr); err != nil {
		t.Fatalf("WriteDiscoveryFile() error = %v", err)
	}
	t.Cleanup(func() { _ = discovery.CleanupDiscoveryFile("mcp-lsp", os.Getpid()) })

	builder := newManifestBuilder("/tmp/super-agent-bin", contract.BuildManifest)
	manifest := builder.Build(PrepareInput{AgentID: "agent-p2"}, "thread-p2")
	lsp := manifestBinary(t, manifest, dto.FamilyLSP)

	if lsp.Type == "http" || lsp.URL != "" {
		t.Fatalf("lsp manifest = %#v, want stdio command fallback when discovery is stale", lsp)
	}
	wantCommand := filepath.Join("/tmp/super-agent-bin", "mcp-lsp")
	if len(lsp.Command) != 1 || lsp.Command[0] != wantCommand {
		t.Fatalf("lsp command = %#v, want [%q]", lsp.Command, wantCommand)
	}
}

func TestManifestIgnoresHealthyHTTPRunnerDiscoveryForStdioOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]any{},
		})
	}))
	t.Cleanup(server.Close)

	addr := strings.TrimPrefix(server.URL, "http://")
	if err := discovery.WriteDiscoveryFile("mcp-lsp", os.Getpid(), addr); err != nil {
		t.Fatalf("WriteDiscoveryFile() error = %v", err)
	}
	t.Cleanup(func() { _ = discovery.CleanupDiscoveryFile("mcp-lsp", os.Getpid()) })

	builder := newManifestBuilder("/tmp/super-agent-bin", contract.BuildManifest)
	manifest := builder.Build(PrepareInput{AgentID: "agent-p2"}, "thread-p2")
	lsp := manifestBinary(t, manifest, dto.FamilyLSP)

	if lsp.Type == "http" || lsp.URL != "" {
		t.Fatalf("lsp manifest = %#v, want stdio command despite healthy HTTP peer", lsp)
	}
	wantCommand := filepath.Join("/tmp/super-agent-bin", "mcp-lsp")
	if len(lsp.Command) != 1 || lsp.Command[0] != wantCommand {
		t.Fatalf("lsp command = %#v, want [%q]", lsp.Command, wantCommand)
	}
}

func TestManifestBuilderPropagatesAdditionalWorkingDirectories(t *testing.T) {
	var got dto.ManifestContext
	builder := newManifestBuilder("/tmp/super-agent-bin", func(ctx dto.ManifestContext) dto.MCPManifest {
		got = ctx
		return dto.MCPManifest{}
	})

	_ = builder.Build(PrepareInput{
		AgentID:                      "agent-p2",
		CWD:                          "/repo",
		AdditionalWorkingDirectories: []string{"/repo/packages/api"},
	}, "thread-p2")

	if got.CWD != "/repo" {
		t.Fatalf("ManifestContext.CWD = %q, want /repo", got.CWD)
	}
	if len(got.AdditionalWorkingDirectories) != 1 || got.AdditionalWorkingDirectories[0] != "/repo/packages/api" {
		t.Fatalf("AdditionalWorkingDirectories = %#v, want [/repo/packages/api]", got.AdditionalWorkingDirectories)
	}
}

func TestManifestBuilderCarriesPeerHTTPToken(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "peer-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer peer-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]any{},
		})
	}))
	t.Cleanup(server.Close)

	addr := strings.TrimPrefix(server.URL, "http://")
	if err := discovery.WriteDiscoveryFile("mcp-lsp", os.Getpid(), addr); err != nil {
		t.Fatalf("WriteDiscoveryFile() error = %v", err)
	}
	t.Cleanup(func() { _ = discovery.CleanupDiscoveryFile("mcp-lsp", os.Getpid()) })

	var got dto.ManifestContext
	builder := newManifestBuilder("/tmp/super-agent-bin", func(ctx dto.ManifestContext) dto.MCPManifest {
		got = ctx
		return dto.MCPManifest{}
	})

	_ = builder.Build(PrepareInput{AgentID: "agent-p2"}, "thread-p2")

	if got.PeerHTTPAddrs[dto.FamilyLSP] != addr {
		t.Fatalf("PeerHTTPAddrs[lsp] = %q, want %q", got.PeerHTTPAddrs[dto.FamilyLSP], addr)
	}
	if got.PeerHTTPTokens[dto.FamilyLSP] != "peer-secret" {
		t.Fatalf("PeerHTTPTokens[lsp] = %q, want peer-secret", got.PeerHTTPTokens[dto.FamilyLSP])
	}
}

func TestManifestBuilderCarriesLegacyPeerHTTPToken(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_SESSION_TOKEN", "")
	t.Setenv("GO_AGENT_MCP_SESSION_TOKEN", "legacy-peer-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer legacy-peer-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]any{},
		})
	}))
	t.Cleanup(server.Close)

	addr := strings.TrimPrefix(server.URL, "http://")
	if err := discovery.WriteDiscoveryFile("mcp-lsp", os.Getpid(), addr); err != nil {
		t.Fatalf("WriteDiscoveryFile() error = %v", err)
	}
	t.Cleanup(func() { _ = discovery.CleanupDiscoveryFile("mcp-lsp", os.Getpid()) })

	var got dto.ManifestContext
	builder := newManifestBuilder("/tmp/super-agent-bin", func(ctx dto.ManifestContext) dto.MCPManifest {
		got = ctx
		return dto.MCPManifest{}
	})

	_ = builder.Build(PrepareInput{AgentID: "agent-p2"}, "thread-p2")

	if got.PeerHTTPAddrs[dto.FamilyLSP] != addr {
		t.Fatalf("PeerHTTPAddrs[lsp] = %q, want %q", got.PeerHTTPAddrs[dto.FamilyLSP], addr)
	}
	if got.PeerHTTPTokens[dto.FamilyLSP] != "legacy-peer-secret" {
		t.Fatalf("PeerHTTPTokens[lsp] = %q, want legacy-peer-secret", got.PeerHTTPTokens[dto.FamilyLSP])
	}
}

func manifestBinary(t *testing.T, manifest dto.MCPManifest, family dto.ToolFamily) dto.MCPBinary {
	t.Helper()
	for _, bin := range manifest.Binaries {
		if bin.Name == string(family) {
			return bin
		}
	}
	t.Fatalf("manifest missing family %q: %#v", family, manifest)
	return dto.MCPBinary{}
}

func closedManifestTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return addr
}
