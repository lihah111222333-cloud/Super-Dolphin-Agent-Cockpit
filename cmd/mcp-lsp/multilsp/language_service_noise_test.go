package multilsp

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

type projectNoiseCase struct {
	languageID string
	targetRel  string
	markerName string
	markerBody string
	body       string
}

func TestProjectAdaptersSkipConfiguredNoiseDirsDuringRootSearch(t *testing.T) {
	registry := NewDefaultLanguageAdapterRegistry()
	for _, noiseDir := range []string{"docs", ".agent", ".workspace", "node_modules", "dist", "coverage"} {
		for _, tc := range projectNoiseCases() {
			t.Run(tc.languageID+"/"+noiseDir, func(t *testing.T) {
				root := canonicalScopePath(t.TempDir(), "")
				writeGenericTestFile(t, filepath.Join(root, noiseDir, tc.markerName), tc.markerBody)
				target := filepath.Join(root, "src", tc.targetRel)
				writeGenericTestFile(t, target, tc.body)

				resolved := resolveProjectNoiseCase(t, registry, root, target, tc.languageID)
				if resolved.WorkspaceRoot != root {
					t.Fatalf("%s workspace root = %q, want repo root %q", tc.languageID, resolved.WorkspaceRoot, root)
				}
				if resolved.RootKind != "dir_fallback" {
					t.Fatalf("%s root kind = %q, want dir_fallback", tc.languageID, resolved.RootKind)
				}
			})
		}
	}
}

func TestProjectAdaptersAllowExplicitTargetsInsideNoiseDirs(t *testing.T) {
	registry := NewDefaultLanguageAdapterRegistry()
	for _, noiseDir := range []string{"docs", ".agent"} {
		for _, tc := range projectNoiseCases() {
			t.Run(tc.languageID+"/"+noiseDir, func(t *testing.T) {
				root := canonicalScopePath(t.TempDir(), "")
				noiseRoot := filepath.Join(root, noiseDir)
				writeGenericTestFile(t, filepath.Join(noiseRoot, tc.markerName), tc.markerBody)
				target := filepath.Join(noiseRoot, "src", tc.targetRel)
				writeGenericTestFile(t, target, tc.body)

				resolved := resolveProjectNoiseCase(t, registry, root, target, tc.languageID)
				if resolved.WorkspaceRoot != noiseRoot {
					t.Fatalf("%s explicit noise target workspace root = %q, want %q", tc.languageID, resolved.WorkspaceRoot, noiseRoot)
				}
				if resolved.RootKind == "dir_fallback" {
					t.Fatalf("%s explicit noise target root kind = dir_fallback, want project marker", tc.languageID)
				}
			})
		}
	}
}

func TestProjectAdapterDoesNotUseUnrelatedNestedMarkerForFileTarget(t *testing.T) {
	registry := NewDefaultLanguageAdapterRegistry()
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "src", "constant.py")
	unrelatedRoot := filepath.Join(root, "unrelated")
	writeGenericTestFile(t, target, "VALUE = 1\n")
	writeGenericTestFile(t, filepath.Join(unrelatedRoot, "pyproject.toml"), "[project]\nname = \"unrelated\"\n")

	resolved := resolveProjectNoiseCase(t, registry, root, target, "python")
	if resolved.WorkspaceRoot != root {
		t.Fatalf("python workspace root = %q, want current workspace root %q, not unrelated project %q", resolved.WorkspaceRoot, root, unrelatedRoot)
	}
	if resolved.RootKind != "dir_fallback" {
		t.Fatalf("python root kind = %q, want dir_fallback", resolved.RootKind)
	}
}

func TestProjectAdapterUsesScopeTargetPathForNestedMarkerDecision(t *testing.T) {
	registry := NewDefaultLanguageAdapterRegistry()
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "src", "constant.py")
	unrelatedRoot := filepath.Join(root, "unrelated")
	writeGenericTestFile(t, target, "VALUE = 1\n")
	writeGenericTestFile(t, filepath.Join(unrelatedRoot, "pyproject.toml"), "[project]\nname = \"unrelated\"\n")

	adapter, ok := registry.AdapterForLanguage("python")
	if !ok {
		t.Fatal("missing python adapter")
	}
	resolved, err := adapter.ResolveRoot(context.Background(), LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: "python",
		TargetPath: target,
	}, "")
	if err != nil {
		t.Fatalf("python ResolveRoot: %v", err)
	}
	if resolved.WorkspaceRoot != root {
		t.Fatalf("python workspace root = %q, want current workspace root %q, not unrelated project %q", resolved.WorkspaceRoot, root, unrelatedRoot)
	}
}

func TestGoRootSkipsNoiseDirsDuringSubmoduleDiscovery(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "docs", "go.mod"), "module example.test/docs\n\ngo 1.25.0\n")
	writeGenericTestFile(t, filepath.Join(root, "docs", "main.go"), "package docs\n")

	info, err := ResolveGoRoot(GoRootRequest{CWD: root, FilePath: root, Env: goEnvForToolchainProbe(t)})
	if err != nil {
		t.Fatalf("ResolveGoRoot: %v", err)
	}
	if info.WorkspaceRoot != root {
		t.Fatalf("Go workspace root = %q, want repo root %q", info.WorkspaceRoot, root)
	}
	if info.RootKind != goRootKindDirFallback {
		t.Fatalf("Go root kind = %q, want %q", info.RootKind, goRootKindDirFallback)
	}
}

func TestGoRootAllowsExplicitTargetsInsideNoiseDirs(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	noiseRoot := filepath.Join(root, "docs")
	target := filepath.Join(noiseRoot, "main.go")
	writeGenericTestFile(t, filepath.Join(noiseRoot, "go.mod"), "module example.test/docs\n\ngo 1.25.0\n")
	writeGenericTestFile(t, target, "package docs\n")

	info, err := ResolveGoRoot(GoRootRequest{CWD: root, FilePath: target, Env: goEnvForToolchainProbe(t)})
	if err != nil {
		t.Fatalf("ResolveGoRoot: %v", err)
	}
	if info.WorkspaceRoot != noiseRoot {
		t.Fatalf("Go explicit noise target workspace root = %q, want %q", info.WorkspaceRoot, noiseRoot)
	}
	if info.RootKind != goRootKindGoMod {
		t.Fatalf("Go explicit noise target root kind = %q, want %q", info.RootKind, goRootKindGoMod)
	}
}

func TestGoInitOptionsExcludeNoiseDirectories(t *testing.T) {
	adapter, ok := NewDefaultLanguageAdapterRegistry().AdapterForLanguage("go")
	if !ok {
		t.Fatal("missing go adapter")
	}
	filters, ok := adapter.InitOptions(ResolvedLanguageScope{})["directoryFilters"].([]string)
	if !ok {
		t.Fatalf("gopls directoryFilters missing from init options")
	}
	for _, want := range []string{"-docs", "-**/docs", "-node_modules", "-**/node_modules", "-dist", "-**/dist", "-coverage", "-**/coverage", "-.agent", "-**/.agent"} {
		if !slices.Contains(filters, want) {
			t.Fatalf("gopls directoryFilters = %#v, missing %q", filters, want)
		}
	}
}

func resolveProjectNoiseCase(t *testing.T, registry *LanguageAdapterRegistry, root, target, languageID string) ResolvedLanguageScope {
	t.Helper()
	adapter, ok := registry.AdapterForLanguage(languageID)
	if !ok {
		t.Fatalf("missing %s adapter", languageID)
	}
	resolved, err := adapter.ResolveRoot(context.Background(), LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: languageID,
		TargetPath: target,
	}, target)
	if err != nil {
		t.Fatalf("%s ResolveRoot: %v", languageID, err)
	}
	return resolved
}

func projectNoiseCases() []projectNoiseCase {
	return []projectNoiseCase{
		{languageID: "typescript", targetRel: "app.ts", markerName: "package.json", markerBody: `{"name":"noise-web"}`, body: "export const value = 1\n"},
		{languageID: "python", targetRel: "app.py", markerName: "pyproject.toml", markerBody: "[project]\nname = \"noise\"\n", body: "value = 1\n"},
		{languageID: "rust", targetRel: "main.rs", markerName: "Cargo.toml", markerBody: "[package]\nname = \"noise\"\nversion = \"0.1.0\"\n", body: "fn main() {}\n"},
		{languageID: "java", targetRel: filepath.Join("main", "Main.java"), markerName: "pom.xml", markerBody: "<project></project>\n", body: "class Main {}\n"},
		{languageID: "css", targetRel: "style.css", markerName: "package.json", markerBody: `{"name":"noise-css"}`, body: ".root { color: black; }\n"},
	}
}
