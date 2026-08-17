package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	lsptools "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/tools"
	"github.com/stretchr/testify/require"
)

// dummyRegistry embeds manager.Registry to stub GetManagerForFile without implementing all methods.
type dummyRegistry struct {
	manager.Registry
}

func (dummyRegistry) GetManagerForFile(ctx context.Context, filePath string) (manager.Manager, error) {
	return dummyManager{}, nil
}

func (dummyRegistry) GetManagerForFileWithLanguage(ctx context.Context, filePath string, languageID string) (manager.Manager, error) {
	return dummyManager{}, nil
}

type dummyManager struct {
	manager.Manager
}

type patchEditRegistry struct {
	manager.Registry
}

func (patchEditRegistry) GetManagerForFile(context.Context, string) (manager.Manager, error) {
	return nil, manager.ErrUnsupportedLanguage
}

func (patchEditRegistry) GetManagerForFileWithLanguage(context.Context, string, string) (manager.Manager, error) {
	return nil, manager.ErrUnsupportedLanguage
}

func (patchEditRegistry) CurrentDiagnosticGeneration() uint64 { return 0 }

// TestToolsHabitsE2E_Inspect validates that the refactored unified 'pos' parameters
// and plain-text output wrappers (wrapScopedToolResult) match the optimal
// model usage habits by returning one clean text result.
func TestToolsHabitsE2E_Inspect(t *testing.T) {
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

	// Verify the single model-facing text representation.
	contentList, ok := wrapped["content"].([]map[string]string)
	require.True(t, ok)
	textOutput := contentList[0]["text"]
	require.Contains(t, textOutput, "Locations Found:")
	require.Contains(t, textOutput, filepath.ToSlash(filePath)+":3:6")
	require.NotContains(t, textOutput, `[{"uri":`)

	_, hasStructuredContent := wrapped["structuredContent"]
	require.False(t, hasStructuredContent)
}

func TestWrapScopedToolResultReturnsPlainTextContent(t *testing.T) {
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
	_, hasStructuredContent := wrapped["structuredContent"]
	require.False(t, hasStructuredContent)
	contentList, ok := wrapped["content"].([]map[string]string)
	require.True(t, ok)
	require.Equal(t, fileText, contentList[0]["text"])
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

func TestToolsHabitsE2E_PatchEditAcceptsBareEndPatchSentinel(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	filePath := filepath.Join(root, "sample.go")
	require.NoError(t, os.WriteFile(filePath, []byte("package main\nold\n"), 0o600))

	var manifest ToolManifest
	for _, candidate := range newLSPToolManifests() {
		if candidate.Name == "patch_edit" {
			manifest = candidate
			break
		}
	}
	require.NotEmpty(t, manifest.Description)
	props, ok := manifest.Schema["properties"].(map[string]any)
	require.True(t, ok)
	_, ok = props["work_dir"]
	require.True(t, ok)

	newline := string([]byte{10})
	patch := " package main" + newline + "-old" + newline + "+new" + newline + "*** End Patch" + newline + newline
	args := map[string]any{
		"action":    "replace_range",
		"file_path": "sample.go",
		"patch":     patch,
		"work_dir":  root,
	}
	rawArgs, err := json.Marshal(args)
	require.NoError(t, err)
	params, err := json.Marshal(map[string]any{
		"name":            "patch_edit",
		"arguments":       json.RawMessage(rawArgs),
		"_cwd":            root,
		"_workspaceRoots": []string{root},
	})
	require.NoError(t, err)

	realHandler := lsptools.NewEditHandlerWithRoot(root, patchEditRegistry{})
	defs := []toolDefinition{{
		Manifest: manifest,
		Handler:  ToolHandler(realHandler),
	}}
	res, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)
	wrapped, ok := res.(map[string]any)
	require.True(t, ok)
	require.NotEqual(t, true, wrapped["isError"])
	contentList, ok := wrapped["content"].([]map[string]string)
	require.True(t, ok)
	require.Contains(t, contentList[0]["text"], "status=applied")
	updated, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "package main\nnew\n", string(updated))
}

func TestToolsHabitsE2E_PatchEditLegacyBareStarSentinel(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	filePath := filepath.Join(root, "sample.go")
	require.NoError(t, os.WriteFile(filePath, []byte("package main\nold\n"), 0o600))
	manifest := ToolManifest{Name: "patch_edit"}
	for _, candidate := range newLSPToolManifests() {
		if candidate.Name == "patch_edit" {
			manifest = candidate
			break
		}
	}
	patch := " package main\n-old\n+new\n***"
	rawArgs, err := json.Marshal(map[string]any{
		"action":    "replace_range",
		"file_path": "sample.go",
		"patch":     patch,
		"work_dir":  root,
	})
	require.NoError(t, err)
	params, err := json.Marshal(map[string]any{
		"name":            "patch_edit",
		"arguments":       json.RawMessage(rawArgs),
		"_cwd":            root,
		"_workspaceRoots": []string{root},
	})
	require.NoError(t, err)
	defs := []toolDefinition{{Manifest: manifest, Handler: ToolHandler(lsptools.NewEditHandlerWithRoot(root, patchEditRegistry{}))}}
	res, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)
	wrapped, ok := res.(map[string]any)
	require.True(t, ok)
	require.NotEqual(t, true, wrapped["isError"])
	contentList, ok := wrapped["content"].([]map[string]string)
	require.True(t, ok)
	require.Contains(t, contentList[0]["text"], "status=applied")
	updated, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "package main\nnew\n", string(updated))
}

func TestToolsHabitsE2E_PatchEditUnsafeSentinelFailsFast(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	filePath := filepath.Join(root, "sample.go")
	require.NoError(t, os.WriteFile(filePath, []byte("package main\nold\n"), 0o600))
	manifest := ToolManifest{Name: "patch_edit"}
	for _, candidate := range newLSPToolManifests() {
		if candidate.Name == "patch_edit" {
			manifest = candidate
			break
		}
	}
	rawArgs, err := json.Marshal(map[string]any{
		"action":    "replace_range",
		"file_path": "sample.go",
		"patch":     " package main\n*** End Patch\n-old\n+new",
		"work_dir":  root,
	})
	require.NoError(t, err)
	params, err := json.Marshal(map[string]any{
		"name":            "patch_edit",
		"arguments":       json.RawMessage(rawArgs),
		"_cwd":            root,
		"_workspaceRoots": []string{root},
	})
	require.NoError(t, err)
	defs := []toolDefinition{{Manifest: manifest, Handler: ToolHandler(lsptools.NewEditHandlerWithRoot(root, patchEditRegistry{}))}}
	res, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)
	wrapped, ok := res.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, wrapped["isError"])
	contentList, ok := wrapped["content"].([]map[string]string)
	require.True(t, ok)
	require.Contains(t, contentList[0]["text"], "incomplete apply_patch envelope")
}
