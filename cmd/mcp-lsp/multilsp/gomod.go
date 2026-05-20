package multilsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func findGoModRoot(path string) (string, error) {
	goModPath, err := findGoModPath(path)
	if err != nil || goModPath == "" {
		return "", err
	}
	return filepath.Dir(goModPath), nil
}

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
	var execErr *exec.Error
	if !errors.As(err, &execErr) {
		return nil, err
	}
	return parseGoWorkModuleRootsFallback(goWorkPath)
}

func parseGoWorkModuleRootsWithGoCommand(goWorkPath string) ([]string, error) {
	cmd := exec.Command("go", "work", "edit", "-json", goWorkPath)
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

func shouldUseClientForLanguage(languageID string) bool {
	id := normalizeLanguageID(languageID)
	// Fallback-only file types don't need an LSP client.
	switch id {
	case "markdown", "json", "yaml":
		return false
	default:
		return true
	}
}

func shouldUseGoWorkspace(languageID string) bool {
	switch normalizeLanguageID(languageID) {
	case "", "go", "gomod", "gosum", "gowork":
		return true
	default:
		return false
	}
}

func shouldUseJSTSWorkspace(languageID string) bool {
	switch normalizeLanguageID(languageID) {
	case "javascript", "typescript", "javascriptreact", "typescriptreact":
		return true
	default:
		return false
	}
}

// findJSTSProjectRoot walks up from path looking for package.json,
// tsconfig.json, or jsconfig.json — the project markers that tsserver
// needs to anchor a workspace.
func findJSTSProjectRoot(path string) (string, error) {
	absPath, err := platformshared.NormalizeAbsolutePath(path)
	if err != nil {
		return "", err
	}
	startDir, err := resolveStartDir(absPath)
	if err != nil {
		return "", err
	}
	for dir := startDir; dir != "" && dir != "."; dir = filepath.Dir(dir) {
		for _, marker := range jstsProjectMarkers {
			if fileExists(filepath.Join(dir, marker)) {
				return dir, nil
			}
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return "", nil
}

var (
	jstsProjectMarkers   = []string{"tsconfig.json", "jsconfig.json", "package.json"}
	jstsProjectMarkerSet = map[string]struct{}{
		"jsconfig.json": {},
		"package.json":  {},
		"tsconfig.json": {},
	}
	jstsIgnoredDirNames = map[string]struct{}{
		".build-cache": {},
		".git":         {},
		".workspace":   {},
		"dist":         {},
		"node_modules": {},
		"vendor":       {},
	}
	jstsBootstrapExtensions = map[string]struct{}{
		".js":  {},
		".jsx": {},
		".ts":  {},
		".tsx": {},
	}
)

type jstsProjectRootWithinFinder struct {
	result string
}

// findJSTSProjectRootWithin walks down from root looking for the first
// directory that contains a JS/TS project marker. Used when no source
// file path is available (e.g. workspace_symbol with only a language).
func findJSTSProjectRootWithin(root string) (string, error) {
	finder := &jstsProjectRootWithinFinder{}
	if err := filepath.WalkDir(root, finder.walk); err != nil {
		return "", err
	}
	return finder.result, nil
}

func (f *jstsProjectRootWithinFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if d == nil {
		return nil
	}
	if d.IsDir() {
		return jstsWalkDirDecision(d.Name())
	}
	if !isJSTSProjectMarker(d.Name()) {
		return nil
	}
	f.result = filepath.Dir(path)
	return filepath.SkipAll
}

func jstsWalkDirDecision(name string) error {
	if strings.HasPrefix(name, ".") {
		return filepath.SkipDir
	}
	if _, ok := jstsIgnoredDirNames[name]; ok {
		return filepath.SkipDir
	}
	return nil
}

func isJSTSProjectMarker(name string) bool {
	_, ok := jstsProjectMarkerSet[name]
	return ok
}

type jstsBootstrapFileFinder struct {
	result string
}

func findJSTSBootstrapFileWithin(root string) (string, error) {
	finder := &jstsBootstrapFileFinder{}
	if err := filepath.WalkDir(root, finder.walk); err != nil {
		return "", err
	}
	return finder.result, nil
}

func (f *jstsBootstrapFileFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if d == nil {
		return nil
	}
	if d.IsDir() {
		return jstsWalkDirDecision(d.Name())
	}
	if !isJSTSBootstrapFile(path) {
		return nil
	}
	f.result = path
	return filepath.SkipAll
}

func isJSTSBootstrapFile(path string) bool {
	_, ok := jstsBootstrapExtensions[strings.ToLower(filepath.Ext(path))]
	return ok
}

func shouldUseJavaWorkspace(languageID string) bool {
	return normalizeLanguageID(languageID) == "java"
}

var (
	javaProjectMarkers   = []string{"pom.xml", "build.gradle", "build.gradle.kts"}
	javaProjectMarkerSet = map[string]struct{}{
		"build.gradle":     {},
		"build.gradle.kts": {},
		"pom.xml":          {},
	}
	javaIgnoredDirNames = map[string]struct{}{
		".build-cache": {},
		".git":         {},
		".gradle":      {},
		".idea":        {},
		".workspace":   {},
		"build":        {},
		"node_modules": {},
		"target":       {},
		"vendor":       {},
	}
	javaBootstrapExtensions = map[string]struct{}{
		".java": {},
	}
)

func findJavaProjectRoot(path string) (string, error) {
	absPath, err := platformshared.NormalizeAbsolutePath(path)
	if err != nil {
		return "", err
	}
	startDir, err := resolveStartDir(absPath)
	if err != nil {
		return "", err
	}
	for dir := startDir; dir != "" && dir != "."; dir = filepath.Dir(dir) {
		for _, marker := range javaProjectMarkers {
			if fileExists(filepath.Join(dir, marker)) {
				return dir, nil
			}
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return "", nil
}

type javaProjectRootWithinFinder struct {
	result string
}

func findJavaProjectRootWithin(root string) (string, error) {
	finder := &javaProjectRootWithinFinder{}
	if err := filepath.WalkDir(root, finder.walk); err != nil {
		return "", err
	}
	return finder.result, nil
}

func (f *javaProjectRootWithinFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if d == nil {
		return nil
	}
	if d.IsDir() {
		return javaWalkDirDecision(d.Name())
	}
	if _, ok := javaProjectMarkerSet[d.Name()]; !ok {
		return nil
	}
	f.result = filepath.Dir(path)
	return filepath.SkipAll
}

func javaWalkDirDecision(name string) error {
	if strings.HasPrefix(name, ".") {
		return filepath.SkipDir
	}
	if _, ok := javaIgnoredDirNames[name]; ok {
		return filepath.SkipDir
	}
	return nil
}

type javaBootstrapFileFinder struct {
	result string
}

func findJavaBootstrapFileWithin(root string) (string, error) {
	finder := &javaBootstrapFileFinder{}
	if err := filepath.WalkDir(root, finder.walk); err != nil {
		return "", err
	}
	return finder.result, nil
}

func (f *javaBootstrapFileFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if d == nil {
		return nil
	}
	if d.IsDir() {
		return javaWalkDirDecision(d.Name())
	}
	if _, ok := javaBootstrapExtensions[strings.ToLower(filepath.Ext(path))]; !ok {
		return nil
	}
	f.result = path
	return filepath.SkipAll
}

func normalizeLanguageID(languageID string) string {
	return strings.ToLower(strings.TrimSpace(languageID))
}

func fileURIFromPath(absPath string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absPath)}).String()
}
