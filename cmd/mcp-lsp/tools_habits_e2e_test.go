package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	lsptools "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/tools"
	"github.com/stretchr/testify/require"
)

// dummyRegistry embeds manager.Registry to stub GetManagerForFile without implementing all methods.
type dummyRegistry struct {
	manager.Registry
}

func (dummyRegistry) GetManagerForFile(ctx context.Context, filePath string) (manager.Manager, error) {
	return dummyManager{}, nil
}

type dummyManager struct {
	manager.Manager
}

// TestToolsHabitsE2E_Inspect validates that the refactored unified 'pos' parameters
// and plain-text output wrappers (wrapScopedToolResult) match the optimal
// model usage habits by returning clean text and preserved raw GUI content.
func TestToolsHabitsE2E_Inspect(t *testing.T) {
	registerPlainTextRendererForTest(t)

	root := canonicalToolTestRoot(t, t.TempDir())
	filePath := filepath.Join(root, "sample.go")
	content := "package main\n\nfunc main() {\n\t// test target\n}\n"
	require.NoError(t, os.WriteFile(filePath, []byte(content), 0o600))

	// Prepare a call to 'inspect' tool with POS parameter format
	args := map[string]any{
		"action": "definition",
		"pos":    filePath + ":3:6",
	}
	rawArgs, err := json.Marshal(args)
	require.NoError(t, err)

	params, err := json.Marshal(map[string]any{
		"name":            "inspect",
		"arguments":       json.RawMessage(rawArgs),
		"_cwd":            root,
		"_workspaceRoots": []string{root},
	})
	require.NoError(t, err)

	// Create mock handler that returns custom mock data
	locationURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filePath)}).String()
	mockInspectHandler := func(ctx context.Context, args json.RawMessage) (any, error) {
		return []protocol.LocationResult{
			{
				Location: &protocol.Location{
					URI: locationURI,
					Range: protocol.Range{
						Start: protocol.Position{Line: 3, Character: 6},
						End:   protocol.Position{Line: 3, Character: 10},
					},
				},
			},
		}, nil
	}

	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "inspect"},
		Handler:  mockInspectHandler,
	}}

	// Directly invoke handleScopedToolsCall to verify wrapScopedToolResult wrapping
	res, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)

	wrapped, ok := res.(map[string]any)
	require.True(t, ok)

	// 1. Verify LLM-facing format (optimally formatted as plain-text/markdown, not raw JSON!)
	contentList, ok := wrapped["content"].([]map[string]string)
	require.True(t, ok)
	textOutput := contentList[0]["text"]
	require.Contains(t, textOutput, "Locations Found:")
	require.Contains(t, textOutput, filepath.ToSlash(filePath)+":3:6")
	require.NotContains(t, textOutput, `[{"uri":`)

	// 2. Verify GUI-facing structured JSON follows the shared MCP object contract.
	structuredContent, ok := wrapped["structuredContent"].(json.RawMessage)
	require.True(t, ok)
	var structuredLocations struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	err = json.Unmarshal(structuredContent, &structuredLocations)
	require.NoError(t, err)
	require.Equal(t, 1, structuredLocations.Total)
	require.Len(t, structuredLocations.Items, 1)
	require.Equal(t, locationURI, structuredLocations.Items[0]["file"])
}

func TestWrapScopedToolResultWrapsStringStructuredContent(t *testing.T) {
	const fileText = " 1: package main\n"
	root := canonicalToolTestRoot(t, t.TempDir())
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return fileText, nil
		},
	}}

	params, err := json.Marshal(map[string]any{
		"name":            "file",
		"arguments":       map[string]any{},
		"_cwd":            root,
		"_workspaceRoots": []string{root},
	})
	require.NoError(t, err)
	res, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)

	wrapped, ok := res.(map[string]any)
	require.True(t, ok)
	structuredContent, ok := wrapped["structuredContent"].(json.RawMessage)
	require.True(t, ok)
	var payload struct {
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(structuredContent, &payload))
	require.Equal(t, fileText, payload.Value)
}

// TestToolsHabitsE2E_FailFast verifies the fail-fast error behavior
// when the model passes an invalid or malformed pos format.
func TestToolsHabitsE2E_FailFast(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	filePath := filepath.Join(root, "sample.go")
	content := "package main\n"
	require.NoError(t, os.WriteFile(filePath, []byte(content), 0o600))

	// Malformed pos: missing column
	args := map[string]any{
		"action": "definition",
		"pos":    filePath + ":3",
	}
	rawArgs, err := json.Marshal(args)
	require.NoError(t, err)

	params, err := json.Marshal(map[string]any{
		"name":            "inspect",
		"arguments":       json.RawMessage(rawArgs),
		"_cwd":            root,
		"_workspaceRoots": []string{root},
	})
	require.NoError(t, err)

	// Instantiate the real inspect handler to test the real pos parsing and fail fast
	realHandler := lsptools.NewInspectHandler(dummyRegistry{})
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "inspect"},
		Handler:  ToolHandler(realHandler),
	}}

	res, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)

	wrapped, ok := res.(map[string]any)
	require.True(t, ok)

	// Real parse failure inside handleScopedToolsCall should return isError = true
	require.Equal(t, true, wrapped["isError"])
	contentList, ok := wrapped["content"].([]map[string]string)
	require.True(t, ok)
	require.Contains(t, contentList[0]["text"], "invalid pos format")
}
