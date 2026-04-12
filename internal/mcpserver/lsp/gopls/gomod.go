package gopls

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func findGoModRoot(path string) (string, error) {
	absPath, err := platformshared.NormalizeAbsolutePath(path)
	if err != nil {
		return "", err
	}
	startDir, err := resolveStartDir(absPath)
	if err != nil {
		return "", err
	}
	for dir := startDir; dir != "" && dir != "."; dir = filepath.Dir(dir) {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return "", nil
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
func findJSTSProjectRootWithin(root string) string {
	finder := &jstsProjectRootWithinFinder{}
	_ = filepath.WalkDir(root, finder.walk)
	return finder.result
}

func (f *jstsProjectRootWithinFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil || d == nil {
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

func findJSTSBootstrapFileWithin(root string) string {
	finder := &jstsBootstrapFileFinder{}
	_ = filepath.WalkDir(root, finder.walk)
	return finder.result
}

func (f *jstsBootstrapFileFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil || d == nil {
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

func findJavaProjectRootWithin(root string) string {
	finder := &javaProjectRootWithinFinder{}
	_ = filepath.WalkDir(root, finder.walk)
	return finder.result
}

func (f *javaProjectRootWithinFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil || d == nil {
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

func findJavaBootstrapFileWithin(root string) string {
	finder := &javaBootstrapFileFinder{}
	_ = filepath.WalkDir(root, finder.walk)
	return finder.result
}

func (f *javaBootstrapFileFinder) walk(path string, d os.DirEntry, walkErr error) error {
	if walkErr != nil || d == nil {
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
