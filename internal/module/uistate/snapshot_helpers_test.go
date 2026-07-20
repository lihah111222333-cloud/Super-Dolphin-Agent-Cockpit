package uistate

import "testing"

func TestPushRecentTurnKeepsFirstTerminalForSameThreadAndTurn(t *testing.T) {
	completed := TurnSummary{
		ID:       "turn-1",
		ThreadID: "thread-1",
		Status:   "completed",
		Success:  boolPointer(true),
	}
	failed := TurnSummary{
		ID:       "turn-1",
		ThreadID: "thread-1",
		Status:   "failed",
		Success:  boolPointer(false),
		Error:    "late failure",
	}

	items := pushRecentTurn(nil, completed, recentTurnLimit)
	items = pushRecentTurn(items, failed, recentTurnLimit)

	if len(items) != 1 {
		t.Fatalf("recent turns = %#v, want one terminal", items)
	}
	if items[0].Status != "completed" || items[0].Success == nil || !*items[0].Success || items[0].Error != "" {
		t.Fatalf("recent turn = %#v, want first completed terminal", items[0])
	}
}

func TestPushRecentTurnMatchesEmptyThreadWithCanonicalAgentAlias(t *testing.T) {
	items := pushRecentTurn(nil, TurnSummary{
		ID:      "turn-1",
		AgentID: "agent-public",
		Status:  "completed",
		Success: boolPointer(true),
	}, recentTurnLimit)
	items = pushRecentTurn(items, TurnSummary{
		ID:       "turn-1",
		ThreadID: "agent-public",
		AgentID:  "agent-public",
		Status:   "failed",
		Success:  boolPointer(false),
		Error:    "late failure",
	}, recentTurnLimit)

	if len(items) != 1 || items[0].Status != "completed" || items[0].Success == nil || !*items[0].Success {
		t.Fatalf("recent turns = %#v, want one first completed terminal", items)
	}
}

func TestPushRecentTurnKeepsSameTurnIDOnDistinctCanonicalThreads(t *testing.T) {
	items := pushRecentTurn(nil, TurnSummary{
		ID:       "turn-1",
		ThreadID: "thread-1",
		Status:   "completed",
		Success:  boolPointer(true),
	}, recentTurnLimit)
	items = pushRecentTurn(items, TurnSummary{
		ID:       "turn-1",
		ThreadID: "thread-2",
		Status:   "failed",
		Success:  boolPointer(false),
	}, recentTurnLimit)

	if len(items) != 2 {
		t.Fatalf("recent turns = %#v, want separate canonical thread identities", items)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
