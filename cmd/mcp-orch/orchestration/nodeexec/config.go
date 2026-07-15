package nodeexec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	sharedfilepath "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilepath"
)

// AutomationKindCommandCard 是当前唯一实装的 automation.kind 取值。
// 其他 kind 保留在 schema 之外；解析期间会 fail-fast，避免把未知执行通道当命令卡运行。
const AutomationKindCommandCard = "command_card"

// ErrUnsupportedAutomationKind 在 ParseAutomationConfig 收到非 command_card 的 kind 时返回；errors.Is 可用。
var ErrUnsupportedAutomationKind = errors.New("nodeexec: unsupported automation.kind")

// 本文件定义 node.config 的 typed wire schema。
// agent、automation、hybrid 各有独立 exec 配置，并共享 inputs/outputs 约定。

// InputsConfig 是节点输入配置。
type InputsConfig struct {
	// FromNodes 列出要注入 prev nodes result 的 node_key（同 DAG 内）。
	FromNodes []string `json:"from_nodes,omitempty"`
	// FromSharedfiles 列出要读的 sharedfile path（受 mcp-orch 白名单约束）。
	FromSharedfiles []string `json:"from_sharedfiles,omitempty"`
	// Summarization 是输入摘要/裁剪策略配置；nil 表示不裁剪。
	Summarization *SummarizationConfig `json:"summarization,omitempty"`
}

// SummarizationConfig 描述输入裁剪/摘要策略。
type SummarizationConfig struct {
	// Strategy: none (默认不动) | last_n (保留最后 N 条) | llm_summary (LLM 摘要).
	Strategy  string `json:"strategy,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

// OutputsConfig 是节点输出配置。
type OutputsConfig struct {
	// ToSharedfile 把节点输出写入 sharedfile（含 lock_mode 防并发冲突）。
	ToSharedfile *SharedfileTarget `json:"to_sharedfile,omitempty"`
	// ToArtifact 把结构化 tool result 里的本地文件物化为 sharedfile。
	ToArtifact *ArtifactTarget `json:"to_artifact,omitempty"`
	// ToNodeResult 是否把输出写入 task_dag_nodes.result JSONB。
	// 仅适合小摘要；大输出必须走 ToSharedfile，避免节点结果列膨胀。
	ToNodeResult bool `json:"to_node_result,omitempty"`
	// NodeResultEnvelope 显式控制 sharedfile-only 输出是否写入轻量 node.result envelope。
	// nil/true = 写入 path/kind/dag/run/node；false = 用户明确禁用。
	NodeResultEnvelope *bool `json:"node_result_envelope,omitempty"`
	// Schema 是可选 JSON Schema：节点输出不符则归类为 validation failure。
	Schema json.RawMessage `json:"schema,omitempty"`
}

// SharedfileTarget 描述 sharedfile 输出位置和并发写策略。
type SharedfileTarget struct {
	Path string `json:"path"`
	// LockMode: exclusive (独占) | append (追加合并) | shared (并发只读).
	LockMode string `json:"lock_mode,omitempty"`
}

// ArtifactTarget 描述从结构化 tool result 中抽取本地文件并导入 sharedfile 的规则。
type ArtifactTarget struct {
	SourceTool         string   `json:"source_tool"`
	SourcePathField    string   `json:"source_path_field,omitempty"`
	SourceTextField    string   `json:"source_text_field,omitempty"`
	PathTemplate       string   `json:"path_template"`
	ContentType        string   `json:"content_type,omitempty"`
	AllowedExtensions  []string `json:"allowed_extensions,omitempty"`
	AllowedSourceRoots []string `json:"allowed_source_roots,omitempty"`
	MaxBytes           int64    `json:"max_bytes,omitempty"`
	Overwrite          string   `json:"overwrite,omitempty"`
}

// Validate 校验 outputs.to_artifact 的 fail-fast 边界。
// source path/text 二选一，target path 必须带 run 占位，避免跨 run 覆盖。
func (t *ArtifactTarget) Validate() error {
	if t == nil {
		return nil
	}
	if strings.TrimSpace(t.SourceTool) == "" {
		return errors.New("nodeexec: outputs.to_artifact.source_tool is required")
	}
	pathField := strings.TrimSpace(t.SourcePathField)
	textField := strings.TrimSpace(t.SourceTextField)
	switch {
	case pathField == "" && textField == "":
		return errors.New("nodeexec: outputs.to_artifact.source_path_field or source_text_field is required")
	case pathField != "" && textField != "":
		return errors.New("nodeexec: outputs.to_artifact.source_path_field and source_text_field are mutually exclusive")
	}
	pathTemplate := strings.TrimSpace(t.PathTemplate)
	if pathTemplate == "" {
		return errors.New("nodeexec: outputs.to_artifact.path_template is required")
	}
	if !strings.Contains(pathTemplate, "{{run_key}}") && !strings.Contains(pathTemplate, "{{run_id}}") {
		return errors.New("nodeexec: outputs.to_artifact.path_template must contain {{run_key}} or {{run_id}}")
	}
	return nil
}

func validateOutputsConfig(out OutputsConfig) error {
	if out.ToArtifact == nil {
		return nil
	}
	return out.ToArtifact.Validate()
}

// ArtifactImportPlan 描述 automation/agent 结构化结果导入 sharedfile 前的文件边界。
// 路径、扩展名、大小和覆盖策略都在 plan 中固定，执行层只能按计划导入。
type ArtifactImportPlan struct {
	SourcePath         string
	TargetPath         string
	ContentType        string
	AllowedExtensions  []string
	AllowedSourceRoots []string
	MaxBytes           int64
	Overwrite          string
}

// ArtifactTextPlan 描述由 agent 结构化正文生成文档 artifact 的最小输入。
type ArtifactTextPlan struct {
	SourceText  string
	TargetPath  string
	ContentType string
	MaxBytes    int64
	Overwrite   string
}

// BuildArtifactImportPlan 从结构化结果构建本地文件导入计划。
// 仅接受 source_path_field；正文生成类 artifact 必须走 BuildArtifactTextPlan。
func BuildArtifactImportPlan(target *ArtifactTarget, rawResult string, runID int64) (ArtifactImportPlan, error) {
	if target == nil {
		return ArtifactImportPlan{}, errors.New("outputs.to_artifact is required")
	}
	if strings.TrimSpace(target.SourceTextField) != "" {
		return ArtifactImportPlan{}, errors.New("source_path_field is required for local artifact import")
	}
	sourcePath, err := extractArtifactSourcePath(rawResult, target.SourceTool, target.SourcePathField)
	if err != nil {
		return ArtifactImportPlan{}, err
	}
	targetPath, err := renderArtifactTargetPath(target.PathTemplate, runID)
	if err != nil {
		return ArtifactImportPlan{}, fmt.Errorf("path_template: %w", err)
	}
	return ArtifactImportPlan{
		SourcePath:         sourcePath,
		TargetPath:         targetPath,
		ContentType:        target.ContentType,
		AllowedExtensions:  append([]string(nil), target.AllowedExtensions...),
		AllowedSourceRoots: append([]string(nil), target.AllowedSourceRoots...),
		MaxBytes:           target.MaxBytes,
		Overwrite:          target.Overwrite,
	}, nil
}

// BuildArtifactTextPlan 从结构化结果中提取正文，并渲染目标 artifact 路径。
func BuildArtifactTextPlan(target *ArtifactTarget, rawResult string, runID int64) (ArtifactTextPlan, error) {
	if target == nil {
		return ArtifactTextPlan{}, errors.New("outputs.to_artifact is required")
	}
	if strings.TrimSpace(target.SourcePathField) != "" {
		return ArtifactTextPlan{}, errors.New("source_text_field is required for generated document artifact")
	}
	sourceText, err := extractArtifactSourceText(rawResult, target.SourceTool, target.SourceTextField)
	if err != nil {
		return ArtifactTextPlan{}, err
	}
	targetPath, err := renderArtifactTargetPath(target.PathTemplate, runID)
	if err != nil {
		return ArtifactTextPlan{}, fmt.Errorf("path_template: %w", err)
	}
	return ArtifactTextPlan{
		SourceText:  sourceText,
		TargetPath:  targetPath,
		ContentType: target.ContentType,
		MaxBytes:    target.MaxBytes,
		Overwrite:   target.Overwrite,
	}, nil
}

func renderArtifactTargetPath(template string, runID int64) (string, error) {
	trimmed := strings.TrimSpace(template)
	if !strings.Contains(trimmed, "{{run_key}}") && !strings.Contains(trimmed, "{{run_id}}") {
		return "", errors.New("must contain {{run_key}} or {{run_id}}")
	}
	if runID <= 0 {
		return "", errors.New("run_id is required")
	}
	runIDText := strconv.FormatInt(runID, 10)
	rendered := strings.ReplaceAll(trimmed, "{{run_id}}", runIDText)
	rendered = strings.ReplaceAll(rendered, "{{run_key}}", "run-"+runIDText)
	return sharedfilepath.ValidateWritePath(rendered)
}

func extractArtifactSourcePath(rawResult, sourceTool, pathField string) (string, error) {
	trimmed := strings.TrimSpace(rawResult)
	if trimmed == "" {
		return "", errors.New("source path requires structured JSON result")
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", fmt.Errorf("source path requires structured JSON result: %w", err)
	}
	path, ok := artifactPathFromValue(payload, strings.TrimSpace(sourceTool), strings.TrimSpace(pathField), false)
	if !ok || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("source path field %q from tool %q not found in structured JSON result", pathField, sourceTool)
	}
	return strings.TrimSpace(path), nil
}

// extractArtifactSourceText 从结构化 JSON 结果中读取 artifact 正文。
// 缺字段时直接返回错误，避免生成空文档或把非目标工具输出误当正文。
func extractArtifactSourceText(rawResult, sourceTool, textField string) (string, error) {
	trimmed := strings.TrimSpace(rawResult)
	if trimmed == "" {
		return "", errors.New("source text requires structured JSON result")
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", fmt.Errorf("source text requires structured JSON result: %w", err)
	}
	text, ok := artifactTextFromValue(payload, strings.TrimSpace(sourceTool), strings.TrimSpace(textField), false)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("source text field %q from tool %q not found in structured JSON result", textField, sourceTool)
	}
	return strings.TrimSpace(text), nil
}

// artifactPathFromValue 在任意嵌套 JSON 值里递归查找 artifact 源路径。
// 数组分支要求后续对象带工具标识，避免不同工具的同名字段串线。
func artifactPathFromValue(value any, sourceTool, pathField string, requireTool bool) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return artifactPathFromObject(typed, sourceTool, pathField, requireTool)
	case []any:
		for _, item := range typed {
			if path, ok := artifactPathFromValue(item, sourceTool, pathField, true); ok {
				return path, true
			}
		}
	}
	return "", false
}

// artifactPathFromObject 从对象和常见 tool-result 容器中读取 artifact 源路径。
// requireTool 为 true 时必须先命中工具名，避免数组中其他工具的同名字段被误选。
func artifactPathFromObject(obj map[string]any, sourceTool, pathField string, requireTool bool) (string, bool) {
	if path, ok := artifactPathFromDirectObject(obj, sourceTool, pathField, requireTool); ok {
		return path, true
	}
	if nested, ok := obj[sourceTool].(map[string]any); ok {
		if path, ok := artifactPathFromObject(nested, sourceTool, pathField, false); ok {
			return path, true
		}
	}
	for _, key := range []string{"structuredContent", "structured_content", "result", "output", "payload"} {
		if nested, ok := obj[key].(map[string]any); ok {
			if path, ok := artifactPathFromObject(nested, sourceTool, pathField, false); ok {
				return path, true
			}
		}
	}
	for _, key := range []string{"tool_results", "toolResults", "items"} {
		if items, ok := obj[key].([]any); ok {
			if path, ok := artifactPathFromValue(items, sourceTool, pathField, true); ok {
				return path, true
			}
		}
	}
	return "", false
}

func artifactPathFromDirectObject(obj map[string]any, sourceTool, pathField string, requireTool bool) (string, bool) {
	if artifactObjectMatchesTool(obj, sourceTool, requireTool) {
		return artifactStringField(obj, pathField)
	}
	return "", false
}

func artifactObjectMatchesTool(obj map[string]any, sourceTool string, requireTool bool) bool {
	for _, key := range []string{"source_tool", "tool_name", "tool", "name"} {
		if got, ok := artifactStringField(obj, key); ok {
			return got == sourceTool
		}
	}
	return !requireTool
}

func artifactStringField(obj map[string]any, key string) (string, bool) {
	value, ok := obj[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", false
	}
	return strings.TrimSpace(text), true
}

// artifactTextFromValue 在任意嵌套 JSON 值里递归查找 artifact 正文。
// 它与路径解析保持同一套容器键，保证 source_path 和 source_text 的来源一致。
func artifactTextFromValue(value any, sourceTool, textField string, requireTool bool) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return artifactTextFromObject(typed, sourceTool, textField, requireTool)
	case []any:
		for _, item := range typed {
			if text, ok := artifactTextFromValue(item, sourceTool, textField, true); ok {
				return text, true
			}
		}
	}
	return "", false
}

// artifactTextFromObject 从对象及常见结构化结果容器中读取 artifact 正文。
// requireTool 为 true 时必须先命中工具名，防止数组中的其他工具结果被误选。
func artifactTextFromObject(obj map[string]any, sourceTool, textField string, requireTool bool) (string, bool) {
	if artifactObjectMatchesTool(obj, sourceTool, requireTool) {
		return artifactStringField(obj, textField)
	}
	if nested, ok := obj[sourceTool].(map[string]any); ok {
		if text, ok := artifactTextFromObject(nested, sourceTool, textField, false); ok {
			return text, true
		}
	}
	for _, key := range []string{"structuredContent", "structured_content", "result", "output", "payload"} {
		if nested, ok := obj[key].(map[string]any); ok {
			if text, ok := artifactTextFromObject(nested, sourceTool, textField, false); ok {
				return text, true
			}
		}
	}
	for _, key := range []string{"tool_results", "toolResults", "items"} {
		if items, ok := obj[key].([]any); ok {
			if text, ok := artifactTextFromValue(items, sourceTool, textField, true); ok {
				return text, true
			}
		}
	}
	return "", false
}

// OnFailureConfig 描述节点失败后的调度策略。
// 它只表达 retry/escalate 等策略数据，具体状态推进仍由 dispatcher 和 DAG store 决定。
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

// ExecutionConfig 描述节点级执行策略。timeout 支持 Go duration 字符串，
// timeout_sec 兼容 task_create_dag 的既有输入形态；两者冲突时 fail-fast。
type ExecutionConfig struct {
	Timeout    string `json:"timeout,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// AgentExecConfig 是 node_type=agent 节点的 exec 块。
// 字段会映射到 LaunchRequest 与 remote thread/start 配置，是 DAG 到 agent runtime 的跨模块 wire 边界。
type AgentExecConfig struct {
	Provider string `json:"provider,omitempty"` // claude | codex
	Model    string `json:"model,omitempty"`    // opus | sonnet | ...
	// Codex 身份字段会映射到 thread/start 的 codexHome/codexInstanceKey/codexModelProvider。
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
	BudgetTokens       int64            `json:"budget_tokens,omitempty"` // 预留给预算策略；当前执行器只透传不扣减
	OnFailure          *OnFailureConfig `json:"on_failure,omitempty"`
}

// AutomationExecConfig 是 node_type=automation 节点的 exec 块。
type AutomationExecConfig struct {
	// Kind 为自动化执行通道，当前仅 command_card 可运行；空字符串兼容为 command_card，其他值 fail-fast。
	Kind           string            `json:"kind,omitempty"`
	CommandRef     string            `json:"command_ref"`    // 查 command_cards 表
	Args           json.RawMessage   `json:"args,omitempty"` // 命令参数（结构由 command 决定）
	CWD            string            `json:"cwd,omitempty"`
	WorkspaceRoots []string          `json:"workspace_roots,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	BudgetTokens   int64             `json:"budget_tokens,omitempty"`
	OnFailure      *OnFailureConfig  `json:"on_failure,omitempty"`
}

// HybridExecConfig 是 node_type=hybrid 节点的 exec 块（先 automation 后 agent verifier）。
type HybridExecConfig struct {
	Automation *AutomationExecConfig `json:"automation,omitempty"`
	Verifier   *AgentExecConfig      `json:"verifier,omitempty"`
}

// AgentNodeConfig 是 node_type=agent 节点的完整 config。
type AgentNodeConfig struct {
	Exec      AgentExecConfig `json:"exec"`
	Execution ExecutionConfig `json:"execution"`
	Inputs    InputsConfig    `json:"inputs"`
	Outputs   OutputsConfig   `json:"outputs"`
	// FirstTurn 可选：覆盖 agent_key 的默认提示词（一次性指令）。
	FirstTurn string `json:"first_turn,omitempty"`
}

// AutomationNodeConfig 是 node_type=automation 节点的完整 config。
type AutomationNodeConfig struct {
	Exec      AutomationExecConfig `json:"exec"`
	Execution ExecutionConfig      `json:"execution"`
	Inputs    InputsConfig         `json:"inputs"`
	Outputs   OutputsConfig        `json:"outputs"`
}

// HybridNodeConfig 是 node_type=hybrid 节点的完整 config。
type HybridNodeConfig struct {
	Exec      HybridExecConfig `json:"exec"`
	Execution ExecutionConfig  `json:"execution"`
	Inputs    InputsConfig     `json:"inputs"`
	Outputs   OutputsConfig    `json:"outputs"`
}

// ErrUnknownNodeType 在 ParseNodeConfig 收到未知 node_type 时返回；errors.Is 可用。
var ErrUnknownNodeType = fmt.Errorf("nodeexec: unknown node_type")

// ParsedNodeConfig 是 ParseNodeConfig 的 tagged union 返回值。
// 按 NodeType 只会填一个具体 Config 指针，调用方据此选择执行路径。
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
	nodeType = NormalizeNodeType(nodeType)
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

// NormalizeNodeType 统一 DAG 节点类型空值兼容规则。
// 空 node_type 是旧模板的 agent 默认值；未知类型保留给 ParseNodeConfig fail-fast。
func NormalizeNodeType(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "agent"
	}
	return t
}

// ValidateLaunchCWDForNodeConfig 从节点配置提取并校验启动 cwd。
// agent 和 hybrid verifier 必须显式提供合法 cwd；automation 节点没有 launch cwd。
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

// ValidatePersistableNodeConfig 在 DAG 模板写入前校验 typed executable config。
// 它只检查持久化后无法安全解释的配置；已有历史行的 cwd/root 缺失仍由执行前 runner
// fail-fast 处理，避免旧模板被误当作可信命令边界。
func ValidatePersistableNodeConfig(nodeType string, raw json.RawMessage) error {
	parsed, err := ParseNodeConfig(nodeType, raw)
	if err != nil {
		return fmt.Errorf("nodeexec: invalid node config: %w", err)
	}
	switch {
	case parsed.Agent != nil:
		if _, err := ValidateLaunchCWDForNodeConfig(nodeType, raw); err != nil {
			return fmt.Errorf("agent launch cwd: %w", err)
		}
	case parsed.Automation != nil:
		return validatePersistableAutomationExec(parsed.Automation.Exec)
	case parsed.Hybrid != nil && parsed.Hybrid.Exec.Automation != nil:
		if err := validatePersistableAutomationExec(*parsed.Hybrid.Exec.Automation); err != nil {
			return fmt.Errorf("hybrid automation: %w", err)
		}
	}
	return nil
}

func validatePersistableAutomationExec(exec AutomationExecConfig) error {
	if exec.Kind != AutomationKindCommandCard {
		return fmt.Errorf("unsupported automation.kind: %q", exec.Kind)
	}
	if strings.TrimSpace(exec.CommandRef) == "" {
		return errors.New("command_ref required in node.config.exec")
	}
	if err := validateAutomationCommandEnv(exec.Env); err != nil {
		return err
	}
	return nil
}

// ResolveExecutionTimeout 合并 DAG metadata 默认 timeout 与节点 config 覆盖值。
// 返回 0 表示未配置 timeout；配置存在但非法时返回错误，调用方必须阻断执行。
func ResolveExecutionTimeout(dagMetadata, nodeConfig json.RawMessage) (time.Duration, error) {
	dagTimeout, hasDag, err := decodeExecutionTimeout(dagMetadata, true)
	if err != nil {
		return 0, fmt.Errorf("dag metadata execution timeout: %w", err)
	}
	nodeTimeout, hasNode, err := decodeExecutionTimeout(nodeConfig, false)
	if err != nil {
		return 0, fmt.Errorf("node config execution timeout: %w", err)
	}
	if hasNode {
		return nodeTimeout, nil
	}
	if hasDag {
		return dagTimeout, nil
	}
	return 0, nil
}

// ExecutionTimeout 解析单个 execution 块，供 executor 直测和 router 合并逻辑共用。
func (cfg ExecutionConfig) ExecutionTimeout() (time.Duration, bool, error) {
	return resolveExecutionConfigTimeout(cfg)
}

type executionTimeoutEnvelope struct {
	Execution ExecutionConfig `json:"execution"`
	Schedule  struct {
		DefaultTimeoutSec int `json:"default_timeout_sec,omitempty"`
	} `json:"schedule"`
}

// decodeExecutionTimeout 解析节点配置或 DAG metadata 里的执行超时。
// includeScheduleDefault 只给 DAG 默认值使用，节点级 timeout 冲突会直接报错。
func decodeExecutionTimeout(raw json.RawMessage, includeScheduleDefault bool) (time.Duration, bool, error) {
	if len(raw) == 0 {
		return 0, false, nil
	}
	var envelope executionTimeoutEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, false, fmt.Errorf("decode execution envelope: %w", err)
	}
	timeout, ok, err := resolveExecutionConfigTimeout(envelope.Execution)
	if err != nil || ok || !includeScheduleDefault {
		return timeout, ok, err
	}
	if envelope.Schedule.DefaultTimeoutSec == 0 {
		return 0, false, nil
	}
	if envelope.Schedule.DefaultTimeoutSec < 0 {
		return 0, false, errors.New("schedule.default_timeout_sec must be positive")
	}
	return time.Duration(envelope.Schedule.DefaultTimeoutSec) * time.Second, true, nil
}

// resolveExecutionConfigTimeout 合并 timeout 字符串和 timeout_sec 兼容字段。
// 两种写法同时存在但数值不同视为配置错误，避免保存了不生效的超时。
func resolveExecutionConfigTimeout(cfg ExecutionConfig) (time.Duration, bool, error) {
	durationTimeout, hasDuration, err := parseExecutionTimeoutString(cfg.Timeout)
	if err != nil {
		return 0, false, err
	}
	secondsTimeout, hasSeconds, err := parseExecutionTimeoutSeconds(cfg.TimeoutSec)
	if err != nil {
		return 0, false, err
	}
	if hasDuration && hasSeconds && durationTimeout != secondsTimeout {
		return 0, false, errors.New("execution.timeout conflicts with execution.timeout_sec")
	}
	switch {
	case hasDuration:
		return durationTimeout, true, nil
	case hasSeconds:
		return secondsTimeout, true, nil
	default:
		return 0, false, nil
	}
}

func parseExecutionTimeoutString(raw string) (time.Duration, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false, nil
	}
	timeout, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, false, fmt.Errorf("execution.timeout parse duration: %w", err)
	}
	if timeout <= 0 {
		return 0, false, errors.New("execution.timeout must be positive")
	}
	return timeout, true, nil
}

func parseExecutionTimeoutSeconds(seconds int) (time.Duration, bool, error) {
	if seconds == 0 {
		return 0, false, nil
	}
	if seconds < 0 {
		return 0, false, errors.New("execution.timeout_sec must be positive")
	}
	return time.Duration(seconds) * time.Second, true, nil
}

// ParseAgentConfig 解码 node_type=agent 的完整 config。
// 空 raw（未配置）返回 zero-value config，不报错。
func ParseAgentConfig(raw json.RawMessage) (*AgentNodeConfig, error) {
	var cfg AgentNodeConfig
	if len(raw) == 0 {
		return &cfg, nil
	}
	if err := decodeStrictNodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("nodeexec: parse agent config: %w", err)
	}
	if err := validateExecutionConfig(cfg.Execution); err != nil {
		return nil, err
	}
	if err := validateOutputsConfig(cfg.Outputs); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ParseAutomationConfig 解码 node_type=automation 的完整 config。
// 空 kind 兼容为 command_card；非 command_card 立即返回 ErrUnsupportedAutomationKind。
func ParseAutomationConfig(raw json.RawMessage) (*AutomationNodeConfig, error) {
	var cfg AutomationNodeConfig
	if len(raw) == 0 {
		return &cfg, nil
	}
	if err := decodeStrictNodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("nodeexec: parse automation config: %w", err)
	}
	if err := validateExecutionConfig(cfg.Execution); err != nil {
		return nil, err
	}
	if err := validateOutputsConfig(cfg.Outputs); err != nil {
		return nil, err
	}
	switch cfg.Exec.Kind {
	case "":
		cfg.Exec.Kind = AutomationKindCommandCard
	case AutomationKindCommandCard:
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
	if err := decodeStrictNodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("nodeexec: parse hybrid config: %w", err)
	}
	if err := validateExecutionConfig(cfg.Execution); err != nil {
		return nil, err
	}
	if err := validateOutputsConfig(cfg.Outputs); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func decodeStrictNodeConfig(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

func validateExecutionConfig(cfg ExecutionConfig) error {
	if _, _, err := cfg.ExecutionTimeout(); err != nil {
		return err
	}
	return nil
}
