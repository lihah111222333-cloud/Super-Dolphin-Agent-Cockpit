package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// DAG prompt 身份诊断扫描上限，避免聊天层误触全库读取。
const (
	defaultDAGPromptIdentityDiagnosisLimit = 50
	maxDAGPromptIdentityDiagnosisLimit     = 200
)

// DiagnoseDAGPromptIdentityGapsInput 是存量 DAG prompt 身份诊断的只读入参。
// dag_key/pos 为空时按 limit 扫描 DAG 列表；不执行任何修复或写入。
type DiagnoseDAGPromptIdentityGapsInput struct {
	DagKey string `json:"dag_key,omitempty"`
	Pos    string `json:"pos,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// DAGPromptIdentityGap 记录存量 DAG 节点缺失的执行者身份字段。
// MissingFields 使用完整字段路径，方便聊天层直接转译给用户或运维。
type DAGPromptIdentityGap struct {
	DagKey          string   `json:"dag_key"`
	NodeKey         string   `json:"node_key"`
	NodeType        string   `json:"node_type"`
	AssignedTo      string   `json:"assigned_to,omitempty"`
	MissingFields   []string `json:"missing_fields"`
	MissingSummary  string   `json:"missing_summary"`
	RecentRunKey    string   `json:"recent_run_key,omitempty"`
	RecentRunStatus string   `json:"recent_run_status,omitempty"`
}

// DAGPromptIdentityDiagnosticsOutput 是存量坏 DAG 诊断结果。
// ReadOnly 永远为 true，Remediation 明确要求显式重绑或重建，避免误读为自动修复。
type DAGPromptIdentityDiagnosticsOutput struct {
	Gaps                     []DAGPromptIdentityGap `json:"gaps"`
	Data                     []DAGPromptIdentityGap `json:"data"`
	Total                    int                    `json:"total"`
	Showing                  int                    `json:"showing"`
	Truncated                bool                   `json:"truncated"`
	Hint                     string                 `json:"hint,omitempty"`
	ScannedDAGs              int                    `json:"scanned_dags"`
	DAGScanLimit             int                    `json:"dag_scan_limit"`
	DAGScanPossiblyTruncated bool                   `json:"dag_scan_possibly_truncated"`
	ReadOnly                 bool                   `json:"read_only"`
	Remediation              string                 `json:"remediation"`
}

// dagPromptIdentityDiagnosisScan 保存一次诊断扫描的明细和边界。
// 输出层依赖这些字段告诉用户是否可能还有未扫描的存量 DAG。
type dagPromptIdentityDiagnosisScan struct {
	Details                  []contract.DAGDetail
	ScannedDAGs              int
	DAGScanLimit             int
	DAGScanPossiblyTruncated bool
}

// diagnosticConfig 是诊断用的轻量节点配置，只保留 exec 子树。
type diagnosticConfig struct {
	Exec *diagnosticExec
}

// diagnosticExec 表示节点 config.exec 中与 prompt 身份相关的字段。
// 字段保持和已落库 JSON 名一致，方便直接指出缺失路径。
type diagnosticExec struct {
	PromptKey          string               `json:"prompt_key,omitempty"`
	AgentKey           string               `json:"agent_key,omitempty"`
	Provider           string               `json:"provider,omitempty"`
	CodexHome          string               `json:"codex_home,omitempty"`
	CodexInstanceKey   string               `json:"codex_instance_key,omitempty"`
	CodexModelProvider string               `json:"codex_model_provider,omitempty"`
	Verifier           *diagnosticAgentExec `json:"verifier,omitempty"`
}

// diagnosticAgentExec 表示 hybrid verifier 这类嵌套 agent 执行身份。
type diagnosticAgentExec struct {
	PromptKey          string `json:"prompt_key,omitempty"`
	AgentKey           string `json:"agent_key,omitempty"`
	Provider           string `json:"provider,omitempty"`
	CodexHome          string `json:"codex_home,omitempty"`
	CodexInstanceKey   string `json:"codex_instance_key,omitempty"`
	CodexModelProvider string `json:"codex_model_provider,omitempty"`
}

// recentRunSnapshot 缓存每个 DAG 最近一次 run 的轻量状态。
type recentRunSnapshot struct {
	RunKey string
	Status string
}

type dagPromptIdentityDiagnosticsPort interface {
	GetDAG(ctx context.Context, dagKey string) (contract.DAGDetail, error)
	ListDAGs(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error)
	ListRuns(ctx context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error)
}

// HandleDiagnoseDAGPromptIdentityGaps 返回只读存量 DAG prompt 身份诊断 handler。
// 它只调用 DAG/runs 读取接口；发现问题后要求显式 task_dag_apply_ops 重绑或重建。
func HandleDiagnoseDAGPromptIdentityGaps(svc dagPromptIdentityDiagnosticsPort) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in DiagnoseDAGPromptIdentityGapsInput) (DAGPromptIdentityDiagnosticsOutput, error) {
		return diagnoseDAGPromptIdentityGaps(ctx, svc, in)
	})
}

// diagnoseDAGPromptIdentityGaps 扫描 DAG 明细并列出缺 prompt/agent 身份的节点。
// 诊断是只读的；任何读取或解析失败都会直接返回错误，避免产出半可信结果。
func diagnoseDAGPromptIdentityGaps(
	ctx context.Context,
	svc dagPromptIdentityDiagnosticsPort,
	in DiagnoseDAGPromptIdentityGapsInput,
) (DAGPromptIdentityDiagnosticsOutput, error) {
	scan, err := loadDAGDetailsForPromptIdentityDiagnosis(ctx, svc, in)
	if err != nil {
		return DAGPromptIdentityDiagnosticsOutput{}, err
	}
	gaps, err := collectDAGPromptIdentityGaps(scan.Details)
	if err != nil {
		return DAGPromptIdentityDiagnosticsOutput{}, err
	}
	if err := enrichPromptIdentityGapsWithRecentRuns(ctx, svc, gaps); err != nil {
		return DAGPromptIdentityDiagnosticsOutput{}, err
	}
	return newDAGPromptIdentityDiagnosticsOutput(gaps, scan), nil
}

// loadDAGDetailsForPromptIdentityDiagnosis 读取诊断需要的 DAG 明细并记录扫描边界。
// dag_key 模式只读取单个 DAG；列表模式才使用 limit，并在返回数量达到 limit 时标记扫描可能被截断。
func loadDAGDetailsForPromptIdentityDiagnosis(
	ctx context.Context,
	svc dagPromptIdentityDiagnosticsPort,
	in DiagnoseDAGPromptIdentityGapsInput,
) (dagPromptIdentityDiagnosisScan, error) {
	dagKey, err := resolveOptionalDAGKeyInput(in.DagKey, in.Pos)
	if err != nil {
		return dagPromptIdentityDiagnosisScan{}, err
	}
	if dagKey != "" {
		detail, loadErr := svc.GetDAG(ctx, dagKey)
		if loadErr != nil {
			return dagPromptIdentityDiagnosisScan{}, loadErr
		}
		return dagPromptIdentityDiagnosisScan{
			Details:     []contract.DAGDetail{detail},
			ScannedDAGs: 1,
		}, nil
	}
	limit := normalizeListLimit(
		in.Limit,
		defaultDAGPromptIdentityDiagnosisLimit,
		maxDAGPromptIdentityDiagnosisLimit,
	)
	dags, err := svc.ListDAGs(ctx, contract.ListDAGsFilter{Limit: limit})
	if err != nil {
		return dagPromptIdentityDiagnosisScan{}, err
	}
	details, err := loadDAGDetailsBySummary(ctx, svc, dags)
	if err != nil {
		return dagPromptIdentityDiagnosisScan{}, err
	}
	return dagPromptIdentityDiagnosisScan{
		Details:                  details,
		ScannedDAGs:              len(dags),
		DAGScanLimit:             limit,
		DAGScanPossiblyTruncated: limit > 0 && len(dags) >= limit,
	}, nil
}

// loadDAGDetailsBySummary 按 ListDAGs 返回的摘要逐个读取完整 DAG。
// 这里遇到空 dag_key 或读取失败会直接报错，避免诊断结果悄悄遗漏坏记录。
func loadDAGDetailsBySummary(
	ctx context.Context,
	svc dagPromptIdentityDiagnosticsPort,
	dags []contract.DAGSummary,
) ([]contract.DAGDetail, error) {
	details := make([]contract.DAGDetail, 0, len(dags))
	for _, dag := range dags {
		dagKey := strings.TrimSpace(dag.DagKey)
		if dagKey == "" {
			return nil, fmt.Errorf("diagnose dag prompt identity gaps: listed DAG has empty dag_key")
		}
		detail, err := svc.GetDAG(ctx, dagKey)
		if err != nil {
			return nil, err
		}
		details = append(details, detail)
	}
	return details, nil
}

// collectDAGPromptIdentityGaps 聚合每个 DAG 节点的身份缺口。
// 一旦节点配置无法解析就直接返回错误，避免诊断结果遗漏坏数据。
func collectDAGPromptIdentityGaps(details []contract.DAGDetail) ([]DAGPromptIdentityGap, error) {
	var gaps []DAGPromptIdentityGap
	for _, detail := range details {
		for _, node := range detail.Nodes {
			nodeGaps, err := diagnoseNodePromptIdentityGaps(node)
			if err != nil {
				return nil, err
			}
			gaps = append(gaps, nodeGaps...)
		}
	}
	return gaps, nil
}

// diagnoseNodePromptIdentityGaps 识别单个节点缺失的 prompt 身份字段。
// 非 agent/hybrid 节点不参与诊断，避免把 automation 配置误判为坏 DAG。
func diagnoseNodePromptIdentityGaps(node contract.DAGNode) ([]DAGPromptIdentityGap, error) {
	cfg, hasExec, err := decodeDiagnosticConfig(node)
	if err != nil {
		return nil, err
	}
	if !hasExec {
		return nil, nil
	}
	switch strings.TrimSpace(node.NodeType) {
	case "", "agent":
		return diagnoseAgentPromptIdentityGap(node, cfg.Exec), nil
	case "hybrid":
		return diagnoseHybridPromptIdentityGap(node, cfg.Exec), nil
	default:
		return nil, nil
	}
}

// decodeDiagnosticConfig 只解析诊断需要的 config.exec 字段。
// 它不会按运行时 schema 补默认值，坏数据会按原样暴露出来。
func decodeDiagnosticConfig(node contract.DAGNode) (diagnosticConfig, bool, error) {
	if !hasExplicitRawJSON(node.Config) {
		return diagnosticConfig{}, false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(node.Config, &object); err != nil {
		return diagnosticConfig{}, false, fmt.Errorf("%s/%s config: %w", node.DagKey, node.NodeKey, err)
	}
	execRaw, ok := object["exec"]
	if !ok || !hasExplicitRawJSON(execRaw) {
		return diagnosticConfig{}, false, nil
	}
	var exec diagnosticExec
	if err := json.Unmarshal(execRaw, &exec); err != nil {
		return diagnosticConfig{}, true, fmt.Errorf("%s/%s config.exec: %w", node.DagKey, node.NodeKey, err)
	}
	return diagnosticConfig{Exec: &exec}, true, nil
}

// diagnoseAgentPromptIdentityGap 检查普通 agent 节点是否缺 prompt_key/agent_key。
func diagnoseAgentPromptIdentityGap(node contract.DAGNode, exec *diagnosticExec) []DAGPromptIdentityGap {
	if exec == nil || strings.TrimSpace(exec.PromptKey) != "" || strings.TrimSpace(exec.AgentKey) != "" {
		return nil
	}
	return []DAGPromptIdentityGap{newPromptIdentityGap(node, []string{
		"config.exec.prompt_key",
		"config.exec.agent_key",
	})}
}

// diagnoseHybridPromptIdentityGap 检查 hybrid verifier 的嵌套执行身份。
func diagnoseHybridPromptIdentityGap(node contract.DAGNode, exec *diagnosticExec) []DAGPromptIdentityGap {
	if exec == nil || exec.Verifier == nil {
		return nil
	}
	missing := hybridVerifierMissingFields(exec.Verifier)
	if len(missing) == 0 {
		return nil
	}
	return []DAGPromptIdentityGap{newPromptIdentityGap(node, missing)}
}

// hybridVerifierMissingFields 返回 verifier 启动所需但缺失的字段路径。
// provider 为空时立即返回，因为无法判断是否还需要 Codex 专属字段。
func hybridVerifierMissingFields(verifier *diagnosticAgentExec) []string {
	var missing []string
	if strings.TrimSpace(verifier.PromptKey) == "" && strings.TrimSpace(verifier.AgentKey) == "" {
		missing = append(missing, "config.exec.verifier.prompt_key", "config.exec.verifier.agent_key")
	}
	provider := strings.ToLower(strings.TrimSpace(verifier.Provider))
	if provider == "" {
		return append(missing, "config.exec.verifier.provider")
	}
	if provider == "codex" {
		missing = append(missing, missingCodexVerifierIdentityFields(verifier)...)
	}
	return missing
}

// missingCodexVerifierIdentityFields 检查 Codex verifier 必须绑定的 provider home 身份。
func missingCodexVerifierIdentityFields(verifier *diagnosticAgentExec) []string {
	var missing []string
	if strings.TrimSpace(verifier.CodexHome) == "" {
		missing = append(missing, "config.exec.verifier.codex_home")
	}
	if strings.TrimSpace(verifier.CodexInstanceKey) == "" {
		missing = append(missing, "config.exec.verifier.codex_instance_key")
	}
	if strings.TrimSpace(verifier.CodexModelProvider) == "" {
		missing = append(missing, "config.exec.verifier.codex_model_provider")
	}
	return missing
}

// newPromptIdentityGap 构造面向用户的缺口记录。
// 空 node_type 按 agent 处理，保持和运行时默认节点类型一致。
func newPromptIdentityGap(node contract.DAGNode, missing []string) DAGPromptIdentityGap {
	nodeType := strings.TrimSpace(node.NodeType)
	if nodeType == "" {
		nodeType = "agent"
	}
	return DAGPromptIdentityGap{
		DagKey:         node.DagKey,
		NodeKey:        node.NodeKey,
		NodeType:       nodeType,
		AssignedTo:     node.AssignedTo,
		MissingFields:  append([]string(nil), missing...),
		MissingSummary: strings.Join(missing, ", "),
	}
}

// enrichPromptIdentityGapsWithRecentRuns 补充每个缺口对应 DAG 的最近 run。
// 同一 DAG 会走本地缓存，避免列表诊断时重复查询 run 表。
func enrichPromptIdentityGapsWithRecentRuns(
	ctx context.Context,
	svc dagPromptIdentityDiagnosticsPort,
	gaps []DAGPromptIdentityGap,
) error {
	cache := make(map[string]recentRunSnapshot)
	for i := range gaps {
		recent, err := recentRunForDAG(ctx, svc, gaps[i].DagKey, cache)
		if err != nil {
			return err
		}
		gaps[i].RecentRunKey = recent.RunKey
		gaps[i].RecentRunStatus = recent.Status
	}
	return nil
}

// recentRunForDAG 读取并缓存某个 DAG 最近一次运行状态。
// 没有运行记录不是错误，返回空快照让诊断结果继续展示。
func recentRunForDAG(
	ctx context.Context,
	svc dagPromptIdentityDiagnosticsPort,
	dagKey string,
	cache map[string]recentRunSnapshot,
) (recentRunSnapshot, error) {
	if recent, ok := cache[dagKey]; ok {
		return recent, nil
	}
	runs, err := svc.ListRuns(ctx, contract.ListRunsRequest{DagKey: dagKey, Limit: 1})
	if err != nil {
		return recentRunSnapshot{}, err
	}
	var recent recentRunSnapshot
	if len(runs.Runs) > 0 {
		recent = recentRunSnapshot{RunKey: runs.Runs[0].RunKey, Status: runs.Runs[0].Status}
	}
	cache[dagKey] = recent
	return recent, nil
}

// newDAGPromptIdentityDiagnosticsOutput 组装只读诊断响应。
// Remediation 明确要求显式重绑或重建，防止调用方期待自动修复。
func newDAGPromptIdentityDiagnosticsOutput(
	gaps []DAGPromptIdentityGap,
	scan dagPromptIdentityDiagnosisScan,
) DAGPromptIdentityDiagnosticsOutput {
	env := newListEnvelope(gaps, 0, "read-only diagnostics; use prompt_list/prompt_get before explicit rebind")
	return DAGPromptIdentityDiagnosticsOutput{
		Gaps:                     env.Data,
		Data:                     env.Data,
		Total:                    env.Total,
		Showing:                  env.Showing,
		Truncated:                env.Truncated,
		Hint:                     env.Hint,
		ScannedDAGs:              scan.ScannedDAGs,
		DAGScanLimit:             scan.DAGScanLimit,
		DAGScanPossiblyTruncated: scan.DAGScanPossiblyTruncated,
		ReadOnly:                 true,
		Remediation:              "只读诊断，不会静默修复。保留历史失败 run 事件；对仍需保留的 DAG 使用 prompt_list/prompt_get 选择明确模板后通过 task_dag_apply_ops 显式重绑，或让用户重新用自然语言重建 DAG。",
	}
}
