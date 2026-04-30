package uistate

import (
	"context"
	"errors"
	"sort"
	"strings"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

// builtinToolsDisabledKey is the preference key that stores the user's chosen
// disabled set for the upstream Claude CLI built-in tools. Persisted as a JSON
// string array of canonical tool IDs.
const builtinToolsDisabledKey = "config/builtinTools.disabled"

// Provider identifiers for built-in tool grouping. Only "claude" is currently
// disable-able from this project — codex's app-server JSON-RPC protocol does
// not expose a per-turn tool filter, so codex tools are surfaced in the UI
// only as an explanatory note (no entries in the registry).
const (
	BuiltinToolProviderClaude = "claude"
	BuiltinToolProviderCodex  = "codex"
)

// BuiltinToolDescriptor describes an upstream CLI built-in tool so the
// settings UI can render a friendly Chinese label and so the transport layer
// can translate user preferences into the CLI's --disallowedTools flag.
type BuiltinToolDescriptor struct {
	ID              string
	Label           string
	Description     string
	DefaultDisabled bool
	// Provider is the upstream that hosts this tool. The settings UI groups
	// the registry by Provider; the transport layer only consults entries
	// whose Provider matches the running upstream.
	Provider string
}

// builtinToolRegistry is the canonical list of upstream built-in tools the
// user can toggle. Order controls the UI rendering order. Labels are Chinese
// so the surfaced UI reads as "读文件" / "执行命令" rather than raw tool IDs.
var builtinToolRegistry = []BuiltinToolDescriptor{
	{ID: "Read", Label: "读文件", Description: "上游 Agent 直接读取工作区文件", DefaultDisabled: true, Provider: BuiltinToolProviderClaude},
	{ID: "Write", Label: "写文件", Description: "上游 Agent 直接写入新文件", DefaultDisabled: true, Provider: BuiltinToolProviderClaude},
	{ID: "Edit", Label: "编辑文件", Description: "上游 Agent 直接修改现有文件", DefaultDisabled: true, Provider: BuiltinToolProviderClaude},
	{ID: "MultiEdit", Label: "批量编辑", Description: "一次调用内批量修改多个位置", DefaultDisabled: true, Provider: BuiltinToolProviderClaude},
	{ID: "Bash", Label: "执行命令", Description: "在本地 shell 中执行任意命令", DefaultDisabled: true, Provider: BuiltinToolProviderClaude},
	{ID: "Grep", Label: "代码搜索", Description: "使用上游内置 grep 在工作区查找", DefaultDisabled: true, Provider: BuiltinToolProviderClaude},
	{ID: "Glob", Label: "文件匹配", Description: "按 glob 模式列出匹配文件", DefaultDisabled: true, Provider: BuiltinToolProviderClaude},
	{ID: "LS", Label: "列目录", Description: "列出目录内容", DefaultDisabled: true, Provider: BuiltinToolProviderClaude},
	{ID: "WebFetch", Label: "抓取网页", Description: "按 URL 拉取网页内容", DefaultDisabled: false, Provider: BuiltinToolProviderClaude},
	{ID: "WebSearch", Label: "网页搜索", Description: "调用内置网页搜索", DefaultDisabled: false, Provider: BuiltinToolProviderClaude},
	{ID: "TodoWrite", Label: "待办记录", Description: "写入上游自带的任务清单", DefaultDisabled: false, Provider: BuiltinToolProviderClaude},
	{ID: "NotebookEdit", Label: "Notebook 编辑", Description: "编辑 Jupyter Notebook", DefaultDisabled: false, Provider: BuiltinToolProviderClaude},
	{ID: "Task", Label: "派生子 Agent", Description: "派生子 Agent 执行任务", DefaultDisabled: false, Provider: BuiltinToolProviderClaude},
	{ID: "ExitPlanMode", Label: "退出计划模式", Description: "离开 Plan Mode 审批界面", DefaultDisabled: false, Provider: BuiltinToolProviderClaude},
}

// builtinToolIndex maps each canonical tool ID to its descriptor for O(1)
// validation of user input.
var builtinToolIndex = func() map[string]BuiltinToolDescriptor {
	out := make(map[string]BuiltinToolDescriptor, len(builtinToolRegistry))
	for _, item := range builtinToolRegistry {
		out[item.ID] = item
	}
	return out
}()

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
}

// BuiltinToolProviderNote is a static informational card surfaced under a
// provider header in the settings UI for upstreams whose built-in tools are
// not disable-able through this project (i.e. codex). The frontend renders
// it instead of a tool list so users still see the tools exist and why they
// can't be flipped off here.
type BuiltinToolProviderNote struct {
	Provider string `json:"provider"`
	Label    string `json:"label"`
	Note     string `json:"note"`
}

// builtinToolsReadResult is the payload returned from config/builtinTools/read.
type builtinToolsReadResult struct {
	Tools         []BuiltinToolView         `json:"tools"`
	ProviderNotes []BuiltinToolProviderNote `json:"providerNotes,omitempty"`
}

// builtinToolProviderNotes is the static set of provider info cards the read
// RPC surfaces to the UI. Currently a single codex card explaining the
// protocol-level limitation.
var builtinToolProviderNotes = []BuiltinToolProviderNote{
	{
		Provider: BuiltinToolProviderCodex,
		Label:    "Codex 内置工具",
		Note:     "Codex app-server 的 JSON-RPC 协议没有暴露 per-turn 工具开关，read_file / shell / apply_patch 等内置工具目前无法在项目侧禁用。要禁用需修改 ~/.codex/config.toml 或等待上游协议支持。",
	},
}

type builtinToolsWriteParams struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Cwd     string `json:"cwd,omitempty"`
}

func readBuiltinTools(ctx context.Context, prefs uipreference.Store, cwd string) (*builtinToolsReadResult, error) {
	disabled, err := effectiveDisabledBuiltinToolSet(ctx, prefs, cwd)
	if err != nil {
		return nil, err
	}
	tools := make([]BuiltinToolView, 0, len(builtinToolRegistry))
	for _, item := range builtinToolRegistry {
		_, isDisabled := disabled[item.ID]
		tools = append(tools, BuiltinToolView{
			ID:          item.ID,
			Label:       item.Label,
			Description: item.Description,
			Enabled:     !isDisabled,
			Provider:    item.Provider,
		})
	}
	notes := append([]BuiltinToolProviderNote(nil), builtinToolProviderNotes...)
	return &builtinToolsReadResult{Tools: tools, ProviderNotes: notes}, nil
}

func writeBuiltinTool(ctx context.Context, prefs uipreference.Store, p builtinToolsWriteParams) (*builtinToolsReadResult, error) {
	if prefs == nil {
		return nil, errConfigPreferenceStoreRequired
	}
	id := strings.TrimSpace(p.ID)
	if _, ok := builtinToolIndex[id]; !ok {
		return nil, errUnknownBuiltinTool
	}
	current, err := effectiveDisabledBuiltinToolSet(ctx, prefs, p.Cwd)
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
	return readBuiltinTools(ctx, prefs, p.Cwd)
}

// ResolveDisabledBuiltinTools returns the sorted list of tool IDs that must be
// sent to the Claude CLI via --disallowedTools for this scope. Defaults apply
// when the caller has never persisted a preference; explicit overrides from
// the preference store replace the defaults entirely.
func ResolveDisabledBuiltinTools(ctx context.Context, prefs uipreference.Store, cwd string) []string {
	disabled, err := effectiveDisabledBuiltinToolSet(ctx, prefs, cwd)
	if err != nil {
		disabled = defaultDisabledBuiltinToolSet()
	}
	out := make([]string, 0, len(disabled))
	for id := range disabled {
		if _, ok := builtinToolIndex[id]; ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// effectiveDisabledBuiltinToolSet returns the set of disabled tool IDs that
// should be treated as the current state: the persisted override when present
// (including an explicit empty array meaning "all enabled"), else the built-in
// defaults.
func effectiveDisabledBuiltinToolSet(ctx context.Context, prefs uipreference.Store, cwd string) (map[string]struct{}, error) {
	stored, present, err := loadStoredDisabledBuiltinToolSet(ctx, prefs, cwd)
	if err != nil {
		return nil, err
	}
	if !present {
		return defaultDisabledBuiltinToolSet(), nil
	}
	return stored, nil
}

// defaultDisabledBuiltinToolSet returns a fresh copy of the descriptor
// defaults so callers can safely mutate it.
func defaultDisabledBuiltinToolSet() map[string]struct{} {
	out := make(map[string]struct{})
	for _, item := range builtinToolRegistry {
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
func loadStoredDisabledBuiltinToolSet(ctx context.Context, prefs uipreference.Store, cwd string) (map[string]struct{}, bool, error) {
	if prefs == nil {
		return nil, false, nil
	}
	raw, err := prefs.GetValue(ctx, strings.TrimSpace(cwd), builtinToolsDisabledKey)
	switch {
	case err == nil:
	case platformdb.IsNotFound(err):
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
		if _, known := builtinToolIndex[id]; !known {
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
