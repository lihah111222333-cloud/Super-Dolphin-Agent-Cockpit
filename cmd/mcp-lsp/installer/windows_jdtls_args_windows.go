//go:build windows

package installer

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

var windowsJDTLSMutableRootMu sync.Mutex

// WindowsJDTLSLaunchArguments 将 JDTLS 的锁定相对 launcher/config 参数转换为
// 以 java.exe 启动时的绝对参数；可变 configuration 与 data 均按 workspace digest
// 自动落入产品私有目录，绝不写入用户 workspace 或不可变 asset tree。缺失资产、
// ACL 或路径异常直接失败。
func WindowsJDTLSLaunchArguments(javaExecutable, workspaceRoot string) ([]string, error) {
	javaExecutable = strings.TrimSpace(javaExecutable)
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if !filepath.IsAbs(javaExecutable) || !strings.EqualFold(filepath.Base(javaExecutable), "java.exe") {
		return nil, errors.New("Windows JDTLS launcher requires an absolute java.exe path")
	}
	if !filepath.IsAbs(workspaceRoot) {
		return nil, errors.New("Windows JDTLS launcher requires an absolute workspace root")
	}
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductJDKJDTLS)
	if err != nil {
		return nil, err
	}
	assetRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(javaExecutable))))
	launcherPath := filepath.Join(assetRoot, filepath.FromSlash(entry.Install.ServerPath))
	if err := requireWindowsJDTLSFile(launcherPath, "JDTLS launcher"); err != nil {
		return nil, err
	}
	sourceConfigurationPath := filepath.Join(assetRoot, "config_win")
	sourceConfigurationInfo, err := os.Lstat(sourceConfigurationPath)
	if err != nil {
		return nil, fmt.Errorf("JDTLS source configuration path %q is missing: %w", sourceConfigurationPath, err)
	}
	if sourceConfigurationInfo.Mode()&os.ModeSymlink != 0 || !sourceConfigurationInfo.IsDir() {
		return nil, fmt.Errorf("JDTLS source configuration path %q is not a real directory", sourceConfigurationPath)
	}
	if err := validateWindowsJDTLSConfigDirectory(assetRoot, sourceConfigurationPath, "JDTLS source configuration path"); err != nil {
		return nil, err
	}
	configurationPath, err := ensureWindowsJDTLSConfigurationRoot(assetRoot, workspaceRoot)
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, len(entry.Install.Args)+2)
	for index := 0; index < len(entry.Install.Args); index++ {
		argument := entry.Install.Args[index]
		switch argument {
		case "-jar":
			args = append(args, argument)
			if index+1 >= len(entry.Install.Args) {
				return nil, errors.New("JDTLS catalog launcher argument is incomplete")
			}
			args = append(args, launcherPath)
			index++
		case "-configuration":
			args = append(args, argument)
			if index+1 >= len(entry.Install.Args) {
				return nil, errors.New("JDTLS catalog configuration argument is incomplete")
			}
			args = append(args, configurationPath)
			index++
		default:
			args = append(args, argument)
		}
	}
	dataPath, err := ensureWindowsJDTLSDataRoot(assetRoot, workspaceRoot)
	if err != nil {
		return nil, err
	}
	args = append(args, "-data", dataPath)
	return args, nil
}

// ensureWindowsJDTLSDataRoot 为 product-owned JDTLS 进程创建 workspace 外部的
// data 目录；它按 canonical workspace digest 隔离，避免 JDTLS 把 fixture 当作
// Eclipse workspace 再嵌套进自身的 .jdtls 目录。该入口只在 Windows product
// resolver 使用，非 Windows 的 JDTLS 启动路径不受影响。
func ensureWindowsJDTLSDataRoot(assetRoot, workspaceRoot string) (string, error) {
	windowsJDTLSMutableRootMu.Lock()
	defer windowsJDTLSMutableRootMu.Unlock()
	assetRoot = filepath.Clean(assetRoot)
	workspaceRoot = filepath.Clean(workspaceRoot)
	dataPath, dataParent, canonicalWorkspace, err := windowsJDTLSDataRootPath(assetRoot, workspaceRoot)
	if err != nil {
		return "", err
	}
	if err := ensureDirectoryNoSymlink(dataParent); err != nil {
		return "", fmt.Errorf("create JDTLS product data parent: %w", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(dataParent, 0o700); err != nil {
		return "", fmt.Errorf("restrict JDTLS product data parent: %w", err)
	}
	if err := ensureDirectoryNoSymlink(dataPath); err != nil {
		return "", fmt.Errorf("create JDTLS product data root: %w", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(dataPath, 0o700); err != nil {
		return "", fmt.Errorf("restrict JDTLS product data root: %w", err)
	}
	relative, err := filepath.Rel(canonicalWorkspace, dataPath)
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)) {
		return "", errors.New("JDTLS product data root overlaps workspace root")
	}
	return dataPath, nil
}

// ensureWindowsJDTLSConfigurationRoot 从不可变 asset config_win 复制出按 workspace
// 隔离的产品私有可写配置。共享代码只调用该 Windows build-tag 实现，因此不会改变
// Darwin/Linux 的 JDTLS 启动和目录生命周期。
func ensureWindowsJDTLSConfigurationRoot(assetRoot, workspaceRoot string) (string, error) {
	windowsJDTLSMutableRootMu.Lock()
	defer windowsJDTLSMutableRootMu.Unlock()
	dataPath, dataParent, canonicalWorkspace, err := windowsJDTLSDataRootPath(assetRoot, workspaceRoot)
	if err != nil {
		return "", err
	}
	configurationParent := filepath.Join(filepath.Dir(dataParent), "jdtls-config")
	configurationWorkspaceRoot := filepath.Join(configurationParent, filepath.Base(dataPath))
	configurationPath := filepath.Join(configurationWorkspaceRoot, "config_win")
	for _, path := range []string{configurationParent, configurationWorkspaceRoot} {
		if err := ensureDirectoryNoSymlink(path); err != nil {
			return "", fmt.Errorf("create JDTLS product configuration directory: %w", err)
		}
		if err := securefs.RestrictPrivateOwnerOnly(path, 0o700); err != nil {
			return "", fmt.Errorf("restrict JDTLS product configuration directory: %w", err)
		}
	}
	if err := prepareWindowsRuntimeDependencyJDTLSWorkspaceConfiguration(assetRoot, configurationWorkspaceRoot); err != nil {
		return "", fmt.Errorf("copy JDTLS product configuration: %w", err)
	}
	if err := validateWindowsJDTLSConfigDirectory(configurationWorkspaceRoot, configurationPath, "JDTLS mutable configuration path"); err != nil {
		return "", err
	}
	relative, err := filepath.Rel(canonicalWorkspace, configurationPath)
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)) {
		return "", errors.New("JDTLS product configuration root overlaps workspace root")
	}
	return configurationPath, nil
}

// validateWindowsJDTLSConfigDirectory 校验已存在的 JDTLS 配置目录及其全部父组件；
// 它只读检查，不创建缺失路径，并拒绝 Windows junction/reparse 越界。
func validateWindowsJDTLSConfigDirectory(root, target, label string) error {
	if err := validateWindowsInstallerPathWithinRoot(root, target, false); err != nil {
		return fmt.Errorf("%s is unsafe: %w", label, err)
	}
	return nil
}

func windowsJDTLSDataRootPath(assetRoot, workspaceRoot string) (dataPath, dataParent, canonicalWorkspace string, err error) {
	assetRoot = filepath.Clean(assetRoot)
	workspaceRoot = filepath.Clean(workspaceRoot)
	canonicalWorkspace, err = filepath.Abs(workspaceRoot)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve JDTLS workspace root: %w", err)
	}
	canonicalWorkspace, err = lspplatform.CanonicalDirectoryPath(canonicalWorkspace)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve canonical JDTLS workspace root: %w", err)
	}
	if !filepath.IsAbs(canonicalWorkspace) {
		return "", "", "", errors.New("canonical JDTLS workspace root is not absolute")
	}

	// assetRoot 的层级是 cache/runtime-dependencies/jdk-jdtls/arch/cohort。
	cacheRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(assetRoot))))
	architecture := filepath.Base(filepath.Dir(assetRoot))
	cohort := filepath.Base(assetRoot)
	dataParent = filepath.Join(cacheRoot, "runtime-workspaces", string(WindowsRuntimeDependencyProductJDKJDTLS), architecture, "jdtls-data")
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(cohort+"\x00"+filepath.Clean(canonicalWorkspace)))))
	dataPath = filepath.Join(dataParent, digest)
	relative, err := filepath.Rel(canonicalWorkspace, dataPath)
	if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)) {
		return "", "", "", errors.New("JDTLS product data root overlaps workspace root")
	}
	return dataPath, dataParent, canonicalWorkspace, nil
}

func requireWindowsJDTLSFile(path, name string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s %q is missing: %w", name, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("%s %q is not a regular non-empty file", name, path)
	}
	return nil
}
