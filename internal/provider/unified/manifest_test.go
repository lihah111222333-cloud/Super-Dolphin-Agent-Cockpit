package unified_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/manifestbuilder"
	"github.com/stretchr/testify/require"
)

func TestBuildManifest_DefaultFamilies(t *testing.T) {
	binaryDir := filepath.Join(t.TempDir(), "default-bin")
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

func TestBuildManifest_WithIDADoesNotExposeUnimplementedIDATools(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{ThreadCaps: dto.CapabilitySet{"ida": true}})
	if len(got.Binaries) != 2 {
		t.Fatalf("unexpected ida manifest: %+v", got.Binaries)
	}
	for _, binary := range got.Binaries {
		if binary.Name == "ida" {
			t.Fatalf("manifest exposed unimplemented ida binary: %+v", got.Binaries)
		}
	}
}

func TestBuildManifest_BinaryPaths(t *testing.T) {
	binaryDir := filepath.Join(t.TempDir(), "bin")
	got := manifestbuilder.BuildManifest(dto.ManifestContext{BinaryDir: binaryDir})
	want := []string{
		filepath.Join(binaryDir, "mcp-lsp"),
		filepath.Join(binaryDir, "mcp-orch"),
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
	}
	for i, binary := range got.Binaries[:2] {
		if binary.Type != "http" || binary.URL != want[i] {
			t.Fatalf("unexpected proxy binary: %+v", got.Binaries)
		}
		if len(binary.Command) != 0 {
			t.Fatalf("binary %q command = %#v, want nil", binary.Name, binary.Command)
		}
	}
	if len(got.Binaries) != 2 {
		t.Fatalf("unexpected extra binaries under proxy mode: %+v", got.Binaries)
	}
}

func TestBuildManifest_UsesProxyHTTPAuthHeader(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		AgentID:        "agent-1",
		ProxyHTTPAddr:  "127.0.0.1:39001",
		ProxyHTTPToken: "proxy-token",
	})

	for _, binary := range got.Binaries {
		if binary.Type != "http" {
			t.Fatalf("binary %q type = %q, want http", binary.Name, binary.Type)
		}
		require.Equal(t, map[string]string{"Authorization": "Bearer proxy-token"}, binary.Headers)
	}
}

func TestBuildManifest_StdioOnlyIgnoresHTTPDiscovery(t *testing.T) {
	binaryDir := filepath.Join(t.TempDir(), "bin")
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		AgentID:       "agent-1",
		BinaryDir:     binaryDir,
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
		if len(binary.Command) != 1 || !strings.Contains(binary.Command[0], filepath.Join(binaryDir, "mcp-")) {
			t.Fatalf("binary %q command = %#v, want managed stdio command", binary.Name, binary.Command)
		}
	}
}

func TestBuildManifest_UsesPeerHTTPAuthHeader(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		PeerHTTPAddrs: map[dto.ToolFamily]string{
			dto.FamilyLSP:  "127.0.0.1:39002",
			dto.FamilyOrch: "127.0.0.1:39003",
		},
		PeerHTTPTokens: map[dto.ToolFamily]string{
			dto.FamilyLSP: "lsp-token",
		},
	})

	lsp := requireManifestBinary(t, got, "lsp")
	require.Equal(t, "http", lsp.Type)
	require.Equal(t, "http://127.0.0.1:39002/mcp", lsp.URL)
	require.Equal(t, map[string]string{"Authorization": "Bearer lsp-token"}, lsp.Headers)

	orch := requireManifestBinary(t, got, "orch")
	require.Equal(t, "http", orch.Type)
	require.Equal(t, "http://127.0.0.1:39003/mcp", orch.URL)
	require.Empty(t, orch.Headers)
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

func TestBuildManifest_StripsDatabaseEnvironmentFromMCPBinaries(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://tester@127.0.0.1:54320/custom_db?sslmode=disable")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@127.0.0.1:54320/compat_db?sslmode=disable")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", filepath.Join(t.TempDir(), "super-dolphin.db"))
	t.Setenv("SUPER_DOLPHIN_INTERNAL_SQLITE_PATH", filepath.Join(t.TempDir(), "internal.db"))

	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		Env: map[string]string{
			"DATABASE_URL":                       "postgres://ctx@127.0.0.1:54320/custom_db?sslmode=disable",
			"POSTGRES_CONNECTION_STRING":         "postgres://ctx-compat@127.0.0.1:54320/compat_db?sslmode=disable",
			"SUPER_DOLPHIN_SQLITE_PATH":          filepath.Join(t.TempDir(), "ctx.db"),
			"SUPER_DOLPHIN_INTERNAL_SQLITE_PATH": filepath.Join(t.TempDir(), "ctx-internal.db"),
		},
	})
	if len(got.Binaries) == 0 {
		t.Fatal("expected manifest binaries")
	}
	for _, bin := range got.Binaries {
		for _, key := range []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING", "SUPER_DOLPHIN_SQLITE_PATH", "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH"} {
			if _, ok := bin.Env[key]; ok {
				t.Fatalf("binary %q leaked %s in env %#v", bin.Name, key, bin.Env)
			}
		}
	}
}

func TestBuildManifest_InferProjectRootFromBinaryDirForMCPEnv(t *testing.T) {
	t.Setenv("PROJECT_ROOT", "")

	productRoot := t.TempDir()
	binaryDir := filepath.Join(productRoot, "bin")
	require.NoError(t, os.MkdirAll(binaryDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(productRoot, "internal", "platform", "db", "sqlite", "migrations"), 0o755))
	workspaceRoot := t.TempDir()

	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		BinaryDir: binaryDir,
		CWD:       workspaceRoot,
	})

	require.NotEmpty(t, got.Binaries)
	for _, bin := range got.Binaries {
		require.Equal(t, productRoot, bin.Env["PROJECT_ROOT"], "binary %q env = %#v", bin.Name, bin.Env)
		require.NotEqual(t, workspaceRoot, bin.Env["PROJECT_ROOT"], "binary %q should not use workspace cwd as project root", bin.Name)
	}
}

func TestBuildManifest_DoesNotInferProjectRootFromLegacyTopLevelMigrations(t *testing.T) {
	t.Setenv("PROJECT_ROOT", "")

	productRoot := t.TempDir()
	binaryDir := filepath.Join(productRoot, "bin")
	require.NoError(t, os.MkdirAll(binaryDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(productRoot, "migrations"), 0o755))

	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		BinaryDir: binaryDir,
		CWD:       t.TempDir(),
	})

	require.NotEmpty(t, got.Binaries)
	for _, bin := range got.Binaries {
		require.NotContains(t, bin.Env, "PROJECT_ROOT", "binary %q env = %#v", bin.Name, bin.Env)
	}
}

func TestBuildManifest_PreservesExplicitProjectRootEnv(t *testing.T) {
	explicitRoot := t.TempDir()

	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		BinaryDir: filepath.Join(t.TempDir(), "bin"),
		CWD:       t.TempDir(),
		Env:       map[string]string{"PROJECT_ROOT": explicitRoot},
	})

	require.NotEmpty(t, got.Binaries)
	for _, bin := range got.Binaries {
		require.Equal(t, explicitRoot, bin.Env["PROJECT_ROOT"], "binary %q env = %#v", bin.Name, bin.Env)
	}
}

func TestBuildManifest_LSPEnvIncludesPrimaryAndAdditionalWorkspaceRoots(t *testing.T) {
	root := t.TempDir()
	apiRoot := filepath.Join(root, "packages", "api")
	webRoot := filepath.Join(root, "packages", "web")
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		CWD:                          root,
		AdditionalWorkingDirectories: []string{apiRoot, " " + apiRoot + " ", webRoot},
	})
	lsp := requireManifestBinary(t, got, "lsp")
	if lsp.Env["GO_AGENT_LSP_ROOT"] != root {
		t.Fatalf("GO_AGENT_LSP_ROOT = %q, want %q; env=%#v", lsp.Env["GO_AGENT_LSP_ROOT"], root, lsp.Env)
	}
	var roots []string
	if err := json.Unmarshal([]byte(lsp.Env["GO_AGENT_LSP_ROOTS"]), &roots); err != nil {
		t.Fatalf("GO_AGENT_LSP_ROOTS = %q, want JSON array: %v", lsp.Env["GO_AGENT_LSP_ROOTS"], err)
	}
	want := []string{root, apiRoot, webRoot}
	require.Equal(t, want, roots)

	orch := requireManifestBinary(t, got, "orch")
	require.NotContains(t, orch.Env, "GO_AGENT_LSP_ROOT")
	require.NotContains(t, orch.Env, "GO_AGENT_LSP_ROOTS")
}

func TestBuildManifest_RelativeAdditionalWorkspaceRootsResolveAgainstCWD(t *testing.T) {
	root := t.TempDir()
	got := manifestbuilder.BuildManifest(dto.ManifestContext{
		CWD:                          root,
		AdditionalWorkingDirectories: []string{"packages/api"},
	})
	lsp := requireManifestBinary(t, got, "lsp")
	var roots []string
	if err := json.Unmarshal([]byte(lsp.Env["GO_AGENT_LSP_ROOTS"]), &roots); err != nil {
		t.Fatalf("GO_AGENT_LSP_ROOTS = %q, want JSON array: %v", lsp.Env["GO_AGENT_LSP_ROOTS"], err)
	}
	want := []string{root, filepath.Join(root, "packages", "api")}
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
				AdditionalWorkingDirectories: []string{filepath.Join(t.TempDir(), "packages", "api")},
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
