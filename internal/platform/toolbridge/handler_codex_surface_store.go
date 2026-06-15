package toolbridge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func (h *Handler) storeCodexToolSurface(surface *codexToolSurface) error {
	replaced := h.replaceCodexToolSurface(surface)
	for _, old := range replaced {
		if err := old.Close(); err != nil {
			return fmt.Errorf("toolbridge: close replaced codex tool surface: %w", err)
		}
	}
	return nil
}

// replaceCodexToolSurface 替换codex工具surface。
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

func (h *Handler) removeCodexToolSurface(surface *codexToolSurface) {
	h.surfaceMu.Lock()
	defer h.surfaceMu.Unlock()
	for _, key := range surface.keys {
		if h.surfaces[key] == surface {
			delete(h.surfaces, key)
		}
	}
}

// BindCodexToolSurface 绑定codex工具surface。
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

// bindCodexToolSurface 绑定codex工具surface。
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

func firstNonEmptySurfaceKey(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

// ReleaseCodexToolSurface 处理releasecodex工具surface。
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

// takeCodexToolSurfaces 处理takecodex工具surfaces。
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
