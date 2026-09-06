package orchestration

import (
	"context"
	"errors"
	"fmt"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/launcherwire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/processctl"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"log/slog"
	"net"
	"os"
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

// LaunchResult 返回 launcher 启动后写回 service runtime 的远端身份。
// 本地 launcher 可以保持空值；远端 launcher 必须提供 thread/agent id 供后续 turn 和 archive 路由。
type LaunchResult struct {
	ThreadID, RemoteAgentID string
}

type localLauncher struct {
	turnStarter contract.OrchestrationTurnStarter
	logger      *slog.Logger
	exitMonitor *exitmonitor.Monitor
}

// NewLocalLauncher 创建本地进程 launcher；本地模式不支持 fork 或远端中断。
func NewLocalLauncher(turnStarter contract.OrchestrationTurnStarter, logger *slog.Logger) AgentLauncher {
	return &localLauncher{turnStarter: turnStarter, logger: logger}
}

// Launch 启动代理会话并记录运行句柄。
func (l *localLauncher) Launch(ctx context.Context, agent *agentRuntime, _ LaunchRequest) (LaunchResult, error) {
	if agent == nil {
		return LaunchResult{}, errors.New("agent is required")
	}
	nextSeq := agent.launchSeq + 1
	if agent.launchSeq != 0 && agent.monitoredSeq < agent.launchSeq && agent.lastExitedSeq < agent.launchSeq {
		nextSeq = agent.launchSeq
	}
	cmd, guard, err := exitmonitor.StartMonitoredCommand(
		l.exitMonitor, l.logger,
		exitmonitor.Target{AgentID: agent.id, LaunchSeq: nextSeq},
		agent.command, agent.cwd,
		append(contract.ScrubDatabaseEnv(os.Environ()), contract.ScrubDatabaseEnv(agent.env)...),
	)
	if err != nil {
		agent.lastError = publicOrchestrationError("Agent process failed to start.", err)
		return LaunchResult{}, err
	}
	now := resolveEventTime(ctx, agent.updatedAt)
	resetLaunchState(agent)
	agent.cmd, agent.processGuard = cmd, guard
	agent.launchSeq, agent.startedAt, agent.updatedAt = nextSeq, now, now
	agent.monitoredSeq = nextSeq
	if l != nil && l.logger != nil {
		l.logger.Info("orchestration: agent launched", "agent_id", agent.id, "pid", cmd.Process.Pid)
	}
	return LaunchResult{}, nil
}

// Fork 在本地 launcher 中直接失败；fork 需要远端 thread 控制面保留父子上下文。
func (l *localLauncher) Fork(context.Context, *agentRuntime, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, errors.New("forked context launch requires remote launcher")
}

// Stop 向本地进程发送停止信号。
func (l *localLauncher) Stop(_ context.Context, agent *agentRuntime) error {
	if agent == nil {
		return nil
	}
	return processctl.RequestStop(agent.cmd, agent.processGuard)
}

// Archive 本地模式下等同于 Stop，归档操作仅停止进程。
func (l *localLauncher) Archive(ctx context.Context, agent *agentRuntime) error {
	return l.Stop(ctx, agent)
}

// Interrupt 本地启动器不支持中断，仅远端 Codex agent 支持。
func (l *localLauncher) Interrupt(context.Context, *agentRuntime, string) error {
	return errors.New("interrupt_agent currently supports remote Codex agents only")
}

// SubmitTurn 通过本地 turnStarter 提交 turn。
// turnStarter 未注入时 fail-fast，避免把 turn 静默吞掉。
func (l *localLauncher) SubmitTurn(ctx context.Context, _ *agentRuntime, submission TurnSubmission) (string, error) {
	if l == nil || l.turnStarter == nil {
		return "", errors.New("turn starter is not configured")
	}
	return l.turnStarter.StartTurn(ctx, submission)
}

// IsRunning 以本地进程句柄是否存在判断 agent 是否仍被 runtime 接管。
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
	closed          bool
}

const remoteLauncherBinaryName = "mcp-orch-remote-launcher"
const remoteLauncherInterval = 10 * time.Second

// NewRemoteLauncher 创建远端 RPC 启动器，addr 是 remote launcher 的 TCP 地址。
func NewRemoteLauncher(addr string) AgentLauncher {
	return &remoteLauncher{addr: strings.TrimSpace(addr), instanceID: shared.NewID("mcp_orch_remote_launcher")}
}

// ensureClient 懒加载或复用 jrpc2 客户端连接，断开后自动重连。
func (r *remoteLauncher) ensureClient(ctx context.Context) (*jrpc2.Client, error) {
	client, previous, instanceID, err := r.prepareClientConnection()
	if err != nil || client != nil {
		return client, err
	}
	if previous != nil {
		_ = previous.Close()
	}
	raw, err := new(net.Dialer).DialContext(ctx, "tcp", r.addr)
	if err != nil {
		return nil, err
	}
	next := jrpc2.NewClient(channel.Line(raw, raw), &jrpc2.ClientOptions{OnNotify: r.handleNotify})
	reg, err := r.registerClient(ctx, next, instanceID)
	if err != nil {
		_ = next.Close()
		return nil, err
	}
	return r.installClientConnection(next, reg)
}

// prepareClientConnection 在锁内复用健康连接，或冻结一次新的连接尝试。
func (r *remoteLauncher) prepareClientConnection() (client, previous *jrpc2.Client, instanceID string, err error) {
	if r == nil || strings.TrimSpace(r.addr) == "" {
		return nil, nil, "", errors.New("remote launcher rpc addr is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, nil, "", errors.New("remote launcher is closed")
	}
	if current := r.client; current != nil && !current.IsStopped() {
		return current, nil, "", nil
	}
	previous = r.detachClientLocked()
	instanceID = strings.TrimSpace(r.instanceID)
	if instanceID == "" {
		instanceID = shared.NewID("mcp_orch_remote_launcher")
		r.instanceID = instanceID
	}
	return nil, previous, instanceID, nil
}

// installClientConnection 在锁内提交已注册连接，并关闭并发竞争产生的冗余连接。
func (r *remoteLauncher) installClientConnection(next *jrpc2.Client, reg mcpdto.RegisterResponse) (*jrpc2.Client, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = next.Close()
		return nil, errors.New("remote launcher is closed")
	}
	if current := r.client; current != nil && !current.IsStopped() {
		r.mu.Unlock()
		_ = next.Close()
		return current, nil
	}
	stale := r.detachClientLocked()
	r.client = next
	startHeartbeat := r.prepareHeartbeatLocked(next, reg)
	r.mu.Unlock()
	if stale != nil {
		_ = stale.Close()
	}
	startHeartbeat()
	return next, nil
}

func (r *remoteLauncher) detachClientLocked() *jrpc2.Client {
	r.stopHeartbeatLocked()
	client := r.client
	r.client = nil
	return client
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

// registerClient 向 remote launcher 注册本实例并校验协议和租约信息。
// 缺少 session token、协议或 lease 会立即失败，避免心跳落到未认证连接。
func (r *remoteLauncher) registerClient(ctx context.Context, client *jrpc2.Client, instanceID string) (mcpdto.RegisterResponse, error) {
	token := remoteLauncherSessionToken()
	if token == "" {
		return mcpdto.RegisterResponse{}, errors.New("remote launcher requires GO_AGENT_CTL_SESSION_TOKEN or GO_AGENT_MCP_SESSION_TOKEN")
	}
	callCtx, cancel := platformconfig.WithRPCRequestTimeout(ctx)
	defer cancel()
	var resp mcpdto.RegisterResponse
	req := mcpdto.RegisterRequest{InstanceID: instanceID, BinaryName: remoteLauncherBinaryName, PID: os.Getpid(), SessionToken: token, ClientKind: mcpdto.ClientKindCustom, PeerKind: mcpdto.PeerKindTool}
	if err := client.CallResult(callCtx, mcpdto.MethodRegister, req, &resp); err != nil {
		return mcpdto.RegisterResponse{}, err
	}
	resp.ServerProtocolVersion = mcpdto.NormalizeProtocolVersion(resp.ServerProtocolVersion)
	if resp.ServerProtocolVersion == "" {
		return mcpdto.RegisterResponse{}, errors.New("remote launcher: register response missing server protocol version")
	}
	if resp.ServerProtocolVersion != mcpdto.ProtocolVersion {
		return mcpdto.RegisterResponse{}, fmt.Errorf("remote launcher: incompatible server protocol version %q", resp.ServerProtocolVersion)
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

func (r *remoteLauncher) prepareHeartbeatLocked(client *jrpc2.Client, reg mcpdto.RegisterResponse) func() {
	r.stopHeartbeatLocked()
	ctx, cancel := context.WithCancel(context.Background())
	r.heartbeatCancel = cancel
	lease := mcpdto.LeaseKey{InstanceID: strings.TrimSpace(reg.InstanceID), Generation: reg.Generation}
	interval := time.Duration(reg.HeartbeatIntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = remoteLauncherInterval
	}
	return func() {
		safego.Go(ctx, nil, "mcp-orch.remoteLauncher.heartbeat", func(runCtx context.Context) {
			remoteLauncherHeartbeat(runCtx, client, lease, interval)
		})
	}
}
func (r *remoteLauncher) stopHeartbeatLocked() {
	if r.heartbeatCancel != nil {
		r.heartbeatCancel()
		r.heartbeatCancel = nil
	}
}

// remoteLauncherHeartbeat 在后台 goroutine 中定期向 remote launcher 发送心跳续约。
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
func managedAgentLaunchDisplayName(name string) string {
	name = strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
	name = strings.Trim(name, "`\"'“”‘’[]()（）【】")
	return strings.TrimSpace(name)
}

// Launch 通过 thread/start RPC 启动远端 agent 线程。
// prompt 不放入 thread/start，任务正文会在启动后作为第一轮 turn 提交。
func (r *remoteLauncher) Launch(ctx context.Context, agent *agentRuntime, req LaunchRequest) (LaunchResult, error) {
	if agent == nil {
		return LaunchResult{}, errors.New("agent is required")
	}
	start := time.Now()
	pkglogger.Info("remoteLauncher: thread/start RPC begin", "agent_id", agent.id, "rpc_addr", r.addr)
	// thread/start 里 prompt/name 都曾表示展示名；这里只使用 req.Name。
	// req.Prompt 作为首轮 turn 提交，避免展示名和任务正文串线。
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
		launcherwire.ParamCwd:              strings.TrimSpace(req.Cwd),
		launcherwire.ParamName:             displayName,
		launcherwire.ParamAgentType:        strings.TrimSpace(req.AgentType),
		launcherwire.ParamAgentKey:         strings.TrimSpace(req.AgentKey),
		launcherwire.ParamPromptKey:        strings.TrimSpace(req.PromptKey),
		launcherwire.ParamAgentMemoryScope: strings.TrimSpace(req.MemoryScope),
		launcherwire.ParamParentAgentID:    strings.TrimSpace(req.ParentID),
		launcherwire.ParamLaunchIntentID:   "launch_" + strings.TrimSpace(agent.id),
		launcherwire.ParamDeferSpawn:       true,
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

// Stop 向远端 launcher 发送 thread/stop RPC 停止 agent 线程。
func (r *remoteLauncher) Stop(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.remoteThreadID == "" {
		return nil
	}
	_, err := rpcCall[struct{}](ctx, r, launcherwire.MethodThreadStop, map[string]string{launcherwire.ParamThreadID: agent.remoteThreadID})
	return err
}

// StopSettlesAgent 说明 Stop 是否会把代理状态收敛到终态。
func (r *remoteLauncher) StopSettlesAgent() bool { return true }

// Archive 向远端 launcher 发送 thread/archive RPC 归档 agent 线程。
func (r *remoteLauncher) Archive(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.remoteThreadID == "" {
		return nil
	}
	_, err := rpcCall[struct{}](ctx, r, launcherwire.MethodThreadArchive,
		map[string]string{launcherwire.ParamThreadID: agent.remoteThreadID})
	return err
}

// Interrupt 向远端 launcher 发送 turn/interrupt RPC 中断 agent 当前 turn。
func (r *remoteLauncher) Interrupt(ctx context.Context, agent *agentRuntime, source string) error {
	if agent == nil || strings.TrimSpace(agent.remoteThreadID) == "" {
		return errors.New("remote thread id is required")
	}
	expectedTurnID := strings.TrimSpace(agent.activeTurnID)
	if expectedTurnID == "" {
		return errors.New("remote active turn id is required")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "parent_agent"
	}
	request := launcherwire.TurnInterruptRequest{
		ThreadID:       strings.TrimSpace(agent.remoteThreadID),
		ExpectedTurnID: expectedTurnID,
		RequestID:      shared.NewID("mcp_orch_interrupt"),
		Source:         source,
	}
	response, err := rpcCall[launcherwire.TurnInterruptResponse](ctx, r, launcherwire.MethodTurnInterrupt, request)
	if err != nil {
		return err
	}
	return validateTurnInterruptResponse(request, response)
}

// validateTurnInterruptResponse 在编排控制器等待本地收口前拒绝过期、不完整或未被远端接受的 stop 回复。
func validateTurnInterruptResponse(request launcherwire.TurnInterruptRequest, response launcherwire.TurnInterruptResponse) error {
	if response.Accepted == nil {
		return errors.New("remote launcher: turn/interrupt response missing accepted")
	}
	if strings.TrimSpace(response.RequestID) != request.RequestID {
		return fmt.Errorf("remote launcher: turn/interrupt response request id %q does not match request %q", response.RequestID, request.RequestID)
	}
	if strings.TrimSpace(response.ExpectedTurnID) != request.ExpectedTurnID {
		return fmt.Errorf("remote launcher: turn/interrupt response expected turn %q does not match request %q", response.ExpectedTurnID, request.ExpectedTurnID)
	}
	if !*response.Accepted {
		if code := strings.TrimSpace(response.ErrorCode); code != "" {
			return fmt.Errorf("remote launcher: turn/interrupt was not accepted for turn %q: %s", request.ExpectedTurnID, code)
		}
		return fmt.Errorf("remote launcher: turn/interrupt was not accepted for turn %q", request.ExpectedTurnID)
	}
	return nil
}

// SubmitTurn 通过 turn/start RPC 向远端 agent 提交 turn。
// 远端没有返回 turn_id 时立即报错，调用方不能假设提交成功。
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
	turnID, _ := resp[launcherwire.RespTurnID].(string)
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return "", errors.New("remote launcher: empty turn id")
	}
	return turnID, nil
}

// IsRunning 以 remoteThreadID 是否存在判断远端线程是否仍可路由。
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
	r.closed = true
	client := r.detachClientLocked()
	r.mu.Unlock()
	if client == nil {
		return nil
	}
	return client.Close()
}

type remoteLauncherEventSink interface {
	handleRemoteTurnTerminal(context.Context, turndto.TurnTerminalV2)
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

// handleNotify 处理来自 remote launcher 的 push 通知，解码后转发给 event sink。
func (r *remoteLauncher) handleNotify(req *jrpc2.Request) {
	if r == nil || req == nil {
		return
	}
	sink := r.currentEventSink()
	if sink == nil {
		return
	}
	switch method := strings.TrimSpace(req.Method()); method {
	case eventsurface.MethodTurnTerminal:
		ev, err := eventsurface.DecodeRemoteTurnTerminal(req.UnmarshalParams)
		if logRemoteLauncherNotifyDecodeError(method, err) {
			return
		}
		sink.handleRemoteTurnTerminal(context.Background(), ev)
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

// handleRemoteTurnTerminal 通过 canonical threadId 的 runtime 绑定解析 owner，再推进内部 turn 生命周期。
func (s *service) handleRemoteTurnTerminal(ctx context.Context, terminal turndto.TurnTerminalV2) {
	// Keep the void sink compatible with legacy notification callers. Receipt-
	// capable callers use the strict result path below.
	if err := s.handleRemoteTurnTerminalResultWithMode(ctx, terminal, false); err != nil {
		pkglogger.Warn("orchestration: remote terminal handling failed", "thread_id", strings.TrimSpace(terminal.ThreadID), "turn_id", strings.TrimSpace(terminal.TurnID), "error", err)
	}
}

// handleRemoteTurnTerminalResult 是 provider terminal 的可确认内部接收路径。
// 已由 durable outcome/head 精确证明的 duplicate（包括 stale replay）返回 nil；
// 只有 owner、fence、projection 或 durable commit 失败才返回错误，供 standalone
// provider sink fail-closed。
func (s *service) handleRemoteTurnTerminalResult(ctx context.Context, terminal turndto.TurnTerminalV2) error {
	return s.handleRemoteTurnTerminalResultWithMode(ctx, terminal, true)
}

// handleRemoteTurnTerminalResultWithMode 按兼容模式接收远端终态并确保 durable 提交。
func (s *service) handleRemoteTurnTerminalResultWithMode(ctx context.Context, terminal turndto.TurnTerminalV2, requireCanonical bool) error {
	if err := validateRemoteTurnTerminalRequest(s, terminal, requireCanonical); err != nil {
		return err
	}
	ownerAgentID, err := s.resolveTerminalOwner(ctx, terminal.ThreadID)
	if err != nil {
		return fmt.Errorf("remote terminal owner lookup: %w", err)
	}
	deliver, err := s.routeRemoteTurnTerminalResult(ctx, ownerAgentID, terminal, requireCanonical)
	if err != nil || !deliver {
		return err
	}
	return s.commitRemoteTurnTerminal(ctx, ownerAgentID, terminal, requireCanonical)
}

// validateRemoteTurnTerminalRequest 在路由和封存前校验终态契约及 durable 依赖。
func validateRemoteTurnTerminalRequest(s *service, terminal turndto.TurnTerminalV2, requireCanonical bool) error {
	if requireCanonical {
		if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
			return fmt.Errorf("remote canonical terminal contract: %w", err)
		}
	}
	if s == nil || s.registry == nil || s.turns == nil {
		return errors.New("orchestration terminal handler is not configured")
	}
	if requireCanonical && s.terminalOutcomes == nil {
		return errors.New("remote canonical terminal owner is not configured")
	}
	return nil
}

// routeRemoteTurnTerminalResult 路由终态并为 stale/buffered 结果执行 canonical 证明。
func (s *service) routeRemoteTurnTerminalResult(ctx context.Context, ownerAgentID string, terminal turndto.TurnTerminalV2, requireCanonical bool) (bool, error) {
	deliver, buffered, routeErr := s.turns.routeRemoteTurnTerminal(ownerAgentID, terminal)
	if routeErr != nil {
		return false, fmt.Errorf("remote terminal reconciliation: %w", routeErr)
	}
	if buffered {
		if requireCanonical {
			return false, fmt.Errorf("%w: terminal is buffered only in memory for owner=%q thread=%q turn=%q",
				errRemoteTerminalRecoveryRequired, strings.TrimSpace(ownerAgentID),
				strings.TrimSpace(terminal.ThreadID), strings.TrimSpace(terminal.TurnID))
		}
		return false, nil
	}
	if deliver || !requireCanonical {
		return deliver, nil
	}
	proven, proofErr := s.durableRemoteTerminalNoop(ctx, ownerAgentID, terminal)
	if proofErr != nil {
		return false, errors.Join(errRemoteTerminalRecoveryRequired,
			fmt.Errorf("remote terminal durable no-op proof: %w", proofErr))
	}
	if !proven {
		return false, fmt.Errorf("%w: owner=%q thread=%q turn=%q", errRemoteTerminalRecoveryRequired,
			strings.TrimSpace(ownerAgentID), strings.TrimSpace(terminal.ThreadID), strings.TrimSpace(terminal.TurnID))
	}
	return false, nil
}

// commitRemoteTurnTerminal 投影、封存并提交已路由的远端终态。
func (s *service) commitRemoteTurnTerminal(ctx context.Context, ownerAgentID string, terminal turndto.TurnTerminalV2, requireCanonical bool) error {
	// Validate/project before sealing so malformed provider payloads cannot
	// consume bounded first-truth capacity or require pointless retries.
	ev, err := eventsurface.ProjectRemoteTurnTerminal(terminal, ownerAgentID)
	if err != nil {
		return fmt.Errorf("remote terminal projection: %w", err)
	}
	acceptance, err := s.turns.acceptRemoteTurnTerminal(terminal)
	if err != nil {
		return fmt.Errorf("remote terminal acceptance: %w", err)
	}
	if !acceptance.accepted {
		return nil
	}
	completed := false
	defer func() {
		if completed {
			s.turns.releaseRemoteTurnTerminal(acceptance)
			return
		}
		// Keep the first canonical truth sealed while allowing an exact retry;
		// conflicting terminal payloads must still be rejected.
		s.turns.retryRemoteTurnTerminal(acceptance)
	}()
	if acceptance.evicted {
		stats := s.turns.remoteTerminalSealStats()
		pkglogger.Info("orchestration: remote terminal seal capacity eviction", "capacity", stats.Capacity, "entries", stats.Entries, "capacity_evictions", stats.CapacityEvictions)
	}
	if err := s.handleRemoteTurnCompletedResultWithMode(ctx, ev, requireCanonical); err != nil {
		return fmt.Errorf("remote terminal commit: %w", err)
	}
	completed = true
	return nil
}

// resolveTerminalOwner 优先通过持久化 provider binding 解析 terminal owner。
// 一旦 service 已装配持久化 thread store 但该 store 没有 durable lookup，立即
// fail-closed；只有纯单元测试/未装配 store 的旧构造路径才允许使用内存 registry。
func (s *service) resolveTerminalOwner(ctx context.Context, threadID string) (string, error) {
	if s == nil || s.registry == nil {
		return "", errors.New("orchestration terminal owner resolver is not configured")
	}
	if s.lifecycle != nil && s.lifecycle.agentThreads != nil {
		lookup, ok := s.lifecycle.agentThreads.(TerminalOwnerLookup)
		if !ok || lookup == nil {
			return "", errors.New("durable terminal owner lookup is unavailable")
		}
		return lookup.LookupOwnerByThread(ctx, threadID)
	}
	return s.registry.ownerAgentIDForThreadID(threadID)
}

// routeRemoteTurnTerminal 按当前 active turn 或受限的启动期缓冲决定是否接收远端终态。
func (s *service) routeRemoteTurnTerminal(ownerAgentID string, terminal turndto.TurnTerminalV2) bool {
	deliver, buffered, routeErr := s.turns.routeRemoteTurnTerminal(ownerAgentID, terminal)
	if routeErr != nil {
		pkglogger.Warn("orchestration: remote terminal reconciliation rejected", "agent_id", ownerAgentID, "thread_id", terminal.ThreadID, "turn_id", terminal.TurnID, "error", routeErr)
		return false
	}
	if buffered {
		return false
	}
	if !deliver {
		pkglogger.Info("orchestration: stale remote terminal ignored after target changed", "agent_id", ownerAgentID, "thread_id", terminal.ThreadID, "turn_id", terminal.TurnID)
		return false
	}
	return true
}

// handleRemoteTurnCompleted 处理来自 remote launcher 的 turn 完成通知，写入 report 并推进状态机。
func (s *service) handleRemoteTurnCompleted(ctx context.Context, ev turndto.TurnCompleted) {
	if err := s.handleRemoteTurnCompletedResultWithMode(ctx, ev, false); err != nil {
		pkglogger.Warn("orchestration: remote turn completion failed", "agent_id", strings.TrimSpace(ev.AgentID), "thread_id", strings.TrimSpace(ev.ThreadID), "turn_id", strings.TrimSpace(ev.TurnID), "error", err)
	}
}

// handleRemoteTurnCompletedResult 返回 canonical commit 错误，供 provider terminal receipt 使用。
func (s *service) handleRemoteTurnCompletedResult(ctx context.Context, ev turndto.TurnCompleted) error {
	return s.handleRemoteTurnCompletedResultWithMode(ctx, ev, true)
}

// handleRemoteTurnCompletedResultWithMode 提交远端完成事件并写入兼容 report。
func (s *service) handleRemoteTurnCompletedResultWithMode(ctx context.Context, ev turndto.TurnCompleted, requireCanonical bool) error {
	if s == nil || !s.hasRuntimeAgent(ev.AgentID) {
		return errors.New("remote turn completion agent is not active")
	}
	// Provider canonical terminal receipts require the durable terminal owner.
	// Keep CommitTurnCompleted's false,nil result for legacy void callers, but
	// never let this receipt-capable path fall through to reports/runtime hooks
	// when the canonical store is not wired.
	if requireCanonical && s.terminalOutcomes == nil {
		return errors.New("remote canonical terminal owner is not configured")
	}
	if strings.TrimSpace(ev.TurnID) == "" {
		return errors.New("remote turn completion missing turn_id")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	eventCtx := withEventTime(ctx, ev.Timestamp)
	handled, err := s.CommitTurnCompleted(eventCtx, ev)
	if err != nil {
		return fmt.Errorf("remote canonical terminal commit: %w", err)
	}
	if handled {
		return nil
	}
	if err := s.recordRemoteTurnCompletedReport(eventCtx, ev); err != nil {
		return err
	}
	handleTurnCompletedEventWithCtx(s, s.logger, ev, eventCtx)
	return nil
}

// recordRemoteTurnCompletedReport 写入终态 report，并保留 agent 缺失时的兼容忽略。
func (s *service) recordRemoteTurnCompletedReport(eventCtx context.Context, ev turndto.TurnCompleted) error {
	_, err := s.reports.HandleReportEvent(eventCtx, ReportEvent{
		AgentID:   strings.TrimSpace(ev.AgentID),
		Report:    turnCompletedReportText(ev),
		EventType: eventsurface.MethodTurnTerminal,
		EventData: mustMarshalHookReportEvent(ev),
	})
	if err != nil && !errors.Is(err, errAgentNotFound) {
		return fmt.Errorf("remote turn completion report: %w", err)
	}
	return nil
}

// handleRemoteTurnInterrupted 处理来自 remote launcher 的 turn 中断通知，写入 report 并推进状态机。
func (s *service) handleRemoteTurnInterrupted(ctx context.Context, ev turndto.TurnInterrupted) {
	if s == nil || !s.hasRuntimeAgent(ev.AgentID) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	eventCtx := withEventTime(ctx, ev.Timestamp)
	handled, err := s.CommitTurnInterrupted(eventCtx, ev)
	if err != nil {
		pkglogger.Warn("orchestration: remote interrupted terminal commit failed",
			"agent_id", strings.TrimSpace(ev.AgentID), "thread_id", strings.TrimSpace(ev.ThreadID),
			"turn_id", strings.TrimSpace(ev.TurnID), "error", err)
		return
	}
	if handled {
		return
	}
	if _, err := s.reports.HandleReportEvent(eventCtx, ReportEvent{AgentID: strings.TrimSpace(ev.AgentID), Report: missingPublicTurnFailureReport, EventType: "turn.aborted", EventData: mustMarshalHookReportEvent(ev)}); err != nil && !errors.Is(err, errAgentNotFound) {
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
