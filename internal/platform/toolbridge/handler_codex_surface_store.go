package toolbridge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

type codexDisabledToolSet map[string]struct{}

func newCodexDisabledToolSet(values []string) codexDisabledToolSet {
	if len(values) == 0 {
		return nil
	}
	out := make(codexDisabledToolSet, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out[value] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s codexDisabledToolSet) match(names ...string) (string, bool) {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := s[name]; ok {
			return name, true
		}
	}
	return "", false
}

func mcpSurfaceToolAliases(family, realName, canonical string) []string {
	aliases := []string{canonical, realName, wrappedMCPToolName(family, realName)}
	aliases = append(aliases, mcpSurfaceDenyAndHiddenAliases(family, canonical)...)
	return aliases
}

// addSingleMCPToolToSurface 处理一个 MCP 工具的可见性、禁用别名和可调用入口。
// 被禁用的工具只登记 deny 别名，不会写入 surface.tools 或返回给 Codex 的 schema。
func addSingleMCPToolToSurface(
	surface *codexToolSurface,
	out *[]contract.DynamicToolSchema,
	family string,
	client mcpClient,
	tool mcpdto.MCPTool,
	disabled codexDisabledToolSet,
) error {
	if _, reserved := reservedHostOnlySurfaceToolCanonicalName(family, tool.Name); reserved {
		return nil
	}
	canonical := canonicalCodexToolName(family, tool.Name)
	if shouldNamespaceExternalMCPTool(surface, family, canonical) {
		canonical = wrappedMCPToolName(family, tool.Name)
	}
	aliases := mcpSurfaceToolAliases(family, tool.Name, canonical)
	if disabledName, ok := disabled.match(aliases...); ok {
		return addDisabledSurfaceToolAliases(surface, disabledName, aliases...)
	}
	entry := codexToolEntry{name: canonical, realName: tool.Name, executionKind: "stdio", family: strings.TrimSpace(family), client: client}
	if err := addSurfaceTool(surface, out, tool, entry); err != nil {
		return err
	}
	if err := addCallableMCPToolAliases(surface, family, tool.Name, canonical); err != nil {
		return err
	}
	for _, alias := range callableLegacyCodexToolAliases(family, canonical) {
		if err := addSurfaceAlias(surface, alias, canonical); err != nil {
			return err
		}
	}
	return nil
}

func addCallableMCPToolAliases(surface *codexToolSurface, family, realName, canonical string) error {
	if err := addMCPToolAlias(surface, family, realName, canonical); err != nil {
		return err
	}
	return addSurfaceAlias(surface, wrappedMCPToolName(family, realName), canonical)
}

// addDisabledSurfaceToolAliases 记录被 session config 禁用的工具别名。
// 禁用项不进入可调用表，但 scoped stale-call 会先命中这里并被拒绝。
func addDisabledSurfaceToolAliases(surface *codexToolSurface, disabledName string, aliases ...string) error {
	disabledName = strings.TrimSpace(disabledName)
	for _, alias := range nonEmptyUnique(aliases...) {
		if disabledName == "" {
			disabledName = alias
		}
		if existing, ok := surface.aliases[alias]; ok {
			return fmt.Errorf("toolbridge: disabled codex surface alias %q conflicts with visible tool %q", alias, existing)
		}
		if existing, ok := surface.disabledTools[alias]; ok && existing != disabledName {
			return fmt.Errorf("toolbridge: disabled codex surface alias %q maps to both %q and %q", alias, existing, disabledName)
		}
		surface.disabledTools[alias] = disabledName
	}
	return nil
}

func disabledCodexSurfaceToolCallResult(surface *codexToolSurface, name string) (*ToolCallResult, bool) {
	if surface == nil {
		return nil, false
	}
	disabledName, ok := surface.disabledTools[strings.TrimSpace(name)]
	if !ok {
		return nil, false
	}
	if disabledName = strings.TrimSpace(disabledName); disabledName == "" {
		disabledName = strings.TrimSpace(name)
	}
	return toolCallTextResult(false, fmt.Sprintf("codex surface tool %q is disabled by session config", disabledName)), true
}

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
