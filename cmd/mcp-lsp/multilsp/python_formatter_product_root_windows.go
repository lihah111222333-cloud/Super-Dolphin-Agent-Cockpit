//go:build windows

package multilsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const pythonFormatterWorkspaceProductDir = ".super-dolphin"

// resolvePythonFormatterProductRootPlatform 优先使用宿主明确授权的产品根；缺失时从
// 严格可信 workspace root 派生隔离根，避免要求 sidecar 继承手工环境变量。
func resolvePythonFormatterProductRootPlatform(m *manager, ctx context.Context) (string, error) {
	if m == nil {
		return "", errors.New("Python formatter manager is nil")
	}
	if configured := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME")); configured != "" {
		root, err := normalizePythonFormatterProductRoot(configured)
		if err != nil {
			return "", err
		}
		if err := preparePythonFormatterProductRoot(root); err != nil {
			return "", err
		}
		return root, nil
	}

	workspaceRoot, err := m.effectiveWorkspaceRoot(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve Python formatter workspace root: %w", err)
	}
	workspaceRoot, err = normalizePythonFormatterProductRoot(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve Python formatter workspace root: %w", err)
	}
	productRoot := filepath.Join(workspaceRoot, pythonFormatterWorkspaceProductDir)
	if err := preparePythonFormatterProductRoot(productRoot); err != nil {
		return "", fmt.Errorf("prepare workspace-scoped Ruff formatter product root: %w", err)
	}
	return productRoot, nil
}

func normalizePythonFormatterProductRoot(value string) (string, error) {
	root := filepath.Clean(strings.TrimSpace(value))
	if root == "." || !filepath.IsAbs(root) {
		return "", fmt.Errorf("Ruff formatter product root must be an absolute path")
	}
	return root, nil
}

// preparePythonFormatterProductRoot creates the root with private defaults and
// then preserves the owner-only/authorization contract before any cache write.
func preparePythonFormatterProductRoot(root string) error {
	if err := rejectPythonFormatterProductRootSymlink(root); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create Ruff formatter product root: %w", securefs.WrapErrorForPath(err, root))
	}
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect Ruff formatter product root: %w", securefs.WrapErrorForPath(err, root))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("Ruff formatter product root is not a real directory: %s", securefs.RedactPath(root))
	}
	if err := securefs.CheckExistingOwnerOnly(root, info); err == nil {
		return nil
	}
	if err := securefs.RestrictOwnerOnly(root, 0o700); err != nil {
		return fmt.Errorf("Ruff formatter product root ACL requires authorization: %w", securefs.WrapErrorForPath(err, root))
	}
	if err := securefs.CheckExistingOwnerOnly(root, nil); err != nil {
		return fmt.Errorf("Ruff formatter product root ACL validation failed: %w", securefs.WrapErrorForPath(err, root))
	}
	return nil
}

func rejectPythonFormatterProductRootSymlink(root string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Ruff formatter product root: %w", securefs.WrapErrorForPath(err, root))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Ruff formatter product root must not be a symlink: %s", securefs.RedactPath(root))
	}
	return nil
}
