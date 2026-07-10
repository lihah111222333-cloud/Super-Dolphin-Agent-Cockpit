# Backend Boundary AI Governance Design

状态：已按复核意见修正，待进入实现计划
日期：2026-07-10
范围：后端边界违规定位、后端治理清单、Archtest 规则地图与 README 测试统计

## 1. 目标

本变更把后端边界从“能发现违规”推进到“AI 能直接定位、能证明每个后端顶层目录由谁治理、能从同一事实源生成导航文档”。完成后：

1. 边界违规输出包含源码相对路径、精确行号和列号。
2. `cmd/<name>`、`internal/<name>`、`pkg/<name>` 中包含 Go 源码的每个顶层后端目录，都必须在机器可校验治理清单中登记。
3. 每个治理项必须绑定至少一条 canonical backend boundary rule，或至少一个带稳定 ID 和真实测试入口的专项 guard。
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

owners、canonical rules、specialized guards 和 governed surfaces 全部收进同一个 `BackendBoundaryRegistry`。生成器只读取 `DefaultBackendBoundaryRegistry()` 返回的深拷贝，输出 Markdown 和 README 统计。优点是类型安全、可由现有 archtest 直接校验、不会产生平行默认集合。

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

新增 `internal/archtest/backend_boundary_governance.go`。这是新增治理能力，不是对 evaluator 的拆分；`backend_boundary_registry.go` 只扩展统一 registry 字段和深拷贝接线。

`BackendBoundaryRegistry` 扩展为一个事实源：

```go
type BackendBoundaryRegistry struct {
	Owners   []BackendBoundaryOwner
	Rules    []BackendBoundaryRule
	Guards   []BackendBoundaryGuard
	Surfaces []BackendBoundarySurface
}
```

- `BackendBoundaryGuard`：稳定 `ID`、精确 `_test.go` 文件、一个或多个 `TestNames`、非空原因。
- `BackendBoundarySurface`：精确顶层目录，例如 `internal/module` 或 `cmd/mcp-lsp`，以及 `RuleIDs`、typed `GuardIDs` 和非空原因。
- surface 不直接持有 guard 文件路径；只能引用已经注册的 guard ID，避免任意文件名伪装成治理证明。

统一 registry 通过公开函数返回完整深拷贝。校验器必须拒绝：

- 实际存在但未登记的 `cmd/<name>`、`internal/<name>`、`pkg/<name>` Go 顶层目录。
- 登记但不存在、没有 Go 源码或重复的 surface。
- 未知 canonical rule ID。
- 未知、重复或没有被任何 surface 引用的 guard ID。
- 不存在、越出 `internal/archtest`、非 `_test.go`、非普通文件，或通过文件/父目录 symlink 逃逸的 guard 文件。
- guard 声明的测试名不存在、重复，或不是当前 Go build context 选中且可由 `cmd/go` 发现的顶层 Test 函数；泛型测试函数必须拒绝，裸 `Test`、`*T`、`*pkg.T` 和空结果列表按 Go 工具语义处理。
- surface 引用的 rule 没有匹配实际 Go 文件，或 policy 驱动的 rule 没有任何实际作用于该 surface 文件的策略。
- `RuleIDs` 与 `GuardIDs` 同时为空。
- 空 reason、重复 rule ID、重复 guard ID。

目录发现只读取生产仓库树，遵循现有 skip-dir 约定；生成文件可以位于已治理 surface 内，但不能让整个 surface 消失。

机器证明边界保持诚实：registry 证明每个 surface 引用了真实生效的 rule 或专项测试入口；`pkg_no_internal_imports` 等需要锁定完整语义的规则同时绑定行为回归 guard。专项 guard 的具体语义仍由该测试执行结果证明，不把“文件存在”夸大成行为证明。

### 4.3 Archtest 规则地图与 README 统计

新增可测试命令 `scripts/archtestmap`：

- 默认刷新模式。
- `--check` 只读比较并在漂移时非零退出。
- 只从 `DefaultBackendBoundaryRegistry()` 生成 `docs/doc/codemap/13-archtest-boundaries.md`。
- 规则地图输出：owner、canonical rule、kind、文件范围、allow/deny/scope/exception 摘要，以及每个后端顶层目录的 rule/guard 归属。
- 使用当前 Go build context 选择源文件，再以 AST 镜像 `cmd/go` 的顶层 Test 签名规则统计 `internal/archtest/**/*_test.go` 中的测试函数和包含这些测试的文件数；README 文案明确标注为源码 AST 统计，不再声称等同于 `go test -list`。
- README 只允许替换唯一 Architecture Tests 表格行内的固定 inline marker 区间；marker 缺失、重复、跨行、顺序错误或出现在其他行必须失败，不得猜测表格位置或重建整张表：

```markdown
| Architecture Tests | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: ...<!-- END GENERATED ARCHTEST STATS --> |
```

- 输出按 surface、rule ID、guard path 排序，禁止依赖 map 迭代顺序。
- 生成内容不写入当前时间、绝对路径或其他每次运行变化的字段。
- 规则地图和 README 先全部预读并写入同目录临时文件，再用原子替换提交；任一提交失败必须回滚已替换目标，禁止留下跨产物半刷新状态。

Makefile 接线：

```text
make codemap-refresh
  -> make archtest-map-refresh
  -> go run scripts/codemap_index.go

make codemap-check
  -> make archtest-map-check
  -> go run scripts/codemap_index.go --check
```

`archtest-map-refresh` 和 `archtest-map-check` 分别封装 `go run ./scripts/archtestmap` 与 `go run ./scripts/archtestmap --check`。这样新增的第 13 卷先生成，再被 `ai-index.json` 收录；check 模式全程不写工作区。

## 5. 数据流与失败语义

```text
single BackendBoundaryRegistry + source tree
  -> registry rule/guard/surface validators
  -> boundary evaluation with import positions
  -> scripts/archtestmap renderer
  -> 13-archtest-boundaries.md + README Architecture Tests row
  -> codemap_index.go
  -> ai-index.json
```

任何输入缺失、未知 ID、未治理目录、无效 guard/test 入口、解析失败、README marker 异常或生成物漂移都必须立即失败。生成器不得写默认空表、吞错继续或自动删除受管 marker 之外的内容。

## 6. 测试设计

按 TDD 顺序实现：

1. 先增加违规夹具，精确断言单行 import 与 import block 的 `line:column`；确认旧实现失败。
2. 增加统一 registry 校验测试：当前树完整、临时新增顶层目录会失败、未知 rule/guard、缺失测试入口、孤儿 guard、重复项和空机制会失败。
3. 增加生成器单测：Markdown 稳定排序、README marker 窄替换、marker 缺失或重复失败、`--check` 漂移失败、刷新后通过。
4. 运行 focused tests，再运行 `./scripts/test_with_guard.sh ./internal/archtest -count=1`、`./scripts/test_with_guard.sh ./scripts/archtestmap -count=1`、`make archtest-map-refresh`、`make archtest-map-check`、`make codemap-refresh`、`make codemap-check`、`make guard`。
5. 对所有修改的 Go 文件运行 LSP diagnostics；Warning、Information、Hint 同样必须清零。

## 7. 验收标准

- 已知边界违规输出含精确 `file:line:column`。
- 当前所有后端顶层 Go 目录恰好被治理清单覆盖，新增未登记目录会使测试失败。
- 每个治理项至少指向 canonical rule 或带稳定 ID、真实文件和真实测试入口的专项 guard。
- Archtest 规则地图和 README 统计均由同一命令生成并支持只读 check。
- README 只修改表格单元格内的固定 inline marker 区间，marker 异常立即失败。
- 新地图被 codemap README/ai-index 正常收录。
- evaluator 文件未拆分。
- archtest、生成器测试、codemap check、make guard、LSP diagnostics 全部通过。
