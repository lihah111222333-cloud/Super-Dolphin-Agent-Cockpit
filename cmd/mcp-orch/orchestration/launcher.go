package orchestration

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/processctl"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// AgentLauncher 负责真正启动、停止和提交 turn。
// service 只管状态；本地进程还是控制面 thread/start，由 launcher 决定。
type AgentLauncher interface {
	Launch(ctx context.Context, agent *agentRuntime, req LaunchRequest) (LaunchResult, error)
	Stop(ctx context.Context, agent *agentRuntime) error
	Archive(ctx context.Context, agent *agentRuntime) error
	SubmitTurn(ctx context.Context, agent *agentRuntime, submission TurnSubmission) (string, error)
	IsRunning(ctx context.Context, agent *agentRuntime) bool
}

type LaunchResult struct {
	ThreadID, RemoteAgentID string
}

// localLauncher handles the local process mode while leaving runtime fields on agentRuntime.
// 本地模式直接拉子进程，没有 remote thread id。
type localLauncher struct {
	turnStarter TurnStarter
	logger      *slog.Logger
}

// NewLocalLauncher 创建本地代理启动器。
func NewLocalLauncher(turnStarter TurnStarter, logger *slog.Logger) AgentLauncher {
	return &localLauncher{turnStarter: turnStarter, logger: logger}
}

// Launch 启动代理会话并记录运行句柄。
func (l *localLauncher) Launch(ctx context.Context, agent *agentRuntime, _ LaunchRequest) (LaunchResult, error) {
	if agent == nil {
		return LaunchResult{}, errors.New("agent is required")
	}
	if len(agent.command) == 0 {
		return LaunchResult{}, errors.New("command is required")
	}
	cmd := exec.Command(agent.command[0], agent.command[1:]...)
	cmd.Dir = agent.cwd
	cmd.Env = append(os.Environ(), agent.env...)
	processctl.Configure(cmd)
	if err := cmd.Start(); err != nil {
		agent.lastError = err.Error()
		return LaunchResult{}, err
	}
	guard := processctl.Attach(cmd, l.logger)
	now := resolveEventTime(ctx, agent.updatedAt)
	resetLaunchState(agent)
	agent.cmd = cmd
	agent.processGuard = guard
	agent.launchSeq++
	agent.startedAt = now
	agent.updatedAt = now
	if l != nil && l.logger != nil {
		l.logger.Info("orchestration: agent launched", "agent_id", agent.id, "pid", cmd.Process.Pid)
	}
	return LaunchResult{}, nil
}

// Stop 停止运行中的代理会话。
func (l *localLauncher) Stop(_ context.Context, agent *agentRuntime) error {
	if agent == nil {
		return nil
	}
	return processctl.RequestStop(agent.cmd, agent.processGuard)
}

// Archive 归档代理线程并释放运行态。
func (l *localLauncher) Archive(ctx context.Context, agent *agentRuntime) error {
	return l.Stop(ctx, agent)
}

// SubmitTurn 向已启动的代理会话提交一轮输入。
func (l *localLauncher) SubmitTurn(ctx context.Context, _ *agentRuntime, submission TurnSubmission) (string, error) {
	if l == nil || l.turnStarter == nil {
		return "", errors.New("turn starter is not configured")
	}
	return l.turnStarter.StartTurn(ctx, submission)
}

// IsRunning 检查指定代理会话是否仍在运行。
func (l *localLauncher) IsRunning(_ context.Context, agent *agentRuntime) bool {
	return agent != nil && agent.cmd != nil
}

type remoteLauncher struct {
	addr      string
	mu        sync.Mutex
	client    *jrpc2.Client
	eventSink remoteLauncherEventSink
}

// remoteLauncher 通过 GO_AGENT_CTL_RPC_ADDR 调主控的 thread/* RPC。
// 它不直接执行 agent，只拿回 thread_id 再交给 service 记录。
func NewRemoteLauncher(addr string) AgentLauncher {
	return &remoteLauncher{addr: strings.TrimSpace(addr)}
}

// 复用到主控的 jrpc2 连接；并发重连时只保留一个 client。
// OnNotify 会把 remote turn 事件送回 service。
func (r *remoteLauncher) ensureClient(ctx context.Context) (*jrpc2.Client, error) {
	if r == nil || strings.TrimSpace(r.addr) == "" {
		return nil, errors.New("remote launcher rpc addr is required")
	}
	r.mu.Lock()
	client := r.client
	r.mu.Unlock()
	if client != nil && !client.IsStopped() {
		return client, nil
	}
	raw, err := new(net.Dialer).DialContext(ctx, "tcp", r.addr)
	if err != nil {
		return nil, err
	}
	next := jrpc2.NewClient(channel.Line(raw, raw), &jrpc2.ClientOptions{
		OnNotify: r.handleNotify,
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	if client = r.client; client == nil || client.IsStopped() {
		if client != nil {
			_ = client.Close()
		}
		r.client = next
		return next, nil
	}
	_ = next.Close()
	return client, nil
}

func rpcCall[T any](ctx context.Context, r *remoteLauncher, method string, params any) (out T, err error) {
	callCtx, cancel := platformconfig.WithRPCRequestTimeout(ctx)
	defer cancel()
	client, err := r.ensureClient(callCtx)
	if err == nil {
		err = client.CallResult(callCtx, method, params, &out)
	}
	return out, err
}

func rpcString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func managedAgentLaunchDisplayName(name string) string {
	name = strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
	name = strings.Trim(name, "`\"'“”‘’[]()（）【】")
	return strings.TrimSpace(name)
}

// Launch 启动代理会话并记录运行句柄。
func (r *remoteLauncher) Launch(ctx context.Context, agent *agentRuntime, req LaunchRequest) (LaunchResult, error) {
	if agent == nil {
		return LaunchResult{}, errors.New("agent is required")
	}
	start := time.Now()
	pkglogger.Info("remoteLauncher: thread/start RPC begin", "agent_id", agent.id, "rpc_addr", r.addr)
	// thread/start treats `prompt` and `name` as legacy aliases for the same
	// display-name slot. Name is intentionally sourced only from req.Name; the
	// launch prompt is submitted as a first turn after thread/start.
	// 不要把 req.Prompt 塞进 thread/start；任务正文会在启动后作为第一轮 turn 提交。
	displayName := managedAgentLaunchDisplayName(req.Name)
	model := shared.FirstTrimmed(envValue(req.Env, "AGENT_MODEL"), commandFlagValue(launchCommandArgs(req.Command), "--model"))
	effort := shared.FirstTrimmed(envValue(req.Env, "AGENT_EFFORT"), commandFlagValue(launchCommandArgs(req.Command), "--effort"))
	pkglogger.Debug("remoteLauncher: thread/start config trace",
		"agent_id", agent.id,
		"provider", launchProvider(req),
		"model", model,
		"effort", effort,
		"env_has_effort", envValue(req.Env, "AGENT_EFFORT") != "",
		"env_has_disabled_tools", envValue(req.Env, "AGENT_DISABLED_TOOLS") != "",
	)
	params := map[string]any{
		LauncherParamAgentID:          strings.TrimSpace(agent.id),
		LauncherParamCwd:              strings.TrimSpace(req.Cwd),
		LauncherParamName:             displayName,
		LauncherParamAgentType:        strings.TrimSpace(req.AgentType),
		LauncherParamAgentKey:         strings.TrimSpace(req.AgentKey),
		LauncherParamPromptKey:        strings.TrimSpace(req.PromptKey),
		LauncherParamAgentMemoryScope: strings.TrimSpace(req.MemoryScope),
		LauncherParamParentAgentID:    strings.TrimSpace(req.ParentID),
		LauncherParamBaseInstructions: strings.TrimSpace(req.Instructions),
		LauncherParamProvider:         launchProvider(req),
		LauncherParamModel:            model,
		LauncherParamEffort:           effort,
		LauncherParamLanguage:         strings.TrimSpace(req.Language),
	}
	config := map[string]any{}
	if disabledTools := envValue(req.Env, "AGENT_DISABLED_TOOLS"); disabledTools != "" {
		config["disallowed_tools"] = disabledTools
	}
	if codexHome := envValue(req.Env, "AGENT_CODEX_HOME"); codexHome != "" {
		config["codexHome"] = codexHome
	}
	if codexInstanceKey := envValue(req.Env, "AGENT_CODEX_INSTANCE_KEY"); codexInstanceKey != "" {
		config["codexInstanceKey"] = codexInstanceKey
	}
	if codexModelProvider := envValue(req.Env, "AGENT_CODEX_MODEL_PROVIDER"); codexModelProvider != "" {
		config["codexModelProvider"] = codexModelProvider
	}
	if len(config) > 0 {
		params[LauncherParamConfig] = config
	}
	resp, err := rpcCall[map[string]any](ctx, r, LauncherMethodThreadStart, params)
	elapsed := time.Since(start)
	if err != nil {
		pkglogger.Warn("remoteLauncher: thread/start RPC failed",
			"agent_id", agent.id, "elapsed", elapsed, "error", err)
		return LaunchResult{}, err
	}
	if elapsed > 5*time.Second {
		pkglogger.Warn("remoteLauncher: thread/start RPC slow",
			"agent_id", agent.id, "elapsed", elapsed)
	}
	thread, _ := resp[LauncherRespThread].(map[string]any)
	result := LaunchResult{
		ThreadID:      resolveLauncherThreadStartAlias(thread, resp, launcherThreadStartThreadIDAliases, ""),
		RemoteAgentID: resolveLauncherThreadStartAlias(nil, resp, launcherThreadStartAgentIDAliases, agent.id),
	}
	if result.ThreadID == "" {
		return LaunchResult{}, errors.New("remote launcher: empty thread id")
	}
	now := resolveEventTime(ctx, agent.updatedAt)
	resetLaunchState(agent)
	agent.launchSeq++
	agent.threadID = result.ThreadID
	agent.remoteThreadID = result.ThreadID
	agent.remoteAgentID = result.RemoteAgentID
	agent.startedAt = now
	agent.updatedAt = now
	return result, nil
}

// Stop 停止运行中的代理会话。
func (r *remoteLauncher) Stop(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.remoteThreadID == "" {
		return nil
	}
	_, err := rpcCall[struct{}](ctx, r, LauncherMethodThreadStop, map[string]string{LauncherParamThreadID: agent.remoteThreadID})
	return err
}

// StopSettlesAgent 说明 Stop 是否会把代理状态收敛到终态。
func (r *remoteLauncher) StopSettlesAgent() bool { return true }

// Archive 归档代理线程并释放运行态。
func (r *remoteLauncher) Archive(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.remoteThreadID == "" {
		return nil
	}
	_, err := rpcCall[struct{}](ctx, r, LauncherMethodThreadArchive,
		map[string]string{LauncherParamThreadID: agent.remoteThreadID})
	return err
}

// SubmitTurn 向已启动的代理会话提交一轮输入。
func (r *remoteLauncher) SubmitTurn(ctx context.Context, agent *agentRuntime, submission TurnSubmission) (string, error) {
	if agent == nil || agent.remoteThreadID == "" {
		return "", errors.New("remote thread id is required")
	}
	params := map[string]any{
		LauncherParamThreadID:             agent.remoteThreadID,
		LauncherParamInput:                submission.Inputs,
		LauncherParamSelectedSkills:       submission.SelectedSkills,
		LauncherParamManualSkillSelection: submission.ManualSkillSelection,
	}
	if len(submission.OutputSchema) > 0 {
		params[LauncherParamOutputSchema] = submission.OutputSchema
	}
	resp, err := rpcCall[map[string]any](ctx, r, LauncherMethodTurnStart, params)
	if err != nil {
		return "", err
	}
	turnID := rpcString(resp[LauncherRespTurnID])
	if turnID == "" {
		return "", errors.New("remote launcher: empty turn id")
	}
	return turnID, nil
}

// IsRunning 检查指定代理会话是否仍在运行。
func (r *remoteLauncher) IsRunning(_ context.Context, agent *agentRuntime) bool {
	return agent != nil && agent.remoteThreadID != ""
}

// SupportsPersistedRuntimeRehydrate 说明启动器是否支持从持久化状态恢复 runtime。
func (r *remoteLauncher) SupportsPersistedRuntimeRehydrate() bool {
	return true
}

// Close 关闭启动器并释放后台资源。
func (r *remoteLauncher) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil {
		return nil
	}
	err := r.client.Close()
	r.client = nil
	return err
}

type remoteLauncherEventSink interface {
	handleRemoteTurnCompleted(context.Context, turndto.TurnCompleted)
	handleRemoteTurnInterrupted(context.Context, turndto.TurnInterrupted)
}

func bindRemoteLauncherEventSink(launcher AgentLauncher, sink remoteLauncherEventSink) {
	if binder, ok := launcher.(interface{ bindRemoteEventSink(remoteLauncherEventSink) }); ok {
		binder.bindRemoteEventSink(sink)
	}
}

func (r *remoteLauncher) bindRemoteEventSink(sink remoteLauncherEventSink) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.eventSink = sink
	r.mu.Unlock()
}

func (r *remoteLauncher) currentEventSink() remoteLauncherEventSink {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.eventSink
}

// 主控推送 turn.completed / turn.interrupted 时，这里只解码并交给 service。
// launcher 不直接改 agentRuntime。
func (r *remoteLauncher) handleNotify(req *jrpc2.Request) {
	if r == nil || req == nil {
		return
	}
	sink := r.currentEventSink()
	if sink == nil {
		return
	}
	switch method := strings.TrimSpace(req.Method()); method {
	case eventsurface.MethodTurnCompleted:
		ev, err := eventsurface.DecodeRemoteTurnCompleted(req.UnmarshalParams)
		if logRemoteLauncherNotifyDecodeError(method, err) {
			return
		}
		sink.handleRemoteTurnCompleted(context.Background(), ev)
	case eventsurface.MethodTurnInterrupted:
		ev, err := eventsurface.DecodeRemoteTurnInterrupted(req.UnmarshalParams)
		if logRemoteLauncherNotifyDecodeError(method, err) {
			return
		}
		sink.handleRemoteTurnInterrupted(context.Background(), ev)
	}
}

func logRemoteLauncherNotifyDecodeError(method string, err error) bool {
	if err == nil {
		return false
	}
	pkglogger.Warn("remoteLauncher: push notification decode failed",
		"method", strings.TrimSpace(method),
		"error", err)
	return true
}

// handleRemoteTurnCompleted 处理远端 turn 完成事件并同步本地运行态。
func (s *service) handleRemoteTurnCompleted(ctx context.Context, ev turndto.TurnCompleted) {
	if s == nil || !s.hasRuntimeAgent(ev.AgentID) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	eventCtx := withEventTime(ctx, ev.Timestamp)
	_, err := s.HandleReportEvent(eventCtx, ReportEvent{
		AgentID:   strings.TrimSpace(ev.AgentID),
		Report:    turnCompletedReportText(ev),
		EventType: eventsurface.MethodTurnCompleted,
		EventData: mustMarshalHookReportEvent(ev),
	})
	if err != nil && !errors.Is(err, errAgentNotFound) {
		pkglogger.Warn("orchestration: remote turn completion report failed", "agent_id", strings.TrimSpace(ev.AgentID), "thread_id", strings.TrimSpace(ev.ThreadID), "turn_id", strings.TrimSpace(ev.TurnID), "error", err)
	}
	lifecycle := ev
	lifecycle.TurnID = ""
	handleTurnCompletedEventWithCtx(s, s.logger, lifecycle, eventCtx)
}

// handleRemoteTurnInterrupted 处理远端 turn 中断事件并同步本地运行态。
func (s *service) handleRemoteTurnInterrupted(ctx context.Context, ev turndto.TurnInterrupted) {
	if s == nil || !s.hasRuntimeAgent(ev.AgentID) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	eventCtx := withEventTime(ctx, ev.Timestamp)
	reason := strings.TrimSpace(ev.Reason)
	report := ""
	if reason != "" {
		report = "turn failed: " + reason
	}
	if _, err := s.HandleReportEvent(eventCtx, ReportEvent{AgentID: strings.TrimSpace(ev.AgentID), Report: report, EventType: "turn.aborted", EventData: mustMarshalHookReportEvent(ev)}); err != nil && !errors.Is(err, errAgentNotFound) {
		pkglogger.Warn("orchestration: remote turn interruption report failed", "agent_id", strings.TrimSpace(ev.AgentID), "thread_id", strings.TrimSpace(ev.ThreadID), "turn_id", strings.TrimSpace(ev.TurnID), "error", err)
	}
	lifecycle := ev
	lifecycle.TurnID = ""
	handleTurnInterruptedEventWithCtx(s, s.logger, lifecycle, eventCtx)
}

func (s *service) stopAgentAfterPermanentTurnFailure(agentID, threadID, source string) {
	cleanupCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.AsyncLaunchTimeout)
	defer cancel()
	if err := s.stopAgentViaLauncher(cleanupCtx, strings.TrimSpace(agentID), source); err != nil {
		pkglogger.Warn("orchestration: permanent turn failure cleanup stop failed", "agent_id", strings.TrimSpace(agentID), "thread_id", strings.TrimSpace(threadID), "source", source, "error", err)
	}
}
