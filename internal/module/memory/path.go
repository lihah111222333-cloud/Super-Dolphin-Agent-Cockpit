package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"golang.org/x/text/unicode/norm"
)

const (
	memoryIndexFileName  = "MEMORY.md"
	memoryProjectsDir    = "projects"
	memoryProjectDirName = "memory"
	sanitizePathMaxLen   = 96
	gitResolveTimeout    = 4 * time.Second
)

var (
	ErrInvalidMemoryRoot      = errors.New("invalid memory root")
	ErrInvalidMemoryReadPath  = errors.New("invalid memory read path")
	ErrInvalidMemoryWritePath = errors.New("invalid memory write path")
)

func GetAutoMemPath(baseRoot, projectRoot string) (string, error) {
	validatedRoot, err := ValidateMemoryRoot(baseRoot)
	if err != nil || validatedRoot == "" {
		return "", err
	}
	canonicalRoot, err := FindCanonicalGitRoot(context.Background(), projectRoot)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(canonicalRoot) == "" {
		return "", fmt.Errorf("%w: empty project root", ErrInvalidMemoryRoot)
	}
	root := strings.TrimSuffix(validatedRoot, string(os.PathSeparator))
	return filepath.Join(root, memoryProjectsDir, SanitizePath(canonicalRoot), memoryProjectDirName), nil
}

func FindCanonicalGitRoot(ctx context.Context, projectRoot string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fallback, err := cleanAbsolutePath(projectRoot)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
	}
	gitCtx, cancel := context.WithTimeout(ctx, gitResolveTimeout)
	defer cancel()

	cmd := exec.CommandContext(gitCtx, "git", "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	cmd.Dir = fallback
	output, err := cmd.Output()
	if err != nil {
		return fallback, nil
	}
	lines := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return fallback, nil
	}
	gitRoot := strings.TrimSpace(lines[0])
	if gitRoot == "" {
		return fallback, nil
	}
	gitRoot = filepath.Clean(norm.NFC.String(gitRoot))
	if len(lines) < 2 {
		return gitRoot, nil
	}
	commonDir := strings.TrimSpace(lines[1])
	if filepath.Base(commonDir) == ".git" {
		parent := filepath.Dir(filepath.Clean(commonDir))
		if parent != "" {
			return parent, nil
		}
	}
	return gitRoot, nil
}

func SanitizePath(raw string) string {
	normalized := filepath.ToSlash(norm.NFC.String(strings.TrimSpace(raw)))
	var builder strings.Builder
	lastDash := false
	for _, r := range normalized {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(unicode.ToLower(r))
			lastDash = false
		case lastDash:
		default:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "project-" + shortHash(normalized)
	}
	if len(slug) <= sanitizePathMaxLen {
		return slug
	}
	prefix := strings.Trim(slug[:sanitizePathMaxLen-9], "-")
	if prefix == "" {
		prefix = "project"
	}
	return prefix + "-" + shortHash(normalized)
}

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
	cleaned, err := cleanAbsolutePath(expanded)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
	}
	if isRootOrNearRoot(cleaned) {
		return "", fmt.Errorf("%w: path is too broad", ErrInvalidMemoryRoot)
	}
	return strings.TrimRight(cleaned, string(os.PathSeparator)) + string(os.PathSeparator), nil
}

func ValidateMemoryWritePath(root, file string) (string, error) {
	validatedRoot, err := ValidateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", invalidMemoryWritePath("empty root")
	}
	rootDir, candidate, err := prepareMemoryPath(validatedRoot, file, invalidMemoryWritePath)
	if err != nil {
		return "", err
	}
	rootReal, err := resolveMemoryWritePath(rootDir)
	if err != nil {
		return "", err
	}
	candidateReal, err := resolveMemoryWritePath(candidate)
	if err != nil {
		return "", err
	}
	if !platformshared.ContainsPath(rootReal, candidateReal) {
		return "", invalidMemoryWritePath("path escapes root")
	}
	return candidate, nil
}

func ValidateMemoryReadPath(root, file string) (string, error) {
	validatedRoot, err := ValidateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", invalidMemoryReadPath("empty root")
	}
	rootDir, candidate, err := prepareMemoryPath(validatedRoot, file, invalidMemoryReadPath)
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

func prepareMemoryPath(validatedRoot, file string, wrap func(string) error) (string, string, error) {
	file = norm.NFC.String(strings.TrimSpace(file))
	if file == "" {
		return "", "", wrap("empty file path")
	}
	if strings.ContainsRune(file, '\x00') {
		return "", "", wrap("null byte")
	}
	rootDir := strings.TrimSuffix(validatedRoot, string(os.PathSeparator))
	candidate := file
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootDir, candidate)
	}
	candidate, err := cleanAbsolutePath(candidate)
	if err != nil {
		return "", "", wrap(err.Error())
	}
	if err := ensureResolvablePath(rootDir); err != nil {
		return "", "", wrap(err.Error())
	}
	if err := ensureResolvablePath(candidate); err != nil {
		return "", "", wrap(err.Error())
	}
	return rootDir, candidate, nil
}

func resolveMemoryWritePath(path string) (string, error) {
	resolved, err := realPathDeepestExisting(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", invalidMemoryWritePath(err.Error())
	}
	if resolved == "" {
		return path, nil
	}
	return resolved, nil
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
	return fmt.Errorf("%w: %s", ErrInvalidMemoryReadPath, reason)
}

func invalidMemoryWritePath(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidMemoryWritePath, reason)
}

func memoryIndexPath(root string) string {
	return filepath.Join(root, memoryIndexFileName)
}

func memoryTypeDir(root string, memoryType MemoryType) string {
	return filepath.Join(root, string(ParseMemoryType(string(memoryType))))
}

func writeAtomicFile(path string, data []byte, perm os.FileMode) error {
	if perm == 0 {
		perm = 0o644
	}
	validatedPath, err := ValidateMemoryWritePath(filepath.Dir(path), path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(validatedPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(validatedPath)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, validatedPath); err != nil {
		return err
	}
	cleanup = false
	return nil
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

func shortHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:4])
}
