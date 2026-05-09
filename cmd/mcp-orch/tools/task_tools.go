package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type CreateDAGInput struct {
	AgentID     string               `json:"agent_id"`
	DagKey      string               `json:"dag_key"`
	Title       string               `json:"title"`
	Description string               `json:"description,omitempty"`
	Schedule    DAGScheduleInput     `json:"schedule"`
	Nodes       []CreateDAGNodeInput `json:"nodes,omitempty"`
}

type DAGScheduleInput struct {
	Trigger           string `json:"trigger,omitempty"`
	DefaultRetry      int    `json:"default_retry,omitempty"`
	DefaultTimeoutSec int    `json:"default_timeout_sec,omitempty"`
	FailFast          bool   `json:"fail_fast,omitempty"`
	MaxConcurrency    int    `json:"max_concurrency,omitempty"`
	QueuePolicy       string `json:"queue_policy,omitempty"`
}

type CreateDAGNodeInput struct {
	NodeKey    string             `json:"node_key"`
	Title      string             `json:"title"`
	NodeType   string             `json:"node_type,omitempty"`
	AssignedTo string             `json:"assigned_to,omitempty"`
	DependsOn  []string           `json:"depends_on,omitempty"`
	CommandRef string             `json:"command_ref,omitempty"`
	Execution  *DAGExecutionInput `json:"execution,omitempty"`
}

type DAGExecutionInput struct {
	OnFailure  string `json:"on_failure,omitempty"`
	Pool       string `json:"pool,omitempty"`
	Priority   int    `json:"priority,omitempty"`
	Retry      int    `json:"retry,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

type DAGKeyInput struct {
	DagKey string `json:"dag_key"`
}

type UpdateNodeInput struct {
	DagKey  string `json:"dag_key"`
	NodeKey string `json:"node_key"`
	Status  string `json:"status"`
	Result  string `json:"result,omitempty"`
}

func HandleCreateDAG(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in CreateDAGInput) (any, error) {
		req, err := createDAGRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		return svc.CreateDAG(ctx, req)
	})
}

func HandleGetDAG(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in DAGKeyInput) (any, error) {
		dagKey, err := requireTrimmed(in.DagKey, "dag_key")
		if err != nil {
			return nil, err
		}
		return svc.GetDAG(ctx, dagKey)
	})
}

func HandleUpdateNode(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in UpdateNodeInput) (any, error) {
		req, err := updateNodeRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		return svc.UpdateNodeStatus(ctx, req)
	})
}

func taskToolDefinitions(svc contract.OrchestrationService) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("task_create_dag", "Create or upsert a DAG and its nodes in the orchestration store.", createDAGSchema(), HandleCreateDAG(svc)),
		defineTool("task_get_dag", "Fetch a DAG and all of its nodes.", ObjectSchema(map[string]Schema{
			"dag_key": StringSchema("Unique DAG key."),
		}, "dag_key"), HandleGetDAG(svc)),
		defineTool("task_update_node", "Update the runtime status for a DAG node.", ObjectSchema(map[string]Schema{
			"dag_key":  StringSchema("DAG key."),
			"node_key": StringSchema("Node key within the DAG."),
			"status":   EnumStringSchema("New node status.", "pending", "running", "done", "failed"),
			"result":   StringSchema("Optional result summary."),
		}, "dag_key", "node_key", "status"), HandleUpdateNode(svc)),
	)
}

func createDAGSchema() Schema {
	return ObjectSchema(map[string]Schema{
		"agent_id":    StringSchema("Creator orchestration agent ID."),
		"dag_key":     StringSchema("Unique DAG key."),
		"title":       StringSchema("DAG title."),
		"description": StringSchema("Optional DAG description."),
		"schedule": ObjectSchema(map[string]Schema{
			"trigger":             StringSchema("Start trigger."),
			"default_retry":       IntegerSchema("Default retry count for nodes."),
			"default_timeout_sec": IntegerSchema("Default node timeout in seconds."),
			"fail_fast":           BooleanSchema("Stop scheduling new nodes on first failure."),
			"max_concurrency":     IntegerSchema("Maximum parallel runnable nodes."),
			"queue_policy":        StringSchema("Ready-queue policy."),
		}),
		"nodes": ArraySchema(ObjectSchema(map[string]Schema{
			"node_key":    StringSchema("Unique node key within the DAG."),
			"title":       StringSchema("Node title."),
			"node_type":   StringSchema("Optional node type."),
			"assigned_to": StringSchema("Optional assignee."),
			"depends_on":  ArraySchema(StringSchema("Dependency node key."), "Node dependency keys."),
			"command_ref": StringSchema("Optional command card key."),
			"execution": ObjectSchema(map[string]Schema{
				"on_failure":  StringSchema("Failure policy override."),
				"pool":        StringSchema("Execution pool name."),
				"priority":    IntegerSchema("Queue priority."),
				"retry":       IntegerSchema("Retry override."),
				"timeout_sec": IntegerSchema("Timeout override in seconds."),
			}),
		}, "node_key", "title"), "Optional DAG nodes."),
	}, "agent_id", "dag_key", "title", "schedule")
}

func createDAGRequestFromInput(in CreateDAGInput) (contract.CreateDAGRequest, error) {
	// Preserve schedule in DAG metadata until the service contract grows a
	// first-class schedule field.
	agentID, err := requireTrimmed(in.AgentID, "agent_id")
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	dagKey, err := requireTrimmed(in.DagKey, "dag_key")
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	title, err := requireTrimmed(in.Title, "title")
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	metadata, err := encodeJSONRaw(createDAGMetadata(in.Schedule))
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	nodes, err := createDAGNodesFromInput(in.Nodes)
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	return contract.CreateDAGRequest{
		DagKey:      dagKey,
		Title:       title,
		Description: strings.TrimSpace(in.Description),
		CreatedBy:   agentID,
		Metadata:    metadata,
		Nodes:       nodes,
	}, nil
}

func createDAGNodesFromInput(nodes []CreateDAGNodeInput) ([]contract.CreateDAGNodeRequest, error) {
	mapped := make([]contract.CreateDAGNodeRequest, 0, len(nodes))
	for i, node := range nodes {
		nodeKey, err := requireTrimmed(node.NodeKey, fmt.Sprintf("nodes[%d].node_key", i))
		if err != nil {
			return nil, err
		}
		title, err := requireTrimmed(node.Title, fmt.Sprintf("nodes[%d].title", i))
		if err != nil {
			return nil, err
		}
		// Preserve node-level execution overrides in Config for the same reason.
		config, err := encodeJSONRaw(nodeConfig(node.Execution))
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, contract.CreateDAGNodeRequest{
			NodeKey:    nodeKey,
			Title:      title,
			NodeType:   strings.TrimSpace(node.NodeType),
			AssignedTo: strings.TrimSpace(node.AssignedTo),
			DependsOn:  append([]string(nil), node.DependsOn...),
			CommandRef: strings.TrimSpace(node.CommandRef),
			Config:     config,
		})
	}
	return mapped, nil
}

func updateNodeRequestFromInput(in UpdateNodeInput) (contract.UpdateNodeStatusRequest, error) {
	dagKey, err := requireTrimmed(in.DagKey, "dag_key")
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	nodeKey, err := requireTrimmed(in.NodeKey, "node_key")
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	status, err := requireTrimmed(in.Status, "status")
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	result, err := encodeOptionalString(strings.TrimSpace(in.Result))
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	return contract.UpdateNodeStatusRequest{
		DagKey:  dagKey,
		NodeKey: nodeKey,
		Status:  status,
		Result:  result,
	}, nil
}

// createDAGMetadata 把 schedule 字段编码为 DAG metadata JSON 子树。
// 旧版 metadata 字段 (S15.1 删除 / migrations/0075_dag_v2_compat.sql 迁移) 不再处理：
// 数据库老行已一次性映射到 trigger 一等字段，tools 入参 schema 不再接受，
// 调用方如果传入会被忽略（向后兼容）。
func createDAGMetadata(schedule DAGScheduleInput) map[string]any {
	return map[string]any{"schedule": scheduleMap(schedule)}
}

func nodeConfig(execution *DAGExecutionInput) map[string]any {
	if execution == nil {
		return nil
	}
	return map[string]any{"execution": executionMap(*execution)}
}

func scheduleMap(in DAGScheduleInput) map[string]any {
	payload := make(map[string]any)
	if in.Trigger != "" {
		payload["trigger"] = strings.TrimSpace(in.Trigger)
	}
	if in.DefaultRetry != 0 {
		payload["default_retry"] = in.DefaultRetry
	}
	if in.DefaultTimeoutSec != 0 {
		payload["default_timeout_sec"] = in.DefaultTimeoutSec
	}
	if in.FailFast {
		payload["fail_fast"] = true
	}
	if in.MaxConcurrency != 0 {
		payload["max_concurrency"] = in.MaxConcurrency
	}
	if in.QueuePolicy != "" {
		payload["queue_policy"] = strings.TrimSpace(in.QueuePolicy)
	}
	return payload
}

func executionMap(in DAGExecutionInput) map[string]any {
	payload := make(map[string]any)
	if in.OnFailure != "" {
		payload["on_failure"] = strings.TrimSpace(in.OnFailure)
	}
	if in.Pool != "" {
		payload["pool"] = strings.TrimSpace(in.Pool)
	}
	if in.Priority != 0 {
		payload["priority"] = in.Priority
	}
	if in.Retry != 0 {
		payload["retry"] = in.Retry
	}
	if in.TimeoutSec != 0 {
		payload["timeout_sec"] = in.TimeoutSec
	}
	return payload
}

func encodeJSONRaw(value any) (json.RawMessage, error) {
	return marshalRawJSON(value, rawJSONOptions{})
}

func encodeOptionalString(value string) (json.RawMessage, error) {
	return marshalRawJSON(value, rawJSONOptions{OmitEmptyString: true})
}
