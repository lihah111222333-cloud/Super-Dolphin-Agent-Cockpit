package turn

import (
	"context"
	"slices"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/stretchr/testify/require"
)

func TestPrepareTurnMergesConfiguredMCPServersIntoAssemblyInput(t *testing.T) {
	t.Parallel()

	assembly := &stubPromptAssemblyService{}
	svc := NewServiceWithPromptAssembly(silentLogger(), assembly).(*service)
	svc.mcpServers = staticTurnMCPServerConfigProvider{servers: map[string]contract.MCPServerConfig{
		"my-search": {
			Transport: "http",
			URL:       "https://your-domain.com/mcp",
			Headers:   map[string]string{"Authorization": "Bearer YOUR_API_KEY"},
		},
	}}
	session := &stubSession{threadID: "thread-1"}

	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt: "search docs",
		CWD:    "/repo",
		MCPSnapshot: contract.MCPSnapshot{
			Servers: []string{"preconfigured"},
			ServerConfigs: map[string]contract.MCPServerConfig{
				"preconfigured": {
					Transport: "http",
					URL:       "https://preconfigured.example/mcp",
				},
			},
		},
	})
	require.NoError(t, err)

	require.True(t, slices.Contains(assembly.lastTurnInput.MCPSnapshot.Servers, "lsp"))
	require.True(t, slices.Contains(assembly.lastTurnInput.MCPSnapshot.Servers, "orch"))
	require.True(t, slices.Contains(assembly.lastTurnInput.MCPSnapshot.Servers, "preconfigured"))
	require.True(t, slices.Contains(assembly.lastTurnInput.MCPSnapshot.Servers, "my-search"))
	require.Equal(t, "https://preconfigured.example/mcp", assembly.lastTurnInput.MCPSnapshot.ServerConfigs["preconfigured"].URL)
	require.Equal(t, "https://your-domain.com/mcp", assembly.lastTurnInput.MCPSnapshot.ServerConfigs["my-search"].URL)
	require.Equal(t, "Bearer YOUR_API_KEY", assembly.lastTurnInput.MCPSnapshot.ServerConfigs["my-search"].Headers["Authorization"])
	requireMCPBinary(t, req.MCP, "my-search")
}

func TestPrepareTurnSkipsDisabledConfiguredMCPServers(t *testing.T) {
	t.Parallel()

	assembly := &stubPromptAssemblyService{}
	svc := NewServiceWithPromptAssembly(silentLogger(), assembly).(*service)
	svc.mcpServers = staticTurnMCPServerConfigProvider{servers: map[string]contract.MCPServerConfig{
		"sqlite": {
			Transport: "stdio",
			Command:   "npx",
			Args:      []string{"-y", "@bytebase/dbhub", "--dsn=sqlite:////tmp/super-dolphin.db"},
			Enabled:   turnBoolPtr(false),
		},
	}}
	session := &stubSession{threadID: "thread-1"}

	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt: "query sqlite",
		CWD:    "/repo",
	})
	require.NoError(t, err)

	require.NotContains(t, assembly.lastTurnInput.MCPSnapshot.Servers, "sqlite")
	require.NotContains(t, assembly.lastTurnInput.MCPSnapshot.ServerConfigs, "sqlite")
	for _, binary := range req.MCP.Binaries {
		require.NotEqual(t, "sqlite", binary.Name)
	}
}

func TestMergeTurnConfiguredMCPServersSkipsActiveServerNames(t *testing.T) {
	t.Parallel()

	got, err := mergeTurnConfiguredMCPServers(contract.MCPSnapshot{
		Servers: []string{"deepwiki"},
	}, map[string]contract.MCPServerConfig{
		"deepwiki": {
			Transport: "http",
			URL:       "https://mcp.deepwiki.com/mcp",
		},
		"my-search": {
			Transport: "http",
			URL:       "https://your-domain.com/mcp",
		},
	})
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"deepwiki", "my-search"}, got.Servers)
	require.NotContains(t, got.ServerConfigs, "deepwiki")
	require.Equal(t, "https://your-domain.com/mcp", got.ServerConfigs["my-search"].URL)
}

func TestMergeTurnConfiguredMCPServersKeepsStdioConfigWhenNameAlreadyPresent(t *testing.T) {
	t.Parallel()

	got, err := mergeTurnConfiguredMCPServers(contract.MCPSnapshot{
		Servers: []string{"sqlite"},
	}, map[string]contract.MCPServerConfig{
		"sqlite": {
			Transport: "stdio",
			Command:   "npx",
			Args:      []string{"-y", "@bytebase/dbhub", "--dsn=sqlite:///tmp/super-dolphin.db"},
		},
	})
	require.NoError(t, err)

	require.Contains(t, got.Servers, "sqlite")
	require.Equal(t, "npx", got.ServerConfigs["sqlite"].Command)
	require.Equal(t, []string{"-y", "@bytebase/dbhub", "--dsn=sqlite:///tmp/super-dolphin.db"}, got.ServerConfigs["sqlite"].Args)
}

type staticTurnMCPServerConfigProvider struct {
	servers map[string]contract.MCPServerConfig
	err     error
}

func (p staticTurnMCPServerConfigProvider) ListMCPServerConfigs(context.Context, string) (map[string]contract.MCPServerConfig, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.servers, nil
}

func requireMCPBinary(t *testing.T, manifest dto.MCPManifest, name string) dto.MCPBinary {
	t.Helper()
	for _, binary := range manifest.Binaries {
		if binary.Name == name {
			return binary
		}
	}
	t.Fatalf("manifest binaries = %#v, want %q", manifest.Binaries, name)
	return dto.MCPBinary{}
}
