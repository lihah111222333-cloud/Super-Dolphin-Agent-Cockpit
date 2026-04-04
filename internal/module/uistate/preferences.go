package uistate

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	preferenceActiveThreadID           = "activeThreadId"
	preferenceActiveCmdThreadID        = "activeCmdThreadId"
	preferenceMainAgentID              = "mainAgentId"
	preferenceStallThresholdSec        = "stallThresholdSec"
	preferenceViewPrefsChat            = "viewPrefs.chat"
	preferenceViewPrefsCmd             = "viewPrefs.cmd"
	preferenceThreadPinsChat           = "threadPins.chat"
	preferenceThreadArchivesChat       = "threadArchives.chat"
	preferenceShowInjectedPromptInChat = "settings.showInjectedPromptInChat"

	recentTurnLimit = 20
)

var errInvalidStallThresholdSec = errors.New("stallThresholdSec must be >= 30 seconds")

func normalizePreferenceKey(key string) string {
	switch strings.TrimSpace(key) {
	case "active_thread_id":
		return preferenceActiveThreadID
	case "active_cmd_thread_id":
		return preferenceActiveCmdThreadID
	case "main_agent_id":
		return preferenceMainAgentID
	case "stall_threshold_sec":
		return preferenceStallThresholdSec
	case "view_prefs.chat":
		return preferenceViewPrefsChat
	case "view_prefs.cmd":
		return preferenceViewPrefsCmd
	case "thread_pins.chat":
		return preferenceThreadPinsChat
	case "thread_archives.chat":
		return preferenceThreadArchivesChat
	case "show_injected_prompt_in_chat":
		return preferenceShowInjectedPromptInChat
	default:
		return strings.TrimSpace(key)
	}
}

func buildPreferences(scope string, values map[string]any) Preferences {
	prefs := Preferences{
		CWD:            scope,
		Values:         shared.CloneJSONMap(values),
		ViewPrefs:      ViewPrefs{Chat: map[string]any{}, Cmd: map[string]any{}},
		ThreadPins:     ThreadCollections{Chat: map[string]int64{}, Cmd: map[string]int64{}},
		ThreadArchives: ThreadCollections{Chat: map[string]int64{}, Cmd: map[string]int64{}},
	}
	prefs.ActiveThreadID = stringPreferenceValue(values, preferenceActiveThreadID, "active_thread_id")
	prefs.ActiveCmdThreadID = stringPreferenceValue(values, preferenceActiveCmdThreadID, "active_cmd_thread_id")
	prefs.MainAgentID = stringPreferenceValue(values, preferenceMainAgentID, "main_agent_id")
	prefs.StallThresholdSec = positiveIntPreferenceValue(values, 30, preferenceStallThresholdSec, "stall_threshold_sec")
	prefs.ShowInjectedPromptInChat = boolPreferenceValue(values, preferenceShowInjectedPromptInChat, "show_injected_prompt_in_chat")
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
		return shared.CloneJSONMap(prefs.ViewPrefs.Chat)
	case preferenceViewPrefsCmd:
		return shared.CloneJSONMap(prefs.ViewPrefs.Cmd)
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
	state.StallThresholdSec = prefs.StallThresholdSec
	state.ShowInjectedPromptInChat = cloneBoolPtr(prefs.ShowInjectedPromptInChat)
	state.ViewPrefsChat = shared.CloneJSONMap(prefs.ViewPrefs.Chat)
	state.ViewPrefsCmd = shared.CloneJSONMap(prefs.ViewPrefs.Cmd)
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
	sidebar.ViewPrefsChat = shared.CloneJSONMap(prefs.ViewPrefs.Chat)
	sidebar.ViewPrefsCmd = shared.CloneJSONMap(prefs.ViewPrefs.Cmd)
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

func positiveIntPreferenceValue(values map[string]any, minVal int, keys ...string) int {
	value, ok := preferenceRawValueFound(values, keys...)
	if !ok {
		return 0
	}
	return asPositiveInt(value, minVal)
}

func boolPreferenceValue(values map[string]any, keys ...string) *bool {
	value, ok := preferenceRawValueFound(values, keys...)
	if !ok {
		return nil
	}
	return boolPreferencePointer(value, false)
}

func boolPreferencePointer(value any, fallback bool) *bool {
	resolved := asBool(value, fallback)
	return &resolved
}

func validatePreferenceValue(key string, value any) error {
	switch normalizePreferenceKey(key) {
	case preferenceStallThresholdSec:
		if asPositiveInt(value, 30) <= 0 {
			return errInvalidStallThresholdSec
		}
	}
	return nil
}

func preferenceRawValue(values map[string]any, keys ...string) any {
	value, _ := preferenceRawValueFound(values, keys...)
	return value
}

func preferenceRawValueFound(values map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func normalizeJSONObject(value any) map[string]any {
	typed, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return shared.CloneJSONMap(typed)
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

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return shared.CloneJSONMap(typed)
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

func asPositiveInt(value any, minVal int) int {
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil || parsed < minVal {
			return 0
		}
		return parsed
	default:
		parsed, ok := asInt64(value)
		if !ok || parsed < int64(minVal) {
			return 0
		}
		return int(parsed)
	}
}

func asBool(value any, fallback bool) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

func emptyStringAsNil(value string) any {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return nil
}
