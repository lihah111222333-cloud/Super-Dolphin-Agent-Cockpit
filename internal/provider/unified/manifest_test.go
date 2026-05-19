package unified_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/manifestbuilder"
	"github.com/stretchr/testify/require"
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
	for i, binary := range got.Binaries[:2] {
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
	for i, binary := range got.Binaries[:2] {
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
	for i, binary := range got.Binaries[:3] {
		if binary.Type != "http" || binary.URL != want[i] {
			t.Fatalf("unexpected proxy binary: %+v", got.Binaries)
		}
		if len(binary.Command) != 0 {
			t.Fatalf("binary %q command = %#v, want nil", binary.Name, binary.Command)
		}
	}
	if len(got.Binaries) != 3 {
		t.Fatalf("unexpected extra binaries under proxy mode: %+v", got.Binaries)
	}
}

func TestBuildManifest_StdioOnlyIgnoresHTTPDiscovery(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		AgentID:       "agent-1",
		BinaryDir:     "/tmp/bin",
		ProxyHTTPAddr: "127.0.0.1:39001",
		PeerHTTPAddrs: map[dto.ToolFamily]string{
			dto.FamilyLSP:  "127.0.0.1:39002",
			dto.FamilyOrch: "127.0.0.1:39003",
		},
		TransportMode: dto.ManifestTransportStdioOnly,
	})

	if len(got.Binaries) != 2 {
		t.Fatalf("binaries = %#v, want lsp/orch stdio entries", got.Binaries)
	}
	for _, binary := range got.Binaries {
		if binary.Type == "http" || binary.URL != "" {
			t.Fatalf("binary %q = %#v, want stdio command despite HTTP discovery", binary.Name, binary)
		}
		if len(binary.Command) != 1 || !strings.Contains(binary.Command[0], "/tmp/bin/mcp-") {
			t.Fatalf("binary %q command = %#v, want managed stdio command", binary.Name, binary.Command)
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
	require.NotEmpty(t, got.Binaries)
	for _, bin := range got.Binaries {
		require.Equal(t, "bar", bin.Env["FOO"], "binary %q env = %#v", bin.Name, bin.Env)
		require.Equal(t, "127.0.0.1:9000", bin.Env["GO_AGENT_CTL_RPC_ADDR"], "binary %q", bin.Name)
		require.Equal(t, "instance-old", bin.Env["GO_AGENT_CTL_INSTANCE_ID"], "binary %q", bin.Name)
		require.Equal(t, "thread-new", bin.Env["GO_AGENT_CTL_THREAD_ID"], "binary %q", bin.Name)
		require.Equal(t, "mcp-lsp", bin.Env["GO_AGENT_CTL_BINARY_NAME"], "binary %q", bin.Name)
		require.Equal(t, "lsp", bin.Env["GO_AGENT_CTL_CLIENT_KIND"], "binary %q", bin.Name)
		require.Equal(t, "token-old", bin.Env["GO_AGENT_CTL_SESSION_TOKEN"], "binary %q", bin.Name)
		require.Equal(t, `{"instance_id":"snap-old"}`, bin.Env["GO_AGENT_CTL_BOOTSTRAP_JSON"], "binary %q", bin.Name)
		for _, legacy := range []string{
			"RPC_ADDR",
			"GO_AGENT_MCP_INSTANCE_ID",
			"GO_AGENT_MCP_THREAD_ID",
			"GO_AGENT_MCP_BINARY_NAME",
			"GO_AGENT_MCP_CLIENT_KIND",
			"GO_AGENT_MCP_SESSION_TOKEN",
			"GO_AGENT_MCP_BOOT_CONTEXT",
		} {
			require.NotContains(t, bin.Env, legacy, "binary %q env = %#v", bin.Name, bin.Env)
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

func TestBuildManifest_LSPEnvIncludesPrimaryAndAdditionalWorkspaceRoots(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		CWD:                          "/repo",
		AdditionalWorkingDirectories: []string{"/repo/packages/api", " /repo/packages/api ", "/repo/packages/web"},
	})
	lsp := requireManifestBinary(t, got, "lsp")
	if lsp.Env["GO_AGENT_LSP_ROOT"] != "/repo" {
		t.Fatalf("GO_AGENT_LSP_ROOT = %q, want /repo; env=%#v", lsp.Env["GO_AGENT_LSP_ROOT"], lsp.Env)
	}
	var roots []string
	if err := json.Unmarshal([]byte(lsp.Env["GO_AGENT_LSP_ROOTS"]), &roots); err != nil {
		t.Fatalf("GO_AGENT_LSP_ROOTS = %q, want JSON array: %v", lsp.Env["GO_AGENT_LSP_ROOTS"], err)
	}
	want := []string{"/repo", "/repo/packages/api", "/repo/packages/web"}
	require.Equal(t, want, roots)

	orch := requireManifestBinary(t, got, "orch")
	require.NotContains(t, orch.Env, "GO_AGENT_LSP_ROOT")
	require.NotContains(t, orch.Env, "GO_AGENT_LSP_ROOTS")
}

func TestBuildManifest_RelativeAdditionalWorkspaceRootsResolveAgainstCWD(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		CWD:                          "/repo",
		AdditionalWorkingDirectories: []string{"packages/api"},
	})
	lsp := requireManifestBinary(t, got, "lsp")
	var roots []string
	if err := json.Unmarshal([]byte(lsp.Env["GO_AGENT_LSP_ROOTS"]), &roots); err != nil {
		t.Fatalf("GO_AGENT_LSP_ROOTS = %q, want JSON array: %v", lsp.Env["GO_AGENT_LSP_ROOTS"], err)
	}
	want := []string{"/repo", "/repo/packages/api"}
	require.Equal(t, want, roots)
}

func TestBuildManifest_DropsRelativeAdditionalWorkspaceRootsWithoutCWD(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		AdditionalWorkingDirectories: []string{"packages/api"},
	})
	lsp := requireManifestBinary(t, got, "lsp")
	require.NotContains(t, lsp.Env, "GO_AGENT_LSP_ROOT")
	require.NotContains(t, lsp.Env, "GO_AGENT_LSP_ROOTS")
}

func TestBuildManifest_DropsAdditionalWorkspaceRootsWithoutTrustedCWD(t *testing.T) {
	for name, cwd := range map[string]string{
		"missing cwd":  "",
		"relative cwd": ".",
	} {
		t.Run(name, func(t *testing.T) {
			got := manifestbuilder.BuildManifest(dto.ManifestContext{
				CWD:                          cwd,
				AdditionalWorkingDirectories: []string{"/repo/packages/api"},
			})
			lsp := requireManifestBinary(t, got, "lsp")
			require.NotContains(t, lsp.Env, "GO_AGENT_LSP_ROOT")
			require.NotContains(t, lsp.Env, "GO_AGENT_LSP_ROOTS")
		})
	}
}

func requireManifestBinary(t *testing.T, manifest dto.MCPManifest, name string) dto.MCPBinary {
	t.Helper()
	for _, binary := range manifest.Binaries {
		if binary.Name == name {
			return binary
		}
	}
	t.Fatalf("manifest missing binary %q: %#v", name, manifest.Binaries)
	return dto.MCPBinary{}
}
