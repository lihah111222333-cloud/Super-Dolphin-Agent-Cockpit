package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gatehook"
)

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
	return runGitHookWithConnector(args, input, stdout, cwd, connector)
}

// runGitHookWithConnector 为一次 Git hook action 生成独立 delivery identity 后执行适配器。
func runGitHookWithConnector(
	args []string,
	input io.Reader,
	stdout io.Writer,
	cwd string,
	connector hookCoordinatorConnector,
) error {
	deliveryID, err := newHookDeliveryID()
	if err != nil {
		return infrastructureError("create hook delivery identity: %v", err)
	}
	return withHookCoordinator(context.Background(), connector, func(ctx context.Context, coordinator hookCoordinator) error {
		switch args[0] {
		case "pre-commit":
			if len(args) != 3 || args[1] != "--tree" || strings.TrimSpace(args[2]) == "" {
				return protocolError("pre-commit hook requires one --tree <staged-tree-sha> argument")
			}
			return runPreCommitHook(ctx, cwd, stdout, coordinator, deliveryID, args[2])
		case "pre-push":
			if len(args) != 3 || strings.TrimSpace(args[1]) == "" || strings.TrimSpace(args[2]) == "" {
				return protocolError("pre-push hook requires exact remote name and URL arguments")
			}
			return runPrePushHook(ctx, cwd, input, stdout, coordinator, args[2], deliveryID)
		default:
			return protocolError("unsupported hook adapter %q", args[0])
		}
	})
}

func runPreCommitHook(
	ctx context.Context,
	cwd string,
	stdout io.Writer,
	coordinator gatehook.Coordinator,
	deliveryID, stagedTreeSHA string,
) error {
	request, err := gatehook.NormalizePreCommit(ctx, cwd, deliveryID, stagedTreeSHA)
	if err != nil {
		return sourceError("normalize captured staged tree: %v", err)
	}
	status, executeErr := executeHookRequest(ctx, coordinator, request)
	if err := gitHookDecision(status, request.Submit.Source.SourceTreeSHA, executeErr); err != nil {
		return err
	}
	return writeGitHookPassedEvidence(stdout, status)
}

// runPrePushHook 为每条已验收 ref update 取得并消费独立 git.push grant。
func runPrePushHook(
	ctx context.Context,
	cwd string,
	input io.Reader,
	stdout io.Writer,
	coordinator gatehook.Coordinator,
	remoteURL string,
	deliveryID string,
) error {
	requests, err := gatehook.NormalizePrePush(ctx, cwd, deliveryID, input)
	if err != nil {
		return sourceError("normalize pre-push refs: %v", err)
	}
	actionAttemptID, err := newActionGrantAttemptID()
	if err != nil {
		return infrastructureError("create pre-push action attempt: %v", err)
	}
	for index, request := range requests {
		status, executeErr := executePrePushRequest(ctx, coordinator, request)
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
			ActionAttemptID: actionAttemptID,
		}); err != nil {
			return fmt.Errorf("pre-push ref update %d: %w", index+1, err)
		}
		if err := writeGitHookPassedEvidence(stdout, status); err != nil {
			return fmt.Errorf("pre-push ref update %d: %w", index+1, err)
		}
	}
	return nil
}

// executePrePushRequest 在同一次 hook action 内等待已提交 job 的终态，避免重试创建重复 invocation。
func executePrePushRequest(
	ctx context.Context,
	coordinator gatehook.Coordinator,
	request gatehook.Request,
) (gatehook.JobStatus, error) {
	status, err := executeHookRequest(ctx, coordinator, request)
	if err != nil || status.State != gatehook.JobStateQueued && status.State != gatehook.JobStateRunning {
		return status, err
	}
	wait, err := gatehook.WaitRequestForStatus(request.Submit.Repository, request.Submit.Invocation, status)
	if err != nil {
		return status, err
	}
	return coordinator.Wait(ctx, wait)
}

// newHookDeliveryID 为一次真实 Git hook action 分配不可预测的 delivery identity。
func newHookDeliveryID() (string, error) {
	entropy := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, entropy); err != nil {
		return "", fmt.Errorf("read hook delivery entropy: %w", err)
	}
	return "delivery:v1:" + hex.EncodeToString(entropy), nil
}

// newActionGrantAttemptID 为一次 pre-push invocation 创建不可预测的共同授权边界。
func newActionGrantAttemptID() (string, error) {
	entropy := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, entropy); err != nil {
		return "", fmt.Errorf("read action attempt entropy: %w", err)
	}
	return "attempt:v1:" + hex.EncodeToString(entropy), nil
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
	} else if decision.Continue {
		decision.Reason = hookPassedEvidence(status)
	}
	return encodeCodexDecision(stdout, decision)
}

func writeGitHookPassedEvidence(stdout io.Writer, status gatehook.JobStatus) error {
	if _, err := fmt.Fprintln(stdout, hookPassedEvidence(status)); err != nil {
		return infrastructureError("write passed hook evidence: %v", err)
	}
	return nil
}

func hookPassedEvidence(status gatehook.JobStatus) string {
	return fmt.Sprintf(
		"gate hook passed: job=%s; receipt=%s; source_tree=%s; status: super-dolphin-gate status --job %s",
		status.JobID,
		status.ReceiptID,
		status.SourceTreeSHA,
		status.JobID,
	)
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
