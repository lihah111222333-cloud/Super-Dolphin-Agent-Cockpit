package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	gateclosure "github.com/lihah111222333-cloud/super-dolphin-agent/build/gate/closure"
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
	config, err := loadProductionCoordinatorConfig()
	if err != nil {
		return nil, err
	}
	planner, err := newProductionCandidateSubmissionPlanner(ctx, config)
	if err != nil {
		return nil, err
	}
	checkpoint, err := localci.ProbeDockerSchedulerAuthority(ctx)
	if err != nil {
		return nil, fmt.Errorf("establish Docker scheduler authority: %w", err)
	}
	return newDeferredCoordinatorClient(ctx, checkpoint, planner, func(connectCtx context.Context) (*localci.SchedulerClient, error) {
		return connectScheduler(connectCtx, checkpoint, executableOwnerStarter{})
	})
}

// runSubmit 先生成 canonical plan，再持久化独立 invocation/job 并提交 scheduler。
func runSubmit(args []string, stdout io.Writer) error {
	return runSubmitWithConnector(args, stdout, connectProductionCoordinator)
}

// runProductionLauncherCLI is the fixed dispatch target installed by production provision.
func runProductionLauncherCLI(args []string, stdout io.Writer) error {
	return runProductionLauncherWithConnector(
		args, stdout, loadProductionCoordinatorConfig, os.Getwd, connectProductionCoordinator, dispatchProductionLauncherFallback,
	)
}

// dispatchProductionLauncherFallback 委托既有顶层 dispatcher，避免 launcher 重复维护命令表。
func dispatchProductionLauncherFallback(args []string, stdout io.Writer) error {
	return dispatchCLI(args, stdout)
}

type productionLauncherSubmit struct {
	plan            gatecontract.GatePlan
	waitForTerminal bool
	releaseGrant    *releaseGrantOptions
}

// runProductionLauncherWithConnector 保留 production launcher 的全部 CLI 命令面，只有 canonical release submit 进入 authority adapter。
func runProductionLauncherWithConnector(
	args []string,
	stdout io.Writer,
	loadConfig func() (productionCoordinatorConfig, error),
	resolveRepositoryRoot func() (string, error),
	connector coordinatorConnector,
	fallback func([]string, io.Writer) error,
) error {
	if len(args) == 0 {
		return fallback(args, stdout)
	}
	if args[0] != "submit" {
		return fallback(args, stdout)
	}
	submit, err := parseProductionLauncherSubmit(args[1:])
	if err != nil {
		return err
	}
	if submit.plan.Profile != gatecontract.ProfileRelease {
		if submit.releaseGrant != nil {
			return protocolError("release grant options require the release profile")
		}
		return runSubmitWithConnector(args[1:], stdout, connector)
	}
	config, err := loadConfig()
	if err != nil {
		return infrastructureError("load production launcher config: %v", err)
	}
	repositoryRoot, err := resolveRepositoryRoot()
	if err != nil {
		return infrastructureError("resolve production release repository root: %v", err)
	}
	return runProductionReleaseSubmitPlanWithWaitConnector(
		submit.plan,
		stdout,
		config,
		repositoryRoot,
		connector,
		submit.waitForTerminal,
		submit.releaseGrant,
	)
}

// parseProductionLauncherSubmit 同时解析 canonical plan、终态等待和可选的 release grant 参数。
func parseProductionLauncherSubmit(args []string) (productionLauncherSubmit, error) {
	planArgsWithReleaseGrant, releaseGrant, err := extractReleaseGrantOptions(args, true)
	if err != nil {
		return productionLauncherSubmit{}, err
	}
	planArgs, waitForTerminal, err := parseSubmitArgs(planArgsWithReleaseGrant)
	if err != nil {
		return productionLauncherSubmit{}, err
	}
	plan, err := parsePlan(planArgs)
	if err != nil {
		return productionLauncherSubmit{}, err
	}
	if releaseGrant != nil && !waitForTerminal {
		return productionLauncherSubmit{}, protocolError("release grant options require submit --wait")
	}
	return productionLauncherSubmit{plan: plan, waitForTerminal: waitForTerminal, releaseGrant: releaseGrant}, nil
}

// runProductionReleaseSubmitPlanWithWaitConnector 通过 release authority owner 提交计划，并按需等待权威终态。
func runProductionReleaseSubmitPlanWithWaitConnector(
	plan gatecontract.GatePlan,
	stdout io.Writer,
	config productionCoordinatorConfig,
	repositoryRoot string,
	connector coordinatorConnector,
	waitForTerminal bool,
	releaseGrant *releaseGrantOptions,
) error {
	if plan.Profile != gatecontract.ProfileRelease {
		return protocolError("production launcher only supports the release profile")
	}
	invocationID, err := newReleaseInvocationID()
	if err != nil {
		return infrastructureError("create production release invocation: %v", err)
	}
	attestation, err := signProductionReleaseAuthority(config, invocationID, plan)
	if err != nil {
		return infrastructureError("sign production release authority: %v", err)
	}
	return withCoordinator(context.Background(), connector, func(ctx context.Context, client coordinatorClient) error {
		status, submitErr := submitAuthoritativeRelease(ctx, client, config, submitRequest{
			RepositoryRoot:       repositoryRoot,
			Plan:                 plan,
			InvocationID:         invocationID,
			Entrypoint:           gatecontract.CIEntrypointRelease,
			AuthorityOwner:       gatecontract.CIEntrypointOwnerRelease,
			AuthorityAttestation: attestation,
		})
		if submitErr != nil {
			return infrastructureError("submit authoritative release gate job: %v", submitErr)
		}
		if releaseGrant == nil {
			return encodeSubmittedStatus(ctx, client, stdout, status, waitForTerminal)
		}
		return waitAndIssueProductionReleaseGrant(ctx, client, stdout, config, status.JobID, plan, *releaseGrant)
	})
}

func signProductionReleaseAuthority(
	config productionCoordinatorConfig,
	invocationID string,
	plan gatecontract.GatePlan,
) (string, error) {
	privateKey, err := loadProductionActionGrantPrivateKey(config)
	if err != nil {
		return "", err
	}
	attestation := gatecontract.ReleaseAuthorityAttestation{
		SchemaVersion: 1,
		Entrypoint:    gatecontract.CIEntrypointRelease,
		Owner:         gatecontract.CIEntrypointOwnerRelease,
		InvocationID:  invocationID,
		Source:        plan.Source,
		PlanDigest:    plan.PlanDigest,
		Signer:        config.ActionGrantAuthority.Signer,
	}
	payload, err := gatecontract.ReleaseAuthorityAttestationSigningPayload(attestation)
	if err != nil {
		return "", err
	}
	attestation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return gatecontract.EncodeReleaseAuthorityAttestation(attestation)
}

// runSubmitWithConnector 为测试保留显式 connector，不使用包级可变服务定位器。
func runSubmitWithConnector(args []string, stdout io.Writer, connector coordinatorConnector) error {
	planArgs, waitForTerminal, err := parseSubmitArgs(args)
	if err != nil {
		return err
	}
	plan, err := parsePlan(planArgs)
	if err != nil {
		return err
	}
	if plan.Profile == gatecontract.ProfileRelease {
		return protocolError("manual CLI cannot submit release; use the authoritative release entrypoint with its attestation")
	}
	repositoryRoot, err := os.Getwd()
	if err != nil {
		return infrastructureError("resolve submit repository root: %v", err)
	}
	return withCoordinator(context.Background(), connector, func(ctx context.Context, client coordinatorClient) error {
		status, submitErr := client.Submit(ctx, submitRequest{
			RepositoryRoot: repositoryRoot, Plan: plan,
			Entrypoint: manualSubmissionAuthority().Entrypoint, AuthorityOwner: manualSubmissionAuthority().Owner,
		})
		if submitErr != nil {
			return infrastructureError("submit gate job: %v", submitErr)
		}
		return encodeSubmittedStatus(ctx, client, stdout, status, waitForTerminal)
	})
}

// parseSubmitArgs extracts the submit-only terminal-observation option without widening plan's command surface.
func parseSubmitArgs(args []string) ([]string, bool, error) {
	planArgs := make([]string, 0, len(args))
	waitForTerminal := false
	for _, arg := range args {
		if arg != "--wait" {
			planArgs = append(planArgs, arg)
			continue
		}
		if waitForTerminal {
			return nil, false, protocolError("submit accepts --wait at most once")
		}
		waitForTerminal = true
	}
	return planArgs, waitForTerminal, nil
}

func manualSubmissionAuthority() submissionAuthority {
	return submissionAuthority{
		Entrypoint: gatecontract.CIEntrypointManualCLI,
		Owner:      gatecontract.CIEntrypointOwnerManualCLI,
	}
}

// runClosureCheck 对 hook 首次捕获的 staged tree 执行受信 CLI 内的不可缓存 closure witness。
func runClosureCheck(args []string) error {
	if len(args) != 3 || args[0] != "check" || args[1] != "--tree" {
		return protocolError("closure check requires one --tree <staged-tree-sha> argument")
	}
	tree := strings.TrimSpace(args[2])
	if tree == "" {
		return protocolError("closure check staged tree sha is required")
	}
	repositoryRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return infrastructureError("resolve closure repository root: %v", err)
	}
	if err := gateclosure.CheckTree(strings.TrimSpace(string(repositoryRoot)), tree); err != nil {
		return gatecontract.WithExitCode(gatecontract.ExitGateViolation, fmt.Errorf("gate-image closure check: %w", err))
	}
	return nil
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
	jobID, expectedTree, err := parseWaitArgs(args)
	if err != nil {
		return err
	}
	return withCoordinator(context.Background(), connector, func(ctx context.Context, client coordinatorClient) error {
		status, waitErr := client.Wait(ctx, jobID)
		if waitErr != nil {
			return infrastructureError("wait for gate job: %v", waitErr)
		}
		if expectedTree != "" && status.JobSourceTreeSHA != expectedTree {
			return gatecontract.WithExitCode(
				gatecontract.ExitGateViolation,
				fmt.Errorf("waited gate job tree %s does not match staged tree %s", status.JobSourceTreeSHA, expectedTree),
			)
		}
		return encodeTerminalStatus(stdout, status)
	})
}

// parseWaitArgs 解析 job 与可选的 authoritative staged tree，拒绝无绑定等待。
func parseWaitArgs(args []string) (string, string, error) {
	if len(args) == 2 && args[0] == "--job" && strings.TrimSpace(args[1]) != "" {
		return args[1], "", nil
	}
	if len(args) == 4 && args[0] == "--job" && strings.TrimSpace(args[1]) != "" &&
		args[2] == "--tree" && strings.TrimSpace(args[3]) != "" {
		return args[1], args[3], nil
	}
	return "", "", protocolError("wait requires --job <job-id> and optional --tree <staged-tree-sha>")
}

// encodeSubmittedStatus preserves asynchronous submit by default, while --wait keeps terminal observation on the same coordinator connection.
func encodeSubmittedStatus(
	ctx context.Context,
	client coordinatorClient,
	stdout io.Writer,
	submitted jobStatus,
	waitForTerminal bool,
) error {
	if !waitForTerminal {
		return encodeCoordinatorStatus(stdout, submitted)
	}
	status, err := client.Wait(ctx, submitted.JobID)
	if err != nil {
		return infrastructureError("wait for submitted gate job: %v", err)
	}
	return encodeTerminalStatus(stdout, status)
}

func encodeTerminalStatus(stdout io.Writer, status jobStatus) error {
	if err := encodeCoordinatorStatus(stdout, status); err != nil {
		return err
	}
	return terminalStatusError(status)
}

// runWorkflow keeps submit and terminal observation in one authoritative client lifetime.
func runWorkflow(_ []string, _ io.Writer) error {
	return protocolError("workflow-host is required for authority-bearing CI execution")
}

// runWorkflowWithConnectorAt 使用同一权威连接完成 workflow 的提交和终态观察，并将 OIDC 摘要固定为 invocation 身份。
func runWorkflowWithConnectorAt(
	args []string,
	stdout io.Writer,
	connector coordinatorConnector,
	repositoryRoot string,
	workflowAttestationDigest string,
) error {
	plan, err := parsePlan(args)
	if err != nil {
		return err
	}
	if err := validateWorkflowPlan(plan); err != nil {
		return err
	}
	if repositoryRoot == "" {
		repositoryRoot, err = os.Getwd()
		if err != nil {
			return infrastructureError("resolve workflow repository root: %v", err)
		}
	}
	if !validWorkflowAttestationDigest(workflowAttestationDigest) {
		return protocolError("workflow OIDC attestation digest is invalid")
	}
	invocationID := "workflow-" + strings.TrimPrefix(workflowAttestationDigest, "sha256:")
	return withCoordinator(context.Background(), connector, func(ctx context.Context, client coordinatorClient) error {
		submitted, submitErr := client.Submit(ctx, submitRequest{
			RepositoryRoot:       repositoryRoot,
			Plan:                 plan,
			Entrypoint:           gatecontract.CIEntrypointWorkflowRequired,
			AuthorityOwner:       gatecontract.CIEntrypointOwnerWorkflowRequired,
			AuthorityAttestation: workflowAttestationDigest,
			InvocationID:         invocationID,
		})
		if submitErr != nil {
			return infrastructureError("submit workflow gate job: %v", submitErr)
		}
		status, waitErr := client.Wait(ctx, submitted.JobID)
		if waitErr != nil {
			return infrastructureError("wait for workflow gate job: %v", waitErr)
		}
		if err := encodeCoordinatorStatus(stdout, status); err != nil {
			return err
		}
		return terminalStatusError(status)
	})
}

// validWorkflowAttestationDigest 校验 workflow OIDC 绑定摘要的固定 sha256 前缀和小写十六进制载荷。
func validWorkflowAttestationDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if character < '0' || character > '9' && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// validateWorkflowPlan 确保计划的 profile 与 source 均在唯一权威 workflow entrypoint 的许可范围内。
func validateWorkflowPlan(plan gatecontract.GatePlan) error {
	for _, entrypoint := range gatecontract.CIEntrypointRegistry() {
		if entrypoint.ID != gatecontract.CIEntrypointWorkflowRequired {
			continue
		}
		if err := entrypoint.Validate(); err != nil {
			return infrastructureError("validate workflow entrypoint: %v", err)
		}
		if !slices.Contains(entrypoint.AllowedProfiles, plan.Profile) ||
			!slices.Contains(entrypoint.AllowedSources, plan.Source.Kind) {
			return protocolError("workflow plan is not permitted by authoritative workflow entrypoint")
		}
		return nil
	}
	return infrastructureError("authoritative workflow entrypoint is unavailable")
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
	return encodeCoordinatorJSON(stdout, status)
}

func encodeCoordinatorJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return infrastructureError("encode coordinator JSON: %v", err)
	}
	return nil
}

// terminalStatusError 严格映射 wait 的终态，不接受非终态成功返回。
func terminalStatusError(status jobStatus) error {
	detail := terminalLogQuery(status)
	switch status.State {
	case jobStatePassed:
		return nil
	case jobStateFailed:
		return gatecontract.WithExitCode(gatecontract.ExitGateViolation, fmt.Errorf("gate job failed%s", detail))
	case jobStateCancelled:
		return gatecontract.WithExitCode(gatecontract.ExitCancelled, errors.New("gate job cancelled"))
	case jobStateTimeout:
		return gatecontract.WithExitCode(gatecontract.ExitTimeout, fmt.Errorf("gate job timed out%s", detail))
	case jobStateInfraFailed:
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf("gate job infrastructure failed%s", detail))
	default:
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf("wait returned non-terminal state %q", status.State))
	}
}

func terminalLogQuery(status jobStatus) string {
	if status.JobID == "" || len(status.GateResults) == 0 {
		return ""
	}
	gateID := status.GateResults[len(status.GateResults)-1].GateID
	return fmt.Sprintf("; inspect: super-dolphin-gate logs --job %s --gate %s", status.JobID, gateID)
}

func infrastructureError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf(format, args...))
}

// validateSubmissionAuthority 把 submit profile 绑定到唯一 entrypoint owner 和可验证 attestation。
func validateSubmissionAuthority(request submitRequest) error {
	entrypoint, err := submissionEntrypoint(request.Entrypoint)
	if err != nil {
		return err
	}
	if entrypoint.Owner != request.AuthorityOwner {
		return errors.New("submit authority owner does not match entrypoint")
	}
	if request.Plan.Profile == gatecontract.ProfileRelease {
		return validateReleaseSubmissionAuthority(entrypoint, request)
	}
	if entrypoint.ID == gatecontract.CIEntrypointWorkflowRequired {
		return validateWorkflowSubmissionAuthority(entrypoint, request)
	}
	return validateNonReleaseSubmissionAuthority(entrypoint, request.AuthorityAttestation)
}

func validateWorkflowSubmissionAuthority(entrypoint gatecontract.CIEntrypoint, request submitRequest) error {
	if !entrypoint.Authoritative {
		return errors.New("workflow submit requires the authoritative workflow entrypoint")
	}
	if err := validateWorkflowPlan(request.Plan); err != nil {
		return err
	}
	if !validWorkflowAttestationDigest(request.AuthorityAttestation) ||
		request.InvocationID != "workflow-"+strings.TrimPrefix(request.AuthorityAttestation, "sha256:") {
		return errors.New("authoritative workflow submit requires its OIDC attestation-bound invocation")
	}
	return nil
}

func submissionEntrypoint(id gatecontract.CIEntrypointID) (gatecontract.CIEntrypoint, error) {
	for _, entrypoint := range gatecontract.CIEntrypointRegistry() {
		if entrypoint.ID == id {
			return entrypoint, nil
		}
	}
	return gatecontract.CIEntrypoint{}, fmt.Errorf("unknown submit entrypoint %q", id)
}

// validateReleaseSubmissionAuthority 要求 release 入口持有已验签的不可构造能力。
func validateReleaseSubmissionAuthority(entrypoint gatecontract.CIEntrypoint, request submitRequest) error {
	if entrypoint.ID != gatecontract.CIEntrypointRelease || !entrypoint.Authoritative {
		return errors.New("release submit requires the authoritative release entrypoint")
	}
	if request.VerifiedRelease == nil || request.VerifiedRelease.attestation != request.AuthorityAttestation {
		return errors.New("release submit requires a verified release-owner attestation")
	}
	attestation, err := gatecontract.DecodeReleaseAuthorityAttestation(request.AuthorityAttestation)
	if err != nil {
		return fmt.Errorf("decode verified release attestation: %w", err)
	}
	if attestation.InvocationID != request.InvocationID || !reflect.DeepEqual(attestation.Source, request.Plan.Source) ||
		attestation.PlanDigest != request.Plan.PlanDigest {
		return errors.New("release attestation does not bind submit invocation, source, and plan")
	}
	return nil
}

// verifyReleaseSubmissionAuthority is the only constructor for the opaque
// release capability accepted by submit. Its key and signer are supplied by a
// caller-specific trust root; there is intentionally no process-wide fallback.
func verifyReleaseSubmissionAuthority(
	request submitRequest,
	trustedSigner gatecontract.SignerIdentity,
	trustedPublicKey ed25519.PublicKey,
) (*verifiedReleaseAuthority, error) {
	attestation, err := gatecontract.DecodeReleaseAuthorityAttestation(request.AuthorityAttestation)
	if err != nil {
		return nil, err
	}
	if attestation.Signer != trustedSigner {
		return nil, errors.New("release authority attestation signer is not the configured owner")
	}
	if err := gatecontract.VerifyReleaseAuthorityAttestation(attestation, trustedPublicKey); err != nil {
		return nil, err
	}
	verified := &verifiedReleaseAuthority{attestation: request.AuthorityAttestation}
	request.VerifiedRelease = verified
	if err := validateReleaseSubmissionAuthority(gatecontract.CIEntrypoint{
		ID: gatecontract.CIEntrypointRelease, Owner: gatecontract.CIEntrypointOwnerRelease, Authoritative: true,
	}, request); err != nil {
		return nil, err
	}
	return verified, nil
}

// verifyProductionReleaseSubmissionAuthority 使用 production ActionGrant trust root 验证外部 release owner。
func verifyProductionReleaseSubmissionAuthority(
	request submitRequest,
	config productionCoordinatorConfig,
) (*verifiedReleaseAuthority, error) {
	publicKey, err := decodeResultReceiptPublicKey(config.ActionGrantAuthority.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decode production release authority public key: %w", err)
	}
	return verifyReleaseSubmissionAuthority(request, config.ActionGrantAuthority.Signer, publicKey)
}

// submitAuthoritativeRelease 是 release adapter 唯一的 production submit 入口；manual CLI 不调用它。
func submitAuthoritativeRelease(
	ctx context.Context,
	client coordinatorClient,
	config productionCoordinatorConfig,
	request submitRequest,
) (jobStatus, error) {
	verified, err := verifyProductionReleaseSubmissionAuthority(request, config)
	if err != nil {
		return jobStatus{}, err
	}
	request.VerifiedRelease = verified
	return client.Submit(ctx, request)
}

func validateNonReleaseSubmissionAuthority(entrypoint gatecontract.CIEntrypoint, attestation string) error {
	if !entrypoint.Authoritative && attestation != "" {
		return errors.New("non-authoritative submit must not carry an authority attestation")
	}
	return nil
}

func newInvocationAndJobIDs() (string, string, error) {
	invocationID, err := newCoordinatorID("inv")
	if err != nil {
		return "", "", err
	}
	jobID, err := newCoordinatorID("job")
	return invocationID, jobID, err
}

func submitCoordinatorIDs(invocationID string) (string, string, error) {
	if invocationID == "" {
		return newInvocationAndJobIDs()
	}
	if err := validateHookInvocationID(invocationID); err != nil {
		return "", "", err
	}
	jobID, err := newCoordinatorID("job")
	return invocationID, jobID, err
}

// validateHookInvocationID 拒绝 hook、workflow 与独立 release authority 命名空间之外的 invocation identity。
func validateHookInvocationID(invocationID string) error {
	prefix := ""
	for _, candidate := range []string{"hook-", "workflow-", "release-"} {
		if strings.HasPrefix(invocationID, candidate) {
			prefix = candidate
			break
		}
	}
	if prefix == "" || len(invocationID) != len(prefix)+64 {
		return errors.New("invocation id must be hook-, workflow-, or release- followed by a SHA-256 digest")
	}
	for _, character := range invocationID[len(prefix):] {
		if character < '0' || character > '9' && (character < 'a' || character > 'f') {
			return errors.New("invocation id digest must be lowercase hexadecimal")
		}
	}
	return nil
}

func newCoordinatorID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate coordinator %s ID: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}

func newReleaseInvocationID() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate release invocation ID: %w", err)
	}
	return "release-" + hex.EncodeToString(value[:]), nil
}

func canonicalAbsolutePath(name, value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", fmt.Errorf("%s must be canonical and absolute", name)
	}
	return value, nil
}

func interfaceIsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}

const coordinatorGateLogSchema = `
CREATE TABLE IF NOT EXISTS coordinator_gate_logs (
 job_id TEXT NOT NULL,
 gate_id TEXT NOT NULL,
 log_digest TEXT NOT NULL,
 log_data BLOB NOT NULL,
 PRIMARY KEY (job_id, gate_id)
);`

type coordinatorGateLog struct {
	JobID     string              `json:"job_id"`
	GateID    gatecontract.GateID `json:"gate_id"`
	LogDigest string              `json:"log_digest"`
	Log       string              `json:"log"`
}

type coordinatorLogClient interface {
	GateLog(context.Context, string, gatecontract.GateID) (coordinatorGateLog, error)
}

func ensureCoordinatorGateLogSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, coordinatorGateLogSchema); err != nil {
		return fmt.Errorf("initialize coordinator gate log schema: %w", err)
	}
	return nil
}

func (store *coordinatorStore) recordGateLog(ctx context.Context, jobID string, gateID gatecontract.GateID, logDigest string, logData []byte) error {
	if err := store.validateGateLogInput(ctx, jobID, gateID, logDigest, logData); err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx,
		"INSERT INTO coordinator_gate_logs(job_id, gate_id, log_digest, log_data) VALUES(?, ?, ?, ?) ON CONFLICT(job_id, gate_id) DO NOTHING",
		jobID, string(gateID), logDigest, append([]byte(nil), logData...),
	)
	if err != nil {
		return fmt.Errorf("persist gate log: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read persisted gate log rows: %w", err)
	}
	if rows == 1 {
		return nil
	}
	return store.validateIdempotentGateLog(ctx, jobID, gateID, logDigest, logData)
}

func (store *coordinatorStore) validateGateLogInput(ctx context.Context, jobID string, gateID gatecontract.GateID, logDigest string, logData []byte) error {
	if len(logData) > localci.MaxFreshContainerLogBytes {
		return fmt.Errorf("gate log exceeds %d-byte persistence limit", localci.MaxFreshContainerLogBytes)
	}
	if logDigest != coordinatorLogDigest(logData) {
		return fmt.Errorf("gate log digest mismatch: got %q want %q", logDigest, coordinatorLogDigest(logData))
	}
	record, err := store.job(ctx, jobID)
	if err != nil {
		return err
	}
	if !planContainsGate(record.Plan, gateID) {
		return fmt.Errorf("%w: gate %q does not belong to job %q", errCoordinatorState, gateID, jobID)
	}
	return nil
}

func (store *coordinatorStore) validateIdempotentGateLog(ctx context.Context, jobID string, gateID gatecontract.GateID, logDigest string, logData []byte) error {
	existing, err := store.gateLog(ctx, jobID, gateID)
	if err != nil {
		return err
	}
	if existing.LogDigest != logDigest || existing.Log != string(logData) {
		return fmt.Errorf("%w: gate log for job %q gate %q already contains different evidence", errCoordinatorState, jobID, gateID)
	}
	return nil
}

// gateLog 读取已持久化的 gate 日志，并复核其计划归属、大小上限和摘要完整性。
func (store *coordinatorStore) gateLog(ctx context.Context, jobID string, gateID gatecontract.GateID) (coordinatorGateLog, error) {
	record, err := store.job(ctx, jobID)
	if err != nil {
		return coordinatorGateLog{}, err
	}
	if !planContainsGate(record.Plan, gateID) {
		return coordinatorGateLog{}, fmt.Errorf("%w: gate %q does not belong to job %q", errCoordinatorState, gateID, jobID)
	}
	var digest string
	var data []byte
	err = store.db.QueryRowContext(ctx,
		"SELECT log_digest, log_data FROM coordinator_gate_logs WHERE job_id = ? AND gate_id = ?", jobID, string(gateID),
	).Scan(&digest, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return coordinatorGateLog{}, fmt.Errorf("%w: no persisted log for job %q gate %q", errCoordinatorNotFound, jobID, gateID)
	}
	if err != nil {
		return coordinatorGateLog{}, fmt.Errorf("read gate log: %w", err)
	}
	if len(data) > localci.MaxFreshContainerLogBytes {
		return coordinatorGateLog{}, fmt.Errorf("%w: persisted gate log exceeds size limit", errCoordinatorState)
	}
	if digest != coordinatorLogDigest(data) {
		return coordinatorGateLog{}, fmt.Errorf("%w: persisted gate log digest mismatch", errCoordinatorState)
	}
	return coordinatorGateLog{JobID: jobID, GateID: gateID, LogDigest: digest, Log: string(data)}, nil
}

// GateLog 通过 coordinator 的 durable store 返回容器删除后仍可验证的 gate 日志证据。
func (client *coordinatorTransportClient) GateLog(ctx context.Context, jobID string, gateID gatecontract.GateID) (coordinatorGateLog, error) {
	if client == nil || client.store == nil || ctx == nil {
		return coordinatorGateLog{}, fmt.Errorf("%w: connected client and context are required", errCoordinatorDependency)
	}
	return client.store.gateLog(ctx, jobID, gateID)
}

func runLogs(args []string, stdout io.Writer) error {
	return runLogsWithConnector(args, stdout, connectProductionCoordinator)
}

func runLogsWithConnector(args []string, stdout io.Writer, connector coordinatorConnector) error {
	jobID, gateID, err := parseLogsFlags(args)
	if err != nil {
		return err
	}
	return withCoordinator(context.Background(), connector, func(ctx context.Context, client coordinatorClient) error {
		logClient, ok := client.(coordinatorLogClient)
		if !ok {
			return infrastructureError("coordinator log query is not supported")
		}
		result, queryErr := logClient.GateLog(ctx, jobID, gateID)
		if queryErr != nil {
			return infrastructureError("read gate log: %v", queryErr)
		}
		return encodeCoordinatorJSON(stdout, result)
	})
}

func parseLogsFlags(args []string) (string, gatecontract.GateID, error) {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jobID := flags.String("job", "", "job")
	gateID := flags.String("gate", "", "gate")
	if err := flags.Parse(args); err != nil {
		return "", "", protocolError("parse logs flags: %v", err)
	}
	if flags.NArg() != 0 || strings.TrimSpace(*jobID) == "" || strings.TrimSpace(*gateID) == "" {
		return "", "", protocolError("logs requires --job and --gate")
	}
	return *jobID, gatecontract.GateID(*gateID), nil
}

func planContainsGate(plan gatecontract.GatePlan, gateID gatecontract.GateID) bool {
	for _, specification := range plan.Gates {
		if specification.ID == gateID {
			return true
		}
	}
	return false
}

func coordinatorLogDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum)
}
