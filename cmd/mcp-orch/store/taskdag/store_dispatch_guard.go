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

type dispatchBlockedEvent struct {
	Kind    string `json:"kind"`
	NodeKey string `json:"node_key"`
	RunID   int64  `json:"run_id"`
	Scope   string `json:"scope"`
	Reason  string `json:"reason"`
	TS      string `json:"ts"`
}

type autoAgentConfig struct {
	Exec autoAgentExec `json:"exec"`
}

type autoAgentExec struct {
	AgentKey  string `json:"agent_key,omitempty"`
	PromptKey string `json:"prompt_key,omitempty"`
	CWD       string `json:"cwd,omitempty"`
}

type autoAutomationConfig struct {
	Exec autoAutomationExec `json:"exec"`
}

type autoAutomationExec struct {
	Kind       string `json:"kind,omitempty"`
	CommandRef string `json:"command_ref,omitempty"`
}

type autoHybridConfig struct {
	Exec struct {
		Automation *autoAutomationExec `json:"automation,omitempty"`
		Verifier   *autoAgentExec      `json:"verifier,omitempty"`
	} `json:"exec"`
}

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

func automaticDispatchNodeType(raw string) string {
	nodeType := strings.TrimSpace(raw)
	if nodeType == "" {
		return "agent"
	}
	return nodeType
}

func validateAutoAgentConfig(raw json.RawMessage, label string) error {
	var cfg autoAgentConfig
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parse %s for automatic dispatch: %w", label, err)
		}
	}
	return validateAutoAgentExec(cfg.Exec, label+".exec")
}

func validateAutoAgentExec(exec autoAgentExec, label string) error {
	if strings.TrimSpace(exec.AgentKey) == "" && strings.TrimSpace(exec.PromptKey) == "" {
		return fmt.Errorf("%s.agent_key or %s.prompt_key required", label, label)
	}
	if err := contract.ValidateLaunchCWD(exec.CWD, ""); err != nil {
		return fmt.Errorf("%s.cwd invalid: %w", label, err)
	}
	return nil
}

func validateAutoAutomationConfig(raw json.RawMessage, label string) error {
	var cfg autoAutomationConfig
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parse %s for automatic dispatch: %w", label, err)
		}
	}
	return validateAutoAutomationExec(cfg.Exec, label+".exec")
}

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
