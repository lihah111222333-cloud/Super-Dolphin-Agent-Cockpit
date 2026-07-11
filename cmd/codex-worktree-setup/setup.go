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
	Tools            []string
}

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
	case commandVerify:
		return verifyProject(ctx, opts.Command, paths)
	default:
		return setupReport{}, fmt.Errorf("unsupported command %q", opts.Command)
	}
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
	return setupReport{Command: command, Paths: paths, LanguageBinaries: languageBinaries, Tools: tools}, nil
}

// writeReport 输出可复查路径、依赖、工具面与新 task 重载提示。
func writeReport(writer io.Writer, report setupReport) {
	_, _ = fmt.Fprintf(writer, "command: %s\nworktree: %s\nbinary: %s\nconfig: %s\n",
		report.Command, report.Paths.Worktree, report.Paths.Binary, report.Paths.Config)
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
