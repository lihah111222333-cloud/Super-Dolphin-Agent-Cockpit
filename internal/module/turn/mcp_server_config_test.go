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
