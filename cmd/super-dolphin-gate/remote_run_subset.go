package main

import (
	"context"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type remoteRunPreparer func(*remoteci.Coordinator, context.Context, remoteci.RunInput) (*remoteci.PreparedRun, error)

// executeRemoteRunSubset 让已冻结的远程 workload 子集经过 normal remote authority 链。
// PrepareSubset 是唯一的子集差异，构造、MISS 绑定和最终化均复用既有 owner。
func executeRemoteRunSubset(
	options remoteRunOptions,
	selected []gate.GateID,
	excluded []gate.GateID,
) (remoteci.RunResult, remoteci.RunInput, error) {
	request := remoteci.RemoteSubsetRequest{
		Selected: slices.Clone(selected),
		Excluded: slices.Clone(excluded),
	}
	return executeRemoteRunWithPrepare(options, func(coordinator *remoteci.Coordinator, ctx context.Context, input remoteci.RunInput) (*remoteci.PreparedRun, error) {
		return coordinator.PrepareSubset(ctx, input, request)
	})
}

// executeRemoteRunWithPrepare 持有 source 选择后的唯一生产 CLI 链。
// 调用方只能选择 Coordinator 的 preparation 操作，不能复制 executor。
func executeRemoteRunWithPrepare(
	options remoteRunOptions,
	prepare remoteRunPreparer,
) (remoteci.RunResult, remoteci.RunInput, error) {
	var result remoteci.RunResult
	if prepare == nil {
		return result, remoteci.RunInput{}, protocolError("remote CI run preparer is required")
	}
	config, state, err := loadRunnableRemoteRunState(options)
	if err != nil {
		return result, remoteci.RunInput{}, err
	}
	runnerIdentity, err := resolveRemoteRunnerIdentity(options.RepositoryRoot, state)
	if err != nil {
		return result, remoteci.RunInput{}, infrastructureError("resolve remote worker execution identity: %v", err)
	}
	input, err := resolveRemoteRunInput(options, state, runnerIdentity)
	if err != nil {
		return result, remoteci.RunInput{}, sourceError("%v", err)
	}
	coordinator, containerDeadline, err := newRemoteRunCoordinator(config, input, options.ProgressObserver)
	if err != nil {
		return result, input, err
	}
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	runCtx, cancel := gateprivate.WithTimeout(signalContext, containerDeadline+10*time.Minute)
	defer cancel()
	prepared, err := prepare(coordinator, runCtx, input)
	if err != nil {
		return result, input, err
	}
	return executePreparedRemoteRun(
		runCtx, options, config, state, runnerIdentity, input, coordinator, prepared,
		defaultRemotePreparedRunDependencies(),
	)
}
