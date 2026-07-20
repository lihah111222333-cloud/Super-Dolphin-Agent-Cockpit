package localci

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

var errPlanReportMissing = errors.New("container did not emit a plan report")

// canonicalReportExecution 标识必须为请求中的每个 gate 产出可核验结果的执行模式。
func canonicalReportExecution(request FreshContainerRequest) bool {
	return request.PlanExecution || len(request.ShardGateIDs) > 0
}

func freshContainerCommand(request FreshContainerRequest) ([]string, error) {
	if len(request.ShardGateIDs) != 0 {
		if request.PlanExecution {
			return nil, errors.New("shard execution cannot use the full-plan executor")
		}
		return gate.ContainerShardExecutorArgv(request.Plan, request.ShardGateIDs)
	}
	if request.PlanExecution {
		return gate.PlanExecutorArgv(request.Plan)
	}
	return commandFromPlan(request.Plan, request.GateID)
}

func commandFromPlan(plan gate.GatePlan, gateID gate.GateID) ([]string, error) {
	for _, spec := range plan.Gates {
		if spec.ID == gateID {
			return append([]string(nil), spec.Argv...), nil
		}
	}
	return nil, fmt.Errorf("gate %q is not present in the canonical plan", gateID)
}

// collectPlanGateResults 验证唯一 canonical report 并映射每个 gate 的持久证据。
func collectPlanGateResults(result *FreshContainerResult, request FreshContainerRequest) error {
	report, err := decodeUniquePlanReport(result.LogOutput)
	if err != nil {
		return err
	}
	if report.Profile != request.Profile || report.PlanDigest != request.Plan.PlanDigest {
		return errors.New("plan report identity does not match requested plan")
	}
	if (result.ExitCode == 0) != allPlanGatesPassed(report.Gates) {
		return errors.New("plan report gate status does not match container exit code")
	}
	expected, err := requestedPlanGateSpecs(request)
	if err != nil {
		return err
	}
	if len(report.Gates) != len(expected) {
		return errors.New("plan report gate coverage does not match requested execution")
	}
	collected := make([]FreshPlanGateResult, 0, len(report.Gates))
	for index, observed := range report.Gates {
		spec := expected[index]
		gateResult, err := buildGateResult(spec.ID, spec.Argv, FreshContainerResult{
			Status: observed.Status, ExitCode: observed.ExitCode,
			StartedAt: observed.StartedAt, CompletedAt: observed.CompletedAt,
			LogDigest: observed.LogDigest,
		})
		if err != nil {
			return err
		}
		collected = append(collected, FreshPlanGateResult{
			GateResult: *gateResult, Status: observed.Status,
			LogOutput: append([]byte(nil), observed.Log...),
		})
	}
	result.PlanGateResults = collected
	return nil
}

// collectFinishedPlanGateResults 仅允许已杀死的取消或超时执行缺少完整 report。
func collectFinishedPlanGateResults(result *FreshContainerResult, request FreshContainerRequest) error {
	if !canonicalReportExecution(request) {
		return nil
	}
	err := collectPlanGateResults(result, request)
	if err == nil || (errors.Is(err, errPlanReportMissing) && terminalWithoutReportStatus(result.Status) && result.Killed) {
		return nil
	}
	return err
}

// collectTerminalPlanGateResults 仅在 Docker 完整证明无报告终态后合成逐 gate 失败证据。
func collectTerminalPlanGateResults(result *FreshContainerResult, request FreshContainerRequest) error {
	if err := validateTerminalPlanCoverage(*result); err != nil {
		return err
	}
	specs, err := requestedPlanGateSpecs(request)
	if err != nil {
		return err
	}
	log := terminalPlanCoverageLog(*result)
	for _, spec := range specs {
		gateResult, err := buildGateResult(spec.ID, spec.Argv, FreshContainerResult{
			Status: result.Status, ExitCode: -1,
			StartedAt: result.StartedAt, CompletedAt: result.CompletedAt, LogDigest: digestBytes(log),
		})
		if err != nil {
			return err
		}
		result.PlanGateResults = append(result.PlanGateResults, FreshPlanGateResult{
			GateResult: *gateResult, Status: result.Status, LogOutput: append([]byte(nil), log...),
		})
	}
	return nil
}

// validateTerminalPlanCoverage 校验取消或超时计划的终态证据闭包。
func validateTerminalPlanCoverage(result FreshContainerResult) error {
	if !terminalWithoutReportStatus(result.Status) || !result.Killed || !result.Container.Removed ||
		result.StartedAt.IsZero() || result.CompletedAt.Before(result.StartedAt) {
		return errors.New("terminal plan coverage requires verified container termination")
	}
	if err := result.Container.Validate(); err != nil {
		return fmt.Errorf("terminal plan container evidence: %w", err)
	}
	if err := validateTerminalPlanDigests(result); err != nil {
		return err
	}
	if result.Status == gate.ResultStatusTimeout {
		return validateTimeoutPlanTerminal(result)
	}
	return nil
}

func validateTerminalPlanDigests(result FreshContainerResult) error {
	if result.LogDigest != digestBytes(result.LogOutput) ||
		result.KillProofDigest != digestBytes([]byte("killed\n"+result.Container.ContainerID+"\n")) ||
		result.RemovalProofDigest != digestBytes([]byte("removed\n"+result.Container.ContainerID+"\n")) {
		return errors.New("terminal plan evidence digest is invalid")
	}
	return nil
}

// validateTimeoutPlanTerminal 要求超时退出不早于执行期限，且终态 inspect 与移除证据相邻闭合。
func validateTimeoutPlanTerminal(result FreshContainerResult) error {
	if err := validateTimeoutPlanTimeline(result); err != nil {
		return err
	}
	return validateTimeoutPlanEvidence(result)
}

// validateTimeoutPlanTimeline 校验超时退出与证据收尾的时钟顺序。
func validateTimeoutPlanTimeline(result FreshContainerResult) error {
	if result.Deadline.IsZero() || !result.Deadline.After(result.StartedAt) || result.ExitedAt.Before(result.Deadline) || result.CompletedAt.Before(result.ExitedAt) {
		return errors.New("timeout plan terminal timeline is invalid")
	}
	return nil
}

// validateTimeoutPlanEvidence 校验终态 inspect 与移除证明相邻且摘要互异。
func validateTimeoutPlanEvidence(result FreshContainerResult) error {
	if len(result.Evidence) < 2 {
		return errors.New("timeout plan terminal inspect evidence is missing")
	}
	terminal, removal := result.Evidence[len(result.Evidence)-2], result.Evidence[len(result.Evidence)-1]
	if terminal.Kind != gate.EvidenceKindDocker || terminal.Digest == result.RemovalProofDigest || removal.Kind != gate.EvidenceKindDocker || removal.Digest != result.RemovalProofDigest {
		return errors.New("timeout plan terminal inspect evidence is invalid")
	}
	if err := terminal.Validate(); err != nil {
		return fmt.Errorf("timeout plan terminal inspect evidence: %w", err)
	}
	return removal.Validate()
}

func terminalWithoutReportStatus(status gate.ResultStatus) bool {
	return status == gate.ResultStatusCancelled || status == gate.ResultStatusTimeout
}

func terminalPlanCoverageLog(result FreshContainerResult) []byte {
	return fmt.Appendf(nil, "%s before canonical report; raw_container_log_digest=%s deadline=%s exited_at=%s kill_proof_digest=%s removal_proof_digest=%s\n",
		result.Status, result.LogDigest, result.Deadline.UTC().Format(time.RFC3339Nano), result.ExitedAt.UTC().Format(time.RFC3339Nano), result.KillProofDigest, result.RemovalProofDigest)
}

func finishCanonicalReport(result FreshContainerResult, request FreshContainerRequest, runErr error) (FreshContainerResult, error) {
	if !terminalWithoutReportStatus(result.Status) || len(result.PlanGateResults) != 0 {
		return result, runErr
	}
	if err := collectTerminalPlanGateResults(&result, request); err != nil {
		result.Status = gate.ResultStatusInfraFailed
		return result, errors.Join(runErr, err)
	}
	return result, runErr
}

// requestedPlanGateSpecs 将完整 canonical plan 精确投影为 worker 被授权的 gate 序列。
func requestedPlanGateSpecs(request FreshContainerRequest) ([]gate.GateSpec, error) {
	if len(request.ShardGateIDs) == 0 {
		return append([]gate.GateSpec(nil), request.Plan.Gates...), nil
	}
	specs := make([]gate.GateSpec, 0, len(request.ShardGateIDs))
	for _, id := range request.ShardGateIDs {
		found := false
		for _, spec := range request.Plan.Gates {
			if spec.ID == id {
				specs = append(specs, spec)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("shard gate %q is not present in the canonical plan", id)
		}
	}
	return specs, nil
}

func decodeUniquePlanReport(logOutput []byte) (gate.PlanExecutionReport, error) {
	var chunks []string
	for line := range bytes.SplitSeq(logOutput, []byte{'\n'}) {
		index := bytes.Index(line, []byte(gate.ExecutorPlanReportChunkPrefix))
		if index < 0 {
			continue
		}
		chunks = append(chunks, strings.TrimSpace(string(line[index:])))
	}
	if len(chunks) == 0 {
		return gate.PlanExecutionReport{}, errPlanReportMissing
	}
	return gate.DecodePlanExecutionReportChunks(chunks)
}

func allPlanGatesPassed(results []gate.PlanGateExecution) bool {
	for _, result := range results {
		if result.Status != gate.ResultStatusPassed {
			return false
		}
	}
	return true
}
