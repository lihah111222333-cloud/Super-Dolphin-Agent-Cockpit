package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/acpnode"
)

type stringList []string

// String 返回命令行重复参数的可读表示。
func (s *stringList) String() string { return fmt.Sprint([]string(*s)) }

// Set 追加一个经过 flag 包解析的重复参数值。
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], acpnode.DefaultProcessFactory()); err != nil {
		logRunFailure(err)
		if errors.Is(err, errExperimentalGate) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// logRunFailure 只输出固定分类和有界指纹，禁止把 peer、路径或底层错误原文写入日志。
func logRunFailure(err error) {
	redactor, redactionErr := acpnode.NewRedactor()
	if redactionErr != nil {
		slog.Error("acp-node run failed", "error_class", "redaction_unavailable")
		return
	}
	slog.Error("acp-node run failed", "error", redactor.LogValue(err))
}

var errExperimentalGate = errors.New("acp-node is experimental; pass --enable-experimental-acp")

func run(ctx context.Context, args []string, factory acpnode.ProcessFactory) error {
	flags, err := parseRunFlags(args)
	if err != nil {
		return err
	}
	cfg, err := makeLaunchConfig(flags)
	if err != nil {
		return err
	}
	return runClient(ctx, cfg, factory)
}

type runFlags struct {
	executable, cwd            string
	argv, env, allowEnv        []string
	startup, request, shutdown time.Duration
	maxMessage, maxStderr      int
}

// parseRunFlags 解析隔离 harness 的显式参数，并拒绝未授权的隐式输入。
func parseRunFlags(args []string) (runFlags, error) {
	fs := flag.NewFlagSet("acp-node", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	enabled := fs.Bool("enable-experimental-acp", false, "enable the isolated ACP development harness")
	executable := fs.String("executable", "", "absolute child executable")
	cwd := fs.String("cwd", "", "absolute existing child working directory")
	var argv stringList
	var env stringList
	var allowEnv stringList
	fs.Var(&argv, "arg", "one direct child argv element; may be repeated")
	fs.Var(&env, "env", "one explicit KEY=VALUE child environment entry; may be repeated")
	fs.Var(&allowEnv, "allow-env", "one explicit environment allowlist key; may be repeated")
	startup := fs.Duration("startup-timeout", 10*time.Second, "initialize handshake timeout")
	request := fs.Duration("request-timeout", 30*time.Second, "request timeout")
	shutdown := fs.Duration("shutdown-timeout", 5*time.Second, "each shutdown grace period")
	maxMessage := fs.Int("max-message", acpnode.DefaultMaxMessage, "maximum JSON message bytes")
	maxStderr := fs.Int("max-stderr", acpnode.DefaultMaxStderr, "maximum stderr bytes")
	if err := fs.Parse(args); err != nil {
		return runFlags{}, err
	}
	if !*enabled {
		return runFlags{}, errExperimentalGate
	}
	if len(fs.Args()) != 0 {
		return runFlags{}, fmt.Errorf("acp: unexpected positional arguments")
	}
	if len(env) == 0 {
		return runFlags{}, fmt.Errorf("acp: at least one --env entry is required")
	}
	if len(allowEnv) == 0 {
		return runFlags{}, fmt.Errorf("acp: --env requires at least one explicit --allow-env key")
	}
	return runFlags{
		executable: *executable,
		cwd:        *cwd,
		argv:       append([]string(nil), argv...),
		env:        append([]string(nil), env...),
		allowEnv:   append([]string(nil), allowEnv...),
		startup:    *startup,
		request:    *request,
		shutdown:   *shutdown,
		maxMessage: *maxMessage,
		maxStderr:  *maxStderr,
	}, nil
}

func makeLaunchConfig(flags runFlags) (acpnode.LaunchConfig, error) {
	cfg := acpnode.LaunchConfig{
		Enabled:         true,
		Executable:      flags.executable,
		CWD:             flags.cwd,
		Args:            append([]string(nil), flags.argv...),
		Env:             append([]string(nil), flags.env...),
		EnvAllowlist:    append([]string(nil), flags.allowEnv...),
		StartupTimeout:  flags.startup,
		RequestTimeout:  flags.request,
		ShutdownTimeout: flags.shutdown,
		MaxMessage:      flags.maxMessage,
		MaxStderr:       flags.maxStderr,
	}
	if err := cfg.Validate(); err != nil {
		return acpnode.LaunchConfig{}, err
	}
	return cfg, nil
}

// runClient 执行初始化握手并把退出原因合并为一个可观察错误。
func runClient(ctx context.Context, cfg acpnode.LaunchConfig, factory acpnode.ProcessFactory) error {
	client, err := acpnode.NewClient(cfg, factory, nil)
	if err != nil {
		return err
	}
	if err := client.Initialize(ctx, map[string]any{"name": "acp-node", "version": "dev"}); err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return closeClient(client, nil)
		}
		return closeClient(client, err)
	}
	var primary error
	select {
	case <-ctx.Done():
	case <-client.Done():
		primary = client.Err()
		if primary == nil {
			primary = client.WaitErr()
		}
	}
	return closeClient(client, primary)
}

// closeClient 等待幂等 closeDone，并合并终端与进程清理错误。
func closeClient(client *acpnode.Client, primary error) error {
	closeErr := client.Close()
	if primary == nil {
		primary = client.Err()
		if primary == nil && closeErr == nil {
			primary = client.WaitErr()
		}
	}
	return errors.Join(primary, closeErr)
}
