package orchestration

import (
	"context"
	"testing"

	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

type cliInterruptFenceSpy struct {
	cliBackendFixture
	legacyCalls, fencedCalls int
	agentID, source          string
	ref                      turndto.TurnRefV1
	seq                      uint64
}

func (s *cliInterruptFenceSpy) Interrupt(context.Context, string, string) error {
	s.legacyCalls++
	return nil
}

func (s *cliInterruptFenceSpy) InterruptTurn(_ context.Context, agentID string, ref turndto.TurnRefV1, seq uint64, source string) error {
	s.fencedCalls++
	s.agentID, s.ref, s.seq, s.source = agentID, ref, seq, source
	return nil
}

// TestCLIInterruptFenceContract 锁住真实 bridge 的旧快照透传，不得按 AgentID 重查当前轮。
func TestCLIInterruptFenceContract(t *testing.T) {
	t.Run("native-receipt-fence", TestNativeReceiptExecutionFence)
	t.Run("reject-legacy", TestCLIInterruptFenceRejectsLegacy)
	spy := &cliInterruptFenceSpy{}
	launcher := &cliLauncher{backend: spy}
	agent := &agentRuntime{id: "agent", remoteThreadID: "old-thread", activeTurnID: "old-turn", launchSeq: 7}
	if err := launcher.Interrupt(context.Background(), agent, "orphan"); err != nil {
		t.Fatal(err)
	}
	if spy.legacyCalls != 0 || spy.fencedCalls != 1 {
		t.Fatalf("lost interrupt fence: legacy=%d fenced=%d", spy.legacyCalls, spy.fencedCalls)
	}
	if spy.agentID != "agent" || spy.ref != (turndto.TurnRefV1{ThreadID: "old-thread", TurnID: "old-turn"}) || spy.seq != 7 || spy.source != "orphan" {
		t.Fatalf("snapshot lost: %+v", spy)
	}
}

// TestCLIInterruptFenceRejectsLegacy 拒绝不支持 fenced 中断的 backend，不能降级调用旧中断。
func TestCLIInterruptFenceRejectsLegacy(t *testing.T) {
	launcher := &cliLauncher{backend: &cliBackendFixture{}}
	if err := launcher.Interrupt(context.Background(), &agentRuntime{id: "agent"}, "cleanup"); err == nil {
		t.Fatal("legacy backend accepted")
	}
	if err := launcher.Interrupt(context.Background(), nil, "cleanup"); err == nil {
		t.Fatal("nil agent accepted")
	}
}
