package nested

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestNestedParseFrontmatterPathsAndMatchTargetPath(t *testing.T) {
	frontmatter := "paths:\n  - src/**/*.go\n  - pkg/*.md\nname: Rule\n"
	if got, want := parseFrontmatterPaths(frontmatter), []string{"src/**/*.go", "pkg/*.md"}; !slices.Equal(got, want) {
		t.Fatalf("parseFrontmatterPaths() = %#v, want %#v", got, want)
	}
	if !MatchTargetPath("/repo/src/app/main.go", []string{"src/**/*.go"}, "/repo") {
		t.Fatal("MatchTargetPath(src/**/*.go) = false, want true")
	}
	if MatchTargetPath("/repo/docs/readme.md", []string{"src/**/*.go"}, "/repo") {
		t.Fatal("MatchTargetPath(non-match) = true, want false")
	}
}

func TestNestedResolveConditionalDelta(t *testing.T) {
	repoRoot := t.TempDir()
	cwd := filepath.Join(repoRoot, "service")
	targetDir := filepath.Join(cwd, "nested")
	for _, dir := range []string{cwd, targetDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	writeClaudeFile(t, filepath.Join(repoRoot, "CLAUDE.md"), "repo base")
	writeClaudeFile(t, filepath.Join(cwd, "CLAUDE.md"), "cwd base")
	writeClaudeFile(t, filepath.Join(cwd, ".claude", "rules", "parent.md"), "---\npaths:\n  - nested/**/*.go\n---\nparent conditional")
	writeClaudeFile(t, filepath.Join(targetDir, "CLAUDE.md"), "nested base")
	writeClaudeFile(t, filepath.Join(targetDir, ".claude", "rules", "child.md"), "---\npaths: [\"main.go\"]\n---\nchild conditional")
	target := filepath.Join(targetDir, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", target, err)
	}
	buildCtx := contract.BuildCtx{GitRoot: repoRoot, CWD: cwd}
	deps := newTestDependencies(testDepsOptions{})
	baseSources := mustResolveClaudeMdSources(t, ClaudeMdResolveConfig{BuildCtx: buildCtx, Dependencies: deps})
	gotSources, err := resolveNestedConditionalSources(context.Background(), buildCtx, deps, target, baseSources)
	if err != nil {
		t.Fatalf("resolveNestedConditionalSources() error = %v", err)
	}
	got := sourcePaths(gotSources)
	want := []string{
		mustResolvedClaudePath(t, filepath.Join(cwd, ".claude", "rules", "parent.md")),
		mustResolvedClaudePath(t, filepath.Join(targetDir, ".claude", "rules", "child.md")),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("resolveNestedConditionalSources() = %#v, want %#v", got, want)
	}
	for _, source := range gotSources {
		if !source.Conditional {
			t.Fatalf("source = %#v, want all nested results conditional", source)
		}
	}
	unwanted := mustResolvedClaudePath(t, filepath.Join(targetDir, "CLAUDE.md"))
	if slices.Contains(got, unwanted) {
		t.Fatalf("resolveNestedConditionalSources() included %q, want conditional delta only", unwanted)
	}
}
