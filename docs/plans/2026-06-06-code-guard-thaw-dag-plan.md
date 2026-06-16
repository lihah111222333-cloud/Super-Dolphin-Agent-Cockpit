# 代码守卫 104 个生产冻结文件解冻审查 DAG 计划

创建日期：2026-06-06
适用范围：`internal/archtest/baseline.json` 中 104 个生产冻结文件
非范围：`internal/archtest/baseline_test.json` 中 47 个测试冻结文件，除非本轮审查发现生产修复必须同步测试

## 背景与领导要求落地

当前 104 个生产文件被 `internal/archtest/baseline.json` 冻结。冻结的作用是让历史违规暂时不阻断 CI，同时由 ratchet 防止继续恶化；这不是豁免，也不是允许继续新增同类问题。

本轮从“按 19 维度审查”开始。所有冻结项必须先审查、再判定真违规或假阳性、再决定是否进入修复。禁止直接运行 `go run scripts/code_size_guard.go --freeze` 重新冻结来绕过问题；禁止放宽阈值；禁止未审查就删除 baseline。

目标是分阶段把 104 个生产冻结文件解冻：每批必须 19 维度全绿、假阳性复核完成、守卫预防方案明确，然后才允许进入修复和 baseline 收缩/毕业。

## 当前静态基线

从 `internal/archtest/baseline.json` 读取到：

- 生产冻结文件：104 个。
- 测试冻结文件：47 个，位于 `internal/archtest/baseline_test.json`。
- 当前生产冻结中，按硬阈值可见的主要历史问题分布：
  - `global_vars > 0`：54 个文件。
  - `todo_count > 0`：25 个文件。
  - `panic_count > 0`：14 个文件。
  - `empty_funcs > 0`：8 个文件。
  - `has_init == true`：6 个文件。
  - `naked_returns > 0`：2 个文件。
  - `lines > 600`：1 个文件。
- 冻结集中目录：
  - `internal/sidecar/orch/orchestration`：9 个文件。
  - `internal/module/skill`：7 个文件。
  - `internal/provider/claudecli`：7 个文件。
  - `internal/provider/codexapp`：6 个文件。
  - `internal/module/prompt`：5 个文件。
  - `internal/ui/wails`：5 个文件。

本机执行守卫前置条件未满足：`./scripts/test_with_guard.sh --guard-only` 因未找到真实 Go 二进制失败。执行前必须安装 Go 或设置 `REAL_GO_BIN=/absolute/path/to/go`。

## 19 维度审查清单

每个冻结文件必须逐项给出 `PASS / TRUE_VIOLATION / FALSE_POSITIVE / N/A`，并附证据。`FALSE_POSITIVE` 必须说明是测量误差、生成代码误识别、合法不可替代模式，还是守卫规则边界问题。

| # | 维度 | 现有守卫连接点 | 判定重点 |
| --- | --- | --- | --- |
| D01 | 文件有效行数 | `lines`, `MaxFileLines` | 是否应拆分单职责文件，是否存在生成/资产类误判 |
| D02 | 函数有效行数 | `max_func_len`, `MaxFuncLines` | 是否可提取纯 helper，是否会破坏事务/锁/上下文语义 |
| D03 | 嵌套深度 | `max_nesting`, `MaxNestingDepth` | 是否可 guard clause / 状态机化，是否保留错误路径 |
| D04 | 圈复杂度 | `max_complexity`, `MaxCCComplexity` | 是否可拆策略表/分支函数，是否需要新增回归测试 |
| D05 | 标识符下划线 | `max_underscore`, `MaxUnderscores` | 是否是 wire/schema/generated 约定；真违规才改名 |
| D06 | 函数参数数 | `max_params` ratchet | 是否需要 options struct，是否影响公共 API |
| D07 | 返回值数 | `max_returns` ratchet | 是否需要结果 struct，是否影响调用侧可读性 |
| D08 | struct 字段数 | `max_struct_fields` ratchet | 是否是 DTO/配置结构；DTO 假阳性需记录 |
| D09 | 包级全局变量 | `global_vars` 零容忍 | 是否是不可变声明、哨兵错误、Fx module，还是可变状态 |
| D10 | `init()` | `has_init` | 是否有启动顺序、副作用、测试污染；合法平台初始化需说明 |
| D11 | `panic()` | `panic_count` 零容忍 | 是否可返回 error；不可恢复 fatal 是否在边界层 |
| D12 | 裸返回 | `naked_returns` 零容忍 | 是否影响可读性和错误路径；通常直接修 |
| D13 | 空函数 | `empty_funcs` 零容忍 | 是否是接口占位/平台 stub；真占位需删除或补实现 |
| D14 | TODO/FIXME/HACK/XXX | `todo_count` 零容忍 | 是否是未完成风险；保留必须转 issue/ADR 或改为非 TODO 注释 |
| D15 | 裸 goroutine | `naked_goroutines` 零容忍 | 是否必须走 `safego` / run.Group / lifecycle owner |
| D16 | 包文件数 | `ViolationPackageCount`, `freeze_registry` | 是否应拆子包，是否已有显式 freeze 元数据 |
| D17 | 包有效行数 | `ViolationPackageLines`, `freeze_registry` | 是否域边界过粗，是否需要迁移计划 |
| D18 | freeze registry 健康 | `freeze_registry.go` dead key/auto shrink | 显式 freeze 是否有 owner/reason/remove_when，是否可删除或收缩 |
| D19 | 假阳性与守卫预防 | 审查结论 -> 新/改 archtest | 历史 fix 范式能否前移成 guard，是否会扫出同类违规 |

## DAG 总览

```text
N0-preflight-inventory
  -> N1-review-dimension-rubric
  -> N2-batch-classification
  -> N3A-core-orchestration-review
  -> N3B-provider-review
  -> N3C-module-review
  -> N3D-platform-ui-tools-review
  -> N4-false-positive-arbitration
  -> N5-guard-integration-design
  -> N6-fix-batch-plan
  -> N7-batch-fix-execution
  -> N8-batch-verification-and-baseline-shrink
  -> N9-final-synthesis
```

依赖规则：

- `N0` 必须先完成，确认 Go 环境、当前 baseline 文件数、当前守卫输出、git 工作区状态。
- `N1` 冻结 19 维度 rubric 后，任何 worker 不得自定义口径。
- `N2` 把 104 文件按 owner/目录/主违规维度分批，每批建议 8-15 个文件。
- `N3A` 到 `N3D` 可并行，只做审查和证据收集，不改代码。
- `N4` 仲裁所有 `FALSE_POSITIVE`，没有仲裁结论不得修代码。
- `N5` 在修复前判断哪些历史 fix 范式应进入守卫。能守卫化的，优先补 `internal/archtest` 测试和规则。
- `N6` 输出具体 fix 批次计划，包含文件、风险、测试、预计 baseline 删除/收缩。
- `N7` 才允许改代码。每批只处理已审查全绿的文件。
- `N8` 跑守卫和影响面测试，确认 baseline 自动收缩或毕业。
- `N9` 汇总剩余冻结、真违规、假阳性、已集成守卫、无法自动守卫化项。

## 节点定义

### N0 preflight-inventory

产出：

- `baseline.json` 文件数必须为 104。
- `baseline_test.json` 文件数记录但不纳入本轮默认修复。
- 当前守卫命令结果：
  - `./scripts/test_with_guard.sh --guard-only`
  - `./scripts/test_with_guard.sh ./internal/archtest/... -count=1`
- 当前 Go 环境；若无 Go，状态为 `BLOCKED_ENV`，不能进入执行。

### N1 review-dimension-rubric

产出：

- 本文 19 维度清单被复核并冻结。
- 每个维度的 `FALSE_POSITIVE` 允许条件写入审查模板。
- 明确 `D19`：任何 fix 如果能抽象为 AST/文本/依赖方向/registry 规则，必须进入守卫候选。

### N2 batch-classification

建议初始分批：

| 批次 | 范围 | 文件数 | 说明 |
| --- | --- | ---: | --- |
| B1 | `internal/sidecar/orch/orchestration/**` | 12 | 编排核心，高风险，串行修；含 `nodeevents` / `nodeexec` / `processctl` 子目录冻结文件 |
| B2 | `internal/provider/claudecli/**` + `internal/provider/codexapp/**` | 13 | provider 生命周期/进程/会话，需强测试 |
| B3 | `internal/module/skill/**` + `internal/module/prompt/**` | 12 | 业务模块，需契约和回归测试 |
| B4 | `internal/module/thread/**` + `internal/module/memory/**` | 7 | 状态/记忆/线程，修复前先补测试 |
| B5 | `internal/platform/**` | 15 | 平台边界，重点全局状态、panic、init |
| B6 | `cmd/mcp-lsp/**` + `internal/sidecar/orch/tools/**` + 其他 cmd | 18 | 工具入口和 schema，注意 wire 兼容 |
| B7 | `internal/app/**` + `internal/ui/wails/**` | 9 | Fx/Wails wiring，重点启动顺序和平台差异 |
| B8 | 其余低聚合文件 | 18 | 小批滚动，避免大改 |

实际批次以 N2 输出为准；同一批不得跨 owner 同时改共享 wiring 文件。

### N3 domain review

每个文件输出一行审查记录：

```text
path:
  batch:
  owner:
  dimensions: D01=PASS, ...
  primary_findings:
  true_violation:
  false_positive:
  guard_candidate:
  required_tests:
  fix_allowed: yes/no
```

### N4 false-positive-arbitration

仲裁规则：

- 假阳性不是“现在不想修”。只有守卫测量口径与真实风险不一致时才可判定。
- 如果是假阳性但会持续噪声，必须给出守卫修正规格和测试。
- 如果是假阳性来自 DTO / generated / platform stub，优先做精确豁免，不做全局放宽。
- 仲裁不通过的项回到真违规队列。

### N5 guard-integration-design

对每类真违规问三个问题：

1. 是否已被当前 guard 覆盖，只需要修历史 baseline。
2. 是否是历史 fix 中反复出现的范式，可以新增 archtest 预防。
3. 是否暂时不能自动守卫化，只能用审查模板或局部测试预防。

可守卫化示例：

- 新增 `panic()` 使用场景的边界豁免必须精确到目录/函数。
- 裸 goroutine 必须走 `safego` 或生命周期 owner。
- `TODO` 注释如果是长期契约，必须改成 ADR/issue 引用，不使用 TODO token。
- 可变全局状态必须禁止；不可变声明需通过 AST 精确识别。

### N6 fix-batch-plan

每批进入修复前必须满足：

- 19 维度审查记录完整。
- 真违规/假阳性仲裁完成。
- 守卫集成项已建任务或随批实现。
- 影响面测试命令列清楚。
- 预计 baseline 操作是“自动收缩/毕业”，不是手工放宽。

### N7 batch-fix-execution

执行纪律：

- 每次只修一个批次。
- 每改完 Go 文件，先跑单文件守卫：

```bash
./scripts/test_with_guard.sh <file.go>
```

- 批次完成后跑影响包：

```bash
./scripts/test_with_guard.sh <affected packages> -count=1
```

- 修改 `internal/archtest`、baseline、freeze registry 时，必须补跑：

```bash
./scripts/test_with_guard.sh ./internal/archtest/... -count=1
```

### N8 batch-verification-and-baseline-shrink

验收命令：

```bash
./scripts/test_with_guard.sh --guard-only
```

验收条件：

- 守卫通过。
- baseline 只允许收缩、毕业或删除已修复文件。
- 不允许新增冻结文件。
- `git diff internal/archtest/baseline.json` 必须能对应本批已修复文件。
- 若新增守卫，必须有红绿证据和 archtest 测试。

### N9 final-synthesis

最终报告必须包含：

- 104 个文件的状态分布：已毕业 / 已收缩 / 真违规待修 / 假阳性 / 阻塞。
- 已新增或修改的守卫规则列表。
- 未能守卫化的 fix 范式和原因。
- 剩余风险和下一轮批次。

## 全绿定义

“全绿”不是所有文件立即修完，而是每个阶段的 gate 全绿：

- 审查全绿：104 个文件都有 19 维度记录，且无未仲裁项。
- 计划全绿：每个真违规都有 fix 批次、测试和守卫预防判断。
- 执行全绿：当前批次守卫/测试通过，baseline 只收缩不放宽。
- 最终全绿：生产 baseline 104 个文件全部毕业，或剩余项全部有经仲裁批准的精确假阳性守卫豁免。

## 当前阻塞

本机缺少可用 Go 二进制，当前无法执行仓库守卫。恢复方式：

```bash
export REAL_GO_BIN=/absolute/path/to/go
./scripts/test_with_guard.sh --guard-only
```

该阻塞解除前，只能完成文档计划、静态 baseline 分类和审查模板准备，不能进入 N7 修复执行。
