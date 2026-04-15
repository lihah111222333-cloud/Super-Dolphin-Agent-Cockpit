package e2e

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	_ "unsafe"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	_ "github.com/anthropic-ai/super-agent-v3/internal/provider/claudecli"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/manifestbuilder"
)

//go:linkname writeManifestConfig github.com/anthropic-ai/super-agent-v3/internal/provider/claudecli.writeManifestConfig
func writeManifestConfig(manifest dto.MCPManifest, cwd string) (string, func(), error)

type claudeManifestFile struct {
	MCPServers map[string]claudeManifestServer `json:"mcpServers"`
}

type claudeManifestServer struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	AutoApprove []string          `json:"autoApprove,omitempty"`
	CWD         string            `json:"cwd,omitempty"`
}

func TestClaudeMCPManifest_E2E(t *testing.T) {
	manifest := manifestbuilder.BuildManifest(dto.ManifestContext{
		BinaryDir: "/tmp/claude-e2e/bin",
		Env: map[string]string{
			"GO_AGENT_CTL_RPC_ADDR": "127.0.0.1:9191",
			"CLAUDE_TEST_ENV":       "1",
		},
		AutoApprove: []string{"tool.alpha", "tool.beta"},
	})
	manifest.Binaries[0].Command = []string{
		"/tmp/claude-e2e/bin/mcp-lsp",
		"--transport", "stdio",
		"--log-level", "debug",
	}

	path, cleanup, err := writeManifestConfig(manifest, "/tmp/claude-e2e/work")
	if err != nil {
		t.Fatalf("writeManifestConfig() error = %v", err)
	}
	cleaned := false
	t.Cleanup(func() {
		if !cleaned && cleanup != nil {
			cleanup()
		}
	})

	raw, doc := readClaudeManifest(t, path)
	if strings.Contains(string(raw), "env_vars") {
		t.Fatalf("manifest = %s, got env_vars key", raw)
	}
	if _, ok := doc.MCPServers["orch"]; !ok {
		t.Fatalf("mcpServers = %#v, want orch", doc.MCPServers)
	}

	lsp, ok := doc.MCPServers["lsp"]
	if !ok {
		t.Fatalf("mcpServers = %#v, want lsp", doc.MCPServers)
	}
	if lsp.Command != "/tmp/claude-e2e/bin/mcp-lsp" {
		t.Fatalf("lsp.command = %q, want binary path", lsp.Command)
	}
	if !reflect.DeepEqual(lsp.Args, []string{"--transport", "stdio", "--log-level", "debug"}) {
		t.Fatalf("lsp.args = %#v, want split args", lsp.Args)
	}
	// Check that caller-supplied env values are preserved.
	// normalizeManifestEnv may auto-add GO_AGENT_CTL_* from os env,
	// so we check contains rather than exact match.
	if lsp.Env["GO_AGENT_CTL_RPC_ADDR"] != "127.0.0.1:9191" {
		t.Fatalf("lsp.env missing GO_AGENT_CTL_RPC_ADDR, got %#v", lsp.Env)
	}
	if lsp.Env["CLAUDE_TEST_ENV"] != "1" {
		t.Fatalf("lsp.env missing CLAUDE_TEST_ENV, got %#v", lsp.Env)
	}
	if !reflect.DeepEqual(lsp.AutoApprove, []string{"tool.alpha", "tool.beta"}) {
		t.Fatalf("lsp.autoApprove = %#v, want preserved tool list", lsp.AutoApprove)
	}
	if lsp.CWD != "/tmp/claude-e2e/work" {
		t.Fatalf("lsp.cwd = %q, want /tmp/claude-e2e/work", lsp.CWD)
	}

	cleanup()
	cleaned = true
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(%q) error = %v, want removed file", path, err)
	}
}

func TestClaudeMCPManifest_OnlyManagedServers_E2E(t *testing.T) {
	manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{
		{
			Name:    "mcp-lsp",
			Command: []string{"/tmp/claude-e2e/bin/mcp-lsp"},
		},
		{
			Name:    "third-party",
			Command: []string{"/tmp/claude-e2e/bin/third-party"},
		},
	}}

	path, cleanup, err := writeManifestConfig(manifest, "/tmp/claude-e2e/work")
	if err != nil {
		t.Fatalf("writeManifestConfig() error = %v", err)
	}
	defer cleanup()

	_, doc := readClaudeManifest(t, path)
	if len(doc.MCPServers) != 1 {
		t.Fatalf("len(mcpServers) = %d, want 1 managed server", len(doc.MCPServers))
	}
	if _, ok := doc.MCPServers["mcp-lsp"]; !ok {
		t.Fatalf("mcpServers = %#v, want mcp-lsp", doc.MCPServers)
	}
	if _, ok := doc.MCPServers["third-party"]; ok {
		t.Fatalf("mcpServers = %#v, got unmanaged server", doc.MCPServers)
	}
}

func readClaudeManifest(t *testing.T, path string) ([]byte, claudeManifestFile) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var doc claudeManifestFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", path, err)
	}
	return raw, doc
}
