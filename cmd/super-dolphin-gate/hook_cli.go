package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gatehook"
)

const managedGitHookInvocationID = "super-dolphin-managed-git-hook-v1"

func runHook(args []string, input io.Reader, stdout io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return infrastructureError("resolve hook cwd: %v", err)
	}
	return runHookWithConnector(args, input, stdout, cwd, connectProductionHookCoordinator)
}

// runHookWithConnector 严格解析 typed adapter 参数并在单一 coordinator 生命周期内执行。
func runHookWithConnector(
	args []string,
	input io.Reader,
	stdout io.Writer,
	cwd string,
	connector hookCoordinatorConnector,
) error {
	if len(args) == 0 {
		return protocolError("hook requires an adapter (pre-commit, pre-push, codex)")
	}
	if args[0] == "codex" {
		if len(args) != 1 {
			return protocolError("codex hook accepts no adapter arguments")
		}
		return runCodexHook(input, stdout, connector)
	}
	return withHookCoordinator(context.Background(), connector, func(ctx context.Context, coordinator hookCoordinator) error {
		switch args[0] {
		case "pre-commit":
			if len(args) != 1 {
				return protocolError("pre-commit hook accepts no adapter arguments")
			}
			return runPreCommitHook(ctx, cwd, coordinator)
		case "pre-push":
			if len(args) != 3 || strings.TrimSpace(args[1]) == "" || strings.TrimSpace(args[2]) == "" {
				return protocolError("pre-push hook requires exact remote name and URL arguments")
			}
			return runPrePushHook(ctx, cwd, input, coordinator, args[2])
		default:
			return protocolError("unsupported hook adapter %q", args[0])
		}
	})
}

func runPreCommitHook(ctx context.Context, cwd string, coordinator gatehook.Coordinator) error {
	request, err := gatehook.NormalizePreCommit(ctx, cwd, managedGitHookInvocationID)
	if err != nil {
		return sourceError("normalize staged index tree: %v", err)
	}
	status, executeErr := executeHookRequest(ctx, coordinator, request)
	return gitHookDecision(status, request.Submit.Source.SourceTreeSHA, executeErr)
}

// runPrePushHook 为每条已验收 ref update 取得并消费独立 git.push grant。
func runPrePushHook(
	ctx context.Context,
	cwd string,
	input io.Reader,
	coordinator gatehook.Coordinator,
	remoteURL string,
) error {
	requests, err := gatehook.NormalizePrePush(ctx, cwd, managedGitHookInvocationID, input)
	if err != nil {
		return sourceError("normalize pre-push refs: %v", err)
	}
	for index, request := range requests {
		status, executeErr := executeHookRequest(ctx, coordinator, request)
		if err := gitHookDecision(status, request.Submit.Source.SourceTreeSHA, executeErr); err != nil {
			return fmt.Errorf("pre-push ref update %d: %w", index+1, err)
		}
		authorizer, ok := coordinator.(interface {
			AuthorizeGitPush(context.Context, gitPushGrantRequest) error
		})
		if !ok {
			return fmt.Errorf("pre-push ref update %d: action grant authority is unavailable", index+1)
		}
		if err := authorizer.AuthorizeGitPush(ctx, gitPushGrantRequest{
			Status: status, Submit: *request.Submit, RemoteURL: remoteURL,
		}); err != nil {
			return fmt.Errorf("pre-push ref update %d: %w", index+1, err)
		}
	}
	return nil
}

func runCodexHook(input io.Reader, stdout io.Writer, connector hookCoordinatorConnector) error {
	ctx := context.Background()
	request, err := gatehook.NormalizeCodexHook(ctx, input)
	if err != nil {
		return encodeCodexDecision(stdout, blockedCodexDecision(gatehook.JobStatus{}, err))
	}
	var status gatehook.JobStatus
	executeErr := withHookCoordinator(ctx, connector, func(ctx context.Context, coordinator hookCoordinator) error {
		status, err = executeHookRequest(ctx, coordinator, request)
		return err
	})
	if executeErr != nil {
		return encodeCodexDecision(stdout, blockedCodexDecision(status, executeErr))
	}
	decision, err := gatehook.DecisionForStatus(status, requestSourceTree(request))
	if err != nil {
		decision = blockedCodexDecision(status, err)
	}
	return encodeCodexDecision(stdout, decision)
}

// executeHookRequest 只分派 typed submit、status 或 wait 分支。
func executeHookRequest(
	ctx context.Context,
	coordinator gatehook.Coordinator,
	request gatehook.Request,
) (gatehook.JobStatus, error) {
	if coordinator == nil {
		return gatehook.JobStatus{}, errCoordinatorDependency
	}
	if err := request.Validate(); err != nil {
		return gatehook.JobStatus{}, err
	}
	switch request.Kind {
	case gatehook.RequestKindSubmit:
		return coordinator.Submit(ctx, *request.Submit)
	case gatehook.RequestKindStatus:
		return coordinator.Status(ctx, *request.Status)
	case gatehook.RequestKindWait:
		return coordinator.Wait(ctx, *request.Wait)
	default:
		return gatehook.JobStatus{}, fmt.Errorf("unsupported hook request kind %q", request.Kind)
	}
}

func gitHookDecision(status gatehook.JobStatus, sourceTree string, executeErr error) error {
	if executeErr != nil {
		return gatecontract.WithExitCode(
			gatecontract.ExitInfrastructure,
			fmt.Errorf("gate hook blocked: %s", hookStatusReason(status, executeErr)),
		)
	}
	decision, err := gatehook.DecisionForStatus(status, sourceTree)
	if err != nil {
		return gatecontract.WithExitCode(
			gatecontract.ExitInfrastructure,
			fmt.Errorf("gate hook blocked: %s", hookStatusReason(status, err)),
		)
	}
	if decision.Continue {
		return nil
	}
	return gatecontract.WithExitCode(hookStateExitCode(status.State), errors.New(decision.Reason))
}

func blockedCodexDecision(status gatehook.JobStatus, err error) gatehook.CodexDecision {
	return gatehook.CodexDecision{Decision: "block", Reason: "gate hook blocked: " + hookStatusReason(status, err)}
}

func hookStatusReason(status gatehook.JobStatus, err error) string {
	reason := err.Error()
	if status.JobID == "" {
		return reason + "; action: restore coordinator reachability and rerun the hook"
	}
	return fmt.Sprintf(
		"%s; job=%s; status: super-dolphin-gate status --job %s; wait: super-dolphin-gate wait --job %s",
		reason,
		status.JobID,
		status.JobID,
		status.JobID,
	)
}

func requestSourceTree(request gatehook.Request) string {
	if request.Submit != nil {
		return request.Submit.Source.SourceTreeSHA
	}
	if request.Status != nil {
		return request.Status.ExpectedSourceTreeSHA
	}
	return ""
}

func hookStateExitCode(state gatehook.JobState) gatecontract.ExitCode {
	switch state {
	case gatehook.JobStateFailed:
		return gatecontract.ExitGateViolation
	case gatehook.JobStateCancelled:
		return gatecontract.ExitCancelled
	case gatehook.JobStateTimeout:
		return gatecontract.ExitTimeout
	default:
		return gatecontract.ExitInfrastructure
	}
}

func encodeCodexDecision(stdout io.Writer, decision gatehook.CodexDecision) error {
	if err := json.NewEncoder(stdout).Encode(decision); err != nil {
		return infrastructureError("encode Codex hook decision: %v", err)
	}
	return nil
}

func withHookCoordinator(
	ctx context.Context,
	connector hookCoordinatorConnector,
	action func(context.Context, hookCoordinator) error,
) error {
	coordinator, err := connector(ctx)
	if err != nil {
		return fmt.Errorf("connect coordinator owner: %w", err)
	}
	actionErr := action(ctx, coordinator)
	return errors.Join(actionErr, coordinator.Close())
}
