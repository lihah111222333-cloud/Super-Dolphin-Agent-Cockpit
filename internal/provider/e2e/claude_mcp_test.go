package e2e

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	_ "unsafe"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	_ "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/claudecli"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/manifestbuilder"
)

//go:linkname writeManifestConfig github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/claudecli.writeManifestConfig
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
	manifest := claudeMCPManifestFixture()
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
	assertClaudeMCPManifest(t, raw, doc)
	cleanup()
	cleaned = true
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(%q) error = %v, want removed file", path, err)
	}
}

func claudeMCPManifestFixture() dto.MCPManifest {
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
	}
	return manifest
}

func assertClaudeMCPManifest(t *testing.T, raw []byte, doc claudeManifestFile) {
	t.Helper()
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
	if len(lsp.Args) != 0 {
		t.Fatalf("lsp.args = %#v, want no args for managed sidecar", lsp.Args)
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
}

func TestClaudeMCPManifest_RejectsUnmanagedStdioServer_E2E(t *testing.T) {
	manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{
		{
			Name:    "lsp",
			Command: []string{"/tmp/claude-e2e/bin/mcp-lsp"},
		},
		{
			Name:    "third-party",
			Command: []string{"/tmp/claude-e2e/bin/mcp-evil"},
		},
	}}

	path, cleanup, err := writeManifestConfig(manifest, "/tmp/claude-e2e/work")
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("writeManifestConfig() = (%q, cleanup, nil), want unmanaged stdio server rejection", path)
	}
	if !strings.Contains(err.Error(), `rejected mcp manifest server "third-party"`) {
		t.Fatalf("writeManifestConfig() error = %v, want third-party rejection", err)
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
