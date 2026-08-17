//go:build linux && e2e

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// resolveIdleRealToolPaths 只接受 PATH 中明确解析出的 Linux 工具身份；缺失或歧义必须失败。
func resolveIdleRealToolPaths(t *testing.T) idleRealToolPaths {
	t.Helper()
	nodeLookupPath, nodePath := requireLinuxIdleRealTool(t, "node")
	typeScriptLookupPath, typeScriptPath := requireLinuxIdleRealTool(t, "typescript-language-server")
	searchPaths := []string{filepath.Dir(typeScriptLookupPath), filepath.Dir(nodeLookupPath)}
	return idleRealToolPaths{
		nodePath:         nodePath,
		typeScriptPath:   typeScriptPath,
		serverSearchPath: strings.Join(uniqueLinuxIdleSearchPaths(searchPaths), string(os.PathListSeparator)),
	}
}

func requireLinuxIdleRealTool(t *testing.T, name string) (string, string) {
	t.Helper()
	lookupPath, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("Linux real idle E2E requires exact %s in PATH: %v", name, err)
	}
	if !filepath.IsAbs(lookupPath) {
		lookupPath, err = filepath.Abs(lookupPath)
		if err != nil {
			t.Fatalf("resolve absolute Linux real tool %s path %q: %v", name, lookupPath, err)
		}
	}
	if filepath.Base(lookupPath) != name {
		t.Fatalf("Linux real idle E2E resolved tool %s to unexpected launch name %q", name, lookupPath)
	}
	resolvedPath := requireLinuxIdleRealExecutableIdentity(t, name, lookupPath)
	identities := linuxIdleRealToolIdentities(t, name)
	if len(identities) != 1 || filepath.Clean(identities[0]) != filepath.Clean(resolvedPath) {
		t.Fatalf("Linux real idle E2E tool %s has ambiguous PATH identities %v; selected=%s", name, identities, resolvedPath)
	}
	t.Logf("Linux real idle tool identity: name=%s launch=%s resolved=%s", name, lookupPath, resolvedPath)
	return lookupPath, resolvedPath
}

// linuxIdleRealToolIdentities 枚举 PATH 中全部同名可执行文件，并按解析后的真实文件身份去重。
// 空目录或相对目录会让工具身份依赖 cwd，因此本正式 E2E 直接拒绝这类 PATH。
func linuxIdleRealToolIdentities(t *testing.T, name string) []string {
	t.Helper()
	rawPath := os.Getenv("PATH")
	if strings.TrimSpace(rawPath) == "" {
		t.Fatal("Linux real idle E2E PATH is empty")
	}
	identities := make(map[string]struct{})
	for _, directory := range filepath.SplitList(rawPath) {
		if strings.TrimSpace(directory) == "" || !filepath.IsAbs(directory) {
			t.Fatalf("Linux real idle E2E PATH contains an empty or relative directory %q", directory)
		}
		candidate := filepath.Join(directory, name)
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			continue
		} else if err != nil {
			t.Fatalf("inspect Linux real idle E2E candidate %s: %v", candidate, err)
		}
		resolved := requireLinuxIdleRealExecutableIdentity(t, name, candidate)
		identities[filepath.Clean(resolved)] = struct{}{}
	}
	result := make([]string, 0, len(identities))
	for identity := range identities {
		result = append(result, identity)
	}
	sort.Strings(result)
	return result
}

func requireLinuxIdleRealExecutableIdentity(t *testing.T, name, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		t.Fatalf("Linux real idle E2E tool %s is not an executable regular file at %s: %v", name, path, err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || strings.TrimSpace(resolved) == "" {
		t.Fatalf("Linux real idle E2E tool %s has no resolvable native target from %s: %v", name, path, err)
	}
	if !filepath.IsAbs(resolved) {
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			t.Fatalf("resolve absolute Linux real idle E2E identity %s: %v", resolved, err)
		}
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil || !resolvedInfo.Mode().IsRegular() || resolvedInfo.Mode()&0111 == 0 {
		t.Fatalf("Linux real idle E2E tool %s resolved to a non-executable target %s: %v", name, resolved, err)
	}
	return resolved
}

func uniqueLinuxIdleSearchPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	return result
}
