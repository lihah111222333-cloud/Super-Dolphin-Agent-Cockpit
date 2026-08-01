package remoteci

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// remoteShardExecutionError 将失败绑定到可复算的分片身份、gate 集合与估时。
func remoteShardExecutionError(shard gate.ContainerShard, err error) error {
	return fmt.Errorf(
		"remote CI shard index=%d identity=%s estimated_duration_ms=%d gates=%q: %w",
		shard.Index, shard.IdentityDigest, shard.EstimatedDurationMS, joinGateIDs(shard.GateIDs), err,
	)
}

// failedRemoteGateError 将失败分片已经读取的有界 worker 日志附加到稳定错误链。
func failedRemoteGateError(shards []ShardResult) error {
	diagnostics := make([]string, 0, len(shards))
	for _, shard := range shards {
		if diagnostic := failedRemoteShardDiagnostic(shard); diagnostic != "" {
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	if len(diagnostics) == 0 {
		return ErrGateFailed
	}
	return fmt.Errorf("%w; %s", ErrGateFailed, strings.Join(diagnostics, "; "))
}

// failedRemoteShardDiagnostic 保留失败 gate 和测试终态，再附带有界日志尾部。
func failedRemoteShardDiagnostic(shard ShardResult) string {
	details := make([]string, 0, len(shard.Report.Gates)+1)
	for _, execution := range shard.Report.Gates {
		if execution.Status == gate.ResultStatusPassed {
			continue
		}
		failedTests := make([]string, 0, len(execution.TestTimings))
		for _, timing := range execution.TestTimings {
			if timing.Status == gate.GoTestStatusFail {
				failedTests = append(failedTests, fmt.Sprintf("%s(%dms)", timing.Name, timing.DurationMS))
			}
		}
		detail := fmt.Sprintf(
			"gate=%q status=%q exit_code=%d",
			execution.GateID,
			execution.Status,
			execution.ExitCode,
		)
		if len(failedTests) != 0 {
			detail += fmt.Sprintf(" failed_tests=%q", strings.Join(failedTests, ","))
		}
		if len(execution.Log) != 0 {
			detail += fmt.Sprintf(" log_tail=%q", remoteShardLogTail(string(execution.Log)))
		}
		details = append(details, detail)
	}
	if len(details) == 0 {
		return ""
	}
	if shard.workerDiagnostic != "" {
		details = append(details, fmt.Sprintf("worker_log_tail=%q", shard.workerDiagnostic))
	}
	return fmt.Sprintf("shard %s %s", shard.ShardIdentity, strings.Join(details, " "))
}

// remoteECIGroupDiagnostic 仅汇总终态容器状态与最近三条 ECI 事件。
func remoteECIGroupDiagnostic(group eci.ContainerGroup) string {
	parts := make([]string, 0, len(group.Containers)+3)
	for _, container := range group.Containers {
		if diagnostic := remoteECIContainerDiagnostic(container); diagnostic != "" {
			parts = append(parts, diagnostic)
		}
	}
	events := slices.Clone(group.Events)
	slices.SortStableFunc(events, compareRemoteECIEvents)
	if len(events) > 3 {
		events = events[:3]
	}
	for _, event := range events {
		if diagnostic := remoteECIEventDiagnostic(event); diagnostic != "" {
			parts = append(parts, diagnostic)
		}
	}
	return remoteShardLogTail(strings.Join(parts, "; "))
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

// remoteECIContainerDiagnostic 格式化单个容器的非空终态证据。
func remoteECIContainerDiagnostic(container eci.ContainerStatus) string {
	state := container.CurrentState
	details := []string{fmt.Sprintf("container=%q", container.Name)}
	if state.State != "" {
		details = append(details, fmt.Sprintf("state=%q", state.State))
	}
	if state.ExitCode != nil {
		details = append(details, fmt.Sprintf("exit_code=%d", *state.ExitCode))
	}
	if state.Reason != "" {
		details = append(details, fmt.Sprintf("reason=%q", state.Reason))
	}
	if state.Message != "" {
		details = append(details, fmt.Sprintf("message=%q", state.Message))
	}
	if len(details) == 1 {
		return ""
	}
	return strings.Join(details, " ")
}

// remoteECIEventDiagnostic 格式化单条非空 ECI 事件证据。
func remoteECIEventDiagnostic(event eci.ContainerGroupEvent) string {
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
	if len(details) == 1 {
		return ""
	}
	return strings.Join(details, " ")
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
