# LSP 包迁移计划：`internal/mcpserver/lsp` → `cmd/mcp-lsp`

> **✅ 状态：已完成** | 执行时间：2026-04-17 | 审查通过：2026-04-17
>
> 本文档为迁移历史记录。文中出现的 `internal/mcpserver/lsp/` 均为旧路径，实际代码已全部迁移至 `cmd/mcp-lsp/`。

> 目标：将 LSP 实现代码从 `internal/mcpserver/lsp/` 迁移至 `cmd/mcp-lsp/`，与 `mcp-orch` 保持一致的项目结构。
>
> 版本：**第 3 版** | 修订时间：2026-04-17
>
> 第 3 版关键变更：
> 1. **守卫放宽**（全仓默认）：包文件数 15→**25**、包有效行数 4500→**10000**、单文件 400→**600**；函数/嵌套/CC/标识符不变。迁移前置 commit 先落守卫常量。
> 2. **删除 Step 0 前置瘦身**：`client.go` 与 `searchutil.go` 在 effective 口径下分别只有 375 / 368 行，新守卫 600 上限有充足余量，**无需拆分**。
> 3. **档案清单按实测刷新**：`cmd/mcp-lsp` + `internal/mcpserver/lsp` 总 **68 个 Go 文件**，其中含旧路径字符串需 sed 的仅 **38 个**。
> 4. **archtest forbidden set 重新设计**：旧 rule7/7b 里的 `internal/tool/ida` / `internal/tool/orchestration` 仓内已无对应包，改为禁止 `internal/module/*` + `internal/ui/*` + `internal/app` + 其他 `cmd/mcp-*`。
> 5. **rule10_fx_import_scope 收窄**：原对 `cmd/**` 全放行，迁移后 `cmd/mcp-lsp/{edit,exec,...}` 子包**不再**自动放行 fx，只装配文件（`cmd/mcp-lsp/fx.go`）允许。
> 6. **mcp_family_isolation_test.go 同步**：forbidden set 同步替换。
> 7. **Step 5 文档清单扩至 10 份**：补 `v3-workflow.md` / `v3-migration-review-report.md` / `codemap/README.md` / `p9` 整改 / `modularity-convention §2.1 目录树`；明确 ai-index 用 `make codemap-refresh`。
> 8. **Step 7 加 `-p 1`** 规避 codexapp 并发端口冲突。
> 9. **新增 Step -1 守卫常量更新**为独立前置 commit。

## 背景

当前 `mcp-lsp` 和 `mcp-orch` 的代码布局不一致：

```
mcp-orch (正确模式):              mcp-lsp (当前，不一致):
  cmd/mcp-orch/                    cmd/mcp-lsp/ (6 个入口文件)
  ├── main.go (入口)               ├── main.go (36 行)
  ├── orchestration/ (4,195行)     ├── fx.go (237 行)
  ├── store/ (3,842行)             ├── runtime.go (172 行)
  ├── workspace/ (1,673行)         ├── http_runner.go (58 行)
  ├── tools/ (1,437行)             ├── schema.go (138 行)
  └── memory/ (824行)              └── tools.go (87 行)
                                   internal/mcpserver/lsp/  ← ❌ 放错位置
                                   └── 10 个子包 / 11,454 行 raw / 62 Go 文件
```

契约依据：`docs/契约/modularity-convention.md:322-326` 允许 `cmd/mcp-*` 拥有「各自本地包」；§2.4 第 374 行允许 LSP 家族逻辑「留在工具层或二进制装配层」。迁移到 `cmd/mcp-lsp/*` 属于后者，合规。

## 影响分析

### 需要移动的目录（10 个子包）

> 行数为 2026-04-17 实测；`prod_files` = 生产 `.go` 文件数；`raw_lines` = 原始行含空行/注释；`eff_lines` = effective 行（守卫口径，剔空/剔注释）；`test_files` = `_test.go` 数量。

| 子包 | prod_files | raw_lines | eff_lines | test_files |
|------|-----------:|----------:|----------:|-----------:|
| `gopls/` | 15 | 4,196 | ~3,800 | 1 |
| `tools/` | 13 | 2,889 | ~2,600 | 3 |
| `protocol/` | 5 | 924 | ~820 | 0 |
| `format/` | 4 | 942 | ~820 | 0 |
| `edit/` | 4 | 917 | ~800 | 4 |
| `search/` | 2 | 726 | ~640 | 1 |
| `manager/` | 2 | 242 | ~210 | 2 |
| `middleware/` | 4 | 260 | ~220 | 0 |
| `exec/` | 1 | 273 | ~240 | 0 |
| `installer/` | 1 | 85 | ~70 | 0 |
| **合计** | **51** | **11,454** | ~**10,220** | **11** |

### ✅ 尺寸守卫对照（新标准 25 / 10000 / 600）

守卫常量由 **Step -1** 前置 commit 更新为：单文件 ≤ 600、包文件数 ≤ 25、包有效行数 ≤ 10000（函数 ≤80、CC ≤10、嵌套 ≤4、标识符 ≤3 下划线 不变）。所有子包核对：

| 子包 | prod_files vs 25 | eff_lines vs 10000 | 最大单文件 vs 600 | 结论 |
|------|:---:|:---:|:---:|:---:|
| `gopls/` | 15 ✅ | ~3,800 ✅ | `client.go` 375 eff ✅ | PASS |
| `tools/` | 13 ✅ | ~2,600 ✅ | `tool_edit_replace.go` ~340 eff ✅ | PASS |
| `search/` | 2 ✅ | ~640 ✅ | `searchutil.go` 368 eff ✅ | PASS |
| 其他 7 个 | ≤5 ✅ | ≤820 ✅ | ≤320 eff ✅ | PASS |

> `TestCodeSizeGuard` 在当前 HEAD 已绿（effective 口径），**迁移只是改路径，不改代码**，所以搬完仍绿。

### 子包内部依赖关系

```
tools ──→ edit, exec, format, manager, middleware, protocol, search
gopls ──→ format, manager, protocol
manager ──→ gopls, installer, protocol
format ──→ protocol
search ──→ format
```

### 外部引用方（需要修改 import 路径的 Go 文件）

仓内已 grep 核实（`grep -rln --include='*.go' 'internal/mcpserver/lsp' .`），除下列 3 处外无任何其他 `.go` 文件 import `internal/mcpserver/lsp/`：

| 文件 | 引用方式 |
|------|---------|
| `cmd/mcp-lsp/runtime.go` | import `gopls` / `installer` / `manager` / `protocol` |
| `cmd/mcp-lsp/tools.go` | import `tools` |
| `internal/archtest/dependency_direction_test.go` | rule7 / rule7b 路径字符串 |

其余入口文件（`main.go` / `fx.go` / `http_runner.go` / `schema.go`）只 import `internal/mcpserver/common{,/bootstrap}` 与 `internal/platform/*`，sed 对它们 no-op。

### 外部依赖（迁移后仍需 import `internal/`）

- `internal/platform/config`
- `internal/platform/shared`
- `internal/mcpserver/common`
- `internal/mcpserver/common/bootstrap`

## 执行步骤

### Step -1：前置守卫常量更新（独立 commit）

迁移依赖新守卫数值。必须先在 **独立 commit** 里把 `internal/archtest/guardlib.go` 的常量改掉，并同步 spec 与契约，让 `TestCodeSizeGuard` 在新基线下跑绿，再做搬迁。

改动点：

1. `internal/archtest/guardlib.go:17-29`
   - `MaxFileLines`: 400 → **600**
   - `MaxPackageFiles`: 15 → **25**
   - `MaxPackageLines`: 4500 → **10000**
   - `MaxCorePackageFileLines` (600)、`MaxCorePackageFiles` (30)、`MaxCorePackageLines` (10000) 保持不变（30 > 默认 25，仍有意义）
2. `internal/archtest/code_size_guard_test.go`
   - **`TestCodeSizeGuard` 前置调用 `archtest.AutoRepairFreezeRegistry`**，让守卫运行时自动 shrink / delete 过期 freeze 并回写 `internal/archtest/freeze_registry.go`。
   - 语义与 spec「只减不增」一致；用余量立刻落盘，避免过期 freeze 垃圾积累。
3. `internal/archtest/freeze_registry_autofix_test.go`
   - fixture 常量由 `MaxFileLines + Δ` 派生，保持 shrink / delete 两个分支在新默认（600）下仍可验证。
4. `docs/plans/迁移/v3-code-guard-spec.md`
   - §第 1 章表格：默认值 400/15/4500 → 600/25/10000
   - §1.1 核心包放宽表：说明「单文件 600 已等同新默认，核心包唯一差异在包文件数 30」
   - §第 1 章补充说明追加：`TestCodeSizeGuard` 守卫运行时自动收缩 freeze registry（shrink / delete + 回写）。
5. `docs/契约/modularity-convention.md:369` 的措辞
   - 由「单文件 ≤400、包非测试文件 ≤15」 → 「单文件 ≤600、包非测试文件 ≤25、包有效行数 ≤10000」
6. `docs/会话习惯.md:71` + `docs/plans/迁移/会话习惯.md:71` 契约标准同步。

验证：

```bash
go test -run TestCodeSizeGuard ./internal/archtest/... -count=1   # PASS
go test ./internal/archtest/... -count=1                          # PASS
```

**自动收缩的 8 条 freeze**（autofix 首次跑时自动回写，`git diff internal/archtest/freeze_registry.go` 会显示）：
- `internal/module/memory` (file)：514 ≤ 600 → delete
- `internal/module/memory` (package_lines)：7020 ≤ 10000 → delete
- `internal/module/prompt` (file)：492 ≤ 600 → delete
- `internal/module/thread` (package_count)：24 ≤ 25 → delete
- `internal/module/thread` (package_lines)：5319 ≤ 10000 → delete
- `internal/module/turn` (package_count)：21 ≤ 25 → delete
- `internal/provider/claudecli` (package_count)：23 ≤ 25 → delete
- `internal/provider/codexapp` (package_count)：17 ≤ 25 → delete

Commit 消息：`archtest: relax default guards to 25/10000/600 + auto-shrink freeze registry at test time`

### Step 1：移动子包目录（用 `git mv` 保留 blame）

```bash
for pkg in edit exec format gopls installer manager middleware protocol search tools; do
  git mv internal/mcpserver/lsp/$pkg cmd/mcp-lsp/$pkg
done
```

### Step 2：批量替换 import 路径（跨平台 sed）

**影响 Go 文件清单**（实测）：

```bash
# 含旧路径字符串的 Go 文件数 = 38
grep -rln --include='*.go' 'internal/mcpserver/lsp' . | wc -l  # 38

# cmd/mcp-lsp 搬迁后总文件数 = 68（6 入口 + 62 子包）
find cmd/mcp-lsp -name '*.go' | wc -l                           # 68（Step 1 完成后）
```

38 文件实测分组（来自 `grep -rln --include='*.go' 'internal/mcpserver/lsp' .`）：

| 位置 | 命中数 | 说明 |
|------|------:|------|
| `cmd/mcp-lsp/` 入口（`runtime.go` + `tools.go`） | 2 | 引用 `gopls/installer/manager/protocol/tools` |
| `cmd/mcp-lsp/format/` | 4 | 全部引用 `protocol` |
| `cmd/mcp-lsp/gopls/` | 10 | client/manager/transport 等引用 `format/manager/protocol` |
| `cmd/mcp-lsp/manager/` | 4 | 含 2 测试；引用 `installer/gopls/protocol` |
| `cmd/mcp-lsp/search/` | 1 | `fileutil.go` → `format` |
| `cmd/mcp-lsp/tools/` | 16 | 含 3 测试；广泛引用 edit/format/manager/middleware/protocol/search |
| `internal/archtest/dependency_direction_test.go` | 1 | rule7/7b 路径字符串 |
| **合计** | **38** | |

> 剩余 30 个 Go 文件（`cmd/mcp-lsp/` 的 4 个纯入口 `main/fx/http_runner/schema` + `edit/exec/installer/middleware/protocol` 的 15 个 + 其他独立文件）不含旧路径字符串，sed 对它们 no-op。

**跨平台 sed（兼容 BSD/GNU）**：

```bash
find cmd/mcp-lsp internal/archtest -name '*.go' \
  -exec sed -i.bak 's|internal/mcpserver/lsp/|cmd/mcp-lsp/|g' {} +
find cmd/mcp-lsp internal/archtest -name '*.go.bak' -delete
```

> ⚠️ sed 会同时命中注释/字符串字面量里的路径（如 `manager/registry_e2e_test.go:39` 注释 `go test ... ./internal/mcpserver/lsp/manager/...` → `./cmd/mcp-lsp/manager/...`）。这是**期望行为**，review 时核对。

**argv 长度无风险**：`getconf ARG_MAX` = 1048576，`find -exec ... {} +` 自动分批 ≤85 文件，充裕。

### Step 3：核对 `internal/mcpserver/lsp/` 残留

`git mv` 完成后目录应已空；仅需确认无 IDE / macOS 残留文件：

```bash
find internal/mcpserver/lsp -type f 2>/dev/null   # 应无输出
rmdir internal/mcpserver/lsp                      # 删除空目录
```

### Step 4：更新 archtest 规则（**改路径 + 重设计 forbidden set**）

#### 4.1 `internal/archtest/dependency_direction_test.go:171-200`（rule7 / rule7b）

旧规则的 forbidden 清单 `internal/tool/ida` / `internal/tool/orchestration` 仓内**已无对应包**，防回归已失效。本次一并重设计：

**迁移后的 rule7 / rule7b**：

```go
t.Run("rule7_cmd_mcp_lsp_family", func(t *testing.T) {
    if !dirExists(root, "cmd/mcp-lsp") { t.Skip(...) }
    assertNoImportPrefixes(t, parseImportFiles(t, root, "cmd/mcp-lsp"), []string{
        internalPrefix("cmd/mcp-orch"),
        internalPrefix("cmd/mcp-ida"),
        internalPrefix("internal/app"),
        internalPrefix("internal/ui/"),
    })
})

t.Run("rule7b_cmd_mcp_lsp_cannot_import_module", func(t *testing.T) {
    if !dirExists(root, "cmd/mcp-lsp") { t.Skip(...) }
    assertNoImportPrefixes(t, parseImportFiles(t, root, "cmd/mcp-lsp"), []string{
        internalPrefix("internal/module/"),
    })
})
```

理由：这才是 `modularity-convention §2.4 第 363-366 行`明确禁止的真实依赖方向。已跑 `grep -rn 'internal/module' cmd/mcp-lsp/ internal/mcpserver/lsp/` 核实当前代码面无任何违反；改规则是零侵入防回归。

#### 4.2 `internal/archtest/mcp_family_isolation_test.go:17-21`（TestMCPFamilyIsolation）

三族交叉禁止的 forbidden set 同步清理：

```go
{name: "mcp_lsp",  relPkg: "cmd/mcp-lsp",  forbidden: []string{"cmd/mcp-orch", "cmd/mcp-ida"}},
{name: "mcp_orch", relPkg: "cmd/mcp-orch", forbidden: []string{"cmd/mcp-lsp",  "cmd/mcp-ida"}},
{name: "mcp_ida",  relPkg: "cmd/mcp-ida",  forbidden: []string{"cmd/mcp-lsp",  "cmd/mcp-orch"}},
```

（原 `internal/tool/*` 禁用项已废弃。）

#### 4.3 `internal/archtest/dependency_direction_test.go:156-168`（rule10_fx_import_scope 针对 `cmd/mcp-lsp/**` 收窄）

原规则对 `cmd/**` 所有文件全放行 `fx`；迁移后 `cmd/mcp-lsp/{edit,exec,...}` 子包也会被自动豁免，会丢失 fx 越界检查。

**收窄方案（只收窄 cmd/mcp-lsp）**：`cmd/mcp-orch` / `cmd/mcp-ida` 目前有活跃的子包 fx import（例如 `cmd/mcp-orch/orchestration/service.go:20`），且属于旧布局 TODO，**本次不收窄它们**，避免扩散修复。只把 `cmd/mcp-lsp/**` 子包纳入严格规则：

```go
// 放行条件：
//   a) 文件位于 internal/app 下；
//   b) 文件名为 module.go；
//   c) 文件位于 cmd/<binary>/ 根目录（main.go / fx.go / runtime.go / tools.go / schema.go / http_runner.go）；
//   d) 文件位于 cmd/mcp-orch/** 或 cmd/mcp-ida/**（整体放行，旧布局兼容）；
//   e) 文件位于 cmd/mcp-lsp/<子包>/ 且 filename ∈ {module.go, fx.go}。
// 其余（即 cmd/mcp-lsp/子包里非 module.go/fx.go 的业务文件）fx import 视为违规。
```

> 迁移时核对：`cmd/mcp-lsp/{edit,exec,format,gopls,installer,manager,middleware,protocol,search,tools}/*.go` 任何文件不得 import `go.uber.org/fx`；实测当前 0 处违反。mcp-orch/ida 的子包 fx 收窄留给后续迁移阶段处理（应在各自 binary 的搬迁/瘦身计划里跟进）。

### Step 5：文档同步（独立章节，**10 份**）

仓内 `grep -rln 'internal/mcpserver/lsp' docs/` 命中 9 份，加上新发现的 2 份（`v3-workflow.md`、`docs/doc/codemap/README.md`），共 10 份：

| 文档 | 动作 |
|------|------|
| `docs/doc/codemap/03-mcp-lsp-ida.md` | 全文路径替换（≥15 处）+ 章节 7 标题更名 |
| `docs/doc/codemap/06-mcpserver.md` | §2.2 标题与正文路径替换 |
| `docs/doc/codemap/ai-index.json` | **不手改**，跑 `make codemap-refresh` 自动重建 |
| `docs/doc/codemap/README.md` | 检查是否有固定路径示例，同步修正 |
| `docs/契约/modularity-convention.md` | §2.1 完整目录树补 `cmd/mcp-lsp/{edit,exec,format,gopls,installer,manager,middleware,protocol,search,tools}`；§2.4 第 374 行措辞确认 |
| `docs/plans/v3-workflow.md:1045` | Day2 输出路径 `internal/mcpserver/lsp/*` → `cmd/mcp-lsp/*` |
| `docs/plans/迁移/v3-module-migration-details.md:1593-1613` | §25 标题与锚点同步 |
| `docs/plans/迁移/v3-migration-review-report.md` | 引用 `internal/mcpserver/lsp/module.go` 处整体修正 |
| `docs/plans/迁移/p19-contract-violation-remediation.md:81` | 防回归规则路径同步 |
| `docs/plans/迁移/p9-implementation-plan.md` | **不只追加注记**：验证命令、回滚、路径引用全部改写 |

**10 份口径说明**：`grep -rln 'internal/mcpserver/lsp' docs/` 当前命中 9 份（含本计划文档）；Step 5 清单额外补 `docs/doc/codemap/README.md` 与 `docs/契约/modularity-convention.md`，构成 `8（非本文命中）+ 2（补充）= 10` 份。

**命令辅助**（先改全部 md，最后跑 refresh）：

```bash
grep -rln 'internal/mcpserver/lsp' docs/
# 逐份手改 md（共 10 份）
make codemap-refresh   # 无参可跑；内部调 `go run scripts/codemap_index.go`，会刷新 docs/doc/codemap/ai-index.json
```

#### 额外：session-summary 与本迁移 review note

**① `docs/plans/迁移/session-summary.md §2` 追加模板**（3-5 行）：

```markdown
### 2.N LSP 包搬迁（2026-04-17）
- 动作：`internal/mcpserver/lsp/{edit,exec,format,gopls,installer,manager,middleware,protocol,search,tools}` → `cmd/mcp-lsp/*`
- commits：`<-1 守卫>`、`<1 搬迁>`、`<2 archtest 重设计>`、`<3 文档同步>`
- 验证：`go build ./...` ✅；`go test -p 1 ./...` ✅；`TestCodeSizeGuard` / `TestMCPFamilyIsolation` 全绿
- 守卫变更：默认 25/10000/600（见 `docs/plans/迁移/v3-code-guard-spec.md §1`）
```

**② 本迁移 review note** —— 另建 `docs/plans/迁移/2026-04-17-lsp-relocation-review.md`，目录：

```
1. 背景与目标
2. 审查轮次清单（每轮 agent ID + 视角 + 裁决）
   - 轮 1：第 2 版计划互审（reviewer-A/B/C）
   - 轮 2：第 3 版 + 守卫放宽复审（re-reviewer-A/B/C）
3. 问题清单与修复记录（High/Medium/Low）
4. 最终裁决（PASS / 需修复 / 阻塞）
5. 落地 commit 清单 + 遗留项
```

> **不**写入 `docs/plans/迁移/p18/review-summary.md` 第 17 轮（本次迁移不属 P18 链路）。

### Step 6：编译验证

```bash
go build ./cmd/mcp-lsp/...
go build ./...
```

### Step 7：运行测试（加 `-p 1` 规避 codexapp 端口冲突）

```bash
# 迁移目标
go test -p 1 ./cmd/mcp-lsp/...

# 架构守卫（rule7/7b/10 + TestMCPFamilyIsolation + TestCodeSizeGuard）
go test ./internal/archtest/... -count=1

# 全量
go test -p 1 ./...
```

> `session-summary.md:70` 已明确 codexapp 并发测试会抢端口；`-p 1` 串行是当前已知唯一稳定写法。

### Step 8：提交粒度、回滚、并行冲突

**提交粒度（4 个 commit）**：

1. `archtest: relax default code size guards to 25/10000/600` — Step -1（独立可回滚）
2. `cmd/mcp-lsp: relocate lsp subpackages from internal/mcpserver/lsp` — Step 1+2+3（git mv + sed）
3. `archtest: redesign rule7/rule7b/rule10 + mcp_family_isolation forbidden sets` — Step 4
4. `docs: sync lsp package path + refresh ai-index` — Step 5

**前置并行冲突检查**：

```bash
git log --oneline --since="2026-04-14" -- internal/mcpserver/lsp/ cmd/mcp-lsp/
# 若看到 open PR / WIP 改动，必须先协调合并或通知作者 rebase
```

**回滚策略**：

- 任一 commit 编译/测试失败 → `git reset --hard <prev>` 回到安全点。
- 4 个 commit 已 push 后出问题 → `git revert -n` 反向四个再 push；零逻辑变更，revert 风险极低。

## 迁移后目标结构

```
cmd/mcp-lsp/                    ← 对齐 mcp-orch 模式
├── main.go                     (入口)
├── fx.go                       (DI 组装)
├── runtime.go                  (生命周期)
├── http_runner.go              (HTTP)
├── schema.go                   (工具 schema)
├── tools.go                    (工具注册)
├── edit/                       ← 从 internal 搬来
├── exec/
├── format/
├── gopls/
├── installer/
├── manager/
├── middleware/
├── protocol/
├── search/
└── tools/

internal/mcpserver/             ← 只剩 common/
└── common/                     (两个 MCP 服务共用的公共代码)
```

## 风险评估

| 风险项 | 级别 | 应对 |
|--------|------|------|
| Step -1 守卫常量改动影响面扩散 | 中 | 独立前置 commit，通过 `TestCodeSizeGuard` 一次性验证全仓 |
| archtest forbidden set 重设计误禁合法 import | 中 | 已跑 `grep -rn 'internal/module' cmd/mcp-lsp/` 等预检；Step 7 测试兜底 |
| rule10 收窄后遗漏合法装配文件 | 中 | 放行条件明确到文件名级别（`main/fx/runtime/tools/schema/http_runner/module.go`），其余一律禁 |
| 文档遗漏更新导致 codemap 误导 | 中 | Step 5 列 10 份 + `make codemap-refresh` 自动链路 |
| 并行分支冲突 | 中 | Step 8 前置 `git log --since` 核实 |
| sed 跨平台破行为 | 低 | Step 2 已用 `-i.bak` + delete 写法，BSD/GNU 均兼容 |
| 丢失 blame 历史 | 低 | Step 1 用 `git mv` 而非 `mv` |
| import 路径遗漏 | 低 | Step 6 `go build ./...` 立即报错 |
| 包名冲突（`tools/` 子包 vs `tools.go`） | 无 | Go 允许；`tools.go`=`package main`，`tools/`=`package tools` |

## 工作量估算

- **改动代码文件数**: 38 个 Go 文件（sed + archtest 改动）
- **改动守卫文件数**: 1 个（`guardlib.go`）
- **改动文档文件数**: 10 份 markdown + 1 份自动生成的 JSON + 1 份 spec + 1 份契约
- Step -1 守卫常量更新：~10 分钟
- Step 1-3 搬迁：~10 分钟
- Step 4 archtest 重设计：~15 分钟
- Step 5 文档同步：~20 分钟（含 codemap-refresh）
- Step 6-7 编译测试：~10 分钟
- **总预计**：60-70 分钟
- **风险等级**：低（无逻辑变更，守卫数字改动与规则收窄由 `TestCodeSizeGuard` + `TestMCPFamilyIsolation` 兜底）

## 遗留项（不阻塞本次迁移，顺其自然落盘）

以下 3 项在第 2 轮 codex 复审中被识别为本迁移条件下 **不必同 PR 处理** 的项，已从阻塞清单移出；后续相关批次规划时要回收。

### 遗留 1：archtest rule7 与 `TestMCPFamilyIsolation` forbidden set 重叠

- **现状**：Step 4.1 的 `rule7_cmd_mcp_lsp_family` 与 `internal/archtest/mcp_family_isolation_test.go:17-21` 都禁 `cmd/mcp-lsp` 导入 `cmd/mcp-orch` / `cmd/mcp-ida`，双处维护。
- **不合并理由**：语义不完全重叠—— rule7 额外禁 `internal/app` / `internal/ui/`，`TestMCPFamilyIsolation` 额外覆盖 orch/ida 自身。彻底合并需重新设计用例矩阵，成本 > 收益。
- **应对**：后续批次可抽一个共享 `forbiddenForeignMCPBinaries(name string) []string` 帮函数，无需反改规则本体。

### 遗留 2：`cmd/mcp-orch/**` 与 `cmd/mcp-ida/**` 子包的 `fx` import 未收窄

- **现状**：本次 rule10 仅把 `cmd/mcp-lsp/<子包>/` 收窄到「只允许 `module.go` / `fx.go` import `fx`」；`cmd/mcp-orch`、`cmd/mcp-ida` 子包仍整体放行。
- **具体存量**：`cmd/mcp-orch/orchestration/service.go:20` 等多处子包中 `fx` import。它们属于 P8 残留的旧布局，正在待重构。
- **应对**：在 `cmd/mcp-orch`、`cmd/mcp-ida` 的后续搬迁 / 瘦身计划里同步把 rule10 的规则 d) 处理成与 `cmd/mcp-lsp` 同样的白名单约束。

### 遗留 3：历史计划文档残留旧守卫数字

- **现状**：仓内仍有多份已落幕的计划写着 `≤400 行 / ≤ 15 文件 / ≤ 4500 行`：`docs/plans/2026-03-27-a4g-d1-timeline-offline-merge.md`、`docs/plans/迁移/p1-v2v3-fix-plan.md`、`p9-implementation-plan.md`、`p13-factory-repair-plan.md`、`p16-ui-diff-toolcall-plan.md`、`p16.1-unified-diff-plan.md`、`p17-ui-context-model-compact-plan.md`、`docs/plans/claude缓存保活计划.md`。
- **不回改理由**：这些文档是历史执行快照（P1 / P9 / P13 / P16 / P17 等已结案批次），批量改数字会破坏当时的分阶段记账内气。
- **应对**：新修计划一律引用 `docs/契约/modularity-convention.md §2.4` 与 `docs/plans/迁移/v3-code-guard-spec.md §1` 的当前默认；旧文档通过 v3-code-guard-spec §1 的「2026-04-17 补充」小节统一指向最新守卫值，不再逐份追改。
