package dashboard

func turnHistoryFromSnapshot(snapshot AgentSnapshot) []TurnRef {
	if snapshot.ActiveTurnID == "" && snapshot.ThreadID == "" {
		return []TurnRef{}
	}
	return []TurnRef{{
		TurnID:   snapshot.ActiveTurnID,
		ThreadID: snapshot.ThreadID,
		AgentID:  snapshot.ID,
		Status:   snapshot.State,
	}}
}
