package gate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// WorkerExecutionStatus 是 worker 进程级执行的稳定终态分类。
type WorkerExecutionStatus string

const (
	WorkerExecutionStatusSuccess WorkerExecutionStatus = "success"
	WorkerExecutionStatusFailed  WorkerExecutionStatus = "failed"
)

// WorkerExecutionReasonCode 是不携带原始错误文本的有界失败原因分类。
type WorkerExecutionReasonCode string

const (
	WorkerExecutionReasonNone                    WorkerExecutionReasonCode = "none"
	WorkerExecutionReasonExecutionError          WorkerExecutionReasonCode = "execution_error"
	WorkerExecutionReasonContextCanceled         WorkerExecutionReasonCode = "context_canceled"
	WorkerExecutionReasonContextDeadlineExceeded WorkerExecutionReasonCode = "context_deadline_exceeded"
)

// WorkerExecutionOutcome 记录 worker 进程级结果，不写入原始路径、参数或错误文本。
type WorkerExecutionOutcome struct {
	Status     WorkerExecutionStatus     `json:"status"`
	ExitCode   int                       `json:"exit_code"`
	ReasonCode WorkerExecutionReasonCode `json:"reason_code"`
}

// SuccessfulWorkerExecutionOutcome 返回无错误的 worker 执行结果。
func SuccessfulWorkerExecutionOutcome() WorkerExecutionOutcome {
	return WorkerExecutionOutcome{
		Status:     WorkerExecutionStatusSuccess,
		ExitCode:   0,
		ReasonCode: WorkerExecutionReasonNone,
	}
}

// WorkerExecutionOutcomeForError 将 worker 错误压缩为稳定、脱敏的结构化结果。
func WorkerExecutionOutcomeForError(err error) WorkerExecutionOutcome {
	if err == nil {
		return SuccessfulWorkerExecutionOutcome()
	}
	reasonCode := WorkerExecutionReasonExecutionError
	if errors.Is(err, context.DeadlineExceeded) {
		reasonCode = WorkerExecutionReasonContextDeadlineExceeded
	} else if errors.Is(err, context.Canceled) {
		reasonCode = WorkerExecutionReasonContextCanceled
	}
	exitCode := ExecutorExitCode(err)
	if exitCode == 0 {
		exitCode = 1
	}
	return WorkerExecutionOutcome{
		Status:     WorkerExecutionStatusFailed,
		ExitCode:   exitCode,
		ReasonCode: reasonCode,
	}
}

// Validate 校验 worker 执行结果的封闭状态机和值域。
func (outcome WorkerExecutionOutcome) Validate() error {
	switch outcome.Status {
	case WorkerExecutionStatusSuccess:
		if outcome.ExitCode != 0 || outcome.ReasonCode != WorkerExecutionReasonNone {
			return errors.New("worker execution success outcome is invalid")
		}
	case WorkerExecutionStatusFailed:
		if outcome.ExitCode <= 0 {
			return errors.New("worker execution failed outcome requires nonzero exit code")
		}
		switch outcome.ReasonCode {
		case WorkerExecutionReasonExecutionError, WorkerExecutionReasonContextCanceled, WorkerExecutionReasonContextDeadlineExceeded:
		default:
			return errors.New("worker execution failed outcome reason is invalid")
		}
	default:
		return errors.New("worker execution outcome status is invalid")
	}
	return nil
}

// encodePlanReportHeader 编码携带可选 agent token 摘要的报告头。
func encodePlanReportHeader(report PlanExecutionReport) (string, error) {
	if err := validatePlanExecutionReportSchema(uint64(report.SchemaVersion)); err != nil {
		return "", err
	}
	if err := report.ExecutionOutcome.Validate(); err != nil {
		return "", fmt.Errorf("plan report execution outcome: %w", err)
	}
	if report.AgentTokenDigest != "" {
		if err := cicontract.ValidateAgentTokenDigest(report.AgentTokenDigest); err != nil {
			return "", fmt.Errorf("plan report agent token digest: %w", err)
		}
	}
	header := fmt.Sprintf(
		"%s %06d %s %s %06d",
		planReportHeaderRecord,
		report.SchemaVersion,
		report.Profile,
		report.PlanDigest,
		len(report.Gates),
	)
	if report.AgentTokenDigest != "" {
		header += " " + report.AgentTokenDigest
	}
	header += " " + string(report.ExecutionOutcome.Status) + " " + strconv.Itoa(report.ExecutionOutcome.ExitCode) + " " + string(report.ExecutionOutcome.ReasonCode)
	return header, nil
}

// DecodePlanExecutionReportChunksForGateSetAndAgentTokenDigest 将 worker 报告绑定到 coordinator 准入的 agent 摘要。
func DecodePlanExecutionReportChunksForGateSetAndAgentTokenDigest(chunks []string, expected []GateID, expectedAgentTokenDigest string) (PlanExecutionReport, error) {
	if err := cicontract.ValidateAgentTokenDigest(expectedAgentTokenDigest); err != nil {
		return PlanExecutionReport{}, fmt.Errorf("expected agent token digest: %w", err)
	}
	report, err := decodePlanExecutionReportChunks(chunks, slices.Clone(expected))
	if err != nil {
		return PlanExecutionReport{}, err
	}
	if report.AgentTokenDigest != expectedAgentTokenDigest {
		return PlanExecutionReport{}, errors.New("plan report agent token digest does not match coordinator assignment")
	}
	return report, nil
}

// decodePlanReportHeader 解码包含可选 agent token 摘要的报告元数据和 gate 总数。
func decodePlanReportHeader(payload string) (PlanExecutionReport, int, error) {
	fields, err := parsePlanReportHeaderFields(payload)
	if err != nil {
		return PlanExecutionReport{}, 0, err
	}
	schema, err := strconv.ParseUint(fields[0], 10, 32)
	if err != nil {
		return PlanExecutionReport{}, 0, errors.New("plan report schema is invalid")
	}
	if err := validatePlanExecutionReportSchema(schema); err != nil {
		return PlanExecutionReport{}, 0, err
	}
	gateCount, err := parsePlanReportGateCount(fields[3])
	if err != nil {
		return PlanExecutionReport{}, 0, err
	}
	agentTokenDigest, err := parsePlanReportAgentTokenDigest(fields)
	if err != nil {
		return PlanExecutionReport{}, 0, err
	}
	executionOutcome, err := parsePlanReportExecutionOutcome(fields)
	if err != nil {
		return PlanExecutionReport{}, 0, err
	}
	report := PlanExecutionReport{
		SchemaVersion:    uint32(schema),
		Profile:          Profile(fields[1]),
		PlanDigest:       fields[2],
		AgentTokenDigest: agentTokenDigest,
		ExecutionOutcome: executionOutcome,
	}
	return report, gateCount, nil
}

// parsePlanReportHeaderFields 拒绝多余空白和非 current header 字段数量。
func parsePlanReportHeaderFields(payload string) ([]string, error) {
	fields := strings.Fields(payload)
	if len(fields) != 7 && len(fields) != 8 {
		return nil, errors.New("plan report header payload is invalid")
	}
	if strings.Join(fields, " ") != payload {
		return nil, errors.New("plan report header payload is invalid")
	}
	return fields, nil
}

// parsePlanReportGateCount 校验固定宽度且受传输记录上限约束的 gate 数量。
func parsePlanReportGateCount(value string) (int, error) {
	gateCount, err := parseSixDigitCount(value)
	if err != nil {
		return 0, errors.New("plan report gate count is invalid")
	}
	if gateCount == 0 || gateCount > executorPlanMaxTransportRecords {
		return 0, errors.New("plan report gate count is invalid")
	}
	return gateCount, nil
}

// parsePlanReportAgentTokenDigest 校验可选 agent token 摘要，不接受畸形身份。
func parsePlanReportAgentTokenDigest(fields []string) (string, error) {
	if len(fields) == 7 {
		return "", nil
	}
	agentTokenDigest := fields[4]
	if err := cicontract.ValidateAgentTokenDigest(agentTokenDigest); err != nil {
		return "", errors.New("plan report agent token digest is invalid")
	}
	return agentTokenDigest, nil
}

// parsePlanReportExecutionOutcome 严格解析 worker 进程级终态，不保留原始错误文本。
func parsePlanReportExecutionOutcome(fields []string) (WorkerExecutionOutcome, error) {
	outcomeStart := 4
	if len(fields) == 8 {
		outcomeStart = 5
	}
	exitCode, err := strconv.Atoi(fields[outcomeStart+1])
	if err != nil || strconv.Itoa(exitCode) != fields[outcomeStart+1] {
		return WorkerExecutionOutcome{}, errors.New("plan report execution outcome exit code is invalid")
	}
	outcome := WorkerExecutionOutcome{
		Status:     WorkerExecutionStatus(fields[outcomeStart]),
		ExitCode:   exitCode,
		ReasonCode: WorkerExecutionReasonCode(fields[outcomeStart+2]),
	}
	if err := outcome.Validate(); err != nil {
		return WorkerExecutionOutcome{}, errors.New("plan report execution outcome is invalid")
	}
	return outcome, nil
}

// validatePlanExecutionReportSchema 只接受当前 worker/coordinator 共同编译的报告 schema。
func validatePlanExecutionReportSchema(schema uint64) error {
	if schema != uint64(ExecutorPlanReportSchemaVersion) {
		return errors.New("plan report schema is unsupported")
	}
	return nil
}
