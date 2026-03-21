package uistate

import (
	"encoding/json"
	"strings"
)

const (
	preferenceActiveThreadID     = "activeThreadId"
	preferenceActiveCmdThreadID  = "activeCmdThreadId"
	preferenceMainAgentID        = "mainAgentId"
	preferenceViewPrefsChat      = "viewPrefs.chat"
	preferenceViewPrefsCmd       = "viewPrefs.cmd"
	preferenceThreadPinsChat     = "threadPins.chat"
	preferenceThreadArchivesChat = "threadArchives.chat"

	recentTurnLimit = 20
)

func normalizePreferenceKey(key string) string {
	switch strings.TrimSpace(key) {
	case "active_thread_id":
		return preferenceActiveThreadID
	case "active_cmd_thread_id":
		return preferenceActiveCmdThreadID
	case "main_agent_id":
		return preferenceMainAgentID
	case "view_prefs.chat":
		return preferenceViewPrefsChat
	case "view_prefs.cmd":
		return preferenceViewPrefsCmd
	case "thread_pins.chat":
		return preferenceThreadPinsChat
	case "thread_archives.chat":
		return preferenceThreadArchivesChat
	default:
		return strings.TrimSpace(key)
	}
}

func buildPreferences(scope string, values map[string]any) Preferences {
	prefs := Preferences{
		CWD:            scope,
		Values:         cloneJSONMap(values),
		ViewPrefs:      ViewPrefs{Chat: map[string]any{}, Cmd: map[string]any{}},
		ThreadPins:     ThreadCollections{Chat: map[string]int64{}, Cmd: map[string]int64{}},
		ThreadArchives: ThreadCollections{Chat: map[string]int64{}, Cmd: map[string]int64{}},
	}
	prefs.ActiveThreadID = stringPreferenceValue(values, preferenceActiveThreadID, "active_thread_id")
	prefs.ActiveCmdThreadID = stringPreferenceValue(values, preferenceActiveCmdThreadID, "active_cmd_thread_id")
	prefs.MainAgentID = stringPreferenceValue(values, preferenceMainAgentID, "main_agent_id")
	prefs.ViewPrefs.Chat = normalizeJSONObject(preferenceRawValue(values, preferenceViewPrefsChat, "view_prefs.chat"))
	prefs.ViewPrefs.Cmd = normalizeJSONObject(preferenceRawValue(values, preferenceViewPrefsCmd, "view_prefs.cmd"))
	prefs.ThreadPins.Chat = normalizeTimestampMap(preferenceRawValue(values, preferenceThreadPinsChat, "thread_pins.chat"))
	prefs.ThreadArchives.Chat = normalizeTimestampMap(preferenceRawValue(values, preferenceThreadArchivesChat, "thread_archives.chat"))
	return prefs
}

func preferenceValue(prefs Preferences, key string) any {
	switch normalizePreferenceKey(key) {
	case preferenceActiveThreadID:
		return emptyStringAsNil(prefs.ActiveThreadID)
	case preferenceActiveCmdThreadID:
		return emptyStringAsNil(prefs.ActiveCmdThreadID)
	case preferenceMainAgentID:
		return emptyStringAsNil(prefs.MainAgentID)
	case preferenceViewPrefsChat:
		return cloneJSONMap(prefs.ViewPrefs.Chat)
	case preferenceViewPrefsCmd:
		return cloneJSONMap(prefs.ViewPrefs.Cmd)
	case preferenceThreadPinsChat:
		return cloneTimestampMap(prefs.ThreadPins.Chat)
	case preferenceThreadArchivesChat:
		return cloneTimestampMap(prefs.ThreadArchives.Chat)
	default:
		rawKey := strings.TrimSpace(key)
		value, ok := prefs.Values[rawKey]
		if !ok {
			value, ok = prefs.Values[normalizePreferenceKey(rawKey)]
		}
		if !ok {
			return nil
		}
		return cloneJSONValue(value)
	}
}

func applyPreferencesToState(state *UIState, prefs *Preferences) {
	if state == nil || prefs == nil {
		return
	}
	state.ActiveThreadID = prefs.ActiveThreadID
	state.ActiveCmdThreadID = prefs.ActiveCmdThreadID
	state.MainAgentID = deriveMainAgentID(state.Agents, prefs.MainAgentID)
	state.ViewPrefsChat = cloneJSONMap(prefs.ViewPrefs.Chat)
	state.ViewPrefsCmd = cloneJSONMap(prefs.ViewPrefs.Cmd)
	state.ThreadPinsChat = cloneTimestampMap(prefs.ThreadPins.Chat)
	state.ThreadArchivesChat = cloneTimestampMap(prefs.ThreadArchives.Chat)
	state.Groups = buildThreadGroups(state.Threads, prefs.ThreadPins.Chat, prefs.ThreadArchives.Chat)
}

func applyPreferencesToSidebar(sidebar *Sidebar, prefs *Preferences) {
	if sidebar == nil || prefs == nil {
		return
	}
	sidebar.ActiveThreadID = prefs.ActiveThreadID
	sidebar.ActiveCmdThreadID = prefs.ActiveCmdThreadID
	sidebar.MainAgentID = deriveMainAgentID(sidebar.Agents, prefs.MainAgentID)
	sidebar.ViewPrefsChat = cloneJSONMap(prefs.ViewPrefs.Chat)
	sidebar.ViewPrefsCmd = cloneJSONMap(prefs.ViewPrefs.Cmd)
	sidebar.ThreadPinsChat = cloneTimestampMap(prefs.ThreadPins.Chat)
	sidebar.ThreadArchivesChat = cloneTimestampMap(prefs.ThreadArchives.Chat)
	sidebar.Groups = buildThreadGroups(sidebar.Threads, prefs.ThreadPins.Chat, prefs.ThreadArchives.Chat)
}

func deriveMainAgentID(agents []AgentSummary, current string) string {
	if current = strings.TrimSpace(current); current != "" {
		return current
	}
	for _, agent := range agents {
		if agent.ThreadID != "" {
			return agent.ID
		}
	}
	if len(agents) == 0 {
		return ""
	}
	return strings.TrimSpace(agents[0].ID)
}

func buildThreadGroups(threads []ThreadSummary, pinned, archived map[string]int64) []ThreadGroup {
	var pinnedThreads, archivedThreads, otherThreads []ThreadSummary
	for _, thread := range threads {
		switch {
		case archived[thread.ID] > 0:
			archivedThreads = append(archivedThreads, thread)
		case pinned[thread.ID] > 0:
			pinnedThreads = append(pinnedThreads, thread)
		default:
			otherThreads = append(otherThreads, thread)
		}
	}
	groups := make([]ThreadGroup, 0, 3)
	if len(pinnedThreads) > 0 {
		groups = append(groups, ThreadGroup{Key: "pinned", Title: "Pinned", Threads: cloneThreads(pinnedThreads)})
	}
	if len(otherThreads) > 0 {
		groups = append(groups, ThreadGroup{Key: "threads", Title: "Threads", Threads: cloneThreads(otherThreads)})
	}
	if len(archivedThreads) > 0 {
		groups = append(groups, ThreadGroup{Key: "archived", Title: "Archived", Threads: cloneThreads(archivedThreads)})
	}
	return groups
}

func stringPreferenceValue(values map[string]any, keys ...string) string {
	value := preferenceRawValue(values, keys...)
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func preferenceRawValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func normalizeJSONObject(value any) map[string]any {
	typed, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return cloneJSONMap(typed)
}

func normalizeTimestampMap(value any) map[string]int64 {
	typed, ok := value.(map[string]any)
	if !ok {
		return map[string]int64{}
	}
	out := make(map[string]int64, len(typed))
	for rawKey, rawValue := range typed {
		id := strings.TrimSpace(rawKey)
		if id == "" {
			continue
		}
		if ts, ok := asInt64(rawValue); ok && ts > 0 {
			out[id] = ts
		}
	}
	return out
}

func cloneJSONMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = cloneJSONValue(typed[i])
		}
		return out
	case json.Number, string, bool, nil, float64, int64, int32, int, uint64, uint32, uint:
		return typed
	default:
		return typed
	}
}

func cloneTimestampMap(input map[string]int64) map[string]int64 {
	if len(input) == 0 {
		return map[string]int64{}
	}
	out := make(map[string]int64, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func asInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func emptyStringAsNil(value string) any {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return nil
}
