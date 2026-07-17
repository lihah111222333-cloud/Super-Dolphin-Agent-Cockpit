package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// main 绑定容器信号、运行单个门禁并按子进程结果退出。
func main() {
	termContext, stopTerm := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stopTerm()
	ctx, stopInterrupt := signal.NotifyContext(termContext, os.Interrupt)
	defer stopInterrupt()
	err := run(ctx, os.Args[1:])
	if err != nil {
		slog.Error("super-dolphin gate executor failed", "error", err)
	}
	exitCode := gate.ExecutorExitCode(err)
	if termContext.Err() != nil {
		exitCode = signalExitCode(syscall.SIGTERM)
	} else if ctx.Err() != nil {
		exitCode = signalExitCode(syscall.SIGINT)
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func run(ctx context.Context, args []string) error {
	return gate.ExecuteExecutor(ctx, args, os.Stdout, os.Stderr)
}

func signalExitCode(caught os.Signal) int {
	signalValue, ok := caught.(syscall.Signal)
	if !ok {
		return 1
	}
	return 128 + int(signalValue)
}
