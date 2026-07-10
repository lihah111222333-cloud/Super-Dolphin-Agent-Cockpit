# Backend Boundary AI Governance Design

状态：已批准进入实现计划
日期：2026-07-10
范围：后端边界违规定位、后端治理清单、Archtest 规则地图与 README 测试统计

## 1. 目标

本变更把后端边界从“能发现违规”推进到“AI 能直接定位、能证明每个后端顶层目录由谁治理、能从同一事实源生成导航文档”。完成后：

1. 边界违规输出包含源码相对路径、精确行号和列号。
2. `cmd/<name>`、`internal/<name>`、`pkg/<name>` 中包含 Go 源码的每个顶层后端目录，都必须在机器可校验治理清单中登记。
3. 每个治理项必须绑定至少一条 canonical backend boundary rule，或至少一个现有 `internal/archtest/*_test.go` 专项 guard。
4. 同一生成器输出 Archtest 规则地图，并刷新根 `README.md` 的架构测试数量。
5. `make codemap-check` 能只读发现规则地图或 README 统计漂移；`make codemap-refresh` 能按正确顺序刷新它们与 `ai-index.json`。

## 2. 非目标

- 不拆分、移动或重命名 `internal/archtest/backend_boundary_evaluator.go`。
- 不引入 JSON、YAML 或手写 Markdown 作为第二边界事实源。
- 不扩大为全仓 SSA 扫描；现有窄 SSA 专项 guard 保持原样。
- 不顺手修改当前主工作区的 capability-contract、launchd 设计或生成脚本草稿。
- 不通过 baseline、宽泛例外或默认治理项掩盖未登记目录。

## 3. 方案选择

### 采用：Go registry 事实源 + Go 生成器

治理清单和 canonical rule 一样由 `internal/archtest` 的 Go 类型表达。生成器只读取公开的深拷贝视图，输出 Markdown 和 README 统计。优点是类型安全、可由现有 archtest 直接校验、不会产生独立配置漂移。

### 不采用：JSON manifest 事实源

JSON 便于外部读取，但会让 rule registry、治理清单和生成文档形成两个需要同步的事实源，违背本次目标。

### 不采用：完全从 AST 自动推断治理关系

AST 可以发现目录和 import，却无法可靠推断某个专项 guard 对哪个目录负责。治理归属必须显式表达，AST 只负责反查覆盖完整性。

## 4. 组件与文件职责

### 4.1 精确违规位置

修改 `internal/archtest/backend_boundary_evaluator.go`，但不拆文件。

- 将解析后的 import 从裸字符串提升为仅在 evaluator 内部使用的结构：导入路径、行号、列号。
- 使用同一个 `token.FileSet` 解析文件，通过 `ImportSpec.Path.Pos()` 取得位置。
- 所有 typed rule evaluator 继续复用同一分派链，只把 import 路径和位置一起传递。
- 违规格式固定为：

```text
internal/module/thread/service.go:3:10 imports github.com/... (rule=... owner=... reason=...)
```

- 语法错误、读文件错误、未知 rule 和零覆盖语义保持 fail-fast。

### 4.2 后端治理清单

新增 `internal/archtest/backend_boundary_governance.go`。这是新增治理能力，不是对 evaluator 的拆分。

核心结构包含：

- `Surface`：精确顶层目录，例如 `internal/module` 或 `cmd/mcp-lsp`。
- `RuleIDs`：该目录使用的 canonical backend boundary rule ID。
- `GuardFiles`：该目录由哪些 `internal/archtest/*_test.go` 专项 guard 补充治理。
- `Reason`：为什么这些 rule/guard 能代表该目录的边界责任。

默认清单通过公开函数返回深拷贝。校验器必须拒绝：

- 实际存在但未登记的 `cmd/<name>`、`internal/<name>`、`pkg/<name>` Go 顶层目录。
- 登记但不存在、没有 Go 源码或重复的 surface。
- 未知 canonical rule ID。
- 不存在、越出 `internal/archtest`、非 `_test.go` 的 guard 文件。
- `RuleIDs` 与 `GuardFiles` 同时为空。
- 空 reason、重复 rule ID、重复 guard 文件。

目录发现只读取生产仓库树，遵循现有 skip-dir 约定；生成文件可以位于已治理 surface 内，但不能让整个 surface 消失。

### 4.3 Archtest 规则地图与 README 统计

新增可测试命令 `scripts/archtestmap`：

- 默认刷新模式。
- `--check` 只读比较并在漂移时非零退出。
- 从 `DefaultBackendBoundaryRegistry()` 和默认治理清单生成 `docs/doc/codemap/13-archtest-boundaries.md`。
- 规则地图输出：owner、canonical rule、kind、文件范围、allow/deny/scope/exception 摘要，以及每个后端顶层目录的 rule/guard 归属。
- 使用 Go AST 统计 `internal/archtest/**/*_test.go` 中顶层 `Test*` 函数和包含这些测试的文件数，确定性刷新根 `README.md` 的 Architecture Tests 行。
- 输出按 surface、rule ID、guard path 排序，禁止依赖 map 迭代顺序。

Makefile 接线：

```text
make codemap-refresh
  -> go run ./scripts/archtestmap
  -> go run scripts/codemap_index.go

make codemap-check
  -> go run ./scripts/archtestmap --check
  -> go run scripts/codemap_index.go --check
```

这样新增的第 13 卷先生成，再被 `ai-index.json` 收录；check 模式全程不写工作区。

## 5. 数据流与失败语义

```text
Go registry + governance manifest + source tree
  -> registry/governance validators
  -> boundary evaluation with import positions
  -> scripts/archtestmap renderer
  -> 13-archtest-boundaries.md + README Architecture Tests row
  -> codemap_index.go
  -> ai-index.json
```

任何输入缺失、未知 ID、未治理目录、无效 guard 路径、解析失败或生成物漂移都必须立即失败。生成器不得写默认空表、吞错继续或自动删除未知内容。

## 6. 测试设计

按 TDD 顺序实现：

1. 先增加违规夹具，精确断言单行 import 与 import block 的 `line:column`；确认旧实现失败。
2. 增加治理清单校验测试：当前树完整、临时新增顶层目录会失败、未知 rule/guard/重复项/空机制会失败。
3. 增加生成器单测：Markdown 稳定排序、README 行精确替换、`--check` 漂移失败、刷新后通过。
4. 运行 focused tests，再运行 `./scripts/test_with_guard.sh ./internal/archtest -count=1`、`go test ./scripts/archtestmap -count=1`、`make codemap-refresh`、`make codemap-check`、`make guard`。
5. 对所有修改的 Go 文件运行 LSP diagnostics；Warning、Information、Hint 同样必须清零。

## 7. 验收标准

- 已知边界违规输出含精确 `file:line:column`。
- 当前所有后端顶层 Go 目录恰好被治理清单覆盖，新增未登记目录会使测试失败。
- 每个治理项至少指向 canonical rule 或真实专项 guard。
- Archtest 规则地图和 README 统计均由同一命令生成并支持只读 check。
- 新地图被 codemap README/ai-index 正常收录。
- evaluator 文件未拆分。
- archtest、生成器测试、codemap check、make guard、LSP diagnostics 全部通过。
