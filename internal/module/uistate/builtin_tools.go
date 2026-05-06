package uistate

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

// builtinToolsDisabledKey is the preference key that stores the user's chosen
// disabled set for the upstream Claude CLI built-in tools. Persisted as a JSON
// string array of canonical tool IDs.
const builtinToolsDisabledKey = "config/builtinTools.disabled"

// errUnknownBuiltinTool is returned by the RPC write handler when the caller
// supplies an ID that is not part of the registry.
var errUnknownBuiltinTool = errors.New("uistate: unknown builtin tool id")

// BuiltinToolView is the per-tool row returned by config/builtinTools/read.
type BuiltinToolView struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider,omitempty"`
	ReplacedBy  string `json:"replacedBy,omitempty"`
	FilterMode  string `json:"filterMode,omitempty"`
}

// builtinToolsReadResult is the payload returned from config/builtinTools/read.
type builtinToolsReadResult struct {
	Tools []BuiltinToolView `json:"tools"`
}

func buildNativeToolIndex(tools []contract.NativeToolDescriptor) map[string]contract.NativeToolDescriptor {
	out := make(map[string]contract.NativeToolDescriptor, len(tools))
	for _, item := range tools {
		out[item.ID] = item
	}
	return out
}

type builtinToolsWriteParams struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Cwd     string `json:"cwd,omitempty"`
}

func readBuiltinTools(ctx context.Context, prefs uipreference.Store, store *skilllibrary.Store, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor, cwd string) (*builtinToolsReadResult, error) {
	var replaced map[string]string
	if store != nil {
		entries, err := store.List()
		if err == nil {
			replaced = aggregateReplacementSources(entries)
		}
	}
	disabled, err := effectiveDisabledBuiltinToolSet(ctx, prefs, cwd, registry, index)
	if err != nil {
		return nil, err
	}
	tools := make([]BuiltinToolView, 0, len(registry))
	for _, item := range registry {
		skillName := replaced[item.ID]
		_, isDisabled := disabled[item.ID]
		tools = append(tools, BuiltinToolView{
			ID:          item.ID,
			Label:       item.Label,
			Description: item.Description,
			Enabled:     !isDisabled && skillName == "",
			Provider:    item.Provider,
			ReplacedBy:  skillName,
			FilterMode:  string(item.FilterMode),
		})
	}
	return &builtinToolsReadResult{Tools: tools}, nil
}

// aggregateReplacementSources 返回 map[toolName]skillName，记录每个被替代工具
// 是被哪个 skill 声明替代的（取第一个声明者）。
func aggregateReplacementSources(entries []skilllibrary.SkillEntry) map[string]string {
	out := make(map[string]string)
	for _, e := range entries {
		if e.Meta == nil || e.Meta.Disabled {
			continue
		}
		for _, tools := range e.Meta.ReplacesNative {
			for _, name := range tools {
				if name == "" {
					continue
				}
				if _, exists := out[name]; !exists {
					out[name] = e.Meta.Name
				}
			}
		}
	}
	return out
}

func writeBuiltinTool(ctx context.Context, prefs uipreference.Store, store *skilllibrary.Store, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor, p builtinToolsWriteParams) (*builtinToolsReadResult, error) {
	if prefs == nil {
		return nil, errConfigPreferenceStoreRequired
	}
	id := strings.TrimSpace(p.ID)
	if _, ok := index[id]; !ok {
		return nil, errUnknownBuiltinTool
	}
	current, err := effectiveDisabledBuiltinToolSet(ctx, prefs, p.Cwd, registry, index)
	if err != nil {
		return nil, err
	}
	if p.Enabled {
		delete(current, id)
	} else {
		current[id] = struct{}{}
	}
	if err := storeDisabledBuiltinToolSet(ctx, prefs, p.Cwd, current); err != nil {
		return nil, err
	}
	return readBuiltinTools(ctx, prefs, store, registry, index, p.Cwd)
}

// ResolveFilteredBuiltinTools returns the sorted list of tool IDs filtered for
// this scope. Defaults apply when the caller has never persisted a preference;
// explicit overrides from the preference store replace the defaults entirely.
func ResolveFilteredBuiltinTools(ctx context.Context, prefs uipreference.Store, cwd string, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor) []string {
	disabled, err := effectiveDisabledBuiltinToolSet(ctx, prefs, cwd, registry, index)
	if err != nil {
		disabled = defaultDisabledBuiltinToolSet(registry)
	}
	out := make([]string, 0, len(disabled))
	for id := range disabled {
		if _, ok := index[id]; ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func ResolveDisabledBuiltinTools(ctx context.Context, prefs uipreference.Store, cwd string, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor) []string {
	return ResolveFilteredBuiltinTools(ctx, prefs, cwd, registry, index)
}

func ResolveSoftFilteredBuiltinTools(ctx context.Context, prefs uipreference.Store, cwd string, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor) []string {
	return filterBuiltinToolsByMode(ResolveFilteredBuiltinTools(ctx, prefs, cwd, registry, index), index, contract.NativeToolFilterModeSoft)
}

func ResolveHardEnabledBuiltinTools(ctx context.Context, prefs uipreference.Store, cwd string, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor, provider string) []string {
	disabled := make(map[string]struct{})
	for _, id := range ResolveFilteredBuiltinTools(ctx, prefs, cwd, registry, index) {
		disabled[id] = struct{}{}
	}
	provider = strings.TrimSpace(provider)
	out := make([]string, 0, len(registry))
	for _, item := range registry {
		if item.FilterMode != contract.NativeToolFilterModeHard {
			continue
		}
		if provider != "" && item.Provider != provider {
			continue
		}
		if _, filtered := disabled[item.ID]; filtered {
			continue
		}
		out = append(out, item.ID)
	}
	sort.Strings(out)
	return out
}

func filterBuiltinToolsByMode(ids []string, index map[string]contract.NativeToolDescriptor, mode contract.NativeToolFilterMode) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		descriptor, ok := index[id]
		if !ok || descriptor.FilterMode != mode {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// effectiveDisabledBuiltinToolSet returns the set of disabled tool IDs that
// should be treated as the current state: the persisted override when present
// (including an explicit empty array meaning "all enabled"), else the built-in
// defaults.
func effectiveDisabledBuiltinToolSet(ctx context.Context, prefs uipreference.Store, cwd string, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor) (map[string]struct{}, error) {
	stored, present, err := loadStoredDisabledBuiltinToolSet(ctx, prefs, cwd, index)
	if err != nil {
		return nil, err
	}
	if !present {
		return defaultDisabledBuiltinToolSet(registry), nil
	}
	return stored, nil
}

// defaultDisabledBuiltinToolSet returns a fresh copy of the descriptor
// defaults so callers can safely mutate it.
func defaultDisabledBuiltinToolSet(registry []contract.NativeToolDescriptor) map[string]struct{} {
	out := make(map[string]struct{})
	for _, item := range registry {
		if item.DefaultDisabled {
			out[item.ID] = struct{}{}
		}
	}
	return out
}

// loadStoredDisabledBuiltinToolSet reads the persisted override. The present
// flag distinguishes "user has customized the list" (even to an empty array)
// from "no override stored yet" so callers can decide whether to apply the
// registry defaults.
func loadStoredDisabledBuiltinToolSet(ctx context.Context, prefs uipreference.Store, cwd string, index map[string]contract.NativeToolDescriptor) (map[string]struct{}, bool, error) {
	if prefs == nil {
		return nil, false, nil
	}
	raw, err := prefs.GetValue(ctx, strings.TrimSpace(cwd), builtinToolsDisabledKey)
	switch {
	case err == nil:
	case contract.IsNotFound(err):
		return nil, false, nil
	default:
		return nil, false, err
	}
	value := decodePreferenceValue(raw)
	ids, ok := value.([]any)
	if !ok {
		return nil, false, nil
	}
	out := make(map[string]struct{}, len(ids))
	for _, entry := range ids {
		id, _ := entry.(string)
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, known := index[id]; !known {
			continue
		}
		out[id] = struct{}{}
	}
	return out, true, nil
}

func storeDisabledBuiltinToolSet(ctx context.Context, prefs uipreference.Store, cwd string, set map[string]struct{}) error {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return storePreference(ctx, prefs, strings.TrimSpace(cwd), builtinToolsDisabledKey, ids)
}
