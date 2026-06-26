package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idgen"
)

// AgentExecutor 执行 node_type=agent 节点：解析 config.exec、装配输入前缀、启动子 agent，
// 并把启动失败归类进 NodeOutcome，供 dispatcher 按失败类别决定重试或终止。
// 成功拿到 child thread_id 后会通过 NodeSpawnRecorder 写回节点行；生产 wiring 缺 recorder
// 会在上层构造路径拦截，直接构造器的 nil recorder 仅用于不需要持久化写回的单测。
type AgentExecutor struct {
	launcher AgentLauncher
	recorder NodeSpawnRecorder
	hooks    map[HookPoint]HookHandler
}

// AgentLauncher 是 AgentExecutor 启动子 agent 的最小端口。
// 入参沿用 orchestration 既有的 contract.LaunchRequest；返回的 threadID 是写回
// task_dag_nodes.spawning_thread_id 的唯一来源，空串会被记录路径拒绝，避免覆盖旧值。
type AgentLauncher interface {
	LaunchAgent(ctx context.Context, req contract.LaunchRequest) (threadID string, err error)
}

var ErrDAGAgentRequiresRemoteLauncher = errors.New("DAG agent launch requires remote launcher")

type DAGAgentLaunchContractValidator interface {
	ValidateDAGAgentLaunch(ctx context.Context, req contract.LaunchRequest, dagKey, nodeKey string) error
}

var ErrSpawnWritebackFailed = errors.New("agent spawn write-back failed")

type AgentLauncherWithSpawnRecord interface {
	LaunchAgentWithSpawnRecord(ctx context.Context, req contract.LaunchRequest, record func(threadID string) error) (threadID string, err error)
}

type LaunchedThreadStopper interface {
	StopLaunchedThread(ctx context.Context, threadID string) error
}

// NodeSpawnRecorder 持久化 child thread_id 与 DAG 节点的绑定。
// runID 把写回限定到当前运行，避免重试或并发 run 把其它节点行误覆盖。
type NodeSpawnRecorder interface {
	RecordNodeSpawn(ctx context.Context, dagKey, nodeKey string, runID int64, threadID string) error
}

// Option 在构造期注入 AgentExecutor 的可选端口。
// inputs 数据不走构造器，而是随每次 Execute 的 RunContext 传入，避免跨 run 状态泄漏。
type Option func(*AgentExecutor)

// WithRecorder 注入 child thread_id 写回端口。
// 生产路径应提供 recorder；省略时 Execute 只做启动，不会持久化 spawn 关系。
func WithRecorder(recorder NodeSpawnRecorder) Option {
	return func(e *AgentExecutor) { e.recorder = recorder }
}

// WithHooks 注册节点执行前后的扩展钩子。
// hook 失败由 router 层记录，不改变节点自身的执行结果，避免观测逻辑反向影响调度。
func WithHooks(hooks map[HookPoint]HookHandler) Option {
	return func(e *AgentExecutor) { e.hooks = cloneHookHandlers(hooks) }
}

// NewAgentExecutor 构造 agent 节点执行器。
// launcher 为 nil 时不 panic，Execute 会把缺失 wiring 归为 validation 失败，
// 让 dispatcher 以正常节点失败路径收敛，而不是让运行循环崩溃。
func NewAgentExecutor(launcher AgentLauncher, opts ...Option) *AgentExecutor {
	e := &AgentExecutor{launcher: launcher}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	return e
}

// Execute 运行 agent 节点并返回节点级 outcome。
// 配置错误、输入读取失败和启动失败都通过带 FailureClass 的 NodeOutcome 表达；
// error 通道只留给框架级中断或无法归属到节点的故障。
func (e *AgentExecutor) Execute(ctx context.Context, node Node, runCtx RunContext) (NodeOutcome, error) {
	if ctx == nil {
		// nil ctx 兜底：与 dispatcher.ProcessBatch / Tick 一致的防御式取默认值。
		ctx = context.Background()
	}

	// 解码 typed config，失败时归为 validation，阻止错误配置进入 launcher。
	cfg, parseErr := ParseAgentConfig(node.Config)
	if parseErr != nil {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: truncateErrSummary("decode agent config: " + parseErr.Error()),
		}, nil
	}
	if cfg == nil {
		err := errors.New("decode agent config: nil parsed config")
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: err.Error(),
		}, nil
	}
	if failure := validateAgentOutputs(node.Config, cfg); failure != nil {
		return *failure, nil
	}

	if failure := normalizeAgentLaunchConfig(cfg); failure != nil {
		return *failure, nil
	}

	// 装载上游节点结果与 sharedfile 内容作为 prompt 前缀。
	// 缺引用属于配置错误；读取端口异常保持原始分类，交给 dispatcher 的失败策略处理。
	inputsPrefix, ierr := e.assembleInputs(ctx, cfg, runCtx, node)
	if ierr != nil {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: ierr.Class,
			ErrorSummary: truncateErrSummary("assemble inputs: " + ierr.Error()),
		}, nil
	}

	// launcher 缺失说明生产 wiring 不完整，作为 validation 失败暴露给调用方。
	if e.launcher == nil {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: "agent executor: launcher not wired",
		}, nil
	}

	// 构造 LaunchRequest 并启动子 agent；inputsPrefix 必须在 first_turn 之前，保证上游上下文先进入模型。
	req := buildLaunchRequestFromAgentConfig(cfg, node, runCtx)
	req.Prompt = composePrompt(inputsPrefix, artifactOutputContract(cfg.Outputs.ToArtifact), cfg.FirstTurn)
	threadID, launchErr := e.launchAgent(ctx, req, node, runCtx)
	return e.agentLaunchOutcome(ctx, node, runCtx, threadID, launchErr), nil
}

func (e *AgentExecutor) agentLaunchOutcome(
	ctx context.Context,
	node Node,
	runCtx RunContext,
	threadID string,
	launchErr error,
) NodeOutcome {
	if launchErr == nil {
		return e.successfulAgentLaunchOutcome(ctx, node, runCtx, threadID)
	}
	if errors.Is(launchErr, ErrSpawnWritebackFailed) || errors.Is(launchErr, ErrDAGAgentRequiresRemoteLauncher) {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassHard,
			ErrorSummary: truncateErrSummary(launchErr.Error()),
		}
	}
	if failure := agentLaunchCWDValidationFailure(launchErr); failure != nil {
		return *failure
	}
	return classifiedAgentLaunchFailure(launchErr)
}

func (e *AgentExecutor) successfulAgentLaunchOutcome(ctx context.Context, node Node, runCtx RunContext, threadID string) NodeOutcome {
	if e.usesPrePromptSpawnRecord() {
		return finalizeAgentLaunchOutcome("")
	}
	return finalizeAgentLaunchOutcome(e.spawnWriteback(ctx, node, runCtx, threadID))
}

func classifiedAgentLaunchFailure(launchErr error) NodeOutcome {
	class := classifyAgentLaunchError(launchErr)
	return NodeOutcome{
		Status:       NodeStatusFailed,
		FailureClass: class,
		ErrorSummary: truncateErrSummary(fmt.Sprintf("launch agent: %v", launchErr)),
	}
}

// Hooks 返回 executor 当前注册的生命周期钩子副本。
// 返回副本是为了避免外部调用方改写内部 map，nil 表示该节点类型没有扩展钩子。
func (e *AgentExecutor) Hooks() map[HookPoint]HookHandler {
	if e == nil {
		return nil
	}
	return cloneHookHandlers(e.hooks)
}

// HasSpawnRecorder 暴露 recorder wiring 状态，供启动路径在进入 DAG 写回前做 fail-fast 校验。
func (e *AgentExecutor) HasSpawnRecorder() bool {
	return e != nil && e.recorder != nil
}

var agentOutputsForbiddenKeys = []string{"webhook_url", "command_ref"}

func normalizeAgentLaunchConfig(cfg *AgentNodeConfig) *NodeOutcome {
	if failure := validateAgentLaunchIdentity(cfg); failure != nil {
		return failure
	}
	provider, err := validateAgentLaunchProvider(cfg.Exec.Provider)
	if err != nil {
		return &NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: truncateErrSummary(err.Error()),
		}
	}
	cfg.Exec.Provider = provider
	if provider == "codex" {
		if failure := validateCodexIdentityOverride(cfg.Exec); failure != nil {
			return failure
		}
	}
	return agentLaunchCWDValidationFailure(contract.ValidateLaunchCWD(cfg.Exec.CWD, ""))
}

func validateAgentOutputs(raw json.RawMessage, _ *AgentNodeConfig) *NodeOutcome {
	return validateOutputsForbiddenKeys(raw, agentOutputsForbiddenKeys, func(key string) NodeOutcome {
		return NodeOutcome{
			Status:       NodeStatusFailed,
			FailureClass: FailureClassValidation,
			ErrorSummary: truncateErrSummary(fmt.Sprintf("agent outputs cannot include external capability field %q", key)),
		}
	})
}

func validateAgentLaunchIdentity(cfg *AgentNodeConfig) *NodeOutcome {
	if strings.TrimSpace(cfg.Exec.AgentKey) != "" || strings.TrimSpace(cfg.Exec.PromptKey) != "" {
		return nil
	}
	return &NodeOutcome{
		Status:       NodeStatusFailed,
		FailureClass: FailureClassValidation,
		ErrorSummary: "agent_key or prompt_key required in node.config.exec",
	}
}

// validateCodexIdentityOverride 校验 Codex 身份覆盖字段必须成组出现。
// 只配置其中一部分会让 provider home、实例 key 与 model provider 脱节，因此作为 validation 失败阻断。
func validateCodexIdentityOverride(exec AgentExecConfig) *NodeOutcome {
	home := strings.TrimSpace(exec.CodexHome)
	instanceKey := strings.TrimSpace(exec.CodexInstanceKey)
	modelProvider := strings.TrimSpace(exec.CodexModelProvider)
	if home == "" && instanceKey == "" && modelProvider == "" {
		return nil
	}
	missing := make([]string, 0, 3)
	if home == "" {
		missing = append(missing, "codex_home")
	}
	if instanceKey == "" {
		missing = append(missing, "codex_instance_key")
	}
	if modelProvider == "" {
		missing = append(missing, "codex_model_provider")
	}
	if len(missing) == 0 {
		return nil
	}
	return &NodeOutcome{
		Status:       NodeStatusFailed,
		FailureClass: FailureClassValidation,
		ErrorSummary: "codex identity requires " + strings.Join(missing, ", "),
	}
}

func agentLaunchCWDValidationFailure(err error) *NodeOutcome {
	if !errors.Is(err, contract.ErrLaunchCWDRequired) && !errors.Is(err, contract.ErrLaunchCWDInvalid) {
		return nil
	}
	return &NodeOutcome{
		Status:       NodeStatusFailed,
		FailureClass: FailureClassValidation,
		ErrorSummary: truncateErrSummary(err.Error()),
	}
}

func finalizeAgentOutcome(errorSummary string) NodeOutcome {
	return NodeOutcome{Status: NodeStatusDone, ErrorSummary: errorSummary}
}

func finalizeAgentLaunchOutcome(errorSummary string) NodeOutcome {
	if errorSummary == "" {
		return finalizeAgentOutcome("")
	}
	return NodeOutcome{Status: NodeStatusFailed, FailureClass: FailureClassHard, ErrorSummary: errorSummary}
}

func (e *AgentExecutor) launchAgent(
	ctx context.Context,
	req contract.LaunchRequest,
	node Node,
	runCtx RunContext,
) (string, error) {
	if err := e.validateDAGAgentLauncherContract(ctx, req, node, runCtx); err != nil {
		return "", err
	}
	if e.usesPrePromptSpawnRecord() {
		launcher := e.launcher.(AgentLauncherWithSpawnRecord)
		return launcher.LaunchAgentWithSpawnRecord(ctx, req, func(threadID string) error {
			summary, _ := e.recordSpawn(ctx, node, runCtx, threadID)
			if summary == "" {
				return nil
			}
			return fmt.Errorf("%w: %s", ErrSpawnWritebackFailed, summary)
		})
	}
	return e.launcher.LaunchAgent(ctx, req)
}

func (e *AgentExecutor) validateDAGAgentLauncherContract(
	ctx context.Context,
	req contract.LaunchRequest,
	node Node,
	runCtx RunContext,
) error {
	validator, ok := e.launcher.(DAGAgentLaunchContractValidator)
	if !ok {
		return nil
	}
	dagKey, nodeKey := resolveSpawnKeys(node, runCtx)
	return validator.ValidateDAGAgentLaunch(ctx, req, dagKey, nodeKey)
}

func (e *AgentExecutor) usesPrePromptSpawnRecord() bool {
	if e == nil || e.recorder == nil {
		return false
	}
	_, ok := e.launcher.(AgentLauncherWithSpawnRecord)
	return ok
}

// spawnWriteback 在子 agent 启动成功后写回 spawning_thread_id。
// 返回空串表示无需改变成功 outcome；返回非空摘要表示持久化边界失败，调用方会把启动成功降为 hard failure，
// 防止已经启动的子线程失去 DAG 节点归属。
func (e *AgentExecutor) spawnWriteback(ctx context.Context, node Node, runCtx RunContext, threadID string) string {
	summary, cause := e.recordSpawn(ctx, node, runCtx, threadID)
	if cause != nil {
		return e.stopLaunchedThreadAfterWritebackFailure(ctx, strings.TrimSpace(threadID), cause)
	}
	return summary
}

// recordSpawn 将 child thread_id 写入当前 DAG run 的节点行。
// recorder 未配置时保持 launch-only 行为；但 threadID 或 DAG key 缺失会返回摘要，提醒调用方不要把未绑定线程当作成功节点。
func (e *AgentExecutor) recordSpawn(ctx context.Context, node Node, runCtx RunContext, threadID string) (string, error) {
	if e.recorder == nil {
		return "", nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "spawning_thread_id write-back skipped: launcher returned empty thread_id", nil
	}
	dagKey, nodeKey := resolveSpawnKeys(node, runCtx)
	if dagKey == "" || nodeKey == "" {
		return "spawning_thread_id write-back skipped: missing dag_key or node_key", nil
	}
	if err := e.recorder.RecordNodeSpawn(ctx, dagKey, nodeKey, runCtx.RunID, threadID); err != nil {
		return fmt.Sprintf("spawning_thread_id write-back failed: %v", err), err
	}
	return "", nil
}

func (e *AgentExecutor) stopLaunchedThreadAfterWritebackFailure(ctx context.Context, threadID string, cause error) string {
	summary := fmt.Sprintf("spawning_thread_id write-back failed: %v", cause)
	stopper, ok := e.launcher.(LaunchedThreadStopper)
	if !ok {
		return truncateErrSummary(summary)
	}
	if err := stopper.StopLaunchedThread(ctx, threadID); err != nil {
		return truncateErrSummary(summary + "; stop launched thread failed: " + err.Error())
	}
	return truncateErrSummary(summary + "; launched thread stopped")
}

// resolveSpawnKeys 取写回所需的 dagKey 与 nodeKey。
// RunContext 是 dispatcher 传入的运行时真值；node 字段仅作兼容补充，两处都缺失时上层会把写回视为失败摘要。
func resolveSpawnKeys(node Node, runCtx RunContext) (string, string) {
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

// buildLaunchRequestFromAgentConfig 把 AgentNodeConfig 映射到 contract.LaunchRequest。
// 命名/字段语义与 wakeup_dispatcher.go::buildLaunchRequestFromWakeup 同源，避免
// 两条 launch 入口（dispatcher vs NodeExecutor）行为漂移。
//
// 关键映射：
//   - AgentID 使用项目统一 agent_{monotonicNumericTimestamp} 生成器；
//   - Name 取 node.Title，去掉控制符并限制长度，供日志/UI 展示；
//   - AgentKey/PromptKey/Language 直填同名字段；
//   - Provider/Model/Effort 通过 LaunchRequest.Env 传递给 remoteLauncher；
//   - CWD 取 cfg.exec.cwd；
//   - FirstTurn 作为初始 Prompt 注入。
func buildLaunchRequestFromAgentConfig(cfg *AgentNodeConfig, node Node, _ RunContext) contract.LaunchRequest {
	if cfg == nil {
		return contract.LaunchRequest{}
	}
	req := contract.LaunchRequest{
		AgentID:   idgen.NewAgentID(),
		Name:      sanitizeLaunchName(node.Title),
		AgentKey:  strings.TrimSpace(cfg.Exec.AgentKey),
		PromptKey: strings.TrimSpace(cfg.Exec.PromptKey),
		Cwd:       cfg.Exec.CWD,
		Language:  strings.TrimSpace(cfg.Exec.Language),
		Prompt:    cfg.FirstTurn,
		AgentType: node.NodeType, // 透传节点类型，供 launcher 保留 DAG 来源信息。
		Env:       agentLaunchEnv(cfg.Exec),
	}
	return req
}

func validateAgentLaunchProvider(raw string) (string, error) {
	provider := strings.ToLower(strings.TrimSpace(raw))
	switch provider {
	case "codex":
		return provider, nil
	case "":
		return "", errors.New("provider required in node.config.exec: set provider to codex")
	default:
		return "", fmt.Errorf("invalid provider %q: must be codex", strings.TrimSpace(raw))
	}
}

// agentLaunchEnv 将 agent exec 配置转成 launcher 可识别的环境变量。
// 这里只写入显式配置项，避免空值覆盖 provider 侧已有身份或模型设置。
func agentLaunchEnv(exec AgentExecConfig) []string {
	env := make([]string, 0, 7)
	if provider := strings.ToLower(strings.TrimSpace(exec.Provider)); provider != "" {
		env = append(env, "AGENT_PROVIDER="+provider)
	}
	if model := strings.TrimSpace(exec.Model); model != "" {
		env = append(env, "AGENT_MODEL="+model)
	}
	if effort := strings.TrimSpace(exec.Effort); effort != "" {
		env = append(env, "AGENT_EFFORT="+effort)
	}
	if codexHome := strings.TrimSpace(exec.CodexHome); codexHome != "" {
		env = append(env, "AGENT_CODEX_HOME="+codexHome)
	}
	if codexInstanceKey := strings.TrimSpace(exec.CodexInstanceKey); codexInstanceKey != "" {
		env = append(env, "AGENT_CODEX_INSTANCE_KEY="+codexInstanceKey)
	}
	if codexModelProvider := strings.TrimSpace(exec.CodexModelProvider); codexModelProvider != "" {
		env = append(env, "AGENT_CODEX_MODEL_PROVIDER="+codexModelProvider)
	}
	if disabledTools := joinTrimmed(exec.DisabledTools); disabledTools != "" {
		env = append(env, "AGENT_DISABLED_TOOLS="+disabledTools)
	}
	return env
}

func joinTrimmed(values []string) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return strings.Join(out, ",")
}

func sanitizeLaunchName(value string) string {
	const limit = 80
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := make([]rune, 0, len(value))
	for _, r := range value {
		if unicode.IsControl(r) {
			continue
		}
		runes = append(runes, r)
		if len(runes) == limit {
			break
		}
	}
	return strings.TrimSpace(string(runes))
}

// classifyAgentLaunchError 将 launcher 错误映射到节点失败类别。
// 匹配顺序必须保持 quota、capability、validation、transient：例如上下文超限同时带 401 时，
// 应优先提示预算/上下文问题，而不是误判为凭据配置错误。未知错误归为 transient，让重试策略接管。
func classifyAgentLaunchError(err error) FailureClass {
	if err == nil {
		return FailureClassTransient
	}
	if errors.Is(err, contract.ErrLaunchCWDRequired) || errors.Is(err, contract.ErrLaunchCWDInvalid) {
		return FailureClassValidation
	}
	msg := strings.ToLower(err.Error())
	switch {
	case launchErrorMatchesAny(msg, launchQuotaKeywords()):
		return FailureClassQuota
	case launchErrorMatchesAny(msg, launchCapabilityKeywords()):
		return FailureClassCapability
	case launchErrorMatchesAny(msg, launchValidationKeywords()):
		return FailureClassValidation
	case launchErrorIsTransient(err, msg):
		return FailureClassTransient
	default:
		return FailureClassTransient
	}
}

func launchQuotaKeywords() []string {
	return []string{
		"context_length_exceeded",
		"context length exceeded",
		"maximum context",
		"prompt is too long",
		"quota_exhausted",
		"insufficient_quota",
		"usage limit",
		"out of credits",
	}
}

func launchCapabilityKeywords() []string {
	return []string{
		"capability",
		"not capable",
		"cannot solve",
		"can't solve",
		"model too weak",
		"requires stronger model",
		"needs stronger model",
	}
}

func launchValidationKeywords() []string {
	return []string{
		"failed to load configuration", "model provider '", "model provider \"",
		"codexhome is required", "codexinstancekey is required", "codexmodelprovider is required",
		"codexhome directory does not exist", "codex identity field has invalid type or value",
		"401", "unauthoriz", "invalid api key", "invalid_api_key",
		"403", "forbidden", "permission denied",
		"402", "payment_required", "subscription expired",
		"selected model", "may not exist or you may not have access",
		"not have access to it", "pick a different model",
		"model unavailable", "model_not_found", "model not found",
	}
}

func launchErrorIsTransient(err error, msg string) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	return launchErrorMatchesAny(msg, []string{
		"deadline exceeded",
		"connection refused",
		"transport unavailable",
		"empty thread id",
		"timed out",
		"i/o timeout",
		"rate limit",
		"rate-limit",
		"rate_limit",
		"too many requests",
		"http 429",
		" 429 ",
		"status 429",
		"status: 429",
	})
}

func launchErrorMatchesAny(msg string, keywords []string) bool {
	for _, k := range keywords {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

// truncateErrSummary 将 ErrorSummary 控制在节点结果约定的 1KB 内。
// 超长消息会截断并加标记，避免单个失败摘要撑大持久化记录和 UI 列表。
func truncateErrSummary(s string) string {
	const limit = 1000
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…(truncated)"
}
