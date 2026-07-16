package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

type coordinatorClient interface {
	Submit(context.Context, submitRequest) (jobStatus, error)
	Status(context.Context, string) (jobStatus, error)
	Wait(context.Context, string) (jobStatus, error)
	Close() error
}

type coordinatorConnector func(context.Context) (coordinatorClient, error)

// connectProductionCoordinator 以真实 Docker daemon identity 发现唯一 owner。
func connectProductionCoordinator(ctx context.Context) (coordinatorClient, error) {
	checkpoint, err := localci.ProbeDockerSchedulerAuthority(ctx)
	if err != nil {
		return nil, fmt.Errorf("establish Docker scheduler authority: %w", err)
	}
	return connectCoordinator(ctx, checkpoint, executableOwnerStarter{})
}

// runSubmit 先生成 canonical plan，再持久化独立 invocation/job 并提交 scheduler。
func runSubmit(args []string, stdout io.Writer) error {
	return runSubmitWithConnector(args, stdout, connectProductionCoordinator)
}

// runSubmitWithConnector 为测试保留显式 connector，不使用包级可变服务定位器。
func runSubmitWithConnector(args []string, stdout io.Writer, connector coordinatorConnector) error {
	plan, err := parsePlan(args)
	if err != nil {
		return err
	}
	repositoryRoot, err := os.Getwd()
	if err != nil {
		return infrastructureError("resolve submit repository root: %v", err)
	}
	return withCoordinator(context.Background(), connector, func(ctx context.Context, client coordinatorClient) error {
		status, submitErr := client.Submit(ctx, submitRequest{RepositoryRoot: repositoryRoot, Plan: plan})
		if submitErr != nil {
			return infrastructureError("submit gate job: %v", submitErr)
		}
		return encodeCoordinatorStatus(stdout, status)
	})
}

// runStatus 读取 owner-global scheduler 与 durable job 的一致状态。
func runStatus(args []string, stdout io.Writer) error {
	return runStatusWithConnector(args, stdout, connectProductionCoordinator)
}

// runStatusWithConnector 通过调用方提供的严格 connector 查询 job。
func runStatusWithConnector(args []string, stdout io.Writer, connector coordinatorConnector) error {
	jobID, err := parseRequiredFlag("status", "job", args)
	if err != nil {
		return err
	}
	return withCoordinator(context.Background(), connector, func(ctx context.Context, client coordinatorClient) error {
		status, statusErr := client.Status(ctx, jobID)
		if statusErr != nil {
			return infrastructureError("read gate job status: %v", statusErr)
		}
		return encodeCoordinatorStatus(stdout, status)
	})
}

// runWait 等待结构化终态，并把终态映射为稳定进程退出码。
func runWait(args []string, stdout io.Writer) error {
	return runWaitWithConnector(args, stdout, connectProductionCoordinator)
}

// runWaitWithConnector 通过调用方提供的严格 connector 等待 job。
func runWaitWithConnector(args []string, stdout io.Writer, connector coordinatorConnector) error {
	jobID, err := parseRequiredFlag("wait", "job", args)
	if err != nil {
		return err
	}
	return withCoordinator(context.Background(), connector, func(ctx context.Context, client coordinatorClient) error {
		status, waitErr := client.Wait(ctx, jobID)
		if waitErr != nil {
			return infrastructureError("wait for gate job: %v", waitErr)
		}
		if err := encodeCoordinatorStatus(stdout, status); err != nil {
			return err
		}
		return terminalStatusError(status)
	})
}

func withCoordinator(
	ctx context.Context,
	connector coordinatorConnector,
	action func(context.Context, coordinatorClient) error,
) error {
	client, err := connector(ctx)
	if err != nil {
		return infrastructureError("connect coordinator: %v", err)
	}
	actionErr := action(ctx, client)
	closeErr := client.Close()
	if closeErr != nil {
		closeErr = infrastructureError("close coordinator client: %v", closeErr)
	}
	return errors.Join(actionErr, closeErr)
}

func encodeCoordinatorStatus(stdout io.Writer, status jobStatus) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(status); err != nil {
		return infrastructureError("encode coordinator status JSON: %v", err)
	}
	return nil
}

// terminalStatusError 严格映射 wait 的终态，不接受非终态成功返回。
func terminalStatusError(status jobStatus) error {
	switch status.State {
	case jobStatePassed:
		return nil
	case jobStateFailed:
		return gatecontract.WithExitCode(gatecontract.ExitGateViolation, errors.New("gate job failed"))
	case jobStateCancelled:
		return gatecontract.WithExitCode(gatecontract.ExitCancelled, errors.New("gate job cancelled"))
	case jobStateTimeout:
		return gatecontract.WithExitCode(gatecontract.ExitTimeout, errors.New("gate job timed out"))
	case jobStateInfraFailed:
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, errors.New("gate job infrastructure failed"))
	default:
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf("wait returned non-terminal state %q", status.State))
	}
}

func infrastructureError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf(format, args...))
}
