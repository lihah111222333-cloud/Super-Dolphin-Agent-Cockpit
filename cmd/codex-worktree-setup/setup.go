package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type setupReport struct {
	Command          setupCommand
	Paths            setupPaths
	LanguageBinaries map[string]string
	FrontendDeps     string
	Tools            []string
}

type frontendCommandRunner func(context.Context, string, string, ...string) ([]byte, error)

// runSetup 执行 configure、ready 或 verify 的严格流水线。
func runSetup(ctx context.Context, opts setupOptions) (setupReport, error) {
	paths, err := resolvePaths(ctx, opts)
	if err != nil {
		return setupReport{}, err
	}
	switch opts.Command {
	case commandConfigure:
		if err := validateLSPBinary(paths.Binary); err != nil {
			return setupReport{}, err
		}
		pathEnv := configuredPath(paths.Worktree, os.Getenv("PATH"))
		if err := configureProject(paths, pathEnv); err != nil {
			return setupReport{}, err
		}
		return setupReport{Command: opts.Command, Paths: paths}, nil
	case commandReady:
		return readyProject(ctx, opts, paths)
	case commandVerify:
		return verifyProject(ctx, opts.Command, paths)
	default:
		return setupReport{}, fmt.Errorf("unsupported command %q", opts.Command)
	}
}

func readyProject(ctx context.Context, opts setupOptions, paths setupPaths) (setupReport, error) {
	if err := prepareFrontendDependencies(ctx, paths.Worktree, opts.FrontendCommand); err != nil {
		return setupReport{}, err
	}
	if err := buildLSPBinary(ctx, paths); err != nil {
		return setupReport{}, err
	}
	pathEnv := configuredPath(paths.Worktree, os.Getenv("PATH"))
	if _, err := preflightLanguageServers(pathEnv); err != nil {
		return setupReport{}, err
	}
	if err := configureProject(paths, pathEnv); err != nil {
		return setupReport{}, err
	}
	return verifyProject(ctx, opts.Command, paths)
}

// prepareFrontendDependencies 为当前 worktree 安装锁文件声明的前端依赖。
func prepareFrontendDependencies(ctx context.Context, worktree string, runner frontendCommandRunner) error {
	if err := validateFrontendManifest(worktree); err != nil {
		return err
	}
	if _, err := validateFrontendDependencies(worktree); err == nil {
		return nil
	}
	if runner == nil {
		runner = runFrontendCommand
	}
	frontendRoot := filepath.Join(worktree, "frontend-app")
	output, err := runner(ctx, frontendRoot, "npm", "ci")
	if err != nil {
		return fmt.Errorf("install frontend dependencies in current worktree: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if _, err := validateFrontendDependencies(worktree); err != nil {
		return fmt.Errorf("validate installed frontend dependencies: %w", err)
	}
	return nil
}

func runFrontendCommand(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func validateFrontendManifest(worktree string) error {
	for _, name := range []string{"package.json", "package-lock.json"} {
		path := filepath.Join(worktree, "frontend-app", name)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat frontend manifest %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("frontend manifest must be a regular file: %s", path)
		}
	}
	return nil
}

// validateFrontendDependencies 拒绝缺失、共享符号链接或相对锁文件过期的依赖目录。
func validateFrontendDependencies(worktree string) (string, error) {
	if err := validateFrontendManifest(worktree); err != nil {
		return "", err
	}
	frontendRoot := filepath.Join(worktree, "frontend-app")
	nodeModules, err := validateFrontendDependencyRoot(frontendRoot)
	if err != nil {
		return "", err
	}
	installedLock, err := validateFrontendInstalledLock(nodeModules)
	if err != nil {
		return "", err
	}
	if err := validateFrontendDependencyFreshness(frontendRoot, installedLock); err != nil {
		return "", err
	}
	if err := validateFrontendDependencyBinaries(nodeModules); err != nil {
		return "", err
	}
	return nodeModules, nil
}

func validateFrontendDependencyRoot(frontendRoot string) (string, error) {
	nodeModules := filepath.Join(frontendRoot, "node_modules")
	info, err := os.Lstat(nodeModules)
	if err != nil {
		return "", fmt.Errorf("frontend dependencies are unavailable in current worktree; run ready: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("frontend dependencies must be a worktree-local directory: %s", nodeModules)
	}
	return nodeModules, nil
}

func validateFrontendInstalledLock(nodeModules string) (os.FileInfo, error) {
	installedLock, err := os.Stat(filepath.Join(nodeModules, ".package-lock.json"))
	if err != nil {
		return nil, fmt.Errorf("frontend installed lock is unavailable; run ready: %w", err)
	}
	if !installedLock.Mode().IsRegular() {
		return nil, errors.New("frontend installed lock must be a regular file; run ready")
	}
	return installedLock, nil
}

func validateFrontendDependencyFreshness(frontendRoot string, installedLock os.FileInfo) error {
	for _, name := range []string{"package.json", "package-lock.json"} {
		manifest, statErr := os.Stat(filepath.Join(frontendRoot, name))
		if statErr != nil {
			return fmt.Errorf("stat frontend manifest %s: %w", name, statErr)
		}
		if manifest.ModTime().After(installedLock.ModTime()) {
			return fmt.Errorf("frontend dependencies are stale for %s; run ready", name)
		}
	}
	return nil
}

// validateFrontendDependencyBinaries 检查前端门禁所需的本地可执行文件。
func validateFrontendDependencyBinaries(nodeModules string) error {
	for _, name := range []string{"eslint", "vitest", "vite"} {
		path := filepath.Join(nodeModules, ".bin", nodeBinName(name))
		binary, statErr := os.Stat(path)
		if statErr != nil {
			return fmt.Errorf("frontend dependency executable %s is unavailable; run ready: %w", path, statErr)
		}
		if !binary.Mode().IsRegular() {
			return fmt.Errorf("frontend dependency executable must be a regular file: %s", path)
		}
		if runtime.GOOS != "windows" && binary.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("frontend dependency executable is not executable: %s", path)
		}
	}
	return nil
}

func nodeBinName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".cmd"
	}
	return name
}

// buildLSPBinary 无条件从当前 worktree 源码重建 sidecar。
func buildLSPBinary(ctx context.Context, paths setupPaths) error {
	if err := os.MkdirAll(filepath.Dir(paths.Binary), 0o755); err != nil {
		return fmt.Errorf("create binary directory: %w", err)
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", paths.Binary, "./cmd/mcp-lsp")
	cmd.Dir = paths.Worktree
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build current worktree mcp-lsp: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return validateLSPBinary(paths.Binary)
}

// validateLSPBinary 验证 binary 是 worktree 内可执行的普通文件。
func validateLSPBinary(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("mcp-lsp binary must be absolute: %s", path)
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("mcp-lsp binary is missing: %s; run `go run ./cmd/codex-worktree-setup ready`", path)
	}
	if err != nil {
		return fmt.Errorf("stat mcp-lsp binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("mcp-lsp binary must be a regular file: %s", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("mcp-lsp binary is not executable: %s", path)
	}
	return nil
}

// verifyProject 只读验证 config、binary、language servers 与真实 MCP tools/list。
func verifyProject(ctx context.Context, command setupCommand, paths setupPaths) (setupReport, error) {
	frontendDeps, err := validateFrontendDependencies(paths.Worktree)
	if err != nil {
		return setupReport{}, err
	}
	raw, err := os.ReadFile(paths.Config)
	if err != nil {
		return setupReport{}, fmt.Errorf("read Codex config: %w", err)
	}
	server, err := validateDecodedConfig(raw, paths)
	if err != nil {
		return setupReport{}, err
	}
	if err := validateLSPBinary(paths.Binary); err != nil {
		return setupReport{}, err
	}
	languageBinaries, err := preflightLanguageServers(server.Env["PATH"])
	if err != nil {
		return setupReport{}, err
	}
	tools, err := probeMCP(ctx, paths.Binary, paths.Worktree, server.Env)
	if err != nil {
		return setupReport{}, err
	}
	return setupReport{
		Command: command, Paths: paths, LanguageBinaries: languageBinaries, FrontendDeps: frontendDeps, Tools: tools,
	}, nil
}

// writeReport 输出可复查路径、依赖、工具面与新 task 重载提示。
func writeReport(writer io.Writer, report setupReport) {
	_, _ = fmt.Fprintf(writer, "command: %s\nworktree: %s\nbinary: %s\nconfig: %s\n",
		report.Command, report.Paths.Worktree, report.Paths.Binary, report.Paths.Config)
	if report.FrontendDeps != "" {
		_, _ = fmt.Fprintf(writer, "frontend-dependencies: %s\n", report.FrontendDeps)
	}
	keys := make([]string, 0, len(report.LanguageBinaries))
	for key := range report.LanguageBinaries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, _ = fmt.Fprintf(writer, "language-server %s: %s\n", key, report.LanguageBinaries[key])
	}
	if len(report.Tools) > 0 {
		_, _ = fmt.Fprintf(writer, "tools: %s\n", strings.Join(report.Tools, ","))
	}
	_, _ = fmt.Fprintln(writer, "start a new Codex task to load the worktree-local LSP server")
}
