package orchestration

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// TestTerminalPublicStateUsesProjectionLifecycle 验证真实store/projector及公开读端一致区分turn与agent终态。
func TestTerminalPublicStateUsesProjectionLifecycle(t *testing.T) {
	for _, tc := range []struct{ projection, status, want string }{
		{"turn_completed", "completed", "idle"}, {"turn_completed", "failed", "idle"}, {"turn_completed", "interrupted", "idle"},
		{"agent_stopped", "", "stopped"}, {"agent_failed", "", "failed"}, {"process_stopped", "", "stopped"}, {"process_failed", "", "failed"},
	} {
		t.Run(tc.projection+"/"+tc.status, func(t *testing.T) {
			svc, _ := newTerminalOutcomeTestService(t)
			agent := addTerminalOutcomeTestAgent(svc)
			commit := terminalPublicStateCommit(t, agent, tc.projection, tc.status)
			if _, err := svc.terminalOutcomes.CommitTerminalOutcome(context.Background(), commit); err != nil {
				t.Fatal(err)
			}
			assertTerminalPublicReadState(t, svc, commit, tc.want)
			if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "public-state-projector", time.Minute, 10); err != nil {
				t.Fatal(err)
			}
			if string(agent.state) != tc.want {
				t.Errorf("projector runtime state=%s want=%s", agent.state, tc.want)
			}
			assertTerminalPublicReadState(t, svc, commit, tc.want)
			stored, err := svc.terminalOutcomes.GetPublicTerminalOutcome(context.Background(), agent.id)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored.PublicOutcome, commit.PublicOutcome) {
				t.Fatal("public outcome changed while deriving runtime state")
			}
		})
	}
}

// terminalPublicStateCommit 使用生产转换器生成完整合法的turn或agent终态。
func terminalPublicStateCommit(t *testing.T, agent *agentRuntime, projection, status string) contract.TerminalOutcomeCommit {
	t.Helper()
	if projection != "turn_completed" {
		commit, err := terminalOutcomeCommitFromAgentTerminal(agent, agent.remoteThreadID, agentSessionID(agent), time.Date(2026, 7, 29, 1, 2, 3, 0, time.UTC), projection)
		if err != nil {
			t.Fatal(err)
		}
		return commit
	}
	base := terminalOutcomeEvent(status == "completed")
	ev := turndto.TurnCompleted{TurnHeader: base.TurnHeader, Success: status == "completed", Status: status, Summary: base.Summary, Reason: "user_request", TerminationRequestID: "interrupt-1"}
	terminal, err := turndto.NewTurnTerminalV2(ev, "public-state-event")
	if err != nil {
		t.Fatal(err)
	}
	ev, err = turndto.AttachCanonicalTurnTerminal(ev, terminal)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := terminalOutcomeCommitFromEvent(agent, ev)
	if err != nil {
		t.Fatal(err)
	}
	return commit
}

// assertTerminalPublicReadState 覆盖Snapshot、ListAgents、GetReport和GetState公开读端。
func assertTerminalPublicReadState(t *testing.T, svc *service, commit contract.TerminalOutcomeCommit, want string) {
	t.Helper()
	ctx := context.Background()
	snapshot, err := svc.Snapshot(ctx, commit.Identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != want {
		t.Errorf("Snapshot state=%s want=%s", snapshot.State, want)
	}
	if !reflect.DeepEqual(snapshot.Outcome, agentOutcomeFromPublic(commit.PublicOutcome)) {
		t.Errorf("Snapshot outcome changed: %#v", snapshot.Outcome)
	}
	report, err := svc.GetReport(ctx, commit.Identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != want {
		t.Errorf("GetReport state=%s want=%s", report.State, want)
	}
	state, err := svc.GetState(ctx, commit.Identity.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != want {
		t.Errorf("GetState state=%s want=%s", state.State, want)
	}
	assertTerminalListState(t, svc, want)
}

// assertTerminalListState 检查列表公开投影与单agent读端一致。
func assertTerminalListState(t *testing.T, svc *service, want string) {
	t.Helper()
	snapshots, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].State != want {
		t.Errorf("ListAgents snapshots=%#v want state=%s", snapshots, want)
	}
}
