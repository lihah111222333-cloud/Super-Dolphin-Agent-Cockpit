package multilsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
)

func TestParseGoWorkModuleRootsCachesByContentAndInvalidatesOnChange(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	mustCreateGoWorkTestDir(t, first)
	mustCreateGoWorkTestDir(t, second)
	goWorkPath := filepath.Join(root, "go.work")
	mustWriteGoWorkTestFile(t, goWorkPath, "first")
	caches := &goResolverCaches{}
	firstRoots, err := parseGoWorkModuleRoots(goWorkPath, []string{"PATH="}, caches)
	if err != nil || len(firstRoots) != 1 || firstRoots[0] != first {
		t.Fatalf("first roots = %#v, err=%v", firstRoots, err)
	}
	secondRoots, err := parseGoWorkModuleRoots(goWorkPath, []string{"PATH="}, caches)
	if err != nil || len(secondRoots) != 1 || secondRoots[0] != first {
		t.Fatalf("cached roots = %#v, err=%v", secondRoots, err)
	}
	mustWriteGoWorkTestFile(t, goWorkPath, "second")
	changedRoots, err := parseGoWorkModuleRoots(goWorkPath, []string{"PATH="}, caches)
	if err != nil || len(changedRoots) != 1 || changedRoots[0] != second {
		t.Fatalf("changed roots = %#v, err=%v", changedRoots, err)
	}
}

func mustCreateGoWorkTestDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWriteGoWorkTestFile(t *testing.T, path, module string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("go 1.25.0\n\nuse ./"+module+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFindJSTSProjectRootWithinFindsFirstValidProject(t *testing.T) {
	root := t.TempDir()

	writeProjectMarker(t, filepath.Join(root, "node_modules", "ignored"), "package.json")
	want := filepath.Join(root, "apps", "web")
	writeProjectMarker(t, want, "tsconfig.json")

	got, err := findJSTSProjectRootWithin(context.Background(), root)
	if err != nil {
		t.Fatalf("findJSTSProjectRootWithin(%q) error = %v", root, err)
	}
	if got != want {
		t.Fatalf("findJSTSProjectRootWithin(%q) = %q, want %q", root, got, want)
	}
}

func TestFindJSTSProjectRootWithinSkipsIgnoredDirectories(t *testing.T) {
	root := t.TempDir()

	writeProjectMarker(t, filepath.Join(root, "node_modules", "ignored"), "package.json")
	writeProjectMarker(t, filepath.Join(root, ".cache", "hidden"), "package.json")
	writeProjectMarker(t, filepath.Join(root, "dist", "site"), "jsconfig.json")

	got, err := findJSTSProjectRootWithin(context.Background(), root)
	if err != nil {
		t.Fatalf("findJSTSProjectRootWithin(%q) error = %v", root, err)
	}
	if got != "" {
		t.Fatalf("findJSTSProjectRootWithin(%q) = %q, want empty result", root, got)
	}
}

func TestFindJSTSProjectRootWithinReturnsWalkError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")

	got, err := findJSTSProjectRootWithin(context.Background(), root)
	if err == nil {
		t.Fatalf("findJSTSProjectRootWithin(%q) error = nil, want walk error", root)
	}
	if got != "" {
		t.Fatalf("findJSTSProjectRootWithin(%q) = %q, want empty result on error", root, got)
	}
}

func TestFindJSTSBootstrapFileWithinFindsFirstValidFile(t *testing.T) {
	root := t.TempDir()

	writeProjectMarker(t, filepath.Join(root, "node_modules", "ignored"), "index.ts")
	want := filepath.Join(root, "apps", "web", "main.ts")
	writeProjectMarker(t, filepath.Join(root, "apps", "web"), "main.ts")

	got, err := findJSTSBootstrapFileWithin(context.Background(), root)
	if err != nil {
		t.Fatalf("findJSTSBootstrapFileWithin(%q) error = %v", root, err)
	}
	if got != want {
		t.Fatalf("findJSTSBootstrapFileWithin(%q) = %q, want %q", root, got, want)
	}
}

func TestFindJSTSBootstrapFileWithinSkipsIgnoredDirectories(t *testing.T) {
	root := t.TempDir()

	writeProjectMarker(t, filepath.Join(root, "node_modules", "ignored"), "index.ts")
	writeProjectMarker(t, filepath.Join(root, ".cache", "hidden"), "main.js")
	writeProjectMarker(t, filepath.Join(root, "dist", "site"), "app.tsx")

	got, err := findJSTSBootstrapFileWithin(context.Background(), root)
	if err != nil {
		t.Fatalf("findJSTSBootstrapFileWithin(%q) error = %v", root, err)
	}
	if got != "" {
		t.Fatalf("findJSTSBootstrapFileWithin(%q) = %q, want empty result", root, got)
	}
}

func TestFindJavaProjectRootFindsMarkerWalkingUp(t *testing.T) {
	root := resolveSymlinks(t, t.TempDir())

	writeProjectMarker(t, root, "pom.xml")
	src := filepath.Join(root, "src", "main", "java", "com", "example")
	writeProjectMarker(t, src, "App.java")

	got, err := findJavaProjectRoot(filepath.Join(src, "App.java"))
	if err != nil {
		t.Fatalf("findJavaProjectRoot: %v", err)
	}
	if got != root {
		t.Fatalf("findJavaProjectRoot = %q, want %q", got, root)
	}
}

func TestFindJavaProjectRootFindsGradleMarker(t *testing.T) {
	root := resolveSymlinks(t, t.TempDir())

	writeProjectMarker(t, root, "build.gradle")
	src := filepath.Join(root, "src", "main", "java")
	writeProjectMarker(t, src, "Main.java")

	got, err := findJavaProjectRoot(filepath.Join(src, "Main.java"))
	if err != nil {
		t.Fatalf("findJavaProjectRoot: %v", err)
	}
	if got != root {
		t.Fatalf("findJavaProjectRoot = %q, want %q", got, root)
	}
}

func TestFindJavaProjectRootWithinFindsFirstProject(t *testing.T) {
	root := t.TempDir()

	writeProjectMarker(t, filepath.Join(root, "target", "ignored"), "pom.xml")
	want := filepath.Join(root, "services", "api")
	writeProjectMarker(t, want, "pom.xml")

	got, err := findJavaProjectRootWithin(context.Background(), root)
	if err != nil {
		t.Fatalf("findJavaProjectRootWithin(%q) error = %v", root, err)
	}
	if got != want {
		t.Fatalf("findJavaProjectRootWithin(%q) = %q, want %q", root, got, want)
	}
}

func TestFindJavaProjectRootWithinSkipsIgnoredDirs(t *testing.T) {
	root := t.TempDir()

	writeProjectMarker(t, filepath.Join(root, "target", "nested"), "pom.xml")
	writeProjectMarker(t, filepath.Join(root, ".gradle", "wrapper"), "build.gradle")
	writeProjectMarker(t, filepath.Join(root, "build", "output"), "pom.xml")

	got, err := findJavaProjectRootWithin(context.Background(), root)
	if err != nil {
		t.Fatalf("findJavaProjectRootWithin(%q) error = %v", root, err)
	}
	if got != "" {
		t.Fatalf("findJavaProjectRootWithin(%q) = %q, want empty result", root, got)
	}
}

func TestFindJavaProjectRootWithinReturnsWalkError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")

	got, err := findJavaProjectRootWithin(context.Background(), root)
	if err == nil {
		t.Fatalf("findJavaProjectRootWithin(%q) error = nil, want walk error", root)
	}
	if got != "" {
		t.Fatalf("findJavaProjectRootWithin(%q) = %q, want empty result on error", root, got)
	}
}

func TestFindJavaBootstrapFileWithinFindsJavaFile(t *testing.T) {
	root := t.TempDir()

	writeProjectMarker(t, filepath.Join(root, "target", "classes"), "App.java")
	want := filepath.Join(root, "src", "main", "java", "App.java")
	writeProjectMarker(t, filepath.Join(root, "src", "main", "java"), "App.java")

	got, err := findJavaBootstrapFileWithin(context.Background(), root)
	if err != nil {
		t.Fatalf("findJavaBootstrapFileWithin(%q) error = %v", root, err)
	}
	if got != want {
		t.Fatalf("findJavaBootstrapFileWithin(%q) = %q, want %q", root, got, want)
	}
}

func TestShouldUseJavaWorkspace(t *testing.T) {
	if !shouldUseJavaWorkspace("java") {
		t.Fatal("shouldUseJavaWorkspace(\"java\") = false, want true")
	}
	if !shouldUseJavaWorkspace("Java") {
		t.Fatal("shouldUseJavaWorkspace(\"Java\") = false, want true")
	}
	if shouldUseJavaWorkspace("go") {
		t.Fatal("shouldUseJavaWorkspace(\"go\") = true, want false")
	}
}

func writeProjectMarker(t *testing.T, dir, marker string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	path := filepath.Join(dir, marker)
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func resolveSymlinks(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := lspplatform.CanonicalDirectoryPath(dir)
	if err != nil {
		t.Fatalf("canonicalize test directory %q: %v", dir, err)
	}
	return resolved
}
