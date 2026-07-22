package uistate

import "testing"

func TestPushRecentTurnKeepsFirstTerminalForSameThreadAndTurn(t *testing.T) {
	tests := []struct {
		name         string
		firstStatus  string
		firstSuccess bool
		nextStatus   string
		nextSuccess  bool
	}{
		{name: "completed", firstStatus: "completed", firstSuccess: true, nextStatus: "failed", nextSuccess: false},
		{name: "failed", firstStatus: "failed", firstSuccess: false, nextStatus: "completed", nextSuccess: true},
		{name: "interrupted", firstStatus: "interrupted", firstSuccess: false, nextStatus: "failed", nextSuccess: false},
		{name: "cancelled", firstStatus: "cancelled", firstSuccess: false, nextStatus: "failed", nextSuccess: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := TurnSummary{
				ID:       "turn-1",
				ThreadID: "thread-1",
				Status:   tt.firstStatus,
				Success:  boolPointer(tt.firstSuccess),
			}
			next := TurnSummary{
				ID:       "turn-1",
				ThreadID: "thread-1",
				Status:   tt.nextStatus,
				Success:  boolPointer(tt.nextSuccess),
				Error:    "late terminal",
			}

			items := pushRecentTurn(nil, first, recentTurnLimit)
			items = pushRecentTurn(items, next, recentTurnLimit)

			if len(items) != 1 {
				t.Fatalf("recent turns = %#v, want one terminal", items)
			}
			if got := items[0]; got.Status != tt.firstStatus || got.Success == nil || *got.Success != tt.firstSuccess || got.Error != "" {
				t.Fatalf("recent turn = %#v, want first %s terminal", got, tt.firstStatus)
			}
		})
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
