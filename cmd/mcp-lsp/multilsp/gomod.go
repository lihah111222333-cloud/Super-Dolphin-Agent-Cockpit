package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

type goWorkEditJSON struct {
	Use []struct {
		DiskPath string `json:"DiskPath"`
	} `json:"Use"`
}

func parseGoWorkModuleRoots(goWorkPath string) ([]string, error) {
	roots, err := parseGoWorkModuleRootsWithGoCommand(goWorkPath)
	if err == nil {
		return cleanSortedUniquePaths(roots), nil
	}
	if _, ok := errors.AsType[*exec.Error](err); !ok {
		return nil, err
	}
	return parseGoWorkModuleRootsFallback(goWorkPath)
}

// parseGoWorkModuleRootsWithGoCommand 通过 `go work edit -json` 读取 go.work 的 use 列表。
// 命令 stderr 会进入错误链，便于上层区分语法错误和 go 命令缺失。
func parseGoWorkModuleRootsWithGoCommand(goWorkPath string) ([]string, error) {
	cmd := hiddenexec.Command("go", "work", "edit", "-json", goWorkPath)
	cmd.Dir = filepath.Dir(goWorkPath)
	output, err := cmd.Output()
	if err != nil {
		if exitErr := new(exec.ExitError); errors.As(err, &exitErr) {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if stderr != "" {
				return nil, fmt.Errorf("parse go.work with go command: %w: %s", err, stderr)
			}
		}
		return nil, err
	}
	var parsed goWorkEditJSON
	if err := json.Unmarshal(output, &parsed); err != nil {
		return nil, fmt.Errorf("decode go work edit -json: %w", err)
	}
	workDir := filepath.Dir(goWorkPath)
	roots := make([]string, 0, len(parsed.Use))
	for _, use := range parsed.Use {
		roots = appendGoWorkUseRoot(roots, workDir, use.DiskPath)
	}
	return roots, nil
}

func parseGoWorkModuleRootsFallback(goWorkPath string) ([]string, error) {
	data, err := os.ReadFile(goWorkPath)
	if err != nil {
		return nil, err
	}
	parser := goWorkUseParser{workDir: filepath.Dir(goWorkPath)}
	for rawLine := range strings.SplitSeq(string(data), "\n") {
		parser.parseLine(rawLine)
	}
	return cleanSortedUniquePaths(parser.roots), nil
}

type goWorkUseParser struct {
	workDir    string
	roots      []string
	inUseBlock bool
}

func (p *goWorkUseParser) parseLine(rawLine string) {
	fields := goWorkFields(rawLine)
	if len(fields) == 0 {
		return
	}
	if p.inUseBlock {
		p.parseUseFields(fields)
		return
	}
	if fields[0] == "use" {
		p.parseUseFields(fields[1:])
	}
}

func goWorkFields(rawLine string) []string {
	return tokenizeGoWorkLine(rawLine)
}

// tokenizeGoWorkLine 解析 go.work 单行中的 use token。
// 它保留括号边界并跳过行尾注释，供 fallback parser 在没有 go 命令时使用。
func tokenizeGoWorkLine(rawLine string) []string {
	line := strings.TrimSpace(rawLine)
	tokens := make([]string, 0, 4)
	for i := 0; i < len(line); {
		i = skipGoWorkSpace(line, i)
		if i >= len(line) || strings.HasPrefix(line[i:], "//") {
			break
		}
		if line[i] == '(' || line[i] == ')' {
			tokens = append(tokens, line[i:i+1])
			i++
			continue
		}
		token, next := scanGoWorkToken(line, i)
		i = next
		if token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func skipGoWorkSpace(line string, i int) int {
	for i < len(line) && (line[i] == ' ' || line[i] == '\t' || line[i] == '\r') {
		i++
	}
	return i
}

func scanGoWorkToken(line string, start int) (string, int) {
	if line[start] == '"' || line[start] == '`' {
		return scanGoWorkQuotedToken(line, start)
	}
	i := start
	for i < len(line) && !isGoWorkTokenBoundary(line, i) {
		i++
	}
	return line[start:i], i
}

func scanGoWorkQuotedToken(line string, start int) (string, int) {
	quote := line[start]
	i := start + 1
	for i < len(line) {
		if quote == '"' && line[i] == '\\' {
			i += 2
			continue
		}
		if line[i] == quote {
			return line[start : i+1], i + 1
		}
		i++
	}
	return line[start:], len(line)
}

func isGoWorkTokenBoundary(line string, i int) bool {
	switch line[i] {
	case ' ', '\t', '\r', '(', ')':
		return true
	default:
		return strings.HasPrefix(line[i:], "//")
	}
}

func (p *goWorkUseParser) parseUseFields(fields []string) {
	for _, field := range fields {
		p.parseUseField(field)
	}
}

func (p *goWorkUseParser) parseUseField(field string) {
	for _, token := range splitGoWorkUseToken(field) {
		switch token {
		case "(":
			p.inUseBlock = true
		case ")":
			p.inUseBlock = false
		default:
			p.roots = appendGoWorkUseRoot(p.roots, p.workDir, token)
		}
	}
}

func splitGoWorkUseToken(field string) []string {
	field = strings.TrimSpace(field)
	if field == "" {
		return nil
	}
	var tokens []string
	for strings.HasPrefix(field, "(") {
		tokens = append(tokens, "(")
		field = strings.TrimPrefix(field, "(")
	}
	closed := strings.HasSuffix(field, ")")
	field = strings.TrimSuffix(field, ")")
	if field != "" {
		tokens = append(tokens, field)
	}
	if closed {
		tokens = append(tokens, ")")
	}
	return tokens
}

// appendGoWorkUseRoot 把 go.work use 项转换为规范化绝对路径后追加到结果。
// 空项、括号和无法规范化的路径会被忽略，避免污染 workspace folder 列表。
func appendGoWorkUseRoot(roots []string, workDir, raw string) []string {
	entry := strings.TrimSpace(raw)
	if unquoted, err := strconv.Unquote(entry); err == nil {
		entry = unquoted
	} else {
		entry = strings.Trim(entry, `"`)
	}
	if entry == "" || entry == ")" || entry == "(" {
		return roots
	}
	if !filepath.IsAbs(entry) {
		entry = filepath.Join(workDir, entry)
	}
	if normalized, err := platformshared.NormalizeAbsolutePath(entry); err == nil && normalized != "" {
		roots = append(roots, normalized)
	}
	return roots
}

// resolveStartDir 把文件、目录或 go.mod 路径转换为向上查找的起点目录。
// stat 失败且不是 NotExist 时会返回错误，避免权限问题被当成普通缺失。
func resolveStartDir(absPath string) (string, error) {
	if strings.EqualFold(filepath.Base(absPath), "go.mod") {
		return filepath.Dir(absPath), nil
	}
	info, statErr := os.Stat(absPath)
	switch {
	case statErr == nil && !info.IsDir():
		return filepath.Dir(absPath), nil
	case statErr != nil && !os.IsNotExist(statErr):
		return "", fmt.Errorf("stat path: %w", statErr)
	case statErr != nil:
		return filepath.Dir(absPath), nil
	default:
		return absPath, nil
	}
}

// absolutePathFromURI 将 file URI 转成规范化绝对路径。
// 空 URI、非 file scheme 或路径规范化失败都会返回错误，调用方不得静默回退。
func absolutePathFromURI(uri string) (string, error) {
	if strings.TrimSpace(uri) == "" {
		return "", ErrDocumentTargetEmpty
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("parse file URI: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "file") {
		return "", fmt.Errorf("unsupported URI scheme: %s", parsed.Scheme)
	}
	path := parsed.Path
	if parsed.Host != "" {
		path = "//" + parsed.Host + path
	}
	if unescaped, err := url.PathUnescape(path); err == nil && unescaped != "" {
		path = unescaped
	}
	return platformshared.NormalizeAbsolutePath(path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func shouldUseJSTSWorkspace(languageID string) bool {
	switch normalizeLanguageID(languageID) {
	case "javascript", "typescript", "javascriptreact", "typescriptreact":
		return true
	default:
		return false
	}
}

func findJSTSProjectRootWithin(ctx context.Context, root string) (string, error) {
	cfg := lspProjectAdapterConfig(contract.LSPServiceJSTS)
	return findProjectRootWithin(ctx, root, cfg.RootMarkers, lspProjectIgnoredDirSet(contract.LSPServiceJSTS))
}

func findJSTSBootstrapFileWithin(ctx context.Context, root string) (string, error) {
	cfg := lspProjectAdapterConfig(contract.LSPServiceJSTS)
	return findBootstrapFileWithin(ctx, root, cfg.FirstSourceExtensions, lspProjectIgnoredDirSet(contract.LSPServiceJSTS))
}

func shouldUseJavaWorkspace(languageID string) bool {
	return normalizeLanguageID(languageID) == "java"
}

func findJavaProjectRoot(path string) (string, error) {
	return findProjectRoot(path, lspProjectAdapterConfig(contract.LSPServiceJava).RootMarkers)
}

func findJavaProjectRootWithin(ctx context.Context, root string) (string, error) {
	cfg := lspProjectAdapterConfig(contract.LSPServiceJava)
	return findProjectRootWithin(ctx, root, cfg.RootMarkers, lspProjectIgnoredDirSet(contract.LSPServiceJava))
}

func findJavaBootstrapFileWithin(ctx context.Context, root string) (string, error) {
	cfg := lspProjectAdapterConfig(contract.LSPServiceJava)
	return findBootstrapFileWithin(ctx, root, cfg.FirstSourceExtensions, lspProjectIgnoredDirSet(contract.LSPServiceJava))
}

func lspProjectAdapterConfig(service string) contract.LSPProjectAdapterConfig {
	return lspConfigWithDefaults(contract.LSPConfig{}).ProjectAdapters[service]
}

func lspProjectIgnoredDirSet(service string) map[string]struct{} {
	cfg := lspConfigWithDefaults(contract.LSPConfig{})
	project := cfg.ProjectAdapters[service]
	names := append([]string(nil), project.IgnoredDirNames...)
	names = append(names, cfg.NoiseDirNames...)
	return stringSetFromList(names)
}

func normalizeLanguageID(languageID string) string {
	languageID = strings.ToLower(strings.TrimSpace(languageID))
	if contract.IsMQLLanguageID(languageID) {
		return "cpp"
	}
	return languageID
}

func fileURIFromPath(absPath string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absPath)}).String()
}
