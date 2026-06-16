package search

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/format"
)

const binaryProbeBytes = 512

var (
	languageByExtension = map[string]string{
		".c":        "c",
		".cc":       "cpp",
		".cjs":      "javascript",
		".cpp":      "cpp",
		".css":      "css",
		".cts":      "typescript",
		".cxx":      "cpp",
		".go":       "go",
		".h":        "c",
		".hpp":      "cpp",
		".htm":      "html",
		".html":     "html",
		".java":     "java",
		".js":       "javascript",
		".json":     "json",
		".jsx":      "javascriptreact",
		".markdown": "markdown",
		".md":       "markdown",
		".mjs":      "javascript",
		".mts":      "typescript",
		".py":       "python",
		".pyi":      "python",
		".rs":       "rust",
		".ts":       "typescript",
		".tsx":      "typescriptreact",
		".yaml":     "yaml",
		".yml":      "yaml",
	}
	languageAliases = map[string]string{
		"c":               "c",
		"c++":             "cpp",
		"cpp":             "cpp",
		"css":             "css",
		"cxx":             "cpp",
		"go":              "go",
		"golang":          "go",
		"html":            "html",
		"java":            "java",
		"javascript":      "javascript",
		"javascriptreact": "javascriptreact",
		"js":              "javascript",
		"json":            "json",
		"markdown":        "markdown",
		"md":              "markdown",
		"py":              "python",
		"python":          "python",
		"rs":              "rust",
		"rust":            "rust",
		"ts":              "typescript",
		"typescript":      "typescript",
		"typescriptreact": "typescriptreact",
		"yaml":            "yaml",
		"yml":             "yaml",
	}
)

// PathInfo describes a search API type.
type PathInfo struct {
	Root        string
	AbsPath     string
	DisplayPath string
}

// FileContent describes a search API type.
type FileContent struct {
	Path       PathInfo
	Content    string
	TotalLines int
}

type validatedFile struct {
	PathInfo PathInfo
	Content  []byte
}

// NormalizeRoot 规范化根目录。
func NormalizeRoot(root string) (string, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}
		trimmed = cwd
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	cleaned := filepath.Clean(absPath)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = filepath.Clean(resolved)
	}
	return cleaned, nil
}

// ResolvePath 解析路径。
func ResolvePath(root, target string) (PathInfo, error) {
	return ResolvePathInRoots(root, nil, target)
}

// ResolvePathInRoots 在根目录解析路径。
func ResolvePathInRoots(primaryRoot string, additionalRoots []string, target string) (PathInfo, error) {
	roots, err := NormalizeRootSet(primaryRoot, additionalRoots)
	if err != nil {
		return PathInfo{}, err
	}
	root, resolved, err := resolveCandidateInRoots(roots, target)
	if err != nil {
		appRoot, appResolved, matched, appErr := resolveAppManagedCandidate(target)
		if appErr != nil {
			return PathInfo{}, appErr
		}
		if !matched {
			return PathInfo{}, err
		}
		root = appRoot
		resolved = appResolved
	}
	return PathInfo{
		Root:        root,
		AbsPath:     resolved,
		DisplayPath: displayPath(resolved),
	}, nil
}

// NormalizeRootSet 规范化根目录set。
func NormalizeRootSet(primaryRoot string, additionalRoots []string) ([]string, error) {
	primary, err := NormalizeRoot(primaryRoot)
	if err != nil {
		return nil, err
	}
	roots := []string{primary}
	seen := map[string]struct{}{primary: {}}
	for _, raw := range additionalRoots {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		root, err := NormalizeRoot(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	return roots, nil
}

func resolveCandidateInRoots(roots []string, target string) (string, string, error) {
	if len(roots) == 0 || strings.TrimSpace(roots[0]) == "" {
		return "", "", fmt.Errorf("workspace root is required")
	}
	trimmed := strings.TrimSpace(target)
	if trimmed == "" || !filepath.IsAbs(trimmed) {
		return resolveRelativeCandidateInRoot(roots[0], trimmed)
	}
	return resolveAbsoluteCandidateInRoots(roots, trimmed)
}

func resolveRelativeCandidateInRoot(root, target string) (string, string, error) {
	candidate, err := absoluteCandidatePath(root, target)
	if err != nil {
		return "", "", err
	}
	resolved, err := resolveExistingPath(candidate)
	if err != nil {
		return "", "", err
	}
	if err := ensureWithinRoot(root, resolved); err != nil {
		return "", "", err
	}
	return root, resolved, nil
}

func resolveAbsoluteCandidateInRoots(roots []string, target string) (string, string, error) {
	candidate, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", "", fmt.Errorf("resolve path: %w", err)
	}
	resolved, err := resolveExistingPath(candidate)
	if err != nil {
		return "", "", err
	}
	root := longestContainingRoot(roots, resolved)
	if root == "" {
		return "", "", outsideWorkspaceRootsError(resolved, roots)
	}
	return root, resolved, nil
}

// resolveAppManagedCandidate 解析appmanaged候选项。
func resolveAppManagedCandidate(target string) (string, string, bool, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" || !filepath.IsAbs(trimmed) {
		return "", "", false, nil
	}
	roots, err := platformshared.AppManagedDataRoots()
	if err != nil {
		return "", "", false, err
	}
	if len(roots) == 0 {
		return "", "", false, nil
	}
	candidate, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", "", false, fmt.Errorf("resolve path: %w", err)
	}
	if longestContainingRoot(roots, candidate) == "" {
		return "", "", false, nil
	}
	root, resolved, err := resolveAbsoluteCandidateInRoots(roots, trimmed)
	if err != nil {
		return "", "", true, err
	}
	return root, resolved, true, nil
}

func longestContainingRoot(roots []string, candidate string) string {
	selected := ""
	for _, root := range roots {
		if !platformshared.ContainsPath(root, candidate) {
			continue
		}
		if len(root) > len(selected) {
			selected = root
		}
	}
	return selected
}

func outsideWorkspaceRootsError(candidate string, roots []string) error {
	return fmt.Errorf("path %s is outside workspace roots [%s]", candidate, strings.Join(roots, ", "))
}

// ReadToolFileContent 读取工具文件内容。
func ReadToolFileContent(root, target string, maxBytes int) (FileContent, error) {
	return ReadToolFileContentInRoots(root, nil, target, maxBytes)
}

// ReadToolFileContentInRoots 在根目录读取工具文件内容。
func ReadToolFileContentInRoots(root string, roots []string, target string, maxBytes int) (FileContent, error) {
	file, err := readValidatedFileInRoots(root, roots, target, maxBytes)
	if err != nil {
		return FileContent{}, err
	}
	return FileContent{
		Path:       file.PathInfo,
		Content:    string(file.Content),
		TotalLines: countNormalizedLines(string(file.Content)),
	}, nil
}

// readValidatedFileInRoots 在根目录读取validated文件。
func readValidatedFileInRoots(root string, roots []string, target string, maxBytes int) (validatedFile, error) {
	pathInfo, err := ResolvePathInRoots(root, roots, target)
	if err != nil {
		return validatedFile{}, err
	}
	info, err := os.Lstat(pathInfo.AbsPath)
	if err != nil {
		return validatedFile{}, fmt.Errorf("stat %s: %w", pathInfo.DisplayPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return validatedFile{}, fmt.Errorf("file_path %q cannot be a symlink", pathInfo.DisplayPath)
	}
	if !info.Mode().IsRegular() {
		return validatedFile{}, fmt.Errorf("file_path %q must reference a regular file", pathInfo.DisplayPath)
	}
	if maxBytes > 0 && info.Size() > int64(maxBytes) {
		return validatedFile{}, fmt.Errorf("file_path %q exceeds %d byte limit", pathInfo.DisplayPath, maxBytes)
	}
	content, err := os.ReadFile(pathInfo.AbsPath)
	if err != nil {
		return validatedFile{}, fmt.Errorf("read %s: %w", pathInfo.DisplayPath, err)
	}
	if isBinaryContent(content) {
		return validatedFile{}, fmt.Errorf("file_path %q appears to be binary", pathInfo.DisplayPath)
	}
	return validatedFile{PathInfo: pathInfo, Content: content}, nil
}

// isSearchCandidate 判断search候选项是否可用。
func isSearchCandidate(path string, entry os.DirEntry, maxBytes int) (bool, error) {
	if entry == nil {
		return false, fmt.Errorf("missing dir entry for %s", path)
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return false, nil
	}
	info, err := entry.Info()
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	if maxBytes > 0 && info.Size() > int64(maxBytes) {
		return false, nil
	}
	binary, err := isBinaryFile(path)
	if err != nil {
		return false, err
	}
	return !binary, nil
}

func isBinaryFile(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open %s for binary probe: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	buf := make([]byte, binaryProbeBytes)
	n, err := file.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read %s for binary probe: %w", path, err)
	}
	return isBinaryContent(buf[:n]), nil
}

func isBinaryContent(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	if len(content) > binaryProbeBytes {
		content = content[:binaryProbeBytes]
	}
	return bytes.IndexByte(content, 0) >= 0
}

func absoluteCandidatePath(root, target string) (string, error) {
	trimmed := strings.TrimSpace(target)
	candidate := root
	if trimmed != "" {
		if filepath.IsAbs(trimmed) {
			candidate = trimmed
		} else {
			candidate = filepath.Join(root, trimmed)
		}
	}
	absPath, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	return absPath, nil
}

func resolveExistingPath(absPath string) (string, error) {
	parent := filepath.Dir(absPath)
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("resolve parent path: %w", err)
	}
	return filepath.Join(parentReal, filepath.Base(absPath)), nil
}

func ensureWithinRoot(root, candidate string) error {
	if !platformshared.ContainsPath(root, candidate) {
		return fmt.Errorf("path %q is outside workspace root %q", candidate, root)
	}
	return nil
}

func displayPath(absPath string) string {
	return format.URIToPath(fileURI(absPath))
}

func fileURI(absPath string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absPath)}).String()
}

func countNormalizedLines(content string) int {
	if content == "" {
		return 1
	}
	return len(strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n"))
}

// resolveGoModCache returns the current GOMODCACHE path from environment.
// Called per-walk (not cached) to support test isolation via t.Setenv.
func resolveGoModCache() string {
	if dir := os.Getenv("GOMODCACHE"); dir != "" {
		return filepath.Clean(dir)
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		return filepath.Join(gopath, "pkg", "mod")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "go", "pkg", "mod")
}

// isInsideGoModCache checks if absPath is inside the Go module cache.
func isInsideGoModCache(absPath string) bool {
	cache := resolveGoModCache()
	if cache != "" {
		cleaned := filepath.Clean(absPath)
		if strings.HasPrefix(cleaned, cache+string(filepath.Separator)) || cleaned == cache {
			return true
		}
	}
	slashed := filepath.ToSlash(absPath)
	return strings.Contains(slashed, "/go/pkg/mod/")
}

func shouldSkipDir(name string) bool {
	_, ok := skippedDirNames[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func shouldExcludePath(path string) bool {
	cleaned := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	for index, segment := range strings.Split(cleaned, "/") {
		if isLinuxTopLevelTmpSegment(index, segment, cleaned) {
			continue
		}
		if _, ok := skippedDirNames[segment]; ok {
			return true
		}
	}
	return false
}

// isLinuxTopLevelTmpSegment 判断Linuxtopleveltmpsegment是否可用。
func isLinuxTopLevelTmpSegment(index int, segment, cleanedPath string) bool {
	if segment != "tmp" {
		return false
	}
	// /tmp/... (Linux/macOS symlink target)
	if index == 1 && strings.HasPrefix(cleanedPath, "/tmp/") {
		return true
	}
	// /private/tmp/... (macOS resolved symlink)
	if index == 2 && strings.HasPrefix(cleanedPath, "/private/tmp/") {
		return true
	}
	return false
}

var skippedDirNames = map[string]struct{}{
	".agent":        {},
	".agents":       {},
	".build-cache":  {},
	".cache":        {},
	".cargo":        {},
	".claude":       {},
	".dart_tool":    {},
	".eslintcache":  {},
	".git":          {},
	".gomodcache":   {},
	".gradle":       {},
	".mvn":          {},
	".mypy_cache":   {},
	".next":         {},
	".nuxt":         {},
	".parcel-cache": {},
	".pytest_cache": {},
	".ruff_cache":   {},
	".svelte-kit":   {},
	".tox":          {},
	".turbo":        {},
	".venv":         {},
	".workspace":    {},
	".worktrees":    {},
	"__pycache__":   {},
	"build":         {},
	"cache":         {},
	"coverage":      {},
	"dist":          {},
	"gomodcache":    {},
	"node_modules":  {},
	"target":        {},
	"tmp":           {},
	"vendor":        {},
	"venv":          {},
}

func inferLanguage(value string) string {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(value)))
	if ext == "" {
		return ""
	}
	return languageByExtension[ext]
}

func normalizeLanguageAlias(value string) string {
	return languageAliases[strings.ToLower(strings.TrimSpace(value))]
}

func collapseSnippet(primary, fallback string) string {
	text := strings.TrimSpace(primary)
	if text == "" {
		text = strings.TrimSpace(fallback)
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	return truncateSnippet(strings.TrimSpace(text))
}

func truncateSnippet(text string) string {
	const maxSnippetRunes = 150
	runes := []rune(text)
	if len(runes) <= maxSnippetRunes {
		return text
	}
	return string(runes[:maxSnippetRunes]) + "..."
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
