package nodeexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"text/template"
)

// AutomationExecutor 执行 node_type=automation 节点。
// 它通过 command_get 读取命令卡，再由 runner 在受限工作区内执行；inputs 会注入到 args.__inputs，
// outputs 负责写入 node.result 或 sharedfile。automation 不能通过 outputs 注入 agent prompt 或路由字段。
type AutomationExecutor struct {
	commandGetter AutomationCommandGetter
	runner        AutomationCommandRunner
	hooks         map[HookPoint]HookHandler
}

type AutomationCommandGetter interface {
	GetCommandCard(ctx context.Context, cardKey string) (AutomationCommandCard, error)
}

type AutomationCommandRunner interface {
	RunCommandCard(ctx context.Context, card AutomationCommandCard, args json.RawMessage, opts ...AutomationCommandRunOptions) (AutomationCommandResult, error)
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

// AutomationCommandRunOptions 收窄命令卡运行时可触达的工作区和环境变量边界。
type AutomationCommandRunOptions struct {
	CWD            string
	WorkspaceRoots []string
	Env            map[string]string
}

type AutomationOption func(*AutomationExecutor)

// WithAutomationHooks 注册 automation 节点的生命周期钩子。
// hook 由 router best-effort 调用，失败只记诊断，不改变命令执行结果。
func WithAutomationHooks(hooks map[HookPoint]HookHandler) AutomationOption {
	return func(e *AutomationExecutor) { e.hooks = cloneHookHandlers(hooks) }
}

// NewAutomationExecutor 构造 automation 执行器。
// getter 和 runner 缺失不会 panic；Execute 会把缺失 wiring 报为 validation 失败。
func NewAutomationExecutor(getter AutomationCommandGetter, runner AutomationCommandRunner, opts ...AutomationOption) *AutomationExecutor {
	e := &AutomationExecutor{commandGetter: getter, runner: runner}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	return e
}

// Execute 解析 automation config、读取命令卡并运行命令。
// 配置、wiring、输入和命令执行错误都通过 NodeOutcome 分类返回，error 通道只保留框架级失败。
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

	result, err := e.runner.RunCommandCard(ctx, card, runArgs, automationCommandRunOptionsFromConfig(cfg))
	if err != nil {
		return failedAutomationOutcome(classifyAutomationError(err), "run command card: "+err.Error()), nil
	}
	return finalizeAutomationOutcome(ctx, cfg, node, runCtx, result)
}

func automationCommandRunOptionsFromConfig(cfg *AutomationNodeConfig) AutomationCommandRunOptions {
	if cfg == nil {
		return AutomationCommandRunOptions{}
	}
	return AutomationCommandRunOptions{
		CWD:            cfg.Exec.CWD,
		WorkspaceRoots: append([]string(nil), cfg.Exec.WorkspaceRoots...),
		Env:            cloneStringMap(cfg.Exec.Env),
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

// finalizeAutomationOutcome 将命令执行结果物化为配置声明的输出。
// sharedfile 写入失败必须让节点失败，避免 node.result 指向不存在或未落盘的产物。
func finalizeAutomationOutcome(ctx context.Context, cfg *AutomationNodeConfig, node Node, runCtx RunContext, result AutomationCommandResult) (NodeOutcome, error) {
	payload, err := automationCommandResultPayload(result)
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

// automationCommandResultPayload 生成可持久化的 command 结果。
// Args 只用于 runner 入参，不能落入 node.result；其他字段再统一递归脱敏后才允许公开。
func automationCommandResultPayload(result AutomationCommandResult) (json.RawMessage, error) {
	payload, err := json.Marshal(struct {
		CardKey  string `json:"card_key"`
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout,omitempty"`
		Stderr   string `json:"stderr,omitempty"`
		Command  string `json:"command,omitempty"`
	}{
		CardKey:  result.CardKey,
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		Command:  result.Command,
	})
	if err != nil {
		return nil, err
	}
	return ScrubAutomationResultPayload(payload), nil
}

// ScrubAutomationResultPayload 清理对外或落库的 automation result JSON。
// 它删除 args/__inputs，并递归遮蔽 token、authorization、cookie、secret、password、api_key 等敏感键。
func ScrubAutomationResultPayload(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return json.RawMessage(`{"redacted":true,"reason":"invalid_result_json"}`)
	}
	cleaned, err := json.Marshal(scrubAutomationResultValue(value))
	if err != nil {
		return json.RawMessage(`{"redacted":true,"reason":"marshal_scrubbed_result"}`)
	}
	return cleaned
}

func scrubAutomationResultValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return scrubAutomationResultObject(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, scrubAutomationResultValue(item))
		}
		return out
	case string:
		return redactSensitiveText(typed)
	default:
		return value
	}
}

func scrubAutomationResultObject(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		if shouldDropAutomationResultKey(key) {
			continue
		}
		if isSensitiveAutomationResultKey(key) {
			out[key] = "[REDACTED]"
			continue
		}
		out[key] = scrubAutomationResultValue(value)
	}
	return out
}

func shouldDropAutomationResultKey(key string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(key))
	return trimmed == "args" || trimmed == "__inputs"
}

func isSensitiveAutomationResultKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	for _, marker := range []string{"token", "authorization", "cookie", "secret", "password", "apikey"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// NodeResultSizeCapBytes 是 outputs.to_node_result 写入 task_dag_nodes.result 前的硬上限。
// 超过 4KB 的命令结果必须改走 outputs.to_sharedfile，避免持久化列和节点列表承载大块原始输出。
const NodeResultSizeCapBytes = 4096

// enforceNodeResultSizeCap 在 node.result 落库前执行大小检查。
// len(payload) <= 4096 放行；超过上限返回 validation outcome，并提示调用方改用 outputs.to_sharedfile。
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

// shouldEmitFullNodeResult 判定是否把完整命令结果写入 NodeOutcome.Result。
// 未声明 sharedfile 时保留完整结果，避免旧配置丢失输出；声明 sharedfile 且未显式要求 node_result 时只写轻量 envelope。
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

// buildAutomationSharedfileEnvelope 构建写入 node.result 的轻量 sharedfile envelope。
// envelope 必须带 dag/run/node 三元组，缺任一上下文都会失败，避免 UI 指向不可审计的输出路径。
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

// Hooks 返回 automation executor 注册的生命周期钩子副本。
// nil 表示未配置 hook；返回副本避免调用方改写内部 map。
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

// classifyAutomationError 将 command_get 或 runner 错误映射为节点失败类别。
// 命令退出码属于业务 hard failure；上下文取消、超时和限流归 transient；存储/传输异常归 infrastructure。
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
		"unsafe shell metacharacter", "shell argv", "shell expansion",
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
	if err := validateCommandTemplateActionsQuoted(commandTemplate); err != nil {
		return "", nil, err
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

var unsafeRenderedShellTokens = []string{
	"\x00", "\r", "\n", "$(", "$", "`", "&&", "||", ";", "|", "&", ">", "<", "*", "?", "[", "]",
}

func validateRenderedCommandShellSafety(command string) error {
	for _, token := range unsafeRenderedShellTokens {
		if strings.Contains(command, token) {
			return fmt.Errorf("unsafe shell metacharacter %q in rendered command; automation command cards run via argv and do not support shell expansion", token)
		}
	}
	return nil
}

// validateCommandTemplateActionsQuoted 要求模板动作放在引号内。
// 未引号包裹的动态值可能被空白拆成多个 argv，因此在渲染前阻断。
func validateCommandTemplateActionsQuoted(commandTemplate string) error {
	state := commandTemplateQuoteState{}
	for i := 0; i < len(commandTemplate); i++ {
		if state.accept(commandTemplate[i]) {
			continue
		}
		if startsTemplateAction(commandTemplate, i) && !state.inQuote() {
			return errors.New("shell argv command template actions must be quoted to prevent whitespace splitting")
		}
	}
	return nil
}

type commandTemplateQuoteState struct {
	inSingle, inDouble, escaped bool
}

// accept 消费一个模板字符并维护引号状态。
// 返回 true 表示该字符已经作为引号/转义控制字符处理，调用方不再当作模板动作检查。
func (s *commandTemplateQuoteState) accept(ch byte) bool {
	if s.escaped {
		s.escaped = false
		return true
	}
	switch {
	case ch == '\\' && !s.inSingle:
		s.escaped = true
	case ch == '\'' && !s.inDouble:
		s.inSingle = !s.inSingle
	case ch == '"' && !s.inSingle:
		s.inDouble = !s.inDouble
	default:
		return false
	}
	return true
}

func (s commandTemplateQuoteState) inQuote() bool {
	return s.inSingle || s.inDouble
}

func startsTemplateAction(commandTemplate string, idx int) bool {
	return commandTemplate[idx] == '{' && idx+1 < len(commandTemplate) && commandTemplate[idx+1] == '{'
}

// ValidateAutomationCommandDispatchConfig 校验 automation command 节点进入执行或模板写入前的硬边界。
// 入口层必须显式给 cwd/workspace_roots，且不能声明 shell mode；历史空配置仍由旧解析路径兼容读取。
func ValidateAutomationCommandDispatchConfig(raw json.RawMessage) error {
	if err := rejectAutomationShellModeFields(raw); err != nil {
		return err
	}
	cfg, err := ParseAutomationConfig(raw)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Exec.CommandRef) == "" {
		return errors.New("automation command exec.command_ref is required")
	}
	if strings.TrimSpace(cfg.Exec.CWD) == "" {
		return errors.New("automation command exec.cwd is required")
	}
	if len(cfg.Exec.WorkspaceRoots) == 0 {
		return errors.New("automation command exec.workspace_roots is required")
	}
	if err := validateAutomationCommandEnv(cfg.Exec.Env); err != nil {
		return err
	}
	return nil
}

// rejectAutomationShellModeFields 读取原始 config，拦截 typed 解码会忽略的 shell-mode 兼容字段。
// 这些字段一旦被接受会让调用方误以为命令仍经 shell 执行，所以入口层必须直接报错。
func rejectAutomationShellModeFields(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	exec, _ := payload["exec"].(map[string]any)
	if exec == nil {
		return nil
	}
	for _, key := range []string{"shell", "shell_mode"} {
		if _, ok := exec[key]; ok {
			return fmt.Errorf("automation command exec.%s is not supported; command cards run via argv", key)
		}
	}
	if mode, ok := exec["mode"].(string); ok && strings.EqualFold(strings.TrimSpace(mode), "shell") {
		return errors.New("automation command exec.mode=shell is not supported; command cards run via argv")
	}
	return nil
}

// automationOutputsForbiddenKeys 列出 automation outputs 中禁止出现的 agent prompt 和路由字段。
// automation 节点只负责命令卡执行与输出落地，不能替下游 agent 决定 prompt、模型、provider 或工具名单。
var automationOutputsForbiddenKeys = []string{
	// prompt 注入字段。
	"prompt", "first_turn", "agent_prompt", "system_prompt", "append_error",
	// agent 路由字段。
	"agent_key", "model", "provider", "language", "tool_choice", "tools",
}

// validateAutomationOutputs 验证 outputs 配置未包含 agent prompt 或路由字段。
// typed OutputsConfig 会忽略未知 key，因此这里必须重读 raw JSON；形状错误留给 typed 解码路径报告。
func validateAutomationOutputs(raw json.RawMessage, _ *AutomationNodeConfig) *NodeOutcome {
	return validateOutputsForbiddenKeys(raw, automationOutputsForbiddenKeys, func(key string) NodeOutcome {
		return failedAutomationOutcome(FailureClassValidation,
			fmt.Sprintf("automation outputs cannot include agent-prompt or agent-routing field %q", key))
	})
}

// validateOutputsForbiddenKeys 在 raw outputs 对象里查找禁止字段。
// 该函数只做语义字段拦截；outputs 不是 object 时交给 typed schema 的验证路径处理。
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
		// outputs 不是 object（例如 null 或数组）时交给 typed 解码路径报错，这里不重复报告。
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

// buildAutomationRunArgs 把 cfg.Inputs 声明的上游结果和 sharedfile 内容合并到 args.__inputs。
// 原 cfg.Exec.Args 不会被修改；如果用户参数已占用 "__inputs"，直接 validation，避免隐式覆盖。
// 缺上游结果或缺 reader 是配置/wiring 错误；实际读取失败按底层错误分类。
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

// decodeArgsForInjection 将 cfg.Exec.Args 解码为可注入的 map。
// "__inputs" 是执行器保留 key；用户原始 args 占用该 key 时必须失败，避免上下文被覆盖。
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

// buildInputsPayload 生成最终写入 args.__inputs 的上下文对象。
// 只有配置声明的来源会被读取，避免 executor 主动扩大 store/sharedfile 访问面。
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

// collectPrevResults 收集上游节点 result 并解码为 template 可访问的值。
// 缺少声明的 node_key 是 validation 失败；非 JSON result 保留为原始字符串，保证命令模板仍能读取。
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

// collectSharedfileInputs 读取 inputs.from_sharedfiles 声明的文件内容。
// reader 未注入或路径不存在是 validation 失败；读取端口返回错误时按 automation 错误分类。
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
	content := stripAutomationControlFieldsBeforePromptReuse(result.Stdout)
	if writer, ok := runCtx.SharedFileWriter.(SharedFileMetadataWriter); ok {
		err := writer.WriteSharedFileWithMetadata(ctx, SharedFileWriteRequest{
			Path:          path,
			Content:       content,
			ContentType:   "text/plain",
			OwnerNode:     automationSharedFileOwnerNode(runCtx),
			ProducerActor: automationSharedFileProducerActor(runCtx),
			RunID:         runCtx.RunID,
		})
		if err != nil {
			outcome := failedAutomationOutcome(FailureClassValidation,
				fmt.Sprintf("outputs.to_sharedfile[%q]: %v", path, err))
			return &outcome
		}
		return nil
	}
	if err := runCtx.SharedFileWriter.WriteSharedFile(ctx, path, content); err != nil {
		outcome := failedAutomationOutcome(FailureClassValidation,
			fmt.Sprintf("outputs.to_sharedfile[%q]: %v", path, err))
		return &outcome
	}
	return nil
}

func automationSharedFileOwnerNode(runCtx RunContext) string {
	dagKey := strings.TrimSpace(runCtx.DagKey)
	nodeKey := strings.TrimSpace(runCtx.NodeKey)
	if dagKey == "" || nodeKey == "" {
		return ""
	}
	return dagKey + "/" + nodeKey
}

func automationSharedFileProducerActor(runCtx RunContext) string {
	nodeKey := strings.TrimSpace(runCtx.NodeKey)
	if nodeKey == "" {
		return ""
	}
	return "automation:" + nodeKey
}
