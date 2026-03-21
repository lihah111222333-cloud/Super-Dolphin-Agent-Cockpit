package dashboard

import "strings"

func turnHistoryFromSnapshot(snapshot AgentSnapshot) []TurnRef {
	turnID := strings.TrimSpace(snapshot.ActiveTurnID)
	if turnID == "" {
		return []TurnRef{}
	}
	return []TurnRef{{
		TurnID:   turnID,
		ThreadID: strings.TrimSpace(snapshot.ThreadID),
		AgentID:  strings.TrimSpace(snapshot.ID),
		Status:   strings.TrimSpace(snapshot.State),
	}}
}
