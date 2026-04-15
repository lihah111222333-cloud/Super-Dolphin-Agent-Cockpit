package unified_test

import (
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/manifestbuilder"
)

func TestBuildManifest_DefaultFamilies(t *testing.T) {
	binaryDir := "/tmp/default-bin"
	got := manifestbuilder.BuildManifest(dto.ManifestContext{BinaryDir: binaryDir})
	if len(got.Binaries) != 2 || got.Binaries[0].Name != "lsp" || got.Binaries[1].Name != "orch" {
		t.Fatalf("unexpected default manifest: %+v", got.Binaries)
	}
	for _, bin := range got.Binaries {
		if len(bin.Command) == 0 {
			t.Errorf("binary %q has empty Command", bin.Name)
		}
		if len(bin.Command) > 0 && !strings.Contains(bin.Command[0], binaryDir) {
			t.Errorf("binary %q Command should contain BinaryDir", bin.Name)
		}
	}
}

func TestBuildManifest_WithIDA(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{ThreadCaps: dto.CapabilitySet{"ida": true}})
	if len(got.Binaries) != 3 || got.Binaries[2].Name != "ida" {
		t.Fatalf("unexpected ida manifest: %+v", got.Binaries)
	}
}

func TestBuildManifest_BinaryPaths(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{BinaryDir: "/tmp/bin"})
	want := []string{
		filepath.Join("/tmp/bin", "mcp-lsp"),
		filepath.Join("/tmp/bin", "mcp-orch"),
	}
	for i, binary := range got.Binaries {
		if len(binary.Command) != 1 || binary.Command[0] != want[i] {
			t.Fatalf("unexpected binary command: %+v", got.Binaries)
		}
	}
}

func TestBuildManifest_EmptyBinaryDirUsesRelativeCommands(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{})
	want := []string{
		filepath.Join("", "mcp-lsp"),
		filepath.Join("", "mcp-orch"),
	}
	for i, binary := range got.Binaries {
		if len(binary.Command) != 1 || binary.Command[0] != want[i] {
			t.Fatalf("unexpected binary command: %+v", got.Binaries)
		}
	}
}

func TestBuildManifest_UsesProxyHTTPAddr(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		AgentID:       "agent-1",
		ProxyHTTPAddr: "127.0.0.1:39001",
		ThreadCaps:    dto.CapabilitySet{"ida": true},
	})
	want := []string{
		"http://127.0.0.1:39001/mcp/lsp/agent-1",
		"http://127.0.0.1:39001/mcp/orch/agent-1",
		"http://127.0.0.1:39001/mcp/ida/agent-1",
	}
	for i, binary := range got.Binaries {
		if binary.Type != "http" || binary.URL != want[i] {
			t.Fatalf("unexpected proxy binary: %+v", got.Binaries)
		}
		if len(binary.Command) != 0 {
			t.Fatalf("binary %q command = %#v, want nil", binary.Name, binary.Command)
		}
	}
}

func TestBuildManifest_DoesNotInjectAgentIDEnvWhenAgentIDIsSet(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		AgentID:     "agent-42",
		Env:         map[string]string{"FOO": "bar"},
		AutoApprove: []string{"tool.alpha", "tool.beta"},
	})
	if len(got.Binaries) == 0 {
		t.Fatal("expected manifest binaries")
	}
	for _, bin := range got.Binaries {
		if bin.Env["FOO"] != "bar" {
			t.Fatalf("binary %q env = %#v, want propagated env", bin.Name, bin.Env)
		}
		if _, ok := bin.Env["GO_AGENT_MCP_AGENT_ID"]; ok {
			t.Fatalf("binary %q env = %#v, want no GO_AGENT_MCP_AGENT_ID", bin.Name, bin.Env)
		}
		if len(bin.AutoApprove) != 2 || bin.AutoApprove[0] != "tool.alpha" || bin.AutoApprove[1] != "tool.beta" {
			t.Fatalf("binary %q autoApprove = %#v", bin.Name, bin.AutoApprove)
		}
	}
}

func TestBuildManifest_OmitsAgentIDEnvWhenEmpty(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		Env: map[string]string{"FOO": "bar"},
	})
	if len(got.Binaries) == 0 {
		t.Fatal("expected manifest binaries")
	}
	for _, bin := range got.Binaries {
		if _, ok := bin.Env["GO_AGENT_MCP_AGENT_ID"]; ok {
			t.Fatalf("binary %q env = %#v, want no GO_AGENT_MCP_AGENT_ID", bin.Name, bin.Env)
		}
		if bin.Env["FOO"] != "bar" {
			t.Fatalf("binary %q env = %#v, want propagated env", bin.Name, bin.Env)
		}
	}
}

func TestBuildManifest_NormalizesControlEnvNames(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		Env: map[string]string{
			"FOO":                        "bar",
			"RPC_ADDR":                   "127.0.0.1:9000",
			"GO_AGENT_MCP_INSTANCE_ID":   "instance-old",
			"GO_AGENT_CTL_THREAD_ID":     "thread-new",
			"GO_AGENT_MCP_THREAD_ID":     "thread-old",
			"GO_AGENT_MCP_BINARY_NAME":   "mcp-lsp",
			"GO_AGENT_MCP_CLIENT_KIND":   "lsp",
			"GO_AGENT_MCP_SESSION_TOKEN": "token-old",
			"GO_AGENT_MCP_BOOT_CONTEXT":  `{"instance_id":"snap-old"}`,
		},
	})
	if len(got.Binaries) == 0 {
		t.Fatal("expected manifest binaries")
	}
	for _, bin := range got.Binaries {
		if bin.Env["FOO"] != "bar" {
			t.Fatalf("binary %q env = %#v, want propagated env", bin.Name, bin.Env)
		}
		if got := bin.Env["GO_AGENT_CTL_RPC_ADDR"]; got != "127.0.0.1:9000" {
			t.Fatalf("binary %q GO_AGENT_CTL_RPC_ADDR = %q", bin.Name, got)
		}
		if got := bin.Env["GO_AGENT_CTL_INSTANCE_ID"]; got != "instance-old" {
			t.Fatalf("binary %q GO_AGENT_CTL_INSTANCE_ID = %q", bin.Name, got)
		}
		if got := bin.Env["GO_AGENT_CTL_THREAD_ID"]; got != "thread-new" {
			t.Fatalf("binary %q GO_AGENT_CTL_THREAD_ID = %q", bin.Name, got)
		}
		if got := bin.Env["GO_AGENT_CTL_BINARY_NAME"]; got != "mcp-lsp" {
			t.Fatalf("binary %q GO_AGENT_CTL_BINARY_NAME = %q", bin.Name, got)
		}
		if got := bin.Env["GO_AGENT_CTL_CLIENT_KIND"]; got != "lsp" {
			t.Fatalf("binary %q GO_AGENT_CTL_CLIENT_KIND = %q", bin.Name, got)
		}
		if got := bin.Env["GO_AGENT_CTL_SESSION_TOKEN"]; got != "token-old" {
			t.Fatalf("binary %q GO_AGENT_CTL_SESSION_TOKEN = %q", bin.Name, got)
		}
		if got := bin.Env["GO_AGENT_CTL_BOOTSTRAP_JSON"]; got != `{"instance_id":"snap-old"}` {
			t.Fatalf("binary %q GO_AGENT_CTL_BOOTSTRAP_JSON = %q", bin.Name, got)
		}
		for _, legacy := range []string{
			"RPC_ADDR",
			"GO_AGENT_MCP_INSTANCE_ID",
			"GO_AGENT_MCP_THREAD_ID",
			"GO_AGENT_MCP_BINARY_NAME",
			"GO_AGENT_MCP_CLIENT_KIND",
			"GO_AGENT_MCP_SESSION_TOKEN",
			"GO_AGENT_MCP_BOOT_CONTEXT",
		} {
			if _, ok := bin.Env[legacy]; ok {
				t.Fatalf("binary %q env = %#v, want no legacy key %q", bin.Name, bin.Env, legacy)
			}
		}
	}
}

func TestBuildManifest_PreservesDatabaseURLFromEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://tester@127.0.0.1:54320/custom_db?sslmode=disable")

	got := manifestbuilder.BuildManifest(dto.ManifestContext{})
	if len(got.Binaries) == 0 {
		t.Fatal("expected manifest binaries")
	}
	for _, bin := range got.Binaries {
		if gotURL := bin.Env["DATABASE_URL"]; gotURL != "postgres://tester@127.0.0.1:54320/custom_db?sslmode=disable" {
			t.Fatalf("binary %q DATABASE_URL = %q", bin.Name, gotURL)
		}
	}
}
