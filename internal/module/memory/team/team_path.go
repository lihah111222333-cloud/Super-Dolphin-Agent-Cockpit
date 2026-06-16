package team

import (
	"fmt"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"unicode"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/memdata"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
	"golang.org/x/text/unicode/norm"
)

const maxTeamPathDecodePasses = 4

type teamPathError func(string) error

// sanitizePathKey performs string-level normalization only.
// Symlink checks and root-containment validation must still go through
// validateTeamMemKey or validateTeamMemWritePath.
func sanitizePathKey(raw string) (string, error) {
	return sanitizePathKeyWithWrap(raw, invalidTeamMemKey)
}

func validateTeamMemKey(root, key string) (string, error) {
	sanitized, err := sanitizePathKey(key)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(root, filepath.FromSlash(sanitized))
	if _, err := validateTeamMemCandidate(root, candidate, invalidTeamMemKey); err != nil {
		return "", err
	}
	return sanitized, nil
}

func validateTeamMemWritePath(root, file string) (string, error) {
	return validateTeamMemCandidate(root, file, invalidTeamMemWritePath)
}

func validateTeamMemWritePathBasic(file string) error {
	normalized, absolute, err := normalizeTeamWriteInput(file, invalidTeamMemWritePath)
	if err != nil {
		return err
	}
	if absolute {
		return nil
	}
	_, err = sanitizePathKeyWithWrap(normalized, invalidTeamMemWritePath)
	return err
}

// validateTeamMemCandidate 校验teammem候选项。
func validateTeamMemCandidate(root, file string, wrap teamPathError) (string, error) {
	rootDir, err := validateTeamMemRoot(root, wrap)
	if err != nil {
		return "", err
	}
	candidate, err := prepareTeamMemCandidate(rootDir, file, wrap)
	if err != nil {
		return "", err
	}
	if candidate == rootDir {
		return "", wrap("path must target a file under team root")
	}
	if !pathutil.ContainsPath(rootDir, candidate) {
		return "", wrap("path escapes team root")
	}
	rootReal, err := resolveTeamMemRealPath(rootDir, wrap)
	if err != nil {
		return "", err
	}
	candidateReal, err := resolveTeamMemRealPath(candidate, wrap)
	if err != nil {
		return "", err
	}
	if candidateReal == rootReal {
		return "", wrap("path must target a file under team root")
	}
	if !pathutil.ContainsPath(rootReal, candidateReal) {
		return "", wrap("path escapes team root via symlink")
	}
	return candidate, nil
}

func validateTeamMemRoot(root string, wrap teamPathError) (string, error) {
	cleaned, err := shared.CleanAbsolutePath(root)
	if err != nil {
		return "", wrap(err.Error())
	}
	if strings.TrimSpace(cleaned) == "" {
		return "", wrap("empty team root")
	}
	return strings.TrimSuffix(cleaned, string(os.PathSeparator)), nil
}

// prepareTeamMemCandidate 准备teammem候选项。
func prepareTeamMemCandidate(root, file string, wrap teamPathError) (string, error) {
	normalized, absolute, err := normalizeTeamWriteInput(file, wrap)
	if err != nil {
		return "", err
	}
	candidate := normalized
	if !absolute {
		key, err := sanitizePathKeyWithWrap(normalized, wrap)
		if err != nil {
			return "", err
		}
		candidate = filepath.Join(root, filepath.FromSlash(key))
	}
	candidate, err = shared.CleanAbsolutePath(candidate)
	if err != nil {
		return "", wrap(err.Error())
	}
	if err := shared.EnsureResolvablePath(root); err != nil {
		return "", wrap(err.Error())
	}
	if err := shared.EnsureResolvablePath(candidate); err != nil {
		return "", wrap(err.Error())
	}
	return candidate, nil
}

// normalizeTeamWriteInput 规范化teamwriteinput。
func normalizeTeamWriteInput(raw string, wrap teamPathError) (string, bool, error) {
	normalized, err := normalizeTeamPathInput(raw, wrap)
	if err != nil {
		return "", false, err
	}
	if containsTeamDotSegments(normalized) {
		return "", false, wrap("path traversal is not allowed")
	}
	if hasWindowsDrivePrefix(normalized) && os.PathSeparator != '\\' {
		return "", false, wrap("windows drive path is not allowed")
	}
	if !isTeamAbsolutePath(normalized) {
		return normalized, false, nil
	}
	cleaned, err := shared.CleanAbsolutePath(filepath.FromSlash(normalized))
	if err != nil {
		return "", false, wrap(err.Error())
	}
	return cleaned, true, nil
}

// sanitizePathKeyWithWrap 清理带wrap的路径键。
func sanitizePathKeyWithWrap(raw string, wrap teamPathError) (string, error) {
	normalized, err := normalizeTeamPathInput(raw, wrap)
	if err != nil {
		return "", err
	}
	if isTeamAbsolutePath(normalized) {
		return "", wrap("absolute path is not allowed")
	}
	if containsTeamDotSegments(normalized) {
		return "", wrap("path traversal is not allowed")
	}
	cleaned := pathpkg.Clean(normalized)
	if cleaned == "." {
		return "", wrap("empty path key")
	}
	if hasTeamTraversal(cleaned) {
		return "", wrap("path traversal is not allowed")
	}
	return cleaned, nil
}

// normalizeTeamPathInput 规范化team路径input。
func normalizeTeamPathInput(raw string, wrap teamPathError) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", wrap("empty path")
	}
	if strings.ContainsRune(trimmed, '\x00') {
		return "", wrap("null byte")
	}
	decoded, err := decodeTeamPathEscapes(norm.NFKC.String(trimmed))
	if err != nil {
		return "", wrap(err.Error())
	}
	normalized := norm.NFKC.String(strings.TrimSpace(decoded))
	if strings.ContainsRune(normalized, '\x00') {
		return "", wrap("null byte")
	}
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	if strings.TrimSpace(normalized) == "" {
		return "", wrap("empty path")
	}
	return normalized, nil
}

func decodeTeamPathEscapes(raw string) (string, error) {
	decoded := raw
	for i := 0; i < maxTeamPathDecodePasses; i++ {
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return "", fmt.Errorf("invalid path escape: %w", err)
		}
		if next == decoded {
			return decoded, nil
		}
		decoded = next
	}
	return decoded, nil
}

func resolveTeamMemRealPath(path string, wrap teamPathError) (string, error) {
	resolved, err := shared.RealPathDeepestExisting(path)
	if err != nil {
		return "", wrap(err.Error())
	}
	if strings.TrimSpace(resolved) == "" {
		return "", wrap("path could not be resolved")
	}
	return resolved, nil
}

func hasTeamTraversal(cleaned string) bool {
	return cleaned == ".." || strings.HasPrefix(cleaned, "../")
}

func containsTeamDotSegments(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case ".", "..":
			return true
		}
	}
	return false
}

func isTeamAbsolutePath(path string) bool {
	return strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || hasWindowsDrivePrefix(path)
}

func hasWindowsDrivePrefix(path string) bool {
	if len(path) < 3 || path[1] != ':' || path[2] != '/' {
		return false
	}
	return unicode.IsLetter(rune(path[0]))
}

func invalidTeamMemWritePath(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidTeamMemWritePath, reason)
}

func invalidTeamMemKey(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidTeamMemKey, reason)
}
