package multilsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestNormalizeLanguageIDMQLAliasesUseCpp(t *testing.T) {
	for _, languageID := range []string{"mql", "mql4", "mql5", "mq4", "mq5", "MQH"} {
		if got := normalizeLanguageID(languageID); got != "cpp" {
			t.Fatalf("normalizeLanguageID(%q) = %q, want cpp", languageID, got)
		}
	}
}

func TestMQLPathDetectionCoversMQL4AndMQL5(t *testing.T) {
	for _, path := range []string{"Experts/legacy.mq4", "Experts/robot.mq5", "Include/common.mqh", "Experts/legacy.mql", "Experts/legacy.mql4", "Experts/legacy.mql5"} {
		if !isMQLPath(path) {
			t.Fatalf("isMQLPath(%q) = false, want true", path)
		}
	}
}

func TestMQLFixtureCompileFlagsSelectCXXForNonStandardExtension(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	fixtureRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "bin", "LSP", "test", "mql"))
	payload, err := os.ReadFile(filepath.Join(fixtureRoot, "compile_flags.txt"))
	if err != nil {
		t.Fatalf("read MQL fixture compile flags: %v", err)
	}
	flags := strings.Fields(string(payload))
	if !slices.Contains(flags, "-x") || !slices.Contains(flags, "c++") {
		t.Fatalf("MQL fixture compile flags = %#v, want explicit -x c++ for .mq5", flags)
	}
}

func TestClangdMQLSourceFailsWithoutCompileTaskAndDoesNotSelectFirstSource(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "Experts", "robot.mq5")
	writeGenericTestFile(t, target, "void OnTick() {}\n")
	writeGenericTestFile(t, filepath.Join(root, "Experts", "other.mq5"), "void OnInit() {}\n")

	_, err := resolveClangdMQLTestRoot(t, root, target)
	if err == nil {
		t.Fatal("ResolveRoot succeeded without compile_flags.txt or compile_commands.json")
	}
	errorText := err.Error()
	for _, forbidden := range []string{root, target, "other.mq5"} {
		if strings.Contains(errorText, forbidden) {
			t.Fatalf("MQL error leaked raw path %q: %s", forbidden, errorText)
		}
	}
	for _, required := range []string{"strategy=no_compile_task", "candidate_count=2", "target_sha256="} {
		if !strings.Contains(errorText, required) {
			t.Fatalf("MQL error = %q, missing %q", errorText, required)
		}
	}
}

func TestClangdMQLSourceUsesExplicitCompileFlags(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "Experts", "robot.mq5")
	writeGenericTestFile(t, target, "void OnTick() {}\n")
	writeGenericTestFile(t, filepath.Join(root, "compile_flags.txt"), "-std=c++20\n")

	resolved, err := resolveClangdMQLTestRoot(t, root, target)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got := resolved.LanguageSpecific[clangdMQLStrategyKey]; got != clangdMQLStrategyCompileFlags {
		t.Fatalf("MQL strategy = %q, want %q", got, clangdMQLStrategyCompileFlags)
	}
	if got := resolved.LanguageSpecific[clangdMQLCandidateCountKey]; got != "1" {
		t.Fatalf("MQL candidate count = %q, want 1", got)
	}
	if policy := clangdTestAdapter(t).BootstrapPolicy(resolved); slices.Contains(policy.FirstSourceExtensions, ".mq5") || slices.Contains(policy.FirstSourceExtensions, ".mqh") {
		t.Fatalf("clangd bootstrap policy selected MQL source: %#v", policy.FirstSourceExtensions)
	}
}

func TestClangdMQLHeaderUsesUniqueSameStemCompileTask(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "Include", "common.mqh")
	source := filepath.Join(root, "Experts", "common.mq5")
	writeGenericTestFile(t, target, "input int Size;\n")
	writeGenericTestFile(t, source, "#include \"common.mqh\"\n")
	writeMQLCompileDatabase(t, root, []map[string]any{{
		"directory": root,
		"file":      source,
		"arguments": []string{"clang++", "-c", source},
	}})

	resolved, err := resolveClangdMQLTestRoot(t, root, target)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got := resolved.LanguageSpecific[clangdMQLStrategyKey]; got != clangdMQLStrategySameStemTask {
		t.Fatalf("MQL header strategy = %q, want %q", got, clangdMQLStrategySameStemTask)
	}
	if got := resolved.LanguageSpecific[clangdMQLCandidateCountKey]; got != "1" {
		t.Fatalf("MQL header candidate count = %q, want 1", got)
	}
}

func TestClangdMQLSourceUsesUniqueSameStemCompileTask(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "Experts", "robot.mq5")
	source := filepath.Join(root, "Experts", "robot.cpp")
	writeGenericTestFile(t, target, "void OnTick() {}\n")
	writeGenericTestFile(t, source, "int main() { return 0; }\n")
	writeMQLCompileDatabase(t, root, []map[string]any{{
		"directory": root,
		"file":      filepath.Join(root, "Experts", "unrelated.cpp"),
		"arguments": []string{"clang++", "-c", "unrelated.cpp"},
	}, {
		"directory": root,
		"file":      source,
		"arguments": []string{"clang++", "-c", source},
	}})

	resolved, err := resolveClangdMQLTestRoot(t, root, target)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got := resolved.LanguageSpecific[clangdMQLStrategyKey]; got != clangdMQLStrategySameStemTask {
		t.Fatalf("MQL source strategy = %q, want %q", got, clangdMQLStrategySameStemTask)
	}
}

func TestClangdMQLAliasesUseUniqueSameStemCompileTask(t *testing.T) {
	cases := []struct {
		name      string
		targetExt string
		sourceExt string
	}{
		{name: "mql_to_mql4", targetExt: ".mql", sourceExt: ".mql4"},
		{name: "mql4_to_mql5", targetExt: ".mql4", sourceExt: ".mql5"},
		{name: "mql5_to_mql", targetExt: ".mql5", sourceExt: ".mql"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := canonicalScopePath(t.TempDir(), "")
			target := filepath.Join(root, "Experts", "robot"+tc.targetExt)
			source := filepath.Join(root, "Experts", "robot"+tc.sourceExt)
			writeGenericTestFile(t, target, "void OnTick() {}\\n")
			writeGenericTestFile(t, source, "int OnInit() { return 0; }\\n")
			writeMQLCompileDatabase(t, root, []map[string]any{{
				"directory": root,
				"file":      source,
				"arguments": []string{"clang++", "-c", source},
			}})

			resolved, err := resolveClangdMQLTestRoot(t, root, target)
			if err != nil {
				t.Fatalf("ResolveRoot: %v", err)
			}
			if got := resolved.LanguageSpecific[clangdMQLStrategyKey]; got != clangdMQLStrategySameStemTask {
				t.Fatalf("MQL alias source strategy = %q, want %q", got, clangdMQLStrategySameStemTask)
			}
		})
	}
}

func TestClangdMQLHeaderRejectsAmbiguousSameStemCompileTasks(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "Include", "common.mqh")
	writeGenericTestFile(t, target, "input int Size;\n")
	writeMQLCompileDatabase(t, root, []map[string]any{{
		"directory": root,
		"file":      filepath.Join(root, "Experts", "common.mq5"),
		"arguments": []string{"clang++", "-c", "common.mq5"},
	}, {
		"directory": root,
		"file":      filepath.Join(root, "Experts", "common.cpp"),
		"arguments": []string{"clang++", "-c", "common.cpp"},
	}})

	_, err := resolveClangdMQLTestRoot(t, root, target)
	if err == nil {
		t.Fatal("ResolveRoot succeeded with two same-stem compile tasks")
	}
	errorText := err.Error()
	if !strings.Contains(errorText, "strategy=ambiguous_candidate") || !strings.Contains(errorText, "candidate_count=2") {
		t.Fatalf("ambiguous MQL error = %q", errorText)
	}
	if strings.Contains(errorText, root) || strings.Contains(errorText, target) {
		t.Fatalf("ambiguous MQL error leaked raw path: %q", errorText)
	}
}

func resolveClangdMQLTestRoot(t *testing.T, root, target string) (ResolvedLanguageScope, error) {
	t.Helper()
	return clangdTestAdapter(t).ResolveRoot(context.Background(), LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: "cpp",
		TargetPath: target,
	}, target)
}

func clangdTestAdapter(t *testing.T) LanguageAdapter {
	t.Helper()
	adapter, ok := NewDefaultLanguageAdapterRegistry().AdapterForLanguage("cpp")
	if !ok {
		t.Fatal("missing clangd cpp adapter")
	}
	return adapter
}

func writeMQLCompileDatabase(t *testing.T, root string, commands []map[string]any) {
	t.Helper()
	payload, err := json.Marshal(commands)
	if err != nil {
		t.Fatalf("marshal compile database: %v", err)
	}
	writeGenericTestFile(t, filepath.Join(root, "compile_commands.json"), string(payload))
}
