package uistate

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
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
	preferenceProviderActive           = "settings.provider.active"

	defaultProviderActive = "codex"
	recentTurnLimit       = 20
)

var errInvalidStallThresholdSec = errors.New("stallThresholdSec must be >= 30 seconds")

func withPreferenceScope(ctx context.Context, cwd string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, preferenceScopeKey{}, strings.TrimSpace(cwd))
}

func preferenceScopeFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(preferenceScopeKey{}).(string)
	return strings.TrimSpace(value)
}

func fallbackPreferenceKey(scope, key string) string { return scope + "\x1f" + strings.TrimSpace(key) }

func splitFallbackPreferenceKey(raw string) (string, string, bool) {
	scope, key, ok := strings.Cut(raw, "\x1f")
	return scope, strings.TrimSpace(key), ok && strings.TrimSpace(key) != ""
}

// normalizePreferenceKey 规范化preference键。
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
	prefValues := clone.JSONMap(values)
	applyPreferenceDefaults(scope, prefValues)
	prefs := Preferences{
		CWD:            scope,
		Values:         prefValues,
		ViewPrefs:      ViewPrefs{Chat: map[string]any{}, Cmd: map[string]any{}},
		ThreadPins:     ThreadCollections{Chat: map[string]int64{}, Cmd: map[string]int64{}},
		ThreadArchives: ThreadCollections{Chat: map[string]int64{}, Cmd: map[string]int64{}},
	}
	prefs.ActiveThreadID = stringPreferenceValue(prefValues, preferenceActiveThreadID, "active_thread_id")
	prefs.ActiveCmdThreadID = stringPreferenceValue(prefValues, preferenceActiveCmdThreadID, "active_cmd_thread_id")
	prefs.MainAgentID = stringPreferenceValue(prefValues, preferenceMainAgentID, "main_agent_id")
	prefs.StallThresholdSec = positiveIntPreferenceValue(prefValues, 30, preferenceStallThresholdSec, "stall_threshold_sec")
	prefs.ShowInjectedPromptInChat = boolPreferenceValue(prefValues, preferenceShowInjectedPromptInChat, "show_injected_prompt_in_chat")
	prefs.ViewPrefs.Chat = normalizeJSONObject(preferenceRawValue(prefValues, preferenceViewPrefsChat, "view_prefs.chat"))
	prefs.ViewPrefs.Cmd = normalizeJSONObject(preferenceRawValue(prefValues, preferenceViewPrefsCmd, "view_prefs.cmd"))
	prefs.ThreadPins.Chat = normalizeTimestampMap(preferenceRawValue(prefValues, preferenceThreadPinsChat, "thread_pins.chat"))
	prefs.ThreadArchives.Chat = normalizeTimestampMap(preferenceRawValue(prefValues, preferenceThreadArchivesChat, "thread_archives.chat"))
	return prefs
}

func applyPreferenceDefaults(_ string, values map[string]any) {
	if values == nil {
		return
	}
	value, ok := values[preferenceProviderActive]
	if !ok || isEmptyProviderPreference(value) {
		values[preferenceProviderActive] = defaultProviderActive
	}
}

func isEmptyProviderPreference(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

// preferenceValue 处理preference值。
func preferenceValue(prefs Preferences, key string) any {
	switch normalizePreferenceKey(key) {
	case preferenceActiveThreadID:
		return emptyStringAsNil(prefs.ActiveThreadID)
	case preferenceActiveCmdThreadID:
		return emptyStringAsNil(prefs.ActiveCmdThreadID)
	case preferenceMainAgentID:
		return emptyStringAsNil(prefs.MainAgentID)
	case preferenceViewPrefsChat:
		return clone.JSONMap(prefs.ViewPrefs.Chat)
	case preferenceViewPrefsCmd:
		return clone.JSONMap(prefs.ViewPrefs.Cmd)
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
	state.ActiveThreadID = activeThreadPreference(state.Threads, prefs.ActiveThreadID)
	state.ActiveCmdThreadID = activeThreadPreference(state.Threads, prefs.ActiveCmdThreadID)
	state.MainAgentID = deriveMainAgentID(state.Agents, prefs.MainAgentID)
	state.StallThresholdSec = prefs.StallThresholdSec
	state.ShowInjectedPromptInChat = cloneBoolPtr(prefs.ShowInjectedPromptInChat)
	state.ViewPrefsChat = clone.JSONMap(prefs.ViewPrefs.Chat)
	state.ViewPrefsCmd = clone.JSONMap(prefs.ViewPrefs.Cmd)
	state.ThreadPinsChat = cloneTimestampMap(prefs.ThreadPins.Chat)
	state.ThreadArchivesChat = projectArchivedThreadStatus(state.Threads, prefs.ThreadArchives.Chat)
	state.Groups = buildThreadGroups(state.Threads, prefs.ThreadPins.Chat, state.ThreadArchivesChat)
}

func applyPreferencesToSidebar(sidebar *Sidebar, prefs *Preferences) {
	if sidebar == nil || prefs == nil {
		return
	}
	sidebar.ActiveThreadID = activeThreadPreference(sidebar.Threads, prefs.ActiveThreadID)
	sidebar.ActiveCmdThreadID = activeThreadPreference(sidebar.Threads, prefs.ActiveCmdThreadID)
	sidebar.MainAgentID = deriveMainAgentID(sidebar.Agents, prefs.MainAgentID)
	sidebar.ViewPrefsChat = clone.JSONMap(prefs.ViewPrefs.Chat)
	sidebar.ViewPrefsCmd = clone.JSONMap(prefs.ViewPrefs.Cmd)
	sidebar.ThreadPinsChat = cloneTimestampMap(prefs.ThreadPins.Chat)
	sidebar.ThreadArchivesChat = projectArchivedThreadStatus(sidebar.Threads, prefs.ThreadArchives.Chat)
	sidebar.Groups = buildThreadGroups(sidebar.Threads, prefs.ThreadPins.Chat, sidebar.ThreadArchivesChat)
}

func activeThreadPreference(threads []ThreadSummary, current string) string {
	current = strings.TrimSpace(current)
	if current == "" {
		return ""
	}
	for _, thread := range threads {
		if strings.TrimSpace(thread.ID) == current {
			return current
		}
	}
	return ""
}

// projectArchivedThreadStatus merges DB-side archived status into the
// preference-driven archive map. LifecycleStatus is the DB lifecycle truth;
// State is only a fallback for old in-memory/test snapshots because it is a
// runtime/UI union field overwritten by deriveThreadStatuses.
// projectArchivedThreadStatus 处理项目archived线程状态。
func projectArchivedThreadStatus(threads []ThreadSummary, archived map[string]int64) map[string]int64 {
	out := cloneTimestampMap(archived)
	for _, thread := range threads {
		id := strings.TrimSpace(thread.ID)
		if id == "" {
			continue
		}
		lifecycle := strings.TrimSpace(thread.LifecycleStatus)
		if lifecycle == "" {
			lifecycle = strings.TrimSpace(thread.State)
		}
		if !strings.EqualFold(lifecycle, "archived") {
			continue
		}
		if out[id] < 1 {
			out[id] = archivedThreadTimestamp(thread)
		}
	}
	return out
}

func archivedThreadTimestamp(thread ThreadSummary) int64 {
	for _, ts := range []*time.Time{thread.UpdatedAt, thread.CreatedAt} {
		if ts != nil && !ts.IsZero() {
			return ts.UnixMilli()
		}
	}
	return 1
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

// buildThreadGroups 构建线程groups。
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
	return clone.JSONMap(typed)
}

// normalizeTimestampMap 规范化timestampmap。
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
		return clone.JSONMap(typed)
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

// asInt64 把uistate处理为int64。
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

// asPositiveInt 把uistate处理为positiveint。
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
