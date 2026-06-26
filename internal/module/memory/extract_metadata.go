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

// shouldExtractThread 判断 turn 完成后是否应对该 thread 做自动抽取。
// 只有启用 extractOnStop、turn 成功且属于 AutoMem 根 thread 时才允许抽取。
func (h *MemoryLifecycleHooks) shouldExtractThread(ctx context.Context, evt turndto.TurnCompleted) bool {
	if h == nil || !h.extractOnStop || !evt.Success {
		return false
	}
	return h.resolveThreadRuntimeMetadata(ctx, strings.TrimSpace(evt.ThreadID)).isAutoMemoryRootThread()
}

// resolveThreadRuntimeMetadata 从线程存储读取 runtime 元数据。
// 读取失败或 thread 缺失返回零值，调用方会按非根 thread 处理，不触发抽取。
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

// resolveThreadRuntimeMetadataFromThread 从契约层 ThreadMetadata 提取记忆运行时字段。
// 它兼容结构化字段和 ConfigOverride.runtime 的旧键名，避免历史 thread 丢失父子关系。
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

// decodeStoredThreadRuntime 解码线程 ConfigOverride 中的 runtime 节点。
// JSON 损坏时返回 nil，让调用方退回显式字段而不是中断 turn 结束处理。
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

// firstRuntimeString 按候选键顺序读取 runtime 字符串。
// 兼容 snake_case 和 camelCase 字段，空值会继续查找下一个键。
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

// runtimeFlagEnabled 判断 runtime 或 session_flags 中是否启用任一 flag。
// 该函数用于识别 bare/root thread 等会影响抽取边界的会话状态。
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

// runtimeBoolMap 从 runtime 中读取布尔 flag map。
// 只保留能解析为布尔值且名称非空的项，避免脏 ConfigOverride 影响 gate 判断。
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

// runtimeBool 将 runtime 任意值按布尔语义解析。
// 解析失败返回 false，调用方可把缺失和非法值都视为未启用。
func runtimeBool(value any) bool {
	parsed, _ := runtimeBoolValue(value)
	return parsed
}

// runtimeBoolValue 解析 runtime 中可能出现的布尔表示。
// 支持 bool 和常见字符串布尔值，返回第二个值表示是否成功识别。
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
