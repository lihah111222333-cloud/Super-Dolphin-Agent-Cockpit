package search

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type SearchMatch struct {
	AbsPath   string `json:"-"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	Text      string `json:"text"`
	FuncStart int    `json:"func_start,omitempty"`
	FuncEnd   int    `json:"func_end,omitempty"`
}

type TextSearchOptions struct {
	Root          string
	Path          string
	Glob          string
	Query         string
	Regex         bool
	CaseSensitive bool
	MaxResults    int
	MaxFileBytes  int
}

type ASTSearchOptions struct {
	Root         string
	Path         string
	Glob         string
	Query        string
	Language     string
	MaxResults   int
	MaxFileBytes int
}

type lineMatcher = shared.LineMatcher

type sgStreamMatch struct {
	File  string `json:"file"`
	Text  string `json:"text"`
	Lines string `json:"lines"`
	Range struct {
		Start struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"start"`
	} `json:"range"`
}

func SearchText(ctx context.Context, opts TextSearchOptions) ([]SearchMatch, error) {
	matcher, err := shared.NewLineMatcher(opts.Query, opts.Regex, opts.CaseSensitive)
	if err != nil {
		return nil, err
	}
	pathInfo, info, err := statSearchPath(opts.Root, opts.Path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return searchTextFile(ctx, pathInfo.AbsPath, pathInfo.AbsPath, opts.Glob, opts.MaxFileBytes, matcher)
	}

	results := make([]SearchMatch, 0, maxInt(opts.MaxResults, 8))
	err = filepath.WalkDir(pathInfo.AbsPath, func(candidate string, entry os.DirEntry, walkErr error) error {
		return walkSearchEntry(ctx, pathInfo.AbsPath, candidate, opts.Glob, opts.MaxFileBytes, matcher, &results, entry, walkErr)
	})
	return results, err
}

func SearchAST(ctx context.Context, opts ASTSearchOptions) ([]SearchMatch, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	pathInfo, info, err := statSearchPath(opts.Root, opts.Path)
	if err != nil {
		return nil, err
	}
	language, err := normalizeASTLanguage(opts.Language, pathInfo.AbsPath, info.IsDir(), opts.Glob)
	if err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("sg"); err != nil {
		return nil, errors.New("sg not found in PATH")
	}

	args := []string{"run", "--pattern", query, "--lang", language, "--json=stream"}
	if glob := strings.TrimSpace(opts.Glob); glob != "" {
		args = append(args, "--globs", glob)
	}
	args = append(args, pathInfo.AbsPath)

	cmd := exec.CommandContext(ctx, "sg", args...)
	cmd.Dir = pathInfo.Root
	output, err := cmd.Output()
	if err != nil {
		// sg exits with code 1 when no matches are found; treat as empty.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("sg run: %w", err)
	}
	return decodeSGMatches(output, pathInfo.Root), nil
}

func FilterAndCapSearchMatches(matches []SearchMatch, maxResults int) ([]SearchMatch, int, bool) {
	filtered := make([]SearchMatch, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if strings.TrimSpace(match.File) == "" || shouldExcludePath(match.AbsPath) {
			continue
		}
		key := fmt.Sprintf("%s:%d:%d:%s", match.AbsPath, match.Line, match.Col, match.Text)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, match)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].File != filtered[j].File {
			return filtered[i].File < filtered[j].File
		}
		if filtered[i].Line != filtered[j].Line {
			return filtered[i].Line < filtered[j].Line
		}
		if filtered[i].Col != filtered[j].Col {
			return filtered[i].Col < filtered[j].Col
		}
		return filtered[i].Text < filtered[j].Text
	})

	total := len(filtered)
	if maxResults <= 0 || total <= maxResults {
		return filtered, total, false
	}
	return append([]SearchMatch(nil), filtered[:maxResults]...), total, true
}

func statSearchPath(root, rawPath string) (PathInfo, os.FileInfo, error) {
	pathInfo, err := ResolvePath(root, rawPath)
	if err != nil {
		return PathInfo{}, nil, err
	}
	info, err := os.Lstat(pathInfo.AbsPath)
	if err != nil {
		return PathInfo{}, nil, fmt.Errorf("stat %s: %w", pathInfo.DisplayPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return PathInfo{}, nil, fmt.Errorf("path %q cannot be a symlink", pathInfo.DisplayPath)
	}
	return pathInfo, info, nil
}

func walkSearchEntry(
	ctx context.Context,
	root, candidate, glob string,
	maxFileBytes int,
	matcher lineMatcher,
	results *[]SearchMatch,
	entry os.DirEntry,
	walkErr error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if walkErr != nil || entry == nil {
		return nil
	}
	if entry.IsDir() {
		if shouldSkipDir(entry.Name()) {
			return filepath.SkipDir
		}
		return nil
	}
	if !matchesPathGlob(root, candidate, glob) || !isSearchCandidate(candidate, entry, maxFileBytes) {
		return nil
	}
	found, err := searchTextFile(ctx, root, candidate, glob, maxFileBytes, matcher)
	if err != nil {
		return nil
	}
	*results = append(*results, found...)
	return nil
}

func findDirEntry(candidate string) (os.DirEntry, error) {
	entries, err := os.ReadDir(filepath.Dir(candidate))
	if err != nil {
		return nil, err
	}
	for _, item := range entries {
		if filepath.Join(filepath.Dir(candidate), item.Name()) == candidate {
			return item, nil
		}
	}
	return nil, nil
}

func searchTextFile(ctx context.Context, root, candidate, glob string, maxFileBytes int, matcher lineMatcher) ([]SearchMatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !matchesPathGlob(root, candidate, glob) {
		return nil, nil
	}
	selected, err := findDirEntry(candidate)
	if err != nil {
		return nil, err
	}
	if !isSearchCandidate(candidate, selected, maxFileBytes) {
		return nil, nil
	}

	file, err := os.Open(candidate)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxInt(maxFileBytes, 64*1024))
	results := make([]SearchMatch, 0, 8)
	for lineNum := 1; scanner.Scan(); lineNum++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := scanner.Text()
		col, ok := matcher.Find(line)
		if !ok {
			continue
		}
		results = append(results, SearchMatch{
			AbsPath: candidate,
			File:    displayPath(candidate),
			Line:    lineNum,
			Col:     col + 1,
			Text:    strings.TrimSpace(line),
		})
	}
	return results, scanner.Err()
}

func decodeSGMatches(output []byte, root string) []SearchMatch {
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 0, 64*1024), 2<<20)
	results := make([]SearchMatch, 0, 16)
	for scanner.Scan() {
		var item sgStreamMatch
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			continue
		}
		absPath := item.File
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(root, item.File)
		}
		absPath = filepath.Clean(absPath)
		results = append(results, SearchMatch{
			AbsPath: absPath,
			File:    displayPath(absPath),
			Line:    item.Range.Start.Line + 1,
			Col:     item.Range.Start.Column + 1,
			Text:    collapseSnippet(item.Lines, item.Text),
		})
	}
	return results
}

func matchesPathGlob(root, candidate, rawGlob string) bool {
	glob := filepath.ToSlash(strings.TrimSpace(rawGlob))
	if glob == "" {
		return true
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	slashRel := filepath.ToSlash(rel)
	if matchesCompiledGlob(glob, slashRel) {
		return true
	}
	if strings.Contains(glob, "/") {
		return false
	}
	return matchesCompiledGlob(glob, path.Base(slashRel))
}

func matchesCompiledGlob(pattern, candidate string) bool {
	ok, err := path.Match(pattern, candidate)
	return err == nil && ok
}

func normalizeASTLanguage(raw, target string, isDir bool, glob string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "go", "golang":
		return "go", nil
	case "md", "markdown":
		return "markdown", nil
	case "json":
		return "json", nil
	case "yaml", "yml":
		return "yaml", nil
	}
	if !isDir {
		if inferred := inferLanguage(target); inferred != "" {
			return inferred, nil
		}
	}
	if inferred := inferLanguage(glob); inferred != "" {
		return inferred, nil
	}
	return "", errors.New("language is required for ast_search")
}
