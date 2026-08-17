package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/pathutil"
)

// SuperDolphinHomeEnv 指向 Super Dolphin 自管数据目录。
const SuperDolphinHomeEnv = "SUPER_DOLPHIN_HOME"

// NormalizeRelativePath 清理调用方传入的相对路径文本；是否允许越界由后续 pathutil 校验负责。
func NormalizeRelativePath(path string) string {
	return filepath.Clean(strings.TrimSpace(path))
}

// ContainsPath 判断 target 是否位于 root 内，保持 shared 包旧入口兼容。
func ContainsPath(root, target string) bool { return pathutil.ContainsPath(root, target) }

// AppManagedDataRoots 返回应用允许清理或迁移的自管数据根。
// 配置目录不能覆盖整个用户 home，避免清理逻辑误伤用户文件。
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
		filepath.Join(userHome, ".super-dolphin", "log"),
		filepath.Join(userHome, ".super-dolphin", "memory"),
		filepath.Join(userHome, ".super-dolphin", "skills"),
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

// isAllowedConfiguredSuperDolphinHome 判断显式 SUPER_DOLPHIN_HOME 是否落在允许位置。
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

// hasPathSuffix 按路径段边界判断后缀，避免普通字符串后缀误匹配。
func hasPathSuffix(path string, suffix string) bool {
	if path == suffix {
		return true
	}
	return strings.HasSuffix(path, string(filepath.Separator)+suffix)
}

// appendAppManagedDataRoot 追加去重后的自管数据根，并拒绝覆盖整个用户 home。
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

// cleanAppManagedDataRoot 展开环境变量和 `~`，并规范成绝对路径。
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
