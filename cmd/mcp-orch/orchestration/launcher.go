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
	"unicode"

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

func normalizeManagedAgentDisplayName(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	value = strings.Trim(value, "`\"'“”‘’[]()（）【】")
	return strings.TrimSpace(value)
}

func hasASCIIOnly(value string) bool {
	for _, r := range value {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func isGenericManagedAgentToken(token string) bool {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "", "agent", "subagent", "sub", "child", "worker", "helper", "assistant", "planner", "reviewer", "verifier", "review", "verify", "research", "researcher", "explore", "explorer", "ui", "task", "thread", "runner", "run", "background", "temp", "tmp", "job", "creator", "coder", "codex", "claude":
		return true
	default:
		return false
	}
}

func looksTechnicalManagedAgentName(value string) bool {
	name := normalizeManagedAgentDisplayName(value)
	if name == "" {
		return true
	}
	if strings.ContainsAny(name, `/\`) {
		return true
	}
	lower := strings.ToLower(name)
	hasSpace := strings.ContainsRune(lower, ' ')
	tokenFields := strings.FieldsFunc(lower, func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '.' || r == '/'
	})
	if !hasSpace && len(tokenFields) > 0 {
		allGeneric := true
		for _, token := range tokenFields {
			if !isGenericManagedAgentToken(token) {
				allGeneric = false
				break
			}
		}
		if allGeneric {
			return true
		}
	}
	if !hasASCIIOnly(lower) {
		return false
	}
	if strings.ContainsAny(lower, "-_.") {
		return true
	}
	for _, r := range lower {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func stripManagedAgentListPrefix(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "-*•> ")
	var digitCount int
	for _, r := range line {
		if !unicode.IsDigit(r) {
			break
		}
		digitCount += 1
	}
	if digitCount > 0 {
		rest := strings.TrimSpace(line[digitCount:])
		for _, prefix := range []string{".", "、", ")", "）", "]", "】", ":"} {
			if strings.HasPrefix(rest, prefix) {
				return strings.TrimSpace(rest[len(prefix):])
			}
		}
	}
	return strings.TrimSpace(line)
}

func trimManagedAgentPromptLead(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"请专注于", "请负责", "子任务：", "子任务:", "麻烦你", "请你", "你负责", "专注于", "任务：", "任务:", "帮我", "帮忙", "请"} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	lower := strings.ToLower(line)
	for _, prefix := range []string{"please ", "task: ", "task:", "subtask: ", "subtask:"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return line
}

func firstManagedAgentTitleClause(line string) string {
	runes := []rune(strings.TrimSpace(line))
	for idx, r := range runes {
		switch r {
		case '，', ',', '。', '.', '；', ';', '：', ':', '！', '!', '？', '?':
			if idx >= 4 {
				return strings.TrimSpace(string(runes[:idx]))
			}
		}
	}
	return strings.TrimSpace(string(runes))
}

func truncateManagedAgentTitle(line string, max int) string {
	runes := []rune(strings.TrimSpace(line))
	if len(runes) <= max {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(string(runes[:max]))
}

func deriveManagedAgentTaskTitle(raw string) string {
	text := normalizeManagedAgentDisplayName(raw)
	if text == "" {
		return ""
	}
	for _, rawLine := range strings.Split(text, "\n") {
		line := stripManagedAgentListPrefix(rawLine)
		line = trimManagedAgentPromptLead(line)
		line = normalizeManagedAgentDisplayName(line)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		line = firstManagedAgentTitleClause(line)
		line = truncateManagedAgentTitle(line, 32)
		if len([]rune(line)) >= 4 {
			return line
		}
	}
	text = truncateManagedAgentTitle(text, 32)
	if len([]rune(text)) < 4 {
		return ""
	}
	return text
}

func managedAgentLaunchDisplayName(name, prompt string) string {
	cleanName := normalizeManagedAgentDisplayName(name)
	if candidate := deriveManagedAgentTaskTitle(prompt); candidate != "" && looksTechnicalManagedAgentName(cleanName) {
		return candidate
	}
	return cleanName
}

func firstManagedAgentTextInputContent(submission TurnSubmission) string {
	for _, item := range submission.Inputs {
		if strings.EqualFold(strings.TrimSpace(item.Type), "text") {
			return item.Content
		}
	}
	return ""
}

func maybeUpdateRemoteManagedAgentName(ctx context.Context, launcher *remoteLauncher, agent *agentRuntime, submission TurnSubmission) {
	if launcher == nil || agent == nil || strings.TrimSpace(agent.remoteThreadID) == "" || !looksTechnicalManagedAgentName(agent.name) {
		return
	}
	candidate := deriveManagedAgentTaskTitle(firstManagedAgentTextInputContent(submission))
	if candidate == "" || candidate == normalizeManagedAgentDisplayName(agent.name) {
		return
	}
	if _, err := rpcCall[struct{}](ctx, launcher, "thread/name/set", map[string]any{
		"thread_id": agent.remoteThreadID,
		"name":      candidate,
	}); err != nil {
		pkglogger.Warn("remoteLauncher: thread/name/set RPC failed",
			"agent_id", agent.id,
			"thread_id", agent.remoteThreadID,
			"name", candidate,
			"error", err)
		return
	}
	agent.name = candidate
}

func (r *remoteLauncher) Launch(ctx context.Context, agent *agentRuntime, req LaunchRequest) (LaunchResult, error) {
	if agent == nil {
		return LaunchResult{}, errors.New("agent is required")
	}
	start := time.Now()
	pkglogger.Info("remoteLauncher: thread/start RPC begin", "agent_id", agent.id, "rpc_addr", r.addr)
	// thread/start treats `prompt` and `name` as legacy aliases for the same
	// display-name slot and rejects the call with -32602 when both are present
	// with different values. Collapse them here: send only `name`, falling back
	// to req.Prompt when Name is empty.
	displayName := managedAgentLaunchDisplayName(req.Name, req.Prompt)
	resp, err := rpcCall[map[string]any](ctx, r, "thread/start", map[string]any{
		"cwd":                strings.TrimSpace(req.Cwd),
		"name":               shared.FirstTrimmed(displayName, req.Prompt),
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
	maybeUpdateRemoteManagedAgentName(ctx, r, agent, submission)
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
