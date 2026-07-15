package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// AutomationExecutor 执行 node_type=automation 节点。
// 它通过 command_get 读取命令卡，再由 runner 在受限工作区内执行；inputs 会注入到 args.__inputs，
// outputs 负责写入 node.result 或 sharedfile。automation 不能通过 outputs 注入 agent prompt 或路由字段。
type AutomationExecutor struct {
	commandGetter AutomationCommandGetter
	runner        AutomationCommandRunner
	hooks         map[HookPoint]HookHandler
}

// AutomationCommandGetter 读取 command card 定义。
// executor 只依赖该窄端口，不直接访问 command store 或 transport。
type AutomationCommandGetter interface {
	GetCommandCard(ctx context.Context, cardKey string) (AutomationCommandCard, error)
}

// AutomationCommandRunner 在受限工作区内执行 command card。
// 调用方必须显式传入 cwd、workspace roots 和环境边界，缺失时执行层应失败。
type AutomationCommandRunner interface {
	RunCommandCard(ctx context.Context, card AutomationCommandCard, args json.RawMessage, opts ...AutomationCommandRunOptions) (AutomationCommandResult, error)
}

// AutomationCommandCard 是 automation 节点可执行命令模板的最小 DTO。
// 风险级别和 enabled 标记由 runner 校验，不能在模板渲染阶段绕过。
type AutomationCommandCard struct {
	CardKey         string          `json:"card_key"`
	Title           string          `json:"title,omitempty"`
	Description     string          `json:"description,omitempty"`
	CommandTemplate string          `json:"command_template"`
	ArgsSchema      json.RawMessage `json:"args_schema,omitempty"`
	RiskLevel       string          `json:"risk_level,omitempty"`
	Enabled         bool            `json:"enabled"`
}

// AutomationCommandResult 是 command card 执行后的原始结果。
// 对外返回或落库前必须经过 scrub，避免把 args 或敏感输出直接暴露。
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

// AutomationOption 在构造 automation executor 时注入可选能力。
// 每个 option 只能改写 executor 端口或 hook，不携带跨 run 状态。
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

type executionTimeoutContextKey struct{}

// WithExecutionTimeout 把 router 解析出的 DAG 默认 timeout 放入执行 context。
// 节点自身 execution.timeout 仍在 AutomationExecutor 内解析并覆盖该默认值。
func WithExecutionTimeout(ctx context.Context, timeout time.Duration) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx
	}
	return context.WithValue(ctx, executionTimeoutContextKey{}, timeout)
}

func executionTimeoutFromContext(ctx context.Context) time.Duration {
	timeout, _ := ctx.Value(executionTimeoutContextKey{}).(time.Duration)
	return timeout
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

	commandCtx, cancel, failure := automationCommandContext(ctx, cfg)
	if failure != nil {
		return *failure, nil
	}
	defer cancel()

	result, err := e.runner.RunCommandCard(commandCtx, card, runArgs, automationCommandRunOptionsFromConfig(cfg))
	if err != nil {
		return failedAutomationOutcome(classifyAutomationError(err), "run command card: "+err.Error()), nil
	}
	return finalizeAutomationOutcome(ctx, cfg, node, runCtx, result)
}

// automationCommandContext 把有效 execution timeout 施加到命令运行上下文。
// 节点配置优先；直接调用 executor 时仍能从 node.config 生效，避免仅 router 路径有超时。
func automationCommandContext(ctx context.Context, cfg *AutomationNodeConfig) (context.Context, context.CancelFunc, *NodeOutcome) {
	timeout := executionTimeoutFromContext(ctx)
	if cfg != nil {
		nodeTimeout, ok, err := cfg.Execution.ExecutionTimeout()
		if err != nil {
			outcome := failedAutomationOutcome(FailureClassValidation, "automation execution timeout invalid: "+err.Error())
			return ctx, func() {}, &outcome
		}
		if ok {
			timeout = nodeTimeout
		}
	}
	if timeout <= 0 {
		return ctx, func() {}, nil
	}
	commandCtx, cancel := platformconfig.WithTimeout(ctx, timeout)
	return commandCtx, cancel, nil
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
		"database", "sqlite", "sql", "transport unavailable", "service unavailable", "bad gateway", "gateway timeout", "http 500", "http 502", "http 503", "http 504",
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
