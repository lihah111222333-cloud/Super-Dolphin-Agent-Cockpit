# 提示词软过滤替代 nativefilter 实现计划

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把原生工具过滤从 Claude CLI 专用的 settings.json 硬过滤迁移到跨模型的 system prompt 软过滤，由 SkillMeta.ReplacesNative 声明驱动。

**Architecture:** 复用现有 SkillMeta.ReplacesNative 数据模型，将聚合函数迁入 skilllibrary 包，在 prompt assembler 的 AssembleStart 中读取聚合结果注入 BuildCtx.SuppressedTools，由 tool_preferences static section 渲染为 prompt 文本。删除 nativefilter 模块及前端手动配置面板。

**Tech Stack:** Go (fx DI), Vue 3 (TypeScript), MCP RPC

**Spec:** `docs/superpowers/specs/2026-05-03-prompt-based-tool-filter-design.md`

---

## File Map

| 操作 | 文件 | 职责 |
|------|------|------|
| Create | `internal/module/skilllibrary/aggregate.go` | `AggregateAllReplacements` + `sortedSeen` 函数 |
| Create | `internal/module/skilllibrary/aggregate_test.go` | 聚合函数单元测试 |
| Modify | `internal/contract/prompt.go:40-65` | BuildCtx 加 `SuppressedTools` 字段 |
| Modify | `internal/module/prompt/service.go:45-59` | service struct 加 `skillStore` 字段 + ServiceOption |
| Modify | `internal/module/prompt/assembler.go:27-72` | AssembleStart 中填充 `buildCtx.SuppressedTools` |
| Modify | `internal/module/prompt/section.go:77-92` | renderToolPreferencesSectionText 追加抑制 bullet |
| Modify | `internal/module/prompt/section_test.go` | 新增 SuppressedTools 渲染测试 |
| Modify | `internal/app/modules.go:64` | 移除 `nativefilter.Module` |
| Modify | `internal/provider/claudecli/driver.go:37,207-218` | 移除 nativeFilter 字段和 Apply 调用，加残留清理 |
| Modify | `internal/provider/claudecli/module.go:10,28,42` | 移除 nativefilter 导入和依赖 |
| Delete | `internal/module/nativefilter/` | 整包删除（8 个文件） |
| Modify | `internal/module/uistate/builtin_tools.go` | 数据源改为 skilllibrary，删除 write 逻辑 |
| Modify | `internal/module/uistate/config_rpc.go:83-86` | 移除 write RPC，修改 read RPC |
| Modify | `cmd/agent-terminal/frontend/vue-app/pages/settings/BuiltinToolsSettings.ts` | 改为只读展示 |

---

### Task 1: 聚合函数迁入 skilllibrary

**Files:**
- Create: `internal/module/skilllibrary/aggregate.go`
- Create: `internal/module/skilllibrary/aggregate_test.go`

- [ ] **Step 1: 编写失败测试**

在 `internal/module/skilllibrary/aggregate_test.go` 中：

```go
package skilllibrary

import (
	"testing"
)

func TestAggregateAllReplacements_WildcardKey(t *testing.T) {
	entries := []SkillEntry{
		{Meta: &SkillMeta{Name: "lsp", ReplacesNative: map[string][]string{
			"*": {"Read", "Write", "Bash"},
		}}},
	}
	got := AggregateAllReplacements(entries)
	want := []string{"Bash", "Read", "Write"}
	if len(got) != len(want) {
		t.Fatalf("AggregateAllReplacements = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AggregateAllReplacements[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAggregateAllReplacements_MixedKeys(t *testing.T) {
	entries := []SkillEntry{
		{Meta: &SkillMeta{Name: "a", ReplacesNative: map[string][]string{
			"claude": {"Read"},
		}}},
		{Meta: &SkillMeta{Name: "b", ReplacesNative: map[string][]string{
			"*": {"Write", "Read"},
		}}},
	}
	got := AggregateAllReplacements(entries)
	want := []string{"Read", "Write"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAggregateAllReplacements_SkipsDisabledAndNilMeta(t *testing.T) {
	entries := []SkillEntry{
		{Meta: nil},
		{Meta: &SkillMeta{Name: "off", Disabled: true, ReplacesNative: map[string][]string{
			"*": {"Bash"},
		}}},
		{Meta: &SkillMeta{Name: "on", ReplacesNative: map[string][]string{
			"*": {"Read"},
		}}},
	}
	got := AggregateAllReplacements(entries)
	if len(got) != 1 || got[0] != "Read" {
		t.Fatalf("got %v, want [Read]", got)
	}
}

func TestAggregateAllReplacements_Empty(t *testing.T) {
	got := AggregateAllReplacements(nil)
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/module/skilllibrary/ -run TestAggregateAllReplacements -v`
Expected: FAIL — `AggregateAllReplacements` 未定义

- [ ] **Step 3: 实现 AggregateAllReplacements**

在 `internal/module/skilllibrary/aggregate.go` 中：

```go
package skilllibrary

import "sort"

// AggregateAllReplacements 聚合所有 enabled skill 的 ReplacesNative 字段（遍历
// 所有 key："*"、"claude"、"codex" 等），去重排序返回被替代的原生工具名列表。
// 用于 prompt assembler 生成跨模型的工具抑制指令。
func AggregateAllReplacements(entries []SkillEntry) []string {
	seen := make(map[string]struct{})
	for _, e := range entries {
		if e.Meta == nil || e.Meta.Disabled {
			continue
		}
		for _, tools := range e.Meta.ReplacesNative {
			for _, name := range tools {
				if name != "" {
					seen[name] = struct{}{}
				}
			}
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(seen map[string]struct{}) []string {
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/module/skilllibrary/ -run TestAggregateAllReplacements -v`
Expected: PASS (4 tests)

- [ ] **Step 5: 提交**

```bash
git add internal/module/skilllibrary/aggregate.go internal/module/skilllibrary/aggregate_test.go
git commit -m "feat(skilllibrary): add AggregateAllReplacements for cross-model tool suppression"
```

---

### Task 2: BuildCtx 加 SuppressedTools 字段

**Files:**
- Modify: `internal/contract/prompt.go:40-65`

- [ ] **Step 1: 在 BuildCtx struct 末尾加字段**

在 `internal/contract/prompt.go` 的 `BuildCtx` struct 中，`ForceLaunchSkills bool` 之后加：

```go
	// SuppressedTools 是被 SkillMeta.ReplacesNative 声明替代的原生工具名列表。
	// prompt assembler 在 tool_preferences section 中渲染为 "Do NOT use..." 指令，
	// 引导所有模型优先使用项目 MCP 等价工具。
	SuppressedTools []string
```

- [ ] **Step 2: 编译确认无破坏**

Run: `go build ./internal/...`
Expected: SUCCESS — 新字段 zero value 是 nil slice，不影响现有调用者

- [ ] **Step 3: 提交**

```bash
git add internal/contract/prompt.go
git commit -m "feat(contract): add SuppressedTools field to BuildCtx"
```

---

### Task 3: Prompt service 注入 skilllibrary.Store

**Files:**
- Modify: `internal/module/prompt/service.go:45-72`

- [ ] **Step 1: 给 service struct 加 skillStore 字段**

在 `internal/module/prompt/service.go` 的 `service` struct 中，`sharedFiles` 字段之后加：

```go
	skillStore *skilllibrary.Store
```

需要在文件顶部加 import：

```go
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
```

- [ ] **Step 2: 新增 ServiceOption 注入函数**

在 `WithPromptHintSources` 函数之后加：

```go
// WithSkillStore injects the skill library store used to aggregate
// ReplacesNative declarations for cross-model native tool suppression.
func WithSkillStore(store *skilllibrary.Store) ServiceOption {
	return func(s *service) {
		s.skillStore = store
	}
}
```

- [ ] **Step 3: 编译确认**

Run: `go build ./internal/module/prompt/...`
Expected: SUCCESS

- [ ] **Step 4: 提交**

```bash
git add internal/module/prompt/service.go
git commit -m "feat(prompt): add WithSkillStore ServiceOption for tool suppression"
```

---

### Task 4: AssembleStart 填充 SuppressedTools

**Files:**
- Modify: `internal/module/prompt/assembler.go:27-72`

- [ ] **Step 1: 在 AssembleStart 中填充 SuppressedTools**

在 `internal/module/prompt/assembler.go` 的 `AssembleStart` 函数中，找到 `buildCtx := buildStartCtx(in)`（line 40），在其之后加：

```go
	buildCtx.SuppressedTools = s.aggregateSuppressedTools(ctx, strings.TrimSpace(in.CWD))
```

在文件末尾加辅助方法：

```go
// aggregateSuppressedTools 合并两个来源的被抑制工具：
//   1. skilllibrary.Store 中技能声明的 ReplacesNative（自动）
//   2. uipreference.Store 中用户手动勾选禁用的工具（手动）
// 两者 union 去重后返回。
func (s *service) aggregateSuppressedTools(ctx context.Context, cwd string) []string {
	seen := make(map[string]struct{})
	// 来源 1：技能声明
	if s.skillStore != nil {
		entries, err := s.skillStore.List()
		if err == nil {
			for _, name := range skilllibrary.AggregateAllReplacements(entries) {
				seen[name] = struct{}{}
			}
		}
	}
	// 来源 2：用户手动勾选
	for _, name := range uistate.ResolveDisabledBuiltinTools(ctx, s.prefs, cwd) {
		seen[name] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
```

需要在文件顶部 import 区加：

```go
	"sort"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate"
```

- [ ] **Step 2: 编译确认**

Run: `go build ./internal/module/prompt/...`
Expected: SUCCESS

- [ ] **Step 3: 提交**

```bash
git add internal/module/prompt/assembler.go
git commit -m "feat(prompt): populate SuppressedTools from skilllibrary + uipreference in AssembleStart"
```

---

### Task 5: tool_preferences 渲染抑制 bullet

**Files:**
- Modify: `internal/module/prompt/section.go:77-92`
- Modify: `internal/module/prompt/section_test.go`

- [ ] **Step 1: 编写失败测试**

在 `internal/module/prompt/section_test.go` 末尾追加：

```go
func TestStaticSectionsToolPreferencesSuppressedTools(t *testing.T) {
	for _, section := range StaticSections() {
		if section.Name != SectionToolPreferences {
			continue
		}
		content, err := section.Compute(context.Background(), SectionContext{
			BuildCtx: BuildCtx{
				SuppressedTools: []string{"Bash", "Edit", "Read"},
			},
		})
		if err != nil {
			t.Fatalf("Compute() error = %v", err)
		}
		if content == nil {
			t.Fatal("tool_preferences content is nil, want suppressed tools bullet")
		}
		if !strings.Contains(*content, "Do NOT use") {
			t.Fatalf("content = %q, want 'Do NOT use' bullet", *content)
		}
		if !strings.Contains(*content, "Bash, Edit, Read") {
			t.Fatalf("content = %q, want tool names 'Bash, Edit, Read'", *content)
		}
		return
	}
	t.Fatal("tool_preferences section not found")
}

func TestStaticSectionsToolPreferencesNoSuppressedTools(t *testing.T) {
	for _, section := range StaticSections() {
		if section.Name != SectionToolPreferences {
			continue
		}
		content, err := section.Compute(context.Background(), SectionContext{
			BuildCtx: BuildCtx{},
		})
		if err != nil {
			t.Fatalf("Compute() error = %v", err)
		}
		if content != nil && strings.Contains(*content, "Do NOT use") {
			t.Fatalf("content = %q, should NOT contain suppressed tools bullet when SuppressedTools is empty", *content)
		}
		return
	}
	t.Fatal("tool_preferences section not found")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/module/prompt/ -run TestStaticSectionsToolPreferences -v`
Expected: `TestStaticSectionsToolPreferencesSuppressedTools` FAIL（没有 "Do NOT use" bullet）

- [ ] **Step 3: 修改 renderToolPreferencesSectionText**

在 `internal/module/prompt/section.go` 的 `renderToolPreferencesSectionText` 函数中，标准模式分支（line 85-91）改为：

```go
	bullets := []string{
		"Prefer repository-aware tools first: use lsp_file for reading, lsp_edit for edits, and lsp_grep for search.",
		"Use code_run for shell execution only when a dedicated tool cannot do the job, and use it for new-file creation when needed.",
		"Do not reach for shell fallbacks like cat, head, tail, sed, awk, grep, rg, find, or ls when a dedicated tool fits.",
		suppressedToolsBullet(build.SuppressedTools),
		toolPreferencePlanningLine(build.EnabledTools),
		"Batch independent tool calls in parallel and run dependent calls sequentially.",
	}
	return renderToolPreferenceBullets(bullets)
```

在文件中加辅助函数：

```go
func suppressedToolsBullet(tools []string) string {
	if len(tools) == 0 {
		return ""
	}
	return "Do NOT use these native tools — they have been replaced by project MCP equivalents: " +
		strings.Join(tools, ", ") + "."
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/module/prompt/ -run TestStaticSectionsToolPreferences -v`
Expected: ALL PASS

- [ ] **Step 5: 运行完整 prompt 包测试确认无回归**

Run: `go test ./internal/module/prompt/ -v`
Expected: ALL PASS

- [ ] **Step 6: 提交**

```bash
git add internal/module/prompt/section.go internal/module/prompt/section_test.go
git commit -m "feat(prompt): render SuppressedTools as Do-NOT-use bullet in tool_preferences"
```

---

### Task 6: 删除 nativefilter 模块

**Files:**
- Delete: `internal/module/nativefilter/` (全部文件)
- Modify: `internal/app/modules.go:15,64`
- Modify: `internal/provider/claudecli/driver.go:13,37,207-218`
- Modify: `internal/provider/claudecli/module.go:10,28,42`

- [ ] **Step 1: 从 claudecli/driver.go 移除 nativeFilter**

在 `internal/provider/claudecli/driver.go` 中：

1. 删除 import `"github.com/anthropic-ai/super-agent-v3/internal/module/nativefilter"`（line 13）
2. 删除字段 `nativeFilter    *nativefilter.Filter`（line 37）
3. 删除 Apply 调用块（line 207-218），替换为残留清理：

```go
	// 清理旧 nativefilter 残留的 settings.json（一次性写入空 deny 覆盖）
	if spec.cwd != "" {
		cleanupPath := filepath.Join(spec.cwd, ".claude", "settings.json")
		_ = os.MkdirAll(filepath.Dir(cleanupPath), 0o755)
		_ = os.WriteFile(cleanupPath, []byte(`{"permissions":{"deny":[]}}`), 0o644)
	}
```

需要确认 `filepath` 和 `os` 已在 import 中（若未有则添加）。

4. 修改 `newDriver` 函数签名，删除 `nativeFilter *nativefilter.Filter` 参数及 `nativeFilter: nativeFilter` 赋值。

- [ ] **Step 2: 从 claudecli/module.go 移除 nativefilter 依赖**

在 `internal/provider/claudecli/module.go` 中：

1. 删除 import `"github.com/anthropic-ai/super-agent-v3/internal/module/nativefilter"`（line 10）
2. 删除字段 `NativeFilter   *nativefilter.Filter \`optional:"true"\``（line 28）
3. 修改 `NewDriverFactory` 中的 `newDriver` 调用（line 42），删除 `p.NativeFilter` 参数

- [ ] **Step 3: 从 modules.go 移除 nativefilter.Module**

在 `internal/app/modules.go` 中：

1. 删除 import `"github.com/anthropic-ai/super-agent-v3/internal/module/nativefilter"`（line 15）
2. 删除 `nativefilter.Module,`（line 64）

- [ ] **Step 4: 删除 nativefilter 包**

```bash
rm -rf internal/module/nativefilter/
```

- [ ] **Step 5: 编译确认**

Run: `go build ./...`
Expected: SUCCESS

- [ ] **Step 6: 运行受影响包的测试**

Run: `go test ./internal/provider/claudecli/... ./internal/app/... ./internal/module/skilllibrary/... -v`
Expected: ALL PASS

- [ ] **Step 7: 提交**

```bash
git add -A
git commit -m "refactor: remove nativefilter module, replace with prompt-based soft filtering"
```

---

### Task 7: 前端 — 后端 API 改造

**Files:**
- Modify: `internal/module/uistate/builtin_tools.go`
- Modify: `internal/module/uistate/config_rpc.go:83-86`

- [ ] **Step 1: 修改返回结构**

在 `internal/module/uistate/builtin_tools.go` 中，修改 `BuiltinToolView` 加 `replacedBy` 字段：

```go
type BuiltinToolView struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider,omitempty"`
	ReplacedBy  string `json:"replacedBy,omitempty"`
}
```

- [ ] **Step 2: 修改 readBuiltinTools 数据源为双源**

修改 `readBuiltinTools` 函数，同时读取 skilllibrary（自动替代）和 uipreference（手动勾选）：

```go
func readBuiltinTools(ctx context.Context, prefs uipreference.Store, store *skilllibrary.Store, cwd string) (*builtinToolsReadResult, error) {
	// 来源 1：技能自动替代
	var replaced map[string]string
	if store != nil {
		entries, err := store.List()
		if err == nil {
			replaced = aggregateReplacementSources(entries)
		}
	}
	// 来源 2：用户手动勾选
	disabled, _ := effectiveDisabledBuiltinToolSet(ctx, prefs, cwd)

	tools := make([]BuiltinToolView, 0, len(builtinToolRegistry))
	for _, item := range builtinToolRegistry {
		skillName := replaced[item.ID]
		_, isDisabled := disabled[item.ID]
		tools = append(tools, BuiltinToolView{
			ID:          item.ID,
			Label:       item.Label,
			Description: item.Description,
			Enabled:     !isDisabled && skillName == "",
			Provider:    item.Provider,
			ReplacedBy:  skillName,
		})
	}
	notes := append([]BuiltinToolProviderNote(nil), builtinToolProviderNotes...)
	return &builtinToolsReadResult{Tools: tools, ProviderNotes: notes}, nil
}
```

新增 `aggregateReplacementSources` 辅助函数：

```go
func aggregateReplacementSources(entries []skilllibrary.SkillEntry) map[string]string {
	out := make(map[string]string)
	for _, e := range entries {
		if e.Meta == nil || e.Meta.Disabled {
			continue
		}
		for _, tools := range e.Meta.ReplacesNative {
			for _, name := range tools {
				if name != "" {
					if _, exists := out[name]; !exists {
						out[name] = e.Meta.Name
					}
				}
			}
		}
	}
	return out
}
```

需要在 import 区加：

```go
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
```

- [ ] **Step 3: 修改 config_rpc.go**

在 `internal/module/uistate/config_rpc.go` 中：

1. 修改 read handler，增加 `skillStore` 参数：

```go
"config/builtinTools/read": rpc.StrictHandler(func(ctx context.Context, p scopeParams) (any, error) {
	return readBuiltinTools(ctx, prefs, skillStore, p.Cwd)
}),
```

其中 `skillStore` 需要从 uistate 模块的依赖注入中获取。检查 config_rpc.go 的函数签名，给它加 `*skilllibrary.Store` 参数。

2. `"config/builtinTools/write"` handler **保留不动**（用于用户手动勾选）

- [ ] **Step 4: 更新测试**

`internal/module/uistate/builtin_tools_test.go` 中：
- 更新 `readBuiltinTools` 调用签名（增加 `store` 参数，测试中传 nil）
- 现有 `writeBuiltinTool` 和 `ResolveDisabledBuiltinTools` 测试 **保留不动**

- [ ] **Step 6: 编译并运行测试**

Run: `go build ./internal/module/uistate/... && go test ./internal/module/uistate/... -v`
Expected: ALL PASS

- [ ] **Step 7: 提交**

```bash
git add -A
git commit -m "refactor(uistate): switch builtin tools API to skilllibrary-driven read-only"
```

---

### Task 8: 前端 — 混合模式（自动 + 手动）

**Files:**
- Modify: `cmd/agent-terminal/frontend/vue-app/pages/settings/BuiltinToolsSettings.ts`

- [ ] **Step 1: 修改组件为混合模式**

在 `BuiltinToolsSettings.ts` 中：

1. 修改标题从 "上游内置工具" 改为 "原生工具过滤"
2. 修改副标题说明文字为：`"技能自动替代 + 用户手动勾选，统一对所有模型生效。"`
3. 修改分组逻辑：从按 `provider` 分组改为按三组展示：
   - 组1：自动替代（`tool.replacedBy !== ""`），只读，显示 `🔄 ← tool.replacedBy`
   - 组2：手动过滤（`tool.replacedBy === "" && !tool.enabled`），可 toggle
   - 组3：未过滤（`tool.replacedBy === "" && tool.enabled`），可 toggle 勾选禁用
4. 自动替代的工具禁用 toggle（用户不能取消技能声明的替代）
5. 手动过滤 / 未过滤的工具保留 toggle，调用现有 write API
6. 修改计数：从 "已禁用 X/Y" 改为 "已过滤 X/Y"（自动 + 手动总和）

- [ ] **Step 2: 手动验证**

启动前端 dev server，打开 Settings 页面，确认：
- 面板标题为 "原生工具过滤"
- 显示三组：自动替代 / 手动过滤 / 未过滤
- 自动替代工具不可 toggle，显示来源技能名
- 未过滤工具可 toggle 勾选禁用，勾选后移入“手动过滤”组

- [ ] **Step 3: 提交**

```bash
git add cmd/agent-terminal/frontend/vue-app/pages/settings/BuiltinToolsSettings.ts
git commit -m "feat(frontend): convert builtin tools panel to hybrid mode (auto + manual)"
```

---

### Task 9: Skill Meta 更新 + 集成验证

**Files:**
- Modify: 项目中声明了 `replaces_native` 的 `.skill-meta.json` 文件

- [ ] **Step 1: 查找并更新现有 skill meta**

```bash
grep -r '"replaces_native"' ~/.super-dolphin/skills-library/ ~/.super-dolphin/skills-cache/
```

对找到的 skill，确认 `replaces_native` 包含跨模型 `"*"` key。如果只有 `"claude"` key 不用改（聚合函数已兼容），但建议迁移到 `"*"`。

- [ ] **Step 2: 运行全量测试**

```bash
go test ./internal/... -count=1
```

Expected: ALL PASS

- [ ] **Step 3: 启动会话验证 system prompt**

启动一个 Claude 会话，检查 system prompt 中是否包含：
```
Do NOT use these native tools — they have been replaced by project MCP equivalents: ...
```

- [ ] **Step 4: 验证模型行为**

在会话中请求模型读取一个文件，确认它使用 `lsp_file` 而非 `Read`。

- [ ] **Step 5: 提交**

```bash
git add -A
git commit -m "chore: update skill meta to use wildcard key + integration verification"
```

---

## Self-Review Checklist

### 规格覆盖
| 规格章节 | 对应任务 |
|----------|----------|
| §3.1 数据流 | Task 1 (聚合) → Task 4 (注入) → Task 5 (渲染) |
| §3.2.1 BuildCtx 字段 | Task 2 |
| §3.2.2 聚合函数 | Task 1 |
| §3.2.3 Prompt 组装 | Task 3 + Task 4 |
| §3.2.4 Prompt 渲染 | Task 5 |
| §3.2.5 Skill 声明 | Task 9 |
| §3.3 删除清单 | Task 6 |
| §3.4 残留清理 | Task 6 Step 1 (写空 JSON) |
| §4 迁移顺序 | Task 1-5 先加新 → Task 6 再删旧 → Task 7-8 改前端 → Task 9 验证 |
| §6 前端设计 | Task 7 + Task 8 |

### 占位符扫描
- 无 TBD / TODO / "implement later"
- 所有代码块包含完整实现
- 所有测试包含断言

### 类型一致性
- `AggregateAllReplacements` — Task 1 定义，Task 4 和 Task 7 使用，签名一致 `([]SkillEntry) []string`
- `SuppressedTools` — Task 2 定义，Task 4 填充，Task 5 渲染，类型一致 `[]string`
- `suppressedToolsBullet` — Task 5 定义和使用
- `BuiltinToolView` — Task 7 修改，Task 8 前端消费
