# Super-Dolphin 依赖规则

日期: 2026-06-17

本文把架构规则落到可执行守卫和迁移约束上。它不替代 `docs/契约/onion-architecture-convention.md`，而是补齐当前仓库的 sidecar 层、日志例外和基线收敛规则。

## 规则来源优先级

1. 源码、同包测试、`make guard-change` / `make guard-commit` 输出。
2. 本文和 `docs/架构决策/架构边界/package-map.md`。
3. `docs/契约/*.md` 中的长期契约。
4. `docs/doc/codemap/*` 和 `.project-map/*` 的索引。
5. `docs/superpowers/plans/*`、`docs/历史归档/归档材料/*` 等历史材料。

历史计划只能解释上下文，不能覆盖当前守卫事实。

## 可执行守卫对应关系

| 规则面 | 当前守卫 | 收敛要求 |
| --- | --- | --- |
| 包依赖方向 | `check_go_boundaries.py` | 修包时同步减少对应 boundary 基线 |
| 直接日志、panic、进程退出、错误包装 | `check_go_ast_rules.py` | 内部层只返回错误，边界统一记录日志 |
| 注释和包文档 | `check_go_comments.py` | 修改包时补 `doc.go` 和导出 API Go doc |
| 文件尺寸和包文件数 | `check_go_size.py` | 每次只拆一个包，文件不继续横向膨胀 |
| 迁移安全 | `check_migrations.py` | 迁移新增项必须有 marker、回滚和破坏性说明 |

## 层级判定

### Command

`cmd/<binary>` 是进程入口。根入口可以做启动参数、信号、Fx wiring、最终 `run` 调用和进程退出。根入口不写业务规则、不访问数据库明细、不直接调用 provider 私有实现。

当前允许的根入口:

- `cmd/agent-terminal`
- `cmd/mcp-lsp`
- `cmd/mcp-orch`
- `cmd/mcp-ida`
- 打包、更新、发布 manifest 这类独立 CLI

### Sidecar 层

`internal/sidecar/lsp/*` 和 `internal/sidecar/orch/*` 是独立 MCP sidecar 的内部实现层。`cmd/mcp-lsp`、`cmd/mcp-orch` 只保留根入口、启动参数、信号处理和 Fx wiring。

允许:

- sidecar 内部包之间相互调用。
- sidecar 调用 `internal/platform/*`、`internal/contract`、`internal/dto`、`pkg/logger`。
- sidecar 保留自己独立的 store、protocol、tool handler。

禁止:

- LSP sidecar 调 Orchestration sidecar 内部包，反之亦然。
- `cmd/mcp-lsp/*`、`cmd/mcp-orch/*` 继续新增或保留非根 Go 包。
- sidecar 调用桌面 UI 内部状态。
- sidecar 绕过公开契约调用 provider 私有实现。
- sidecar 直接依赖宿主 `internal/store/*` 具体实现。
- 为了消除 import cycle 新增 `common`、`utils`、`shared` 这类泛包。

守卫要求:

- `check_go_boundaries.py` 会拒绝 `cmd/mcp-lsp/*`、`cmd/mcp-orch/*` 非根 Go 包。
- `check_go_ast_rules.py` 把 `internal/sidecar/*` 识别为 sidecar 边界层，允许进程运行日志和边界级 best-effort 清理；业务层、平台层和宿主 store 仍按内部层规则收紧。

### Application Assembly

`internal/app` 是桌面后端总装配点。它可以聚合 platform、store、module、provider、mcpserver、ui，但不能承载业务规则或协议解析。

### Business Modules

`internal/module/*` 是当前业务层。新增代码应按职责继续向 use case、纯规则、port 拆分，避免继续把 service、DTO、worker、store adapter 逻辑堆进同一个包。上下文内部的纯规则子包（如 `internal/module/thread/promptrouting`）只处理已加载契约对象，不回接 store、provider、UI、日志或配置。

业务模块访问持久化只能通过 `internal/contract` port，由 `internal/app` 或对应装配层注入具体 store 实现。不得在用例逻辑里新增对 `internal/store/*` 的直接依赖；如缺少 port，应先抽出最小业务接口再接入 store adapter。

业务层禁止:

- import `cmd/*`
- import `internal/provider/*`
- import `internal/mcpserver/*`
- import `internal/ui/*`
- 直接依赖 SQL/sqlc 类型或数据库驱动
- 在普通用例里直接 `slog.`、`fmt.Print*`、`os.Exit` 或 `panic`

### Store

`internal/store/*` 是持久化防腐层。store 负责映射和资源访问，不负责跨业务流程编排，不创建 handler、service 或 use case。

允许的非数据库平台例外仅限文件型持久化适配器: `internal/store/sharedfile` 可依赖 `internal/platform/sharedfilefs`、`internal/platform/sharedfilepath`、`internal/platform/sharedfilegitignore` 和装配所需的 `internal/platform/config`。其他 store 包不得新增非 `internal/platform/db` 的平台依赖；如需要，应先抽为 contract port 或移动到明确的 platform 子包。

### Platform

`internal/platform/*` 是技术基础设施。platform 可以依赖第三方基础设施库，但不能反向依赖业务模块、provider、MCP server 或 UI。

### Adapter

`internal/mcpserver/*`、`internal/provider/*`、`internal/ui/*` 是外部适配层。适配层负责协议和边界日志，可以调用内部公开接口，但同级适配器之间不能偷用私有实现。

## 日志规则

当前业务日志入口是 `internal/platform/logging`。业务模块不得直接依赖 `pkg/logger`；`pkg/logger` 仅作为入口、sidecar、适配层和日志平台 facade 的兼容入口，也是 AST 守卫允许直接使用 `slog` 的例外包。

日志必须遵守:

- 业务内部层返回错误，不重复 log and return。
- 进程边界、协议边界、worker owner 边界记录结果日志。
- 结构化字段优先使用 `component`、`operation`、`request_id`、`trace_id`、安全业务 ID。
- 不记录 token、密钥、私钥、完整 credential、完整敏感 payload。
- 二进制路径、语言 ID、工具名、状态码可以记录，但不要记录用户文件内容。

## 注释规则

每个非平凡 Go 包需要 `doc.go`。包注释必须说明:

- 包拥有什么职责。
- 允许依赖哪些层。
- 明确不应该做什么。

所有导出的 type、func、method、const、var 必须有以标识符开头的 Go doc。注释写契约、限制、生命周期、并发所有权、错误语义或安全边界，不写重复语法的空话。

## 基线收敛规则

守卫基线只用于冻结历史债务，不能用于接受新增债务。

每次整改必须执行:

1. 修改前确认目标包在基线里的条目。
2. 修复目标包后删除对应基线项。
3. 单独运行对应脚本确认基线删除没有造成新失败。
4. 最后运行 `make guard-change`。

禁止:

- 把新增违规加入基线。
- 用宽泛 allowlist 替代具体修复。
- 为了通过守卫删除或弱化守卫逻辑。

## 尺寸收紧规则

文件超过守卫阈值时，优先按职责拆分:

- `*_validation.go`
- `*_lifecycle.go`
- `*_mapping.go`
- `*_worker.go`
- `*_persistence.go`
- `*_logging.go`

包 Go 文件数超过阈值时，优先拆子包，而不是继续横向加文件。每次只迁移一个包，迁移前补最小行为测试。

## 验收命令

普通 Go 修改:

```bash
go test ./<changed-package>
make guard-change
```

提交或交接前:

```bash
make project-map
go test ./...
go vet ./...
go build ./cmd/...
make guard-commit
```

合并、发布或部署前:

```bash
make guard-release
```
