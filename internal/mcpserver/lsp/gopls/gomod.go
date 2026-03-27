package gopls

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func findGoModRoot(path string) (string, error) {
	absPath, err := normalizeAbsolutePath(path)
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

func normalizeAbsolutePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	cleaned := filepath.Clean(absPath)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = filepath.Clean(resolved)
	}
	return cleaned, nil
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
	return normalizeAbsolutePath(path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func shouldUseClientForLanguage(languageID string) bool {
	return shouldUseGoWorkspace(languageID)
}

func shouldUseGoWorkspace(languageID string) bool {
	switch normalizeLanguageID(languageID) {
	case "", "go", "gomod", "gosum", "gowork":
		return true
	default:
		return false
	}
}

func normalizeLanguageID(languageID string) string {
	return strings.ToLower(strings.TrimSpace(languageID))
}

func detectLanguageID(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "go.mod":
		return "gomod"
	case "go.sum":
		return "gosum"
	case "go.work":
		return "gowork"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	default:
		return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	}
}

func fileURIFromPath(absPath string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absPath)}).String()
}
