package runtimeenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func packagedRuntimeFromResourcesForOS(goos, resources, userHome string) PackagedRuntime {
	return PackagedRuntime{
		ResourcesDir:  resources,
		BinDir:        filepath.Join(resources, "bin"),
		MigrationsDir: filepath.Join(resources, "internal", "platform", "db", "sqlite", "migrations"),
		AppDataDir:    packagedAppDataDirForOS(goos, userHome),
	}
}

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

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func executableNameForOS(goos, name string) string {
	if goos == "windows" && !strings.EqualFold(filepath.Ext(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func executableNamesForOS(goos string, names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, executableNameForOS(goos, name))
	}
	return out
}

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
