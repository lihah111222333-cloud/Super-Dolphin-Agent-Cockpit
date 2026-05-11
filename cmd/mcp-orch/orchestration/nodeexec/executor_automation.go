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

type ShellCommandRunner struct{}

func NewShellCommandRunner() *ShellCommandRunner { return &ShellCommandRunner{} }

func (ShellCommandRunner) RunCommandCard(ctx context.Context, card AutomationCommandCard, args json.RawMessage) (AutomationCommandResult, error) {
	command, normalizedArgs, err := renderCommandTemplate(card.CommandTemplate, args)
	if err != nil {
		return AutomationCommandResult{}, err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
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

type CommandExitError struct {
	ExitCode int
	Err      error
}

func (e CommandExitError) Error() string {
	return fmt.Sprintf("command exited with code %d: %v", e.ExitCode, e.Err)
}

func (e CommandExitError) Unwrap() error { return e.Err }

func NewAutomationExecutor(getter AutomationCommandGetter, runner AutomationCommandRunner) *AutomationExecutor {
	return &AutomationExecutor{commandGetter: getter, runner: runner}
}

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
	return finalizeAutomationOutcome(ctx, cfg, runCtx, result)
}

// finalizeAutomationOutcome 把 runner 返回结果按 cfg.Outputs 写入 sharedfile / node.result，
// 并生成最终 NodeOutcome。拆出独立函数是为了把 Execute CC 压在代码守卫阈以下。
//
// finalizeAutomationOutcome materialises the AutomationCommandResult into the configured outputs
// (sharedfile + node.result) and produces the terminal NodeOutcome. Pulled out so Execute stays
// under the cyclomatic-complexity guard.
func finalizeAutomationOutcome(ctx context.Context, cfg *AutomationNodeConfig, runCtx RunContext, result AutomationCommandResult) (NodeOutcome, error) {
	payload, err := json.Marshal(result)
	if err != nil {
		return failedAutomationOutcome(FailureClassValidation, "marshal automation result: "+err.Error()), nil
	}
	if failure := writeAutomationSharedfile(ctx, cfg, runCtx, result); failure != nil {
		return *failure, nil
	}
	outcome := NodeOutcome{Status: NodeStatusDone}
	if shouldEmitNodeResult(cfg.Outputs) {
		outcome.Result = payload
	}
	return outcome, nil
}

// shouldEmitNodeResult 判定是否在 NodeOutcome.Result 写入 marshal 后的命令结果。
// F2.1 原行为：总是写入 payload。F2.2 保持向下兼容：
//   - Outputs.ToNodeResult 显式 true → 写入；
//   - Outputs.ToSharedfile 显式配置且 ToNodeResult 未勾选 → 仅写 sharedfile，不在 node.result 重复；
//   - 两者都默认零值（旧 DAG / F2.1 测例）→ 保留写入，避免静默丢失输出。
func shouldEmitNodeResult(out OutputsConfig) bool {
	if out.ToNodeResult {
		return true
	}
	if out.ToSharedfile != nil && strings.TrimSpace(out.ToSharedfile.Path) != "" {
		return false
	}
	return true
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

func (e *AutomationExecutor) Hooks() map[HookPoint]HookHandler { return nil }

func failedAutomationOutcome(class FailureClass, summary string) NodeOutcome {
	return NodeOutcome{
		Status:       NodeStatusFailed,
		FailureClass: class,
		ErrorSummary: truncateErrSummary(summary),
	}
}

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
// 注入」语义字段名。automation 节点只负责命令卡执行 + 输出落地，不得为下游 agent
// 拼 prompt，避免跨节点路径被“转变”为隐式 agent 调用（F1.3 / ADR-011 边界）。
//
// automationOutputsForbiddenKeys lists field names whose presence in an automation
// node's outputs config indicates an attempt to inject agent-style prompts; per
// the F1.3 / ADR-011 boundary, those flows belong to hybrid (agent→automation),
// not automation outputs, so the executor must reject them at validation time.
var automationOutputsForbiddenKeys = []string{
	"prompt", "first_turn", "agent_prompt", "agent_key", "append_error", "system_prompt",
}

// validateAutomationOutputs 验证 outputs 配置未含 agent prompt 注入字段。
// 由于 typed OutputsConfig 会默认忽略未知 json key，这里手工重读 raw 才能猝住违规。
// 空 raw / 缺 outputs / 非对象 outputs 都算合法（sanity 失败不在本守守范畴）。
func validateAutomationOutputs(raw json.RawMessage, _ *AutomationNodeConfig) *NodeOutcome {
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
	for _, key := range automationOutputsForbiddenKeys {
		if _, ok := fields[key]; ok {
			outcome := failedAutomationOutcome(FailureClassValidation,
				fmt.Sprintf("automation outputs cannot include agent-prompt field %q", key))
			return &outcome
		}
	}
	return nil
}

// buildAutomationRunArgs 把 cfg.Inputs 指定的 prev_results / sharedfiles 合并到 args.inputs 子对象。
// 返回的 json.RawMessage 用于 RunCommandCard；原 cfg.Exec.Args 不被修改。
//
// 合并规则：
//   - inputs.FromNodes / FromSharedfiles 都空 → 返回原 args，F2.1 happy 路径不变；
//   - args 本身已包含 "inputs" key → validation（避免隐式覆盖）；
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
	argsMap["inputs"] = injected
	merged, err := json.Marshal(argsMap)
	if err != nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "marshal merged command args: "+err.Error())
		return nil, &outcome
	}
	return merged, nil
}

// decodeArgsForInjection 把 cfg.Exec.Args 解码为 map，同时拒绝占用 reserved “inputs” key 的原始 args。
// 该 helper 拆出是为了压住 buildAutomationRunArgs 的圏复杂度（代码守卫阈 CC ≤ 10）。
func decodeArgsForInjection(raw json.RawMessage) (map[string]any, *NodeOutcome) {
	argsMap := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &argsMap); err != nil {
			outcome := failedAutomationOutcome(FailureClassValidation, "decode command args: "+err.Error())
			return nil, &outcome
		}
	}
	if _, conflict := argsMap["inputs"]; conflict {
		outcome := failedAutomationOutcome(FailureClassValidation,
			"command args already define reserved key \"inputs\"; remove it before injecting Inputs config")
		return nil, &outcome
	}
	return argsMap, nil
}

// buildInputsPayload 面向 cfg.Inputs 生成最终注入 args.inputs 子对象的 map；nil failure 表示成功。
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
