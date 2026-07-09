// Package prompt 实现 prompt section 注入和模板自动路由的条件判断。
// enable_when 和 match_when 都对坏 JSON fail-closed，避免损坏规则误注入提示内容。
package prompt

import (
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// EvaluateEnableWhen 判断 prompt_template_section 是否应注入当前 prompt。
// 表达式使用 JSONB 键值 AND 匹配；空值视为无条件注入，坏 JSON fail-closed 跳过该 section。
//
// 支持的表达式形态：
//
//	null / empty / {}            → 无条件注入
//	invalid JSON                 → 不注入
//	{"language":"zh"}           → buildCtx.Language == "zh" 时注入
//	{"isWorktree":true}         → buildCtx.IsWorktree 为 true 时注入
//	{"provider":"claude-cli",
//	 "model":"sonnet-4"}        → provider 和 model 必须同时匹配
//	{"sessionFlags.debug":true} → buildCtx.SessionFlags["debug"] == true
//	{"tags_has":"refactor"}     → userPrompt 中存在该子串（大小写不敏感）
//	{"tags_has":["rename","trace","impact"]}
//	                            → 数组按 OR 匹配
//	{"enabled_tools_has":"grep"}
//	                            → BuildCtx.EnabledTools 包含该短工具名
//	{"enabled_tools_has":["grep","xref"]}
//	                            → 数组按 OR 匹配
//	{"enabled_tools_all":["task_create_dag","task_start_dag"]}
//	                            → 数组按 AND 匹配。
//
// 除 tags_has 和 enabled_tools_* 外不支持 $not/$in/regex；未知字段一律不匹配，避免误注入。
func EvaluateEnableWhen(raw []byte, buildCtx contract.BuildCtx, userPrompt string) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return true
	}
	var expr map[string]any
	if err := json.Unmarshal([]byte(trimmed), &expr); err != nil {
		return false
	}
	if len(expr) == 0 {
		return true
	}
	for key, want := range expr {
		if !sectionEnableKeyMatches(key, want, buildCtx, userPrompt) {
			return false
		}
	}
	return true
}

// sectionEnableKeyMatches 分派单个 enable_when 字段。
// tags_has 和 enabled_tools_* 有专用匹配逻辑，其余字段复用 BuildCtx 等值表。
func sectionEnableKeyMatches(key string, want any, buildCtx contract.BuildCtx, userPrompt string) bool {
	switch key {
	case "tags_has":
		return matchSectionTagsHas(want, userPrompt)
	case "enabled_tools_has":
		return matchEnabledToolsHas(want, buildCtx.EnabledTools)
	case "enabled_tools_all":
		return matchEnabledToolsAll(want, buildCtx.EnabledTools)
	}
	got, ok := resolveEnableWhenField(key, buildCtx)
	if !ok {
		return false
	}
	return enableWhenValueEquals(got, want)
}

// matchEnabledToolsHas 实现 enabled_tools_has：字符串匹配单个工具，数组按 OR 匹配。
// 比较前会把兼容别名规范化为当前短工具名，保证旧配置和新工具名能共存。
func matchEnabledToolsHas(want any, enabled []string) bool {
	if len(enabled) == 0 {
		return false
	}
	switch v := want.(type) {
	case string:
		return containsExact(enabled, v)
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if containsExact(enabled, s) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// matchEnabledToolsAll 实现 enabled_tools_all：数组中的每个工具都必须启用。
func matchEnabledToolsAll(want any, enabled []string) bool {
	if len(enabled) == 0 {
		return false
	}
	items, ok := want.([]any)
	if !ok || len(items) == 0 {
		return false
	}
	for _, item := range items {
		s, ok := item.(string)
		if !ok || !containsExact(enabled, s) {
			return false
		}
	}
	return true
}

// containsExact 对工具名做规范化后精确匹配，避免别名和大小写清理逻辑散落在调用方。
func containsExact(values []string, want string) bool {
	if want == "" {
		return false
	}
	want = canonicalPromptToolName(want)
	for _, v := range values {
		if canonicalPromptToolName(v) == want {
			return true
		}
	}
	return false
}

// canonicalPromptToolName 规范化当前工具名和 orchestration 别名。
func canonicalPromptToolName(name string) string {
	switch strings.TrimSpace(name) {
	case "patch_edit":
		return "patch_edit"
	default:
		if canonical, ok := canonicalPromptOrchestrationToolName(name); ok {
			return canonical
		}
		return strings.TrimSpace(name)
	}
}

func canonicalPromptOrchestrationToolName(name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	for _, canonical := range contract.OrchestrationToolCanonicalNames() {
		if trimmed == canonical {
			return canonical, true
		}
	}
	return "", false
}

// canonicalPromptLSPTools 从启用工具列表中提取规范化的 LSP 工具名。
func canonicalPromptLSPTools(values []string) []string {
	tools := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range sortedPromptValues(values) {
		tool := canonicalPromptToolName(value)
		if !isPromptLSPToolName(tool) {
			continue
		}
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		tools = append(tools, tool)
	}
	return tools
}

// isPromptLSPToolName 判断工具名是否属于 prompt 可展示的 LSP 工具集合。
func isPromptLSPToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case "file", "grep", "inspect", "xref", "structure", "patch_edit", "completion":
		return true
	default:
		return false
	}
}

// matchSectionTagsHas 实现 section 级 tags_has：字符串为单个子串探测，数组按 OR 匹配。
func matchSectionTagsHas(want any, userPrompt string) bool {
	if strings.TrimSpace(userPrompt) == "" {
		return false
	}
	switch v := want.(type) {
	case string:
		return matchTagsHas(v, userPrompt)
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if matchTagsHas(s, userPrompt) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// resolveEnableWhenField 返回 BuildCtx 中的运行时字段；未知 key 返回 false 让上层 fail-closed。
func resolveEnableWhenField(key string, c contract.BuildCtx) (any, bool) {
	if name, ok := strings.CutPrefix(key, "sessionFlags."); ok {
		if name == "" {
			return nil, false
		}
		return c.SessionFlags[name], true
	}
	switch key {
	case "cwd":
		return c.CWD, true
	case "gitRoot":
		return c.GitRoot, true
	case "isWorktree":
		return c.IsWorktree, true
	case "language":
		return c.Language, true
	case "provider":
		return c.Provider, true
	case "model":
		return c.Model, true
	default:
		return nil, false
	}
}

// enableWhenValueEquals 比较 BuildCtx 字段和 JSON 期望值。
// 缺失的 bool session flag 视为 false，使 {"sessionFlags.debug":false} 可匹配未设置状态。
func enableWhenValueEquals(got, want any) bool {
	if got == nil {
		// 缺失 session flag 等价 bool 零值，只在调用方明确要求 false 时匹配。
		if w, ok := want.(bool); ok {
			return !w
		}
		return false
	}
	switch g := got.(type) {
	case bool:
		w, ok := want.(bool)
		return ok && g == w
	case string:
		w, ok := want.(string)
		return ok && g == w
	default:
		return false
	}
}

// EvaluateMatchWhen 判断 prompt_template 是否参与自动路由。
// 它和 EvaluateEnableWhen 的失败策略不同：路由规则损坏时跳过模板，避免错误规则抢占用户请求。
//
//   - nil / empty        → 不匹配，表示未启用自动路由
//   - malformed JSON     → 不匹配，fail-closed
//   - "{}"                → 无条件匹配，后续仍按 priority 排序
//   - JSON kv AND match  → 所有字段都必须匹配当前 BuildCtx
//
// 支持的字段：
//
//	cwd_glob        用 filepath.Match 匹配 buildCtx.CWD
//	                  e.g. "*/projects/data-*"
//	cwd_prefix      用 strings.HasPrefix 匹配 buildCtx.CWD
//	                  e.g. "/Users/mac/work"
//	language        buildCtx.Language == value
//	provider        buildCtx.Provider == value
//	model           buildCtx.Model == value
//	isWorktree      buildCtx.IsWorktree == value
//	sessionFlags.X  buildCtx.SessionFlags[X] == value
//	tags_has        模板路由不再支持，始终 fail-closed
//
// 模板 match_when 和 section enable_when 都读 BuildCtx，但分别发生在路由与组装阶段。
func EvaluateMatchWhen(raw []byte, buildCtx contract.BuildCtx, userPrompt string) bool {
	_ = userPrompt
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false
	}
	var expr map[string]any
	if err := json.Unmarshal([]byte(trimmed), &expr); err != nil {
		return false
	}
	if len(expr) == 0 {
		return true
	}
	for key, want := range expr {
		if !matchWhenKeyMatches(key, want, buildCtx, userPrompt) {
			return false
		}
	}
	return true
}

// matchWhenKeyMatches 处理单个 match_when 字段。
// cwd_glob/cwd_prefix 走路径匹配，其余字段复用 BuildCtx 等值表；未知字段 fail-closed。
func matchWhenKeyMatches(key string, want any, buildCtx contract.BuildCtx, userPrompt string) bool {
	_ = userPrompt
	switch key {
	case "cwd_glob":
		return matchCWDGlob(matchWhenStringValue(want), buildCtx.CWD)
	case "cwd_prefix":
		return matchCWDPrefix(matchWhenStringValue(want), buildCtx.CWD)
	case "tags_has":
		// Template-level keyword routing is retired；关键词只允许用于 section 级 enable_when。
		return false
	default:
		// 共享 BuildCtx 等值表覆盖 language/provider/model/cwd/gitRoot/isWorktree/sessionFlags.*。
		got, ok := resolveEnableWhenField(key, buildCtx)
		if !ok {
			return false
		}
		return enableWhenValueEquals(got, want)
	}
}
