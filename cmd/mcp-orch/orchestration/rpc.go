package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"go.uber.org/fx"
)

// runtimeReportParams 是 reportRuntime RPC 的入参，兼容 agent_id 和 agentId。
type runtimeReportParams struct {
	AgentID  string `json:"agent_id"`
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// UnmarshalJSON 严格解码 runtime report，并校验 agent_id/agentId 别名不能冲突。
func (p *runtimeReportParams) UnmarshalJSON(data []byte) error {
	type payload struct {
		AgentID       string `json:"agent_id"`
		AgentIDLegacy string `json:"agentId"`
		Port          int    `json:"port,omitempty"`
		Provider      string `json:"provider,omitempty"`
	}
	return decodeLegacyAliasWith(data, new(payload), func(raw *payload, legacy *payload) error {
		p.AgentID = strings.TrimSpace(raw.AgentID)
		p.Port = raw.Port
		p.Provider = raw.Provider
		agentID := strings.TrimSpace(p.AgentID)
		legacyAgentID := strings.TrimSpace(legacy.AgentIDLegacy)
		if agentID != "" && legacyAgentID != "" && agentID != legacyAgentID {
			return fmt.Errorf("runtime report agent id aliases conflict: agent_id=%q agentId=%q", agentID, legacyAgentID)
		}
		p.AgentID = shared.FirstTrimmed(agentID, legacyAgentID)
		return nil
	}, decodeStrictRuntimeReportJSON)
}

// decodeStrictRuntimeReportJSON 禁止 reportRuntime payload 出现未知字段或多段 JSON。
func decodeStrictRuntimeReportJSON(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(struct{})); err != io.EOF {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

// RPCFacadeParams 按 RPC handler 实际消费面注入 orchestration 窄端口。
type RPCFacadeParams struct {
	fx.In

	Launch     contract.AgentLaunchPort
	State      contract.AgentStateReader
	Stop       contract.AgentStopPort
	Turns      contract.TurnSubmissionPort
	Runtime    contract.AgentRuntimePort
	Reports    contract.AgentReportPort
	DAGCreate  contract.DAGCreateRuntime
	DAGRuntime contract.DAGRuntime
	DAGDelete  contract.DAGDeleteRuntime
	NodeStatus contract.DAGNodeStatusRuntime
}

// ProvideRPCFacade 把 orchestration RPC handler 集合交给 cmd/mcp-orch 根装配层。
// 它只通过 fx group:"rpc_handlers" 被根入口消费，不是给其它子包复用的通用协议壳。
func ProvideRPCFacade(ports RPCFacadeParams) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"agent/launch": rpc.StrictHandler(func(ctx context.Context, p launchParams) (any, error) {
			req := launchRequestFromParams(p)
			if err := ports.Launch.LaunchAgent(ctx, req); err != nil {
				return nil, err
			}
			return map[string]any{"success": true, "agent_id": strings.TrimSpace(req.AgentID), "status": "running"}, nil
		}),
		"agent/submit": rpc.StrictHandler(func(ctx context.Context, p submitParams) (any, error) {
			req, err := submissionFromParams(ctx, ports.State, p)
			if err != nil {
				return nil, err
			}
			if err := ports.Turns.SubmitTurn(ctx, req); err != nil {
				return nil, err
			}
			return map[string]bool{"success": true}, nil
		}),
		"agent/submitPrompt": rpc.StrictHandler(func(ctx context.Context, p submitPromptParams) (any, error) {
			req, err := submissionFromParams(ctx, ports.State, submitParams(p))
			if err != nil {
				return nil, err
			}
			if err := ports.Turns.SubmitTurn(ctx, req); err != nil {
				return nil, err
			}
			return map[string]bool{"success": true}, nil
		}),
		"agent/stop": rpc.StrictHandler(func(ctx context.Context, p agentIDParams) (any, error) {
			return nil, ports.Stop.StopAgent(ctx, p.AgentID)
		}),
		"agent/list": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return ports.State.ListAgents(ctx)
		}),
		"agent/snapshot": rpc.StrictHandler(func(ctx context.Context, p agentIDParams) (any, error) {
			return ports.State.Snapshot(ctx, p.AgentID)
		}),
		"orchestration/reportRuntime": rpc.StrictHandler(func(ctx context.Context, p runtimeReportParams) (any, error) {
			if err := ports.Runtime.UpdateRuntime(ctx, runtimeReportFromParams(p)); err != nil {
				return nil, err
			}
			return map[string]bool{"success": true}, nil
		}),
		"agent/getState": rpc.StrictHandler(func(ctx context.Context, p agentIDParams) (any, error) {
			return ports.State.GetState(ctx, p.AgentID)
		}),
		"agent/getReport": rpc.StrictHandler(func(ctx context.Context, p agentIDParams) (any, error) {
			return ports.Reports.GetReport(ctx, p.AgentID)
		}),
		ReportMethodRememberReportRequest: rpc.StrictHandler(func(ctx context.Context, p rememberReportRequestParams) (any, error) {
			return ports.Reports.RememberReportRequest(ctx, rememberReportRequestFromParams(p))
		}),
		ReportMethodReportEvent: rpc.StrictHandler(func(ctx context.Context, p reportEventParams) (any, error) {
			return ports.Reports.HandleReportEvent(ctx, reportEventFromParams(p))
		}),
		"task/dag/create": rpc.StrictHandler(func(ctx context.Context, p createDAGParams) (any, error) {
			return ports.DAGCreate.CreateDAG(ctx, createDAGRequestFromParams(p))
		}),
		"task/dag/get": rpc.StrictHandler(func(ctx context.Context, p dagKeyParams) (any, error) {
			return ports.DAGRuntime.GetDAG(ctx, p.DagKey)
		}),
		"task/dag/list": rpc.StrictHandler(func(ctx context.Context, p listDAGsParams) (any, error) {
			return ports.DAGRuntime.ListDAGs(ctx, listDAGsFilterFromParams(p))
		}),
		"task/dag/delete": rpc.StrictHandler(func(ctx context.Context, p dagKeyParams) (any, error) {
			return nil, ports.DAGDelete.DeleteDAG(ctx, contract.DeleteDAGRequest{DagKey: p.DagKey})
		}),
		"task/node/update": rpc.StrictHandler(func(ctx context.Context, p updateNodeParams) (any, error) {
			return ports.NodeStatus.UpdateNodeStatus(ctx, updateNodeRequestFromParams(p))
		}),
		"orchestration/report": rpc.StrictHandler(func(ctx context.Context, p reportParams) (any, error) {
			return ports.Reports.GetReport(ctx, p.AgentID)
		}),
	}}
}

// launchRequestFromParams 将 RPC launch 入参转换为 service 层 LaunchRequest。
func launchRequestFromParams(p launchParams) LaunchRequest {
	return LaunchRequest{
		AgentID:      p.AgentID,
		Name:         p.Name,
		Prompt:       p.Prompt,
		Instructions: p.Instructions,
		ParentID:     p.ParentID,
		AgentType:    p.AgentType,
		AgentKey:     p.AgentKey,
		PromptKey:    p.PromptKey,
		MemoryScope:  p.MemoryScope,
		Cwd:          p.CWD,
		Command:      append([]string(nil), p.Command...),
		Env:          envList(p.Env),
	}
}

// submissionFromParams 将 submit RPC 入参转换为 TurnSubmission，并补齐当前 thread id。
func submissionFromParams(ctx context.Context, snapshots contract.AgentStateReader, p submitParams) (TurnSubmission, error) {
	agentID := strings.TrimSpace(p.AgentID)
	items, err := inputItemsFromSubmitParams(p)
	if err != nil {
		return TurnSubmission{}, err
	}
	return TurnSubmission{
		AgentID:              agentID,
		ThreadID:             submissionThreadID(ctx, snapshots, agentID),
		Inputs:               items,
		SelectedSkills:       append([]string(nil), p.SelectedSkills...),
		ManualSkillSelection: p.ManualSkillSelection,
		OutputSchema:         append(json.RawMessage(nil), p.OutputSchema...),
	}, nil
}

// inputItemsFromSubmitParams 兼容旧版 input JSON 与新版 prompt/images/files 三类输入。
func inputItemsFromSubmitParams(p submitParams) ([]shareddto.InputItem, error) {
	if len(p.legacyInput) > 0 && strings.TrimSpace(p.Prompt) == "" && len(p.Images) == 0 && len(p.Files) == 0 {
		return decodeInputItems(p.legacyInput)
	}
	items := make([]shareddto.InputItem, 0, 1+len(p.Images)+len(p.Files))
	if prompt := strings.TrimSpace(p.Prompt); prompt != "" {
		items = append(items, shareddto.InputItem{Type: "text", Content: prompt})
	}
	for _, raw := range p.Images {
		if path := strings.TrimSpace(raw); path != "" {
			items = append(items, shareddto.InputItem{Type: "image", Path: path})
		}
	}
	for _, raw := range p.Files {
		if path := strings.TrimSpace(raw); path != "" {
			items = append(items, shareddto.InputItem{Type: "mention", Path: path})
		}
	}
	return items, nil
}

// decodeInputItems 兼容单个 InputItem 或 InputItem 数组两种旧版 payload。
func decodeInputItems(raw json.RawMessage) ([]shareddto.InputItem, error) {
	var items []shareddto.InputItem
	if err := json.Unmarshal(raw, &items); err == nil {
		return items, nil
	}
	var item shareddto.InputItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, err
	}
	return []shareddto.InputItem{item}, nil
}

// submissionThreadID 从 snapshot 获取当前 provider thread，失败时退回 agentID 作为兼容 thread id。
func submissionThreadID(ctx context.Context, snapshots contract.AgentStateReader, agentID string) string {
	snapshot, err := snapshots.Snapshot(ctx, agentID)
	if err == nil && strings.TrimSpace(snapshot.ThreadID) != "" {
		return snapshot.ThreadID
	}
	return strings.TrimSpace(agentID)
}

// envList 将 map 形式环境变量稳定排序为 KEY=VALUE 列表。
func envList(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+env[key])
	}
	return values
}

// rememberReportRequestFromParams 将 RPC 入参映射为 report requester 请求。
func rememberReportRequestFromParams(p rememberReportRequestParams) RememberReportRequest {
	return RememberReportRequest{
		AgentID:     p.AgentID,
		RequesterID: p.RequesterID,
	}
}

// reportEventFromParams 复制 event_data，避免后续修改 RPC buffer 影响业务层。
func reportEventFromParams(p reportEventParams) ReportEvent {
	return ReportEvent{
		AgentID:   p.AgentID,
		Report:    p.Report,
		EventType: p.EventType,
		EventData: append(json.RawMessage(nil), p.EventData...),
	}
}

// runtimeReportFromParams 将 runtime 上报 RPC 入参映射为 service DTO。
func runtimeReportFromParams(p runtimeReportParams) RuntimeReport {
	return RuntimeReport{
		AgentID:  p.AgentID,
		Port:     p.Port,
		Provider: p.Provider,
	}
}

// createDAGRequestFromParams 将 DAG create RPC 入参转换为 service 请求。
func createDAGRequestFromParams(p createDAGParams) CreateDAGRequest {
	return CreateDAGRequest{
		DagKey:      p.DagKey,
		Title:       p.Title,
		Description: p.Description,
		CreatedBy:   p.CreatedBy,
		Metadata:    append(json.RawMessage(nil), p.Metadata...),
		Nodes:       createDAGNodesFromParams(p.Nodes),
	}
}

// createDAGNodesFromParams 深拷贝 DAG 节点列表，隔离 RPC 入参切片。
func createDAGNodesFromParams(nodes []createDAGNodeParams) []CreateDAGNodeRequest {
	mapped := make([]CreateDAGNodeRequest, 0, len(nodes))
	for _, node := range nodes {
		mapped = append(mapped, CreateDAGNodeRequest{
			NodeKey:    node.NodeKey,
			Title:      node.Title,
			NodeType:   node.NodeType,
			AssignedTo: node.AssignedTo,
			DependsOn:  append([]string(nil), node.DependsOn...),
			CommandRef: node.CommandRef,
			Config:     append(json.RawMessage(nil), node.Config...),
		})
	}
	return mapped
}

// listDAGsFilterFromParams 将 DAG list RPC 入参映射为 store 过滤条件。
func listDAGsFilterFromParams(p listDAGsParams) ListDAGsFilter {
	return ListDAGsFilter{Status: p.Status, Keyword: p.Keyword, Limit: p.Limit}
}

// updateNodeRequestFromParams 将节点状态更新 RPC 入参转换为 service 请求。
func updateNodeRequestFromParams(p updateNodeParams) UpdateNodeStatusRequest {
	return UpdateNodeStatusRequest{
		DagKey:  p.DagKey,
		NodeKey: p.NodeKey,
		RunID:   p.RunID,
		Status:  p.Status,
		Result:  append(json.RawMessage(nil), p.Result...),
	}
}

// launchParams 是 agent/launch RPC 的 wire 入参。
type launchParams struct {
	AgentID      string            `json:"id"`
	Name         string            `json:"name,omitempty"`
	Prompt       string            `json:"prompt,omitempty"`
	CWD          string            `json:"cwd,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Command      []string          `json:"command,omitempty"`
	ParentID     string            `json:"parent_id,omitempty"`
	AgentType    string            `json:"agent_type,omitempty"`
	AgentKey     string            `json:"agent_key,omitempty"`
	PromptKey    string            `json:"prompt_key,omitempty"`
	MemoryScope  string            `json:"memory_scope,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
}

// launchConfigParams 兼容旧版 config 嵌套字段和 camelCase 命名。
type launchConfigParams struct {
	ParentID       string `json:"parent_id,omitempty"`
	ParentIDAlt    string `json:"parentId,omitempty"`
	ParentIDLegacy string `json:"parentID,omitempty"`
	AgentType      string `json:"agent_type,omitempty"`
	AgentTypeAlt   string `json:"agentType,omitempty"`
	PromptKey      string `json:"prompt_key,omitempty"`
	PromptKeyAlt   string `json:"promptKey,omitempty"`
	MemoryScope    string `json:"memory_scope,omitempty"`
	MemoryScopeAlt string `json:"memoryScope,omitempty"`
	AgentScope     string `json:"agent_memory_scope,omitempty"`
	AgentScopeAlt  string `json:"agentMemoryScope,omitempty"`
}

// UnmarshalJSON 兼容 launch 入参的新旧字段别名，并优先保留显式新字段。
func (p *launchParams) UnmarshalJSON(data []byte) error {
	type current launchParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID      string             `json:"agentId"`
		AgentIDSnake string             `json:"agent_id"`
		ParentID     string             `json:"parentId"`
		ParentIDAlt  string             `json:"parentID"`
		AgentType    string             `json:"agentType"`
		AgentTypeAlt string             `json:"agent_type"`
		PromptKey    string             `json:"promptKey"`
		PromptKeyAlt string             `json:"prompt_key"`
		MemoryScope  string             `json:"memoryScope"`
		MemoryAlt    string             `json:"memory_scope"`
		AgentScope   string             `json:"agent_memory_scope"`
		Config       launchConfigParams `json:"config"`
	}) error {
		*p = launchParams(*raw)
		if strings.TrimSpace(p.AgentID) == "" {
			p.AgentID = shared.FirstTrimmed(legacy.AgentIDSnake, legacy.AgentID)
		}
		if strings.TrimSpace(p.ParentID) == "" {
			p.ParentID = shared.FirstTrimmed(
				legacy.ParentID,
				legacy.ParentIDAlt,
				legacy.Config.ParentID,
				legacy.Config.ParentIDAlt,
				legacy.Config.ParentIDLegacy,
			)
		}
		if strings.TrimSpace(p.AgentType) == "" {
			p.AgentType = shared.FirstTrimmed(
				legacy.AgentTypeAlt,
				legacy.AgentType,
				legacy.Config.AgentType,
				legacy.Config.AgentTypeAlt,
			)
		}
		if strings.TrimSpace(p.PromptKey) == "" {
			p.PromptKey = shared.FirstTrimmed(
				legacy.PromptKeyAlt,
				legacy.PromptKey,
				legacy.Config.PromptKey,
				legacy.Config.PromptKeyAlt,
			)
		}
		if strings.TrimSpace(p.MemoryScope) == "" {
			p.MemoryScope = shared.FirstTrimmed(
				legacy.MemoryAlt,
				legacy.MemoryScope,
				legacy.AgentScope,
				legacy.Config.MemoryScope,
				legacy.Config.MemoryScopeAlt,
				legacy.Config.AgentScope,
				legacy.Config.AgentScopeAlt,
			)
		}
		return nil
	})
}

// agentIDParams 是只需要 agent id 的 RPC 通用入参。
type agentIDParams struct {
	AgentID string `json:"agent_id"`
}

// UnmarshalJSON 兼容 agent_id 和 agentId 两种命名。
func (p *agentIDParams) UnmarshalJSON(data []byte) error {
	type current agentIDParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID string `json:"agentId"`
	}) error {
		*p = agentIDParams(*raw)
		if strings.TrimSpace(p.AgentID) == "" {
			p.AgentID = strings.TrimSpace(legacy.AgentID)
		}
		return nil
	})
}

// dagKeyParams 是只需要 dag_key 的 RPC 通用入参。
type dagKeyParams struct {
	DagKey string `json:"dag_key"`
}

// UnmarshalJSON 兼容 dag_key 和 dagKey 两种命名。
func (p *dagKeyParams) UnmarshalJSON(data []byte) error {
	type current dagKeyParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		DagKey string `json:"dagKey"`
	}) error {
		*p = dagKeyParams(*raw)
		if strings.TrimSpace(p.DagKey) == "" {
			p.DagKey = strings.TrimSpace(legacy.DagKey)
		}
		return nil
	})
}

// dagNodeParams 是定位 DAG 节点的 RPC 入参。
type dagNodeParams struct {
	DagKey  string `json:"dag_key"`
	NodeKey string `json:"node_key"`
}

// UnmarshalJSON 兼容 dag/node key 的 snake_case 和 camelCase 命名。
func (p *dagNodeParams) UnmarshalJSON(data []byte) error {
	type current dagNodeParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		DagKey  string `json:"dagKey"`
		NodeKey string `json:"nodeKey"`
	}) error {
		*p = dagNodeParams(*raw)
		if strings.TrimSpace(p.DagKey) == "" {
			p.DagKey = strings.TrimSpace(legacy.DagKey)
		}
		if strings.TrimSpace(p.NodeKey) == "" {
			p.NodeKey = strings.TrimSpace(legacy.NodeKey)
		}
		return nil
	})
}

// submitParams 是 agent/submit 与 agent/submitPrompt 共用的 RPC 入参。
type submitParams struct {
	AgentID string   `json:"agent_id"`
	Prompt  string   `json:"prompt"`
	Images  []string `json:"images"`
	Files   []string `json:"files"`

	SelectedSkills       []string        `json:"selected_skills,omitempty"`
	ManualSkillSelection bool            `json:"manual_skill_selection,omitempty"`
	OutputSchema         json.RawMessage `json:"output_schema,omitempty"`

	legacyInput json.RawMessage
}

// submitPromptParams 保留旧 agent/submitPrompt 方法的入参别名。
type submitPromptParams = submitParams

// UnmarshalJSON 兼容 submit 旧 input 字段以及 selectedSkills/outputSchema camelCase 字段。
func (p *submitParams) UnmarshalJSON(data []byte) error {
	type current submitParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID              string          `json:"agentId"`
		Input                json.RawMessage `json:"input"`
		SelectedSkills       []string        `json:"selectedSkills"`
		ManualSkillSelection *bool           `json:"manualSkillSelection"`
		OutputSchema         json.RawMessage `json:"outputSchema"`
	}) error {
		*p = submitParams(*raw)
		if strings.TrimSpace(p.AgentID) == "" {
			p.AgentID = strings.TrimSpace(legacy.AgentID)
		}
		if len(p.SelectedSkills) == 0 && len(legacy.SelectedSkills) > 0 {
			p.SelectedSkills = append([]string(nil), legacy.SelectedSkills...)
		}
		if !p.ManualSkillSelection && legacy.ManualSkillSelection != nil {
			p.ManualSkillSelection = *legacy.ManualSkillSelection
		}
		if len(p.OutputSchema) == 0 {
			p.OutputSchema = append(json.RawMessage(nil), legacy.OutputSchema...)
		}
		p.legacyInput = append([]byte(nil), legacy.Input...)
		return nil
	})
}

// reportParams 是旧 orchestration/report RPC 的入参。
type reportParams struct {
	AgentID string `json:"agent_id"`
	Report  string `json:"report,omitempty"`
}

// UnmarshalJSON 兼容 report RPC 的 agent_id 和 agentId。
func (p *reportParams) UnmarshalJSON(data []byte) error {
	type current reportParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID string `json:"agentId"`
	}) error {
		*p = reportParams(*raw)
		if strings.TrimSpace(p.AgentID) == "" {
			p.AgentID = strings.TrimSpace(legacy.AgentID)
		}
		return nil
	})
}

// rememberReportRequestParams 是 report requester 记录 RPC 的 wire 入参。
type rememberReportRequestParams struct {
	AgentID     string `json:"worker_id"`
	RequesterID string `json:"sender_id"`
}

// UnmarshalJSON 兼容 worker/sender 命名和 agent/requester 命名。
func (p *rememberReportRequestParams) UnmarshalJSON(data []byte) error {
	type current rememberReportRequestParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID          string `json:"agentId"`
		RequesterID      string `json:"requesterId"`
		AgentIDSnake     string `json:"agent_id"`
		RequesterIDSnake string `json:"requester_id"`
	}) error {
		*p = rememberReportRequestParams(*raw)
		if strings.TrimSpace(p.AgentID) == "" {
			p.AgentID = shared.FirstTrimmed(legacy.AgentID, legacy.AgentIDSnake)
		}
		if strings.TrimSpace(p.RequesterID) == "" {
			p.RequesterID = shared.FirstTrimmed(legacy.RequesterID, legacy.RequesterIDSnake)
		}
		return nil
	})
}

// reportEventParams 是 provider/hook report event RPC 的 wire 入参。
type reportEventParams struct {
	AgentID   string          `json:"agent_id"`
	Report    string          `json:"report,omitempty"`
	EventType string          `json:"event_type,omitempty"`
	EventData json.RawMessage `json:"event_data,omitempty"`
}

// UnmarshalJSON 兼容 report event 的 snake_case 与 camelCase 字段名。
func (p *reportEventParams) UnmarshalJSON(data []byte) error {
	type current reportEventParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID   string          `json:"agentId"`
		EventType string          `json:"eventType"`
		EventData json.RawMessage `json:"eventData"`
	}) error {
		*p = reportEventParams(*raw)
		if strings.TrimSpace(p.AgentID) == "" {
			p.AgentID = strings.TrimSpace(legacy.AgentID)
		}
		if strings.TrimSpace(p.EventType) == "" {
			p.EventType = strings.TrimSpace(legacy.EventType)
		}
		if len(p.EventData) == 0 {
			p.EventData = append([]byte(nil), legacy.EventData...)
		}
		return nil
	})
}
