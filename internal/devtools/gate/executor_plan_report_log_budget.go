package gate

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"
)

const (
	// executorPlanCompileGroupLogPolicyVersion 将 selector 日志窗口策略绑定到报告摘要。
	executorPlanCompileGroupLogPolicyVersion    = "compile-group-log-budget/v1"
	executorPlanCompileGroupFullFailureLogBytes = executorPlanMaxLogBytes
	executorPlanCompileGroupOtherLogBytes       = executorPlanSuccessfulSelectorLogBytes
)

// compileGroupReportLogLimits 按 compile group 的 canonical workload 顺序分配日志窗口。
// 每个 group 的首个失败 selector 可保留完整窗口，其他 selector 只保留短尾部。
func compileGroupReportLogLimits(report PlanExecutionReport) (map[GateID]int, error) {
	gateResults, err := indexCompileGroupReportGates(report.Gates)
	if err != nil {
		return nil, err
	}
	limits := make(map[GateID]int)
	for groupIndex, execution := range report.CompileGroupExecutions {
		if err := appendCompileGroupReportLogLimits(limits, gateResults, execution, groupIndex); err != nil {
			return nil, err
		}
	}
	return limits, nil
}

func indexCompileGroupReportGates(gates []PlanGateExecution) (map[GateID]PlanGateExecution, error) {
	gateResults := make(map[GateID]PlanGateExecution, len(gates))
	for _, result := range gates {
		if result.GateID == "" {
			return nil, errors.New("compile group report gate identity is empty")
		}
		if _, duplicate := gateResults[result.GateID]; duplicate {
			return nil, fmt.Errorf("compile group report gate %q is duplicated", result.GateID)
		}
		gateResults[result.GateID] = result
	}
	return gateResults, nil
}

func appendCompileGroupReportLogLimits(limits map[GateID]int, gateResults map[GateID]PlanGateExecution, execution CompileGroupExecution, groupIndex int) error {
	seen := make(map[GateID]struct{}, len(execution.WorkloadIDs))
	fullFailureAssigned := false
	for _, workloadID := range execution.WorkloadIDs {
		if workloadID == "" {
			return fmt.Errorf("compile group %d contains an empty workload identity", groupIndex)
		}
		if _, duplicate := seen[workloadID]; duplicate {
			return fmt.Errorf("compile group %d workload %q is duplicated", groupIndex, workloadID)
		}
		seen[workloadID] = struct{}{}
		result, observed := gateResults[workloadID]
		if !observed {
			continue
		}
		limit := executorPlanCompileGroupOtherLogBytes
		if result.Status == ResultStatusFailed && !fullFailureAssigned {
			limit = executorPlanCompileGroupFullFailureLogBytes
			fullFailureAssigned = true
		}
		limits[workloadID] = limit
	}
	return nil
}

// normalizeCompileGroupReportLogs 将合法但过宽的 selector 日志收敛到 compile-group 策略。
// 超过协议硬上限或包含非法文本的日志不做静默修复，继续交由 canonical 校验拒绝。
func normalizeCompileGroupReportLogs(report PlanExecutionReport) (PlanExecutionReport, error) {
	limits, err := compileGroupReportLogLimits(report)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	if len(limits) == 0 {
		return report, nil
	}
	normalized := report
	normalized.Gates = slices.Clone(report.Gates)
	for index := range normalized.Gates {
		result := &normalized.Gates[index]
		limit, bounded := limits[result.GateID]
		if !bounded || len(result.Log) <= limit || len(result.Log) > executorPlanMaxLogBytes || !utf8.Valid(result.Log) || bytes.IndexByte(result.Log, 0) >= 0 {
			continue
		}
		boundedLog := newBoundedPlanLog(limit)
		_, _ = boundedLog.Write(result.Log)
		result.Log = PlainTextLog(slices.Clone(boundedLog.Bytes()))
		result.LogDigest = digestPlanLog(result.Log)
	}
	return normalized, nil
}

// validateCompileGroupReportLogBudget 拒绝绕过编码器直接注入过宽 selector 日志的报告。
func validateCompileGroupReportLogBudget(report PlanExecutionReport) error {
	limits, err := compileGroupReportLogLimits(report)
	if err != nil {
		return err
	}
	for _, result := range report.Gates {
		if limit, bounded := limits[result.GateID]; bounded && len(result.Log) > limit {
			return fmt.Errorf("compile group selector %q log exceeds %d bytes", result.GateID, limit)
		}
	}
	return nil
}

// appendExecutionProfileDigest 将后端执行画像的每个字段加入报告摘要，防止 wire profile 篡改漏检。
func appendExecutionProfileDigest(destination []byte, profile ExecutionProfile) []byte {
	destination = appendPlanReportField(destination, "execution-go-flags", profile.GoFlags)
	destination = appendPlanReportField(destination, "execution-cache-source", profile.CacheSource)
	destination = appendPlanReportField(destination, "execution-cache-status", string(profile.CacheStatus))
	destination = appendPlanReportField(destination, "execution-cache-measurement", profile.CacheMeasurement)
	destination = appendPlanReportField(destination, "execution-private-hit-count", strconv.FormatUint(profile.PrivateHitCount, 10))
	destination = appendPlanReportField(destination, "execution-baseline-hit-count", strconv.FormatUint(profile.BaselineHitCount, 10))
	destination = appendPlanReportField(destination, "execution-cache-miss-count", strconv.FormatUint(profile.CacheMissCount, 10))
	destination = appendPlanReportField(destination, "execution-cache-put-count", strconv.FormatUint(profile.CachePutCount, 10))
	destination = appendPlanReportField(destination, "execution-materialize-ms", strconv.FormatInt(profile.MaterializeMS, 10))
	destination = appendPlanReportField(destination, "execution-download-ms", strconv.FormatInt(profile.DownloadMS, 10))
	destination = appendPlanReportField(destination, "execution-verify-ms", strconv.FormatInt(profile.VerifyMS, 10))
	destination = appendPlanReportField(destination, "execution-startup-ms", strconv.FormatInt(profile.StartupMS, 10))
	destination = appendPlanReportField(destination, "execution-test-body-ms", strconv.FormatInt(profile.TestBodyMS, 10))
	destination = appendPlanReportField(destination, "execution-total-ms", strconv.FormatInt(profile.TotalMS, 10))
	frontend, _ := encodeFrontendExecutionProfile(profile.Frontend)
	return appendPlanReportField(destination, "frontend-execution-profile", frontend)
}

// appendCompileGroupLogBudgetDigest 将 compile-group selector 日志策略加入报告摘要。
func appendCompileGroupLogBudgetDigest(destination []byte, compileGroupCount int) []byte {
	if compileGroupCount == 0 {
		return destination
	}
	destination = appendPlanReportField(destination, "compile-group-log-policy", executorPlanCompileGroupLogPolicyVersion)
	destination = appendPlanReportField(destination, "compile-group-full-failure-log-bytes", strconv.Itoa(executorPlanCompileGroupFullFailureLogBytes))
	return appendPlanReportField(destination, "compile-group-other-selector-log-bytes", strconv.Itoa(executorPlanCompileGroupOtherLogBytes))
}
