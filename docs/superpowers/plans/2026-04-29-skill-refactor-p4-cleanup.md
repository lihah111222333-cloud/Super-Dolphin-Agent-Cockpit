# Skill Refactor — Phase 4: Shared Old Code Cleanup Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 to implement this plan task-by-task.

**Goal:** P2 + P3 之后清理已无消费方的旧代码：删除 `skills/expandBody` + `skills/readResource` RPC handlers、`ExpandBody` + `ReadResource` 方法、3 个 prompt.Config 死字段、`DynamicSectionSkillCatalog` 死再导出。同时修复几条 P1/P2/P3 review 留下的 OBSERVATION 项（`extractDescriptionFromSkillMD` 改用真 frontmatter parser；`removeOrphans` 错误吞咽；`Store.Get` 错误前缀；`ReadSection` anchor 契约文档）。

**Architecture:** 删除 P3 后无消费方的 RPC 链路 + 配置死字段。Mode/ArtifactKind/ApproveCandidate 保留（仍有活跃消费方）。

**Tech Stack:** Go 1.22+，已建立的 skillforge / skilllibrary / toolbridge 包。

**前置阅读：**
- spec §11 删除清单（其中 SkillRef.Mode / ArtifactKind 标"删除"实际**不能删**——P3 后 explore 确认它们仍有活跃消费方）
- P3 final review observations
- P1 / P2 final review observations

---

## File Structure

**修改**：
```
internal/module/prompt/config.go                 (Task 1: 删 3 个字段)
internal/module/prompt/config_test.go            (Task 1: 删测试)
internal/module/prompt/dynamic.go                (Task 2: 删 DynamicSectionSkillCatalog 再导出)
internal/module/skill/rpc.go                     (Task 3: 删 expand/read RPC handlers + 注册)
internal/module/skill/skills_expand.go           (Task 4: 删 ExpandBody / ReadResource 方法)
internal/module/skill/skills_expand_test.go      (Task 4: 删对应测试)
internal/provider/codexapp/skill_manifest.go     (Task 5: 用 skillforge.Parse 替换 extractDescriptionFromSkillMD)
internal/module/skilllibrary/reconcile.go        (Task 6: removeOrphans 上报错误)
internal/module/skilllibrary/store.go            (Task 6: Get 错误前缀)
internal/module/skilllibrary/section.go          (Task 6: anchor 契约 godoc)
```

无新增文件、无删除文件。

---

## Task 1: 删除 3 个 prompt.Config 死字段

**Files:**
- Modify: `internal/module/prompt/config.go` (删字段 + env 解析)
- Modify: `internal/module/prompt/config_test.go`（删默认值断言）

死字段：`EnableSkillProgressiveDisclosure`、`SkillCatalogTokenBudget`、`EmitSkillCatalogMetaInstructions`。P2 删 SkillCatalogProvider 后，这 3 个字段无 reader（仅测试断言默认值），可整删。

### Step 1: Failing test

不需要新增 failing test —— 既然字段被删，已有测试如果还引用它会自动失败。先跑：

```
cd /private/tmp/super-dolphin-skill-refactor-p4
go test ./internal/module/prompt/... -count=1 2>&1 | grep -E "FAIL|PASS:" | head -10
```

记录基线（应该全 PASS）。

### Step 2: 删字段 + env 解析

修改 `internal/module/prompt/config.go`：
- 删除 `EnableSkillProgressiveDisclosure bool` 字段
- 删除 `SkillCatalogTokenBudget int` 字段
- 删除 `EmitSkillCatalogMetaInstructions bool` 字段
- 删除对应 env 常量（如 `envSkillProgressiveDisclosure` 等）
- 删除 `NewConfig` 中对它们的赋值
- 删除任何 helper（如 `parseSkillProgressiveDisclosure` 等）

修改 `internal/module/prompt/config_test.go`：
- 删除断言这些字段默认值的测试

### Step 3: Verify

```
go build ./...
go test ./internal/module/prompt/... -count=1
```

### Step 4: Commit

```
git add internal/module/prompt/config.go internal/module/prompt/config_test.go
git commit -m "refactor(prompt): delete 3 dead skill catalog config fields

P2 removed SkillCatalogProvider; EnableSkillProgressiveDisclosure +
SkillCatalogTokenBudget + EmitSkillCatalogMetaInstructions had no
remaining readers (only default-value test assertions)."
```

---

## Task 2: 删 DynamicSectionSkillCatalog 死再导出

**Files:**
- Modify: `internal/module/prompt/dynamic.go`

`contract.prompt.DynamicSectionSkillCatalog` 在 `dynamic.go` 里再导出但已无消费方。`contract` 端常量本身先留（contract 包的清理是更大动作，留待真正必要时再做）。

### Step 1: Find references

```
grep -rn "DynamicSectionSkillCatalog" --include="*.go" .
```

确认除 `contract/prompt.go`（定义）+ `module/prompt/dynamic.go`（再导出）以外无其他引用。

### Step 2: 删 dynamic.go 的再导出

如果是 `var DynamicSectionSkillCatalog = contract.DynamicSectionSkillCatalog` 这种简单 alias，直接删；如果嵌在更大的常量列表里，仅去除该一项。

### Step 3-4: build + commit

```
git commit -m "refactor(prompt): remove DynamicSectionSkillCatalog dead re-export"
```

---

## Task 3: 删 RPC expand/read handlers + 注册

**Files:**
- Modify: `internal/module/skill/rpc.go`

删除：
- `skillExpandBodyHandler` 函数（line ~288-296）
- `skillReadResourceHandler` 函数（line ~298-306）
- 它们在 register-handlers 列表中的注册（line ~181-182）
- `rpc_skill_types.go` 中相关 request/response 类型（如 `expandBodyRequest` / `readResourceRequest`，仅当无外部消费方）

P3 已经把 toolbridge 切到 `skill_read_section`，这两个 RPC 不再被任何 dispatcher 调用。

### Step 1: Find references

```
grep -rn "skillExpandBodyHandler\|skillReadResourceHandler\|skills/expandBody\|skills/readResource" --include="*.go" .
```

应该只剩 rpc.go 本身 + 其测试。

### Step 2-4: 删 + build + test + commit

```
git commit -m "refactor(skill): delete skills/expandBody and skills/readResource RPC handlers

P3 swapped toolbridge to skill_read_section; these RPC handlers had no
remaining callers."
```

---

## Task 4: 删 ExpandBody / ReadResource 方法 + 测试

**Files:**
- Modify: `internal/module/skill/skills_expand.go`（删 `ExpandBody` + `ReadResource` 方法 + 不再被使用的私有 helper）
- Modify: `internal/module/skill/skills_expand_test.go`（删对应测试，可能整文件删）

Task 3 删完 RPC handlers 后，`ExpandBody` / `ReadResource` 方法的唯一调用方就消失了。

注意：保留 `requireArtifactApproval` 等 helper（如果它们仍被 service 别处用）。

### Step 1: Find references

```
grep -rn "\.ExpandBody\|\.ReadResource" --include="*.go" internal/module/skill/
grep -rn "service.ExpandBody\|service.ReadResource" --include="*.go" .
```

### Step 2: 删方法 + 私有 helper（仅当确实无其他调用）

### Step 3: 跑测试

```
go test ./internal/module/skill/... -count=1
```

可能需要删除大量 `skills_expand_test.go` 中的测试（spec §8 列出 18 个 ExpandBody + 12 个 ReadResource 测试）。整文件删除如果剩余测试都是这两个方法的。

### Step 4: Commit

```
git commit -m "refactor(skill): delete ExpandBody / ReadResource methods (~30 obsolete tests)

Task 3 removed the only callers (RPC handlers); these methods + their
test file are now dead."
```

---

## Task 5: extractDescriptionFromSkillMD 用 skillforge.Parse 替换

**File:**
- Modify: `internal/provider/codexapp/skill_manifest.go`

P3 OBSERVATION：当前 `extractDescriptionFromSkillMD` 用裸 `HasPrefix("description:")` 在前 10 行查找，对带 YAML fence 或前导空行的 frontmatter 静默返回空。改用 `skillforge.Parse` 真正解析。

### Step 1: Failing test

加测试覆盖现失败 case：

```go
func TestBuildSkillManifest_HandlesQuotedDescription(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{
			Meta: &skilllibrary.SkillMeta{Name: "x"},
			SkillMD: "---\nname: x\ndescription: \"含逗号的描述, 测试解析\"\n---\n",
		},
	}
	out := buildSkillManifest(entries, 8192)
	if !strings.Contains(out, "含逗号的描述, 测试解析") {
		t.Errorf("manifest 未正确解析带引号 description: %s", out)
	}
}
```

### Step 2: 替换实现

```go
import "github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"

func extractDescriptionFromSkillMD(src string) string {
	ps, err := skillforge.Parse(src)
	if err != nil {
		return ""
	}
	return ps.Description
}
```

或直接 inline：

```go
func renderL1CBlock(e skilllibrary.SkillEntry) string {
	desc := ""
	if ps, err := skillforge.Parse(e.SkillMD); err == nil {
		desc = ps.Description
	}
	// ...
}
```

后者更彻底，把 `extractDescriptionFromSkillMD` 整函数删除。

### Step 3-4: test + commit

```
git commit -m "fix(codexapp): use skillforge.Parse for SKILL.md description extraction"
```

---

## Task 6: P1/P2 OBSERVATION 修复合并 commit

**Files:**
- Modify: `internal/module/skilllibrary/reconcile.go` (`removeOrphans` 错误上报)
- Modify: `internal/module/skilllibrary/store.go` (`Get` 错误前缀)
- Modify: `internal/module/skilllibrary/section.go` (anchor 契约 godoc)

3 个独立修，合并成一个清理 commit。

### 6a: removeOrphans 错误上报

P2 final review OBSERVATION：`os.ReadDir(cacheDir)` 错误（如权限问题）当前被 silently dropped；不上报到 `report.Errors`。修：

```go
func (r *Reconciler) removeOrphans(report *ReconcileReport, libNames map[string]struct{}) {
	cacheEntries, err := os.ReadDir(r.cacheDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		report.Errors = append(report.Errors, fmt.Errorf("read cache dir: %w", err))
		return
	}
	// ... 现有循环
}
```

### 6b: Store.Get 错误前缀

```go
func (s *Store) Get(name string) (*SkillEntry, error) {
	if name == "" {
		return nil, fmt.Errorf("skilllibrary: get empty name")
	}
	dir := filepath.Join(s.root, name)
	skillBytes, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		// fs.ErrNotExist 直通保持 errors.Is 兼容；其他 wrap
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("skilllibrary: get %q SKILL.md: %w", name, err)
	}
	meta, err := ReadMeta(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("skilllibrary: get %q meta: %w", name, err)
	}
	return &SkillEntry{Dir: dir, SkillMD: string(skillBytes), Meta: meta}, nil
}
```

注意：保持 `errors.Is(err, fs.ErrNotExist)` 在 caller 端的兼容（必须直通 fs.ErrNotExist 不 wrap）。

### 6c: ReadSection anchor 契约 godoc

补充：

```go
// ReadSection 读取 cacheDir/<name>/references/<NN-anchor>.md。
//
// **Anchor 契约：** anchor 必须是 H2 标题被 SectionFilename 清洗过后的形式
// （非法字符替换为 `-`，长度截断到 maxSectionTitleRunes runes）。空标题对应
// "untitled"。后缀匹配 "-<anchor>.md"，因此 anchor 必须是文件名中数字前缀
// 之后的完整 slug；不允许只传 anchor 的子串（哪怕语义上看起来匹配）。
//
// 错误规约：...
func ReadSection(...) {...}
```

### Step: build + test + commit

```
git commit -m "fix(skilllibrary): improve error handling + ReadSection anchor godoc

- reconcile.removeOrphans: surface ReadDir errors instead of silently dropping
- store.Get: wrap non-NotExist errors with skilllibrary: prefix and skill name context
- section.ReadSection: document anchor format contract (post-SectionFilename slug)"
```

---

## Task 7: P4 全测试 + 冒烟

仅运行测试，不改代码。

### Step 1: 触动包测试

```
cd /private/tmp/super-dolphin-skill-refactor-p4
go test -count=1 -short \
  ./internal/module/prompt/... \
  ./internal/module/skill/... \
  ./internal/module/skilllibrary/... \
  ./internal/module/skillforge/... \
  ./internal/provider/codexapp/... \
  ./internal/platform/toolbridge/... \
  ./internal/archtest/...
```

### Step 2: 全项目测试

```
go test -short ./...
```

### Step 3: build + vet

```
go build ./...
go vet ./...
```

### Step 4: 无新冒烟脚本（P4 是删代码，行为应不变；P3 的冒烟已覆盖端到端）。

如果某个删除引入回归，应该在 Task 1-6 中 build/test 阶段就被发现。

---

## Phase 4 自审

按 编写计划 §自审：

**1. 规格覆盖：** 对照 spec §11 和各 phase final review observations:
- §11 表里"删除"标记的项，P3 explore 后实际不可删的（SkillRef.Mode / ArtifactKind / ApproveCandidate）已明确保留
- §11 真正可删的都覆盖了：3 个 prompt config 字段（Task 1）、DynamicSectionSkillCatalog 再导出（Task 2）、RPC expand/read handlers（Task 3）、ExpandBody/ReadResource 方法（Task 4）
- P3 OBSERVATIONS：extractDescription 替换（Task 5）、ReadSection 契约（Task 6c）
- P1/P2 OBSERVATIONS：removeOrphans 错误（Task 6a）、Store.Get 错误前缀（Task 6b）

**未覆盖项**（明确不在 P4 范畴）：
- FBSD 频次降级 → P6
- F1 native CLI 过滤 → P5
- `SkillRef.Mode` / `ArtifactKind` / `ApproveCandidate` 删除 → 这些不能删（仍有活跃消费方），spec §11 的标记是错误的，P4 explore 已澄清
- contract.DynamicSectionSkillCatalog 常量本身（仅 P4 不动）→ 跨包清理风险高，留待真正必要时

**2. 占位符扫描：** 全部代码段为完整可应用编辑；测试代码完整。

**3. 类型一致性：**
- skillforge.Parse 跨 Task 5 一致引用
- ReadSection / Get 跨 Task 6 一致

修复内联：暂无问题。

---

## 执行交接

计划已完成并保存到 `docs/superpowers/plans/2026-04-29-skill-refactor-p4-cleanup.md`。两个执行选项：

1. **子代理驱动（推荐）**
2. **当前会话内执行**

