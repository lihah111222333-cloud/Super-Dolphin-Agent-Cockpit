package runtimeenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// packagedRuntimeFromResourcesForOS 根据包资源根目录拼出运行时二进制、迁移和用户数据路径。
func packagedRuntimeFromResourcesForOS(goos, resources, userHome string) PackagedRuntime {
	return PackagedRuntime{
		ResourcesDir:  resources,
		BinDir:        filepath.Join(resources, "bin"),
		MigrationsDir: filepath.Join(resources, "internal", "platform", "db", "sqlite", "migrations"),
		AppDataDir:    packagedAppDataDirForOS(goos, userHome),
	}
}

// packagedAppDataDirForOS 返回各平台约定的 Super Dolphin 用户数据目录。
// userHome 为空时返回空串，让上层在缺少 home 时按 fail-fast 路径报错。
func packagedAppDataDirForOS(goos, userHome string) string {
	userHome = strings.TrimSpace(userHome)
	if userHome == "" {
		return ""
	}
	if goos == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			return filepath.Join(appData, "Super Dolphin")
		}
		return filepath.Join(userHome, "AppData", "Roaming", "Super Dolphin")
	}
	if goos == "darwin" {
		return filepath.Join(userHome, "Library", "Application Support", "Super Dolphin")
	}
	return filepath.Join(userHome, ".config", "Super Dolphin")
}

// packagedResourcesDirForOS 从可执行文件位置推断打包资源根目录。
// 只有 macOS app bundle 和 Windows 发行包形态会被识别，普通开发二进制返回空串。
func packagedResourcesDirForOS(goos, executablePath string) string {
	executablePath = strings.TrimSpace(executablePath)
	if executablePath == "" {
		return ""
	}
	if goos == "darwin" {
		return packagedMacOSResourcesDir(executablePath)
	}
	if goos != "windows" || !strings.EqualFold(filepath.Ext(executablePath), ".exe") {
		return ""
	}
	exeDir := filepath.Dir(executablePath)
	if fileExists(filepath.Join(exeDir, runtimeManifestName)) {
		return exeDir
	}
	if filepath.Base(exeDir) == "bin" {
		parent := filepath.Dir(exeDir)
		if fileExists(filepath.Join(parent, runtimeManifestName)) {
			return parent
		}
	}
	return ""
}

// packagedMacOSResourcesDir 只接受 .app/Contents/MacOS 下的可执行文件布局。
func packagedMacOSResourcesDir(executablePath string) string {
	executablePath = strings.TrimSpace(executablePath)
	if executablePath == "" {
		return ""
	}
	exeDir := filepath.Dir(executablePath)
	if filepath.Base(exeDir) != "MacOS" {
		return ""
	}
	contentsDir := filepath.Dir(exeDir)
	if filepath.Base(contentsDir) != "Contents" {
		return ""
	}
	return filepath.Join(contentsDir, "Resources")
}

// fileExists 判断路径存在且不是目录，用于识别 runtime manifest 哨兵文件。
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// executableNameForOS 为 Windows 运行时补齐可执行文件扩展名。
func executableNameForOS(goos, name string) string {
	if goos == "windows" && !strings.EqualFold(filepath.Ext(name), ".exe") {
		return name + ".exe"
	}
	return name
}

// executableNamesForOS 批量转换 sidecar 名称，保持调用方的顺序不变。
func executableNamesForOS(goos string, names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, executableNameForOS(goos, name))
	}
	return out
}

// requireExecutableFileForOS 校验路径存在、不是目录，并满足目标系统的可执行判定。
func requireExecutableFileForOS(goos, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("not an executable file")
	}
	if goos == "windows" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".exe", ".cmd", ".bat", ".ps1":
			return nil
		default:
			return fmt.Errorf("not an executable file")
		}
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("not an executable file")
	}
	return nil
}

// packagedPathEntriesForOS 返回 owner 进程 PATH，优先使用包内 bin 和 LSP 工具链。
func packagedPathEntriesForOS(goos string, runtime PackagedRuntime) []string {
	lspDir := filepath.Join(runtime.ResourcesDir, lspBundleName)
	if goos == "windows" {
		entries := []string{
			runtime.BinDir,
			filepath.Join(lspDir, "bin"),
			filepath.Join(lspDir, "node"),
			filepath.Join(lspDir, "node_modules", ".bin"),
		}
		return append(entries, windowsSystemPathEntries()...)
	}
	return []string{
		runtime.BinDir,
		filepath.Join(lspDir, "bin"),
		filepath.Join(lspDir, "node", "bin"),
		filepath.Join(lspDir, "node_modules", ".bin"),
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}
}

// packagedSidecarPathEntriesForOS 返回 sidecar 进程 PATH，先暴露 LSP 运行依赖再暴露 peer bin。
func packagedSidecarPathEntriesForOS(goos string, runtime PackagedRuntime) []string {
	lspDir := filepath.Join(runtime.ResourcesDir, lspBundleName)
	if goos == "windows" {
		entries := []string{
			filepath.Join(lspDir, "bin"),
			filepath.Join(lspDir, "node"),
			filepath.Join(lspDir, "node_modules", ".bin"),
			runtime.BinDir,
		}
		return append(entries, windowsSystemPathEntries()...)
	}
	return []string{
		filepath.Join(lspDir, "bin"),
		filepath.Join(lspDir, "node", "bin"),
		filepath.Join(lspDir, "node_modules", ".bin"),
		runtime.BinDir,
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}
}

// windowsSystemPathEntries 保留 Windows 系统目录，避免覆盖 PATH 后基础系统命令不可用。
func windowsSystemPathEntries() []string {
	root := strings.TrimSpace(os.Getenv("SystemRoot"))
	if root == "" {
		root = strings.TrimSpace(os.Getenv("WINDIR"))
	}
	if root == "" {
		root = `C:\Windows`
	}
	return []string{
		filepath.Join(root, "System32"),
		root,
	}
}
