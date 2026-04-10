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

var jstsProjectMarkers = []string{"tsconfig.json", "jsconfig.json", "package.json"}

// findJSTSProjectRootWithin walks down from root looking for the first
// directory that contains a JS/TS project marker. Used when no source
// file path is available (e.g. workspace_symbol with only a language).
func findJSTSProjectRootWithin(root string) string {
	var result string
	filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "vendor", ".workspace", ".build-cache", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		for _, marker := range jstsProjectMarkers {
			if d.Name() == marker {
				result = filepath.Dir(path)
				return filepath.SkipAll
			}
		}
		return nil
	})
	return result
}

func normalizeLanguageID(languageID string) string {
	return strings.ToLower(strings.TrimSpace(languageID))
}

func fileURIFromPath(absPath string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absPath)}).String()
}
