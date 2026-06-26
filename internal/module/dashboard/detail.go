package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

var errOrchestrationServiceNotAvailable = errors.New("dashboard: orchestration service not available")

const dashboardUICreatedBy = "dashboard-ui"

// buildMetadata 存储从 debug.BuildInfo 提取的构建元数据。
type buildMetadata struct {
	version   string // 发布版本号，读不到时为 "dev"
	commit    string // git commit 短 hash，读不到时为 "unknown"
	buildTime string // 构建时间，ISO 8601 格式
	dirty     bool   // vcs 是否有未提交修改
	goVersion string // 编译所用 Go 版本
	runtime   string // GOOS/GOARCH 组合
}

// turnHistoryFromSnapshot 从 agent 快照提取当前活跃 turn 引用，无活跃 turn 时返回空切片。
func turnHistoryFromSnapshot(snapshot AgentSnapshot) []TurnRef {
	turnID := strings.TrimSpace(snapshot.ActiveTurnID)
	if turnID == "" {
		return []TurnRef{}
	}
	return []TurnRef{{
		TurnID:   turnID,
		ThreadID: strings.TrimSpace(snapshot.ThreadID),
		AgentID:  strings.TrimSpace(snapshot.ID),
		Status:   strings.TrimSpace(snapshot.State),
	}}
}

// effectiveDAGRuntime 返回优先使用 dagRuntime，回退到 orchestration 的 DAG 运行时。
func (s *service) effectiveDAGRuntime() contract.DAGRuntime {
	if s == nil {
		return nil
	}
	if s.dagRuntime != nil {
		return s.dagRuntime
	}
	return s.orchestration
}

// ListDAGs 列出 dashboard 可见的 DAG 摘要。
// 优先使用数据库快照以减少对编排服务的依赖；无快照时要求 DAG runtime 已配置。
func (s *service) ListDAGs(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	filter.Limit = util.ClampLimit(filter.Limit, 1, maxLogLimit, defaultLogLimit)
	if s.hasDAGSnapshotQueries() {
		return s.listDAGsFromSnapshot(ctx, filter)
	}
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	return runtime.ListDAGs(ctx, filter)
}

// GetDAGDetail 读取 DAG 模板详情。
// dagKey 为空直接报错；快照和 runtime 两条路径都不返回部分详情。
func (s *service) GetDAGDetail(ctx context.Context, dagKey string) (*contract.DAGDetail, error) {
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return nil, errors.New("dashboard: dag key is required")
	}
	if s.hasDAGSnapshotQueries() {
		return s.getDAGDetailFromSnapshot(ctx, key)
	}
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	detail, err := runtime.GetDAG(ctx, key)
	if err != nil {
		return nil, err
	}
	return &detail, nil
}

// ListDAGRuns 列出指定 DAG 的运行记录。
// limit 会被 clamp，status 只做 trim 后下传，具体枚举由 runtime/store 负责。
func (s *service) ListDAGRuns(ctx context.Context, dagKey, status string, limit int32) ([]contract.Run, error) {
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return nil, errors.New("dashboard: dag key is required")
	}
	limit = int32(util.ClampLimit(int(limit), 1, maxLogLimit, 50))
	status = strings.TrimSpace(status)
	if s.hasDAGSnapshotQueries() {
		return s.listDAGRunsFromSnapshot(ctx, key, status, limit)
	}
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	resp, err := runtime.ListRuns(ctx, contract.ListRunsRequest{
		DagKey: key,
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return nil, err
	}
	if resp.Runs == nil {
		return []contract.Run{}, nil
	}
	return resp.Runs, nil
}

// GetDAGRun 读取单次 DAG run 详情。
// nodes 为 nil 时转换为空切片，保持前端 JSON wire 兼容。
func (s *service) GetDAGRun(ctx context.Context, runKey string) (contract.GetRunResponse, error) {
	key := strings.TrimSpace(runKey)
	if key == "" {
		return contract.GetRunResponse{}, errors.New("dashboard: run key is required")
	}
	if s.hasDAGSnapshotQueries() {
		return s.getDAGRunFromSnapshot(ctx, key)
	}
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return contract.GetRunResponse{}, errOrchestrationServiceNotAvailable
	}
	resp, err := runtime.GetRun(ctx, contract.GetRunRequest{RunKey: key})
	if err != nil {
		return contract.GetRunResponse{}, err
	}
	if resp.Nodes == nil {
		resp.Nodes = []contract.DAGNode{}
	}
	return resp, nil
}

// CreateAndStartDAG 创建模板 DAG 并立即以手动触发方式启动一次运行。
func (s *service) CreateAndStartDAG(ctx context.Context, req contract.CreateDAGRequest, idempotencyKey string) (contract.DAGDetail, contract.StartDAGResponse, error) {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return contract.DAGDetail{}, contract.StartDAGResponse{}, errOrchestrationServiceNotAvailable
	}
	creator, ok := any(runtime).(contract.DAGCreateRuntime)
	if !ok {
		return contract.DAGDetail{}, contract.StartDAGResponse{}, errOrchestrationServiceNotAvailable
	}
	req.DagKey = strings.TrimSpace(req.DagKey)
	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)
	req.CreatedBy = strings.TrimSpace(req.CreatedBy)
	if req.CreatedBy == "" {
		req.CreatedBy = dashboardUICreatedBy
	}
	if req.DagKey == "" {
		return contract.DAGDetail{}, contract.StartDAGResponse{}, errors.New("dashboard: dag key is required")
	}
	if req.Title == "" {
		return contract.DAGDetail{}, contract.StartDAGResponse{}, errors.New("dashboard: dag title is required")
	}
	if len(req.Nodes) == 0 {
		return contract.DAGDetail{}, contract.StartDAGResponse{}, errors.New("dashboard: dag nodes are required")
	}
	detail, err := creator.CreateDAG(ctx, req)
	if err != nil {
		return contract.DAGDetail{}, contract.StartDAGResponse{}, err
	}
	started, err := s.StartDAG(ctx, req.DagKey, "manual", idempotencyKey)
	if err != nil {
		return detail, contract.StartDAGResponse{}, err
	}
	return detail, started, nil
}

// StartDAG 以手动触发方式启动 DAG。
// dashboard 入口只允许 manual trigger，防止 UI 越权模拟 scheduler 触发来源。
func (s *service) StartDAG(ctx context.Context, dagKey, triggerSource, idempotencyKey string) (contract.StartDAGResponse, error) {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return contract.StartDAGResponse{}, errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return contract.StartDAGResponse{}, errors.New("dashboard: dag key is required")
	}
	source := strings.TrimSpace(triggerSource)
	if source == "" {
		source = "manual"
	}
	if source != "manual" {
		return contract.StartDAGResponse{}, errors.New("dashboard: dag start trigger source must be manual")
	}
	return runtime.StartDAG(ctx, contract.StartDAGRequest{
		DagKey:         key,
		TriggerSource:  source,
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
	})
}

// DispatchDAGNode 将指定 DAG run 的节点派发给目标 agent/thread。
// 请求字段必须完整，runtime 未实现 DispatchNode 时返回配置错误。
func (s *service) DispatchDAGNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error) {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return contract.DispatchNodeResponse{}, errOrchestrationServiceNotAvailable
	}
	dispatcher, ok := any(runtime).(interface {
		DispatchNode(context.Context, contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error)
	})
	if !ok {
		return contract.DispatchNodeResponse{}, errOrchestrationServiceNotAvailable
	}
	request := contract.DispatchNodeRequest{
		DagKey:     strings.TrimSpace(req.DagKey),
		NodeKey:    strings.TrimSpace(req.NodeKey),
		RunID:      req.RunID,
		AssignedTo: strings.TrimSpace(req.AssignedTo),
	}
	if request.DagKey == "" {
		return contract.DispatchNodeResponse{}, errors.New("dashboard: dag key is required")
	}
	if request.NodeKey == "" {
		return contract.DispatchNodeResponse{}, errors.New("dashboard: node key is required")
	}
	if request.RunID <= 0 {
		return contract.DispatchNodeResponse{}, errors.New("dashboard: runId is required")
	}
	if request.AssignedTo == "" {
		return contract.DispatchNodeResponse{}, errors.New("dashboard: assignedTo is required")
	}
	return dispatcher.DispatchNode(ctx, request)
}

// TerminateDAG 终止指定 DAG run。
// reason 为空时使用 user_requested，确保审计和状态变更都有可读原因。
func (s *service) TerminateDAG(ctx context.Context, dagKey, runKey, reason string) error {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return errors.New("dashboard: dag key is required")
	}
	run := strings.TrimSpace(runKey)
	if run == "" {
		return errors.New("dashboard: run key is required")
	}
	cause := strings.TrimSpace(reason)
	if cause == "" {
		cause = "user_requested"
	}
	return runtime.TerminateDAG(ctx, contract.TerminateDAGRequest{
		DagKey: key,
		RunKey: run,
		Reason: cause,
	})
}

// DeleteDAG 删除 DAG 模板。
// 仅当 runtime 显式实现 DAGDeleteRuntime 时开放，避免 dashboard 调用未声明能力。
func (s *service) DeleteDAG(ctx context.Context, dagKey string) error {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return errOrchestrationServiceNotAvailable
	}
	deleter, ok := any(runtime).(contract.DAGDeleteRuntime)
	if !ok {
		return errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return errors.New("dashboard: dag key is required")
	}
	return deleter.DeleteDAG(ctx, contract.DeleteDAGRequest{DagKey: key})
}

// ApplyDAGOps 将前端编辑操作应用到 DAG 模板。
// baseVersion 不能为负，ops 必须是非空 JSON 数组，防止空补丁覆盖模板。
func (s *service) ApplyDAGOps(ctx context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return contract.ApplyOpsResponse{}, errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(req.DagKey)
	if key == "" {
		return contract.ApplyOpsResponse{}, errors.New("dashboard: dag key is required")
	}
	if req.BaseVersion < 0 {
		return contract.ApplyOpsResponse{}, errors.New("dashboard: dag base version must be non-negative")
	}
	if err := validateDashboardApplyOps(req.Ops); err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	return runtime.ApplyOps(ctx, contract.ApplyOpsRequest{
		DagKey:      key,
		BaseVersion: req.BaseVersion,
		Ops:         append([]byte(nil), req.Ops...),
	})
}

// validateDashboardApplyOps 校验 ops 字节是否为合法的非空 JSON 数组。
func validateDashboardApplyOps(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return errors.New("dashboard: dag ops are required")
	}
	var ops []json.RawMessage
	if err := json.Unmarshal(trimmed, &ops); err != nil {
		return fmt.Errorf("dashboard: dag ops must be a non-empty array: %w", err)
	}
	if len(ops) == 0 {
		return errors.New("dashboard: dag ops are required")
	}
	return nil
}
