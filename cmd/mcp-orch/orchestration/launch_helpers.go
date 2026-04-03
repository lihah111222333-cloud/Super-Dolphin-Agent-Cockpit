package orchestration

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
)

func cloneTurnSubmission(sub turndto.TurnSubmission) turndto.TurnSubmission {
	cloned := sub
	cloned.Inputs = append([]turndto.InputItem(nil), sub.Inputs...)
	cloned.SelectedSkills = append([]string(nil), sub.SelectedSkills...)
	cloned.OutputSchema = append([]byte(nil), sub.OutputSchema...)
	return cloned
}

type SubmissionQueue struct {
	mu    sync.Mutex
	items []turndto.TurnSubmission
}

func (q *SubmissionQueue) Enqueue(s turndto.TurnSubmission) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, cloneTurnSubmission(s))
}

func (q *SubmissionQueue) Prepend(s turndto.TurnSubmission) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append([]turndto.TurnSubmission{cloneTurnSubmission(s)}, q.items...)
}

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

// Peek is currently used only in tests; kept for diagnostic/debugging use.
func (q *SubmissionQueue) Peek() (turndto.TurnSubmission, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return turndto.TurnSubmission{}, false
	}
	return cloneTurnSubmission(q.items[0]), true
}

func (q *SubmissionQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *SubmissionQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = nil
}

func (s *service) agentForLaunchLocked(req LaunchRequest) *agentRuntime {
	agent, ok := s.agents[req.AgentID]
	if !ok {
		agent = s.newAgentLocked(req.AgentID)
		s.agents[req.AgentID] = agent
	}
	agent.name = req.Name
	agent.parentID = req.ParentID
	agent.cwd = req.Cwd
	agent.command = append([]string(nil), req.Command...)
	agent.env = append([]string(nil), req.Env...)
	agent.port = launchPort(req)
	agent.portSource = inferredLaunchSource(agent.port)
	agent.provider = launchProvider(req)
	agent.providerSource = inferredLaunchSourceString(agent.provider)
	resetRuntimeStateLocked(agent)
	agent.lastError = ""
	agent.stopRequested = false
	agent.stopReason = ""
	return agent
}

func (s *service) prepareLaunchStateLocked(ctx context.Context, agent *agentRuntime) error {
	if err := s.normalizeLaunchStateLocked(ctx, agent); err != nil {
		return err
	}
	resetRuntimeStateLocked(agent)
	agent.lastError = ""
	agent.stopRequested = false
	agent.activeTurnID = ""
	agent.threadID = ""
	agent.exitedAt = nil
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	return nil
}

func (s *service) newAgentLocked(agentID string) *agentRuntime {
	agent := &agentRuntime{
		id: agentID,
		// Initial construction starts in the machine's configured initial state.
		state:     agentdto.StateProvisioning,
		updatedAt: time.Now(),
		queue:     &SubmissionQueue{},
	}
	agent.sm = platformstatemachine.New(s.machineCfg, func() string {
		return agent.state
	}, func(next string) {
		// The state machine owns subsequent transitions through this sink.
		agent.state = next
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

func (s *service) turnIDFor(sub TurnSubmission) string {
	if turnID := strings.TrimSpace(sub.ExpectedTurnID); turnID != "" {
		return turnID
	}
	baseID := strings.TrimSpace(sub.ThreadID)
	if baseID == "" {
		baseID = strings.TrimSpace(sub.AgentID)
	}
	if baseID == "" {
		baseID = "turn"
	}
	s.nextTurnSeq++
	return fmt.Sprintf("%s-turn-%d", baseID, s.nextTurnSeq)
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
	return commandFlagValue(launchCommandArgs(req.Command), "--provider")
}

func inferredLaunchSource(value int) string {
	if value <= 0 {
		return ""
	}
	return "inferred"
}

func inferredLaunchSourceString(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "inferred"
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

func validateLaunchRequest(req LaunchRequest) error {
	if strings.TrimSpace(req.AgentID) == "" {
		return errors.New("agent id is required")
	}
	if len(req.Command) == 0 {
		return errors.New("command is required")
	}
	return nil
}

func stopProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
