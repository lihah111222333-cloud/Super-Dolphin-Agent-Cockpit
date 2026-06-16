# Super-Dolphin 架构与守卫整改计划

日期: 2026-06-16
范围: `/Volumes/word/github/wuji-banbot/Super-Dolphin`
目标: 让当前 Go 代码、架构文档、注释、日志与项目守卫重新一致；不恢复已删除的旧测试文件，改为新增可维护测试并用守卫基线阻止新增债务。

## 当前审计结论

当前项目在宏观上有清晰分层: `cmd/*` 为入口和侧车，`internal/module/*` 承载业务模块，`internal/store/*` 做持久化防腐层，`internal/platform/*` 做基础设施，`internal/{mcpserver,provider,ui}` 做外部适配层。现有文档 `docs/契约/onion-architecture-convention.md` 已说明这些层级和依赖方向。

初始审计时，当前工作区不符合守卫要求，且不能作为可交付状态:

- 工作区很脏: `git status --porcelain=v1` 统计为 `D 516`、`A 3`、`M 4`、` D 1452`、` M 135`。
- 当前文件树下没有任何 Go 测试文件: `rg --files -g '*_test.go' | wc -l` 输出 `0`。
- 已跟踪但当前删除的 Go 测试文件数量为 `1166`: `git diff --name-only -- '*_test.go' | wc -l` 输出 `1166`。
- `go test ./...` 通过，但所有包均为 `[no test files]`，只能证明编译通过，不能证明行为。
- `go vet ./...` 通过。
- `go build ./cmd/...` 通过，但 macOS 链接阶段有新版对象文件警告。
- `make guard-change` 失败，首先失败在源码尺寸检查。
- 单独运行后续守卫脚本显示:
  - `check_go_size.py`: `188` 项违规。
  - `check_go_comments.py`: `1362` 项违规。
  - `check_go_boundaries.py`: `162` 项违规。
  - `check_go_ast_rules.py`: `532` 项违规。

按最新要求，本次不恢复旧测试文件，而是先完成第一批可验证修复:

- 新增 `internal/archtest/go_project_guard_scripts_test.go`，覆盖边界守卫、AST 守卫和尺寸守卫的关键行为。
- 为守卫脚本增加基线机制，当前历史债务进入精确文本基线；基线只屏蔽已有违规，新增未入基线违规仍会失败。
- 调整 `cmd/mcp-lsp/*`、`cmd/mcp-orch/*` 的侧车库包识别，避免把现有侧车内部依赖误判为入口层违规。
- 调整 AST 守卫，使 `pkg/logger` 作为当前日志实现包被允许，并把 `scripts/*.go`、`docs/security/internal/*.go` 识别为 command 工具路径。
- 调整 migration 守卫，为既有迁移缺少 goose marker 和破坏性 DDL 标记的问题建立精确基线。
- 当前文件树已有 1 个新的 Go 测试文件，`make guard-change` 已能通过。
- `.agents/` 被 `.gitignore` 忽略，但 `Makefile` 会直接执行 `.agents/skills/...` 下的守卫脚本；后续若需要团队共享这套守卫修复，应明确是提交 `.agents`、迁入可跟踪目录，还是通过技能安装流程分发。

## 主要问题

### 1. 原测试被清空，但本轮不恢复旧测试

当前工作区把全部 `*_test.go` 从文件树中删除，导致 `go test ./...` 只能做编译检查。任何行为变更、架构迁移、日志修复都缺少回归保护。

处理原则:

- 按最新要求，不恢复这 1166 个旧测试文件。
- 新增测试从守卫脚本开始，因为守卫是当前架构整改的入口条件。
- 后续每处理一个业务包或侧车包，都新增该包的最小必要测试；不以“恢复旧数量”为目标，而以覆盖关键契约、迁移边界和回归风险为目标。

### 2. 守卫与现有代码布局存在系统性冲突

当前 `check_go_boundaries.py` 使用默认 hexagonal 模型:

- 所有 `cmd/**` 都被判定为 `cmd` 层。
- `cmd` 层只允许导入 `bootstrap` 或 `other`。
- 仓库实际把大量库包放在 `cmd/mcp-lsp/*`、`cmd/mcp-orch/*` 下，例如 `manager`、`multilsp`、`tools`、`store`、`orchestration`。

这会把 `cmd/mcp-lsp/tools` 导入 `cmd/mcp-lsp/manager`、`cmd/mcp-orch/orchestration` 导入 `cmd/mcp-orch/store/taskdag` 等正常内部依赖判定为违规。

处理原则:

- 不删除守卫，也不把所有违规改成宽泛 allowlist。
- 本次采用过渡路径: `cmd/mcp-lsp/*`、`cmd/mcp-orch/*` 的非根包暂时识别为 sidecar internal layer，根入口仍保持 command layer。
- 后续推荐路径仍是把非 `main` 库包逐步迁入 `internal/sidecar/lsp/*`、`internal/sidecar/orch/*` 或更具体的业务目录，只让 `cmd/<binary>` 保持薄入口。
- 需要补充 `docs/architecture/package-map.md` 与 `docs/architecture/dependency-rules.md`，把这条过渡规则和迁移期限写清楚。

### 3. 源码尺寸失控

`make guard-change` 第一处失败是 `check_go_size.py`。当前有 188 项尺寸违规，其中包括:

- 单文件超过 400 行，例如 `cmd/mcp-orch/orchestration/dag.go`、`cmd/mcp-lsp/multilsp/manager_lifecycle.go`、`internal/module/memory/ui_rpc.go`、`internal/provider/codexapp/support.go`。
- 包内 Go 文件数超过 30，例如 `cmd/mcp-orch/orchestration`、`internal/module/memory`、`internal/module/thread`、`internal/provider/claudecli`。

处理原则:

- 本次先用尺寸基线把历史债务冻结，保证新增超限文件不会绕过守卫。
- 后续不做纯机械拆分。按职责拆出小文件或子包。
- 先处理正在阻塞架构迁移的包: `cmd/mcp-lsp/*`、`cmd/mcp-orch/*`。
- 再处理核心业务大包: `internal/module/{memory,thread,skill,turn,prompt}`。
- 最后处理 provider/platform/store 中的大文件。

### 4. 注释规范大面积不达标

当前只有 3 个 `doc.go`，但项目地图显示 198 个 Go 包。注释守卫发现 1362 项问题，主要是:

- 大量包缺少包注释。
- 大量 exported type/function/const/var 缺少以标识符开头的 Go doc。
- 多个长函数缺少意图注释。

处理原则:

- 本次先用注释基线冻结历史债务，避免已有 1371 项历史问题继续阻塞第一批守卫修复。
- 后续每个非平凡包新增或补齐 `doc.go`，说明职责、允许依赖、禁止职责。
- exported DTO/protocol/contract 类型要写契约含义，不写重复语法的空注释。
- 对大函数，优先通过拆分降低长度；确实保留时增加意图注释。

### 5. 日志抽象与 AST 守卫不一致

技能规范要求通过平台日志抽象使用日志。当前代码实际使用 `pkg/logger` 包装 `log/slog`，而仓库没有 `internal/platform/logging` 包。AST 守卫因此报告大量 direct `slog.`、`fmt.Print*`、`os.Exit`、`panic`、错误吞掉和 `%v` 包装问题。

处理原则:

- 本次先把 `pkg/logger` 识别为当前日志实现包，避免守卫误报自身日志封装。
- 长期可以新建 `internal/platform/logging`，把当前 `pkg/logger` 的核心实现迁入平台日志包。
- 若迁移，则保留 `pkg/logger` 为兼容薄层，逐步把内部代码导入改成 `internal/platform/logging`。
- 只允许 `cmd/*`、`internal/bootstrap/*`、`internal/platform/logging/*` 直接接触 `slog` 或进程退出。
- 对 `scripts/*.go` 和 `docs/security/internal/*.go` 中的 CLI 工具，本次已在 AST 守卫中明确识别为 command layer；后续如要收紧目录，可再迁到 `cmd/dev/*`。

## 修改计划

### 已完成: 第一批守卫修复

已修改:

- `.agents/skills/guarding-go-projects/scripts/check_go_size.py`
- `.agents/skills/guarding-go-projects/scripts/check_go_comments.py`
- `.agents/skills/guarding-go-projects/scripts/check_go_boundaries.py`
- `.agents/skills/guarding-go-projects/scripts/check_go_ast_rules.py`
- `.agents/skills/guarding-go-projects/scripts/check_migrations.py`
- `.agents/skills/guarding-go-projects/baselines/*.txt`
- `internal/archtest/go_project_guard_scripts_test.go`

完成内容:

1. 新增守卫脚本测试，不恢复旧测试文件。
2. `check_go_size.py`、`check_go_comments.py`、`check_go_boundaries.py`、`check_go_ast_rules.py`、`check_migrations.py` 支持基线文件。
3. 默认基线路径放在 `.agents/skills/guarding-go-projects/baselines/`，当前基线规模为:
   - `check_go_size.txt`: 188 项。
   - `check_go_comments.txt`: 1371 项。
   - `check_go_boundaries.txt`: 5 项。
   - `check_go_ast_rules.txt`: 371 项。
   - `check_migrations.txt`: 227 项。
4. `cmd/mcp-lsp/*`、`cmd/mcp-orch/*` 的非根包识别为 sidecar internal layer。
5. `pkg/logger` 识别为当前日志实现包；`scripts/*.go` 和 `docs/security/internal/*.go` 识别为 command 工具路径。
6. `go mod tidy` 结果已同步到 `go.mod`、`go.sum`，移除当前文件树不再引用的旧测试依赖。

当前验收:

- `go test ./internal/archtest -run 'TestGo(Boundary|AST|Size)Guard|TestMigrationGuardBaseline' -count=1` 通过。
- 五个守卫脚本单独运行均通过。
- `make guard-change` 通过。

注意: 这些结果表示守卫已经可以执行且能阻止新增违规，不表示 2162 项历史债务已经完成重构、补注释或迁移修复。

### 阶段 1: 建立权威架构文档

新增:

- `docs/architecture/package-map.md`
- `docs/architecture/dependency-rules.md`

内容必须覆盖:

- `cmd/agent-terminal`、`cmd/mcp-lsp`、`cmd/mcp-orch`、`cmd/mcp-ida` 的入口职责。
- `internal/app` 作为桌面后端 Fx 总装配点的职责。
- `internal/module/*` 的业务边界。
- `internal/store/*` 的持久化防腐职责。
- `internal/platform/*` 的基础设施边界。
- `internal/mcpserver/*`、`internal/provider/*`、`internal/ui/*` 的适配层职责。
- `pkg/*` 是否仍允许存在；如果存在，必须说明外部稳定 API 边界。
- `cmd/mcp-lsp/*` 和 `cmd/mcp-orch/*` 中非入口包的过渡规则、迁移策略和移除基线的条件。

验收:

- 文档和 `docs/契约/onion-architecture-convention.md` 不冲突。
- 文档能解释当前守卫规则中的每一条层级判断。
- 文档明确 `.agents/` 守卫脚本的分发方式，避免只有本地机器具备修复后的守卫。

### 阶段 2: 基线收敛规则

每次后续整改必须遵守:

1. 不新增基线项，除非是先记录历史债务并附带对应整改 issue 或计划。
2. 修改某个包时，优先删除该包对应的基线项。
3. 基线删除后必须运行对应脚本确认仍通过。
4. 不允许把新增违规写入基线来绕过守卫。

验收:

- `python3 .agents/skills/guarding-go-projects/scripts/check_go_size.py .`
- `python3 .agents/skills/guarding-go-projects/scripts/check_go_comments.py .`
- `python3 .agents/skills/guarding-go-projects/scripts/check_go_boundaries.py .`
- `python3 .agents/skills/guarding-go-projects/scripts/check_go_ast_rules.py .`
- `python3 .agents/skills/guarding-go-projects/scripts/check_migrations.py .`

### 阶段 3: 新增测试策略

不恢复旧 `*_test.go`。新增测试按风险排序:

1. 守卫脚本测试: 已新增 `internal/archtest/go_project_guard_scripts_test.go`。
2. LSP sidecar 行为测试: manager、multilsp、tools、protocol。
3. Orchestration/DAG/wakeup 流程测试。
4. Store sqlite 集成测试。
5. Provider session、recovery、toolbridge 测试。
6. UI/Wails RPC 边界测试。

验收:

- `go test ./...` 至少包含新增测试包，不再是全仓只有编译检查。
- 每次迁移或拆分一个包，新增该包的最小必要测试。
- 对并发、worker、生命周期包运行 `go test -race ./...` 或更窄的 race 命令。

### 阶段 4: 源码尺寸分批拆分

优先拆分路径:

1. LSP sidecar:
   - `cmd/mcp-lsp/multilsp/*.go`
   - `cmd/mcp-lsp/tools/*.go`
   - `cmd/mcp-lsp/search/*.go`
   - `cmd/mcp-lsp/runtime.go`
2. Orchestration sidecar:
   - `cmd/mcp-orch/orchestration/*.go`
   - `cmd/mcp-orch/tools/*.go`
   - `cmd/mcp-orch/store/taskdag/*.go`
3. 核心模块:
   - `internal/module/memory/*.go`
   - `internal/module/thread/*.go`
   - `internal/module/skill/*.go`
   - `internal/module/prompt/*.go`
   - `internal/module/turn/*.go`
4. Provider/platform/store:
   - `internal/provider/codexapp/*.go`
   - `internal/provider/claudecli/*.go`
   - `internal/platform/toolbridge/*.go`
   - `internal/store/*/store.go`

拆分规则:

- 每次只处理一个包。
- 先新增该包测试。
- 以职责命名文件，例如 `*_validation.go`、`*_lifecycle.go`、`*_mapping.go`、`*_worker.go`、`*_persistence.go`。
- 每个文件压到 400 行以下。
- 每个包压到 30 个 Go 文件以内；超过时拆子包，而不是继续横向加文件。

验收:

- `python3 .agents/skills/guarding-go-projects/scripts/check_go_size.py .` 通过。
- `check_go_size.txt` 对应条目单调下降，最终为 0。

### 阶段 5: 补齐注释和包文档

新增或修改:

- 每个非平凡包的 `doc.go`。
- 所有 exported 类型、函数、方法、常量、变量的 Go doc。

优先级:

1. `internal/contract`、`internal/dto`、`cmd/mcp-lsp/protocol`，这些是契约面，注释应先补齐。
2. `internal/module/*`，补业务不变量、幂等、事务和副作用说明。
3. `internal/platform/*`，补生命周期、并发、资源关闭、日志/指标契约。
4. `cmd/*` 和 `scripts`，补 command 入口说明。

验收:

- `python3 .agents/skills/guarding-go-projects/scripts/check_go_comments.py .` 通过。
- `check_go_comments.txt` 对应条目单调下降，最终为 0。
- 不使用批量空注释；每条注释说明职责、契约或限制。

### 阶段 6: 修复 AST 规则问题

按类别处理:

1. `%v` 包装 error 改为 `%w`，保留 `errors.Is/As` 能力。
2. `panic` 改为显式错误，只有启动期或不可达不变量允许保留，并写明原因。
3. 内部层 `log and return err` 改成只返回，由边界统一记录。
4. `return nil` 吞掉错误的位置改成返回错误，或写明 best-effort 并记录安全降级。
5. `fmt.Print*`、`os.Exit` 工具移动到 command 层。

验收:

- `python3 .agents/skills/guarding-go-projects/scripts/check_go_ast_rules.py .` 通过。
- `check_go_ast_rules.txt` 对应条目单调下降，最终为 0。

### 阶段 7: 日志抽象收敛

可选迁移路径:

1. 保持 `pkg/logger` 作为对外稳定日志包，并在架构文档中说明它是例外的稳定 API。
2. 或新增 `internal/platform/logging`，把实现迁入平台层，`pkg/logger` 只保留兼容转发。
3. 无论选择哪条路径，都要补充日志字段、脱敏和边界记录规范。

验收:

- `pkg/logger` 或 `internal/platform/logging` 的实现边界有文档说明。
- 日志实现包之外不直接使用 `slog.`。
- 对外边界日志包含稳定字段，并确认不输出 token、密钥、私钥和完整敏感 payload。

### 阶段 8: 全量守卫收口

最终命令:

```bash
make project-map
go test ./...
go vet ./...
go build ./cmd/...
make guard-change
make guard-commit
```

合并、发布或部署前再运行:

```bash
make guard-release
```

完成标准:

- `.project-map/` 仍被 git 忽略且未暂存。
- `make guard-change` 通过。
- `make guard-commit` 通过。
- 新增测试策略已落地，且不依赖恢复旧测试文件。
- 架构文档、守卫脚本、源码依赖方向三者一致。
