package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"golang.org/x/text/unicode/norm"
)

const (
	memoryIndexFileName  = "MEMORY.md"
	memoryProjectsDir    = "projects"
	memoryProjectDirName = "memory"
	memoryUserDir        = "user"
	memoryLocalDir       = "local"
	gitResolveTimeout    = 4 * time.Second
)

func resolveScopeRoot(ctx context.Context, cfg *Config, scope contract.MemoryScope) (string, string, error) {
	baseRoot, err := resolveValidatedBaseRoot(cfg)
	if err != nil {
		return "", "", err
	}
	scope = normalizeMemoryScope(scope)
	if scope == contract.MemoryScopeUser {
		return userScopeRoot(baseRoot), "", nil
	}
	projectRoot, err := findCanonicalGitRoot(ctx, cfg.ProjectRoot)
	if projectRootUnavailable(projectRoot, err) {
		return "", unavailableScopeReason(scope), nil
	}
	projectKey := sanitizePath(projectRoot)
	if scope == contract.MemoryScopeLocal {
		return localScopeRoot(baseRoot, projectKey, cfg.MachineID)
	}
	return projectScopeRoot(baseRoot, projectKey), "", nil
}

func resolveValidatedBaseRoot(cfg *Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("%w: config is nil", contract.ErrMemoryInvalidParam)
	}
	validatedRoot, err := validateMemoryRoot(cfg.RootDir)
	if err != nil {
		return "", fmt.Errorf("%w: %v", contract.ErrMemoryInvalidParam, err)
	}
	if validatedRoot == "" {
		return "", fmt.Errorf("%w: empty root", contract.ErrMemoryInvalidParam)
	}
	return strings.TrimSuffix(validatedRoot, string(os.PathSeparator)), nil
}

func normalizeMemoryScope(scope contract.MemoryScope) contract.MemoryScope {
	if scope == "" {
		return contract.MemoryScopeProject
	}
	return scope
}

func userScopeRoot(baseRoot string) string {
	return filepath.Join(baseRoot, memoryUserDir, memoryProjectDirName)
}

func projectRootUnavailable(projectRoot string, err error) bool {
	return err != nil || strings.TrimSpace(projectRoot) == ""
}

func unavailableScopeReason(scope contract.MemoryScope) string {
	if scope == contract.MemoryScopeLocal {
		return "local_unavailable"
	}
	return "deny"
}

func localScopeRoot(baseRoot, projectKey, machineID string) (string, string, error) {
	machine := sanitizePath(machineID)
	if machine == "" {
		return "", "local_unavailable", nil
	}
	return filepath.Join(baseRoot, memoryLocalDir, machine, memoryProjectsDir, projectKey, memoryProjectDirName), "", nil
}

func projectScopeRoot(baseRoot, projectKey string) string {
	return filepath.Join(baseRoot, memoryProjectsDir, projectKey, memoryProjectDirName)
}

func validateMemoryWritePath(root, file string) (string, error) {
	validatedRoot, err := validateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", errors.New("empty root")
	}
	rootDir, candidate, err := prepareWritePath(validatedRoot, file)
	if err != nil {
		return "", err
	}
	rootReal, err := resolveRealPath(rootDir)
	if err != nil {
		return "", err
	}
	candidateReal, err := resolveRealPath(candidate)
	if err != nil {
		return "", err
	}
	if !platformshared.ContainsPath(rootReal, candidateReal) {
		return "", errors.New("path escapes root")
	}
	return candidate, nil
}

func findCanonicalGitRoot(ctx context.Context, projectRoot string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fallback, err := cleanAbsolutePath(projectRoot)
	if err != nil {
		return "", err
	}
	gitCtx, cancel := platformconfig.WithTimeout(ctx, gitResolveTimeout)
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
	if filepath.Base(commonDir) != ".git" {
		return gitRoot, nil
	}
	parent := filepath.Dir(filepath.Clean(commonDir))
	if parent == "" {
		return gitRoot, nil
	}
	return parent, nil
}

func sanitizePath(raw string) string {
	return platformshared.SanitizeMemoryProjectKey(raw)
}

func validateMemoryRoot(raw string) (string, error) {
	raw = norm.NFC.String(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if strings.ContainsRune(raw, '\x00') {
		return "", errors.New("null byte")
	}
	if strings.HasPrefix(raw, `\\`) {
		return "", errors.New("UNC path is not allowed")
	}
	if isWindowsDriveRoot(raw) {
		return "", errors.New("drive root is not allowed")
	}
	expanded, err := expandHomePath(raw)
	if err != nil {
		return "", err
	}
	if !isAbsoluteMemoryPath(expanded) {
		return "", errors.New("path must be absolute")
	}
	cleaned, err := cleanAbsolutePath(expanded)
	if err != nil {
		return "", err
	}
	if isRootOrNearRoot(cleaned) {
		return "", errors.New("path is too broad")
	}
	return strings.TrimRight(cleaned, string(os.PathSeparator)) + string(os.PathSeparator), nil
}

func prepareWritePath(validatedRoot, file string) (string, string, error) {
	file = norm.NFC.String(strings.TrimSpace(file))
	if file == "" {
		return "", "", errors.New("empty file path")
	}
	if strings.ContainsRune(file, '\x00') {
		return "", "", errors.New("null byte")
	}
	rootDir := strings.TrimSuffix(validatedRoot, string(os.PathSeparator))
	candidate := file
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootDir, candidate)
	}
	candidate, err := cleanAbsolutePath(candidate)
	if err != nil {
		return "", "", err
	}
	if err := ensureResolvablePath(rootDir); err != nil {
		return "", "", err
	}
	if err := ensureResolvablePath(candidate); err != nil {
		return "", "", err
	}
	return rootDir, candidate, nil
}

func resolveRealPath(path string) (string, error) {
	resolved, err := realPathDeepestExisting(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if resolved == "" {
		return path, nil
	}
	return resolved, nil
}

func expandHomePath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(home) == "" {
			return "", errors.New("home is empty")
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	return path, nil
}

func isWindowsDriveRoot(path string) bool {
	if len(path) != 3 || path[1:] != ":\\" {
		return false
	}
	r := rune(path[0])
	return unicode.IsLetter(r)
}

func isAbsoluteMemoryPath(path string) bool {
	return filepath.IsAbs(path) || (len(path) >= 3 && unicode.IsLetter(rune(path[0])) && path[1] == ':' && (path[2] == '\\' || path[2] == '/'))
}

func isRootOrNearRoot(path string) bool {
	cleaned := filepath.Clean(path)
	if cleaned == string(os.PathSeparator) {
		return true
	}
	parent := filepath.Dir(cleaned)
	return parent == string(os.PathSeparator) || parent == cleaned
}

func cleanAbsolutePath(path string) (string, error) {
	path = filepath.Clean(norm.NFC.String(strings.TrimSpace(path)))
	if path == "." || path == "" {
		return "", errors.New("path is empty")
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	return path, nil
}

func realPathDeepestExisting(path string) (string, error) {
	path = filepath.Clean(path)
	current := path
	missing := make([]string, 0, 8)
	for {
		resolved, err := filepath.EvalSymlinks(current)
		switch {
		case err == nil:
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		case errors.Is(err, os.ErrNotExist):
			parent := filepath.Dir(current)
			if parent == current {
				return "", os.ErrNotExist
			}
			missing = append(missing, filepath.Base(current))
			current = parent
		default:
			return "", err
		}
	}
}

func ensureResolvablePath(path string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink == 0 {
				return nil
			}
			_, err = filepath.EvalSymlinks(current)
			return err
		case errors.Is(err, os.ErrNotExist):
			parent := filepath.Dir(current)
			if parent == current {
				return err
			}
			current = parent
		default:
			return err
		}
	}
}
