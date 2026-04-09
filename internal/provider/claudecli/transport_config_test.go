package claudecli

import (
	"encoding/json"
	"os"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestWriteManifestConfigIncludesEnvAndAutoApprove(t *testing.T) {
	t.Parallel()

	manifest := dto.BuildManifest(dto.ManifestContext{
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

func TestWriteManifestConfigAcceptsShortFamilyName(t *testing.T) {
	t.Parallel()

	manifest := dto.BuildManifest(dto.ManifestContext{BinaryDir: "/tmp/bin"})
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

	manifest := dto.BuildManifest(dto.ManifestContext{BinaryDir: "/tmp/bin"})
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
