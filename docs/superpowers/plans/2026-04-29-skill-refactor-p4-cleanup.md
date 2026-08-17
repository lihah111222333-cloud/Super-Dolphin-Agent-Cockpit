# Skill Refactor — Phase 4: Shared Old Code Cleanup Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 to implement this plan task-by-task.

> **本计划已对齐 2026-04-29 修订版 spec**（重点：§10 重写 trust 策略、§11 标 ArtifactKind/RPC/旧工具为延后删除、§13 #10 新增旧/新工具并存窗口约束）。

## Goal

P2 + P3 之后清理已无消费方的旧代码 + 修补 P1/P3 实现与新 spec 之间的真实偏差。具体范围：

**已完成**（按 commit 顺序）：
- `1072f9f` — 删 3 个 prompt.Config 死字段（Task 1）
- `39c830b` — 删 `DynamicSectionSkillCatalog` 死再导出（Task 2）
- `fced958` — 删 `skills/expandBody` + `skills/readResource` RPC handler 注册（Task 3）
- `182d032` — Gap #1 P1 补丁：`atomic.go` 改成 spec §4.4 要求的 non-lossy `tmp+backup` 双 rename，避免 publish 失败丢旧缓存
- `9843bfd` — Gap #2 P1 补丁：`parse.go` 加 fenced code block 跳过 + closing `## Title ##` 处理，避免代码示例被误切
- `516410d` — Gap #3 P3 补丁：`ReadSection` anchor not-found 返回 `*UnknownAnchorError` 含 `Available[]` 列表（spec §7.2 硬要求）；同时把 anchor 契约 godoc 落在 section.go 上，替代原 Task 6c

**剩余**：
- Task 4：删 `ExpandBody` + `ReadResource` 服务方法 + 旧测试 + `pkg/skilltool` 旧 schema + toolbridge `SkillHostTools` 包装器（按新 spec §11 三处一起清，因为它们已在 P3 后无 fx wire / RPC 注册路径）
- Task 5：`extractDescriptionFromSkillMD` 改用真 frontmatter parser（`skillforge.Parse`）
- Task 6：`removeOrphans` 错误上报（6a）+ `Store.Get` 错误前缀（6b）。原 6c godoc 已并入 Gap #3，本 Task 不再覆盖
- Task 7：全测试 + 冒烟

## Architecture

P4 是删除 P2/P3 后无活跃消费方的旧链路。删除清单严格对齐**新 spec §11**：

| 项 | spec §11 标记 | P4 行动 |
|---|---|---|
| 3 个 prompt.Config 死字段 | 隐含死字段 | ✅ 已删（`1072f9f`） |
| RPC `skills/expandBody` / `skills/readResource` handlers | "先兼容保留，后删；前置：旧 UI / canary / toolbridge 不再调用" | ✅ 已满足前置（fx 已不 wire `SkillHostTools`，仓内无 .ts/.tsx 调用），已删（`fced958`） |
| `service.ExpandBody` / `ReadResource` 方法 + tests | 同上链 | Task 4 |
| `pkg/skilltool` 旧 schema (`ExpandBodyInputSchema` / `ReadResourceInputSchema` / `ToolName*` / `Description*`) | "迁移为 skill_read_section 等新工具；新旧并存灰度窗口" | Task 4（fx 灰度窗口已过：`provideHostToolRegistry` 只 wire `SkillReadSectionRegistry`） |
| `toolbridge.SkillHostTools` 包装器 + `host_tools_test.go` | 同上 | Task 4 |
| `module/skill/approval.go` ArtifactKind | "**延后删除**；§10 安全替代完成才能删，不得在 P4 之前抢删" | ❌ 不动；列入未覆盖项 |
| `dto.SkillRef.Mode` 三态枚举 | "P2/P3/P4 之后；turn factory、archtest、Claude/Codex tests 全部迁移" | ❌ 不动；P5+（仍有活跃 consumer：`turn/skills.go`、`cron/turn_adapter.go`、`pkg/skillmetrics`、provider/turn.go） |
| env `SKILL_WRITER_FORMAT` | "SkillRef.Mode 删除后同步清理" | ✅ 已在 P3 `e4303ae` 删 |
| `contract.DynamicSectionSkillCatalog` 常量本身 | （未明列；仅 P4 不动） | ❌ 不动；跨包清理另开计划 |

## Tech Stack

Go 1.22+；已建立的 `skillforge` / `skilllibrary` / `toolbridge` 包；无新增运行时依赖。

## 前置阅读

- spec §10（trust 重写、`allowed_tools` enforcement 硬要求、`skills-trust.json` 迁移、marketplace 不自动 trusted）
- spec §11（删除清单 + 各项前置条件）
- spec §13 #10（旧/新工具并存窗口 system prompt 推荐一致性）
- 已合并的 P1/P2/P3 final review observations

---

## File Structure

**P4 仅修改、不新增/删除文件**。Task 4 范围扩展后涉及的文件：

```
internal/module/prompt/config.go                  (Task 1: ✅ 已删)
internal/module/prompt/config_test.go             (Task 1: ✅ 已删)
internal/module/prompt/dynamic.go                 (Task 2: ✅ 已删 re-export)
internal/module/skill/rpc.go                      (Task 3: ✅ 已删 handler 注册)

internal/module/skill/skills_expand.go            (Task 4: 删 ExpandBody/ReadResource 方法)
internal/module/skill/skills_expand_test.go       (Task 4: 删对应测试)
internal/module/skill/rpc_skill_types.go          (Task 4: 删 ExpandBodyParams/Result + ReadResourceParams/Result，仅当无别处引用)
internal/module/skill/cwd_scope_test.go           (Task 4: 删依赖这两方法的 cwd-scope 测试)
pkg/skilltool/schema.go                           (Task 4: 删全文件，旧工具 schema 不再被 toolbridge 消费)
pkg/skilltool/schema_test.go                      (Task 4: 删全文件)
internal/platform/toolbridge/host_tools.go        (Task 4: 删 SkillHostTools + NewSkillHostTools，保留 HostToolRegistry 接口)
internal/platform/toolbridge/host_tools_test.go   (Task 4: 删全文件)
internal/platform/toolbridge/handler_host_tools.go(Task 4: 删 ExpandBodyResult/ReadResourceResult 分支)
internal/platform/toolbridge/handler.go           (Task 4: 删注释里对旧 SkillHostTools 的描述)
internal/platform/toolbridge/module.go            (Task 4: 删旧 SkillHostTools provider 残留注释)
internal/archtest/interface_isolation_guard_test.go (Task 4: 移除 SkillHostTools 字段守卫条目)

internal/provider/codexapp/skill_manifest.go      (Task 5: 用 skillforge.Parse 替换 extractDescriptionFromSkillMD)

internal/module/skilllibrary/reconcile.go         (Task 6a: removeOrphans 上报错误)
internal/module/skilllibrary/store.go             (Task 6b: Get 错误前缀)
```

---

## Task 1-3：✅ 已完成

- Task 1（commit `1072f9f`）：删除 `EnableSkillProgressiveDisclosure` / `SkillCatalogTokenBudget` / `EmitSkillCatalogMetaInstructions` 字段 + env 解析 + 默认值断言测试。
- Task 2（commit `39c830b`）：`internal/module/prompt/dynamic.go` 移除 `DynamicSectionSkillCatalog` 死 re-export；contract 端常量保留。
- Task 3（commit `fced958`）：`internal/module/skill/rpc.go` 删 `skillExpandBodyHandler` + `skillReadResourceHandler` + 它们的注册行；`rpc_types_test.go` 删两条空 CWD 测试条目。**未删** `ExpandBody/ReadResource` types（仍被 service 方法 + toolbridge 包装器引用，留 Task 4）。

满足新 spec §11 RPC 行的"先兼容保留，后删；前置：旧 UI / canary / toolbridge 不再调用"前置条件：
- toolbridge：`provideHostToolRegistry` 只 wire `SkillReadSectionRegistry`，`SkillHostTools` fx 注入路径已断
- 旧 UI / canary：仓内 .ts/.tsx 0 引用，cmd/ 下 0 引用

---

## Task 4: 删 ExpandBody/ReadResource 方法 + pkg/skilltool 旧 schema + toolbridge SkillHostTools wrapper

✅ **已完成**：commit `5ce5333`，净 −1943 LoC（15 个文件变动，2 个整删）。spec / code reviewer 均 APPROVED，pre-commit 守卫（CC ≤ 10／文件 ≤ 600／包 ≤ 30 文件 / 10000 行）全过。

**新范围**（相比原计划扩展）：
- 服务层（`internal/module/skill/skills_expand.go` + tests + 私有 helper）
- pkg/skilltool 整包（旧工具 schema 不再被任何 fx provider 消费）
- toolbridge `SkillHostTools` 包装器（fx wire 已断；只剩自家 tests + 类型残留）

### Step 1: 全仓 reference 扫描（确保删除安全）

```
cd /private/tmp/super-dolphin-skill-refactor-p4

# 服务方法引用
grep -rn "\.ExpandBody\|\.ReadResource" --include="*.go" .

# pkg/skilltool symbol
grep -rn "skilltool\." --include="*.go" .
grep -rn "ToolNameExpandBody\|ToolNameReadResource\|DescriptionExpandBody\|DescriptionReadResource\|ExpandBodyInputSchema\|ReadResourceInputSchema" --include="*.go" .

# toolbridge 旧包装器
grep -rn "SkillHostTools\|NewSkillHostTools\|SkillHostToolReader" --include="*.go" .

# host_tools.go 里 ExpandBodyResult/ReadResourceResult dispatch 分支
grep -rn "ExpandBodyResult\|ReadResourceResult" --include="*.go" .
```

期望找到的引用全部限定在被删的文件 + 自家测试 + 注释里。**任何在生产 dispatcher 路径上的引用**都是未识别的 consumer，必须先解决再删。

### Step 2: 删 service 方法（skills_expand.go）

- 删 `ExpandBody(ctx, ExpandBodyParams) (ExpandBodyResult, error)`
- 删 `ReadResource(ctx, ReadResourceParams) (ReadResourceResult, error)`
- 删私有 helper：`expandBodyApprovalMetadata` / `readResourceApprovalMetadata` / `readResourceData`
- **保留** `requireArtifactApproval`（如果它仍被 service 别处用；先 grep 确认）
- **保留** `ArtifactKindBody` / `ArtifactKindResource` 枚举（spec §10 + §11 明确说 ArtifactKind 不在 P4 范围）

### Step 3: 删测试（skills_expand_test.go）

整文件大概率可整删（spec §8 指 18 ExpandBody + 12 ReadResource 测试）。先扫一遍是否有 helper 被别的 test 文件引用：

```
grep -n "^func " internal/module/skill/skills_expand_test.go
grep -rn "expandTestContext\|skillTestContext" --include="*.go" internal/module/skill/
```

helper 仍被引用就保留 helper 文件，仅删测试函数。

`cwd_scope_test.go` 里所有验证 `ExpandBody/ReadResource` cwd 隔离的测试都要删；保留测试 `Service.ListSkills` / `EnsureSkill` 等其它方法的 cwd 隔离断言。

### Step 4: 删 rpc_skill_types.go 中相关 types

仅当 grep 确认无别处引用时删：`ExpandBodyParams`, `ExpandBodyResult`, `ReadResourceParams`, `ReadResourceResult`。如果 toolbridge `host_tools.go` 还在引用（Step 6 之前是这种情况），先做 Step 6 删 toolbridge wrapper，再回头删 types。

### Step 5: 删 pkg/skilltool 整包

- 整删 `pkg/skilltool/schema.go` + `pkg/skilltool/schema_test.go`
- `pkg/skilltool/` 目录如能整删则整删（确认无其它文件）
- 删完后跑 `go build ./...` 找出任何还 import 这个包的代码（应该只剩 toolbridge `host_tools.go`，下一步一并清）

### Step 6: 删 toolbridge SkillHostTools 包装器

- 整删 `internal/platform/toolbridge/host_tools_test.go`
- `internal/platform/toolbridge/host_tools.go`：
  - 删 `SkillHostTools` 类型 + `NewSkillHostTools` + `(s *SkillHostTools) ListHostTools/HasTool/CallHostTool`
  - **保留** `HostToolRegistry` 接口（仍被 `SkillReadSectionRegistry` 实现）
  - **保留** 注释里"the new HostToolRegistry interface"段说明
  - 如果 `SkillHostToolReader` 接口（`skillpkg.SkillHostToolReader`）只被 `SkillHostTools` 用，一并删；如果还有别的 consumer，保留
- `handler_host_tools.go`：删 `case skillpkg.ExpandBodyResult` / `case skillpkg.ReadResourceResult` 分支
- `handler.go`：更新 line ~39 的注释（删旧 SkillHostTools 描述）
- `module.go`：删 line ~101 旧注释

### Step 7: 修 archtest

`internal/archtest/interface_isolation_guard_test.go:93` 有一条守卫行：
```go
{relPath: "internal/platform/toolbridge/host_tools.go", structName: "SkillHostTools", fieldName: "svc", want: "skillpkg.SkillHostToolReader"},
```
连这一条整删（被守卫的 struct 已不存在）。

### Step 8: build + test

```
cd /private/tmp/super-dolphin-skill-refactor-p4
go build ./...
go vet ./...
go test -count=1 -short \
  ./internal/module/skill/... \
  ./internal/platform/toolbridge/... \
  ./internal/archtest/... \
  ./internal/provider/codexapp/...
```

任何 break 大概率是漏 grep 的 consumer，回 Step 1 重扫。

### Step 9: Commit

```
git commit -m "refactor(skill,toolbridge,skilltool): delete ExpandBody/ReadResource service path

Per spec §11, the skill_expand_body / skill_read_resource pipeline
has had no remaining consumer since P3 (toolbridge host registry only
wires SkillReadSectionRegistry; fx no longer constructs SkillHostTools).
Removed the now-orphaned three layers in one commit:

- internal/module/skill/skills_expand.go: ExpandBody / ReadResource methods
  + private helpers + ~30 test cases (skills_expand_test.go整删)
- pkg/skilltool: 整包删除（旧工具 InputSchema/Description/ToolName 常量）
- internal/platform/toolbridge: SkillHostTools wrapper + host_tools_test.go
  整删；handler_host_tools.go 的 ExpandBodyResult/ReadResourceResult
  dispatch 分支删；archtest interface guard 同步删

ArtifactKind 枚举保留（spec §10 + §11 明确禁止 P4 抢删）。
HostToolRegistry 接口保留（SkillReadSectionRegistry 仍实现）。"
```

---

## Task 5: extractDescriptionFromSkillMD 用 skillforge.Parse 替换

✅ **已完成**：commit `6ddfad2`。原 helper 整函数删除，inline 到 `renderL1CBlock`；新增 3 个测试覆盖 quoted description / CRLF frontmatter / malformed SKILL.md 场景。

**File:** `internal/provider/codexapp/skill_manifest.go`

P3 OBSERVATION：当前 `extractDescriptionFromSkillMD` 用裸 `HasPrefix("description:")` 在前 10 行查找，对带 quoted value（含逗号）/ 前导空行 / BOM 的 frontmatter 静默丢字段。改用 `skillforge.Parse`（已在 Gap #2 commit 中加固 fenced/closing/CRLF 处理）。

### Step 1: Failing test

```go
func TestBuildSkillManifest_HandlesQuotedDescription(t *testing.T) {
    entries := []skilllibrary.SkillEntry{
        {
            Meta:    &skilllibrary.SkillMeta{Name: "x"},
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

倾向 inline 方案（彻底删除原 helper）：

```go
func renderL1CBlock(e skilllibrary.SkillEntry) string {
    desc := ""
    if ps, err := skillforge.Parse(e.SkillMD); err == nil {
        desc = ps.Description
    }
    // ... 后续逻辑保持不变
}
```

新增 import：`github.com/anthropic-ai/super-agent-v3/internal/module/skillforge`

### Step 3-4: test + commit

```
git commit -m "fix(codexapp): use skillforge.Parse for SKILL.md description extraction

原 extractDescriptionFromSkillMD 用 HasPrefix(\"description:\") 在前 10
行查找，对 quoted value、CRLF、BOM、缩进等场景静默丢字段。改用
skillforge.Parse（包含 P1 Gap #2 后已加固的 fenced/closing/CRLF 处理）
真正解析 frontmatter。整函数删除，inline 到 renderL1CBlock。"
```

---

## Task 6: P1/P2 OBSERVATION 修复合并 commit

✅ **已完成**：commit `d71d8ea`。含 6a `removeOrphans` 错误上报 + 6b `Store.Get` 错误前缀。原 6c 已在 Gap #3 commit `516410d` 合并。

**Files**:
- `internal/module/skilllibrary/reconcile.go` (`removeOrphans` 错误上报)
- `internal/module/skilllibrary/store.go` (`Get` 错误前缀)

> **原 6c（ReadSection anchor 契约 godoc）已在 Gap #3 commit `516410d` 落地，本 Task 不再覆盖。**

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

### Step: build + test + commit

```
git commit -m "fix(skilllibrary): improve error handling in reconcile + store.Get

- reconcile.removeOrphans: surface ReadDir errors instead of silently dropping
- store.Get: wrap non-NotExist errors with skilllibrary: prefix and skill name context"
```

---

## Task 7: P4 全测试 + 冒烟

✅ **已完成**：`go test -short ./internal/...` 79 个包全 PASS、0 FAIL。`go build ./...` + `go vet ./...` 干净（仅 macOS SDK 链接警告，与本分支无关）。

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

---

## 已知偏差与未覆盖项

### 不在 P4 范畴但点名给 P5+

按新 spec §10/§11/§12，下列项是后续 phase 的硬前置，P4 **必须明示**给后续工作者：

1. **§10 `allowed_tools` enforcement** — ✅ **已完成**（commit `b3eee92`）。`nativefilter.AggregateAllowedTools` + `BuildClaudeSettings` `permissions.allow` allowlist 在运行时收敛 Claude 会话工具集。设计折中：Claude Code 2.1.119 的 permissions.allow 是 session 级 allowlist，不支持 per-skill 精确控制；本实现是全局并集近似，是现有 CLI 能力下能做到的最接近 spec 语义的严格收敛。
2. **§10 `~/.super-dolphin/skills-trust.json` 迁移/废弃策略** — ⏸️ **当前不需要迁移**：approval.go 由 spec §11 明确要求 P5+ 之前不删，旧 `skills-trust.json` cache 仍在用。但 codify 入 plan：当 ArtifactKind 删除被提上日程（P5+ 某阶段）时，**必须先实现**：
   - 读旧 `skills-trust.json` cache（详见 `internal/module/skill/approval.go: NewApprovalCache`）
   - 按 sidecar 新模型重新发现状态（SkillMeta.Origin / `EvaluateTrust`）与旧 cache 决议合并
   - 写迁移标记避免重复迁移
   - 不得默认丢弃旧用户决议。
   本项本身不跳过 archtest 守卫（当前仅 codify）。
3. **§10 marketplace `signature` 不实现前的 untrusted 处理** — ✅ **已完成**（commit `199a016`）。新增 `skilllibrary.EvaluateTrust(meta) TrustLevel` + `IsTrusted(meta) bool`：origin=marketplace 无论 signature 是否为空均返回 untrusted，走 ArtifactKind 审批 + AllowedTools 收敛路径，避免 "看起来已签名实际未验证" 安全错觉。未来签名校验落地后，本函数升级为对已验证 signature 返回 trusted。

§10 三项中两项已落地，第二项（trust file 迁移）**当前不是 blocking**：approval.go 仍在使用，等未来 ArtifactKind 删除 phase 上日程时再实现迁移函数。**ArtifactKind 删除现在可以 unblock**（§10-1 / §10-3 完成），但仍要等 §10-2 迁移函数实现后才安全删。

### 已知偏差（已 ship，无追溯空间）

按新 spec §12 表格，每个 phase 应附带 feature flag（`SUPER_DOLPHIN_SKILL_CACHE_V2` / `SUPER_DOLPHIN_CLAUDE_CACHE_SKILLS` / `SUPER_DOLPHIN_CODEX_SECTION_TOOLS`）以支持灰度回滚。**P1/P2/P3 实际全部硬切，无任何 flag**。原因：phase 之间快速演进；旧链路已删，flag 已无可保护对象。

后续 phase（P5 nativefilter / P6 FBSD）尚未开始，**必须严格按 spec §12 加 feature flag**（`SUPER_DOLPHIN_NATIVE_FILTER` / `SUPER_DOLPHIN_SKILL_FBSD`），不得复制 P1-P3 的硬切模式。

### 已知偏差（spec 已自标 deferred）

下列项 spec 自身在 §13 / §12 / FBSD 章节标记为 deferred，**不算 P4 偏差**，仅在此重申避免被遗忘：

- **Windows 跨盘 hardlink-copy 第三档 fallback**（spec §6.1 + §13.5）：当前 cliadapter 仅实现 junction → symlink 两档，跨盘失败仍会 error。spec §13.5 自标 deferred。
- **`buildSkillManifest` 单一 L1-C 渲染 vs Hot/Warm/Cold/Frozen 4 tier**（spec §7.1 + §9）：tier 化是 FBSD（P6）的核心动作；P3 注释已明确 "FBSD 暂未实现"。
- **`skill_list_sections` / `skill_list_all` 条件工具**（spec §7.2）：FBSD 降级到 L1-A 时才需要；P6 一并实现。
- **Budget 兜底"按 tier 降级再试"**（spec §7.3）：依赖 tier 化，P6。
- **§4.4 startup recovery**（清 stale `.tmp-*` / 恢复 `.bak-*`）：spec §4.4 列出但 P1 Task 12 reconcile 未实现。**Gap #1 已修 publish 路径**，但 startup 时如有残留 `.bak-*` 仍是手工救援。建议 **P5 reconcile 增强或单独 hotfix** 补这一段。

---

## Phase 4 自审

按 编写计划 §自审：

**1. 规格覆盖：** 对照新 spec §10/§11/§12/§13 + Gap audit 结果：
- §11 RPC handlers / 服务方法 / pkg/skilltool / toolbridge wrapper：Task 1-4 覆盖；ArtifactKind / SkillRef.Mode 延后到 P5+ 已显式说明
- §13 #10 旧/新工具并存窗口：P3 已硬切（`SkillHostTools` fx 不再 wire），Task 4 删剩余类型；system prompt 推荐侧由 codexapp `buildSkillManifest` 控制，无 dual recommendation
- §10 安全替代 3 项：未覆盖项段落显式列入 P5+ 前置
- P1 Gap #1 / #2 / P3 Gap #3：commit `182d032` / `9843bfd` / `516410d` 已 ship
- P5/P6 范围（FBSD / nativefilter / Windows hardlink / startup recovery）显式列入"已知偏差"

**2. 占位符扫描：** 全部代码段为完整可应用编辑；测试代码完整。

**3. 类型一致性：**
- `skillforge.Parse` 跨 Task 5 一致引用；签名与 `internal/module/skillforge/parse.go` 一致
- `errors.Is(err, fs.ErrNotExist)` / `*UnknownAnchorError` 在 Task 6 与 Gap #3 一致

修复内联：暂无问题。

---

## 执行交接

计划已更新并保存到 `docs/superpowers/plans/2026-04-29-skill-refactor-p4-cleanup.md`。Task 1-3 + Gap #1-3 已 commit。两个执行选项：

1. **子代理驱动（推荐）**：每个剩余 Task 派 implementer + spec reviewer + code reviewer，APPROVED 才 commit
2. **当前会话内执行**：implementer 直接落代码，最后跑 Task 7 自检
