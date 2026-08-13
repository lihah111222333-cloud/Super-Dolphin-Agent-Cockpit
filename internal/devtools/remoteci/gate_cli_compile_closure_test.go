package remoteci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

func TestLoadGateCLICompileClosureTracksOnlyGateCompileInputs(t *testing.T) {
	repo := newGateCLICompileClosureRepository(t)
	baseTree := commitGateCLICompileClosureTree(t, repo, "base")
	baseSource, baseToolchain, baseEntries, err := LoadGateCLICompileClosure(context.Background(), repo, baseTree)
	if err != nil {
		t.Fatalf("load base compile closure: %v", err)
	}
	if len(baseEntries) != 5 {
		t.Fatalf("compile closure entries = %d, want 5", len(baseEntries))
	}

	writeGateCLICompileClosureFile(t, repo, "internal/ordinary.go", "package internal\nconst Ordinary = \"changed\"\n")
	ordinaryTree := commitGateCLICompileClosureTree(t, repo, "ordinary source change")
	ordinarySource, ordinaryToolchain, _, err := LoadGateCLICompileClosure(context.Background(), repo, ordinaryTree)
	if err != nil {
		t.Fatalf("load ordinary-change compile closure: %v", err)
	}
	if ordinarySource != baseSource || ordinaryToolchain != baseToolchain {
		t.Fatalf("ordinary source changed compile closure: source %q/%q toolchain %q/%q", ordinarySource, baseSource, ordinaryToolchain, baseToolchain)
	}

	writeGateCLICompileClosureFile(t, repo, "internal/gateimpl/gateimpl.go", "package gateimpl\n\nconst Value = \"changed\"\n\nfunc Run() { println(Value) }\n")
	importedTree := commitGateCLICompileClosureTree(t, repo, "imported source change")
	importedSource, importedToolchain, _, err := LoadGateCLICompileClosure(context.Background(), repo, importedTree)
	if err != nil {
		t.Fatalf("load imported-source compile closure: %v", err)
	}
	if importedSource == baseSource {
		t.Fatal("transitively imported source change did not change compile closure digest")
	}
	if importedToolchain != baseToolchain {
		t.Fatalf("imported source changed toolchain digest: %q != %q", importedToolchain, baseToolchain)
	}

	writeGateCLICompileClosureFile(t, repo, "cmd/super-dolphin-gate/main.go", "package main\nfunc main() { println(\"changed\") }\n")
	cliTree := commitGateCLICompileClosureTree(t, repo, "CLI source change")
	cliSource, cliToolchain, _, err := LoadGateCLICompileClosure(context.Background(), repo, cliTree)
	if err != nil {
		t.Fatalf("load CLI-change compile closure: %v", err)
	}
	if cliSource == baseSource {
		t.Fatal("CLI source change did not change compile closure digest")
	}
	if cliToolchain != baseToolchain {
		t.Fatalf("CLI source change changed toolchain digest: %q != %q", cliToolchain, baseToolchain)
	}
}

func TestLoadTrustedGateLauncherCompileClosureTracksEmbeddedAssets(t *testing.T) {
	repo := newGateCLICompileClosureRepository(t)
	writeGateCLICompileClosureFile(t, repo, "internal/gateimpl/gateimpl.go", `package gateimpl

import _ "embed"

//go:embed assets/value.txt
var Value string

func Run() { println(Value) }
`)
	writeGateCLICompileClosureFile(t, repo, "internal/gateimpl/assets/value.txt", "base\n")
	embeddedTree := commitGateCLICompileClosureTree(t, repo, "embedded asset base")
	stableSource, stableToolchain, _, err := LoadGateCLICompileClosure(context.Background(), repo, embeddedTree)
	if err != nil {
		t.Fatalf("load stable embedded compile closure: %v", err)
	}
	embeddedSource, embeddedToolchain, embeddedEntries, err := LoadTrustedGateLauncherCompileClosure(context.Background(), repo, embeddedTree)
	if err != nil {
		t.Fatalf("load trusted launcher embedded compile closure: %v", err)
	}
	if embeddedSource == stableSource {
		t.Fatal("trusted launcher compile closure did not add the embedded asset")
	}
	if !gateCLICompileClosureContainsPath(embeddedEntries, "internal/gateimpl/assets/value.txt") {
		t.Fatal("embedded asset is absent from trusted launcher compile closure")
	}

	writeGateCLICompileClosureFile(t, repo, "internal/gateimpl/assets/value.txt", "changed\n")
	changedTree := commitGateCLICompileClosureTree(t, repo, "embedded asset change")
	changedStableSource, changedStableToolchain, _, err := LoadGateCLICompileClosure(context.Background(), repo, changedTree)
	if err != nil {
		t.Fatalf("load changed stable compile closure: %v", err)
	}
	assertGateCLICompileClosureIdentity(t, changedStableSource, changedStableToolchain, stableSource, stableToolchain, "embedded asset changed cross-generation closure")
	changedSource, changedToolchain, _, err := LoadTrustedGateLauncherCompileClosure(context.Background(), repo, changedTree)
	if err != nil {
		t.Fatalf("load changed trusted launcher compile closure: %v", err)
	}
	if changedSource == embeddedSource {
		t.Fatal("embedded asset change did not change trusted launcher compile closure digest")
	}
	if changedToolchain != embeddedToolchain {
		t.Fatalf("embedded asset changed toolchain digest: %q != %q", changedToolchain, embeddedToolchain)
	}

	writeGateCLICompileClosureFile(t, repo, "internal/gateimpl/assets/unreferenced.txt", "unreferenced\n")
	unreferencedTree := commitGateCLICompileClosureTree(t, repo, "unreferenced asset change")
	unreferencedSource, unreferencedToolchain, _, err := LoadTrustedGateLauncherCompileClosure(context.Background(), repo, unreferencedTree)
	if err != nil {
		t.Fatalf("load unreferenced-asset compile closure: %v", err)
	}
	assertGateCLICompileClosureIdentity(t, unreferencedSource, unreferencedToolchain, changedSource, changedToolchain, "unreferenced asset changed compile closure")
}

func gateCLICompileClosureContainsPath(entries []sourceexport.TreeEntry, want string) bool {
	return slices.ContainsFunc(entries, func(entry sourceexport.TreeEntry) bool {
		return entry.Path == want
	})
}

func assertGateCLICompileClosureIdentity(t *testing.T, source, toolchain, wantSource, wantToolchain, message string) {
	t.Helper()
	if source != wantSource || toolchain != wantToolchain {
		t.Fatalf("%s: source %q/%q toolchain %q/%q", message, source, wantSource, toolchain, wantToolchain)
	}
}

func TestLoadTrustedGateLauncherCompileClosureRejectsUnmatchedEmbeddedAssets(t *testing.T) {
	repo := newGateCLICompileClosureRepository(t)
	writeGateCLICompileClosureFile(t, repo, "internal/gateimpl/gateimpl.go", `package gateimpl

import _ "embed"

//go:embed missing/*
var Value string

func Run() { println(Value) }
`)
	tree := commitGateCLICompileClosureTree(t, repo, "unmatched embedded asset")
	_, _, _, err := LoadTrustedGateLauncherCompileClosure(context.Background(), repo, tree)
	if err == nil || !strings.Contains(err.Error(), "go:embed pattern") {
		t.Fatalf("unmatched embedded asset error = %v, want go:embed pattern", err)
	}
}

func TestLoadGateCLICompileClosureRejectsMissingAndMaliciousInputs(t *testing.T) {
	t.Run("missing compile input", func(t *testing.T) {
		repo := newGateCLICompileClosureRepository(t)
		if err := os.Remove(filepath.Join(repo, "go.sum")); err != nil {
			t.Fatalf("remove compile input: %v", err)
		}
		tree := commitGateCLICompileClosureTree(t, repo, "missing go sum")
		_, _, _, err := LoadGateCLICompileClosure(context.Background(), repo, tree)
		if err == nil || !strings.Contains(err.Error(), "go.sum") {
			t.Fatalf("missing compile input error = %v, want go.sum", err)
		}
	})

	t.Run("malicious manifest path cannot expand closure", func(t *testing.T) {
		repo := newGateCLICompileClosureRepository(t)
		baseTree := commitGateCLICompileClosureTree(t, repo, "base")
		baseSource, _, _, err := LoadGateCLICompileClosure(context.Background(), repo, baseTree)
		if err != nil {
			t.Fatalf("load base compile closure: %v", err)
		}
		writeGateCLICompileClosureFile(t, repo, "build/gate/inputs.json", `{
  "schema_version": "2",
  "dockerfile": "build/gate/Dockerfile",
  "inputs": ["build/gate/Dockerfile", "build/gate/inputs.json", "build/gate/toolchain.lock", "cmd/super-dolphin-gate/main.go", "go.mod", "go.sum"],
  "gate_compile_inputs": ["../escape.go", "cmd/super-dolphin-gate/main.go", "go.mod", "go.sum"]
}`+"\n")
		tree := commitGateCLICompileClosureTree(t, repo, "malicious manifest")
		got, _, _, err := LoadGateCLICompileClosure(context.Background(), repo, tree)
		if err != nil {
			t.Fatalf("load compile closure with ignored manifest: %v", err)
		}
		if got != baseSource {
			t.Fatalf("manifest-only change altered compile closure: %q != %q", got, baseSource)
		}
	})
}

func TestLoadGateCLICompileClosureRejectsExternalLocalGoReplacements(t *testing.T) {
	tests := []struct {
		name        string
		replacement string
	}{
		{name: "absolute", replacement: "/tmp/super-dolphin-external"},
		{name: "home relative", replacement: "~/super-dolphin-external"},
		{name: "parent relative", replacement: "../super-dolphin-external"},
		{name: "nested parent relative", replacement: "./../../super-dolphin-external"},
		{name: "windows absolute", replacement: `C:\\super-dolphin-external`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newGateCLICompileClosureRepository(t)
			writeGateCLICompileClosureFile(t, repo, "go.mod", "module example.invalid/gate\n\ngo 1.24.0\n\nreplace example.invalid/replaced => "+test.replacement+"\n")
			tree := commitGateCLICompileClosureTree(t, repo, "external local replacement")
			_, _, _, err := LoadGateCLICompileClosure(context.Background(), repo, tree)
			if err == nil {
				t.Fatalf("external replacement %q was accepted", test.replacement)
			}
		})
	}
}

func TestLocalRemoteGoModuleMappingRejectsExternalFilesystemPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "absolute", path: "/tmp/super-dolphin-external"},
		{name: "home relative", path: "~/super-dolphin-external"},
		{name: "parent relative", path: "../super-dolphin-external"},
		{name: "nested parent relative", path: "./../../super-dolphin-external"},
		{name: "windows absolute", path: `C:\\super-dolphin-external`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapping, ok, err := localRemoteGoModuleMapping(&modfile.Replace{
				Old: module.Version{Path: "example.invalid/replaced"},
				New: module.Version{Path: test.path},
			})
			if err == nil || ok || mapping != (remoteGoModuleMapping{}) {
				t.Fatalf("localRemoteGoModuleMapping(%q) = (%#v, %t, %v), want fail-fast", test.path, mapping, ok, err)
			}
		})
	}
}

func TestLocalRemoteGoModuleMappingRetainsVersionedModuleReplacement(t *testing.T) {
	mapping, ok, err := localRemoteGoModuleMapping(&modfile.Replace{
		Old: module.Version{Path: "example.invalid/replaced"},
		New: module.Version{Path: "example.invalid/new", Version: "v1.2.3"},
	})
	if err != nil || ok || mapping != (remoteGoModuleMapping{}) {
		t.Fatalf("versioned module replacement = (%#v, %t, %v), want ignored without error", mapping, ok, err)
	}
}

func TestLocalRemoteGoModuleMappingCanonicalizesRepositoryRelativePath(t *testing.T) {
	mapping, ok, err := localRemoteGoModuleMapping(&modfile.Replace{
		Old: module.Version{Path: "example.invalid/replaced"},
		New: module.Version{Path: `..\\internal\\replaced`},
	})
	if err == nil || ok || mapping != (remoteGoModuleMapping{}) {
		t.Fatalf("escaping repository-relative path = (%#v, %t, %v), want fail-fast", mapping, ok, err)
	}
	mapping, ok, err = localRemoteGoModuleMapping(&modfile.Replace{
		Old: module.Version{Path: "example.invalid/replaced"},
		New: module.Version{Path: `.\\internal\\replaced`},
	})
	if err != nil || !ok || mapping.directory != "internal/replaced" {
		t.Fatalf("repository-relative path = (%#v, %t, %v), want internal/replaced mapping", mapping, ok, err)
	}
}

func TestLoadGateCLICompileClosureRetainsVersionedModuleReplacement(t *testing.T) {
	repo := newGateCLICompileClosureRepository(t)
	writeGateCLICompileClosureFile(t, repo, "go.mod", "module example.invalid/gate\n\ngo 1.24.0\n\nreplace example.invalid/replaced => example.invalid/new v1.2.3\n")
	tree := commitGateCLICompileClosureTree(t, repo, "versioned module replacement")
	_, _, entries, err := LoadGateCLICompileClosure(context.Background(), repo, tree)
	if err != nil {
		t.Fatalf("versioned module replacement was rejected: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("versioned module replacement changed compile closure entries = %d, want 5", len(entries))
	}
}

func TestLoadGateCLICompileClosureRejectsImportedRepositoryLocalReplacement(t *testing.T) {
	repo := newGateCLICompileClosureRepository(t)
	writeGateCLICompileClosureFile(t, repo, "go.mod", "module example.invalid/gate\n\ngo 1.24.0\n\nreplace example.invalid/replaced => ./internal/replaced\n")
	writeGateCLICompileClosureFile(t, repo, "cmd/super-dolphin-gate/main.go", "package main\n\nimport \"example.invalid/replaced/sub\"\n\nfunc main() { sub.Run() }\n")
	writeGateCLICompileClosureFile(t, repo, "internal/replaced/go.mod", "module example.invalid/replaced\n\ngo 1.24.0\n")
	writeGateCLICompileClosureFile(t, repo, "internal/replaced/go.sum", "example.invalid/dependency v1.0.0 h1:abc\n")
	writeGateCLICompileClosureFile(t, repo, "internal/replaced/sub/sub.go", "package sub\n\nfunc Run() {}\n")
	tree := commitGateCLICompileClosureTree(t, repo, "repository local replacement")
	_, _, _, err := LoadGateCLICompileClosure(context.Background(), repo, tree)
	if err == nil || !strings.Contains(err.Error(), "repository-local replacement module") {
		t.Fatalf("imported repository local replacement error = %v, want fail-fast", err)
	}
}

func TestLoadGateCLICompileClosureKeepsUnreachableLocalReplacementMetadataOutsideStableProtocol(t *testing.T) {
	repo := newGateCLICompileClosureRepository(t)
	writeGateCLICompileClosureFile(t, repo, "go.mod", "module example.invalid/gate\n\ngo 1.24.0\n\nreplace example.invalid/replaced => ./internal/replaced\n")
	writeGateCLICompileClosureFile(t, repo, "internal/replaced/go.mod", "module example.invalid/replaced\n\ngo 1.24.0\n")
	writeGateCLICompileClosureFile(t, repo, "internal/replaced/go.sum", "example.invalid/dependency v1.0.0 h1:abc\n")
	baseTree := commitGateCLICompileClosureTree(t, repo, "unreachable repository local replacement")
	baseSource, _, _, err := LoadGateCLICompileClosure(context.Background(), repo, baseTree)
	if err != nil {
		t.Fatalf("load gate closure with unreachable local replacement: %v", err)
	}
	writeGateCLICompileClosureFile(t, repo, "internal/replaced/go.mod", "module example.invalid/replaced\n\ngo 1.25.0\n")
	changedTree := commitGateCLICompileClosureTree(t, repo, "unreachable local replacement metadata change")
	changedSource, _, _, err := LoadGateCLICompileClosure(context.Background(), repo, changedTree)
	if err != nil {
		t.Fatalf("load changed gate closure with unreachable local replacement: %v", err)
	}
	if changedSource != baseSource {
		t.Fatalf("unreachable local replacement metadata altered stable gate closure: %q != %q", changedSource, baseSource)
	}
}

func newGateCLICompileClosureRepository(t *testing.T) string {
	t.Helper()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve repository path: %v", err)
	}
	runGateCLICompileClosureGit(t, repo, "init")
	runGateCLICompileClosureGit(t, repo, "config", "user.email", "test@example.invalid")
	runGateCLICompileClosureGit(t, repo, "config", "user.name", "Local CI Test")
	for path, data := range map[string]string{
		"build/gate/inputs.json": `{
  "schema_version": "2",
  "dockerfile": "build/gate/Dockerfile",
  "inputs": ["build/gate/Dockerfile", "build/gate/inputs.json", "build/gate/toolchain.lock", "cmd/super-dolphin-gate/main.go", "go.mod", "go.sum"],
  "gate_compile_inputs": ["cmd/super-dolphin-gate/main.go", "go.mod", "go.sum"]
}` + "\n",
		"build/gate/Dockerfile":          "FROM scratch\n",
		toolchainLockPath:                "go=1.24.0\n",
		"cmd/super-dolphin-gate/main.go": "package main\n\nimport \"example.invalid/gate/internal/gateimpl\"\n\nfunc main() { gateimpl.Run() }\n",
		"go.mod":                         "module example.invalid/gate\n\ngo 1.24.0\n",
		"go.sum":                         "example.invalid/dependency v1.0.0 h1:abc\n",
		"internal/gateimpl/gateimpl.go":  "package gateimpl\n\nconst Value = \"base\"\n\nfunc Run() { println(Value) }\n",
		"internal/ordinary.go":           "package internal\nconst Ordinary = \"base\"\n",
	} {
		writeGateCLICompileClosureFile(t, repo, path, data)
	}
	return repo
}

func commitGateCLICompileClosureTree(t *testing.T, repo string, message string) string {
	t.Helper()
	runGateCLICompileClosureGit(t, repo, "add", ".")
	runGateCLICompileClosureGit(t, repo, "commit", "-m", message)
	return strings.TrimSpace(runGateCLICompileClosureGit(t, repo, "rev-parse", "HEAD^{tree}"))
}

func writeGateCLICompileClosureFile(t *testing.T, repo string, name string, data string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("make directory for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func runGateCLICompileClosureGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
