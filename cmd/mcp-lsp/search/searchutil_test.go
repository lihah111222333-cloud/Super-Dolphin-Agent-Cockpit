package search

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

func TestSearchTextCapsLineSnippet(t *testing.T) {
	root := t.TempDir()
	long := "needle " + strings.Repeat("x", 220)
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte(long+"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	matches, err := SearchText(context.Background(), TextSearchOptions{
		Root:         root,
		Path:         path,
		Query:        "needle",
		MaxFileBytes: 1024,
	})
	if err != nil {
		t.Fatalf("SearchText error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	if got := len([]rune(matches[0].Text)); got != 153 {
		t.Fatalf("snippet length = %d, want 153 with ellipsis", got)
	}
	if !strings.HasSuffix(matches[0].Text, "...") {
		t.Fatalf("snippet = %q, want ellipsis suffix", matches[0].Text)
	}
}

func TestSearchTextSmartCaseDefault(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte("needle\nNeedle\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	lowerMatches, err := SearchText(context.Background(), TextSearchOptions{
		Root:         root,
		Path:         path,
		Query:        "needle",
		MaxFileBytes: 1024,
	})
	if err != nil {
		t.Fatalf("SearchText lowercase error: %v", err)
	}
	if len(lowerMatches) != 2 {
		t.Fatalf("lowercase smart-case matches = %d, want 2", len(lowerMatches))
	}

	upperMatches, err := SearchText(context.Background(), TextSearchOptions{
		Root:         root,
		Path:         path,
		Query:        "Needle",
		MaxFileBytes: 1024,
	})
	if err != nil {
		t.Fatalf("SearchText uppercase error: %v", err)
	}
	if len(upperMatches) != 1 || upperMatches[0].Text != "Needle" {
		t.Fatalf("uppercase smart-case matches = %#v, want exact-case Needle only", upperMatches)
	}
}

func TestSearchTextCaseSensitiveOverride(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte("needle\nNeedle\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	insensitive := false
	insensitiveMatches, err := SearchText(context.Background(), TextSearchOptions{
		Root:          root,
		Path:          path,
		Query:         "Needle",
		CaseSensitive: &insensitive,
		MaxFileBytes:  1024,
	})
	if err != nil {
		t.Fatalf("SearchText explicit insensitive error: %v", err)
	}
	if len(insensitiveMatches) != 2 {
		t.Fatalf("explicit insensitive matches = %d, want 2", len(insensitiveMatches))
	}

	sensitive := true
	sensitiveMatches, err := SearchText(context.Background(), TextSearchOptions{
		Root:          root,
		Path:          path,
		Query:         "needle",
		CaseSensitive: &sensitive,
		MaxFileBytes:  1024,
	})
	if err != nil {
		t.Fatalf("SearchText explicit sensitive error: %v", err)
	}
	if len(sensitiveMatches) != 1 || sensitiveMatches[0].Text != "needle" {
		t.Fatalf("explicit sensitive matches = %#v, want exact-case needle only", sensitiveMatches)
	}
}

func TestWalkSearchEntryPropagatesWalkErr(t *testing.T) {
	var results []SearchMatch
	err := walkSearchEntry(context.Background(), "/repo", "/repo/a.go", "/repo", "", 1024, literalMatcher(t, "x"), 0, &results, nil, errors.New("walk boom"))
	if err == nil || !strings.Contains(err.Error(), "walk boom") {
		t.Fatalf("walkSearchEntry() error = %v, want walk boom", err)
	}
}

func TestSearchTextSingleFilePropagatesCandidateProbeError(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o000); err != nil {
		t.Fatalf("write target: %v", err)
	}
	makeUnreadableForTest(t, target)

	_, err := SearchText(context.Background(), TextSearchOptions{Root: root, Path: target, Query: "package"})
	if err == nil || !strings.Contains(err.Error(), "binary probe") {
		t.Fatalf("SearchText() error = %v, want single-file probe failure", err)
	}
}

func TestSearchTextInvalidGlobReturnsError(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	_, err := SearchText(context.Background(), TextSearchOptions{Root: root, Path: target, Query: "package", Glob: "["})
	if err == nil || !strings.Contains(err.Error(), "glob") {
		t.Fatalf("SearchText() error = %v, want invalid glob error", err)
	}
}

func TestSearchTextDoublestarGlobMatchesCurrentAndNestedFiles(t *testing.T) {
	root, err := NormalizeRoot(t.TempDir())
	if err != nil {
		t.Fatalf("NormalizeRoot() error = %v", err)
	}
	writeSearchTestFile(t, filepath.Join(root, "current_test.go"), "package root\nconst needleCurrent = true\n")
	writeSearchTestFile(t, filepath.Join(root, "nested", "child_test.go"), "package nested\nconst needleChild = true\n")
	writeSearchTestFile(t, filepath.Join(root, "nested", "deeper", "grand_test.go"), "package deeper\nconst needleGrand = true\n")
	writeSearchTestFile(t, filepath.Join(root, "nested", "skip.go"), "package nested\nconst needleSkip = true\n")

	matches, err := SearchText(context.Background(), TextSearchOptions{
		Root:         root,
		Query:        "needle",
		Glob:         "**/*_test.go",
		MaxFileBytes: 1024,
	})
	if err != nil {
		t.Fatalf("SearchText() error = %v", err)
	}
	if len(matches) != 3 {
		t.Fatalf("SearchText() matches = %#v, want current and nested test files only", matches)
	}
	got := map[string]bool{}
	for _, match := range matches {
		rel, err := filepath.Rel(root, match.AbsPath)
		if err != nil {
			t.Fatalf("relative match path: %v", err)
		}
		got[filepath.ToSlash(rel)] = true
	}
	for _, want := range []string{"current_test.go", "nested/child_test.go", "nested/deeper/grand_test.go"} {
		if !got[want] {
			t.Fatalf("SearchText() paths = %#v, missing %s", got, want)
		}
	}
	if got["nested/skip.go"] {
		t.Fatalf("SearchText() paths = %#v, included non-test file", got)
	}
}

func TestSearchTextBraceGlobMatchesAlternativesAndEscapedLiteralBrace(t *testing.T) {
	root, err := NormalizeRoot(t.TempDir())
	if err != nil {
		t.Fatalf("NormalizeRoot() error = %v", err)
	}
	writeSearchTestFile(t, filepath.Join(root, "src", "client.js"), "const needle = true\n")
	writeSearchTestFile(t, filepath.Join(root, "src", "App.jsx"), "export const needle = <main />\n")
	writeSearchTestFile(t, filepath.Join(root, "src", "style.css"), ".needle { color: black; }\n")
	writeSearchTestFile(t, filepath.Join(root, "src", "component{demo}.jsx"), "export const literalNeedle = true\n")

	matches, err := SearchText(context.Background(), TextSearchOptions{
		Root:         root,
		Path:         filepath.Join(root, "src"),
		Query:        "needle",
		Glob:         "**/*.{js,jsx}",
		MaxFileBytes: 1024,
	})
	if err != nil {
		t.Fatalf("SearchText() brace glob error = %v", err)
	}
	got := searchTestRelativePaths(t, root, matches)
	for _, want := range []string{"src/client.js", "src/App.jsx", "src/component{demo}.jsx"} {
		if !got[want] {
			t.Fatalf("brace glob paths = %#v, missing %s", got, want)
		}
	}
	if got["src/style.css"] {
		t.Fatalf("brace glob paths = %#v, included CSS file", got)
	}

	literalMatches, err := SearchText(context.Background(), TextSearchOptions{
		Root:         root,
		Path:         filepath.Join(root, "src"),
		Query:        "literalNeedle",
		Glob:         `*\{demo\}.jsx`,
		MaxFileBytes: 1024,
	})
	if err != nil {
		t.Fatalf("SearchText() escaped literal brace glob error = %v", err)
	}
	got = searchTestRelativePaths(t, root, literalMatches)
	if len(got) != 1 || !got["src/component{demo}.jsx"] {
		t.Fatalf("escaped literal brace paths = %#v, want component{demo}.jsx only", got)
	}
}

func searchTestRelativePaths(t *testing.T, root string, matches []SearchMatch) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	for _, match := range matches {
		rel, err := filepath.Rel(root, match.AbsPath)
		if err != nil {
			t.Fatalf("relative match path: %v", err)
		}
		got[filepath.ToSlash(rel)] = true
	}
	return got
}

func TestSearchTextSearchesDelimitedPaths(t *testing.T) {
	root, err := NormalizeRoot(t.TempDir())
	if err != nil {
		t.Fatalf("NormalizeRoot() error = %v", err)
	}
	writeSearchTestFile(t, filepath.Join(root, "first", "one.go"), "package first\nconst needleOne = true\n")
	writeSearchTestFile(t, filepath.Join(root, "second", "two.go"), "package second\nconst needleTwo = true\n")
	writeSearchTestFile(t, filepath.Join(root, "third", "skip.go"), "package third\nconst needleThree = true\n")

	cases := map[string]string{
		"comma":       "first,second",
		"comma_space": "first, second",
		"newline":     "first\nsecond",
		"space":       "first second",
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			matches, err := SearchText(context.Background(), TextSearchOptions{
				Root:         root,
				Path:         path,
				Query:        "needle",
				MaxFileBytes: 1024,
			})
			if err != nil {
				t.Fatalf("SearchText() error = %v", err)
			}
			assertSearchTextDelimitedPathMatches(t, root, matches)
		})
	}
}

func TestSearchTextSearchesExplicitPathList(t *testing.T) {
	root, err := NormalizeRoot(t.TempDir())
	if err != nil {
		t.Fatalf("NormalizeRoot() error = %v", err)
	}
	writeSearchTestFile(t, filepath.Join(root, "first dir", "one.go"), "package first\nconst needleOne = true\n")
	writeSearchTestFile(t, filepath.Join(root, "second dir", "two.go"), "package second\nconst needleTwo = true\n")
	writeSearchTestFile(t, filepath.Join(root, "third dir", "skip.go"), "package third\nconst needleThree = true\n")

	matches, err := SearchText(context.Background(), TextSearchOptions{
		Root:         root,
		Paths:        []string{"first dir", "second dir"},
		Query:        "needle",
		MaxFileBytes: 1024,
	})
	if err != nil {
		t.Fatalf("SearchText() error = %v", err)
	}
	assertSearchTextPathMatches(t, root, matches, []string{"first dir/one.go", "second dir/two.go"}, "third dir/skip.go")
}

func assertSearchTextDelimitedPathMatches(t *testing.T, root string, matches []SearchMatch) {
	assertSearchTextPathMatches(t, root, matches, []string{"first/one.go", "second/two.go"}, "third/skip.go")
}

func assertSearchTextPathMatches(t *testing.T, root string, matches []SearchMatch, wants []string, unwanted string) {
	t.Helper()
	if len(matches) != len(wants) {
		t.Fatalf("SearchText() matches = %#v, want %d matches from requested paths", matches, len(wants))
	}
	got := map[string]bool{}
	for _, match := range matches {
		rel, err := filepath.Rel(root, match.AbsPath)
		if err != nil {
			t.Fatalf("relative match path: %v", err)
		}
		got[filepath.ToSlash(rel)] = true
	}
	for _, want := range wants {
		if !got[want] {
			t.Fatalf("SearchText() paths = %#v, missing %s", got, want)
		}
	}
	if got[unwanted] {
		t.Fatalf("SearchText() paths = %#v, searched path outside requested scopes", got)
	}
}

func TestSearchTextSkipsWorkspaceNoiseDirectories(t *testing.T) {
	root, err := NormalizeRoot(t.TempDir())
	if err != nil {
		t.Fatalf("NormalizeRoot() error = %v", err)
	}
	keep := filepath.Join(root, "src", "keep.go")
	writeSearchTestFile(t, keep, "package main\nconst needle = true\n")
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
		writeSearchTestFile(t, filepath.Join(root, dir, "skip.go"), "package main\nconst needle = false\n")
	}

	matches, err := SearchText(context.Background(), TextSearchOptions{
		Root:  root,
		Query: "needle",
	})
	if err != nil {
		t.Fatalf("SearchText() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("SearchText() matches = %#v, want one match outside skipped dirs", matches)
	}
	if matches[0].AbsPath != keep {
		t.Fatalf("SearchText() match path = %q, want %q", matches[0].AbsPath, keep)
	}
}

func TestFilterAndCapSearchMatchesExcludesWorkspaceNoisePaths(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "src", "keep.go")
	matches := []SearchMatch{
		{AbsPath: filepath.Join(root, ".build-cache", "skip.go"), SearchRoot: root, File: ".build-cache/skip.go", Line: 1, Col: 1, Text: "needle"},
		{AbsPath: filepath.Join(root, ".workspace", "skip.go"), SearchRoot: root, File: ".workspace/skip.go", Line: 1, Col: 1, Text: "needle"},
		{AbsPath: filepath.Join(root, "node_modules", "skip.go"), SearchRoot: root, File: "node_modules/skip.go", Line: 1, Col: 1, Text: "needle"},
		{AbsPath: keep, SearchRoot: root, File: "src/keep.go", Line: 1, Col: 1, Text: "needle"},
	}

	filtered, total, truncated := FilterAndCapSearchMatches(matches, 0)
	if truncated {
		t.Fatal("FilterAndCapSearchMatches() truncated = true, want false")
	}
	if total != 1 || len(filtered) != 1 {
		t.Fatalf("FilterAndCapSearchMatches() total=%d filtered=%#v, want one kept match", total, filtered)
	}
	if filtered[0].AbsPath != keep {
		t.Fatalf("FilterAndCapSearchMatches() kept path = %q, want %q", filtered[0].AbsPath, keep)
	}
}

func TestDecodeSGRejectsInvalidJSON(t *testing.T) {
	_, err := decodeSGMatchesReader(bytes.NewReader([]byte("{bad-json}\n")), "/repo", 0, func() {})
	if err == nil || !strings.Contains(err.Error(), "sg json") {
		t.Fatalf("decodeSGMatches() error = %v, want json failure", err)
	}
	_, err = decodeSGScanMatchesReader(bytes.NewReader([]byte("{bad-json}")), "/repo", 0, func() {})
	if err == nil || !strings.Contains(err.Error(), "sg scan json") {
		t.Fatalf("decodeSGScanMatches() error = %v, want scan json failure", err)
	}
}

func TestSearchASTExitOneWithStderrReturnsError(t *testing.T) {
	sg := writeFakeSG(t, 1, "", "parse error")
	setFakeSGPath(t, sg)

	_, err := SearchAST(context.Background(), ASTSearchOptions{Root: t.TempDir(), Query: "bad", Language: "go"})
	if err == nil || !strings.Contains(err.Error(), "parse error") {
		t.Fatalf("SearchAST() error = %v, want sg stderr", err)
	}
}

func TestSearchASTExitOneWithoutStderrReturnsNoMatches(t *testing.T) {
	sg := writeFakeSG(t, 1, "", "")
	setFakeSGPath(t, sg)

	matches, err := SearchAST(context.Background(), ASTSearchOptions{Root: t.TempDir(), Query: "bad", Language: "go"})
	if err != nil {
		t.Fatalf("SearchAST() error = %v, want nil no-match error", err)
	}
	if len(matches) != 0 {
		t.Fatalf("SearchAST() matches = %#v, want empty no-match result", matches)
	}
}

func TestSearchASTScanExitOneWithStderrReturnsError(t *testing.T) {
	sg := writeFakeSG(t, 1, "", "scan parse error")
	setFakeSGPath(t, sg)
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	_, err := SearchAST(context.Background(), ASTSearchOptions{Root: root, Path: target, Query: "function_declaration", Language: "go"})
	if err == nil || !strings.Contains(err.Error(), "scan parse error") {
		t.Fatalf("SearchAST scan error = %v, want sg scan stderr", err)
	}
}

func TestSearchASTScanExitOneWithoutStderrReturnsNoMatches(t *testing.T) {
	sg := writeFakeSG(t, 1, "", "")
	setFakeSGPath(t, sg)
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	matches, err := SearchAST(context.Background(), ASTSearchOptions{Root: root, Path: target, Query: "function_declaration", Language: "go"})
	if err != nil {
		t.Fatalf("SearchAST scan error = %v, want nil no-match error", err)
	}
	if len(matches) != 0 {
		t.Fatalf("SearchAST scan matches = %#v, want empty no-match result", matches)
	}
}

func TestSearchASTPassesReactLanguageAliasToSGPattern(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "component.tsx")
	if err := os.WriteFile(target, []byte("export const View = () => <div />\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	argsPath := filepath.Join(t.TempDir(), "sg-args.txt")
	sg := writeFakeSGArgs(t, argsPath)
	setFakeSGPath(t, sg)

	_, err := SearchAST(context.Background(), ASTSearchOptions{Root: root, Path: "component.tsx", Query: "console.log($A)"})
	if err != nil {
		t.Fatalf("SearchAST() error = %v", err)
	}
	args := readFakeSGArgs(t, argsPath)
	if got := argAfter(args, "--lang"); got != "tsx" {
		t.Fatalf("sg --lang = %q, args = %#v, want tsx", got, args)
	}
}

func TestASTGrepLanguageIDMapsReactIDs(t *testing.T) {
	cases := map[string]string{
		"javascriptreact": "jsx",
		"typescriptreact": "tsx",
		"typescript":      "typescript",
	}
	for input, want := range cases {
		if got := astGrepLanguageID(input); got != want {
			t.Fatalf("astGrepLanguageID(%q) = %q, want %q", input, got, want)
		}
	}
}

func literalMatcher(t *testing.T, query string) lineMatcher {
	t.Helper()
	matcher, err := shared.NewLineMatcher(query, false, false)
	if err != nil {
		t.Fatalf("create literal matcher: %v", err)
	}
	return matcher
}

type fakeDirEntry struct {
	name    string
	mode    fs.FileMode
	infoErr error
}

func (e fakeDirEntry) Name() string      { return e.name }
func (e fakeDirEntry) IsDir() bool       { return e.mode.IsDir() }
func (e fakeDirEntry) Type() fs.FileMode { return e.mode.Type() }
func (e fakeDirEntry) Info() (fs.FileInfo, error) {
	if e.infoErr != nil {
		return nil, e.infoErr
	}
	return fakeFileInfo{name: e.name, mode: e.mode, size: 1}, nil
}

type fakeFileInfo struct {
	name string
	mode fs.FileMode
	size int64
}

func (i fakeFileInfo) Name() string       { return i.name }
func (i fakeFileInfo) Size() int64        { return i.size }
func (i fakeFileInfo) Mode() fs.FileMode  { return i.mode }
func (i fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (i fakeFileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fakeFileInfo) Sys() any           { return nil }

func writeFakeSG(t *testing.T, exitCode int, stdout, stderr string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, fakeSGName())
	if runtime.GOOS == "windows" {
		script := "@echo off\r\n" +
			"powershell -NoProfile -ExecutionPolicy Bypass -Command \"[Console]::Out.Write(" + powershellSingleQuote(stdout) + "); [Console]::Error.Write(" + powershellSingleQuote(stderr) + "); exit " + strconv.Itoa(exitCode) + "\"\r\n" +
			"exit /b %ERRORLEVEL%\r\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatalf("write fake sg: %v", err)
		}
		return path
	}
	script := "#!/bin/sh\n" +
		"printf '%s' " + shellQuote(stdout) + "\n" +
		"printf '%s' " + shellQuote(stderr) + " >&2\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sg: %v", err)
	}
	return path
}

func writeFakeSGArgs(t *testing.T, argsPath string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, fakeSGName())
	if runtime.GOOS == "windows" {
		psPath := filepath.Join(dir, "sg.ps1")
		psScript := "Set-Content -LiteralPath " + powershellSingleQuote(argsPath) + " -Value $args\nexit 1\n"
		if err := os.WriteFile(psPath, []byte(psScript), 0o755); err != nil {
			t.Fatalf("write fake sg args powershell: %v", err)
		}
		script := "@echo off\r\n" +
			"powershell -NoProfile -ExecutionPolicy Bypass -File \"%~dp0sg.ps1\" %*\r\n" +
			"exit /b %ERRORLEVEL%\r\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatalf("write fake sg args: %v", err)
		}
		return path
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + shellQuote(argsPath) + "\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sg args: %v", err)
	}
	return path
}

func setFakeSGPath(t *testing.T, sg string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Setenv("PATH", filepath.Dir(sg)+string(os.PathListSeparator)+os.Getenv("PATH"))
		return
	}
	t.Setenv("PATH", filepath.Dir(sg))
}

func readFakeSGArgs(t *testing.T, argsPath string) []string {
	t.Helper()
	payload, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake sg args: %v", err)
	}
	text := strings.TrimSpace(strings.ReplaceAll(string(payload), "\r\n", "\n"))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func argAfter(args []string, flag string) string {
	for index, arg := range args {
		if arg == flag && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func fakeSGName() string {
	if runtime.GOOS == "windows" {
		return "sg.cmd"
	}
	return "sg"
}

func powershellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func makeUnreadableForTest(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
		return
	}
	principal := os.Getenv("USERNAME")
	if domain := os.Getenv("USERDOMAIN"); domain != "" && principal != "" {
		principal = domain + `\` + principal
	}
	if strings.TrimSpace(principal) == "" {
		t.Skip("USERNAME is required to make the test file unreadable on Windows")
	}
	deny := principal + ":(R)"
	cmd := exec.Command("icacls", path, "/deny", deny)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("icacls deny read unavailable: %v; output=%s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("icacls", path, "/remove:d", principal).Run()
		_ = os.Chmod(path, 0o600)
	})
}

func writeSearchTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestSearchTextSkipsGoModCache(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	keep := filepath.Join(root, "src", "keep.go")
	writeSearchTestFile(t, keep, "package main\nconst needle = true\n")
	modCache := filepath.Join(root, "go", "pkg", "mod", "github.com", "foo", "bar")
	writeSearchTestFile(t, filepath.Join(modCache, "skip.go"), "package bar\nconst needle = false\n")
	t.Setenv("GOMODCACHE", filepath.Join(root, "go", "pkg", "mod"))
	matches, err := SearchText(context.Background(), TextSearchOptions{Root: root, Query: "needle"})
	if err != nil {
		t.Fatalf("SearchText() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("SearchText() matches = %d, want 1 (only src/keep.go)", len(matches))
	}
	if matches[0].AbsPath != keep {
		t.Fatalf("SearchText() match = %q, want %q", matches[0].AbsPath, keep)
	}
}

func TestSearchTextSkipsGoModCacheByPathSegment(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	keep := filepath.Join(root, "src", "keep.go")
	writeSearchTestFile(t, keep, "package main\nconst needle = true\n")
	modDir := filepath.Join(root, "go", "pkg", "mod", "example.com", "lib")
	writeSearchTestFile(t, filepath.Join(modDir, "skip.go"), "package lib\nconst needle = false\n")
	t.Setenv("GOMODCACHE", "")
	t.Setenv("GOPATH", "")
	matches, err := SearchText(context.Background(), TextSearchOptions{Root: root, Query: "needle"})
	if err != nil {
		t.Fatalf("SearchText() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("SearchText() matches = %d, want 1", len(matches))
	}
	if matches[0].AbsPath != keep {
		t.Fatalf("SearchText() match = %q, want %q", matches[0].AbsPath, keep)
	}
}

func TestSearchTextDoesNotSkipUserDirNamedMod(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	modFile := filepath.Join(root, "mod", "keep.go")
	writeSearchTestFile(t, modFile, "package mod\nconst needle = true\n")
	t.Setenv("GOMODCACHE", "/nonexistent/path")
	matches, err := SearchText(context.Background(), TextSearchOptions{Root: root, Query: "needle"})
	if err != nil {
		t.Fatalf("SearchText() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("SearchText() matches = %d, want 1 (mod/keep.go should be included)", len(matches))
	}
}
