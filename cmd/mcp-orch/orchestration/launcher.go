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

func (r *remoteLauncher) Launch(ctx context.Context, agent *agentRuntime, req LaunchRequest) (LaunchResult, error) {
	if agent == nil {
		return LaunchResult{}, errors.New("agent is required")
	}
	start := time.Now()
	pkglogger.Info("remoteLauncher: thread/start RPC begin", "agent_id", agent.id, "rpc_addr", r.addr)
	resp, err := rpcCall[map[string]any](ctx, r, "thread/start", map[string]any{
		"cwd":                strings.TrimSpace(req.Cwd),
		"prompt":             shared.FirstTrimmed(req.Prompt, req.Name),
		"name":               strings.TrimSpace(req.Name),
		"agent_type":         strings.TrimSpace(req.AgentType),
		"agent_memory_scope": strings.TrimSpace(req.MemoryScope),
		"parent_agent_id":    strings.TrimSpace(req.ParentID),
		"base_instructions":  strings.TrimSpace(req.Instructions),
		"provider":           launchProvider(req),
		"model":              shared.FirstTrimmed(envValue(req.Env, "AGENT_MODEL"), commandFlagValue(launchCommandArgs(req.Command), "--model")),
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
	thread, _ := resp["thread"].(map[string]any)
	result := LaunchResult{
		ThreadID:      shared.FirstTrimmed(rpcString(thread["id"]), rpcString(resp["threadId"]), rpcString(resp["thread_id"])),
		RemoteAgentID: shared.FirstTrimmed(rpcString(resp["agentId"]), rpcString(resp["agent_id"]), agent.id),
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
	_, err := rpcCall[struct{}](ctx, r, "thread/stop", map[string]string{"thread_id": agent.remoteThreadID})
	return err
}

func (r *remoteLauncher) SubmitTurn(ctx context.Context, agent *agentRuntime, submission TurnSubmission) (string, error) {
	if agent == nil || agent.remoteThreadID == "" {
		return "", errors.New("remote thread id is required")
	}
	params := map[string]any{
		"thread_id":              agent.remoteThreadID,
		"input":                  submission.Inputs,
		"selected_skills":        submission.SelectedSkills,
		"manual_skill_selection": submission.ManualSkillSelection,
	}
	if len(submission.OutputSchema) > 0 {
		params["output_schema"] = submission.OutputSchema
	}
	resp, err := rpcCall[map[string]any](ctx, r, "turn/start", params)
	if err != nil {
		return "", err
	}
	turnID := rpcString(resp["turn_id"])
	if turnID == "" {
		return "", errors.New("remote launcher: empty turn id")
	}
	return turnID, nil
}

func (r *remoteLauncher) IsRunning(_ context.Context, agent *agentRuntime) bool {
	return agent != nil && agent.remoteThreadID != ""
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
