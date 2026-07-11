package orchestration

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/processctl"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformstatemachine "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/statemachine"
)

func cloneTurnSubmission(sub turndto.TurnSubmission) turndto.TurnSubmission {
	cloned := sub
	cloned.Inputs = append([]turndto.InputItem(nil), sub.Inputs...)
	cloned.SelectedSkills = append([]string(nil), sub.SelectedSkills...)
	cloned.OutputSchema = append([]byte(nil), sub.OutputSchema...)
	return cloned
}

// SubmissionQueue 保存单个 agent 尚未领取的 turn 队列。
// 入队和出队都会复制 payload，避免调用方在锁外修改队列中的 turn。
type SubmissionQueue struct {
	mu    sync.Mutex
	items []turndto.TurnSubmission
}

// Enqueue 把项目追加到队尾。
func (q *SubmissionQueue) Enqueue(s turndto.TurnSubmission) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, cloneTurnSubmission(s))
}

// Prepend 把项目放到队头。
func (q *SubmissionQueue) Prepend(s turndto.TurnSubmission) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append([]turndto.TurnSubmission{cloneTurnSubmission(s)}, q.items...)
}

// Dequeue 从队头取出项目。
func (q *SubmissionQueue) Dequeue() (turndto.TurnSubmission, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return turndto.TurnSubmission{}, false
	}
	s := q.items[0]
	q.items = q.items[1:]
	return cloneTurnSubmission(s), true
}

// Peek 查看队头 turn 但不移除。
// 生产路径不依赖它，主要用于诊断和测试队列顺序。
func (q *SubmissionQueue) Peek() (turndto.TurnSubmission, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return turndto.TurnSubmission{}, false
	}
	return cloneTurnSubmission(q.items[0]), true
}

// Len 返回队列当前长度。
func (q *SubmissionQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Clear 清空队列中尚未领取的 turn。
func (q *SubmissionQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = nil
}

func (s *service) agentForLaunchLocked(req LaunchRequest) *agentRuntime {
	return s.registry.agentForLaunchLocked(req, s.newAgentLocked)
}

func (s *service) prepareLaunchStateLocked(ctx context.Context, agent *agentRuntime) error {
	if err := s.normalizeLaunchStateLocked(ctx, agent); err != nil {
		return err
	}
	resetRuntimeStateLocked(agent)
	clearAgentLifecycleErrorLocked(agent)
	clearAgentTurnStateLocked(agent)
	clearAgentAutoRecoveryLocked(agent)
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	return nil
}

func (s *service) newAgentLocked(agentID string) *agentRuntime {
	agent := &agentRuntime{
		id: agentID,
		// 初始构造从状态机配置的初始状态开始。
		state:     agentdto.StateProvisioning,
		updatedAt: time.Now(),
		queue:     &SubmissionQueue{},
	}
	agent.sm = platformstatemachine.New(s.lifecycle.machineCfg, func() string {
		return string(agent.state)
	}, func(next string) {
		// 后续状态转换都由状态机通过这个 sink 写回 agent.state。
		agent.state = agentdto.AgentState(next)
	})
	return agent
}

func (s *service) normalizeLaunchStateLocked(ctx context.Context, agent *agentRuntime) error {
	switch agent.state {
	case "", agentdto.StateProvisioning, agentdto.StateRecovering:
		return nil
	default:
		return s.fireOrForceLocked(ctx, agent, agentdto.TriggerRecoverRequested)
	}
}

func launchPort(req LaunchRequest) int {
	if port := parsePositiveInt(envValue(req.Env, "PORT")); port > 0 {
		return port
	}
	args := launchCommandArgs(req.Command)
	for _, flag := range []string{"--port", "-p"} {
		if port := parsePositiveInt(commandFlagValue(args, flag)); port > 0 {
			return port
		}
	}
	return 0
}

func launchProvider(req LaunchRequest) string {
	for _, key := range []string{"AGENT_PROVIDER", "CODEX_PROVIDER", "PROVIDER"} {
		if value := envValue(req.Env, key); value != "" {
			return value
		}
	}
	if value := commandFlagValue(launchCommandArgs(req.Command), "--provider"); value != "" {
		return value
	}
	return "codex"
}

func launchCommandArgs(command []string) []string {
	if len(command) <= 1 {
		return nil
	}
	return command[1:]
}

func envValue(env []string, key string) string {
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(strings.TrimSpace(name), key) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func envCSVValue(env []string, key string) []string {
	raw := envValue(env, key)
	if raw == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for item := range strings.SplitSeq(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// launchStartConfig 把 launch env 中的运行配置转成 thread/start config。
// 这里集中处理子 agent 工具禁用和 Codex 实例身份，避免 remoteLauncher.Launch 继续膨胀。
func launchStartConfig(env []string) map[string]any {
	config := map[string]any{}
	if disabledTools := envValue(env, "AGENT_DISABLED_TOOLS"); disabledTools != "" {
		config["disallowed_tools"] = disabledTools
	}
	if disabledNativeTools := envCSVValue(env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS"); len(disabledNativeTools) > 0 {
		config["codexDisabledNativeTools"] = disabledNativeTools
	}
	if codexHome := envValue(env, "AGENT_CODEX_HOME"); codexHome != "" {
		config["codexHome"] = codexHome
	}
	if codexInstanceKey := envValue(env, "AGENT_CODEX_INSTANCE_KEY"); codexInstanceKey != "" {
		config["codexInstanceKey"] = codexInstanceKey
	}
	if codexModelProvider := envValue(env, "AGENT_CODEX_MODEL_PROVIDER"); codexModelProvider != "" {
		config["codexModelProvider"] = codexModelProvider
	}
	return config
}

func commandFlagValue(args []string, flag string) string {
	for idx, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == flag && idx+1 < len(args) {
			return strings.TrimSpace(args[idx+1])
		}
		if value, ok := strings.CutPrefix(arg, flag+"="); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parsePositiveInt(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func validateLaunchRequestForLauncher(req LaunchRequest, launcher AgentLauncher) error {
	if strings.TrimSpace(req.AgentID) == "" {
		return errors.New("agent id is required")
	}
	if err := contract.ValidateLaunchCWD(req.Cwd, req.ParentID); err != nil {
		return err
	}
	if _, isRemote := launcher.(*remoteLauncher); !isRemote && len(req.Command) == 0 {
		return errors.New("command is required")
	}
	return nil
}

func stopProcess(cmd *exec.Cmd) error {
	return processctl.ForceStop(cmd, nil)
}

func closeAgentProcessGuard(agent *agentRuntime) {
	if agent == nil || agent.processGuard == nil {
		return
	}
	agent.processGuard.Close()
	agent.processGuard = nil
}
