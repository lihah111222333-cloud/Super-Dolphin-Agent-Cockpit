package remoteci

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const maxRemoteRootDiagnostics = 8

type remoteFailureDiagnosticSummary struct {
	diagnostics                       []string
	failedShards                      int
	cancelledWorkloads                int
	omittedRoots                      int
	cancelledInfrastructureDiagnostic string
}

// remoteShardExecutionError 将失败绑定到可复算的分片身份、gate 集合与估时。
func remoteShardExecutionError(shard gate.ContainerShard, err error) error {
	return fmt.Errorf(
		"remote CI shard index=%d identity=%s estimated_duration_ms=%d gates=%q: %w",
		shard.Index, shard.IdentityDigest, shard.EstimatedDurationMS, diagnosticGateIDs(shard.GateIDs), err,
	)
}

// diagnosticGateIDs 仅用于协调器错误文本，不参与 worker argv 或请求身份。
func diagnosticGateIDs(ids []gate.GateID) string {
	values := make([]string, len(ids))
	for index, id := range ids {
		values[index] = string(id)
	}
	return strings.Join(values, ",")
}

// failedRemoteGateError 只展开真实根失败，取消分片仅汇总，避免首错被并发取消噪声淹没。
func failedRemoteGateError(shards []ShardResult) error {
	summary := remoteFailureDiagnosticSummary{diagnostics: make([]string, 0, maxRemoteRootDiagnostics+2)}
	for _, shard := range shards {
		summary.observe(shard)
	}
	if summary.omittedRoots != 0 {
		summary.diagnostics = append(summary.diagnostics, fmt.Sprintf("omitted_root_diagnostics=%d", summary.omittedRoots))
	}
	if len(summary.diagnostics) == 0 && summary.cancelledInfrastructureDiagnostic != "" {
		summary.diagnostics = append(summary.diagnostics, summary.cancelledInfrastructureDiagnostic)
	}
	if summary.failedShards != 0 || summary.cancelledWorkloads != 0 {
		summary.diagnostics = append(summary.diagnostics, fmt.Sprintf("failed_shards=%d cancelled_workloads=%d", summary.failedShards, summary.cancelledWorkloads))
	}
	if len(summary.diagnostics) == 0 {
		return ErrGateFailed
	}
	return fmt.Errorf("%w; %s", ErrGateFailed, strings.Join(summary.diagnostics, "; "))
}

func (summary *remoteFailureDiagnosticSummary) observe(shard ShardResult) {
	if shard.ContainerStatus != "Succeeded" {
		summary.failedShards++
	}
	executionDetails, cancelled := remoteShardExecutionDiagnostics(shard.Report.Gates)
	summary.cancelledWorkloads += cancelled
	if !remoteShardHasRootFailure(shard, executionDetails) {
		summary.observeCancelledInfrastructure(shard)
		return
	}
	if len(summary.diagnostics) == maxRemoteRootDiagnostics {
		summary.omittedRoots++
		return
	}
	if diagnostic := failedRemoteShardDiagnostic(shard, executionDetails); diagnostic != "" {
		summary.diagnostics = append(summary.diagnostics, diagnostic)
	}
}

func (summary *remoteFailureDiagnosticSummary) observeCancelledInfrastructure(shard ShardResult) {
	if summary.cancelledInfrastructureDiagnostic != "" || shard.ContainerStatus == "Succeeded" || shard.workerDiagnostic == "" {
		return
	}
	summary.cancelledInfrastructureDiagnostic = fmt.Sprintf(
		"cancelled_shard=%s container_status=%q worker_log_tail=%q",
		shard.ShardIdentity, shard.ContainerStatus, remoteShardLogTail(shard.workerDiagnostic),
	)
}

func remoteShardHasRootFailure(shard ShardResult, executionDetails []string) bool {
	return len(executionDetails) != 0 ||
		(shard.ContainerStatus != "Succeeded" && shard.TerminalEvidence != nil) ||
		(len(shard.Report.Gates) == 0 && shard.ContainerStatus != "Succeeded")
}

// failedRemoteShardDiagnostic 保留真实失败 gate 和测试终态，再附带有界日志尾部。
func failedRemoteShardDiagnostic(shard ShardResult, executionDetails []string) string {
	details := make([]string, 0, len(shard.Report.Gates)+1)
	if shard.ContainerStatus != "Succeeded" {
		details = append(details, fmt.Sprintf("container_status=%q", shard.ContainerStatus))
	}
	details = append(details, executionDetails...)
	if terminalDiagnostic := remoteECITerminalEvidenceDiagnostic(shard.TerminalEvidence); terminalDiagnostic != "" {
		details = append(details, terminalDiagnostic)
	}
	if shard.workerDiagnostic != "" {
		details = append(details, fmt.Sprintf("worker_log_tail=%q", remoteShardLogTail(shard.workerDiagnostic)))
	}
	if len(details) == 0 {
		return ""
	}
	return fmt.Sprintf("shard %s %s", shard.ShardIdentity, strings.Join(details, " "))
}

// remoteShardExecutionDiagnostics 汇总非通过 workload 的 bounded gate 证据。
func remoteShardExecutionDiagnostics(executions []gate.PlanGateExecution) ([]string, int) {
	details := make([]string, 0)
	cancelled := 0
	for _, execution := range executions {
		profile := execution.ExecutionProfile
		if remoteGateDiagnosticIsSuccessful(execution) {
			continue
		}
		if execution.Status == gate.ResultStatusCancelled {
			cancelled++
			continue
		}
		failedTests := make([]string, 0, len(execution.TestTimings))
		for _, timing := range execution.TestTimings {
			if timing.Status == gate.GoTestStatusFail {
				failedTests = append(failedTests, fmt.Sprintf("%s(%dms)", timing.Name, timing.DurationMS))
			}
		}
		detail := fmt.Sprintf(
			"gate=%q status=%q exit_code=%d startup_ms=%d test_body_ms=%d total_ms=%d",
			execution.GateID,
			execution.Status,
			execution.ExitCode,
			profile.StartupMS,
			profile.TestBodyMS,
			profile.TotalMS,
		)
		if len(failedTests) != 0 {
			detail += fmt.Sprintf(" failed_tests=%q", strings.Join(failedTests, ","))
		}
		if len(execution.Log) != 0 {
			detail += fmt.Sprintf(" log_tail=%q", remoteShardLogTail(string(execution.Log)))
		}
		details = append(details, detail)
	}
	return details, cancelled
}

// failedObservedWorkloadError 在聚合计时前报告真正失败或取消的 workload，避免 pending 结果遮住首错。
func failedObservedWorkloadError(observed map[string]gate.PlanGateExecution) error {
	details, cancelled := failedObservedWorkloadDetails(observed)
	if cancelled != 0 {
		details = append(details, fmt.Sprintf("cancelled_workloads=%d", cancelled))
	}
	if len(details) == 0 {
		return nil
	}
	return fmt.Errorf("%w; %s", ErrGateFailed, strings.Join(details, "; "))
}

// failedObservedWorkloadDetails 选择有限数量的真实失败 workload，并统计取消项。
func failedObservedWorkloadDetails(observed map[string]gate.PlanGateExecution) ([]string, int) {
	details := make([]string, 0)
	cancelled := 0
	omitted := 0
	for _, id := range slices.Sorted(maps.Keys(observed)) {
		execution := observed[id]
		if execution.Status == gate.ResultStatusPassed {
			continue
		}
		if execution.Status == gate.ResultStatusCancelled {
			cancelled++
			continue
		}
		detail := fmt.Sprintf("gate=%q status=%q exit_code=%d", execution.GateID, execution.Status, execution.ExitCode)
		if len(execution.Log) != 0 {
			detail += fmt.Sprintf(" log_tail=%q", remoteShardLogTail(string(execution.Log)))
		}
		if len(details) < maxRemoteRootDiagnostics {
			details = append(details, detail)
		} else {
			omitted++
		}
	}
	if omitted != 0 {
		details = append(details, fmt.Sprintf("omitted_failed_workloads=%d", omitted))
	}
	return details, cancelled
}

func remoteGateDiagnosticIsSuccessful(execution gate.PlanGateExecution) bool {
	return execution.Status == gate.ResultStatusPassed && execution.ExecutionProfile.StartupMS > 0 && execution.ExecutionProfile.TestBodyMS > 0
}

// remoteECIGroupDiagnostic 仅汇总终态容器状态与最近三条 ECI 事件。
func remoteECIGroupDiagnostic(group eci.ContainerGroup) string {
	evidence, err := remoteECITerminalEvidence(group)
	if err != nil {
		return ""
	}
	return remoteECITerminalEvidenceDiagnostic(evidence)
}

// remoteECITerminalEvidence 只投影有界的 provider 终态字段，刻意排除镜像、环境变量和日志。
func remoteECITerminalEvidence(group eci.ContainerGroup) (*gate.RemoteCITerminalEvidence, error) {
	evidence := &gate.RemoteCITerminalEvidence{
		Containers:     make([]gate.RemoteCIContainerTerminalEvidence, 0, len(group.Containers)),
		InitContainers: make([]gate.RemoteCIContainerTerminalEvidence, 0, len(group.InitContainers)),
	}
	for _, container := range group.Containers {
		evidence.Containers = append(evidence.Containers, remoteECIContainerEvidence(container))
	}
	for _, container := range group.InitContainers {
		evidence.InitContainers = append(evidence.InitContainers, remoteECIContainerEvidence(container))
	}
	appendRemoteECIEventEvidence(evidence, group.Events)
	if err := evidence.Validate(); err != nil {
		return nil, err
	}
	return evidence, nil
}

// appendRemoteECIEventEvidence 按异常优先和最新时间选择最多三条事件。
func appendRemoteECIEventEvidence(evidence *gate.RemoteCITerminalEvidence, source []eci.ContainerGroupEvent) {
	events := slices.Clone(source)
	slices.SortStableFunc(events, compareRemoteECIEvents)
	for _, event := range events {
		if event.Type == "" && event.Reason == "" && event.Message == "" && event.Count == 0 && event.LastTimestamp == "" {
			continue
		}
		if len(evidence.Events) == 3 {
			break
		}
		evidence.Events = append(evidence.Events, gate.RemoteCIEventEvidence{
			Type: boundedRemoteECIField(event.Type), Reason: boundedRemoteECIField(event.Reason),
			Message: boundedRemoteECIField(event.Message), Count: event.Count,
			LastTimestamp: boundedRemoteECIField(event.LastTimestamp),
		})
	}
}

func remoteECIContainerEvidence(container eci.ContainerStatus) gate.RemoteCIContainerTerminalEvidence {
	state := container.CurrentState
	var exitCode *int64
	if state.ExitCode != nil {
		value := *state.ExitCode
		exitCode = &value
	}
	return gate.RemoteCIContainerTerminalEvidence{
		Name:     boundedRemoteECIField(container.Name),
		State:    boundedRemoteECIField(state.State),
		ExitCode: exitCode,
		Reason:   boundedRemoteECIField(state.Reason),
		Message:  boundedRemoteECIField(state.Message),
	}
}

func remoteECITerminalEvidenceDiagnostic(evidence *gate.RemoteCITerminalEvidence) string {
	if evidence == nil {
		return ""
	}
	parts := make([]string, 0, len(evidence.Containers)+len(evidence.InitContainers)+len(evidence.Events))
	for _, container := range evidence.Containers {
		parts = append(parts, remoteCIContainerEvidenceDiagnostic("container", container))
	}
	for _, container := range evidence.InitContainers {
		parts = append(parts, remoteCIContainerEvidenceDiagnostic("init_container", container))
	}
	for _, event := range evidence.Events {
		parts = append(parts, remoteECIEventEvidenceDiagnostic(event))
	}
	return remoteShardLogTail(strings.Join(parts, "; "))
}

func remoteCIContainerEvidenceDiagnostic(kind string, container gate.RemoteCIContainerTerminalEvidence) string {
	details := []string{fmt.Sprintf("%s=%q", kind, container.Name)}
	if container.State != "" {
		details = append(details, fmt.Sprintf("state=%q", container.State))
	}
	if container.ExitCode != nil {
		details = append(details, fmt.Sprintf("exit_code=%d", *container.ExitCode))
	}
	if container.Reason != "" {
		details = append(details, fmt.Sprintf("reason=%q", container.Reason))
	}
	if container.Message != "" {
		details = append(details, fmt.Sprintf("message=%q", container.Message))
	}
	return strings.Join(details, " ")
}

// remoteECIEventEvidenceDiagnostic 格式化一条有界 provider 事件。
func remoteECIEventEvidenceDiagnostic(event gate.RemoteCIEventEvidence) string {
	details := []string{"event"}
	if event.Type != "" {
		details = append(details, fmt.Sprintf("type=%q", event.Type))
	}
	if event.Reason != "" {
		details = append(details, fmt.Sprintf("reason=%q", event.Reason))
	}
	if event.Message != "" {
		details = append(details, fmt.Sprintf("message=%q", event.Message))
	}
	if event.Count > 0 {
		details = append(details, fmt.Sprintf("count=%d", event.Count))
	}
	if event.LastTimestamp != "" {
		details = append(details, fmt.Sprintf("last=%q", event.LastTimestamp))
	}
	return strings.Join(details, " ")
}

func boundedRemoteECIField(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 1024 {
		return value
	}
	return diagnosticPrefix(value, 1024)
}

// compareRemoteECIEvents 优先保留异常事件，再按最新时间排序。
func compareRemoteECIEvents(left, right eci.ContainerGroupEvent) int {
	leftPriority := remoteECIEventPriority(left)
	rightPriority := remoteECIEventPriority(right)
	if leftPriority != rightPriority {
		return rightPriority - leftPriority
	}
	return strings.Compare(right.LastTimestamp, left.LastTimestamp)
}

// remoteECIEventPriority 将 Warning 和其他异常类型排在普通事件之前。
func remoteECIEventPriority(event eci.ContainerGroupEvent) int {
	if strings.EqualFold(event.Type, "Warning") {
		return 2
	}
	if event.Type != "" && !strings.EqualFold(event.Type, "Normal") {
		return 1
	}
	return 0
}

// remoteShardLogTail 限制上报日志大小并保留最接近失败点的尾部。
func remoteShardLogTail(log string) string {
	log = strings.TrimSpace(log)
	if len(log) <= remoteShardDiagnosticMaxBytes {
		return log
	}
	return diagnosticSuffix(log, remoteShardDiagnosticMaxBytes)
}

// boundedRemoteRunErrorText 同时保留失败摘要开头和最终日志，并用摘要绑定完整错误。
func boundedRemoteRunErrorText(runErr error) string {
	if runErr == nil {
		return ""
	}
	fullText := runErr.Error()
	if len(fullText) <= remoteShardDiagnosticMaxBytes {
		return fullText
	}
	digest := sha256.Sum256([]byte(fullText))
	marker := fmt.Sprintf(
		"\n...[remote CI error truncated full_bytes=%d sha256:%x]...\n",
		len(fullText),
		digest,
	)
	contentBudget := remoteShardDiagnosticMaxBytes - len(marker)
	headBudget := contentBudget / 2
	return diagnosticPrefix(fullText, headBudget) +
		marker +
		diagnosticSuffix(fullText, contentBudget-headBudget)
}

func diagnosticPrefix(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func diagnosticSuffix(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	start := len(value) - maxBytes
	for start < len(value) && !utf8.ValidString(value[start:]) {
		start++
	}
	return value[start:]
}
