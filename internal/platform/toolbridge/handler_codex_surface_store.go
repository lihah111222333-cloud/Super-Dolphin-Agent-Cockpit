package toolbridge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// storeCodexToolSurface 将新的 Codex tool surface 写入索引。
// 如果同一 key 已绑定旧 surface，旧 surface 会在替换后关闭，确保 stdio client 不泄漏。
func (h *Handler) storeCodexToolSurface(surface *codexToolSurface) error {
	replaced := h.replaceCodexToolSurface(surface)
	for _, old := range replaced {
		if err := old.Close(); err != nil {
			return fmt.Errorf("toolbridge: close replaced codex tool surface: %w", err)
		}
	}
	return nil
}

// replaceCodexToolSurface 在锁内替换所有 surface key，并返回需要关闭的旧 surface。
func (h *Handler) replaceCodexToolSurface(surface *codexToolSurface) []*codexToolSurface {
	h.surfaceMu.Lock()
	defer h.surfaceMu.Unlock()
	if h.surfaces == nil {
		h.surfaces = make(map[string]*codexToolSurface)
	}
	replaced := make([]*codexToolSurface, 0)
	seen := make(map[*codexToolSurface]struct{})
	for _, key := range surface.keys {
		old := h.surfaces[key]
		if old == nil || old == surface {
			continue
		}
		if _, ok := seen[old]; ok {
			continue
		}
		seen[old] = struct{}{}
		replaced = append(replaced, old)
	}
	for _, old := range replaced {
		for _, key := range old.keys {
			if h.surfaces[key] == old {
				delete(h.surfaces, key)
			}
		}
	}
	for _, key := range surface.keys {
		h.surfaces[key] = surface
	}
	return replaced
}

// removeCodexToolSurface 从索引中移除指定 surface 的所有 key。
// 只有当前 key 仍指向同一个 surface 时才删除，避免误删并发绑定的新 surface。
func (h *Handler) removeCodexToolSurface(surface *codexToolSurface) {
	h.surfaceMu.Lock()
	defer h.surfaceMu.Unlock()
	for _, key := range surface.keys {
		if h.surfaces[key] == surface {
			delete(h.surfaces, key)
		}
	}
}

// BindCodexToolSurface 将已准备好的 Codex tool surface 绑定到更多 agent/thread key。
// sourceKey 必须已经存在，目标 key 不能被其他 surface 占用。
func (h *Handler) BindCodexToolSurface(scope contract.CodexToolSurfaceScope) error {
	sourceKey := firstNonEmptySurfaceKey(surfaceIDKey(scope.SurfaceID), scope.AgentID)
	if sourceKey == "" {
		return fmt.Errorf("toolbridge: codex tool surface bind source is required")
	}
	keys := codexSurfaceKeys(scope)
	if len(keys) < 2 {
		return fmt.Errorf("toolbridge: codex tool surface bind target key is required")
	}
	return h.bindCodexToolSurface(sourceKey, keys)
}

// bindCodexToolSurface 在锁内完成 key 绑定，并同步 surface.keys 供释放时反向清理。
func (h *Handler) bindCodexToolSurface(sourceKey string, keys []string) error {
	h.surfaceMu.Lock()
	defer h.surfaceMu.Unlock()
	surface := h.surfaces[sourceKey]
	if surface == nil {
		return fmt.Errorf("toolbridge: codex tool surface is not prepared for agent %q", sourceKey)
	}
	for _, key := range keys {
		if existing := h.surfaces[key]; existing != nil && existing != surface {
			return fmt.Errorf("toolbridge: codex tool surface key %q is already bound", key)
		}
	}
	for _, key := range keys {
		h.surfaces[key] = surface
	}
	merged := append(append([]string(nil), surface.keys...), keys...)
	surface.keys = nonEmptyUnique(merged...)
	return nil
}

// firstNonEmptySurfaceKey 返回第一个非空 surface key，用于兼容 surfaceID 与 agentID 两种索引。
func firstNonEmptySurfaceKey(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

// ReleaseCodexToolSurface 释放 scope 能命中的所有 Codex tool surface。
// 多个 key 指向同一 surface 时只关闭一次，关闭错误会合并返回。
func (h *Handler) ReleaseCodexToolSurface(scope contract.CodexToolSurfaceScope) error {
	keys := codexSurfaceKeys(scope)
	if len(keys) == 0 {
		return fmt.Errorf("toolbridge: codex tool surface release scope is empty")
	}
	surfaces := h.takeCodexToolSurfaces(keys)
	var err error
	for _, surface := range surfaces {
		err = errors.Join(err, surface.Close())
	}
	return err
}

// takeCodexToolSurfaces 在锁内摘除 keys 对应的唯一 surface 集合。
func (h *Handler) takeCodexToolSurfaces(keys []string) []*codexToolSurface {
	h.surfaceMu.Lock()
	defer h.surfaceMu.Unlock()
	seen := make(map[*codexToolSurface]struct{})
	out := make([]*codexToolSurface, 0, len(keys))
	for _, key := range keys {
		surface := h.surfaces[key]
		if surface == nil {
			continue
		}
		if _, ok := seen[surface]; ok {
			continue
		}
		seen[surface] = struct{}{}
		out = append(out, surface)
	}
	for _, surface := range out {
		for _, key := range surface.keys {
			if h.surfaces[key] == surface {
				delete(h.surfaces, key)
			}
		}
	}
	return out
}
