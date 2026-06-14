package nodeexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"text/template"
)

// AutomationExecutor wires node_type=automation through command_get and a
// command-card runner. F2.1 接通了 command_ref 解码 + 执行路径；F2.2 补足 inputs/outputs
// 处理：
//   - Inputs：按 cfg.Inputs.FromNodes/FromSharedfiles 从 RunContext 拉取内容，合并到 args.inputs
//     子对象供 command_template 渲染。args 本身拥有同名 key 时走 validation。
//   - Outputs：成功后根据 cfg.Outputs 写入 sharedfile / node.result；二者同时缺省时保持 F2.1 走 to_node_result
//     的该路径，避免静默丢失输出。
//   - 安全边界：拒绝 automation 节点 outputs 反向注入 agent prompt（F1.3 / ADR-011 边界）。
type AutomationExecutor struct {
	commandGetter AutomationCommandGetter
	runner        AutomationCommandRunner
	hooks         map[HookPoint]HookHandler
}

type AutomationCommandGetter interface {
	GetCommandCard(ctx context.Context, cardKey string) (AutomationCommandCard, error)
}

type AutomationCommandRunner interface {
	RunCommandCard(ctx context.Context, card AutomationCommandCard, args json.RawMessage) (AutomationCommandResult, error)
}

type AutomationCommandCard struct {
	CardKey         string          `json:"card_key"`
	Title           string          `json:"title,omitempty"`
	Description     string          `json:"description,omitempty"`
	CommandTemplate string          `json:"command_template"`
	ArgsSchema      json.RawMessage `json:"args_schema,omitempty"`
	RiskLevel       string          `json:"risk_level,omitempty"`
	Enabled         bool            `json:"enabled"`
}

type AutomationCommandResult struct {
	CardKey  string          `json:"card_key"`
	ExitCode int             `json:"exit_code"`
	Stdout   string          `json:"stdout,omitempty"`
	Stderr   string          `json:"stderr,omitempty"`
	Command  string          `json:"command,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`
}

const (
	automationCommandStdoutLimitBytes = 1024 * 1024
	automationCommandStderrLimitBytes = 256 * 1024
)

type ShellCommandRunner struct{}

// NewShellCommandRunner 创建shell命令runner。
func NewShellCommandRunner() *ShellCommandRunner { return &ShellCommandRunner{} }

// RunCommandCard 运行命令card。
func (ShellCommandRunner) RunCommandCard(ctx context.Context, card AutomationCommandCard, args json.RawMessage) (AutomationCommandResult, error) {
	command, normalizedArgs, err := renderCommandTemplate(card.CommandTemplate, args)
	if err != nil {
		return AutomationCommandResult{}, err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	stdout := newCommandOutputBuffer("stdout", automationCommandStdoutLimitBytes)
	stderr := newCommandOutputBuffer("stderr", automationCommandStderrLimitBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	result := AutomationCommandResult{
		CardKey:  card.CardKey,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Command:  command,
		Args:     normalizedArgs,
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		return result, CommandExitError{ExitCode: result.ExitCode, Err: err}
	}
	return result, nil
}

type commandOutputBuffer struct {
	label     string
	limit     int
	buf       bytes.Buffer
	total     int
	truncated bool
}

func newCommandOutputBuffer(label string, limit int) *commandOutputBuffer {
	return &commandOutputBuffer{label: label, limit: limit}
}

// Write 写入编排。
func (b *commandOutputBuffer) Write(p []byte) (int, error) {
	b.total += len(p)
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || len(p) > 0
		return len(p), nil
	}
	if len(p) > remaining {
		if _, err := b.buf.Write(p[:remaining]); err != nil {
			return 0, err
		}
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

// String 返回字符串表示。
func (b *commandOutputBuffer) String() string {
	out := b.buf.String()
	if !b.truncated {
		return out
	}
	dropped := b.total - b.buf.Len()
	if dropped < 0 {
		dropped = 0
	}
	return out + fmt.Sprintf(
		"\n[super-dolphin: %s truncated after %d bytes; dropped %d bytes]\n",
		b.label,
		b.limit,
		dropped,
	)
}

type CommandExitError struct {
	ExitCode int
	Err      error
}

// Error 返回错误文本。
func (e CommandExitError) Error() string {
	return fmt.Sprintf("command exited with code %d: %v", e.ExitCode, e.Err)
}

// Unwrap 返回底层错误。
func (e CommandExitError) Unwrap() error { return e.Err }

type AutomationOption func(*AutomationExecutor)

// WithAutomationHooks registers lifecycle hooks for automation nodes.
// WithAutomationHooks 设置automationhooks。
func WithAutomationHooks(hooks map[HookPoint]HookHandler) AutomationOption {
	return func(e *AutomationExecutor) { e.hooks = cloneHookHandlers(hooks) }
}

// NewAutomationExecutor 创建automationexecutor。
func NewAutomationExecutor(getter AutomationCommandGetter, runner AutomationCommandRunner, opts ...AutomationOption) *AutomationExecutor {
	e := &AutomationExecutor{commandGetter: getter, runner: runner}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	return e
}

// Execute 执行编排。
func (e *AutomationExecutor) Execute(ctx context.Context, node Node, runCtx RunContext) (NodeOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, failure := parseExecutableAutomationConfig(node.Config)
	if failure != nil {
		return *failure, nil
	}
	if failure := e.validateWiring(); failure != nil {
		return *failure, nil
	}
	if failure := validateAutomationOutputs(node.Config, cfg); failure != nil {
		return *failure, nil
	}

	card, failure := e.loadCommandCard(ctx, cfg)
	if failure != nil {
		return *failure, nil
	}

	runArgs, failure := buildAutomationRunArgs(ctx, cfg, runCtx)
	if failure != nil {
		return *failure, nil
	}

	result, err := e.runner.RunCommandCard(ctx, card, runArgs)
	if err != nil {
		return failedAutomationOutcome(classifyAutomationError(err), "run command card: "+err.Error()), nil
	}
	return finalizeAutomationOutcome(ctx, cfg, node, runCtx, result)
}

// finalizeAutomationOutcome 把 runner 返回结果按 cfg.Outputs 写入 sharedfile / node.result，
// 并生成最终 NodeOutcome。拆出独立函数是为了把 Execute CC 压在代码守卫阈以下。
//
// finalizeAutomationOutcome materialises the AutomationCommandResult into the configured outputs
// (sharedfile + node.result) and produces the terminal NodeOutcome. Pulled out so Execute stays
// under the cyclomatic-complexity guard.
func finalizeAutomationOutcome(ctx context.Context, cfg *AutomationNodeConfig, node Node, runCtx RunContext, result AutomationCommandResult) (NodeOutcome, error) {
	payload, err := json.Marshal(result)
	if err != nil {
		return failedAutomationOutcome(FailureClassValidation, "marshal automation result: "+err.Error()), nil
	}
	outcome := NodeOutcome{Status: NodeStatusDone}
	if shouldEmitFullNodeResult(cfg.Outputs) {
		if failure := enforceNodeResultSizeCap(payload); failure != nil {
			return *failure, nil
		}
		outcome.Result = payload
	} else if shouldEmitSharedfileEnvelope(cfg.Outputs) {
		envelope, failure := buildAutomationSharedfileEnvelope(cfg.Outputs, node, runCtx)
		if failure != nil {
			return *failure, nil
		}
		outcome.Result = envelope
	}
	if failure := writeAutomationSharedfile(ctx, cfg, runCtx, result); failure != nil {
		return *failure, nil
	}
	return outcome, nil
}

// NodeResultSizeCapBytes 是 outputs.to_node_result 路径下 NodeOutcome.Result 的硬上限。
// ADR-006 决策（Accepted 2026-05-12）：4KB 摘要阈值，超出 → validation 失败，提示配置
// outputs.to_sharedfile。大输出走 sharedfile 是 ADR 主路径，避免 task_dag_nodes.result
// jsonb 列膨胀拖垮 PG 查询 / UI 列表渲染。
//
// 4KB = 4096 bytes 含义来自蓝图 v2 §5 关键决策汇总 "result vs sharedfile 边界" 行；
// 选 4KB 的依据见 ADR-006 §1：下游 LLM context 阈值的经验拍 + PG jsonb toast 256KB 的
// 1/64 中位拍，足够装下结构化摘要（~ <2KB JSON）+ 留余量。
//
// NodeResultSizeCapBytes is the hard cap enforced on NodeOutcome.Result before writing to
// task_dag_nodes.result. ADR-006 (Accepted 2026-05-12) chose 4096 bytes as the validation
// threshold; oversize payloads must route through outputs.to_sharedfile.
const NodeResultSizeCapBytes = 4096

// enforceNodeResultSizeCap 在 NodeOutcome.Result 落库前测 size，超阈返回 validation
// 失败 outcome；不超返 nil。F1.3 实装 ADR-006 决策。
//
// 边界：len(payload) <= 4096 OK；> 4096 拒绝。错误消息显式建议「configure
// outputs.to_sharedfile」让运营者知道修复路径。
//
// enforceNodeResultSizeCap returns a *NodeOutcome iff payload exceeds
// NodeResultSizeCapBytes; nil means within cap. ADR-006 main path.
func enforceNodeResultSizeCap(payload []byte) *NodeOutcome {
	if len(payload) <= NodeResultSizeCapBytes {
		return nil
	}
	outcome := failedAutomationOutcome(
		FailureClassValidation,
		fmt.Sprintf(
			"result exceeds 4KB size cap (%d > %d bytes), configure outputs.to_sharedfile (ADR-006)",
			len(payload), NodeResultSizeCapBytes,
		),
	)
	return &outcome
}

// shouldEmitFullNodeResult 判定是否在 NodeOutcome.Result 写入 marshal 后的完整命令结果。
// F2.1 原行为：总是写入 payload。F2.2 保持向下兼容：
//   - Outputs.ToNodeResult 显式 true → 写入；
//   - Outputs.ToSharedfile 显式配置且 ToNodeResult 未勾选 → 不写完整 payload；由轻量 envelope 指向 sharedfile；
//   - 两者都默认零值（旧 DAG / F2.1 测例）→ 保留写入，避免静默丢失输出。
func shouldEmitFullNodeResult(out OutputsConfig) bool {
	if out.ToNodeResult {
		return true
	}
	return automationSharedfilePath(out) == ""
}

func shouldEmitSharedfileEnvelope(out OutputsConfig) bool {
	if automationSharedfilePath(out) == "" {
		return false
	}
	return out.NodeResultEnvelope == nil || *out.NodeResultEnvelope
}

func automationSharedfilePath(out OutputsConfig) string {
	if out.ToSharedfile == nil {
		return ""
	}
	return strings.TrimSpace(out.ToSharedfile.Path)
}

// buildAutomationSharedfileEnvelope 构建automationsharedfile包装。
func buildAutomationSharedfileEnvelope(out OutputsConfig, node Node, runCtx RunContext) (json.RawMessage, *NodeOutcome) {
	path := automationSharedfilePath(out)
	if path == "" {
		return nil, nil
	}
	dagKey, nodeKey := resolveAutomationEnvelopeKeys(node, runCtx)
	if dagKey == "" || nodeKey == "" || runCtx.RunID <= 0 {
		outcome := failedAutomationOutcome(FailureClassValidation,
			"outputs.to_sharedfile requires dag_key/run_id/node_key for node result envelope")
		return nil, &outcome
	}
	payload, err := json.Marshal(struct {
		Kind       string `json:"kind"`
		Path       string `json:"path"`
		Dag        string `json:"dag"`
		Run        int64  `json:"run"`
		Node       string `json:"node"`
		Sharedfile struct {
			Path string `json:"path"`
		} `json:"sharedfile"`
	}{
		Kind: "sharedfile",
		Path: path,
		Dag:  dagKey,
		Run:  runCtx.RunID,
		Node: nodeKey,
		Sharedfile: struct {
			Path string `json:"path"`
		}{Path: path},
	})
	if err != nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "marshal automation sharedfile envelope: "+err.Error())
		return nil, &outcome
	}
	return payload, nil
}

func resolveAutomationEnvelopeKeys(node Node, runCtx RunContext) (string, string) {
	dagKey := strings.TrimSpace(runCtx.DagKey)
	if dagKey == "" {
		dagKey = strings.TrimSpace(node.DagKey)
	}
	nodeKey := strings.TrimSpace(runCtx.NodeKey)
	if nodeKey == "" {
		nodeKey = strings.TrimSpace(node.NodeKey)
	}
	return dagKey, nodeKey
}

func parseExecutableAutomationConfig(raw json.RawMessage) (*AutomationNodeConfig, *NodeOutcome) {
	cfg, parseErr := ParseAutomationConfig(raw)
	if parseErr != nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "decode automation config: "+parseErr.Error())
		return nil, &outcome
	}
	if cfg == nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "decode automation config: nil parsed config")
		return nil, &outcome
	}
	if cfg.Exec.Kind != AutomationKindCommandCard {
		outcome := failedAutomationOutcome(FailureClassValidation, fmt.Sprintf("unsupported automation.kind: %q", cfg.Exec.Kind))
		return nil, &outcome
	}
	if strings.TrimSpace(cfg.Exec.CommandRef) == "" {
		outcome := failedAutomationOutcome(FailureClassValidation, "command_ref required in node.config.exec")
		return nil, &outcome
	}
	_ = cfg.Inputs
	_ = cfg.Outputs
	return cfg, nil
}

func (e *AutomationExecutor) validateWiring() *NodeOutcome {
	if e == nil || e.commandGetter == nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "automation executor: command_get client not wired")
		return &outcome
	}
	if e.runner == nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "automation executor: command runner not wired")
		return &outcome
	}
	return nil
}

func (e *AutomationExecutor) loadCommandCard(ctx context.Context, cfg *AutomationNodeConfig) (AutomationCommandCard, *NodeOutcome) {
	commandRef := strings.TrimSpace(cfg.Exec.CommandRef)
	card, err := e.commandGetter.GetCommandCard(ctx, commandRef)
	if err != nil {
		outcome := failedAutomationOutcome(classifyAutomationError(err), "command_get: "+err.Error())
		return AutomationCommandCard{}, &outcome
	}
	if !card.Enabled {
		outcome := failedAutomationOutcome(FailureClassHard, fmt.Sprintf("command card %q is disabled", commandRef))
		return AutomationCommandCard{}, &outcome
	}
	return card, nil
}

// Hooks 处理hooks。
func (e *AutomationExecutor) Hooks() map[HookPoint]HookHandler {
	if e == nil {
		return nil
	}
	return cloneHookHandlers(e.hooks)
}

func failedAutomationOutcome(class FailureClass, summary string) NodeOutcome {
	return NodeOutcome{
		Status:       NodeStatusFailed,
		FailureClass: class,
		ErrorSummary: truncateErrSummary(summary),
	}
}

// classifyAutomationError 分类automation错误。
func classifyAutomationError(err error) FailureClass {
	if err == nil {
		return FailureClassTransient
	}
	var exitErr CommandExitError
	if errors.As(err, &exitErr) {
		return FailureClassHard
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return FailureClassTransient
	}
	msg := strings.ToLower(err.Error())
	switch {
	case containsAny(msg, automationValidationKeywords):
		return FailureClassValidation
	case containsAny(msg, automationNotFoundKeywords):
		return FailureClassHard
	case containsAny(msg, automationTransientKeywords):
		return FailureClassTransient
	case containsAny(msg, automationInfrastructureKeywords):
		return FailureClassInfrastructure
	default:
		return FailureClassHard
	}
}

var (
	automationValidationKeywords = []string{
		"parse", "decode", "unmarshal", "marshal", "json", "template", "required", "missing key",
	}
	automationNotFoundKeywords  = []string{"not found", "no such command", "unknown command"}
	automationTransientKeywords = []string{
		"deadline exceeded", "timeout", "timed out", "i/o timeout", "connection refused", "connection reset",
		"temporary", "temporarily", "rate limit", "rate-limit", "rate_limit", "too many requests", "http 429", "status 429",
	}
	automationInfrastructureKeywords = []string{
		"database", "postgres", "pgx", "sql", "transport unavailable", "service unavailable", "bad gateway", "gateway timeout", "http 500", "http 502", "http 503", "http 504",
	}
)

func containsAny(msg string, keywords []string) bool {
	for _, k := range keywords {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

// renderCommandTemplate 渲染命令template。
func renderCommandTemplate(commandTemplate string, args json.RawMessage) (string, json.RawMessage, error) {
	if strings.TrimSpace(commandTemplate) == "" {
		return "", nil, errors.New("command_template is required")
	}
	data := map[string]any{}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	if err := json.Unmarshal(args, &data); err != nil {
		return "", nil, fmt.Errorf("parse command args: %w", err)
	}
	normalizedArgs, err := json.Marshal(data)
	if err != nil {
		return "", nil, fmt.Errorf("marshal command args: %w", err)
	}
	tpl, err := template.New("command_card").Option("missingkey=error").Parse(commandTemplate)
	if err != nil {
		return "", nil, fmt.Errorf("parse command template: %w", err)
	}
	var rendered bytes.Buffer
	if err := tpl.Execute(&rendered, data); err != nil {
		return "", nil, fmt.Errorf("render command template: %w", err)
	}
	command := strings.TrimSpace(rendered.String())
	if command == "" {
		return "", nil, errors.New("rendered command is empty")
	}
	return command, normalizedArgs, nil
}

// automationOutputsForbiddenKeys 是 automation 节点 outputs 中不得出现的「agent prompt
// 注入 / agent 路由」语义字段名。automation 节点只负责命令卡执行 + 输出落地，
// 不得为下游 agent 拼 prompt 或路由 agent 实例，避免跨节点路径被「转变」为
// 隐式 agent 调用 / 隐式模型升级（F1.3 / ADR-011 边界）。
//
// 字段集来源（R1 P1 #3 扩展）：
//   - prompt 系：prompt / first_turn / agent_prompt / system_prompt / append_error
//     —— 直接注入 prompt 文本会让 automation outputs 隐式驱动下游 agent；
//   - 路由系：agent_key / model / provider / language / tool_choice / tools
//     —— automation 节点 outputs 不得替下游 agent 决定模型 / provider / 工具白
//     名单等路由字段；路由必须由该 agent 节点的 config.exec 显式声明。
//
// automationOutputsForbiddenKeys lists field names whose presence in an automation
// node's outputs config indicates an attempt to inject agent-style prompts or
// silently reroute downstream agent dispatch; per the F1.3 / ADR-011 boundary,
// those flows belong to hybrid (agent→automation), not automation outputs, so
// the executor must reject them at validation time.
var automationOutputsForbiddenKeys = []string{
	// prompt-injection family
	"prompt", "first_turn", "agent_prompt", "system_prompt", "append_error",
	// agent-routing family
	"agent_key", "model", "provider", "language", "tool_choice", "tools",
}

// validateAutomationOutputs 验证 outputs 配置未含 agent prompt 注入字段。
// 由于 typed OutputsConfig 会默认忽略未知 json key，这里手工重读 raw 才能猝住违规。
// 空 raw / 缺 outputs / 非对象 outputs 都算合法（sanity 失败不在本守守范畴）。
func validateAutomationOutputs(raw json.RawMessage, _ *AutomationNodeConfig) *NodeOutcome {
	return validateOutputsForbiddenKeys(raw, automationOutputsForbiddenKeys, func(key string) NodeOutcome {
		return failedAutomationOutcome(FailureClassValidation,
			fmt.Sprintf("automation outputs cannot include agent-prompt or agent-routing field %q", key))
	})
}

// validateOutputsForbiddenKeys 校验outputsforbidden键。
func validateOutputsForbiddenKeys(raw json.RawMessage, forbiddenKeys []string, buildOutcome func(string) NodeOutcome) *NodeOutcome {
	if len(raw) == 0 {
		return nil
	}
	var envelope struct {
		Outputs json.RawMessage `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Outputs) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Outputs, &fields); err != nil {
		// outputs 不是 object（例如 null 或数组）——typed 解码阶段会报错，本守守不重复报告。
		return nil
	}
	for _, key := range forbiddenKeys {
		if _, ok := fields[key]; ok {
			outcome := buildOutcome(key)
			return &outcome
		}
	}
	return nil
}

// buildAutomationRunArgs 把 cfg.Inputs 指定的 prev_results / sharedfiles 合并到 args.__inputs 子对象。
// 返回的 json.RawMessage 用于 RunCommandCard；原 cfg.Exec.Args 不被修改。
//
// 收敛 batch 第 6 项（R1 P2 #4）：reserved key 从 "inputs" 改为 "__inputs"（双下划线
// 前缀），避免与普通 command_card args 用户自定义的 "inputs" 字段冲突。command_template
// 渲染路径用 `{{.__inputs.from_nodes.X}}` 访问。
//
// 合并规则：
//   - inputs.FromNodes / FromSharedfiles 都空 → 返回原 args，F2.1 happy 路径不变；
//   - args 本身已包含 "__inputs" key → validation（避免隐式覆盖）；
//   - FromNodes 里的 node_key 在 PrevResults 中不存在 → validation；
//   - FromSharedfiles 非空 但 SharedFileReader == nil → validation；读失败走 classify 分类。
func buildAutomationRunArgs(ctx context.Context, cfg *AutomationNodeConfig, runCtx RunContext) (json.RawMessage, *NodeOutcome) {
	in := cfg.Inputs
	if len(in.FromNodes) == 0 && len(in.FromSharedfiles) == 0 {
		return cfg.Exec.Args, nil
	}
	argsMap, failure := decodeArgsForInjection(cfg.Exec.Args)
	if failure != nil {
		return nil, failure
	}
	injected, failure := buildInputsPayload(ctx, in, runCtx)
	if failure != nil {
		return nil, failure
	}
	argsMap["__inputs"] = injected
	merged, err := json.Marshal(argsMap)
	if err != nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "marshal merged command args: "+err.Error())
		return nil, &outcome
	}
	return merged, nil
}

// decodeArgsForInjection 把 cfg.Exec.Args 解码为 map，同时拒绝占用 reserved "__inputs" key 的原始 args。
// 该 helper 拆出是为了压住 buildAutomationRunArgs 的圏复杂度（代码守卫阈 CC ≤ 10）。
func decodeArgsForInjection(raw json.RawMessage) (map[string]any, *NodeOutcome) {
	argsMap := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &argsMap); err != nil {
			outcome := failedAutomationOutcome(FailureClassValidation, "decode command args: "+err.Error())
			return nil, &outcome
		}
	}
	if _, conflict := argsMap["__inputs"]; conflict {
		outcome := failedAutomationOutcome(FailureClassValidation,
			"command args already define reserved key \"__inputs\"; remove it before injecting Inputs config")
		return nil, &outcome
	}
	return argsMap, nil
}

// buildInputsPayload 面向 cfg.Inputs 生成最终注入 args.__inputs 子对象的 map；nil failure 表示成功。
func buildInputsPayload(ctx context.Context, in InputsConfig, runCtx RunContext) (map[string]any, *NodeOutcome) {
	injected := map[string]any{}
	fromNodes, failure := collectPrevResults(in.FromNodes, runCtx.PrevResults)
	if failure != nil {
		return nil, failure
	}
	if len(fromNodes) > 0 {
		injected["from_nodes"] = fromNodes
	}
	fromShared, failure := collectSharedfileInputs(ctx, in.FromSharedfiles, runCtx.SharedFileReader)
	if failure != nil {
		return nil, failure
	}
	if len(fromShared) > 0 {
		injected["from_sharedfiles"] = fromShared
	}
	return injected, nil
}

// collectPrevResults 收集prev结果。
func collectPrevResults(fromNodes []string, prev map[string]json.RawMessage) (map[string]any, *NodeOutcome) {
	if len(fromNodes) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(fromNodes))
	for _, key := range fromNodes {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		raw, ok := prev[k]
		if !ok {
			outcome := failedAutomationOutcome(FailureClassValidation,
				fmt.Sprintf("inputs.from_nodes: missing prev result for node_key %q", k))
			return nil, &outcome
		}
		var decoded any
		if len(raw) == 0 || string(raw) == "null" {
			decoded = nil
		} else if err := json.Unmarshal(raw, &decoded); err != nil {
			// 不可解析作 JSON 时退为原始字符串，让 command_template 仍能拿到内容。
			decoded = string(raw)
		}
		out[k] = decoded
	}
	return out, nil
}

// collectSharedfileInputs 收集sharedfileinputs。
func collectSharedfileInputs(ctx context.Context, paths []string, reader SharedFileReader) (map[string]string, *NodeOutcome) {
	if len(paths) == 0 {
		return nil, nil
	}
	if reader == nil {
		outcome := failedAutomationOutcome(FailureClassValidation,
			"inputs.from_sharedfiles configured but SharedFileReader not wired in RunContext")
		return nil, &outcome
	}
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		path := strings.TrimSpace(p)
		if path == "" {
			continue
		}
		content, exists, err := reader.ReadSharedFile(ctx, path)
		if err != nil {
			outcome := failedAutomationOutcome(classifyAutomationError(err),
				fmt.Sprintf("inputs.from_sharedfiles[%q]: %v", path, err))
			return nil, &outcome
		}
		if !exists {
			outcome := failedAutomationOutcome(FailureClassValidation,
				fmt.Sprintf("inputs.from_sharedfiles references unknown path %q", path))
			return nil, &outcome
		}
		out[path] = content
	}
	return out, nil
}

// writeAutomationSharedfile 在配置了 outputs.to_sharedfile 时，把 result.Stdout 写入 sharedfile。
// 设计选型：仅写 stdout——这是 command_card 输出的「有意义载荷」；如需完整 result（包括 stderr / exit）
// 可后续在 SharedfileTarget 上加 mode 字段扩展。写入失败 → validation（任务约定）。
func writeAutomationSharedfile(ctx context.Context, cfg *AutomationNodeConfig, runCtx RunContext, result AutomationCommandResult) *NodeOutcome {
	target := cfg.Outputs.ToSharedfile
	if target == nil {
		return nil
	}
	path := strings.TrimSpace(target.Path)
	if path == "" {
		return nil
	}
	if runCtx.SharedFileWriter == nil {
		outcome := failedAutomationOutcome(FailureClassValidation,
			"outputs.to_sharedfile configured but SharedFileWriter not wired in RunContext")
		return &outcome
	}
	if err := runCtx.SharedFileWriter.WriteSharedFile(ctx, path, result.Stdout); err != nil {
		outcome := failedAutomationOutcome(FailureClassValidation,
			fmt.Sprintf("outputs.to_sharedfile[%q]: %v", path, err))
		return &outcome
	}
	return nil
}
