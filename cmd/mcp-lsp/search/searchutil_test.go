package search

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
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

func TestWalkSearchEntryPropagatesWalkErr(t *testing.T) {
	var results []SearchMatch
	err := walkSearchEntry(context.Background(), "/repo", "/repo/a.go", "", 1024, literalMatcher("x"), &results, nil, errors.New("walk boom"))
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
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) })

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

func TestDecodeSGMatchesRejectsInvalidJSON(t *testing.T) {
	_, err := decodeSGMatches([]byte("{bad-json}\n"), "/repo")
	if err == nil || !strings.Contains(err.Error(), "sg json") {
		t.Fatalf("decodeSGMatches() error = %v, want json failure", err)
	}
}

func TestDecodeSGScanMatchesRejectsInvalidJSON(t *testing.T) {
	_, err := decodeSGScanMatches([]byte("{bad-json}"), "/repo")
	if err == nil || !strings.Contains(err.Error(), "sg scan json") {
		t.Fatalf("decodeSGScanMatches() error = %v, want scan json failure", err)
	}
}

func TestSearchASTExitOneWithStderrReturnsError(t *testing.T) {
	sg := writeFakeSG(t, 1, "", "parse error")
	t.Setenv("PATH", filepath.Dir(sg)+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := SearchAST(context.Background(), ASTSearchOptions{Root: t.TempDir(), Query: "bad", Language: "go"})
	if err == nil || !strings.Contains(err.Error(), "parse error") {
		t.Fatalf("SearchAST() error = %v, want sg stderr", err)
	}
}

func TestSearchASTExitOneWithoutStderrReturnsNoMatches(t *testing.T) {
	sg := writeFakeSG(t, 1, "", "")
	t.Setenv("PATH", filepath.Dir(sg)+string(os.PathListSeparator)+os.Getenv("PATH"))

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
	t.Setenv("PATH", filepath.Dir(sg)+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	t.Setenv("PATH", filepath.Dir(sg)+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	t.Setenv("PATH", filepath.Dir(sg)+string(os.PathListSeparator)+os.Getenv("PATH"))

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

func literalMatcher(query string) lineMatcher {
	matcher, err := shared.NewLineMatcher(query, false, false)
	if err != nil {
		panic(err)
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
	path := filepath.Join(dir, "sg")
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
	path := filepath.Join(dir, "sg")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + shellQuote(argsPath) + "\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sg args: %v", err)
	}
	return path
}

func readFakeSGArgs(t *testing.T, argsPath string) []string {
	t.Helper()
	payload, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake sg args: %v", err)
	}
	text := strings.TrimSpace(string(payload))
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
