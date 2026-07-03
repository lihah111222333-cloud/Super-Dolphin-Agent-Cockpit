package uistate

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// builtin tools 偏好键保存用户禁用项和上次见过的 registry 快照。
// 值以 JSON 字符串数组持久化，读写路径必须只接受当前 registry 中的规范工具 ID。
const builtinToolsDisabledKey = "config/builtinTools.disabled"
const builtinToolsKnownIDsKey = "config/builtinTools.knownIDs"

// errUnknownBuiltinTool 表示写入请求携带了 registry 之外的工具 ID，RPC 层应直接拒绝。
var errUnknownBuiltinTool = errors.New("uistate: unknown builtin tool id")

// BuiltinToolView 是 config/builtinTools/read 返回给 UI 的单个工具行。
// 字段保持 wire 兼容，Enabled 已合并用户禁用和 skill 替代两类过滤来源。
type BuiltinToolView struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider,omitempty"`
	ReplacedBy  string `json:"replacedBy,omitempty"`
	FilterMode  string `json:"filterMode,omitempty"`
	Enforcement string `json:"enforcement,omitempty"`
}

// builtinToolsReadResult 是 config/builtinTools/read 的 wire payload。
type builtinToolsReadResult struct {
	Tools []BuiltinToolView `json:"tools"`
}

// buildNativeToolIndex 按工具 ID 构建 registry 索引，供读写路径校验未知 ID。
func buildNativeToolIndex(tools []contract.NativeToolDescriptor) map[string]contract.NativeToolDescriptor {
	out := make(map[string]contract.NativeToolDescriptor, len(tools))
	for _, item := range tools {
		out[item.ID] = item
	}
	return out
}

// builtinToolsWriteParams 是 config/builtinTools/write 的入参。
type builtinToolsWriteParams struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Cwd     string `json:"cwd,omitempty"`
}

// readBuiltinTools 汇总 registry、用户禁用偏好和 skill 替代关系，生成 UI 可展示的工具状态。
func readBuiltinTools(ctx context.Context, prefs preferenceStore, store contract.SkillLister, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor, cwd string) (*builtinToolsReadResult, error) {
	var replaced map[string]string
	if store != nil {
		entries, err := store.ListSkills(contract.WithSkillCWD(ctx, cwd))
		if err == nil {
			replaced = aggregateReplacementSources(entries)
		}
	}
	disabled, err := effectiveDisabledBuiltinToolSet(ctx, prefs, cwd, registry, index)
	if err != nil {
		return nil, err
	}
	filtered := filteredBuiltinToolSet(disabled, replaced)
	codexPolicy := contract.NewCodexNativeToolPolicy(setKeys(filtered))
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
			Enforcement: builtinToolEnforcement(item, filtered, codexPolicy),
		})
	}
	return &builtinToolsReadResult{Tools: tools}, nil
}

// filteredBuiltinToolSet 合并用户禁用和 skill 替代两类过滤来源。
func filteredBuiltinToolSet(disabled map[string]struct{}, replaced map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(disabled)+len(replaced))
	for id := range disabled {
		out[id] = struct{}{}
	}
	for id, skill := range replaced {
		if strings.TrimSpace(skill) != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

// builtinToolEnforcement 根据 provider 和 filter mode 返回实际执行层级。
func builtinToolEnforcement(item contract.NativeToolDescriptor, filtered map[string]struct{}, codexPolicy contract.CodexNativeToolPolicy) string {
	if _, ok := filtered[item.ID]; !ok {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(item.Provider), "codex") {
		return string(codexPolicy.Tier(item.ID))
	}
	switch item.FilterMode {
	case contract.NativeToolFilterModeHard:
		return string(contract.NativeToolEnforcementNativeHard)
	case contract.NativeToolFilterModeSoft:
		return string(contract.NativeToolEnforcementSoftAudit)
	default:
		return ""
	}
}

// setKeys 返回稳定排序的集合 key，供 Codex policy 计算使用。
func setKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// aggregateReplacementSources 返回 map[toolName]skillName，记录每个被替代工具
// 是被哪个 skill 声明替代的（取第一个声明者）。
func aggregateReplacementSources(entries []contract.SkillInfo) map[string]string {
	out := make(map[string]string)
	for _, e := range entries {
		for _, tools := range e.ReplacesNative {
			for _, name := range tools {
				if name == "" {
					continue
				}
				if _, exists := out[name]; !exists {
					out[name] = e.Name
				}
			}
		}
	}
	return out
}

// writeBuiltinTool 更新单个 native tool 的启用状态，并持久化当前 registry 快照。
func writeBuiltinTool(ctx context.Context, prefs preferenceStore, store contract.SkillLister, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor, p builtinToolsWriteParams) (*builtinToolsReadResult, error) {
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
	if err := storeKnownBuiltinToolSet(ctx, prefs, p.Cwd, registry); err != nil {
		return nil, err
	}
	return readBuiltinTools(ctx, prefs, store, registry, index, p.Cwd)
}

// ResolveFilteredBuiltinTools 返回当前作用域应过滤的工具 ID。
// 偏好读取失败会直接返回错误，避免把读失败误当成“没有配置”而启用/禁用错误工具。
func ResolveFilteredBuiltinTools(ctx context.Context, prefs preferenceValueReader, cwd string, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor) ([]string, error) {
	disabled, err := effectiveDisabledBuiltinToolSet(ctx, prefs, cwd, registry, index)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(disabled))
	for id := range disabled {
		if _, ok := index[id]; ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ResolveDisabledBuiltinTools 保留旧 API 名称，语义等同 ResolveFilteredBuiltinTools。
func ResolveDisabledBuiltinTools(ctx context.Context, prefs preferenceValueReader, cwd string, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor) []string {
	filtered, err := ResolveFilteredBuiltinTools(ctx, prefs, cwd, registry, index)
	if err != nil {
		return nil
	}
	return filtered
}

// ResolveSoftFilteredBuiltinTools 返回指定 provider 下 soft filter 的禁用工具。
func ResolveSoftFilteredBuiltinTools(ctx context.Context, prefs preferenceValueReader, cwd string, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor, provider string) ([]string, error) {
	filtered, err := ResolveFilteredBuiltinTools(ctx, prefs, cwd, registry, index)
	if err != nil {
		return nil, err
	}
	return filterBuiltinToolsByModeAndProvider(filtered, index, contract.NativeToolFilterModeSoft, provider), nil
}

// ResolveExplicitSoftFilteredBuiltinTools 只返回用户显式保存的 soft filter 工具。
// 默认禁用项只影响配置页初始状态，不应在无用户偏好时覆盖 provider sandbox 权限。
func ResolveExplicitSoftFilteredBuiltinTools(
	ctx context.Context,
	prefs preferenceValueReader,
	cwd string,
	registry []contract.NativeToolDescriptor,
	index map[string]contract.NativeToolDescriptor,
	provider string,
) ([]string, error) {
	stored, present, err := loadStoredDisabledBuiltinToolSet(ctx, prefs, cwd, index)
	if err != nil {
		return nil, err
	}
	if !present || len(stored) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(stored))
	for id := range stored {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return filterBuiltinToolsByModeAndProvider(ids, index, contract.NativeToolFilterModeSoft, provider), nil
}

// ResolveHardEnabledBuiltinTools 返回指定 provider 下 hard filter 但当前仍启用的工具。
func ResolveHardEnabledBuiltinTools(ctx context.Context, prefs preferenceValueReader, cwd string, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor, provider string) ([]string, error) {
	disabled := make(map[string]struct{})
	filtered, err := ResolveFilteredBuiltinTools(ctx, prefs, cwd, registry, index)
	if err != nil {
		return nil, err
	}
	for _, id := range filtered {
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
	return out, nil
}

// filterBuiltinToolsByModeAndProvider 按 filter mode 和可选 provider 过滤工具 ID。
func filterBuiltinToolsByModeAndProvider(ids []string, index map[string]contract.NativeToolDescriptor, mode contract.NativeToolFilterMode, provider string) []string {
	out := make([]string, 0, len(ids))
	provider = strings.TrimSpace(provider)
	for _, id := range ids {
		descriptor, ok := index[id]
		if !ok || descriptor.FilterMode != mode {
			continue
		}
		if provider != "" && descriptor.Provider != provider {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// effectiveDisabledBuiltinToolSet 计算当前应禁用的工具集合。
// 用户显式保存的空数组表示全部启用；未保存过偏好时使用 registry 默认禁用项。
func effectiveDisabledBuiltinToolSet(ctx context.Context, prefs preferenceValueReader, cwd string, registry []contract.NativeToolDescriptor, index map[string]contract.NativeToolDescriptor) (map[string]struct{}, error) {
	stored, present, err := loadStoredDisabledBuiltinToolSet(ctx, prefs, cwd, index)
	if err != nil {
		return nil, err
	}
	if !present {
		return defaultDisabledBuiltinToolSet(registry), nil
	}
	if len(stored) == 0 {
		return stored, nil
	}
	known, knownPresent, err := loadStoredBuiltinToolKnownSet(ctx, prefs, cwd, index)
	if err != nil {
		return nil, err
	}
	return mergeNewDefaultDisabledTools(stored, known, knownPresent, registry), nil
}

// defaultDisabledBuiltinToolSet 返回 registry 默认禁用项的新 map，调用方可安全增删。
func defaultDisabledBuiltinToolSet(registry []contract.NativeToolDescriptor) map[string]struct{} {
	out := make(map[string]struct{})
	for _, item := range registry {
		if item.DefaultDisabled {
			out[item.ID] = struct{}{}
		}
	}
	return out
}

// loadStoredDisabledBuiltinToolSet 读取用户禁用列表；present 区分“显式空列表”和“从未配置”。
func loadStoredDisabledBuiltinToolSet(ctx context.Context, prefs preferenceValueReader, cwd string, index map[string]contract.NativeToolDescriptor) (map[string]struct{}, bool, error) {
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

// storeDisabledBuiltinToolSet 以稳定排序写回用户禁用列表。
func storeDisabledBuiltinToolSet(ctx context.Context, prefs preferenceStore, cwd string, set map[string]struct{}) error {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return storePreference(ctx, prefs, strings.TrimSpace(cwd), builtinToolsDisabledKey, ids)
}

// loadStoredBuiltinToolKnownSet 读取上次写入时已知的工具 ID，用于识别新增默认禁用项。
func loadStoredBuiltinToolKnownSet(ctx context.Context, prefs preferenceValueReader, cwd string, index map[string]contract.NativeToolDescriptor) (map[string]struct{}, bool, error) {
	if prefs == nil {
		return nil, false, nil
	}
	raw, err := prefs.GetValue(ctx, strings.TrimSpace(cwd), builtinToolsKnownIDsKey)
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

// mergeNewDefaultDisabledTools 把 registry 中新增的默认禁用工具合并进用户已有偏好。
func mergeNewDefaultDisabledTools(stored map[string]struct{}, known map[string]struct{}, knownPresent bool, registry []contract.NativeToolDescriptor) map[string]struct{} {
	out := make(map[string]struct{}, len(stored)+len(registry))
	for id := range stored {
		out[id] = struct{}{}
	}
	for _, item := range registry {
		if !item.DefaultDisabled {
			continue
		}
		if knownPresent {
			if _, ok := known[item.ID]; ok {
				continue
			}
		}
		out[item.ID] = struct{}{}
	}
	return out
}

// storeKnownBuiltinToolSet 持久化当前 registry 工具 ID，供下一次合并新增默认禁用项。
func storeKnownBuiltinToolSet(ctx context.Context, prefs preferenceStore, cwd string, registry []contract.NativeToolDescriptor) error {
	ids := make([]string, 0, len(registry))
	for _, item := range registry {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	sort.Strings(ids)
	return storePreference(ctx, prefs, strings.TrimSpace(cwd), builtinToolsKnownIDsKey, ids)
}
