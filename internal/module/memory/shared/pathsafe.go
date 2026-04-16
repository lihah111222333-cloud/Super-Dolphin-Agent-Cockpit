package memshared

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var ErrInvalidMemoryRoot = errors.New("invalid memory root")

func ValidateMemoryRoot(raw string) (string, error) {
	raw = norm.NFC.String(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if strings.ContainsRune(raw, '\x00') {
		return "", fmt.Errorf("%w: null byte", ErrInvalidMemoryRoot)
	}
	if strings.HasPrefix(raw, `\\`) || strings.HasPrefix(raw, "//") {
		return "", fmt.Errorf("%w: UNC path is not allowed", ErrInvalidMemoryRoot)
	}
	if isWindowsDriveRoot(raw) {
		return "", fmt.Errorf("%w: drive root is not allowed", ErrInvalidMemoryRoot)
	}
	expanded, err := expandHomePath(raw)
	if err != nil {
		return "", err
	}
	if !isAbsoluteMemoryPath(expanded) {
		return "", fmt.Errorf("%w: path must be absolute", ErrInvalidMemoryRoot)
	}
	cleaned, err := CleanAbsolutePath(expanded)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
	}
	if isRootOrNearRoot(cleaned) {
		return "", fmt.Errorf("%w: path is too broad", ErrInvalidMemoryRoot)
	}
	return strings.TrimRight(cleaned, string(os.PathSeparator)) + string(os.PathSeparator), nil
}

func CleanAbsolutePath(raw string) (string, error) {
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

func RealPathDeepestExisting(path string) (string, error) {
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
			for index := len(suffix) - 1; index >= 0; index-- {
				real = filepath.Join(real, suffix[index])
			}
			return real, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
}

func EnsureResolvablePath(path string) error {
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

func ShortHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:4])
}

func expandHomePath(raw string) (string, error) {
	switch {
	case raw == "~", raw == "~/", raw == `~\\`:
		return "", fmt.Errorf("%w: home root is not allowed", ErrInvalidMemoryRoot)
	case strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\\`):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
		}
		tail := strings.TrimLeft(raw[1:], `/\\`)
		if strings.TrimSpace(tail) == "" || filepath.Clean(tail) == "." {
			return "", fmt.Errorf("%w: home root is not allowed", ErrInvalidMemoryRoot)
		}
		return filepath.Join(home, tail), nil
	case strings.HasPrefix(raw, "~"):
		return "", fmt.Errorf("%w: unsupported home path", ErrInvalidMemoryRoot)
	default:
		return raw, nil
	}
}

func isWindowsDriveRoot(path string) bool {
	if len(path) < 2 || path[1] != ':' {
		return false
	}
	rest := strings.Trim(path[2:], `/\\`)
	return rest == ""
}

func isAbsoluteMemoryPath(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
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
