package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
)

type setupPaths struct {
	Worktree string
	Binary   string
	Config   string
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// resolvePaths 将 worktree、binary 和 config 规范化，并拒绝任何逃逸路径。
func resolvePaths(ctx context.Context, opts setupOptions) (setupPaths, error) {
	worktree, err := resolveWorktree(ctx, opts.Worktree)
	if err != nil {
		return setupPaths{}, err
	}
	binary, err := resolveOwnedPath(
		worktree, opts.Binary, filepath.Join(worktree, "bin", executableName("mcp-lsp")), "binary",
	)
	if err != nil {
		return setupPaths{}, err
	}
	config, err := resolveOwnedPath(
		worktree, opts.Config, filepath.Join(worktree, ".codex", "config.toml"), "config",
	)
	if err != nil {
		return setupPaths{}, err
	}
	return setupPaths{Worktree: worktree, Binary: binary, Config: config}, nil
}

// resolveWorktree 查找并规范化 Git worktree 根，且要求该根当前可访问。
func resolveWorktree(ctx context.Context, input string) (string, error) {
	worktree := strings.TrimSpace(input)
	if worktree == "" {
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
		raw, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("resolve Git worktree root: %w", err)
		}
		worktree = strings.TrimSpace(string(raw))
	}
	canonicalRoot, err := pathutil.NormalizeAbsolutePath(worktree)
	if err != nil || canonicalRoot == "" {
		return "", fmt.Errorf("resolve worktree path: %w", err)
	}
	info, err := os.Stat(canonicalRoot)
	if err != nil {
		return "", fmt.Errorf("stat worktree: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("worktree must be a directory: %s", canonicalRoot)
	}
	return canonicalRoot, nil
}

// resolveOwnedPath 规范化 worktree 内的产物路径，并拒绝符号链接或相对路径逃逸。
func resolveOwnedPath(worktree, input, fallback, kind string) (string, error) {
	path := strings.TrimSpace(input)
	if path == "" {
		path = fallback
	}
	canonical, err := pathutil.NormalizeAbsolutePath(path)
	if err != nil || canonical == "" {
		return "", fmt.Errorf("resolve %s path: %w", kind, err)
	}
	if !pathutil.ContainsPath(worktree, canonical) {
		return "", errors.New(kind + " path must stay inside worktree")
	}
	return canonical, nil
}

// configuredPath 将当前 worktree 的 binary 目录放到 PATH 首位并保留调用者 PATH。
func configuredPath(worktree, inherited string) string {
	entries := []string{filepath.Join(worktree, "bin")}
	seen := map[string]bool{entries[0]: true}
	for _, entry := range filepath.SplitList(inherited) {
		entry = strings.TrimSpace(entry)
		slashed := filepath.ToSlash(entry)
		if entry == "" || seen[entry] || strings.Contains(slashed, "/.codex/tmp/") ||
			strings.Contains(slashed, "/var/run/com.apple.security.cryptexd/") ||
			strings.Contains(slashed, "/System/Cryptexes/") ||
			strings.Contains(slashed, "/Applications/ChatGPT.app/") {
			continue
		}
		seen[entry] = true
		entries = append(entries, entry)
	}
	return strings.Join(entries, string(os.PathListSeparator))
}

// preflightLanguageServers 验证配置 PATH 中的 Go 与 JS/TS 运行时同伴均可执行。
func preflightLanguageServers(pathEnv string) (map[string]string, error) {
	found := make(map[string]string, 3)
	for _, name := range []string{"gopls", "typescript-language-server", "tsserver"} {
		path, err := findExecutable(pathEnv, name)
		if err != nil {
			hint := "go install golang.org/x/tools/gopls@latest"
			if name != "gopls" {
				hint = "npm install -g typescript-language-server typescript@5.9.3"
			}
			return nil, fmt.Errorf(
				"required language server companion %s is unavailable on configured PATH; run `%s`: %w",
				name, hint, err,
			)
		}
		found[name] = path
	}
	return found, nil
}

// findExecutable 仅接受 PATH 中存在且在当前平台可执行的普通文件。
func findExecutable(pathEnv, name string) (string, error) {
	for _, dir := range filepath.SplitList(pathEnv) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		candidate := filepath.Join(dir, executableName(name))
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("executable %q not found", name)
}
