package orchestration

import (
	"context"
	"errors"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/launcherwire"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/processctl"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// AgentLauncher 负责真正启动、停止和提交 turn。
// service 只管状态；本地进程还是控制面 thread/start，由 launcher 决定。
type AgentLauncher interface {
	Launch(ctx context.Context, agent *agentRuntime, req LaunchRequest) (LaunchResult, error)
	Fork(ctx context.Context, parent *agentRuntime, child *agentRuntime, req LaunchRequest) (LaunchResult, error)
	Stop(ctx context.Context, agent *agentRuntime) error
	Archive(ctx context.Context, agent *agentRuntime) error
	Interrupt(ctx context.Context, agent *agentRuntime, source string) error
	SubmitTurn(ctx context.Context, agent *agentRuntime, submission TurnSubmission) (string, error)
	IsRunning(ctx context.Context, agent *agentRuntime) bool
}
type LaunchResult struct {
	ThreadID, RemoteAgentID string
}

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
	cmd.Env = append(contract.ScrubDatabaseEnv(os.Environ()), contract.ScrubDatabaseEnv(agent.env)...)
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

func (l *localLauncher) Fork(context.Context, *agentRuntime, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, errors.New("forked context launch requires remote launcher")
}

func (l *localLauncher) Stop(_ context.Context, agent *agentRuntime) error {
	if agent == nil {
		return nil
	}
	return processctl.RequestStop(agent.cmd, agent.processGuard)
}

func (l *localLauncher) Archive(ctx context.Context, agent *agentRuntime) error {
	return l.Stop(ctx, agent)
}

func (l *localLauncher) Interrupt(context.Context, *agentRuntime, string) error {
	return errors.New("interrupt_agent currently supports remote Codex agents only")
}

func (l *localLauncher) SubmitTurn(ctx context.Context, _ *agentRuntime, submission TurnSubmission) (string, error) {
	if l == nil || l.turnStarter == nil {
		return "", errors.New("turn starter is not configured")
	}
	return l.turnStarter.StartTurn(ctx, submission)
}

func (l *localLauncher) IsRunning(_ context.Context, agent *agentRuntime) bool {
	return agent != nil && agent.cmd != nil
}

type remoteLauncher struct {
	addr            string
	mu              sync.Mutex
	client          *jrpc2.Client
	eventSink       remoteLauncherEventSink
	instanceID      string
	heartbeatCancel context.CancelFunc
}

const remoteLauncherBinaryName = "mcp-orch-remote-launcher"
const remoteLauncherInterval = 10 * time.Second

func NewRemoteLauncher(addr string) AgentLauncher {
	return &remoteLauncher{addr: strings.TrimSpace(addr), instanceID: shared.NewID("mcp_orch_remote_launcher")}
}

func (r *remoteLauncher) ensureClient(ctx context.Context) (*jrpc2.Client, error) {
	if r == nil || strings.TrimSpace(r.addr) == "" {
		return nil, errors.New("remote launcher rpc addr is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	client := r.client
	if client != nil && !client.IsStopped() {
		return client, nil
	}
	r.stopHeartbeatLocked()
	if client != nil {
		_ = client.Close()
	}
	r.client = nil
	raw, err := new(net.Dialer).DialContext(ctx, "tcp", r.addr)
	if err != nil {
		return nil, err
	}
	next := jrpc2.NewClient(channel.Line(raw, raw), &jrpc2.ClientOptions{OnNotify: r.handleNotify})
	reg, err := r.registerClient(ctx, next)
	if err != nil {
		_ = next.Close()
		return nil, err
	}
	r.client = next
	r.startHeartbeatLocked(next, reg)
	return next, nil
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

func (r *remoteLauncher) registerClient(ctx context.Context, client *jrpc2.Client) (mcpdto.RegisterResponse, error) {
	token := remoteLauncherSessionToken()
	if token == "" {
		return mcpdto.RegisterResponse{}, errors.New("remote launcher requires GO_AGENT_CTL_SESSION_TOKEN or GO_AGENT_MCP_SESSION_TOKEN")
	}
	r.instanceID = shared.FirstTrimmed(r.instanceID, shared.NewID("mcp_orch_remote_launcher"))
	callCtx, cancel := platformconfig.WithRPCRequestTimeout(ctx)
	defer cancel()
	var resp mcpdto.RegisterResponse
	req := mcpdto.RegisterRequest{InstanceID: r.instanceID, BinaryName: remoteLauncherBinaryName, PID: os.Getpid(), SessionToken: token, ClientKind: mcpdto.ClientKindCustom, PeerKind: mcpdto.PeerKindTool}
	if err := client.CallResult(callCtx, mcpdto.MethodRegister, req, &resp); err != nil {
		return mcpdto.RegisterResponse{}, err
	}
	resp.InstanceID = shared.FirstTrimmed(resp.InstanceID, req.InstanceID)
	if resp.Generation == 0 {
		resp.Generation = resp.AcceptedGeneration
	}
	if strings.TrimSpace(resp.InstanceID) == "" || resp.Generation == 0 {
		return mcpdto.RegisterResponse{}, errors.New("remote launcher: register response missing lease key")
	}
	return resp, nil
}
func remoteLauncherSessionToken() string {
	if token := strings.TrimSpace(os.Getenv("GO_AGENT_CTL_SESSION_TOKEN")); token != "" {
		return token
	}
	return strings.TrimSpace(os.Getenv("GO_AGENT_MCP_SESSION_TOKEN"))
}

func (r *remoteLauncher) startHeartbeatLocked(client *jrpc2.Client, reg mcpdto.RegisterResponse) {
	r.stopHeartbeatLocked()
	ctx, cancel := context.WithCancel(context.Background())
	r.heartbeatCancel = cancel
	lease := mcpdto.LeaseKey{InstanceID: strings.TrimSpace(reg.InstanceID), Generation: reg.Generation}
	interval := time.Duration(reg.HeartbeatIntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = remoteLauncherInterval
	}
	go remoteLauncherHeartbeat(ctx, client, lease, interval)
}
func (r *remoteLauncher) stopHeartbeatLocked() {
	if r.heartbeatCancel != nil {
		r.heartbeatCancel()
		r.heartbeatCancel = nil
	}
}

func remoteLauncherHeartbeat(ctx context.Context, client *jrpc2.Client, lease mcpdto.LeaseKey, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for seq := uint64(1); ; seq++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		callCtx, cancel := platformconfig.WithPeerTimeout(ctx, 5*time.Second)
		var resp mcpdto.HeartbeatResponse
		err := client.CallResult(callCtx, mcpdto.MethodHeartbeat, mcpdto.HeartbeatRequest{InstanceID: lease.InstanceID, Generation: lease.Generation, HeartbeatSeq: seq, Status: mcpdto.StatusActive}, &resp)
		cancel()
		if err != nil {
			if ctx.Err() != nil || client.IsStopped() {
				return
			}
			pkglogger.Warn("remoteLauncher: heartbeat failed", "lease", lease, "error", err)
		}
	}
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
		launcherwire.ParamAgentID:          strings.TrimSpace(agent.id),
		launcherwire.ParamCwd:              strings.TrimSpace(req.Cwd),
		launcherwire.ParamName:             displayName,
		launcherwire.ParamAgentType:        strings.TrimSpace(req.AgentType),
		launcherwire.ParamAgentKey:         strings.TrimSpace(req.AgentKey),
		launcherwire.ParamPromptKey:        strings.TrimSpace(req.PromptKey),
		launcherwire.ParamAgentMemoryScope: strings.TrimSpace(req.MemoryScope),
		launcherwire.ParamParentAgentID:    strings.TrimSpace(req.ParentID),
		launcherwire.ParamBaseInstructions: strings.TrimSpace(req.Instructions),
		launcherwire.ParamProvider:         launchProvider(req),
		launcherwire.ParamModel:            model,
		launcherwire.ParamEffort:           effort,
		launcherwire.ParamLanguage:         strings.TrimSpace(req.Language),
	}
	config := launchStartConfig(req.Env)
	if len(config) > 0 {
		params[launcherwire.ParamConfig] = config
	}
	resp, err := rpcCall[map[string]any](ctx, r, launcherwire.MethodThreadStart, params)
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
	thread, _ := resp[launcherwire.RespThread].(map[string]any)
	result := LaunchResult{
		ThreadID:      launcherwire.ResolveThreadStartThreadID(thread, resp, ""),
		RemoteAgentID: launcherwire.ResolveThreadStartAgentID(resp, agent.id),
	}
	if result.ThreadID == "" {
		return LaunchResult{}, errors.New("remote launcher: empty thread id")
	}
	bindRemoteLaunchRuntime(ctx, agent, result)
	return result, nil
}

// Fork 从父 provider thread fork 出 child thread，并把 child runtime 绑定到新 thread。
func (r *remoteLauncher) Fork(ctx context.Context, parent *agentRuntime, child *agentRuntime, req LaunchRequest) (LaunchResult, error) {
	if parent == nil || child == nil {
		return LaunchResult{}, errors.New("parent and child agents are required for forked launch")
	}
	if strings.TrimSpace(parent.remoteThreadID) == "" {
		return LaunchResult{}, errors.New("parent agent remote thread id is required for forked launch")
	}
	resp, err := rpcCall[map[string]any](ctx, r, launcherwire.MethodThreadFork, map[string]string{launcherwire.ParamThreadID: strings.TrimSpace(parent.remoteThreadID)})
	if err != nil {
		return LaunchResult{}, err
	}
	thread, _ := resp[launcherwire.RespThread].(map[string]any)
	result := LaunchResult{
		ThreadID:      launcherwire.ResolveThreadForkThreadID(thread, resp, ""),
		RemoteAgentID: launcherwire.ResolveThreadStartAgentID(resp, child.id),
	}
	if result.ThreadID == "" {
		return LaunchResult{}, errors.New("remote launcher: empty forked thread id")
	}
	bindRemoteLaunchRuntime(ctx, child, result)
	if displayName := managedAgentLaunchDisplayName(req.Name); displayName != "" {
		_, err := rpcCall[struct{}](ctx, r, launcherwire.MethodThreadNameSet, map[string]string{launcherwire.ParamThreadID: result.ThreadID, launcherwire.ParamName: displayName})
		if err != nil {
			return LaunchResult{}, err
		}
	}
	return result, nil
}
func bindRemoteLaunchRuntime(ctx context.Context, agent *agentRuntime, result LaunchResult) {
	now := resolveEventTime(ctx, agent.updatedAt)
	resetLaunchState(agent)
	agent.launchSeq++
	agent.threadID, agent.remoteThreadID = result.ThreadID, result.ThreadID
	agent.remoteAgentID, agent.startedAt, agent.updatedAt = result.RemoteAgentID, now, now
}

func (r *remoteLauncher) Stop(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.remoteThreadID == "" {
		return nil
	}
	_, err := rpcCall[struct{}](ctx, r, launcherwire.MethodThreadStop, map[string]string{launcherwire.ParamThreadID: agent.remoteThreadID})
	return err
}

// StopSettlesAgent 说明 Stop 是否会把代理状态收敛到终态。
func (r *remoteLauncher) StopSettlesAgent() bool { return true }

func (r *remoteLauncher) Archive(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.remoteThreadID == "" {
		return nil
	}
	_, err := rpcCall[struct{}](ctx, r, launcherwire.MethodThreadArchive,
		map[string]string{launcherwire.ParamThreadID: agent.remoteThreadID})
	return err
}

func (r *remoteLauncher) Interrupt(ctx context.Context, agent *agentRuntime, source string) error {
	if agent == nil || strings.TrimSpace(agent.remoteThreadID) == "" {
		return errors.New("remote thread id is required")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "parent_agent"
	}
	_, err := rpcCall[struct{}](ctx, r, launcherwire.MethodTurnInterrupt, map[string]string{
		launcherwire.ParamThreadID: strings.TrimSpace(agent.remoteThreadID),
		launcherwire.ParamSource:   source,
	})
	return err
}

func (r *remoteLauncher) SubmitTurn(ctx context.Context, agent *agentRuntime, submission TurnSubmission) (string, error) {
	if agent == nil || agent.remoteThreadID == "" {
		return "", errors.New("remote thread id is required")
	}
	params := map[string]any{
		launcherwire.ParamThreadID:             agent.remoteThreadID,
		launcherwire.ParamInput:                submission.Inputs,
		launcherwire.ParamSelectedSkills:       submission.SelectedSkills,
		launcherwire.ParamManualSkillSelection: submission.ManualSkillSelection,
	}
	if len(submission.OutputSchema) > 0 {
		params[launcherwire.ParamOutputSchema] = submission.OutputSchema
	}
	resp, err := rpcCall[map[string]any](ctx, r, launcherwire.MethodTurnStart, params)
	if err != nil {
		return "", err
	}
	turnID := rpcString(resp[launcherwire.RespTurnID])
	if turnID == "" {
		return "", errors.New("remote launcher: empty turn id")
	}
	return turnID, nil
}

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
	r.stopHeartbeatLocked()
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
