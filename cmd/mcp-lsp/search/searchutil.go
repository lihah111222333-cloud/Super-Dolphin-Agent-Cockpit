package search

import (
	"bufio"
	"bytes"
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
	"unicode"

	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type SearchMatch struct {
	AbsPath    string `json:"-"`
	SearchRoot string `json:"-"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Col        int    `json:"col"`
	Text       string `json:"text"`
	FuncStart  int    `json:"func_start,omitempty"`
	FuncEnd    int    `json:"func_end,omitempty"`
}

type TextSearchOptions struct {
	Root          string
	Roots         []string
	Path          string
	Paths         []string
	Glob          string
	Query         string
	Regex         bool
	CaseSensitive *bool
	MaxResults    int
	MaxFileBytes  int
}

type ASTSearchOptions struct {
	Root         string
	Roots        []string
	Path         string
	Paths        []string
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

// SearchText 搜索文本。
func SearchText(ctx context.Context, opts TextSearchOptions) ([]SearchMatch, error) {
	caseSensitive := strings.ToLower(opts.Query) != opts.Query
	if opts.CaseSensitive != nil {
		caseSensitive = *opts.CaseSensitive
	}
	matcher, err := shared.NewLineMatcher(opts.Query, opts.Regex, caseSensitive)
	if err != nil {
		return nil, err
	}
	if err := validateSearchGlob(opts.Glob); err != nil {
		return nil, err
	}
	searchPaths, err := statSearchPaths(opts.Root, opts.Roots, opts.Path, opts.Paths)
	if err != nil {
		return nil, err
	}
	pkglogger.Info("mcp-lsp grep text_search paths resolved",
		"query", opts.Query,
		"path", opts.Path,
		"paths_count", len(opts.Paths),
		"glob", opts.Glob,
		"root", opts.Root,
		"roots_count", len(opts.Roots),
		"search_paths_count", len(searchPaths),
		"max_results", opts.MaxResults,
	)
	results := make([]SearchMatch, 0, maxInt(opts.MaxResults, 8))
	for _, searchPath := range searchPaths {
		if !searchPath.Info.IsDir() {
			found, err := searchTextFile(ctx, searchPath.Path.AbsPath, searchPath.Path.AbsPath, searchPath.Path.Root, opts.Glob, opts.MaxFileBytes, matcher)
			if err != nil {
				return nil, err
			}
			results = append(results, found...)
			continue
		}
		if err := filepath.WalkDir(searchPath.Path.AbsPath, func(candidate string, entry os.DirEntry, walkErr error) error {
			return walkSearchEntry(ctx, searchPath.Path.AbsPath, candidate, searchPath.Path.Root, opts.Glob, opts.MaxFileBytes, matcher, &results, entry, walkErr)
		}); err != nil {
			return nil, err
		}
	}
	pkglogger.Info("mcp-lsp grep text_search completed",
		"query", opts.Query,
		"path", opts.Path,
		"paths_count", len(opts.Paths),
		"glob", opts.Glob,
		"root", opts.Root,
		"matches", len(results),
	)
	return results, nil
}

// SearchAST 搜索ast。
func SearchAST(ctx context.Context, opts ASTSearchOptions) ([]SearchMatch, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	if err := validateSearchGlob(opts.Glob); err != nil {
		return nil, err
	}
	searchPaths, err := statSearchPaths(opts.Root, opts.Roots, opts.Path, opts.Paths)
	if err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("sg"); err != nil {
		return nil, errors.New("sg not found in PATH")
	}

	results := make([]SearchMatch, 0, maxInt(opts.MaxResults, 8))
	for _, searchPath := range searchPaths {
		language, err := normalizeASTLanguage(opts.Language, searchPath.Path.AbsPath, searchPath.Info.IsDir(), opts.Glob)
		if err != nil {
			return nil, err
		}
		var found []SearchMatch
		if isLikelyNodeType(query) {
			found, err = runSGKindSearch(ctx, query, language, searchPath.Path.AbsPath, searchPath.Path.Root, opts.Glob)
		} else {
			found, err = runSGPatternSearch(ctx, query, language, searchPath.Path.AbsPath, searchPath.Path.Root, opts.Glob)
		}
		if err != nil {
			return nil, err
		}
		results = append(results, found...)
	}
	return results, nil
}

// FilterAndCapSearchMatches 判断过滤条件capsearch是否匹配。
func FilterAndCapSearchMatches(matches []SearchMatch, maxResults int) ([]SearchMatch, int, bool) {
	filtered := make([]SearchMatch, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if strings.TrimSpace(match.File) == "" || shouldExcludeSearchMatch(match) {
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
	if maxResults <= 0 {
		return filtered, total, false
	}
	return capSearchMatchesPerFile(filtered, total, maxResults)
}

func shouldExcludeSearchMatch(match SearchMatch) bool {
	filterPath := match.AbsPath
	if relPath := relativeSearchMatchPath(match.SearchRoot, match.AbsPath); relPath != "" {
		filterPath = relPath
	}
	return shouldExcludePath(filterPath)
}

// relativeSearchMatchPath 处理相对searchmatch路径。
func relativeSearchMatchPath(root, absPath string) string {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(absPath) == "" {
		return ""
	}
	relPath, err := filepath.Rel(filepath.Clean(root), filepath.Clean(absPath))
	if err != nil || relPath == "" || relPath == "." || relPath == ".." {
		return ""
	}
	if strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return ""
	}
	return relPath
}

func statSearchPath(root string, roots []string, rawPath string) (PathInfo, os.FileInfo, error) {
	pathInfo, err := ResolvePathInRoots(root, roots, rawPath)
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

type searchPathStat struct {
	Path PathInfo
	Info os.FileInfo
}

// statSearchPaths 处理statsearch路径。
func statSearchPaths(root string, roots []string, rawPath string, explicitPaths []string) ([]searchPathStat, error) {
	if len(explicitPaths) > 0 {
		return statExplicitSearchPaths(root, roots, explicitPaths)
	}
	pathInfo, info, err := statSearchPath(root, roots, rawPath)
	if err == nil {
		return []searchPathStat{{Path: pathInfo, Info: info}}, nil
	}
	fields := splitSearchPathList(rawPath)
	if len(fields) <= 1 {
		return nil, err
	}
	searchPaths := make([]searchPathStat, 0, len(fields))
	for _, field := range fields {
		pathInfo, info, fieldErr := statSearchPath(root, roots, field)
		if fieldErr != nil {
			return nil, fmt.Errorf("stat search path %q from %q: %w", field, rawPath, fieldErr)
		}
		searchPaths = append(searchPaths, searchPathStat{Path: pathInfo, Info: info})
	}
	return searchPaths, nil
}

func statExplicitSearchPaths(root string, roots []string, rawPaths []string) ([]searchPathStat, error) {
	searchPaths := make([]searchPathStat, 0, len(rawPaths))
	for _, rawPath := range rawPaths {
		pathInfo, info, err := statSearchPath(root, roots, rawPath)
		if err != nil {
			return nil, fmt.Errorf("stat search path %q: %w", rawPath, err)
		}
		searchPaths = append(searchPaths, searchPathStat{Path: pathInfo, Info: info})
	}
	return searchPaths, nil
}

func splitSearchPathList(rawPath string) []string {
	return strings.FieldsFunc(rawPath, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
}

// walkSearchEntry 处理walksearch条目。
func walkSearchEntry(
	ctx context.Context,
	root, candidate, searchRoot, glob string,
	maxFileBytes int,
	matcher lineMatcher,
	results *[]SearchMatch,
	entry os.DirEntry,
	walkErr error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if walkErr != nil {
		return fmt.Errorf("walk %s: %w", candidate, walkErr)
	}
	if entry == nil {
		return fmt.Errorf("walk %s: missing dir entry", candidate)
	}
	if entry.IsDir() {
		if shouldSkipDir(entry.Name()) {
			return filepath.SkipDir
		}
		if isInsideGoModCache(candidate) {
			return filepath.SkipDir
		}
		return nil
	}
	selected, err := shouldSearchPath(root, candidate, glob, maxFileBytes, entry)
	if err != nil {
		return err
	}
	if !selected {
		return nil
	}
	found, err := searchTextFile(ctx, root, candidate, searchRoot, glob, maxFileBytes, matcher)
	if err != nil {
		return fmt.Errorf("search %s: %w", candidate, err)
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

func searchTextFile(ctx context.Context, root, candidate, searchRoot, glob string, maxFileBytes int, matcher lineMatcher) ([]SearchMatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ok, err := shouldSearchFile(root, candidate, glob, maxFileBytes)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	file, err := os.Open(candidate)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", candidate, err)
	}
	defer func() { _ = file.Close() }()

	return scanSearchTextFile(ctx, candidate, searchRoot, maxFileBytes, matcher, file)
}

func shouldSearchFile(root, candidate, glob string, maxFileBytes int) (bool, error) {
	selected, err := findDirEntry(candidate)
	if err != nil {
		return false, fmt.Errorf("read dir entry for %s: %w", candidate, err)
	}
	return shouldSearchPath(root, candidate, glob, maxFileBytes, selected)
}

// shouldSearchPath 判断search路径是否可用。
func shouldSearchPath(root, candidate, glob string, maxFileBytes int, entry os.DirEntry) (bool, error) {
	matched, err := matchesPathGlob(root, candidate, glob)
	if err != nil || !matched {
		if err == nil && strings.TrimSpace(glob) != "" && filepath.Clean(root) == filepath.Clean(candidate) {
			pkglogger.Info("mcp-lsp grep text_search skipped single file by glob",
				"root", root,
				"candidate", candidate,
				"glob", glob,
			)
		}
		return matched, err
	}
	return isSearchCandidate(candidate, entry, maxFileBytes)
}

func scanSearchTextFile(ctx context.Context, candidate, searchRoot string, maxFileBytes int, matcher lineMatcher, file *os.File) ([]SearchMatch, error) {
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
			AbsPath:    candidate,
			SearchRoot: searchRoot,
			File:       displayPath(candidate),
			Line:       lineNum,
			Col:        col + 1,
			Text:       truncateSnippet(strings.TrimSpace(line)),
		})
	}
	return results, scanner.Err()
}

func runSGPatternSearch(ctx context.Context, query, language, absPath, root, glob string) ([]SearchMatch, error) {
	args := []string{"run", "--pattern", query, "--lang", astGrepLanguageID(language), "--json=stream"}
	if glob := strings.TrimSpace(glob); glob != "" {
		args = append(args, "--globs", glob)
	}
	args = append(args, absPath)

	cmd := hiddenCommandContext(ctx, "sg", args...)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		if isSGNoMatchExitCodeOneWithoutStderr(err) {
			return []SearchMatch{}, nil
		}
		return nil, formatSGCommandError("sg run", err)
	}
	return decodeSGMatches(output, root)
}

// isLikelyNodeType returns true when the query looks like a tree-sitter node
// type name (e.g. "function_declaration", "type_spec") rather than an ast-grep
// code pattern. Node types are strictly lowercase letters and underscores.
// isLikelyNodeType 判断likely节点type是否可用。
func isLikelyNodeType(query string) bool {
	if len(query) < 4 || !strings.Contains(query, "_") {
		return false
	}
	for _, ch := range query {
		if ch != '_' && (ch < 'a' || ch > 'z') {
			return false
		}
	}
	return true
}

// runSGKindSearch executes `sg scan --rule <tmpfile>` to find AST nodes by
// their tree-sitter kind (e.g. function_declaration, type_spec).
// runSGKindSearch 运行sgkindsearch。
func runSGKindSearch(ctx context.Context, kind, language, absPath, root, glob string) ([]SearchMatch, error) {
	rule := fmt.Sprintf("id: kind-search\nlanguage: %s\nrule:\n  kind: %s\n", astGrepLanguageID(language), kind)

	tmpFile, err := os.CreateTemp("", "sg-rule-*.yml")
	if err != nil {
		return nil, fmt.Errorf("create temp rule file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.WriteString(rule); err != nil {
		if closeErr := tmpFile.Close(); closeErr != nil {
			return nil, fmt.Errorf("write temp rule file: %w (close temp rule file: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("write temp rule file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("close temp rule file: %w", err)
	}

	args := []string{"scan", "--rule", tmpFile.Name(), "--json"}
	if g := strings.TrimSpace(glob); g != "" {
		args = append(args, "--globs", g)
	}
	args = append(args, absPath)

	cmd := hiddenCommandContext(ctx, "sg", args...)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		if isSGNoMatchExitCodeOneWithoutStderr(err) {
			return []SearchMatch{}, nil
		}
		return nil, formatSGCommandError("sg scan", err)
	}
	// sg scan --json outputs a JSON array, not NDJSON like sg run --json=stream.
	return decodeSGScanMatches(output, root)
}

// decodeSGScanMatches parses the JSON array output from `sg scan --json`.
func decodeSGScanMatches(output []byte, root string) ([]SearchMatch, error) {
	var items []sgStreamMatch
	if err := json.Unmarshal(output, &items); err != nil {
		return nil, fmt.Errorf("sg scan json: %w", err)
	}
	results := make([]SearchMatch, 0, len(items))
	for _, item := range items {
		absPath := item.File
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(root, item.File)
		}
		absPath = filepath.Clean(absPath)
		results = append(results, SearchMatch{
			AbsPath:    absPath,
			SearchRoot: root,
			File:       displayPath(absPath),
			Line:       item.Range.Start.Line + 1,
			Col:        item.Range.Start.Column + 1,
			Text:       collapseSnippet(item.Lines, item.Text),
		})
	}
	return results, nil
}

// decodeSGMatches 解码sgmatches。
func decodeSGMatches(output []byte, root string) ([]SearchMatch, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 2<<20)
	results := make([]SearchMatch, 0, 16)
	line := 0
	for scanner.Scan() {
		line++
		var item sgStreamMatch
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("sg json line %d: %w", line, err)
		}
		absPath := item.File
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(root, item.File)
		}
		absPath = filepath.Clean(absPath)
		results = append(results, SearchMatch{
			AbsPath:    absPath,
			SearchRoot: root,
			File:       displayPath(absPath),
			Line:       item.Range.Start.Line + 1,
			Col:        item.Range.Start.Column + 1,
			Text:       collapseSnippet(item.Lines, item.Text),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("sg json scan: %w", err)
	}
	return results, nil
}

func validateSearchGlob(rawGlob string) error {
	glob := filepath.ToSlash(strings.TrimSpace(rawGlob))
	if glob == "" {
		return nil
	}
	for _, pattern := range []string{glob} {
		if _, err := path.Match(pattern, "probe"); err != nil {
			return fmt.Errorf("invalid glob %q: %w", rawGlob, err)
		}
	}
	return nil
}

// matchesPathGlob 判断路径glob是否匹配。
func matchesPathGlob(root, candidate, rawGlob string) (bool, error) {
	glob := filepath.ToSlash(strings.TrimSpace(rawGlob))
	if glob == "" {
		return true, nil
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, fmt.Errorf("resolve relative path for glob %s: %w", candidate, err)
	}
	slashRel := filepath.ToSlash(rel)
	ok, err := matchesCompiledGlob(glob, slashRel)
	if err != nil || ok {
		return ok, err
	}
	if strings.Contains(glob, "/") {
		return false, nil
	}
	return matchesCompiledGlob(glob, path.Base(slashRel))
}

func matchesCompiledGlob(pattern, candidate string) (bool, error) {
	if strings.Contains(pattern, "**") {
		return matchesGlobSegments(strings.Split(pattern, "/"), strings.Split(candidate, "/"))
	}
	ok, err := path.Match(pattern, candidate)
	if err != nil {
		return false, fmt.Errorf("invalid glob %q: %w", pattern, err)
	}
	return ok, nil
}

// matchesGlobSegments 判断globsegments是否匹配。
func matchesGlobSegments(patterns, candidates []string) (bool, error) {
	for len(patterns) > 0 {
		if patterns[0] == "**" {
			patterns = patterns[1:]
			if len(patterns) == 0 {
				return true, nil
			}
			for i := 0; i <= len(candidates); i++ {
				ok, err := matchesGlobSegments(patterns, candidates[i:])
				if err != nil || ok {
					return ok, err
				}
			}
			return false, nil
		}
		if len(candidates) == 0 {
			return false, nil
		}
		ok, err := path.Match(patterns[0], candidates[0])
		if err != nil {
			return false, fmt.Errorf("invalid glob segment %q: %w", patterns[0], err)
		}
		if !ok {
			return false, nil
		}
		patterns = patterns[1:]
		candidates = candidates[1:]
	}
	return len(candidates) == 0, nil
}

func formatSGCommandError(prefix string, err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		if stderr != "" {
			return fmt.Errorf("%s: %w: %s", prefix, err, stderr)
		}
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func isSGNoMatchExitCodeOneWithoutStderr(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) &&
		exitErr.ExitCode() == 1 &&
		len(bytes.TrimSpace(exitErr.Stderr)) == 0
}

func normalizeASTLanguage(raw, target string, isDir bool, glob string) (string, error) {
	if normalized := normalizeLanguageAlias(raw); normalized != "" {
		return normalized, nil
	}
	if inferred := inferASTLanguage(target, isDir, glob); inferred != "" {
		return inferred, nil
	}
	return "", errors.New("language is required for ast_search")
}

func astGrepLanguageID(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "javascriptreact":
		return "jsx"
	case "typescriptreact":
		return "tsx"
	default:
		return strings.ToLower(strings.TrimSpace(language))
	}
}

func inferASTLanguage(target string, isDir bool, glob string) string {
	if !isDir {
		if inferred := inferLanguage(target); inferred != "" {
			return inferred
		}
	}
	return inferLanguage(glob)
}
