package search

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// SearchMatch 是 text/AST 搜索返回的单条命中，函数范围由 LSP/静态补充后可选填充。
type SearchMatch struct {
	AbsPath, SearchRoot string `json:"-"`
	limitReached        bool
	File                string `json:"file"`
	Line                int    `json:"line"`
	Col                 int    `json:"col"`
	Text                string `json:"text"`
	FuncStart           int    `json:"func_start,omitempty"`
	FuncEnd             int    `json:"func_end,omitempty"`
}

// TextSearchOptions 描述文本搜索输入，Root/Roots 是可信边界，Path/Paths 是用户选择的搜索范围。
type TextSearchOptions struct {
	Root, Path, Glob, Query  string
	Roots, Paths             []string
	Regex                    bool
	CaseSensitive            *bool
	MaxResults, MaxFileBytes int
}

// ASTSearchOptions 描述 ast-grep 搜索输入，Language 为空时按目标路径推断。
type ASTSearchOptions struct {
	Root, Path, Glob, Query  string
	Roots, Paths             []string
	Language                 string
	MaxResults, MaxFileBytes int
}

type lineMatcher = shared.LineMatcher

// sgStreamMatch 是 ast-grep JSON stream 的原始单条输出。
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

var errSearchResultsLimitReached = errors.New("search results limit reached")

func maxResultsReached(count, maxResults int) bool { return maxResults > 0 && count >= maxResults }

// SearchText 在可信根目录内执行逐行文本搜索。
// 路径和 glob 会先校验，symlink、二进制和超限文件会被跳过而不会越过 workspace 边界。
func SearchText(ctx context.Context, opts TextSearchOptions) ([]SearchMatch, error) {
	caseSensitive := opts.CaseSensitive != nil && *opts.CaseSensitive || opts.CaseSensitive == nil && strings.ToLower(opts.Query) != opts.Query
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
	return searchTextResolvedPaths(ctx, opts, matcher, searchPaths)
}

// searchTextResolvedPaths 在已解析可信路径上执行文本搜索，并在达到 max_results 后停止遍历。
func searchTextResolvedPaths(ctx context.Context, opts TextSearchOptions, matcher lineMatcher, searchPaths []searchPathStat) ([]SearchMatch, error) {
	results := make([]SearchMatch, 0, maxInt(opts.MaxResults, 8))
	for _, searchPath := range searchPaths {
		if maxResultsReached(len(results), opts.MaxResults) {
			results[len(results)-1].limitReached = true
			break
		}
		if !searchPath.Info.IsDir() {
			found, err := searchTextFile(ctx, searchPath.Path.AbsPath, searchPath.Path.AbsPath, searchPath.Path.Root, opts.Glob, opts.MaxFileBytes, matcher, opts.MaxResults-len(results))
			if stop, err := appendSearchResults(&results, found, err); err != nil {
				return nil, err
			} else if stop {
				break
			}
			continue
		}
		if err := filepath.WalkDir(searchPath.Path.AbsPath, func(candidate string, entry os.DirEntry, walkErr error) error {
			return walkSearchEntry(ctx, searchPath.Path.AbsPath, candidate, searchPath.Path.Root, opts.Glob, opts.MaxFileBytes, matcher, opts.MaxResults, &results, entry, walkErr)
		}); err != nil {
			if errors.Is(err, errSearchResultsLimitReached) {
				break
			}
			return nil, err
		}
	}
	return results, nil
}

// SearchAST 使用 ast-grep 在可信根目录内执行结构化搜索。
// query 为空或 sg 不可用会立即报错；节点类型查询和模式查询走不同命令以保持输出可解析。
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
	results := make([]SearchMatch, 0, maxInt(opts.MaxResults, 8))
	for _, searchPath := range searchPaths {
		if maxResultsReached(len(results), opts.MaxResults) {
			results[len(results)-1].limitReached = true
			break
		}
		language, err := normalizeASTLanguage(opts.Language, searchPath.Path.AbsPath, searchPath.Info.IsDir(), opts.Glob)
		if err != nil {
			return nil, err
		}
		var found []SearchMatch
		if isLikelyNodeType(query) {
			found, err = runSGKindSearch(ctx, query, language, searchPath.Path.AbsPath, searchPath.Path.Root, opts.Glob, opts.MaxResults-len(results))
		} else {
			found, err = runSGPatternSearch(ctx, query, language, searchPath.Path.AbsPath, searchPath.Path.Root, opts.Glob, opts.MaxResults-len(results))
		}
		if stop, err := appendSearchResults(&results, found, err); err != nil {
			return nil, err
		} else if stop {
			break
		}
	}
	return results, nil
}

func appendSearchResults(results *[]SearchMatch, found []SearchMatch, err error) (bool, error) {
	if err == nil {
		*results = append(*results, found...)
		return false, nil
	}
	if errors.Is(err, errSearchResultsLimitReached) {
		found[len(found)-1].limitReached = true
		*results = append(*results, found...)
		return true, nil
	}
	return false, err
}

// FilterAndCapSearchMatches 去重、排除默认跳过路径并按文件稳定截断结果。
// 返回 total 保留截断前数量；搜索层达上限时即使只返回有限集，也要保留截断提示。
func FilterAndCapSearchMatches(matches []SearchMatch, maxResults int) ([]SearchMatch, int, bool) {
	filtered := make([]SearchMatch, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	limitReached := false
	for _, match := range matches {
		limitReached = limitReached || match.limitReached
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
		a, b := filtered[i], filtered[j]
		return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col), strings.Compare(a.Text, b.Text)) < 0
	})

	total := len(filtered)
	if maxResults <= 0 {
		return filtered, total, false
	}
	capped, total, truncated := capSearchMatchesPerFile(filtered, total, maxResults)
	return capped, total, truncated || limitReached
}

func shouldExcludeSearchMatch(match SearchMatch) bool {
	filterPath := match.AbsPath
	if relPath := relativeSearchMatchPath(match.SearchRoot, match.AbsPath); relPath != "" {
		filterPath = relPath
	}
	return shouldExcludePath(filterPath)
}

// relativeSearchMatchPath 返回命中相对搜索根的路径。
// 无法计算相对路径时返回空，调用方会回退到绝对路径排除逻辑。
func relativeSearchMatchPath(root, absPath string) string {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(absPath) == "" {
		return ""
	}
	relPath, err := filepath.Rel(filepath.Clean(root), filepath.Clean(absPath))
	if err != nil || relPath == "" || relPath == "." || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
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

// statSearchPaths 解析并 stat 一个或多个搜索入口。
// 显式 paths 优先；单个 path 失败时才尝试按空白/逗号拆分，避免静默扩大搜索范围。
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
	return strings.FieldsFunc(rawPath, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

// walkSearchEntry 处理 WalkDir 遍历到的单个候选项。
// 目录会按跳过规则剪枝，文件必须通过 glob、大小和二进制检查后才读取。
func walkSearchEntry(ctx context.Context, root, candidate, searchRoot, glob string, maxFileBytes int, matcher lineMatcher, maxResults int, results *[]SearchMatch, entry os.DirEntry, walkErr error) error {
	if maxResultsReached(len(*results), maxResults) {
		(*results)[len(*results)-1].limitReached = true
		return errSearchResultsLimitReached
	}
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
		if shouldSkipDir(entry.Name()) || isInsideGoModCache(candidate) {
			return filepath.SkipDir
		}
		return nil
	}
	return walkSearchFile(ctx, root, candidate, searchRoot, glob, maxFileBytes, matcher, maxResults, results, entry)
}

func walkSearchFile(ctx context.Context, root, candidate, searchRoot, glob string, maxFileBytes int, matcher lineMatcher, maxResults int, results *[]SearchMatch, entry os.DirEntry) error {
	selected, err := shouldSearchPath(root, candidate, glob, maxFileBytes, entry)
	if err != nil {
		return err
	}
	if !selected {
		return nil
	}
	found, err := searchTextFile(ctx, root, candidate, searchRoot, glob, maxFileBytes, matcher, maxResults-len(*results))
	if stop, err := appendSearchResults(results, found, err); err != nil {
		return fmt.Errorf("search %s: %w", candidate, err)
	} else if stop {
		return errSearchResultsLimitReached
	}
	return nil
}

func searchTextFile(ctx context.Context, root, candidate, searchRoot, glob string, maxFileBytes int, matcher lineMatcher, maxResults int) ([]SearchMatch, error) {
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

	return scanSearchTextFile(ctx, candidate, searchRoot, maxFileBytes, matcher, maxResults, file)
}

func shouldSearchFile(root, candidate, glob string, maxFileBytes int) (bool, error) {
	info, err := os.Lstat(candidate)
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", candidate, err)
	}
	return shouldSearchPath(root, candidate, glob, maxFileBytes, fs.FileInfoToDirEntry(info))
}

// shouldSearchPath 判断候选文件是否满足 glob 和文件类型限制。
// 单文件被 glob 排除时记录日志，帮助调用方理解为什么没有命中。
func shouldSearchPath(root, candidate, glob string, maxFileBytes int, entry os.DirEntry) (bool, error) {
	matched, err := matchesPathGlob(root, candidate, glob)
	if err != nil || !matched {
		return matched, err
	}
	return isSearchCandidate(candidate, entry, maxFileBytes)
}

func scanSearchTextFile(ctx context.Context, candidate, searchRoot string, maxFileBytes int, matcher lineMatcher, maxResults int, file *os.File) ([]SearchMatch, error) {
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
		match := SearchMatch{
			AbsPath:    candidate,
			SearchRoot: searchRoot,
			File:       displayPath(candidate),
			Line:       lineNum,
			Col:        col + 1,
			Text:       truncateSnippet(strings.TrimSpace(line)),
		}
		if maxResultsReached(len(results), maxResults) {
			return results, errSearchResultsLimitReached
		}
		results = append(results, match)
	}
	return results, scanner.Err()
}

func runSGPatternSearch(ctx context.Context, query, language, absPath, root, glob string, maxResults int) ([]SearchMatch, error) {
	args := []string{"run", "--pattern", query, "--lang", astGrepLanguageID(language), "--json=stream"}
	if glob := strings.TrimSpace(glob); glob != "" {
		args = append(args, "--globs", glob)
	}
	args = append(args, absPath)

	return runSGStreaming(ctx, "sg run", args, root, maxResults, decodeSGMatchesReader)
}

// isLikelyNodeType 判断 query 是否像 tree-sitter 节点类型而不是 ast-grep 代码模式。
// 仅接受小写字母和下划线，避免普通代码片段被误走 kind 搜索。
func isLikelyNodeType(query string) bool {
	if len(query) < 4 || !strings.Contains(query, "_") {
		return false
	}
	return !strings.ContainsFunc(query, func(ch rune) bool { return ch != '_' && (ch < 'a' || ch > 'z') })
}

// runSGKindSearch 通过临时 ast-grep rule 按 tree-sitter kind 搜索节点。
// `sg scan --json` 输出数组，因此结果解码路径与 `sg run --json=stream` 分开处理。
func runSGKindSearch(ctx context.Context, kind, language, absPath, root, glob string, maxResults int) ([]SearchMatch, error) {
	rule := fmt.Sprintf("id: kind-search\nlanguage: %s\nrule:\n  kind: %s\n", astGrepLanguageID(language), kind)

	tmpFile, err := os.CreateTemp("", "sg-rule-*.yml")
	if err != nil {
		return nil, fmt.Errorf("create temp rule file: %w", err)
	}
	rulePath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("close temp rule file: %w", err)
	}
	defer func() { _ = os.Remove(rulePath) }()
	if err := os.WriteFile(rulePath, []byte(rule), 0o600); err != nil {
		return nil, fmt.Errorf("write temp rule file: %w", err)
	}

	args := []string{"scan", "--rule", rulePath, "--json"}
	if g := strings.TrimSpace(glob); g != "" {
		args = append(args, "--globs", g)
	}
	args = append(args, absPath)

	// sg scan --json 输出 JSON 数组，不是 sg run --json=stream 的逐行 JSON。
	return runSGStreaming(ctx, "sg scan", args, root, maxResults, decodeSGScanMatchesReader)
}

type sgDecodeFunc func(io.Reader, string, int, context.CancelFunc) ([]SearchMatch, error)

// runSGStreaming 启动 ast-grep 并边读 stdout 边解码命中。
// resultLimiter 达到上限时会 cancel 命令 ctx，避免继续等待 ast-grep 遍历整个工程。
func runSGStreaming(ctx context.Context, label string, args []string, root string, maxResults int, decode sgDecodeFunc) ([]SearchMatch, error) {
	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := hiddenCommandContext(cmdCtx, "sg", args...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s stdout pipe: %w", label, err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s start: %w", label, err)
	}
	matches, decodeErr := decode(stdout, root, maxResults, cancel)
	if decodeErr != nil {
		cancel()
	}
	if errors.Is(decodeErr, errSearchResultsLimitReached) {
		go func() { _ = cmd.Wait() }()
		return matches, errSearchResultsLimitReached
	}
	waitErr := cmd.Wait()
	switch {
	case waitErr != nil && isSGNoMatchExitCodeOneWithoutStderrBytes(waitErr, stderr.Bytes()):
		return []SearchMatch{}, nil
	case waitErr != nil:
		return nil, formatSGCommandErrorWithStderr(label, waitErr, stderr.Bytes())
	case decodeErr != nil:
		return nil, decodeErr
	default:
		return matches, nil
	}
}

// decodeSGScanMatchesReader 解码 sg scan --json 的数组输出，并在达到上限时取消 sg。
func decodeSGScanMatchesReader(reader io.Reader, root string, maxResults int, cancel context.CancelFunc) ([]SearchMatch, error) {
	decoder := json.NewDecoder(reader)
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("sg scan json: %w", err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '[' {
		return nil, errors.New("sg scan json: expected array")
	}
	results := make([]SearchMatch, 0, 16)
	for decoder.More() {
		var item sgStreamMatch
		if err := decoder.Decode(&item); err != nil {
			return nil, fmt.Errorf("sg scan json: %w", err)
		}
		if maxResultsReached(len(results), maxResults) {
			cancel()
			return results, errSearchResultsLimitReached
		}
		results = append(results, sgItemToSearchMatch(item, root))
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("sg scan json: %w", err)
	}
	return results, nil
}

func decodeSGMatchesReader(reader io.Reader, root string, maxResults int, cancel context.CancelFunc) ([]SearchMatch, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 2<<20)
	results := make([]SearchMatch, 0, 16)
	line := 0
	for scanner.Scan() {
		line++
		var item sgStreamMatch
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("sg json line %d: %w", line, err)
		}
		if maxResultsReached(len(results), maxResults) {
			cancel()
			return results, errSearchResultsLimitReached
		}
		results = append(results, sgItemToSearchMatch(item, root))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("sg json scan: %w", err)
	}
	return results, nil
}

func sgItemToSearchMatch(item sgStreamMatch, root string) SearchMatch {
	absPath := item.File
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(root, item.File)
	}
	absPath = filepath.Clean(absPath)
	return SearchMatch{
		AbsPath:    absPath,
		SearchRoot: root,
		File:       displayPath(absPath),
		Line:       item.Range.Start.Line + 1,
		Col:        item.Range.Start.Column + 1,
		Text:       collapseSnippet(item.Lines, item.Text),
	}
}

func validateSearchGlob(rawGlob string) error {
	glob := filepath.ToSlash(strings.TrimSpace(rawGlob))
	if glob == "" {
		return nil
	}
	if _, err := path.Match(glob, "probe"); err != nil {
		return fmt.Errorf("invalid glob %q: %w", rawGlob, err)
	}
	return nil
}

// matchesPathGlob 判断候选文件是否匹配用户提供的 glob。
// 含路径分隔符的 glob 按相对路径匹配，裸文件名 glob 额外尝试 basename。
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

// matchesGlobSegments 递归匹配支持 `**` 的路径 glob 片段。
// 任一片段语法非法会返回错误，而不是把非法 glob 当作无匹配吞掉。
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

func formatSGCommandErrorWithStderr(prefix string, err error, stderrBytes []byte) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if len(stderrBytes) == 0 {
			stderrBytes = exitErr.Stderr
		}
		stderr := strings.TrimSpace(string(stderrBytes))
		if stderr != "" {
			return fmt.Errorf("%s: %w: %s", prefix, err, stderr)
		}
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func isSGNoMatchExitCodeOneWithoutStderrBytes(err error, stderrBytes []byte) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return false
	}
	if len(stderrBytes) == 0 {
		stderrBytes = exitErr.Stderr
	}
	return len(bytes.TrimSpace(stderrBytes)) == 0
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
	normalized := strings.ToLower(strings.TrimSpace(language))
	switch normalized {
	case "javascriptreact":
		return "jsx"
	case "typescriptreact":
		return "tsx"
	default:
		return normalized
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
