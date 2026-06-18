package multilsp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/internal/hiddenexec"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	jstsPackageManagerKey        = "jstsPackageManager"
	jstsPackageManagerPnpm       = "pnpm"
	jstsPnpmInstallRootKey       = "jstsPnpmInstallRoot"
	jstsPnpmLockFile             = "pnpm-lock.yaml"
	jstsNodeModulesDir           = "node_modules"
	jstsPnpmInstallTimeout       = 10 * time.Minute
	jstsPnpmInstallOutputMaxSize = 8 * 1024
)

// findJSTSPnpmInstallRoot 查找jstspnpm安装根目录。
func findJSTSPnpmInstallRoot(projectRoot, boundaryRoot string) (string, error) {
	root, err := normalizeJSTSDependencyPath(projectRoot)
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", nil
	}
	boundary, err := normalizeJSTSDependencyPath(boundaryRoot)
	if err != nil {
		return "", err
	}
	if boundary != "" && !platformshared.ContainsPath(boundary, root) {
		return "", nil
	}
	for dir := root; dir != ""; dir = filepath.Dir(dir) {
		if fileExists(filepath.Join(dir, jstsPnpmLockFile)) {
			return dir, nil
		}
		if sameJSTSDependencyPath(dir, boundary) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", nil
}

func prepareWorkspaceDependencies(ctx context.Context, cfg workspaceConfig) error {
	if !shouldUseJSTSWorkspace(cfg.languageID) {
		return nil
	}
	if cfg.languageSpecific[jstsPackageManagerKey] != jstsPackageManagerPnpm {
		return nil
	}
	installRoot := strings.TrimSpace(cfg.languageSpecific[jstsPnpmInstallRootKey])
	if installRoot == "" {
		return nil
	}
	if err := ensureJSTSPnpmDependencies(ctx, cfg.projectRoot, installRoot); err != nil {
		return fmt.Errorf("prepare JS/TS workspace dependencies: %w", err)
	}
	return nil
}

// ensureJSTSPnpmDependencies 确保jstspnpmdependencies。
func ensureJSTSPnpmDependencies(ctx context.Context, projectRoot, installRoot string) error {
	hasNodeModules, err := hasJSTSNodeModulesInResolutionChain(projectRoot, installRoot)
	if err != nil {
		return err
	}
	if hasNodeModules {
		return nil
	}
	if !fileExists(filepath.Join(installRoot, jstsPnpmLockFile)) {
		return fmt.Errorf("pnpm install root %q is missing %s", installRoot, jstsPnpmLockFile)
	}
	if err := runJSTSPnpmInstall(ctx, installRoot); err != nil {
		return err
	}
	hasNodeModules, err = hasJSTSNodeModulesInResolutionChain(projectRoot, installRoot)
	if err != nil {
		return err
	}
	if !hasNodeModules {
		return fmt.Errorf("pnpm install --frozen-lockfile in %s completed but node_modules is still missing", installRoot)
	}
	return nil
}

// hasJSTSNodeModulesInResolutionChain 判断解析链上是否已有 JS/TS node_modules。
func hasJSTSNodeModulesInResolutionChain(projectRoot, installRoot string) (bool, error) {
	root, err := normalizeJSTSDependencyPath(projectRoot)
	if err != nil {
		return false, err
	}
	boundary, err := normalizeJSTSDependencyPath(installRoot)
	if err != nil {
		return false, err
	}
	if root == "" || boundary == "" {
		return false, nil
	}
	if !platformshared.ContainsPath(boundary, root) {
		return false, fmt.Errorf("JS/TS project root %q is outside pnpm install root %q", root, boundary)
	}
	for dir := root; dir != ""; dir = filepath.Dir(dir) {
		if dirExists(filepath.Join(dir, jstsNodeModulesDir)) {
			return true, nil
		}
		if sameJSTSDependencyPath(dir, boundary) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return false, nil
}

func runJSTSPnpmInstall(ctx context.Context, installRoot string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	pnpmPath, err := exec.LookPath(jstsPackageManagerPnpm)
	if err != nil {
		return fmt.Errorf("pnpm install --frozen-lockfile in %s cannot start: %w", installRoot, err)
	}
	installCtx, cancel := platformconfig.WithTimeoutIfNone(ctx, jstsPnpmInstallTimeout)
	defer cancel()
	cmd := hiddenexec.CommandContext(installCtx, pnpmPath, "install", "--frozen-lockfile", "--ignore-scripts")
	cmd.Dir = installRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pnpm install --frozen-lockfile in %s failed: %w\nOutput: %s", installRoot, err, limitedJSTSCommandOutput(output))
	}
	return nil
}

func normalizeJSTSDependencyPath(path string) (string, error) {
	normalized, err := platformshared.NormalizeAbsolutePath(path)
	if err != nil {
		return "", err
	}
	return normalized, nil
}

func sameJSTSDependencyPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	rel, err := filepath.Rel(a, b)
	return err == nil && rel == "."
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func limitedJSTSCommandOutput(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if len(trimmed) <= jstsPnpmInstallOutputMaxSize {
		return trimmed
	}
	return trimmed[:jstsPnpmInstallOutputMaxSize] + "...(truncated)"
}
