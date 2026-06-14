package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

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
		if bashDriveMountAvailable(drive) {
			return "/mnt/" + drive + "/" + rest
		}
	}
	return slashed
}

func bashDriveMountAvailable(drive string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if cached, ok := bashDriveMountCache.Load(drive); ok {
		return cached.(bool)
	}
	err := exec.Command("bash", "-lc", "test -d /mnt/"+drive).Run()
	available := err == nil
	bashDriveMountCache.Store(drive, available)
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
		env = upsertEnv(env, "GIT_DIR", gitRevParseRequired(t, "--absolute-git-dir"))
		env = upsertEnv(env, "GIT_WORK_TREE", gitRevParseRequired(t, "--show-toplevel"))
		keys = append(keys, "GIT_DIR/p", "GIT_WORK_TREE/p")
	}
	return appendWSLEnvKeys(env, keys...)
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

	want := bashArg("", scriptRepoRoot(t))
	if !strings.Contains(string(output), want) {
		t.Fatalf("WSL git rev-parse output = %q, want repository root %q", output, want)
	}
}
