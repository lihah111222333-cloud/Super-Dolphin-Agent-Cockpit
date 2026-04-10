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

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
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
		".jsx":      "javascript",
		".markdown": "markdown",
		".md":       "markdown",
		".mjs":      "javascript",
		".mts":      "typescript",
		".py":       "python",
		".pyi":      "python",
		".rs":       "rust",
		".ts":       "typescript",
		".tsx":      "typescript",
		".yaml":     "yaml",
		".yml":      "yaml",
	}
	languageAliases = map[string]string{
		"c":          "c",
		"c++":        "cpp",
		"cpp":        "cpp",
		"css":        "css",
		"cxx":        "cpp",
		"go":         "go",
		"golang":     "go",
		"html":       "html",
		"java":       "java",
		"javascript": "javascript",
		"js":         "javascript",
		"json":       "json",
		"markdown":   "markdown",
		"md":         "markdown",
		"py":         "python",
		"python":     "python",
		"rs":         "rust",
		"rust":       "rust",
		"ts":         "typescript",
		"typescript": "typescript",
		"yaml":       "yaml",
		"yml":        "yaml",
	}
)

type PathInfo struct {
	Root        string
	AbsPath     string
	DisplayPath string
}

type FileContent struct {
	Path       PathInfo
	Content    string
	TotalLines int
}

type validatedFile struct {
	PathInfo PathInfo
	Content  []byte
}

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

func ResolvePath(root, target string) (PathInfo, error) {
	normalizedRoot, err := NormalizeRoot(root)
	if err != nil {
		return PathInfo{}, err
	}
	candidate, err := absoluteCandidatePath(normalizedRoot, target)
	if err != nil {
		return PathInfo{}, err
	}
	resolved, err := resolveExistingPath(candidate)
	if err != nil {
		return PathInfo{}, err
	}
	if err := ensureWithinRoot(normalizedRoot, resolved); err != nil {
		return PathInfo{}, err
	}
	return PathInfo{
		Root:        normalizedRoot,
		AbsPath:     resolved,
		DisplayPath: displayPath(resolved),
	}, nil
}

func ReadToolFileContent(root, target string, maxBytes int) (FileContent, error) {
	file, err := readValidatedFile(root, target, maxBytes)
	if err != nil {
		return FileContent{}, err
	}
	return FileContent{
		Path:       file.PathInfo,
		Content:    string(file.Content),
		TotalLines: countNormalizedLines(string(file.Content)),
	}, nil
}

func readValidatedFile(root, target string, maxBytes int) (validatedFile, error) {
	pathInfo, err := ResolvePath(root, target)
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

func isSearchCandidate(path string, entry os.DirEntry, maxBytes int) bool {
	if entry == nil {
		return false
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return false
	}
	info, err := entry.Info()
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		return false
	}
	if maxBytes > 0 && info.Size() > int64(maxBytes) {
		return false
	}
	return !isBinaryFile(path)
}

func isBinaryFile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return true
	}
	defer func() { _ = file.Close() }()

	buf := make([]byte, binaryProbeBytes)
	n, err := file.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return true
	}
	return isBinaryContent(buf[:n])
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

func shouldSkipDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".cache", ".git", "__pycache__", "build", "coverage", "dist", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func shouldExcludePath(path string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(path))
	for _, segment := range []string{".cache", ".git", "__pycache__", "build", "coverage", "dist", "node_modules", "vendor"} {
		if strings.Contains(cleaned, "/"+segment+"/") || strings.HasSuffix(cleaned, "/"+segment) {
			return true
		}
	}
	return false
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
	return strings.TrimSpace(text)
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
