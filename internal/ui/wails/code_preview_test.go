package wails

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestCodeSaveResultJSONFieldsMatchConsumerRegistry(t *testing.T) {
	t.Parallel()

	consumerRegistry := map[string]struct{}{
		"ok":             {},
		"filePath":       {},
		"relative":       {},
		"totalLines":     {},
		"contentVersion": {},
	}
	producerFields := make(map[string]struct{})
	resultType := reflect.TypeFor[codeSaveResult]()
	for i := range resultType.NumField() {
		tag, _, _ := strings.Cut(resultType.Field(i).Tag.Get("json"), ",")
		if tag != "" && tag != "-" {
			producerFields[tag] = struct{}{}
		}
	}
	missing := registryDifference(consumerRegistry, producerFields)
	stale := registryDifference(producerFields, consumerRegistry)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("codeSaveResult field drift: missing=%v stale=%v producer=%v", missing, stale, producerFields)
	}
}

func registryDifference(left, right map[string]struct{}) []string {
	diff := make([]string, 0)
	for field := range left {
		if _, ok := right[field]; !ok {
			diff = append(diff, field)
		}
	}
	sort.Strings(diff)
	return diff
}

func TestSaveScopedFileWritesWithinScope(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "docs", "readme.md")
	writeTestFile(t, target, "old")
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}

	result, err := saveScopedFile(target, "# Saved\n\nBody", []string{root}, false, "full", codeContentVersion([]byte("old")))
	if err != nil {
		t.Fatalf("saveScopedFile() error = %v", err)
	}
	data := assertSavedFileContentAndMode(t, target, "# Saved\n\nBody", 0o600)
	wantVersion := codeContentVersion(data)
	assertSuccessfulSaveResult(t, result, wantVersion, codeContentVersion([]byte("old")))
}

func assertSavedFileContentAndMode(t *testing.T, target, wantBody string, wantMode os.FileMode) []byte {
	t.Helper()
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != wantBody {
		t.Fatalf("saveScopedFile() body = %q, want %q", string(data), wantBody)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != wantMode {
		t.Fatalf("saveScopedFile() mode = %o, want %o", got, wantMode)
	}
	return data
}

func assertSuccessfulSaveResult(t *testing.T, result codeSaveResult, wantVersion, staleVersion string) {
	t.Helper()
	if !result.Ok {
		t.Fatal("saveScopedFile() ok = false, want true")
	}
	if result.Relative != "docs/readme.md" {
		t.Fatalf("saveScopedFile() relative = %q, want %q", result.Relative, "docs/readme.md")
	}
	if result.TotalLines != 3 {
		t.Fatalf("saveScopedFile() totalLines = %d, want 3", result.TotalLines)
	}
	if result.ContentVersion != wantVersion {
		t.Fatalf("saveScopedFile() contentVersion = %q, want hash of written bytes %q", result.ContentVersion, wantVersion)
	}
	if result.ContentVersion == staleVersion {
		t.Fatal("saveScopedFile() returned stale request contentVersion")
	}
	assertCodeSaveResultJSONContentVersion(t, result, wantVersion)
}

func TestReplaceFileAtomicallyKeepsOriginalWhenPublishFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "notes.md")
	writeTestFile(t, target, "original")
	wantErr := errors.New("publish failed")

	err := replaceFileAtomically(target, []byte("replacement"), 0o640, func(_, _ string) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("replaceFileAtomically() error = %v, want %v", err, wantErr)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if string(data) != "original" {
		t.Fatalf("target body = %q, want original content", data)
	}
	entries, readDirErr := os.ReadDir(root)
	if readDirErr != nil {
		t.Fatalf("ReadDir() error = %v", readDirErr)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(target) {
		t.Fatalf("directory entries = %v, want only original target", entries)
	}
}

func assertCodeSaveResultJSONContentVersion(t *testing.T, result codeSaveResult, wantVersion string) {
	t.Helper()
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal(codeSaveResult) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(wire, &payload); err != nil {
		t.Fatalf("json.Unmarshal(codeSaveResult) error = %v", err)
	}
	if payload["contentVersion"] != wantVersion {
		t.Fatalf("codeSaveResult JSON contentVersion = %#v, want %q", payload["contentVersion"], wantVersion)
	}
}

// TestBuildCodeOpenResultSnippetIsNotFullSaveToken verifies snippet previews cannot carry overwrite tokens.
func TestBuildCodeOpenResultSnippetIsNotFullSaveToken(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "src", "main.go")
	writeTestFile(t, target, strings.Join([]string{
		"package main",
		"",
		"func main() {",
		"\tprintln(\"hello\")",
		"}",
	}, "\n"))

	result, err := buildCodeOpenResult(scopedPath{Root: root, Abs: target, Relative: "src/main.go"}, 3)
	if err != nil {
		t.Fatalf("buildCodeOpenResult() error = %v", err)
	}
	if result.PreviewMode != "snippet" {
		t.Fatalf("buildCodeOpenResult() previewMode = %q, want snippet", result.PreviewMode)
	}
	if result.ContentVersion != "" {
		t.Fatalf("buildCodeOpenResult() contentVersion = %q, want empty snippet save token", result.ContentVersion)
	}
	if result.RangeStartLine != result.StartLine || result.RangeEndLine != result.EndLine {
		t.Fatalf("buildCodeOpenResult() range = %d-%d, start/end = %d-%d", result.RangeStartLine, result.RangeEndLine, result.StartLine, result.EndLine)
	}
}

// TestSaveScopedFileRejectsSnippetPreviewMode verifies save rejects non-full preview payloads.
func TestSaveScopedFileRejectsSnippetPreviewMode(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "docs", "readme.md")
	writeTestFile(t, target, "# Title\n")

	_, err := saveScopedFile(target, "# Changed\n", []string{root}, false, "snippet", "")
	if err == nil {
		t.Fatal("saveScopedFile() error = nil, want snippet preview mode rejection")
	}
	if !strings.Contains(err.Error(), "previewMode") {
		t.Fatalf("saveScopedFile() error = %v, want previewMode rejection", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if string(data) != "# Title\n" {
		t.Fatalf("saveScopedFile() body = %q, want original content preserved", string(data))
	}
}

func TestSaveScopedFileRejectsOutsideScope(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.txt")

	_, err := saveScopedFile(outside, "nope", []string{root}, false, "full", codeContentVersion(nil))
	if err == nil {
		t.Fatal("saveScopedFile() error = nil, want scope error")
	}
}

func TestSaveScopedFileRejectsMissingFileWithoutCreateNew(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "docs", "new.md")

	_, err := saveScopedFile(target, "body", []string{root}, false, "full", codeContentVersion(nil))
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

	_, err := saveScopedFile(target, "body", []string{root}, true, "full", codeContentVersion(nil))
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
	for range codeSearchMaxDepth + 2 {
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
