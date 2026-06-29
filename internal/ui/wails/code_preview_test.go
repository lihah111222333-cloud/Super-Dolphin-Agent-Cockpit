package wails

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSaveScopedFileWritesWithinScope(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "docs", "readme.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile(old) error = %v", err)
	}

	result, err := saveScopedFile(target, "# Saved\n\nBody", []string{root}, false)
	if err != nil {
		t.Fatalf("saveScopedFile() error = %v", err)
	}
	if !result.Ok {
		t.Fatal("saveScopedFile() ok = false, want true")
	}
	if result.Relative != "docs/readme.md" {
		t.Fatalf("saveScopedFile() relative = %q, want %q", result.Relative, "docs/readme.md")
	}
	if result.TotalLines != 3 {
		t.Fatalf("saveScopedFile() totalLines = %d, want 3", result.TotalLines)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "# Saved\n\nBody" {
		t.Fatalf("saveScopedFile() body = %q, want normalized markdown", string(data))
	}
}

func TestSaveScopedFileRejectsOutsideScope(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.txt")

	_, err := saveScopedFile(outside, "nope", []string{root}, false)
	if err == nil {
		t.Fatal("saveScopedFile() error = nil, want scope error")
	}
}

func TestSaveScopedFileRejectsMissingFileWithoutCreateNew(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "docs", "new.md")

	_, err := saveScopedFile(target, "body", []string{root}, false)
	if err == nil {
		t.Fatal("saveScopedFile() error = nil, want missing-file validation error")
	}
	if !errors.Is(err, errCodeSaveFileMustExist) {
		t.Fatalf("saveScopedFile() error = %v, want %v", err, errCodeSaveFileMustExist)
	}
}

func TestSaveScopedFileRejectsMissingFileWithCreateNew(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "docs", "new.md")

	_, err := saveScopedFile(target, "body", []string{root}, true)
	if err == nil {
		t.Fatal("saveScopedFile() error = nil, want missing-file validation error")
	}
	if !errors.Is(err, errCodeSaveFileMustExist) {
		t.Fatalf("saveScopedFile() error = %v, want %v", err, errCodeSaveFileMustExist)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Stat() error = %v, want not exist", statErr)
	}
}

func TestLocateScopedFileFindsSuffixMatches(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "src", "a.js")
	second := filepath.Join(root, "lib", "src", "a.js")
	writeTestFile(t, first, "const answer = 42;\n")
	writeTestFile(t, second, "export const picked = true;\n")

	result, err := locateScopedFile(context.Background(), "src/a.js", []string{root}, 10)
	if err != nil {
		t.Fatalf("locateScopedFile() error = %v", err)
	}
	if result.Truncated {
		t.Fatal("locateScopedFile() truncated = true, want false")
	}
	wantPaths := []string{first, second}
	if !reflect.DeepEqual(result.Paths, wantPaths) {
		t.Fatalf("locateScopedFile() paths = %#v, want %#v", result.Paths, wantPaths)
	}
	if len(result.Matches) != 2 {
		t.Fatalf("locateScopedFile() matches = %d, want 2", len(result.Matches))
	}
}

func TestBuildCodeOpenResultReturnsFullMarkdownPreview(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "docs", "guide.md")
	writeTestFile(t, target, "# Title\n\nBody\n")

	scoped, err := resolveOpenTarget(context.Background(), target, []string{root})
	if err != nil {
		t.Fatalf("resolveOpenTarget() error = %v", err)
	}
	result, err := buildCodeOpenResult(scoped, 2)
	if err != nil {
		t.Fatalf("buildCodeOpenResult() error = %v", err)
	}
	if !result.Ok {
		t.Fatal("buildCodeOpenResult() ok = false, want true")
	}
	if result.StartLine != 1 || result.EndLine != 3 || result.TotalLines != 3 {
		t.Fatalf("buildCodeOpenResult() line range = %d-%d/%d, want 1-3/3", result.StartLine, result.EndLine, result.TotalLines)
	}
	snippet, ok := result.Snippet.(string)
	if !ok {
		t.Fatalf("buildCodeOpenResult() snippet type = %T, want string", result.Snippet)
	}
	if snippet != "# Title\n\nBody\n" {
		t.Fatalf("buildCodeOpenResult() snippet = %q, want full markdown", snippet)
	}
}

func TestLocateScopedFileSkipsIgnoredDirectories(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "src", "keep.js"), "const keep = true;\n")
	for _, dir := range []string{
		".agent",
		".agents",
		".build-cache",
		".cache",
		".claude",
		".git",
		".workspace",
		".worktrees",
		"__pycache__",
		"build",
		"coverage",
		"dist",
		"node_modules",
		"vendor",
	} {
		writeTestFile(t, filepath.Join(root, dir, "src", "keep.js"), "const skip = true;\n")
	}

	result, err := locateScopedFile(context.Background(), "keep.js", []string{root}, 10)
	if err != nil {
		t.Fatalf("locateScopedFile() error = %v", err)
	}
	if len(result.Paths) != 1 || result.Paths[0] != filepath.Join(root, "src", "keep.js") {
		t.Fatalf("locateScopedFile() paths = %#v", result.Paths)
	}
}

func TestLocateScopedFileSkipsTooDeepDirectories(t *testing.T) {
	root := t.TempDir()
	deep := root
	for i := 0; i < codeSearchMaxDepth+2; i++ {
		deep = filepath.Join(deep, "nested")
	}
	writeTestFile(t, filepath.Join(deep, "target.js"), "const depth = true;\n")

	result, err := locateScopedFile(context.Background(), "target.js", []string{root}, 10)
	if err != nil {
		t.Fatalf("locateScopedFile() error = %v", err)
	}
	if len(result.Paths) != 0 {
		t.Fatalf("locateScopedFile() paths = %#v, want no deep matches", result.Paths)
	}
}

func TestBuildCodeOpenResultRejectsLargeFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "docs", "large.txt")
	writeTestFile(t, target, strings.Repeat("a", int(maxCodeOpenFileBytes)+1))

	result, err := buildCodeOpenResult(scopedPath{Root: root, Abs: target, Relative: "docs/large.txt"}, 1)
	if err == nil {
		t.Fatalf("buildCodeOpenResult() result = %#v, want size error", result)
	}
}

func TestBuildCodeOpenResultRejectsLargeImageFiles(t *testing.T) {
	root := t.TempDir()
	for _, ext := range []string{".png", ".jpg", ".gif", ".svg", ".webp", ".ico"} {
		ext := ext
		t.Run(ext, func(t *testing.T) {
			target := filepath.Join(root, "assets", "large"+ext)
			writeTestFile(t, target, strings.Repeat("a", int(maxCodeOpenFileBytes)+1))

			result, err := buildCodeOpenResult(scopedPath{Root: root, Abs: target, Relative: "assets/large" + ext}, 1)
			if err == nil {
				t.Fatalf("buildCodeOpenResult() result = %#v, want image size rejection", result)
			}
		})
	}
}

func TestBuildCodeOpenResultRejectsImageExtensionWithInvalidHeader(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "assets", "fake.png")
	writeTestFile(t, target, "not really a png")

	result, err := buildCodeOpenResult(scopedPath{Root: root, Abs: target, Relative: "assets/fake.png"}, 1)
	if err == nil {
		t.Fatalf("buildCodeOpenResult() result = %#v, want image header rejection", result)
	}
	if !strings.Contains(err.Error(), "image") {
		t.Fatalf("buildCodeOpenResult() error = %v, want image validation failure", err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
