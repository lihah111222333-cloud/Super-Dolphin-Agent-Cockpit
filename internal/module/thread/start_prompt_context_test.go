package thread

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestBuildStartCtxFallsBackToConfigAndRegistry(t *testing.T) {
	t.Parallel()

	repoRoot, cwd := newPromptGitFixture(t)
	ctx := buildStartCtx(StartRequest{
		Provider: "codex",
		CWD:      cwd,
		Model:    "gpt-5.5",
		Config: map[string]any{
			"language":                     "Chinese",
			"enabledTools":                 []any{"spawn_agent", "request_user_input", "spawn_agent"},
			"additionalWorkingDirectories": []any{filepath.Join(repoRoot, "extra"), " " + filepath.Join(repoRoot, "extra-two") + " "},
			"claudeMdExcludes":             []any{filepath.Join(repoRoot, "**", "CLAUDE.local.md")},
			"sessionFlags":                 map[string]any{"verification_required": true},
			"outputStyleConfig": map[string]any{
				"name":                   "Explanatory",
				"prompt":                 "Explain decisions.",
				"keepCodingInstructions": true,
			},
		},
	}, &contract.Config{ProjectRoot: repoRoot}, promptToolRegistryStub{instances: []contract.ToolInstance{
		{BinaryName: "mcp-lsp", ClientKind: "lsp", Status: mcpdto.StatusActive},
		{BinaryName: "mcp-orch", ClientKind: "orch", Status: mcpdto.StatusActive},
		{BinaryName: "mcp-ida", ClientKind: "ida", Status: mcpdto.StatusDisconnected},
	}})

	if ctx.CWD != cwd {
		t.Fatalf("CWD = %q, want %q", ctx.CWD, cwd)
	}
	if ctx.GitRoot != repoRoot {
		t.Fatalf("GitRoot = %q, want %q", ctx.GitRoot, repoRoot)
	}
	if !ctx.IsWorktree {
		t.Fatal("IsWorktree = false, want true")
	}
	if ctx.Language != "Chinese" {
		t.Fatalf("Language = %q, want Chinese", ctx.Language)
	}
	if got := sortedStrings(ctx.EnabledTools); !slices.Equal(got, []string{"request_user_input", "spawn_agent"}) {
		t.Fatalf("EnabledTools = %#v", ctx.EnabledTools)
	}
	wantDirs := []string{filepath.Join(repoRoot, "extra"), filepath.Join(repoRoot, "extra-two")}
	if got := sortedStrings(ctx.AdditionalWorkingDirectories); !slices.Equal(got, wantDirs) {
		t.Fatalf("AdditionalWorkingDirectories = %#v, want %#v", ctx.AdditionalWorkingDirectories, wantDirs)
	}
	if got := sortedStrings(ctx.ClaudeMdExcludes); !slices.Equal(got, []string{filepath.Join(repoRoot, "**", "CLAUDE.local.md")}) {
		t.Fatalf("ClaudeMdExcludes = %#v", ctx.ClaudeMdExcludes)
	}
	if !ctx.SessionFlags["verification_required"] {
		t.Fatalf("SessionFlags = %#v, want verification_required=true", ctx.SessionFlags)
	}
	if ctx.OutputStyleConfig == nil || ctx.OutputStyleConfig.Name != "Explanatory" {
		t.Fatalf("OutputStyleConfig = %#v, want typed style config", ctx.OutputStyleConfig)
	}
	if ctx.KeepCodingInstructions == nil || !*ctx.KeepCodingInstructions {
		t.Fatalf("KeepCodingInstructions = %#v, want true", ctx.KeepCodingInstructions)
	}
	if got := sortedStrings(ctx.MCPSnapshot.Servers); !slices.Equal(got, []string{"lsp", "orch"}) {
		t.Fatalf("MCPSnapshot.Servers = %#v, want [lsp orch]", ctx.MCPSnapshot.Servers)
	}
}

func TestBuildStartCtxInjectsPersistentSubagentDefaultFromConfig(t *testing.T) {
	t.Parallel()

	ctx := buildStartCtx(StartRequest{
		Config: map[string]any{
			"sessionFlags": map[string]any{"verification_required": true},
		},
	}, &contract.Config{
		Agent: contract.AgentConfig{PersistentSubagentDefault: true},
	}, nil)

	if !ctx.SessionFlags["verification_required"] || !ctx.SessionFlags["persistent_subagent_default"] {
		t.Fatalf("SessionFlags = %#v, want verification_required + persistent_subagent_default", ctx.SessionFlags)
	}
}

func TestBuildStartCtxFiltersSpawnAgentWhenPersistentManagedLaunchEnabled(t *testing.T) {
	t.Parallel()

	ctx := buildStartCtx(StartRequest{
		Config: map[string]any{
			"enabledTools": []any{"spawn_agent", "orchestration_launch_agent", "request_user_input"},
		},
	}, &contract.Config{
		Agent: contract.AgentConfig{PersistentSubagentDefault: true},
	}, nil)

	if got := sortedStrings(ctx.EnabledTools); !slices.Equal(got, []string{"orchestration_launch_agent", "request_user_input"}) {
		t.Fatalf("EnabledTools = %#v, want managed-only child-agent tools", ctx.EnabledTools)
	}
}

func TestBuildStartCtxPreservesExplicitPersistentSubagentOverride(t *testing.T) {
	t.Parallel()

	ctx := buildStartCtx(StartRequest{
		SessionFlags: map[string]bool{"persistent_subagent_default": false},
	}, &contract.Config{
		Agent: contract.AgentConfig{PersistentSubagentDefault: true},
	}, nil)

	value, ok := ctx.SessionFlags["persistent_subagent_default"]
	if !ok || value {
		t.Fatalf("SessionFlags = %#v, want explicit persistent_subagent_default=false to win", ctx.SessionFlags)
	}
}

func TestServiceStartPassesFullPromptAssemblyContext(t *testing.T) {
	t.Parallel()

	repoRoot, cwd := newPromptGitFixture(t)
	assembly := &capturingPromptAssembly{start: contract.StartAssembly{DisplayName: "assembled name"}}
	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
		if req.StartAssembly.DisplayName != "assembled name" {
			t.Fatalf("StartAssembly.DisplayName = %q, want assembled name", req.StartAssembly.DisplayName)
		}
		session := &stubSession{threadID: "provider-thread-ctx"}
		sessions.session = session
		return session, nil
	}}
	orch := &stubThreadOrchestration{}
	svc := NewServiceWithPromptAssembly(
		silentLogger(),
		threads,
		nil,
		sessions,
		starter,
		nil,
		orch,
		nil,
		assembly,
		&contract.Config{ProjectRoot: repoRoot},
		promptToolRegistryStub{instances: []contract.ToolInstance{{BinaryName: "mcp-lsp", ClientKind: "lsp", Status: mcpdto.StatusActive}}},
	).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:          "agent-ctx",
		Provider:         "codex",
		ParentAgentID:    "agent-root",
		AgentType:        "worker",
		AgentMemoryScope: "project",
		Name:             "display name",
		CWD:              cwd,
		Model:            "gpt-5.5",
		Language:         "Chinese",
		EnabledTools:     []string{"lsp_file", "spawn_agent", "lsp_file"},
		SessionFlags:     map[string]bool{"verification_required": true},
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	got := assembly.startInput
	if got.ThreadID != "agent-ctx" {
		t.Fatalf("ThreadID = %q, want agent-ctx", got.ThreadID)
	}
	if got.ParentAgentID != "agent-root" || got.AgentType != "worker" || got.AgentMemoryScope != "project" {
		t.Fatalf("child agent metadata = %#v", got)
	}
	if got.CWD != cwd {
		t.Fatalf("CWD = %q, want %q", got.CWD, cwd)
	}
	if got.GitRoot != repoRoot {
		t.Fatalf("GitRoot = %q, want %q", got.GitRoot, repoRoot)
	}
	if !got.IsWorktree {
		t.Fatal("IsWorktree = false, want true")
	}
	if got.Language != "Chinese" {
		t.Fatalf("Language = %q, want Chinese", got.Language)
	}
	if got := sortedStrings(got.EnabledTools); !slices.Equal(got, []string{"lsp_file", "spawn_agent"}) {
		t.Fatalf("EnabledTools = %#v", assembly.startInput.EnabledTools)
	}
	if !assembly.startInput.SessionFlags["verification_required"] {
		t.Fatalf("SessionFlags = %#v, want verification_required=true", assembly.startInput.SessionFlags)
	}
	if assembly.startInput.OutputStyleConfig != nil {
		t.Fatalf("OutputStyleConfig = %#v, want nil when not configured", assembly.startInput.OutputStyleConfig)
	}
	if got := sortedStrings(assembly.startInput.MCPSnapshot.Servers); !slices.Equal(got, []string{"lsp"}) {
		t.Fatalf("MCPSnapshot.Servers = %#v, want [lsp]", assembly.startInput.MCPSnapshot.Servers)
	}
	if orch.launchReq.ParentID != "agent-root" || orch.launchReq.AgentType != "worker" || orch.launchReq.MemoryScope != "project" {
		t.Fatalf("launch request metadata = %#v", orch.launchReq)
	}
}

func newPromptGitFixture(t *testing.T) (string, string) {
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

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	slices.Sort(out)
	return out
}

type capturingPromptAssembly struct {
	start      contract.StartAssembly
	startInput contract.StartInput
}

func (c *capturingPromptAssembly) AssembleStart(_ context.Context, in contract.StartInput) (contract.StartAssembly, error) {
	c.startInput = in
	return c.start, nil
}

func (*capturingPromptAssembly) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (*capturingPromptAssembly) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (*capturingPromptAssembly) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

type promptToolRegistryStub struct {
	instances []contract.ToolInstance
}

func (s promptToolRegistryStub) Register(context.Context, mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
	return mcpdto.RegisterResponse{}, nil
}

func (s promptToolRegistryStub) Heartbeat(context.Context, mcpdto.HeartbeatRequest) (mcpdto.HeartbeatResponse, error) {
	return mcpdto.HeartbeatResponse{}, nil
}

func (s promptToolRegistryStub) GetInstance(mcpdto.LeaseKey) (contract.ToolInstance, bool) {
	return contract.ToolInstance{}, false
}

func (s promptToolRegistryStub) ShutdownInstance(context.Context, mcpdto.LeaseKey, mcpdto.ShutdownRequest) error {
	return nil
}

func (s promptToolRegistryStub) ListInstances() []contract.ToolInstance {
	return append([]contract.ToolInstance(nil), s.instances...)
}
