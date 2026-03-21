package uistate

import (
	"sort"
	"strings"
	"time"
)

func cloneThreadGroups(items []ThreadGroup) []ThreadGroup {
	out := make([]ThreadGroup, len(items))
	for i := range items {
		out[i] = ThreadGroup{
			Key:     items[i].Key,
			Title:   items[i].Title,
			Threads: cloneThreads(items[i].Threads),
		}
	}
	return out
}

func cloneViewPrefs(value ViewPrefs) ViewPrefs {
	return ViewPrefs{
		Chat: cloneJSONMap(value.Chat),
		Cmd:  cloneJSONMap(value.Cmd),
	}
}

func cloneThreadCollections(value ThreadCollections) ThreadCollections {
	return ThreadCollections{
		Chat: cloneTimestampMap(value.Chat),
		Cmd:  cloneTimestampMap(value.Cmd),
	}
}

func pushRecentTurn(items []TurnSummary, next TurnSummary, limit int) []TurnSummary {
	next.ID = strings.TrimSpace(next.ID)
	if next.ID == "" {
		return items
	}
	updated := false
	for i := range items {
		if items[i].ID != next.ID {
			continue
		}
		items[i] = next
		updated = true
		break
	}
	if !updated {
		items = append(items, next)
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := recentTurnTime(items[i]), recentTurnTime(items[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return items[i].ID < items[j].ID
	})
	if limit > 0 && len(items) > limit {
		items = append([]TurnSummary(nil), items[:limit]...)
	}
	return items
}

func markThreadStopped(items []ThreadSummary, threadID, status string) []ThreadSummary {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return items
	}
	if status = strings.TrimSpace(status); status == "" {
		status = "stopped"
	}
	for i := range items {
		if items[i].ID != threadID {
			continue
		}
		items[i].AgentID = ""
		items[i].State = status
		return items
	}
	return append(items, ThreadSummary{ID: threadID, State: status})
}

func recentTurnTime(value TurnSummary) time.Time {
	if value.CompletedAt != nil {
		return *value.CompletedAt
	}
	return zeroTime(value.StartedAt)
}
