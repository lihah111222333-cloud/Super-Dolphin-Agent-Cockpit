package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// archguard:ignore global_vars -- caches expensive bash drive-mount probing across tests.
var bashDriveMountCache sync.Map

func bashPath(parts ...string) string {
	return filepath.ToSlash(filepath.Join(parts...))
}

func bashArgs(root string, args []string) []string {
	converted := make([]string, len(args))
	for i, arg := range args {
		converted[i] = bashArg(root, arg)
	}
	return converted
}

func bashArg(root, arg string) string {
	if filepath.IsAbs(arg) {
		if root != "" {
			if rel, err := filepath.Rel(root, arg); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return filepath.ToSlash(rel)
			}
		}
		return bashAbsolutePath(arg)
	}
	return filepath.ToSlash(arg)
}

func bashAbsolutePath(path string) string {
	slashed := filepath.ToSlash(path)
	volume := filepath.VolumeName(path)
	if len(volume) == 2 && volume[1] == ':' {
		drive := strings.ToLower(volume[:1])
		rest := strings.TrimLeft(filepath.ToSlash(strings.TrimPrefix(path, volume)), "/")
		if root := bashDriveRoot(drive); root != "" {
			return root + "/" + rest
		}
	}
	return slashed
}

func bashDriveMountAvailable(drive string) bool {
	return bashDriveRoot(drive) != ""
}

func bashDriveRoot(drive string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, root := range []string{"/mnt/" + drive, "/" + drive} {
		if bashDirectoryAvailable(root) {
			return root
		}
	}
	return ""
}

func bashDirectoryAvailable(path string) bool {
	cacheKey := "dir:" + path
	if cached, ok := bashDriveMountCache.Load(cacheKey); ok {
		return cached.(bool)
	}
	err := exec.Command("bash", "-lc", "test -d "+bashQuote(path)).Run()
	available := err == nil
	bashDriveMountCache.Store(cacheKey, available)
	return available
}

func bashQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func bashVerifierPlatform() string {
	if runtime.GOOS == "windows" {
		return "linux-amd64"
	}
	return runtime.GOOS + "-" + runtime.GOARCH
}

func appendWSLEnvKeys(env []string, keys ...string) []string {
	keySet := wslEnvKeySet(keys...)
	mergeWSLEnvParts(keySet, wslEnvKeysFromEnv(env))
	mergeWSLEnvParts(keySet, wslEnvValue(env))
	parts := make([]string, 0, len(keySet))
	for key := range keySet {
		parts = append(parts, key)
	}
	sort.Strings(parts)
	return append(env, "WSLENV="+strings.Join(parts, ":"))
}

func wslEnvKeySet(keys ...string) map[string]struct{} {
	keySet := map[string]struct{}{}
	for _, key := range keys {
		if key != "" {
			keySet[key] = struct{}{}
		}
	}
	return keySet
}

func wslEnvKeysFromEnv(env []string) string {
	parts := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && key != "" && key != "WSLENV" {
			parts = append(parts, key)
		}
	}
	return strings.Join(parts, ":")
}

func wslEnvValue(env []string) string {
	existing := os.Getenv("WSLENV")
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key == "WSLENV" {
			existing = value
		}
	}
	return existing
}

func mergeWSLEnvParts(keySet map[string]struct{}, existing string) {
	for _, part := range strings.Split(existing, ":") {
		if wslEnvPartName(part) != "" {
			keySet[part] = struct{}{}
		}
	}
}

func wslEnvPartName(part string) string {
	if part == "" {
		return ""
	}
	if idx := strings.IndexByte(part, '/'); idx >= 0 {
		return part[:idx]
	}
	return part
}

func appendWSLEnvKeysWithGitWorktree(t testing.TB, env []string, keys ...string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		env = upsertEnv(env, "GIT_DIR", bashAbsolutePath(gitRevParseRequired(t, "--absolute-git-dir")))
		env = upsertEnv(env, "GIT_WORK_TREE", bashAbsolutePath(gitRevParseRequired(t, "--show-toplevel")))
		keys = append(keys, "GIT_DIR", "GIT_WORK_TREE")
	}
	env, keys = appendBashGitPathForWindows(env, keys...)
	return appendWSLEnvKeys(env, keys...)
}

func appendWSLEnvKeysWithGitPath(t testing.TB, env []string, keys ...string) []string {
	t.Helper()
	env, keys = appendBashGitPathForWindows(env, keys...)
	return appendWSLEnvKeys(env, keys...)
}

func appendBashGitPathForWindows(env []string, keys ...string) ([]string, []string) {
	if runtime.GOOS != "windows" {
		return env, keys
	}
	for _, entry := range []string{"/cmd", "/c/Program Files/Git/cmd", "/c/Program Files/Git/mingw64/bin", "/mnt/c/Program Files/Git/cmd", "/mnt/c/Program Files/Git/mingw64/bin"} {
		env = appendBashPathEntry(env, entry)
	}
	keys = append(keys, "PATH")
	if gitDir := bashCommandDirWithEnv(env, "git"); gitDir != "" {
		env = appendBashPathEntry(env, gitDir)
		return env, keys
	}
	if gitDir := bashCommandDirWithEnv(nil, "git"); gitDir != "" {
		env = appendBashPathEntry(env, gitDir)
		return env, keys
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return env, keys
	}
	env = appendBashPathDir(env, filepath.Dir(gitPath))
	return env, keys
}

func bashCommandAvailable(name string) bool {
	return bashCommandAvailableWithEnv(nil, name)
}

func bashCommandAvailableWithEnv(env []string, name string) bool {
	return bashCommandPathWithEnv(env, name) != ""
}

func bashCommandDirWithEnv(env []string, name string) string {
	commandPath := bashCommandPathWithEnv(env, name)
	if commandPath == "" {
		return ""
	}
	if idx := strings.LastIndexByte(commandPath, '/'); idx > 0 {
		return commandPath[:idx]
	}
	return ""
}

func bashCommandPathWithEnv(env []string, name string) string {
	if runtime.GOOS != "windows" {
		return name
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-lc", "command -v "+bashQuote(name))
	if env != nil {
		cmd.Env = env
	}
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stripWSLInteropBanner(string(output)))
}

func appendBashPathDir(env []string, dir string) []string {
	if strings.TrimSpace(dir) == "" {
		return env
	}
	return appendBashPathEntry(env, bashAbsolutePath(dir))
}

func appendBashPathEntry(env []string, entry string) []string {
	if strings.TrimSpace(entry) == "" {
		return env
	}
	found := false
	for i, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok && key == "PATH" {
			found = true
			env[i] = "PATH=" + value + ":" + entry
		}
	}
	if found {
		return env
	}
	return append(env, "PATH="+entry)
}

func gitRevParseRequired(t testing.TB, arg string) string {
	t.Helper()
	output, err := exec.Command("git", "rev-parse", arg).CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse %s failed: %v\n%s", arg, err, output)
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		t.Fatalf("git rev-parse %s returned an empty path", arg)
	}
	return value
}

func upsertEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func stripWSLInteropBanner(output string) string {
	output = strings.ReplaceAll(output, "\x00", "")
	lines := strings.Split(output, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if isWSLInteropBannerLine(strings.TrimSpace(line)) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func isWSLInteropBannerLine(line string) bool {
	return strings.HasPrefix(line, "wsl:") &&
		strings.Contains(line, "WSL") &&
		strings.Contains(line, "localhost")
}

func TestStripWSLInteropBannerKeepsScriptError(t *testing.T) {
	raw := "w\x00s\x00l\x00:\x00 \x00localhost W\x00S\x00L\x00 NAT\n\x00missing model registry: /tmp/models.yaml\n"
	want := "missing model registry: /tmp/models.yaml"
	if got := stripWSLInteropBanner(raw); got != want {
		t.Fatalf("stripWSLInteropBanner() = %q, want %q", got, want)
	}
}

func TestPackageScriptValidationEnvLetsWSLGitResolveWindowsWorktree(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows WSL gitdir path conversion regression")
	}

	cmd := exec.Command("bash", "-lc", "git rev-parse --show-toplevel")
	cmd.Dir = "."
	cmd.Env = packageScriptValidationEnv(t, "linux", nil)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("WSL git rev-parse failed: %v\n%s", err, output)
	}

	got := stripWSLInteropBanner(string(output))
	wantBash := bashArg("", scriptRepoRoot(t))
	wantWindowsGit := filepath.ToSlash(scriptRepoRoot(t))
	if !strings.Contains(got, wantBash) && !strings.Contains(got, wantWindowsGit) {
		t.Fatalf("WSL git rev-parse output = %q, want repository root %q or %q", output, wantBash, wantWindowsGit)
	}
}
