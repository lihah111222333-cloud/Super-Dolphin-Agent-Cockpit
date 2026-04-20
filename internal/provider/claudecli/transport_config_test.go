package claudecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/manifestbuilder"
)

func TestWriteManifestConfigIncludesEnvAndAutoApprove(t *testing.T) {
	t.Parallel()

	manifest := manifestbuilder.BuildManifest(dto.ManifestContext{
		BinaryDir: "/tmp/bin",
		Env:       map[string]string{"CLAUDE_TEST_ENV": "1"},
	})
	manifest.Binaries[0].AutoApprove = []string{"tool.alpha", "tool.beta"}

	path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
	if err != nil {
		t.Fatalf("writeManifestConfig() error = %v", err)
	}
	defer cleanup()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	server, _ := servers[manifest.Binaries[0].Name].(map[string]any)
	env, _ := server["env"].(map[string]any)
	if got := env["CLAUDE_TEST_ENV"]; got != "1" {
		t.Fatalf("server.env = %#v, want CLAUDE_TEST_ENV=1", env)
	}
	autoApprove, _ := server["autoApprove"].([]any)
	if len(autoApprove) != 2 || autoApprove[0] != "tool.alpha" || autoApprove[1] != "tool.beta" {
		t.Fatalf("server.autoApprove = %#v, want ordered tool list", server["autoApprove"])
	}
	if got := server["cwd"]; got != "/tmp/work" {
		t.Fatalf("server.cwd = %#v, want /tmp/work", got)
	}
}

func TestResolvePermissionModeAcceptsLegacyAndNewApprovalPolicies(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		policy string
		want   string
	}{
		{name: "empty", policy: "", want: "bypassPermissions"},
		{name: "never", policy: "never", want: "bypassPermissions"},
		{name: "on-request", policy: "on-request", want: "bypassPermissions"},
		{name: "always", policy: "always", want: "bypassPermissions"},
		{name: "auto", policy: "auto", want: "bypassPermissions"},
		{name: "on-failure", policy: "on-failure", want: "default"},
		{name: "untrusted", policy: "untrusted", want: "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolvePermissionMode(tc.policy, ""); got != tc.want {
				t.Fatalf("resolvePermissionMode(%q, \"\") = %q, want %q", tc.policy, got, tc.want)
			}
		})
	}
}

func TestComposeLaunchSystemPromptUsesPromptAssemblySnapshot(t *testing.T) {
	t.Parallel()

	got := composeLaunchSystemPrompt("", cliLaunchConfig{
		DeveloperInstructions: "legacy developer",
		PromptSnapshot: contract.PromptAssemblySnapshot{
			BaseInstructions:      "assembled base",
			DeveloperInstructions: "assembled developer",
		},
	})
	if got != "assembled base\n\nassembled developer" {
		t.Fatalf("composeLaunchSystemPrompt() = %q", got)
	}
}

func TestClaudeSkillInjectionPortInjectL1ManifestAppendsManifest(t *testing.T) {
	t.Parallel()

	port := NewSkillInjectionPort()
	if got := port.InjectL1Manifest("base instructions", "skills manifest"); got != "base instructions\n\nskills manifest" {
		t.Fatalf("InjectL1Manifest() = %q", got)
	}
}

func TestClaudeSkillInjectionPortBuildTurnSectionUsesLegacySummaryMarkers(t *testing.T) {
	t.Parallel()

	port := NewSkillInjectionPort()
	section, ok := port.BuildTurnSection([]dto.SkillRef{
		{Name: "planner", Mode: dto.SkillModeSummary, Summary: "do planning"},
		{Name: "implementer", Mode: dto.SkillModeFull, Prompt: "full body"},
	})
	if !ok {
		t.Fatal("BuildTurnSection() ok = false, want true")
	}
	if !strings.Contains(section, "[skill:planner]\n摘要: do planning\n使用方式: Call skill_expand_body(\"planner\") for full body") {
		t.Fatalf("BuildTurnSection() = %q, want legacy summary block", section)
	}
	if !strings.Contains(section, "[skill:implementer::full@v1]") {
		t.Fatalf("BuildTurnSection() = %q, want full block", section)
	}
}

func TestClaudeSkillInjectionPortApplyNativeOverridesPrefersGitRoot(t *testing.T) {
	t.Parallel()

	port := NewSkillInjectionPort()
	overridePort := contract.NativeSkillOverridePort(port)

	gitRoot := t.TempDir()
	cwd := t.TempDir()
	for _, tc := range []struct {
		root string
		name string
	}{
		{root: gitRoot, name: "planner"},
		{root: cwd, name: "cwd-only"},
	} {
		path := filepath.Join(tc.root, ".claude", "skills", tc.name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("# skill"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}

	refs := []dto.SkillRef{
		{Name: "planner", Mode: dto.SkillModeFull, Prompt: "custom body", Summary: "custom summary"},
		{Name: "cwd-only", Mode: dto.SkillModeFull, Prompt: "cwd body", Summary: "cwd summary"},
	}
	got := overridePort.ApplyNativeOverrides(refs, gitRoot, cwd)
	if got[0].Mode != dto.SkillModeNone || got[0].Source != dto.SkillSourceNative || got[0].Prompt != "" || got[0].Summary != "" {
		t.Fatalf("planner override = %#v, want native none/body cleared", got[0])
	}
	if got[1].Mode != dto.SkillModeFull || got[1].Prompt != "cwd body" {
		t.Fatalf("cwd fallback should be ignored when gitRoot wins, got %#v", got[1])
	}
	if refs[0].Prompt != "custom body" {
		t.Fatalf("ApplyNativeOverrides mutated input slice: %#v", refs)
	}
}

func TestBuildCLIArgsSplitsBoundaryBlocksIntoRepeatedSystemPrompts(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "", "", cliLaunchConfig{
		DeveloperInstructions: "legacy developer",
		PromptSnapshot: contract.PromptAssemblySnapshot{
			BaseInstructions: "assembled base",
			Boundary: &dto.PromptAssemblyBoundary{
				CachedPrefix: "cached prefix",
				UncachedTail: "uncached tail",
			},
			DeveloperInstructions: "assembled developer",
		},
	})
	if got := flagValues(args, "--system-prompt"); len(got) != 3 ||
		got[0] != "cached prefix" ||
		got[1] != "uncached tail" ||
		got[2] != "assembled developer" {
		t.Fatalf("flagValues(--system-prompt) = %#v, want cached/uncached/developer blocks", got)
	}
}

func TestWriteManifestConfigAcceptsShortFamilyName(t *testing.T) {
	t.Parallel()

	manifest := manifestbuilder.BuildManifest(dto.ManifestContext{BinaryDir: "/tmp/bin"})
	path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
	if err != nil {
		t.Fatalf("writeManifestConfig() error = %v", err)
	}
	defer cleanup()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	// BuildManifest now emits short family names ("lsp", "orch").
	if _, ok := servers["lsp"]; !ok {
		t.Fatalf("mcpServers = %#v, want short family name key \"lsp\"", servers)
	}
	if _, ok := servers["orch"]; !ok {
		t.Fatalf("mcpServers = %#v, want short family name key \"orch\"", servers)
	}
}

func TestClaude_MCP_SmokeTest(t *testing.T) {
	t.Parallel()

	manifest := manifestbuilder.BuildManifest(dto.ManifestContext{BinaryDir: "/tmp/bin"})
	path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
	if err != nil {
		t.Fatalf("writeManifestConfig() error = %v", err)
	}
	defer cleanup()

	args := buildCLIArgs("claude-sonnet", "system", path, cliLaunchConfig{})
	for i := 0; i < len(args)-1; i++ {
		if args[i] != "--mcp-config" {
			continue
		}
		if args[i+1] != path {
			t.Fatalf("--mcp-config path = %q, want %q", args[i+1], path)
		}
		if _, err := os.Stat(args[i+1]); err != nil {
			t.Fatalf("Stat(%q) error = %v", args[i+1], err)
		}
		return
	}
	t.Fatalf("buildCLIArgs() args = %#v, want --mcp-config %q", args, path)
}

func flagValues(args []string, flag string) []string {
	values := make([]string, 0, len(args)/2)
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			values = append(values, args[i+1])
		}
	}
	return values
}
