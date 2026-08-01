package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

// runtimeServerCacheRoot 解析并严格校验共享 LSP cache 根目录。
func runtimeServerCacheRoot() (string, error) {
	var root string
	if configured := strings.TrimSpace(os.Getenv(agentLSPSharedCacheDirEnv)); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", errors.New(agentLSPSharedCacheDirEnv + " must be an absolute path")
		}
		root = filepath.Clean(configured)
	} else {
		userCacheDir, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("resolve user cache directory for language servers: %w", err)
		}
		if strings.TrimSpace(userCacheDir) == "" {
			return "", errors.New("user cache directory for language servers is empty")
		}
		root = filepath.Join(userCacheDir, "super-agent-v3", "mcp-lsp", "language-servers")
	}
	if err := runtimeServerEnsurePrivateRoot(root); err != nil {
		return "", fmt.Errorf("secure shared LSP cache root %s: %w", root, err)
	}
	return root, nil
}

// runtimeServerEnsurePrivateRoot 创建或校验私有 cache 根，拒绝已有不安全目录。
func runtimeServerEnsurePrivateRoot(path string) error {
	_, err := os.Lstat(path)
	created := errors.Is(err, os.ErrNotExist)
	if err != nil && !created {
		return err
	}
	if created {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		if err := runtimeServerHardenPrivateDirectory(path); err != nil {
			return err
		}
	}
	return runtimeServerValidatePrivateDirectory(path)
}

// runtimeServerEnsurePrivateDescendant 逐级创建并校验 target，禁止逃逸 root 或穿过符号链接。
func runtimeServerEnsurePrivateDescendant(root, target string) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("path %s escapes shared cache root %s", target, root)
	}
	if err := runtimeServerValidatePrivateDirectory(root); err != nil {
		return err
	}
	current := root
	if relative == "." {
		return nil
	}
	for component := range strings.SplitSeq(relative, string(os.PathSeparator)) {
		current = filepath.Join(current, component)
		if err := runtimeServerEnsurePrivateChild(current); err != nil {
			return err
		}
	}
	return nil
}

func runtimeServerEnsurePrivateChild(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil {
			return fmt.Errorf("create private directory %s: %w", path, err)
		}
		if err := runtimeServerHardenPrivateDirectory(path); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("inspect private directory %s: %w", path, err)
	}
	return runtimeServerValidatePrivateDirectory(path)
}

func runtimeServerValidatePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private directory must not be a symlink: %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("private directory is not a directory: %s", path)
	}
	return runtimeServerValidatePrivateDirectoryPlatform(path, info)
}

// runtimeServerCacheName 把二进制文件名规范化为稳定且可读的缓存目录名。
func runtimeServerCacheName(base string) string {
	base = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(base)), strings.ToLower(filepath.Ext(base)))
	var normalized strings.Builder
	lastWasDash := false
	for _, value := range base {
		switch {
		case unicode.IsLetter(value), unicode.IsDigit(value):
			normalized.WriteRune(value)
			lastWasDash = false
		case normalized.Len() > 0 && !lastWasDash:
			normalized.WriteByte('-')
			lastWasDash = true
		}
	}
	name := strings.Trim(normalized.String(), "-")
	if name == "" {
		return "language-server"
	}
	return name
}

// runtimeServerNodeVersion 返回当前 Node 版本，以及该版本是否支持跨路径 portable compile cache。
func runtimeServerNodeVersion(overrides []string) (string, bool, error) {
	pathEnv := runtimeServerEnvValue(overrides, "PATH")
	nodePath, err := runtimeServerLookPath(
		"node",
		pathEnv,
		runtimeServerEnvValue(overrides, "PATHEXT"),
	)
	if err != nil {
		return "", false, fmt.Errorf("resolve Node runtime for language server: %w", err)
	}
	cmd := hiddenexec.Command(nodePath, "--version")
	if pathEnv != "" {
		cmd.Env = append(os.Environ(), "PATH="+pathEnv)
	}
	output, err := cmd.Output()
	if err != nil {
		return "", false, fmt.Errorf("read Node runtime version: %w", err)
	}
	version := strings.TrimSpace(string(output))
	major, minor, err := runtimeParseNodeVersion(version)
	if err != nil {
		return "", false, err
	}
	result := runtimeNodeVersionResult{
		version:  version,
		portable: major > 24 || (major == 24 && minor >= 12),
	}
	return result.version, result.portable, nil
}

// runtimeServerLookPath 按显式 PATH 查找可执行文件，并拒绝当前目录与相对目录项。
func runtimeServerLookPath(file, pathEnv, pathExt string) (string, error) {
	if filepath.IsAbs(file) || strings.ContainsAny(file, `/\`) {
		return runtimeServerValidateExecutable(file)
	}
	if strings.TrimSpace(pathEnv) == "" {
		return exec.LookPath(file)
	}
	extensions := runtimeServerExecutableExtensions(file, pathExt)
	for _, dir := range filepath.SplitList(pathEnv) {
		if err := runtimeServerValidatePATHDirectory(file, dir); err != nil {
			return "", err
		}
		resolved, found, err := runtimeServerLookPathInDirectory(dir, file, extensions)
		if err != nil {
			return "", err
		}
		if found {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%w: %s", exec.ErrNotFound, file)
}

func runtimeServerValidateExecutable(file string) (string, error) {
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: %s", exec.ErrNotFound, file)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%w: %s", exec.ErrNotFound, file)
	}
	return file, nil
}

func runtimeServerExecutableExtensions(file, pathExt string) []string {
	if runtime.GOOS != "windows" || filepath.Ext(file) != "" {
		return []string{""}
	}
	extensions := filepath.SplitList(pathExt)
	if len(extensions) == 0 {
		return []string{".COM", ".EXE", ".BAT", ".CMD"}
	}
	return extensions
}

func runtimeServerValidatePATHDirectory(file, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("resolve %s: PATH contains unsafe current-directory entry %q", file, dir)
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("resolve %s: PATH contains unsafe current-directory entry %q", file, dir)
	}
	return nil
}

// runtimeServerLookPathInDirectory 在单个绝对 PATH 目录内查找第一个可执行候选。
func runtimeServerLookPathInDirectory(dir, file string, extensions []string) (string, bool, error) {
	for _, extension := range extensions {
		candidate := filepath.Join(dir, file+extension)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			continue
		}
		resolved, err := filepath.Abs(candidate)
		if err != nil {
			return "", false, err
		}
		return resolved, true, nil
	}
	return "", false, nil
}

type runtimeNodeVersionResult struct {
	version  string
	portable bool
}

// runtimeParseNodeVersion 解析 Node 主次版本，拒绝缺失或非数字版本。
func runtimeParseNodeVersion(version string) (int, int, error) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("parse Node runtime version %q", version)
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 0 || minor < 0 {
		return 0, 0, fmt.Errorf("parse Node runtime version %q", version)
	}
	return major, minor, nil
}

func runtimeServerEnvValue(overrides []string, key string) string {
	value, _ := runtimeServerEnvLookup(overrides, key)
	return value
}

// runtimeServerEnvLookup 按子进程 last-wins 语义读取配置，并保留 reject-only 旧键的存在性。
func runtimeServerEnvLookup(overrides []string, key string) (string, bool) {
	raw, configured := os.LookupEnv(key)
	value := strings.TrimSpace(raw)
	for _, entry := range overrides {
		entryKey, entryValue, ok := strings.Cut(entry, "=")
		if ok && entryKey == key {
			value = strings.TrimSpace(entryValue)
			configured = true
		}
	}
	return value, configured
}

// runtimeServerResolveGitCommonDir 校验 Git 目录，并为 linked worktree 读取必需的 commondir。
func runtimeServerResolveGitCommonDir(gitDir string, linked bool) (string, error) {
	info, err := os.Lstat(gitDir)
	if err != nil {
		return "", fmt.Errorf("inspect Git directory for resource cohort: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("Git directory for resource cohort is not a real directory: %s", gitDir)
	}
	commonDir := gitDir
	if linked {
		commonDir, err = runtimeServerReadGitCommonDir(gitDir)
		if err != nil {
			return "", err
		}
	}
	absCommon, err := filepath.Abs(commonDir)
	if err != nil {
		return "", fmt.Errorf("resolve Git common directory: %w", err)
	}
	absCommon, err = filepath.EvalSymlinks(absCommon)
	if err != nil {
		return "", fmt.Errorf("resolve real Git common directory: %w", err)
	}
	commonInfo, err := os.Stat(absCommon)
	if err != nil {
		return "", fmt.Errorf("stat Git common directory: %w", err)
	}
	if !commonInfo.IsDir() {
		return "", fmt.Errorf("Git common path is not a directory: %s", absCommon)
	}
	return filepath.Clean(absCommon), nil
}

// runtimeServerReadGitCommonDir 严格读取 linked worktree 必需的 commondir marker。
func runtimeServerReadGitCommonDir(gitDir string) (string, error) {
	marker := filepath.Join(gitDir, "commondir")
	info, err := os.Lstat(marker)
	if err != nil {
		return "", fmt.Errorf("inspect Git commondir marker: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", fmt.Errorf("Git commondir marker is invalid: %s", marker)
	}
	payload, err := os.ReadFile(marker)
	if err != nil {
		return "", fmt.Errorf("read Git commondir marker: %w", err)
	}
	commonDir := strings.TrimSpace(string(payload))
	if commonDir == "" {
		return "", errors.New("Git commondir marker is empty")
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	return commonDir, nil
}
