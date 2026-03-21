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
