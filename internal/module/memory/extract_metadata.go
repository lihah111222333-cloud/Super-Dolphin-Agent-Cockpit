package memory

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

type threadRuntimeMetadata struct {
	resolved         bool
	parentAgentID    string
	threadKind       string
	ownerThreadID    string
	agentMemoryScope string
	sessionFlags     map[string]bool
	bareMode         bool
}

type storedThreadRuntime struct {
	Runtime map[string]any `json:"runtime,omitempty"`
}

func (h *MemoryLifecycleHooks) shouldExtractThread(ctx context.Context, evt turndto.TurnCompleted) bool {
	if h == nil || !h.extractOnStop || !evt.Success {
		return false
	}
	return h.resolveThreadRuntimeMetadata(ctx, strings.TrimSpace(evt.ThreadID)).isAutoMemoryRootThread()
}

// resolveThreadRuntimeMetadata 解析线程运行时元数据。
func (h *MemoryLifecycleHooks) resolveThreadRuntimeMetadata(ctx context.Context, threadID string) threadRuntimeMetadata {
	if h == nil || h.threadStore == nil || threadID == "" {
		return threadRuntimeMetadata{}
	}
	thread, err := h.threadStore.GetByThreadID(ctx, threadID)
	if err != nil || thread == nil {
		return threadRuntimeMetadata{}
	}
	return resolveThreadRuntimeMetadataFromThread(thread)
}

// resolveThreadRuntimeMetadataFromThread 从线程解析线程运行时元数据。
func resolveThreadRuntimeMetadataFromThread(thread *contract.ThreadMetadata) threadRuntimeMetadata {
	if thread == nil {
		return threadRuntimeMetadata{}
	}
	meta := threadRuntimeMetadata{
		resolved:         true,
		parentAgentID:    strings.TrimSpace(thread.ParentAgentID),
		ownerThreadID:    strings.TrimSpace(thread.OwnerThreadID),
		agentMemoryScope: strings.TrimSpace(thread.AgentMemoryScope),
	}
	cfg := decodeStoredThreadRuntime(thread)
	if meta.parentAgentID == "" {
		meta.parentAgentID = firstRuntimeString(cfg, "parent_agent_id", "parentAgentId", "parentId", "parentID")
	}
	meta.threadKind = firstRuntimeString(cfg, "thread_kind", "threadKind")
	meta.sessionFlags = runtimeBoolMap(cfg, "sessionFlags", "session_flags")
	meta.bareMode = runtimeFlagEnabled(cfg, "bare_mode", "bareMode", "bare")
	if meta.threadKind == "" && (meta.parentAgentID != "" || meta.ownerThreadID != "") {
		meta.threadKind = "fork"
	}
	return meta
}

func decodeStoredThreadRuntime(thread *contract.ThreadMetadata) map[string]any {
	if thread == nil || len(thread.ConfigOverride) == 0 {
		return nil
	}
	var stored storedThreadRuntime
	if err := json.Unmarshal(thread.ConfigOverride, &stored); err != nil {
		return nil
	}
	return stored.Runtime
}

func firstRuntimeString(runtime map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := runtime[key]
		if !ok {
			continue
		}
		text, _ := value.(string)
		if text = strings.TrimSpace(text); text != "" {
			return text
		}
	}
	return ""
}

func runtimeFlagEnabled(runtime map[string]any, keys ...string) bool {
	flags, _ := runtime["sessionFlags"].(map[string]any)
	if len(flags) == 0 {
		flags, _ = runtime["session_flags"].(map[string]any)
	}
	for _, key := range keys {
		if runtimeBool(runtime[key]) {
			return true
		}
		if runtimeBool(flags[key]) {
			return true
		}
	}
	return false
}

// runtimeBoolMap 处理运行时boolmap。
func runtimeBoolMap(runtime map[string]any, keys ...string) map[string]bool {
	for _, key := range keys {
		raw, _ := runtime[key].(map[string]any)
		if len(raw) == 0 {
			continue
		}
		out := make(map[string]bool, len(raw))
		for name, value := range raw {
			if parsed, ok := runtimeBoolValue(value); ok {
				if name = strings.TrimSpace(name); name != "" {
					out[name] = parsed
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func runtimeBool(value any) bool {
	parsed, _ := runtimeBoolValue(value)
	return parsed
}

func runtimeBoolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true, true
		case "0", "false", "no", "off":
			return false, true
		}
	}
	return false, false
}

func (h *MemoryLifecycleHooks) resolvedMemoryGate(ctx context.Context, threadID string) MemoryGateSnapshot {
	if h == nil {
		return MemoryGateSnapshot{}
	}
	meta := h.resolveThreadRuntimeMetadata(ctx, strings.TrimSpace(threadID))
	return ResolveMemoryGate(meta.buildCtx(), h.cfg)
}

func (h *MemoryLifecycleHooks) writeOptions(ctx context.Context, threadID string) WriteOptions {
	return WriteOptions{SkipIndex: h.resolvedMemoryGate(ctx, threadID).SkipIndex}
}
