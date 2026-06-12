package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
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
	existing := wslEnvValue(env)
	for _, part := range strings.Split(existing, ":") {
		addWSLEnvPart(keySet, part)
	}
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

func addWSLEnvPart(keySet map[string]struct{}, part string) {
	if part == "" {
		return
	}
	name := part
	if idx := strings.IndexByte(part, '/'); idx >= 0 {
		name = part[:idx]
	}
	if name != "" {
		keySet[part] = struct{}{}
	}
}
