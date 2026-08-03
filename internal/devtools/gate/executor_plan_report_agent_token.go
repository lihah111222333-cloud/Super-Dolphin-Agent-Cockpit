package gate

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// encodePlanReportHeader 编码携带可选 agent token 摘要的报告头。
func encodePlanReportHeader(report PlanExecutionReport) (string, error) {
	if err := validatePlanExecutionReportSchema(uint64(report.SchemaVersion)); err != nil {
		return "", err
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
	report := PlanExecutionReport{
		SchemaVersion:    uint32(schema),
		Profile:          Profile(fields[1]),
		PlanDigest:       fields[2],
		AgentTokenDigest: agentTokenDigest,
	}
	return report, gateCount, nil
}

// parsePlanReportHeaderFields 拒绝多余空白和非 current header 字段数量。
func parsePlanReportHeaderFields(payload string) ([]string, error) {
	fields := strings.Fields(payload)
	if len(fields) != 4 && len(fields) != 5 {
		return nil, errors.New("plan report header payload is invalid")
	}
	if strings.Join(fields, " ") != payload {
		return nil, errors.New("plan report header payload is invalid")
	}
	return fields, nil
}

// parsePlanReportGateCount 校验固定宽度且受协议上限约束的 gate 数量。
func parsePlanReportGateCount(value string) (int, error) {
	gateCount, err := parseSixDigitCount(value)
	if err != nil {
		return 0, errors.New("plan report gate count is invalid")
	}
	if gateCount == 0 || gateCount > 64 {
		return 0, errors.New("plan report gate count is invalid")
	}
	return gateCount, nil
}

// parsePlanReportAgentTokenDigest 校验可选 agent token 摘要，不接受畸形身份。
func parsePlanReportAgentTokenDigest(fields []string) (string, error) {
	if len(fields) == 4 {
		return "", nil
	}
	agentTokenDigest := fields[4]
	if err := cicontract.ValidateAgentTokenDigest(agentTokenDigest); err != nil {
		return "", errors.New("plan report agent token digest is invalid")
	}
	return agentTokenDigest, nil
}

// validatePlanExecutionReportSchema 只接受当前 worker/coordinator 共同编译的报告 schema。
func validatePlanExecutionReportSchema(schema uint64) error {
	if schema != uint64(ExecutorPlanReportSchemaVersion) {
		return errors.New("plan report schema is unsupported")
	}
	return nil
}
