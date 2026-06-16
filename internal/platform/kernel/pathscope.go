package kernel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
)

// SuperDolphinHomeEnv names the environment variable for app-managed data roots.
const SuperDolphinHomeEnv = "SUPER_DOLPHIN_HOME"

// NormalizeRelativePath 规范化相对路径。
func NormalizeRelativePath(path string) string {
	return filepath.Clean(strings.TrimSpace(path))
}

// ContainsPath delegates to pathutil.ContainsPath.
// ContainsPath 判断路径是否可用。
func ContainsPath(root, target string) bool { return pathutil.ContainsPath(root, target) }

// AppManagedDataRoots returns the explicit user-data roots managed by Super Dolphin.
// AppManagedDataRoots 处理appmanaged数据根目录。
func AppManagedDataRoots() ([]string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home: %w", err)
	}
	userHome = strings.TrimSpace(userHome)
	if userHome == "" {
		return nil, fmt.Errorf("resolve user home: empty path")
	}

	roots := make([]string, 0, 6)
	seen := make(map[string]struct{})
	if configuredHome := strings.TrimSpace(os.Getenv(SuperDolphinHomeEnv)); configuredHome != "" {
		cleanedConfiguredHome, cleanErr := cleanAppManagedDataRoot(configuredHome, userHome)
		if cleanErr != nil {
			return nil, cleanErr
		}
		if !isAllowedConfiguredSuperDolphinHome(cleanedConfiguredHome, userHome) {
			return nil, fmt.Errorf("%s=%s is not an allowed app-managed data root", SuperDolphinHomeEnv, cleanedConfiguredHome)
		}
		var appendErr error
		roots, appendErr = appendAppManagedDataRoot(roots, seen, cleanedConfiguredHome, userHome)
		if appendErr != nil {
			return nil, appendErr
		}
	} else {
		var appendErr error
		roots, appendErr = appendAppManagedDataRoot(roots, seen, filepath.Join(userHome, ".super-dolphin"), userHome)
		if appendErr != nil {
			return nil, appendErr
		}
	}

	for _, root := range []string{
		filepath.Join(userHome, ".multi-agent", "log"),
		filepath.Join(userHome, ".multi-agent", "memory"),
		filepath.Join(userHome, ".multi-agent", "skills"),
		filepath.Join(userHome, "sharedfile"),
	} {
		var appendErr error
		roots, appendErr = appendAppManagedDataRoot(roots, seen, root, userHome)
		if appendErr != nil {
			return nil, appendErr
		}
	}
	return roots, nil
}

func isAllowedConfiguredSuperDolphinHome(cleaned string, userHome string) bool {
	cleanedHome, err := cleanAppManagedDataRoot(userHome, userHome)
	if err != nil {
		return false
	}
	if !ContainsPath(cleanedHome, cleaned) {
		return true
	}
	if filepath.Base(cleaned) == ".super-dolphin" {
		return true
	}
	return hasPathSuffix(cleaned, filepath.Join("Library", "Application Support", "Super Dolphin")) ||
		hasPathSuffix(cleaned, filepath.Join("AppData", "Roaming", "Super Dolphin"))
}

func hasPathSuffix(path string, suffix string) bool {
	if path == suffix {
		return true
	}
	return strings.HasSuffix(path, string(filepath.Separator)+suffix)
}

// appendAppManagedDataRoot 追加appmanaged数据根目录。
func appendAppManagedDataRoot(roots []string, seen map[string]struct{}, root, userHome string) ([]string, error) {
	cleaned, err := cleanAppManagedDataRoot(root, userHome)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cleaned) == "" {
		return roots, nil
	}
	cleanedHome, err := cleanAppManagedDataRoot(userHome, userHome)
	if err != nil {
		return nil, fmt.Errorf("normalize user home: %w", err)
	}
	if cleaned == cleanedHome || (ContainsPath(cleaned, cleanedHome) && !ContainsPath(cleanedHome, cleaned)) {
		return nil, fmt.Errorf("app-managed data root must not include the whole user home: %s", cleaned)
	}
	if _, ok := seen[cleaned]; ok {
		return roots, nil
	}
	seen[cleaned] = struct{}{}
	return append(roots, cleaned), nil
}

// cleanAppManagedDataRoot 处理cleanappmanaged数据根目录。
func cleanAppManagedDataRoot(root, userHome string) (string, error) {
	root = strings.TrimSpace(os.ExpandEnv(root))
	if root == "" {
		return "", nil
	}
	if root == "~" {
		root = userHome
	} else if strings.HasPrefix(root, "~/") {
		root = filepath.Join(userHome, strings.TrimPrefix(root, "~/"))
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("app-managed data root must be absolute: %s", root)
	}
	cleaned, err := pathutil.NormalizeAbsolutePath(root)
	if err != nil {
		return "", fmt.Errorf("normalize app-managed data root %q: %w", root, err)
	}
	return cleaned, nil
}
