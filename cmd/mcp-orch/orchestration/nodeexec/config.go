package nodeexec

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// AutomationKindCommandCard 是当前唯一实装的 automation.kind 取值（ADR-007 §6 登记表）。
// 其余 kind（webhook / shell / mcp_call ...）字段位先行，后续按 ADR-007 §4 渐进开通。
const AutomationKindCommandCard = "command_card"

// ErrUnsupportedAutomationKind 在 ParseAutomationConfig 收到非 command_card 的 kind 时返回；errors.Is 可用。
var ErrUnsupportedAutomationKind = errors.New("nodeexec: unsupported automation.kind")

// node.config 的 typed schema —— 蓝图 v2 §7 + 实施计划 S5.1 + S5.2。
// 三种 node_type (agent/automation/hybrid) 各有一份完整 config，共享 inputs/outputs。
// 骨架阶段：仅定义 struct + ParseNodeConfig；F1.x/F2.x/F3.x 真实使用。

// =====================================================
// 共享子配置（所有 node_type 通用）
// =====================================================

// InputsConfig 是节点输入配置。
type InputsConfig struct {
	// FromNodes 列出要注入 prev nodes result 的 node_key（同 DAG 内）。
	FromNodes []string `json:"from_nodes,omitempty"`
	// FromSharedfiles 列出要读的 sharedfile path（受 mcp-orch 白名单约束）。
	FromSharedfiles []string `json:"from_sharedfiles,omitempty"`
	// Summarization 是输入摘要/裁剪策略字段位（H7 真实实现）。
	Summarization *SummarizationConfig `json:"summarization,omitempty"`
}

// SummarizationConfig 是输入裁剪/摘要策略（蓝图 v2 §10 补丁 11）。
type SummarizationConfig struct {
	// Strategy: none (默认不动) | last_n (保留最后 N 条) | llm_summary (LLM 摘要).
	Strategy  string `json:"strategy,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

// OutputsConfig 是节点输出配置。
type OutputsConfig struct {
	// ToSharedfile 把节点输出写入 sharedfile（含 lock_mode 防并发冲突）。
	ToSharedfile *SharedfileTarget `json:"to_sharedfile,omitempty"`
	// ToNodeResult 是否把输出写入 task_dag_nodes.result JSONB。
	// **仅适合 < 4KB 摘要**（蓝图 v2 §5 决策）；大输出走 ToSharedfile。
	ToNodeResult bool `json:"to_node_result,omitempty"`
	// Schema 是可选 JSON Schema：节点输出不符则归类为 validation failure。
	Schema json.RawMessage `json:"schema,omitempty"`
}

// SharedfileTarget 是一个 sharedfile 输出位置 + 写入策略（蓝图 v2 §10 补丁 14）。
type SharedfileTarget struct {
	Path string `json:"path"`
	// LockMode: exclusive (独占) | append (追加合并) | shared (并发只读).
	LockMode string `json:"lock_mode,omitempty"`
}

// OnFailureConfig 是节点失败处理策略（蓝图 v2 §7 + §10 补丁 8）。
// 解码与 by_class lookup 的中间函数在 nodeexec/on_failure.go (S5.3)。
type OnFailureConfig struct {
	// Default 是 by_class 未命中时的兜底策略。
	Default OnFailureStrategy `json:"default,omitempty"`
	// ByClass 按 FailureClass 分发不同策略（智能重试核心）。
	ByClass map[FailureClass]OnFailureStrategy `json:"by_class,omitempty"`
	// MaxAttempts 是包含首发的总尝试次数。
	MaxAttempts int `json:"max_attempts,omitempty"`
	// EscalationChain 是 escalate_model 策略的 model 升级链（如 ["sonnet", "opus"]）。
	EscalationChain []string `json:"escalation_chain,omitempty"`
}

// =====================================================
// 三种 exec 配置（按 node_type 区分）
// =====================================================

// AgentExecConfig 是 node_type=agent 节点的 exec 块。
// 字段对齐 orchestration_launch_agent 入参（F1.1 解码后映射）。
type AgentExecConfig struct {
	Provider string `json:"provider,omitempty"` // claude | codex
	Model    string `json:"model,omitempty"`    // opus | sonnet | ...
	// Codex identity maps to thread/start config.codexHome/codexInstanceKey/codexModelProvider.
	CodexHome          string           `json:"codex_home,omitempty"`
	CodexInstanceKey   string           `json:"codex_instance_key,omitempty"`
	CodexModelProvider string           `json:"codex_model_provider,omitempty"`
	AgentKey           string           `json:"agent_key,omitempty"`     // 查 prompt_templates 表
	PromptKey          string           `json:"prompt_key,omitempty"`    // 精确 prompt_templates.prompt_key
	CWD                string           `json:"cwd,omitempty"`           // explicit absolute launch cwd
	Effort             string           `json:"effort,omitempty"`        // xhigh | high | medium | low
	Language           string           `json:"language,omitempty"`      // zh | en
	Isolation          string           `json:"isolation,omitempty"`     // shared (默认) | worktree
	AllowedTools       []string         `json:"allowed_tools,omitempty"` // 白名单
	DisabledTools      []string         `json:"disabled_tools,omitempty"`
	BudgetTokens       int64            `json:"budget_tokens,omitempty"` // 字段位，骨架不 enforce
	OnFailure          *OnFailureConfig `json:"on_failure,omitempty"`
}

// AutomationExecConfig 是 node_type=automation 节点的 exec 块。
type AutomationExecConfig struct {
	// Kind 为自动化执行通道，当前仅 "command_card" 实装；其余 kind 留位 ADR-007 渐进开通。空字符串视为 command_card（向下兼容）。详 ADR-007。
	Kind         string           `json:"kind,omitempty"`
	CommandRef   string           `json:"command_ref"`    // 查 command_cards 表
	Args         json.RawMessage  `json:"args,omitempty"` // 命令参数（结构由 command 决定）
	BudgetTokens int64            `json:"budget_tokens,omitempty"`
	OnFailure    *OnFailureConfig `json:"on_failure,omitempty"`
}

// HybridExecConfig 是 node_type=hybrid 节点的 exec 块（先 automation 后 agent verifier）。
type HybridExecConfig struct {
	Automation *AutomationExecConfig `json:"automation,omitempty"`
	Verifier   *AgentExecConfig      `json:"verifier,omitempty"`
}

// =====================================================
// 顶层 config（三种 node_type 各一份）
// =====================================================

// AgentNodeConfig 是 node_type=agent 节点的完整 config。
type AgentNodeConfig struct {
	Exec    AgentExecConfig `json:"exec"`
	Inputs  InputsConfig    `json:"inputs,omitempty"`
	Outputs OutputsConfig   `json:"outputs,omitempty"`
	// FirstTurn 可选：覆盖 agent_key 的默认提示词（一次性指令）。
	FirstTurn string `json:"first_turn,omitempty"`
}

// AutomationNodeConfig 是 node_type=automation 节点的完整 config。
type AutomationNodeConfig struct {
	Exec    AutomationExecConfig `json:"exec"`
	Inputs  InputsConfig         `json:"inputs,omitempty"`
	Outputs OutputsConfig        `json:"outputs,omitempty"`
}

// HybridNodeConfig 是 node_type=hybrid 节点的完整 config。
type HybridNodeConfig struct {
	Exec    HybridExecConfig `json:"exec"`
	Inputs  InputsConfig     `json:"inputs,omitempty"`
	Outputs OutputsConfig    `json:"outputs,omitempty"`
}

// =====================================================
// 解码器（S5.2）
// =====================================================

// ErrUnknownNodeType 在 ParseNodeConfig 收到未知 node_type 时返回；errors.Is 可用。
var ErrUnknownNodeType = fmt.Errorf("nodeexec: unknown node_type")

// ParsedNodeConfig 是 ParseNodeConfig 的返回值。
// 按 NodeType 只有一个 *Config 字段非 nil；其他字段为 nil。
type ParsedNodeConfig struct {
	NodeType   string
	Agent      *AgentNodeConfig
	Automation *AutomationNodeConfig
	Hybrid     *HybridNodeConfig
}

// ParseNodeConfig 按 node_type 把 raw json 解码到对应的 typed config。
// 空 raw 返回 zero-value config（不报错），让旧 DAG 兼容。
// 未知 node_type 返回 ErrUnknownNodeType。
func ParseNodeConfig(nodeType string, raw json.RawMessage) (*ParsedNodeConfig, error) {
	switch nodeType {
	case "agent":
		cfg, err := ParseAgentConfig(raw)
		if err != nil {
			return nil, err
		}
		return &ParsedNodeConfig{NodeType: nodeType, Agent: cfg}, nil
	case "automation":
		cfg, err := ParseAutomationConfig(raw)
		if err != nil {
			return nil, err
		}
		return &ParsedNodeConfig{NodeType: nodeType, Automation: cfg}, nil
	case "hybrid":
		cfg, err := ParseHybridConfig(raw)
		if err != nil {
			return nil, err
		}
		return &ParsedNodeConfig{NodeType: nodeType, Hybrid: cfg}, nil
	default:
		return nil, fmt.Errorf("%w: %q (allowed: agent | automation | hybrid)", ErrUnknownNodeType, nodeType)
	}
}

func ValidateLaunchCWDForNodeConfig(nodeType string, raw json.RawMessage) (string, error) {
	parsed, err := ParseNodeConfig(nodeType, raw)
	if err != nil {
		return "", fmt.Errorf("%w: parse node config for launch cwd: %v", contract.ErrLaunchCWDRequired, err)
	}
	cwd := launchCWDFromParsedConfig(parsed)
	if err := contract.ValidateLaunchCWD(cwd, ""); err != nil {
		return "", err
	}
	return cwd, nil
}

func launchCWDFromParsedConfig(parsed *ParsedNodeConfig) string {
	switch {
	case parsed == nil:
		return ""
	case parsed.Agent != nil:
		return parsed.Agent.Exec.CWD
	case parsed.Hybrid != nil && parsed.Hybrid.Exec.Verifier != nil:
		return parsed.Hybrid.Exec.Verifier.CWD
	default:
		return ""
	}
}

// ParseAgentConfig 解码 node_type=agent 的完整 config。
// 空 raw（未配置）返回 zero-value config，不报错。
func ParseAgentConfig(raw json.RawMessage) (*AgentNodeConfig, error) {
	var cfg AgentNodeConfig
	if len(raw) == 0 {
		return &cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("nodeexec: parse agent config: %w", err)
	}
	return &cfg, nil
}

// ParseAutomationConfig 解码 node_type=automation 的完整 config。
// 空 kind 默认填 AutomationKindCommandCard（向下兼容）；非 command_card 的 kind 返回 ErrUnsupportedAutomationKind。
func ParseAutomationConfig(raw json.RawMessage) (*AutomationNodeConfig, error) {
	var cfg AutomationNodeConfig
	if len(raw) == 0 {
		return &cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("nodeexec: parse automation config: %w", err)
	}
	switch cfg.Exec.Kind {
	case "":
		cfg.Exec.Kind = AutomationKindCommandCard
	case AutomationKindCommandCard:
		// ok
	default:
		return nil, fmt.Errorf("%w: %q (allowed: command_card)", ErrUnsupportedAutomationKind, cfg.Exec.Kind)
	}
	return &cfg, nil
}

// ParseHybridConfig 解码 node_type=hybrid 的完整 config。
func ParseHybridConfig(raw json.RawMessage) (*HybridNodeConfig, error) {
	var cfg HybridNodeConfig
	if len(raw) == 0 {
		return &cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("nodeexec: parse hybrid config: %w", err)
	}
	return &cfg, nil
}
