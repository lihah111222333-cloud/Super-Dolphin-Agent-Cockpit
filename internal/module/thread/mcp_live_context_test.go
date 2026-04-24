package thread

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
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
	}, &platformconfig.Config{ProjectRoot: repoRoot}, mcpLiveToolRegistryStub{instances: []contract.ToolInstance{{BinaryName: "mcp-lsp", ClientKind: "lsp", Status: mcpdto.StatusActive}}})

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
