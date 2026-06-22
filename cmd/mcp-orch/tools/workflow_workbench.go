package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	defaultWorkflowDiagnosticsLimit = 25
	maxWorkflowDiagnosticsLimit     = 100
)

// WorkflowDiagnosticsInput 是工作台诊断工具的定位条件。
// 至少需要一个定位符，避免在大库里无界扫描运行记录。
type WorkflowDiagnosticsInput struct {
	Pos           string `json:"pos,omitempty"`
	TraceID       string `json:"trace_id,omitempty"`
	RunKey        string `json:"run_key,omitempty"`
	RunID         int64  `json:"run_id,omitempty"`
	NodeKey       string `json:"node_key,omitempty"`
	ChildThreadID string `json:"child_thread_id,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

// WorkflowDiagnosticsOutput 是工作台使用的紧凑诊断响应。
// Runs 保留派生状态，Nodes 只返回命中查询的运行节点。
type WorkflowDiagnosticsOutput struct {
	Runs       []contract.Run     `json:"runs"`
	Nodes      []contract.DAGNode `json:"nodes"`
	Data       []contract.Run     `json:"data"`
	TotalRuns  int                `json:"total_runs"`
	TotalNodes int                `json:"total_nodes"`
	Limit      int                `json:"limit"`
	Hint       string             `json:"hint,omitempty"`
}

// WorkflowRecoveryActionInput 描述工作台触发的受控恢复动作。
// cancel_with_cleanup 需要 dag/run，retry_failed_node 需要 run_id/node。
type WorkflowRecoveryActionInput struct {
	Pos     string `json:"pos,omitempty"`
	Action  string `json:"action"`
	DagKey  string `json:"dag_key,omitempty"`
	RunKey  string `json:"run_key,omitempty"`
	RunID   int64  `json:"run_id,omitempty"`
	NodeKey string `json:"node_key,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// WorkflowRecoveryActionOutput 返回恢复入口的执行结果。
// 当前只有 cancel_with_cleanup 会真正落到后端终止动作。
type WorkflowRecoveryActionOutput struct {
	Action  string `json:"action"`
	Status  string `json:"status"`
	DagKey  string `json:"dag_key,omitempty"`
	RunKey  string `json:"run_key,omitempty"`
	RunID   int64  `json:"run_id,omitempty"`
	NodeKey string `json:"node_key,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type workflowDiagnosticsQuery struct {
	dagKey        string
	runKey        string
	traceID       string
	runID         int64
	nodeKey       string
	childThreadID string
	limit         int
}

// HandleWorkflowDiagnostics 读取运行快照并生成工作台诊断信息。
// 它只派生展示状态，不修改持久化的 run/node 状态。
func HandleWorkflowDiagnostics(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in WorkflowDiagnosticsInput) (any, error) {
		query, err := workflowDiagnosticsQueryFromInput(in)
		if err != nil {
			return nil, err
		}
		return workflowDiagnostics(ctx, svc, query)
	})
}

// HandleWorkflowRecoveryAction 执行工作台允许的恢复动作。
// 未落地的 retry 合约必须 fail-fast，避免静默重写运行时状态。
func HandleWorkflowRecoveryAction(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in WorkflowRecoveryActionInput) (any, error) {
		action, err := requireEnum(in.Action, "action", recoveryActionEnum)
		if err != nil {
			return nil, err
		}
		switch action {
		case "cancel_with_cleanup":
			return runCancelWithCleanup(ctx, svc, in)
		case "retry_failed_node":
			return rejectRetryFailedNode(in)
		default:
			return nil, fmt.Errorf("unsupported workflow recovery action %q", action)
		}
	})
}

// enrichWorkflowRuns 填充工作台需要的派生运行摘要。
// 传入 nodes 时会合并节点产物和失败信息；列表视图可传 nil 保持轻量。
func enrichWorkflowRuns(runs []contract.Run, nodes []contract.DAGNode) []contract.Run {
	enrichedNodes := enrichWorkflowNodes(nodes)
	out := make([]contract.Run, 0, len(runs))
	for _, run := range runs {
		item := run
		if item.ArtifactCount == 0 {
			item.ArtifactCount = workflowArtifactCount(item, enrichedNodes)
		}
		if item.DerivedState == "" {
			item.DerivedState = workflowDerivedState(item, enrichedNodes)
		}
		if item.BlockedReason == "" {
			item.BlockedReason = workflowBlockedReason(item.DerivedState, enrichedNodes)
		}
		if item.NextAction == "" {
			item.NextAction = workflowNextAction(item.DerivedState)
		}
		if len(item.RecoveryActions) == 0 {
			item.RecoveryActions = workflowRecoveryActions(item)
		}
		out = append(out, item)
	}
	return out
}

// workflowDiagnosticsQueryFromInput 校验诊断定位符并合并 pos 与旧字段。
// 多个定位符同时出现时按交集过滤，冲突值立即报错。
func workflowDiagnosticsQueryFromInput(in WorkflowDiagnosticsInput) (workflowDiagnosticsQuery, error) {
	parsed, err := parseOrchPos(in.Pos)
	if err != nil {
		return workflowDiagnosticsQuery{}, err
	}
	query := workflowDiagnosticsQuery{
		dagKey:        strings.TrimSpace(parsed.DagKey),
		runKey:        strings.TrimSpace(in.RunKey),
		traceID:       strings.TrimSpace(in.TraceID),
		runID:         in.RunID,
		nodeKey:       strings.TrimSpace(in.NodeKey),
		childThreadID: strings.TrimSpace(in.ChildThreadID),
		limit:         normalizeWorkflowDiagnosticsLimit(in.Limit),
	}
	if err := mergeWorkflowPos(&query.runKey, parsed.RunKey, "run_key"); err != nil {
		return workflowDiagnosticsQuery{}, err
	}
	if err := mergeWorkflowPos(&query.nodeKey, parsed.NodeKey, "node_key"); err != nil {
		return workflowDiagnosticsQuery{}, err
	}
	if parsed.RunID > 0 {
		if query.runID > 0 && query.runID != parsed.RunID {
			return workflowDiagnosticsQuery{}, fmt.Errorf("run_id %d conflicts with pos value %d", query.runID, parsed.RunID)
		}
		query.runID = parsed.RunID
	}
	if !query.hasIdentifier() {
		return workflowDiagnosticsQuery{}, errors.New("diagnostics require trace_id, run_key, run_id, node_key, child_thread_id, or pos")
	}
	return query, nil
}

func mergeWorkflowPos(dst *string, value, field string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if *dst != "" && *dst != value {
		return fmt.Errorf("%s %q conflicts with pos value %q", field, *dst, value)
	}
	*dst = value
	return nil
}

func (q workflowDiagnosticsQuery) hasIdentifier() bool {
	return q.dagKey != "" ||
		q.runKey != "" ||
		q.traceID != "" ||
		q.runID > 0 ||
		q.nodeKey != "" ||
		q.childThreadID != ""
}

func normalizeWorkflowDiagnosticsLimit(limit int) int {
	if limit <= 0 {
		return defaultWorkflowDiagnosticsLimit
	}
	if limit > maxWorkflowDiagnosticsLimit {
		return maxWorkflowDiagnosticsLimit
	}
	return limit
}

// workflowDiagnostics 根据定位条件读取候选 run，并返回命中的紧凑结果。
// run_key 走精确读取；否则按 dag 或近期 DAG 列表做有上限扫描。
func workflowDiagnostics(ctx context.Context, svc contract.OrchestrationService, query workflowDiagnosticsQuery) (WorkflowDiagnosticsOutput, error) {
	var runs []contract.GetRunResponse
	var err error
	if query.runKey != "" {
		runs, err = workflowDiagnosticsByRunKey(ctx, svc, query.runKey)
	} else {
		runs, err = workflowDiagnosticsByScan(ctx, svc, query)
	}
	if err != nil {
		return WorkflowDiagnosticsOutput{}, err
	}
	return newWorkflowDiagnosticsOutput(runs, query), nil
}

func workflowDiagnosticsByRunKey(ctx context.Context, svc contract.OrchestrationService, runKey string) ([]contract.GetRunResponse, error) {
	resp, err := svc.GetRun(ctx, contract.GetRunRequest{RunKey: runKey})
	if err != nil {
		return nil, err
	}
	return []contract.GetRunResponse{resp}, nil
}

func workflowDiagnosticsByScan(
	ctx context.Context,
	svc contract.OrchestrationService,
	query workflowDiagnosticsQuery,
) ([]contract.GetRunResponse, error) {
	dagKeys, err := workflowDiagnosticDAGKeys(ctx, svc, query)
	if err != nil {
		return nil, err
	}
	out := make([]contract.GetRunResponse, 0, query.limit)
	for _, dagKey := range dagKeys {
		runs, err := svc.ListRuns(ctx, contract.ListRunsRequest{DagKey: dagKey, Limit: int32(query.limit)})
		if err != nil {
			return nil, err
		}
		for _, run := range runs.Runs {
			if len(out) >= query.limit {
				return out, nil
			}
			resp, err := svc.GetRun(ctx, contract.GetRunRequest{RunKey: run.RunKey})
			if err != nil {
				return nil, err
			}
			out = append(out, resp)
		}
	}
	return out, nil
}

func workflowDiagnosticDAGKeys(
	ctx context.Context,
	svc contract.OrchestrationService,
	query workflowDiagnosticsQuery,
) ([]string, error) {
	if query.dagKey != "" {
		return []string{query.dagKey}, nil
	}
	dags, err := svc.ListDAGs(ctx, contract.ListDAGsFilter{Limit: query.limit})
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(dags))
	for _, dag := range dags {
		if strings.TrimSpace(dag.DagKey) != "" {
			keys = append(keys, dag.DagKey)
		}
	}
	return keys, nil
}

func newWorkflowDiagnosticsOutput(runs []contract.GetRunResponse, query workflowDiagnosticsQuery) WorkflowDiagnosticsOutput {
	out := WorkflowDiagnosticsOutput{Limit: query.limit, Hint: "next: use task_get_run for the full runtime snapshot"}
	for _, resp := range runs {
		if !workflowRunIdentityMatches(resp.Run, query) {
			continue
		}
		nodes := workflowMatchingNodes(enrichWorkflowNodes(resp.Nodes), resp.Run, query)
		if !workflowRunMatches(resp.Run, query) && len(nodes) == 0 {
			continue
		}
		enrichedRun := enrichWorkflowRuns([]contract.Run{resp.Run}, nodes)[0]
		out.Runs = append(out.Runs, enrichedRun)
		out.Nodes = append(out.Nodes, nodes...)
	}
	out.Data = out.Runs
	out.TotalRuns = len(out.Runs)
	out.TotalNodes = len(out.Nodes)
	return out
}

func workflowRunIdentityMatches(run contract.Run, query workflowDiagnosticsQuery) bool {
	if query.runKey != "" && run.RunKey != query.runKey {
		return false
	}
	if query.runID > 0 && run.ID != query.runID {
		return false
	}
	return true
}

func workflowRunMatches(run contract.Run, query workflowDiagnosticsQuery) bool {
	if !workflowRunIdentityMatches(run, query) {
		return false
	}
	if query.traceID != "" && !rawMessageContains(run.Events, query.traceID) && !rawMessageContains(run.Metadata, query.traceID) {
		return false
	}
	return true
}

func workflowMatchingNodes(nodes []contract.DAGNode, run contract.Run, query workflowDiagnosticsQuery) []contract.DAGNode {
	out := make([]contract.DAGNode, 0, len(nodes))
	for _, node := range nodes {
		if workflowNodeMatches(node, query) {
			out = append(out, node)
		}
	}
	if query.nodeKey == "" && query.childThreadID == "" && query.traceID == "" && workflowRunMatches(run, query) {
		return nodes
	}
	return out
}

func workflowNodeMatches(node contract.DAGNode, query workflowDiagnosticsQuery) bool {
	if query.nodeKey != "" && node.NodeKey != query.nodeKey {
		return false
	}
	if query.childThreadID != "" && (node.SpawningThreadID == nil || *node.SpawningThreadID != query.childThreadID) {
		return false
	}
	if query.traceID != "" && !rawMessageContains(node.Config, query.traceID) && !rawMessageContains(node.Result, query.traceID) {
		return false
	}
	return true
}

func rawMessageContains(raw json.RawMessage, value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && strings.Contains(string(raw), value)
}

// enrichWorkflowNodes 派生节点诊断字段。
// 它不改写节点状态，只补 executor、failure_class、artifact_links 和 next_action。
func enrichWorkflowNodes(nodes []contract.DAGNode) []contract.DAGNode {
	out := make([]contract.DAGNode, 0, len(nodes))
	for _, node := range nodes {
		item := node
		if item.Executor == "" {
			item.Executor = strings.TrimSpace(item.AssignedTo)
		}
		if item.FailureClass == "" {
			item.FailureClass = failureClassFromRaw(item.Result)
		}
		if item.LastWakeupAt == nil {
			item.LastWakeupAt = item.LastEventAt
		}
		if len(item.ArtifactLinks) == 0 {
			item.ArtifactLinks = artifactLinksFromRaw(item.Result, item.NodeKey)
		}
		if item.NextAction == "" {
			item.NextAction = workflowNodeNextAction(item)
		}
		out = append(out, item)
	}
	return out
}

func workflowArtifactCount(run contract.Run, nodes []contract.DAGNode) int {
	count := 0
	if _, ok := contract.FinalOutputFileFromRunMetadata(run.Metadata); ok {
		count++
	}
	for _, node := range nodes {
		count += len(node.ArtifactLinks)
	}
	return count
}

func workflowDerivedState(run contract.Run, nodes []contract.DAGNode) string {
	status := strings.TrimSpace(run.Status)
	switch status {
	case "running":
		if nodeWaitingForAssignee(nodes) != nil {
			return "waiting_for_assignee"
		}
		if nodeWaitingForTimer(nodes) != nil {
			return "waiting_timer"
		}
		return "active"
	case "failed":
		if hasRecoverableFailedNode(nodes) {
			return "recoverable_failed"
		}
		return "failed"
	default:
		return status
	}
}

func workflowBlockedReason(state string, nodes []contract.DAGNode) string {
	switch state {
	case "waiting_for_assignee":
		if node := nodeWaitingForAssignee(nodes); node != nil {
			return fmt.Sprintf("node %s waiting for assignee", node.NodeKey)
		}
	case "waiting_timer":
		if node := nodeWaitingForTimer(nodes); node != nil {
			return fmt.Sprintf("node %s waiting for wakeup", node.NodeKey)
		}
	case "failed", "recoverable_failed":
		if node := firstFailedNode(nodes); node != nil && strings.TrimSpace(node.FailureClass) != "" {
			return fmt.Sprintf("node %s failed: %s", node.NodeKey, node.FailureClass)
		}
	}
	return ""
}

func workflowNextAction(state string) string {
	switch state {
	case "active":
		return "monitor"
	case "waiting_for_assignee":
		return "dispatch_node"
	case "waiting_timer":
		return "wait_for_wakeup"
	case "recoverable_failed":
		return "retry_failed_node"
	case "failed":
		return "inspect_failure"
	default:
		return ""
	}
}

func workflowRecoveryActions(run contract.Run) []contract.WorkflowRecoveryAction {
	switch workflowDerivedState(run, nil) {
	case "active", "waiting_for_assignee", "waiting_timer":
		return []contract.WorkflowRecoveryAction{{
			Action:  "cancel_with_cleanup",
			Label:   "Cancel with cleanup",
			Enabled: true,
			Policy:  "allow",
		}}
	case "failed", "recoverable_failed":
		return []contract.WorkflowRecoveryAction{{
			Action:  "retry_failed_node",
			Label:   "Retry failed node",
			Enabled: false,
			Reason:  "runtime reset/retry contract is not available",
			Policy:  "runtime_contract_missing",
		}}
	default:
		return nil
	}
}

func workflowNodeNextAction(node contract.DAGNode) string {
	switch strings.TrimSpace(node.Status) {
	case "pending", "ready":
		if strings.TrimSpace(node.AssignedTo) == "" {
			return "dispatch_node"
		}
		return "wait_for_wakeup"
	case "running":
		return "monitor"
	case "failed":
		if recoverableFailureClass(node.FailureClass) {
			return "retry_failed_node"
		}
		return "inspect_failure"
	default:
		return ""
	}
}

func nodeWaitingForAssignee(nodes []contract.DAGNode) *contract.DAGNode {
	for i := range nodes {
		status := strings.TrimSpace(nodes[i].Status)
		if (status == "pending" || status == "ready") && strings.TrimSpace(nodes[i].AssignedTo) == "" {
			return &nodes[i]
		}
	}
	return nil
}

func nodeWaitingForTimer(nodes []contract.DAGNode) *contract.DAGNode {
	for i := range nodes {
		status := strings.TrimSpace(nodes[i].Status)
		if (status == "pending" || status == "ready") && strings.TrimSpace(nodes[i].AssignedTo) != "" && nodes[i].ActiveWakeupID == nil {
			return &nodes[i]
		}
	}
	return nil
}

func firstFailedNode(nodes []contract.DAGNode) *contract.DAGNode {
	for i := range nodes {
		if strings.TrimSpace(nodes[i].Status) == "failed" {
			return &nodes[i]
		}
	}
	return nil
}

func hasRecoverableFailedNode(nodes []contract.DAGNode) bool {
	for _, node := range nodes {
		if strings.TrimSpace(node.Status) == "failed" && recoverableFailureClass(node.FailureClass) {
			return true
		}
	}
	return false
}

func recoverableFailureClass(class string) bool {
	switch strings.TrimSpace(class) {
	case "transient", "timeout", "rate_limit", "tool_error", "agent_interrupted":
		return true
	default:
		return false
	}
}

func failureClassFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var payload struct {
		FailureClass string `json:"failure_class"`
		ErrorClass   string `json:"error_class"`
		Error        struct {
			Class string `json:"class"`
			Code  string `json:"code"`
			Kind  string `json:"kind"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	for _, value := range []string{payload.FailureClass, payload.ErrorClass, payload.Error.Class, payload.Error.Code, payload.Error.Kind} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func artifactLinksFromRaw(raw json.RawMessage, nodeKey string) []contract.WorkflowArtifactLink {
	if len(raw) == 0 {
		return nil
	}
	var payload struct {
		SharedFile *struct {
			Path  string `json:"path"`
			Label string `json:"label"`
		} `json:"sharedfile"`
		ArtifactLinks []contract.WorkflowArtifactLink `json:"artifact_links"`
		Artifacts     []contract.WorkflowArtifactLink `json:"artifacts"`
		OutputFile    string                          `json:"output_file"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	var links []contract.WorkflowArtifactLink
	links = appendNormalizedArtifactLinks(links, payload.ArtifactLinks, nodeKey)
	links = appendNormalizedArtifactLinks(links, payload.Artifacts, nodeKey)
	if payload.SharedFile != nil {
		links = appendArtifactLink(links, "sharedfile", payload.SharedFile.Label, payload.SharedFile.Path, nodeKey)
	}
	if strings.TrimSpace(payload.OutputFile) != "" {
		links = appendArtifactLink(links, "sharedfile", "output file", payload.OutputFile, nodeKey)
	}
	return links
}

func appendNormalizedArtifactLinks(
	dst []contract.WorkflowArtifactLink,
	links []contract.WorkflowArtifactLink,
	nodeKey string,
) []contract.WorkflowArtifactLink {
	for _, link := range links {
		dst = appendArtifactLink(dst, link.Kind, link.Label, firstNonEmpty(link.Path, link.URL), firstNonEmpty(link.NodeKey, nodeKey))
	}
	return dst
}

func appendArtifactLink(dst []contract.WorkflowArtifactLink, kind, label, path, nodeKey string) []contract.WorkflowArtifactLink {
	path = strings.TrimSpace(path)
	if path == "" {
		return dst
	}
	kind = firstNonEmpty(kind, "sharedfile")
	label = firstNonEmpty(label, path)
	return append(dst, contract.WorkflowArtifactLink{
		Kind:    kind,
		Label:   label,
		Path:    path,
		NodeKey: strings.TrimSpace(nodeKey),
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func runCancelWithCleanup(
	ctx context.Context,
	svc contract.OrchestrationService,
	in WorkflowRecoveryActionInput,
) (WorkflowRecoveryActionOutput, error) {
	req, err := terminateDAGRequestFromInput(TerminateDAGInput{
		DagKey: in.DagKey,
		RunKey: in.RunKey,
		Pos:    in.Pos,
		Reason: in.Reason,
	})
	if err != nil {
		return WorkflowRecoveryActionOutput{}, err
	}
	if err := svc.TerminateDAG(ctx, req); err != nil {
		return WorkflowRecoveryActionOutput{}, translateTerminateDAGError(req, err)
	}
	return WorkflowRecoveryActionOutput{
		Action: "cancel_with_cleanup",
		Status: "accepted",
		DagKey: req.DagKey,
		RunKey: req.RunKey,
		Reason: req.Reason,
	}, nil
}

func rejectRetryFailedNode(in WorkflowRecoveryActionInput) (WorkflowRecoveryActionOutput, error) {
	runID, err := resolveRunIDInput(in.RunID, in.Pos)
	if err != nil {
		return WorkflowRecoveryActionOutput{}, err
	}
	nodeKey, err := resolveNodeKeyInput(in.NodeKey, in.Pos)
	if err != nil {
		return WorkflowRecoveryActionOutput{}, err
	}
	return WorkflowRecoveryActionOutput{}, fmt.Errorf(
		"retry_failed_node requires runtime reset/retry contract before it can safely reset run_id=%d node_key=%s",
		runID,
		nodeKey,
	)
}
