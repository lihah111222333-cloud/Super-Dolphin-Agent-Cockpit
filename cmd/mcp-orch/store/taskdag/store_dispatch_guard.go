package taskdag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const dispatchBlockedEventKind = "node_dispatch_blocked"
const autoAutomationKindCommandCard = "command_card"

// dispatchBlockedEvent 是写入 task_dag_runs.events 的派发阻断事件 JSON 载荷。
type dispatchBlockedEvent struct {
	Kind    string `json:"kind"`
	NodeKey string `json:"node_key"`
	RunID   int64  `json:"run_id"`
	Scope   string `json:"scope"`
	Reason  string `json:"reason"`
	TS      string `json:"ts"`
}

// autoAgentConfig 是 agent 节点 config 的顶层结构。
type autoAgentConfig struct {
	Exec autoAgentExec `json:"exec"`
}

// autoAgentExec 是 agent exec 字段，agent_key/prompt_key 二选一，cwd 可选。
type autoAgentExec struct {
	AgentKey  string `json:"agent_key,omitempty"`
	PromptKey string `json:"prompt_key,omitempty"`
	CWD       string `json:"cwd,omitempty"`
}

// autoAutomationConfig 是 automation 节点 config 的顶层结构。
type autoAutomationConfig struct {
	Exec autoAutomationExec `json:"exec"`
}

// autoAutomationExec 是 automation exec 字段，kind 默认为 command_card。
type autoAutomationExec struct {
	Kind       string `json:"kind,omitempty"`
	CommandRef string `json:"command_ref,omitempty"`
}

// autoHybridConfig 是 hybrid 节点 config 的顶层结构，automation 和 verifier 至少有其一。
type autoHybridConfig struct {
	Exec struct {
		Automation *autoAutomationExec `json:"automation,omitempty"`
		Verifier   *autoAgentExec      `json:"verifier,omitempty"`
	} `json:"exec"`
}

// appendDispatchBlockedEvent 向 run events 追加 node_dispatch_blocked 事件，记录自动派发失败原因。
// 节点保持 ready，不修改状态；事件仅用于审计和调试。
func appendDispatchBlockedEvent(ctx context.Context, txStore *store, node *Node, runID int64, scope string, cause error) error {
	reason := strings.TrimSpace(fmt.Sprint(cause))
	payload, err := json.Marshal(dispatchBlockedEvent{
		Kind:    dispatchBlockedEventKind,
		NodeKey: node.NodeKey,
		RunID:   runID,
		Scope:   scope,
		Reason:  reason,
		TS:      time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("marshal dispatch blocked event for %s/%s: %w", node.DagKey, node.NodeKey, err)
	}
	if _, err := txStore.appendTaskDagRunEvent(ctx, node.DagKey, runID, payload); err != nil {
		return fmt.Errorf("append dispatch blocked event for %s/%s run_id=%d: %w", node.DagKey, node.NodeKey, runID, err)
	}
	return nil
}

// validateAutomaticDispatchConfig 根据 node_type 分流到对应配置校验函数，不支持的类型直接报错。
func validateAutomaticDispatchConfig(node *Node) error {
	switch automaticDispatchNodeType(node.NodeType) {
	case "agent":
		return validateAutoAgentConfig(node.Config, "node.config")
	case "automation":
		return validateAutoAutomationConfig(node.Config, "node.config")
	case "hybrid":
		return validateAutoHybridConfig(node.Config)
	default:
		return fmt.Errorf("unsupported node_type %q for automatic dispatch", strings.TrimSpace(node.NodeType))
	}
}

// automaticDispatchNodeType 规范化 node_type：空串默认视为 "agent"。
func automaticDispatchNodeType(raw string) string {
	nodeType := strings.TrimSpace(raw)
	if nodeType == "" {
		return "agent"
	}
	return nodeType
}

// validateAutoAgentConfig 解析并校验 agent 节点的 exec 配置。
func validateAutoAgentConfig(raw json.RawMessage, label string) error {
	var cfg autoAgentConfig
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parse %s for automatic dispatch: %w", label, err)
		}
	}
	return validateAutoAgentExec(cfg.Exec, label+".exec")
}

// validateAutoAgentExec 校验 agent exec 字段：agent_key 或 prompt_key 必须有其一，cwd 若非空需合法。
func validateAutoAgentExec(exec autoAgentExec, label string) error {
	if strings.TrimSpace(exec.AgentKey) == "" && strings.TrimSpace(exec.PromptKey) == "" {
		return fmt.Errorf("%s.agent_key or %s.prompt_key required", label, label)
	}
	if err := contract.ValidateLaunchCWD(exec.CWD, ""); err != nil {
		return fmt.Errorf("%s.cwd invalid: %w", label, err)
	}
	return nil
}

// validateAutoAutomationConfig 解析并校验 automation 节点的 exec 配置。
func validateAutoAutomationConfig(raw json.RawMessage, label string) error {
	var cfg autoAutomationConfig
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parse %s for automatic dispatch: %w", label, err)
		}
	}
	return validateAutoAutomationExec(cfg.Exec, label+".exec")
}

// validateAutoAutomationExec 校验 automation exec 字段：kind 只允许空或 command_card，command_ref 必填。
func validateAutoAutomationExec(exec autoAutomationExec, label string) error {
	switch kind := strings.TrimSpace(exec.Kind); kind {
	case "", autoAutomationKindCommandCard:
	default:
		return fmt.Errorf("%s.kind %q is unsupported for automatic dispatch", label, kind)
	}
	if strings.TrimSpace(exec.CommandRef) == "" {
		return fmt.Errorf("%s.command_ref required", label)
	}
	return nil
}

// validateAutoHybridConfig 校验 hybrid 节点至少包含 automation 或 verifier 配置。
func validateAutoHybridConfig(raw json.RawMessage) error {
	var cfg autoHybridConfig
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parse node.config for automatic dispatch: %w", err)
		}
	}
	if cfg.Exec.Automation == nil && cfg.Exec.Verifier == nil {
		return fmt.Errorf("node.config.exec.automation or node.config.exec.verifier required")
	}
	if cfg.Exec.Automation != nil {
		if err := validateAutoAutomationExec(*cfg.Exec.Automation, "node.config.exec.automation"); err != nil {
			return err
		}
	}
	if cfg.Exec.Verifier != nil {
		if err := validateAutoAgentExec(*cfg.Exec.Verifier, "node.config.exec.verifier"); err != nil {
			return err
		}
	}
	return nil
}
