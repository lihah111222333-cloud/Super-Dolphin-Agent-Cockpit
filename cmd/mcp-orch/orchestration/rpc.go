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

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type successResponse struct {
	Success bool `json:"success"`
}

type runtimeReportParams struct {
	AgentID  string `json:"agent_id"`
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

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

// ProvideRPCFacade returns the orchestration subpackage's RPC handler
// bundle to the cmd/mcp-orch root assembly. It is consumed exclusively
// through the fx `group:"rpc_handlers"` hookup (see buildOrchestrationOptions
// in cmd/mcp-orch/fx.go) and is not a generic subpackage-to-subpackage
// protocol shell.
//
// P22 P4 S4c3: the previous export `NewOrchestrationHandlers` named this
// constructor after the orchestration protocol surface, which encouraged
// other subpackages to treat cmd/mcp-orch/orchestration as a reusable RPC
// shell (plan §117, §277 — handler.Map 协议壳 退回根入口 / 被 facade 替代).
// The name now explicitly frames this as a root-entry facade; the
// archtest in
// internal/archtest/orchestration_no_rpc_shell_export_guard_test.go
// locks the old name out so it cannot re-surface.
func ProvideRPCFacade(svc Service) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"agent/launch": rpc.StrictHandler(func(ctx context.Context, p launchParams) (any, error) {
			return nil, svc.LaunchAgent(ctx, launchRequestFromParams(p))
		}),
		"agent/submit": rpc.StrictHandler(func(ctx context.Context, p submitParams) (any, error) {
			req, err := submissionFromParams(ctx, svc, p)
			if err != nil {
				return nil, err
			}
			if err := svc.SubmitTurn(ctx, req); err != nil {
				return nil, err
			}
			return successResponse{Success: true}, nil
		}),
		"agent/submitPrompt": rpc.StrictHandler(func(ctx context.Context, p submitPromptParams) (any, error) {
			req, err := submissionFromParams(ctx, svc, submitParams(p))
			if err != nil {
				return nil, err
			}
			if err := svc.SubmitTurn(ctx, req); err != nil {
				return nil, err
			}
			return successResponse{Success: true}, nil
		}),
		"agent/stop": rpc.StrictHandler(func(ctx context.Context, p agentIDParams) (any, error) {
			return nil, svc.StopAgent(ctx, p.AgentID)
		}),
		"agent/list": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.ListAgents(ctx)
		}),
		"agent/snapshot": rpc.StrictHandler(func(ctx context.Context, p agentIDParams) (any, error) {
			return svc.Snapshot(ctx, p.AgentID)
		}),
		"orchestration/reportRuntime": rpc.StrictHandler(func(ctx context.Context, p runtimeReportParams) (any, error) {
			if err := svc.UpdateRuntime(ctx, runtimeReportFromParams(p)); err != nil {
				return nil, err
			}
			return successResponse{Success: true}, nil
		}),
		"agent/getState": rpc.StrictHandler(func(ctx context.Context, p agentIDParams) (any, error) {
			return svc.GetState(ctx, p.AgentID)
		}),
		"agent/getReport": rpc.StrictHandler(func(ctx context.Context, p agentIDParams) (any, error) {
			return svc.GetReport(ctx, p.AgentID)
		}),
		ReportMethodRememberReportRequest: rpc.StrictHandler(func(ctx context.Context, p rememberReportRequestParams) (any, error) {
			return svc.RememberReportRequest(ctx, rememberReportRequestFromParams(p))
		}),
		ReportMethodReportEvent: rpc.StrictHandler(func(ctx context.Context, p reportEventParams) (any, error) {
			return svc.HandleReportEvent(ctx, reportEventFromParams(p))
		}),
		"task/dag/create": rpc.StrictHandler(func(ctx context.Context, p createDAGParams) (any, error) {
			return svc.CreateDAG(ctx, createDAGRequestFromParams(p))
		}),
		"task/dag/get": rpc.StrictHandler(func(ctx context.Context, p dagKeyParams) (any, error) {
			return svc.GetDAG(ctx, p.DagKey)
		}),
		"task/dag/list": rpc.StrictHandler(func(ctx context.Context, p listDAGsParams) (any, error) {
			return svc.ListDAGs(ctx, listDAGsFilterFromParams(p))
		}),
		"task/node/update": rpc.StrictHandler(func(ctx context.Context, p updateNodeParams) (any, error) {
			return svc.UpdateNodeStatus(ctx, updateNodeRequestFromParams(p))
		}),
		"orchestration/report": rpc.StrictHandler(func(ctx context.Context, p reportParams) (any, error) {
			return svc.GetReport(ctx, p.AgentID)
		}),
	}}
}

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

func submissionFromParams(ctx context.Context, svc Service, p submitParams) (TurnSubmission, error) {
	agentID := strings.TrimSpace(p.AgentID)
	items, err := inputItemsFromSubmitParams(p)
	if err != nil {
		return TurnSubmission{}, err
	}
	return TurnSubmission{
		AgentID:              agentID,
		ThreadID:             submissionThreadID(ctx, svc, agentID),
		Inputs:               items,
		SelectedSkills:       append([]string(nil), p.SelectedSkills...),
		ManualSkillSelection: p.ManualSkillSelection,
		OutputSchema:         append(json.RawMessage(nil), p.OutputSchema...),
	}, nil
}

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

func submissionThreadID(ctx context.Context, svc Service, agentID string) string {
	snapshot, err := svc.Snapshot(ctx, agentID)
	if err == nil && strings.TrimSpace(snapshot.ThreadID) != "" {
		return snapshot.ThreadID
	}
	return strings.TrimSpace(agentID)
}

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

func rememberReportRequestFromParams(p rememberReportRequestParams) RememberReportRequest {
	return RememberReportRequest{
		AgentID:     p.AgentID,
		RequesterID: p.RequesterID,
	}
}

func reportEventFromParams(p reportEventParams) ReportEvent {
	return ReportEvent{
		AgentID:   p.AgentID,
		Report:    p.Report,
		EventType: p.EventType,
		EventData: append(json.RawMessage(nil), p.EventData...),
	}
}

func runtimeReportFromParams(p runtimeReportParams) RuntimeReport {
	return RuntimeReport{
		AgentID:  p.AgentID,
		Port:     p.Port,
		Provider: p.Provider,
	}
}

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

func listDAGsFilterFromParams(p listDAGsParams) ListDAGsFilter {
	return ListDAGsFilter{Status: p.Status, Keyword: p.Keyword, Limit: p.Limit}
}

func updateNodeRequestFromParams(p updateNodeParams) UpdateNodeStatusRequest {
	return UpdateNodeStatusRequest{
		DagKey:  p.DagKey,
		NodeKey: p.NodeKey,
		RunID:   p.RunID,
		Status:  p.Status,
		Result:  append(json.RawMessage(nil), p.Result...),
	}
}
