package remoteci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
