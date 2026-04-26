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

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type AgentLauncher interface {
	Launch(ctx context.Context, agent *agentRuntime, req LaunchRequest) (LaunchResult, error)
	Stop(ctx context.Context, agent *agentRuntime) error
	Archive(ctx context.Context, agent *agentRuntime) error
	SubmitTurn(ctx context.Context, agent *agentRuntime, submission TurnSubmission) (string, error)
	IsRunning(ctx context.Context, agent *agentRuntime) bool
}

type LaunchResult struct {
	ThreadID      string
	RemoteAgentID string
}

// localLauncher handles the local process mode while leaving runtime fields on agentRuntime.
type localLauncher struct {
	turnStarter TurnStarter
	logger      *slog.Logger
}

func NewLocalLauncher(turnStarter TurnStarter, logger *slog.Logger) AgentLauncher {
	return &localLauncher{turnStarter: turnStarter, logger: logger}
}

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
	if err := cmd.Start(); err != nil {
		agent.lastError = err.Error()
		return LaunchResult{}, err
	}
	now := resolveEventTime(ctx, agent.updatedAt)
	resetLaunchState(agent)
	agent.cmd = cmd
	agent.launchSeq++
	agent.startedAt = now
	agent.updatedAt = now
	if l != nil && l.logger != nil {
		l.logger.Info("orchestration: agent launched", "agent_id", agent.id, "pid", cmd.Process.Pid)
	}
	return LaunchResult{}, nil
}

func (l *localLauncher) Stop(_ context.Context, agent *agentRuntime) error {
	if agent == nil {
		return nil
	}
	return stopProcess(agent.cmd)
}

func (l *localLauncher) Archive(ctx context.Context, agent *agentRuntime) error {
	return l.Stop(ctx, agent)
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
	addr   string
	mu     sync.Mutex
	client *jrpc2.Client
}

func NewRemoteLauncher(addr string) AgentLauncher {
	return &remoteLauncher{addr: strings.TrimSpace(addr)}
}

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
	next := jrpc2.NewClient(channel.Line(raw, raw), nil)
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

func normalizeManagedAgentDisplayName(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	value = strings.Trim(value, "`\"'“”‘’[]()（）【】")
	return strings.TrimSpace(value)
}

func managedAgentLaunchDisplayName(name string) string {
	return normalizeManagedAgentDisplayName(name)
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
	displayName := managedAgentLaunchDisplayName(req.Name)
	model := shared.FirstTrimmed(envValue(req.Env, "AGENT_MODEL"), commandFlagValue(launchCommandArgs(req.Command), "--model"))
	effort := shared.FirstTrimmed(envValue(req.Env, "AGENT_EFFORT"), commandFlagValue(launchCommandArgs(req.Command), "--effort"))
	pkglogger.Warn("remoteLauncher: thread/start config trace",
		"agent_id", agent.id,
		"provider", launchProvider(req),
		"model", model,
		"effort", effort,
		"env_has_effort", envValue(req.Env, "AGENT_EFFORT") != "",
	)
	resp, err := rpcCall[map[string]any](ctx, r, LauncherMethodThreadStart, map[string]any{
		LauncherParamAgentID:          strings.TrimSpace(agent.id),
		LauncherParamCwd:              strings.TrimSpace(req.Cwd),
		LauncherParamName:             displayName,
		LauncherParamAgentType:        strings.TrimSpace(req.AgentType),
		LauncherParamAgentKey:         strings.TrimSpace(req.AgentKey),
		LauncherParamAgentMemoryScope: strings.TrimSpace(req.MemoryScope),
		LauncherParamParentAgentID:    strings.TrimSpace(req.ParentID),
		LauncherParamBaseInstructions: strings.TrimSpace(req.Instructions),
		LauncherParamProvider:         launchProvider(req),
		LauncherParamModel:            model,
		LauncherParamEffort:           effort,
	})
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

func (r *remoteLauncher) Stop(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.remoteThreadID == "" {
		return nil
	}
	_, err := rpcCall[struct{}](ctx, r, LauncherMethodThreadStop, map[string]string{LauncherParamThreadID: agent.remoteThreadID})
	return err
}

func (r *remoteLauncher) Archive(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.remoteThreadID == "" {
		return nil
	}
	_, err := rpcCall[struct{}](ctx, r, LauncherMethodThreadArchive,
		map[string]string{LauncherParamThreadID: agent.remoteThreadID})
	return err
}

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

func (r *remoteLauncher) IsRunning(_ context.Context, agent *agentRuntime) bool {
	return agent != nil && agent.remoteThreadID != ""
}

func (r *remoteLauncher) SupportsPersistedRuntimeRehydrate() bool {
	return true
}

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
