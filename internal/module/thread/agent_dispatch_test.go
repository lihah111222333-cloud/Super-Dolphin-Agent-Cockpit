package thread

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// dispatchRecorder observes which assembler entry point the thread helper
// routes through, so the test can assert the AgentType-based dispatch rule.
type dispatchRecorder struct {
	calledStart bool
	calledAgent bool
	seenAgent   contract.AgentType
}

func (d *dispatchRecorder) AssembleStart(_ context.Context, in contract.StartInput) (contract.StartAssembly, error) {
	d.calledStart = true
	return contract.StartAssembly{DisplayName: in.Name, BaseInstructions: "start-path"}, nil
}

func (d *dispatchRecorder) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (d *dispatchRecorder) AssembleAgent(_ context.Context, in contract.AgentInput) (contract.StartAssembly, error) {
	d.calledAgent = true
	d.seenAgent = in.AgentType
	return contract.StartAssembly{DisplayName: in.StartInput.Name, BaseInstructions: "agent-path"}, nil
}

func (d *dispatchRecorder) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

// TestDispatchPromptAssembly_ExploreRoutesToAgentPath 验证 Explore/Plan 类型会走 agent prompt 装配路径。
// launch_agent 传入 Claude AgentType 时必须调用 AssembleAgent，
// 不能退回普通 thread start 的 AssembleStart。
func TestDispatchPromptAssembly_ExploreRoutesToAgentPath(t *testing.T) {
	recorder := &dispatchRecorder{}
	req := StartRequest{
		Name:              "explorer",
		AgentType:         string(contract.AgentTypeExplore),
		PromptAssemblyRef: recorder,
	}
	input := contract.StartInput{
		Name:      req.Name,
		AgentType: req.AgentType,
	}
	got, err := dispatchPromptAssembly(context.Background(), req, input)
	if err != nil {
		t.Fatalf("dispatchPromptAssembly() error = %v", err)
	}
	if recorder.calledStart {
		t.Fatal("Explore AgentType must not call AssembleStart")
	}
	if !recorder.calledAgent {
		t.Fatal("Explore AgentType must call AssembleAgent")
	}
	if recorder.seenAgent != contract.AgentTypeExplore {
		t.Fatalf("AssembleAgent saw AgentType=%q, want Explore", recorder.seenAgent)
	}
	if got.BaseInstructions != "agent-path" {
		t.Fatalf("BaseInstructions = %q, want agent-path", got.BaseInstructions)
	}
}

// TestDispatchPromptAssembly_UnknownAgentTypeFallsBack ensures user-defined
// or legacy AgentType values ("worker", "main", "Writer", ...) keep routing
// through AssembleStart, so historical orchestration callers are unaffected.
func TestDispatchPromptAssembly_UnknownAgentTypeFallsBack(t *testing.T) {
	cases := []string{"", "worker", "main", "Writer", "sub"}
	for _, agentType := range cases {
		t.Run("agentType="+agentType, func(t *testing.T) {
			recorder := &dispatchRecorder{}
			req := StartRequest{
				Name:              "legacy",
				AgentType:         agentType,
				PromptAssemblyRef: recorder,
			}
			input := contract.StartInput{
				Name:      req.Name,
				AgentType: strings.TrimSpace(agentType),
			}
			if _, err := dispatchPromptAssembly(context.Background(), req, input); err != nil {
				t.Fatalf("dispatchPromptAssembly() error = %v", err)
			}
			if !recorder.calledStart {
				t.Fatalf("AgentType=%q expected AssembleStart path", agentType)
			}
			if recorder.calledAgent {
				t.Fatalf("AgentType=%q must not call AssembleAgent", agentType)
			}
		})
	}
}

// TestDispatchPromptAssembly_PlanRoutesToAgentPath mirrors the Explore test
// for the Plan agent type, confirming both Claude-taxonomy values flow
// through the subagent path.
func TestDispatchPromptAssembly_PlanRoutesToAgentPath(t *testing.T) {
	recorder := &dispatchRecorder{}
	req := StartRequest{
		Name:              "planner",
		AgentType:         string(contract.AgentTypePlan),
		PromptAssemblyRef: recorder,
	}
	input := contract.StartInput{
		Name:      req.Name,
		AgentType: req.AgentType,
	}
	if _, err := dispatchPromptAssembly(context.Background(), req, input); err != nil {
		t.Fatalf("dispatchPromptAssembly() error = %v", err)
	}
	if !recorder.calledAgent || recorder.seenAgent != contract.AgentTypePlan {
		t.Fatalf("Plan AgentType routing failed: calledAgent=%v seenAgent=%q", recorder.calledAgent, recorder.seenAgent)
	}
}
