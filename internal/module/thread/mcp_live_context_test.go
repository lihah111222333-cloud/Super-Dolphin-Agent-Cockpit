package thread

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestBuildStartCtxKeepsOnlyLiveMCPServers(t *testing.T) {
	t.Parallel()

	repoRoot, cwd := newMCPPromptGitFixture(t)
	ctx := buildStartCtx(StartRequest{
		Provider: "codex",
		CWD:      cwd,
		Model:    "gpt-5.5",
		MCPSnapshot: contract.MCPSnapshot{
			Servers:      []string{"stale"},
			Instructions: map[string]string{"stale": "stale instructions", "lsp": "Use the LSP MCP first."},
		},
		Config: map[string]any{
			"mcpServers":                  []any{"shadow"},
			"mcpInstructions":             map[string]any{"shadow": "shadow instructions"},
			"mcpInstructionsDeltaEnabled": true,
			"frcConfig":                   map[string]any{"enabled": true, "supportedModels": []any{"gpt-5.5"}, "keepRecent": 2},
		},
	}, &contract.Config{ProjectRoot: repoRoot}, mcpLiveToolRegistryStub{instances: []contract.ToolInstance{{BinaryName: "mcp-lsp", ClientKind: "lsp", Status: mcpdto.StatusActive}}})

	if got := mcpLiveSortedStrings(ctx.MCPSnapshot.Servers); !slices.Equal(got, []string{"lsp"}) {
		t.Fatalf("MCPSnapshot.Servers = %#v, want [lsp]", ctx.MCPSnapshot.Servers)
	}
	if !ctx.MCPSnapshot.InstructionsDeltaEnabled {
		t.Fatal("InstructionsDeltaEnabled = false, want true from config")
	}
	if ctx.FRCConfig == nil || !ctx.FRCConfig.EnabledForModel("gpt-5.5") {
		t.Fatalf("FRCConfig = %#v, want parsed config for supported model", ctx.FRCConfig)
	}
}

func TestMergeConfiguredMCPServersAddsProjectServersToSnapshot(t *testing.T) {
	t.Parallel()

	got, err := mergeConfiguredMCPServers(context.Background(), contract.MCPSnapshot{
		Servers: []string{"lsp"},
	}, staticMCPServerConfigProvider{servers: map[string]contract.MCPServerConfig{
		"my-search": {
			Transport: "http",
			URL:       "https://your-domain.com/mcp",
			Headers:   map[string]string{"Authorization": "Bearer YOUR_API_KEY"},
		},
	}}, "/repo")
	if err != nil {
		t.Fatalf("mergeConfiguredMCPServers() error = %v", err)
	}
	if want := []string{"lsp", "my-search"}; !slices.Equal(mcpLiveSortedStrings(got.Servers), want) {
		t.Fatalf("MCPSnapshot.Servers = %#v, want %#v", got.Servers, want)
	}
	server := got.ServerConfigs["my-search"]
	if server.Transport != "http" || server.URL != "https://your-domain.com/mcp" || server.Headers["Authorization"] != "Bearer YOUR_API_KEY" {
		t.Fatalf("ServerConfigs = %#v, want my-search HTTP config", got.ServerConfigs)
	}
}

func newMCPPromptGitFixture(t *testing.T) (string, string) {
	t.Helper()
	repoRoot := t.TempDir()
	worktreeRoot := filepath.Join(repoRoot, "worktree")
	gitDir := filepath.Join(repoRoot, ".git", "worktrees", "feature")
	if err := os.MkdirAll(filepath.Join(worktreeRoot, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir gitdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeRoot, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatalf("write worktree .git: %v", err)
	}
	return repoRoot, filepath.Join(worktreeRoot, "pkg")
}

type mcpLiveToolRegistryStub struct {
	instances []contract.ToolInstance
}

type staticMCPServerConfigProvider struct {
	servers map[string]contract.MCPServerConfig
	err     error
}

func (p staticMCPServerConfigProvider) ListMCPServerConfigs(context.Context, string) (map[string]contract.MCPServerConfig, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.servers, nil
}

func (s mcpLiveToolRegistryStub) Register(context.Context, mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
	return mcpdto.RegisterResponse{}, nil
}

func (s mcpLiveToolRegistryStub) Heartbeat(context.Context, mcpdto.HeartbeatRequest) (mcpdto.HeartbeatResponse, error) {
	return mcpdto.HeartbeatResponse{}, nil
}

func (s mcpLiveToolRegistryStub) GetInstance(mcpdto.LeaseKey) (contract.ToolInstance, bool) {
	return contract.ToolInstance{}, false
}

func (s mcpLiveToolRegistryStub) ShutdownInstance(context.Context, mcpdto.LeaseKey, mcpdto.ShutdownRequest) error {
	return nil
}

func (s mcpLiveToolRegistryStub) ListInstances() []contract.ToolInstance {
	return append([]contract.ToolInstance(nil), s.instances...)
}

func mcpLiveSortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	slices.Sort(out)
	return out
}
