package gate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// compileGroupBatchEnvironment 构造单 batch 的隔离目录，并绑定 shard 级共享
// candidate GOCACHE 与 accepted baseline seed；HOME/XDG 保留在长工作根下，
// TMPDIR/GOTMPDIR 使用现有 temp-data 下的短、唯一、owner-only 运行根。
func compileGroupBatchEnvironment(artifact compiledGroupArtifact, batchID string) ([]string, string, string, error) {
	if strings.TrimSpace(batchID) == "" || filepath.Base(batchID) != batchID || strings.ContainsAny(batchID, "\\/\x00\r\n") {
		return nil, "", "", errors.New("compile group batch ID is required for environment isolation")
	}
	batchRoot, err := createCompileGroupBatchRoot(artifact.layout.runRoot, batchID)
	if err != nil {
		return nil, "", "", err
	}
	if err := ensureCompileGroupBatchDirectories(batchRoot); err != nil {
		return nil, "", "", cleanupCompileGroupBatchRoot(batchRoot, err)
	}
	shortTempRoot, err := createCompileGroupBatchShortTempRoot()
	if err != nil {
		return nil, "", "", cleanupCompileGroupBatchRoot(batchRoot, err)
	}
	if err := ensureCompileGroupBatchShortTempDirectories(shortTempRoot); err != nil {
		return nil, "", "", errors.Join(err, cleanupCompileGroupBatchRoots(batchRoot, shortTempRoot))
	}
	environment := compileGroupBatchRuntimeEnvironment(artifact.environment, batchRoot, shortTempRoot)
	if err := configureCompileGroupBatchCache(&environment, artifact, batchID); err != nil {
		return nil, "", "", errors.Join(err, cleanupCompileGroupBatchRoots(batchRoot, shortTempRoot))
	}
	return environment, batchRoot, shortTempRoot, nil
}

// cleanupCompileGroupBatchRoot 汇总 batch 初始化失败与目录清理错误。
func cleanupCompileGroupBatchRoot(batchRoot string, primaryErr error) error {
	return errors.Join(primaryErr, cleanupCompileGroupBatchRoots(batchRoot, ""))
}

// cleanupCompileGroupBatchRoots 删除 batch 长工作根与 temp-data 下的短运行根。
func cleanupCompileGroupBatchRoots(batchRoot string, shortTempRoot string) error {
	var cleanupErr error
	for _, root := range []string{batchRoot, shortTempRoot} {
		if root == "" {
			continue
		}
		if err := os.RemoveAll(root); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove compile group batch runtime root %q: %w", root, err))
		}
	}
	return cleanupErr
}

// createCompileGroupBatchRoot 创建 batch 专属根目录。
func createCompileGroupBatchRoot(runRoot string, batchID string) (string, error) {
	if strings.TrimSpace(runRoot) == "" || !filepath.IsAbs(runRoot) {
		return "", errors.New("compile group batch run root must be an absolute path")
	}
	if strings.TrimSpace(batchID) == "" || filepath.Base(batchID) != batchID || strings.ContainsAny(batchID, "\\/\x00\r\n") {
		return "", errors.New("compile group batch ID is required for environment isolation")
	}
	batchRoot := filepath.Join(runRoot, "batches", batchID)
	if err := os.MkdirAll(batchRoot, 0o700); err != nil {
		return "", fmt.Errorf("create compile group batch root: %w", err)
	}
	return batchRoot, nil
}

// createCompileGroupBatchShortTempRoot 在当前 temp-data 挂载下创建短、唯一的 owner-only 根目录。
func createCompileGroupBatchShortTempRoot() (string, error) {
	tempDataRoot := os.Getenv("TMPDIR")
	if strings.TrimSpace(tempDataRoot) == "" || !filepath.IsAbs(tempDataRoot) {
		return "", errors.New("compile group batch TMPDIR must be an absolute mounted temp-data path")
	}
	root, err := os.MkdirTemp(tempDataRoot, "sd-b-")
	if err != nil {
		return "", fmt.Errorf("create short compile group batch temp root: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", errors.Join(fmt.Errorf("secure short compile group batch temp root: %w", err), cleanupCompileGroupBatchRoots("", root))
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", errors.Join(fmt.Errorf("stat short compile group batch temp root: %w", err), cleanupCompileGroupBatchRoots("", root))
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", errors.Join(errors.New("short compile group batch temp root must be an owner-only directory"), cleanupCompileGroupBatchRoots("", root))
	}
	return root, nil
}

// ensureCompileGroupBatchShortTempDirectories 创建短运行根下的 TMPDIR/GOTMPDIR 子目录。
func ensureCompileGroupBatchShortTempDirectories(shortTempRoot string) error {
	for _, name := range []string{"tmp", "gotmp"} {
		if err := os.MkdirAll(filepath.Join(shortTempRoot, name), 0o700); err != nil {
			return fmt.Errorf("create short compile group batch %s: %w", name, err)
		}
	}
	return nil
}

// ensureCompileGroupBatchDirectories 创建运行时目录，不创建共享可写目录。
func ensureCompileGroupBatchDirectories(batchRoot string) error {
	for _, name := range []string{"home", "home/.codex", "xdg-cache", "xdg-config", "xdg-data", "gocache"} {
		if err := os.MkdirAll(filepath.Join(batchRoot, name), 0o700); err != nil {
			return fmt.Errorf("create compile group batch %s: %w", name, err)
		}
	}
	return nil
}

// compileGroupBatchRuntimeEnvironment 绑定 batch 专属 HOME、XDG、GOCACHE 与短 TMP。
func compileGroupBatchRuntimeEnvironment(base []string, batchRoot string, shortTempRoot string) []string {
	environment := append([]string(nil), base...)
	for _, item := range []struct{ key, path string }{
		{key: "HOME", path: filepath.Join(batchRoot, "home")},
		{key: "TMPDIR", path: filepath.Join(shortTempRoot, "tmp")},
		{key: "GOTMPDIR", path: filepath.Join(shortTempRoot, "gotmp")},
		{key: "XDG_CACHE_HOME", path: filepath.Join(batchRoot, "xdg-cache")},
		{key: "XDG_CONFIG_HOME", path: filepath.Join(batchRoot, "xdg-config")},
		{key: "XDG_DATA_HOME", path: filepath.Join(batchRoot, "xdg-data")},
		{key: "GOCACHE", path: filepath.Join(batchRoot, "gocache")},
	} {
		environment = setCompileGroupEnvironmentValue(environment, item.key, item.path)
	}
	return environment
}

// setCompileGroupEnvironmentValue 覆盖环境变量并保持其他输入原样。
func setCompileGroupEnvironmentValue(environment []string, key string, value string) []string {
	prefix := key + "="
	for index, item := range environment {
		if strings.HasPrefix(item, prefix) {
			environment[index] = prefix + value
			return environment
		}
	}
	return append(environment, prefix+value)
}

// compileGroupBatchProcessEnvironment 为 archtest 单 test-binary 设置契约冻结的
// GOMEMLIMIT=3GiB；其他 compile group 保持候选环境原样。
func compileGroupBatchProcessEnvironment(environment []string, packageTarget string) []string {
	if packageTarget != AtomicArchtestPackageTarget {
		return environment
	}
	return setCompileGroupEnvironmentValue(environment, "GOMEMLIMIT", "3GiB")
}
