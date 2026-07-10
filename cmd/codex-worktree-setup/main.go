// Package main 提供 Codex Git worktree 的仓库内 LSP 准备命令。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

type setupCommand string

const (
	commandConfigure setupCommand = "configure"
	commandReady     setupCommand = "ready"
	commandVerify    setupCommand = "verify"
)

type setupOptions struct {
	Command  setupCommand
	Worktree string
	Binary   string
	Config   string
}

// main 解析显式子命令并用稳定退出码报告配置错误或运行时失败。
func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

// runCLI 执行 setup 命令；参数错误返回 2，运行时失败返回 1。
func runCLI(args []string, stdout, stderr io.Writer) int {
	opts, err := parseCLI(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codex-worktree-setup: %v\n", err)
		return 2
	}
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, err := runSetup(ctx, opts)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codex-worktree-setup %s failed: %v\n", opts.Command, err)
		return 1
	}
	writeReport(stdout, report)
	return 0
}

// parseCLI 要求 configure、ready 或 verify 之一，并拒绝未消费的位置参数。
func parseCLI(args []string) (setupOptions, error) {
	if len(args) == 0 {
		return setupOptions{}, fmt.Errorf("subcommand is required (configure, ready, verify)")
	}
	command := setupCommand(strings.TrimSpace(args[0]))
	switch command {
	case commandConfigure, commandReady, commandVerify:
	default:
		return setupOptions{}, fmt.Errorf("unknown subcommand %q (valid: configure, ready, verify)", args[0])
	}
	opts := setupOptions{Command: command}
	flags := flag.NewFlagSet(string(command), flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.Worktree, "worktree", "", "Git worktree root")
	flags.StringVar(&opts.Binary, "binary", "", "worktree-local mcp-lsp binary")
	flags.StringVar(&opts.Config, "config", "", "project-local Codex config")
	if err := flags.Parse(args[1:]); err != nil {
		return setupOptions{}, err
	}
	if flags.NArg() != 0 {
		return setupOptions{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	return opts, nil
}
