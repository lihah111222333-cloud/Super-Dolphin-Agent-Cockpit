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

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// binaryProbeBytes 是判断文件是否疑似二进制时读取的前缀字节数。
const binaryProbeBytes = 512

// 语言映射表用于在没有显式 language 参数时从扩展名或别名推断 LSP languageID。
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

// PathInfo 是经过 workspace containment 校验后的文件路径信息。
type PathInfo struct {
	Root        string // 命中的 workspace root。
	AbsPath     string // 规范绝对路径。
	DisplayPath string // 面向工具响应的相对或清理后路径。
}

// FileContent 保存文件读取结果及其规范路径信息。
type FileContent struct {
	Path       PathInfo // 已校验路径。
	Content    string   // 文本内容，二进制文件不会进入这里。
	TotalLines int      // 规范化换行后的总行数。
}

// validatedFile 是读取文件后的内部结构，确保后续搜索只处理已校验候选。
type validatedFile struct {
	PathInfo PathInfo
	Content  []byte
}

// NormalizeRoot 规范化根目录，空 root 会回退到当前目录并解析符号链接。
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

// ResolvePath 在单个 workspace root 内解析目标路径。
func ResolvePath(root, target string) (PathInfo, error) {
	return ResolvePathInRoots(root, nil, target)
}

// ResolvePathInRoots 在多个可信 root 内解析目标路径，越界路径会 fail-fast。
func ResolvePathInRoots(primaryRoot string, additionalRoots []string, target string) (PathInfo, error) {
	roots, err := NormalizeRootSet(primaryRoot, additionalRoots)
	if err != nil {
		return PathInfo{}, err
	}
	root, resolved, err := resolveCandidateInRoots(roots, target)
	if err != nil {
		return PathInfo{}, err
	}
	return PathInfo{
		Root:        root,
		AbsPath:     resolved,
		DisplayPath: displayPath(resolved),
	}, nil
}

// NormalizeRootSet 规范化并去重 workspace roots，保留 primary root 在首位。
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

// ReadToolFileContent 在单一 workspace 根内读取文本文件。
// 该快捷入口复用多根校验，确保 symlink、二进制和超限文件不会被返回。
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

// readValidatedFileInRoots 完成路径 containment、普通文件、大小和二进制校验后读取内容。
// 任一校验失败都会返回错误，避免 file 工具对不可读目标静默降级。
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

// isSearchCandidate 判断 WalkDir 候选文件是否允许进入搜索。
// symlink、目录、超限文件和二进制文件都会被跳过，读取错误则显式返回。
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

// resolveGoModCache 每次从环境变量解析当前 Go module cache。
// 这里不缓存结果，测试通过 t.Setenv 切换 GOMODCACHE/GOPATH 时能立即生效。
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

// isInsideGoModCache 判断路径是否位于 Go module cache。
// 搜索默认排除依赖缓存，避免跨到外部模块源码产生噪声。
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

// isLinuxTopLevelTmpSegment 识别系统顶层 tmp 目录段。
// 顶层临时目录允许继续向下检查，避免把整个绝对路径因包含 tmp 而误排除。
func isLinuxTopLevelTmpSegment(index int, segment, cleanedPath string) bool {
	if segment != "tmp" {
		return false
	}
	// Linux 或 macOS 符号链接解析后的顶层 /tmp 路径。
	if index == 1 && strings.HasPrefix(cleanedPath, "/tmp/") {
		return true
	}
	// macOS 解析后的 /private/tmp 路径。
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
