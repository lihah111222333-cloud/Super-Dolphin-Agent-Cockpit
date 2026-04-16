package retrieval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"golang.org/x/text/unicode/norm"
)

const memoryIndexFileName = "MEMORY.md"

var (
	errInvalidMemoryRoot     = errors.New("invalid memory root")
	errInvalidMemoryReadPath = errors.New("invalid memory read path")
)

func normalizeStoreRoot(root string) (string, error) {
	return validateMemoryRoot(root)
}

func validateMemoryRoot(raw string) (string, error) {
	raw = norm.NFC.String(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if strings.ContainsRune(raw, '\x00') {
		return "", fmt.Errorf("%w: null byte", errInvalidMemoryRoot)
	}
	if strings.HasPrefix(raw, `\\`) || strings.HasPrefix(raw, "//") {
		return "", fmt.Errorf("%w: UNC path is not allowed", errInvalidMemoryRoot)
	}
	expanded, err := expandHomePath(raw)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("%w: path must be absolute", errInvalidMemoryRoot)
	}
	cleaned, err := cleanAbsolutePath(expanded)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errInvalidMemoryRoot, err)
	}
	if isRootOrNearRoot(cleaned) {
		return "", fmt.Errorf("%w: path is too broad", errInvalidMemoryRoot)
	}
	return strings.TrimRight(cleaned, string(os.PathSeparator)) + string(os.PathSeparator), nil
}

func validateMemoryReadPath(root, file string) (string, error) {
	validatedRoot, err := validateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", invalidMemoryReadPath("empty root")
	}
	rootDir, candidate, err := prepareMemoryPath(validatedRoot, file)
	if err != nil {
		return "", err
	}
	rootReal, err := resolveExistingMemoryPath(rootDir)
	if err != nil {
		return "", invalidMemoryReadPath(err.Error())
	}
	candidateReal, err := resolveExistingMemoryPath(candidate)
	if err != nil {
		return "", invalidMemoryReadPath(err.Error())
	}
	if !platformshared.ContainsPath(rootReal, candidateReal) {
		return "", invalidMemoryReadPath("path escapes root")
	}
	if info, err := os.Stat(candidateReal); err != nil {
		return "", invalidMemoryReadPath(err.Error())
	} else if info.IsDir() {
		return "", invalidMemoryReadPath("path is a directory")
	}
	return candidateReal, nil
}

func prepareMemoryPath(validatedRoot, file string) (string, string, error) {
	file = norm.NFC.String(strings.TrimSpace(file))
	if file == "" {
		return "", "", invalidMemoryReadPath("empty file path")
	}
	if strings.ContainsRune(file, '\x00') {
		return "", "", invalidMemoryReadPath("null byte")
	}
	rootDir := strings.TrimSuffix(validatedRoot, string(os.PathSeparator))
	candidate := file
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootDir, candidate)
	}
	candidate, err := cleanAbsolutePath(candidate)
	if err != nil {
		return "", "", invalidMemoryReadPath(err.Error())
	}
	if err := ensureResolvablePath(rootDir); err != nil {
		return "", "", invalidMemoryReadPath(err.Error())
	}
	if err := ensureResolvablePath(candidate); err != nil {
		return "", "", invalidMemoryReadPath(err.Error())
	}
	return rootDir, candidate, nil
}

func resolveExistingMemoryPath(path string) (string, error) {
	resolved, err := realPathDeepestExisting(path)
	if err != nil {
		return "", err
	}
	if resolved == "" {
		return "", os.ErrNotExist
	}
	return resolved, nil
}

func invalidMemoryReadPath(reason string) error {
	return fmt.Errorf("%w: %s", errInvalidMemoryReadPath, reason)
}

func expandHomePath(raw string) (string, error) {
	switch {
	case raw == "~", raw == "~/", raw == `~\\`:
		return "", fmt.Errorf("%w: home root is not allowed", errInvalidMemoryRoot)
	case strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\\`):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%w: %v", errInvalidMemoryRoot, err)
		}
		tail := strings.TrimLeft(raw[1:], `/\\`)
		if strings.TrimSpace(tail) == "" || filepath.Clean(tail) == "." {
			return "", fmt.Errorf("%w: home root is not allowed", errInvalidMemoryRoot)
		}
		return filepath.Join(home, tail), nil
	case strings.HasPrefix(raw, "~"):
		return "", fmt.Errorf("%w: unsupported home path", errInvalidMemoryRoot)
	default:
		return raw, nil
	}
}

func isRootOrNearRoot(path string) bool {
	volume := filepath.VolumeName(path)
	trimmed := strings.TrimPrefix(path, volume)
	trimmed = strings.TrimRight(trimmed, string(os.PathSeparator))
	if trimmed == "" || trimmed == string(os.PathSeparator) {
		return true
	}
	trimmed = strings.TrimPrefix(trimmed, string(os.PathSeparator))
	return !strings.Contains(trimmed, string(os.PathSeparator))
}

func cleanAbsolutePath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("path is empty")
	}
	cleaned := filepath.Clean(norm.NFC.String(strings.TrimSpace(raw)))
	if !filepath.IsAbs(cleaned) {
		absolute, err := filepath.Abs(cleaned)
		if err != nil {
			return "", err
		}
		cleaned = filepath.Clean(absolute)
	}
	return cleaned, nil
}

func realPathDeepestExisting(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if _, err := os.Stat(cleaned); err == nil {
		return filepath.EvalSymlinks(cleaned)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	current := cleaned
	var suffix []string
	for {
		next := filepath.Dir(current)
		if next == current {
			return "", os.ErrNotExist
		}
		suffix = append(suffix, filepath.Base(current))
		current = next
		if _, err := os.Stat(current); err == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return real, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
}

func ensureResolvablePath(path string) error {
	for probe := filepath.Clean(path); ; probe = filepath.Dir(probe) {
		info, err := os.Lstat(probe)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				if _, err := filepath.EvalSymlinks(probe); err != nil {
					return err
				}
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
	}
}
