# V3 架构项目结构与开发 SOP

> **加载条件**: 新建模块/文件、组织代码目录、添加新 API 时加载。

---

## 核心目录结构

遵循 V3 的 `modularity-convention.md`、`onion-architecture-convention.md` 和 `fx-convention.md` 契约。先用 `docs/契约/README.md`、`docs/架构/README.md` 和 codemap 缩小范围，再用 LSP 确认源码定义、引用、调用层级和 diagnostics。

| 路径 | 说明 | 规则与契约 |
|------|------|----------|
| `cmd/` | 程序入口与 sidecar | `cmd/agent-terminal` 是桌面主进程；`cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida` 是 MCP peer；`cmd/agent-runtime`、updater/release-manifest 是独立工具入口。 |
| `internal/app/` | 根 Fx 组装层 | `internal/app/modules.go` 组装 platform、store、module、provider、toolbridge；DAG 编排由独立 `mcp-orch` MCP server 承担，不嵌入桌面根图。 |
| `internal/contract/` | 稳定跨模块契约 | 只放窄接口、DTO、哨兵错误和 bridge 模型；不要把单一模块 owner 的胖 Service/Store 抬进 contract。 |
| `internal/module/` | 核心业务领域 | **严禁依赖 cmd 或外部框架细节**。按领域划分（如 `thread`, `turn`, `prompt`, `memory`, `skill`, `dashboard`, `uistate`）。 |
| `internal/platform/` | 基础设施平台 | 提供薄适配层，如 `db`, `rpc`, `bus`, `hooks`, `mcpcontrol`, `runner`, `statemachine`, `toolbridge`。 |
| `internal/provider/` | Provider adapter | Codex / Claude / unified session 适配；provider-native mirror 由 skill 模块在 provider 启动/acquire 前刷新。 |
| `internal/mcpserver/` | MCP 协议适配层 | 将模块能力通过 MCP 协议暴露；通用 MCP 协议优先放 `internal/mcpserver/common`。 |
| `internal/store/` | SQLite/sqlc store 层 | `internal/store/module.go` 提供共享 `sqlc.Queries` 并聚合子 store；store.Module 是明确的聚合例外，不放业务逻辑。 |
| `pkg/` | 公共组件 | 可供其他仓库引用的纯净库（如 `logger`, `errors`）。 |
| `sql/` | 产品 Store 查询 | 根目录 `sql/queries` 生成 `internal/store/sqlc`；mcp-orch sidecar 查询位于 `cmd/mcp-orch/sql/queries`。 |

> [!IMPORTANT]
> V3 项目强制模块隔离，禁止跨领域深度耦合，所有装配由 `fx` 完成。

---

## 业务模块 (`internal/module/{name}/`)

每个业务模块 MUST 完全封装自身逻辑，只对外暴露 Interface 和 `fx.Module`。

```text
internal/module/workspace/
├── module.go        # 模块声明 (暴露 fx.Module)
├── interface.go     # 对外公开的 Service 接口定义
├── dto.go           # 数据传输对象 (输入/输出结构体)
├── service.go       # 接口的私有具体实现 (type workspaceService struct)
├── store.go         # 数据库访问层封装 (封装 sqlc 生成的代码)
└── internal/        # (可选) 模块专属的私有子包
```

### 模块导出契约
- **Interface 公开**：`type Service interface { ... }`
- **Implementation 私有**：`type service struct { ... }`
- **跨模块端口收窄**：如果只有一个上游需要，优先在使用方定义窄接口或 adapter；只有稳定跨模块能力才进入 `internal/contract`。
- **Module 注册**：
```go
var Module = fx.Module("workspace",
    fx.Provide(
        newService, // 返回 Service 接口
        newStore,   // 返回 Store 接口
    ),
)
```

---

## 基础设施层 (`internal/platform/`)

数据库入口是 `internal/platform/db`，使用 `database/sql` 和 `modernc.org/sqlite` 打开 SQLite、执行迁移并校验 SQLite schema。

```text
internal/platform/
├── db/              # SQLite 打开、迁移与 schema 校验 (database/sql + modernc.org/sqlite)
├── rpc/             # jrpc2 核心装配与路由分发
├── config/          # 全局配置加载
├── runner/          # RunGroup runner 聚合
├── statemachine/    # qmuntal/stateless 适配
├── mcpcontrol/      # MCP peer 注册、心跳、hook callback
├── hooks/           # hook 三阶段分发与审批恢复
├── runtimesafe/     # 运行时安全与守卫
└── toolbridge/      # provider/tool 调用桥接
```

---

## Store 层 (`internal/store/`)

`internal/store/module.go` 是唯一允许集中 import store 子包的根聚合文件。它负责：

- `fx.Provide(func(db *sql.DB) *sqlc.Queries { return sqlc.New(db) })`
- `fx.Provide(func(q *sqlc.Queries) sqlc.Querier { return q })`
- 聚合各 `internal/store/<name>.Module`

其它 root 包不要复制这种聚合模式。业务模块应依赖自己的 Store 接口或使用方窄端口，不要直接把 `sqlc.Queries` 泄露到 Service 实现。

---

## 添加新功能（API 或 Service）的 SOP

遵循 **确认契约 → DTO / Port → 私有实现 → Store 防腐层 → 暴露 fx.Provide → 根图装配 → 验证** 顺序：

### 1. 确认契约和代码地图

先看 `docs/契约/README.md`、相关 `docs/契约/*.md` 和 `docs/架构/*.md`，再看对应 codemap 分卷。涉及源码判断时必须用 LSP 确认定义、引用、调用层级和 diagnostics。

### 2. 定义 DTO (`dto.go`)

使用具体的 Struct，禁止使用 `map[string]any` 乱传数据。涉及外部 RPC / MCP 暴露时添加 `json` tag，并实现显式校验；缺字段、空配置或非法状态必须 fail-fast。

```go
type CreateThreadRequest struct {
    Title string `json:"title"`
}
```

### 3. 定义 Service 接口或使用方 Port (`interface.go`)

模块自己的领域服务可以在模块根目录定义。跨模块消费优先在使用方定义窄端口，必要时用 adapter 把 store/service 裁剪成最小能力。

```go
type Service interface {
    CreateThread(ctx context.Context, req CreateThreadRequest) (string, error)
}
```

### 4. 实现 Service (`service.go`)

私有化实现，依赖项必须通过构造函数注入（利用 `fx`）。

```go
type service struct {
    store Store
    bus   *event.Dispatcher
}

func newService(store Store, bus *event.Dispatcher) Service {
    return &service{store: store, bus: bus}
}

func (s *service) CreateThread(ctx context.Context, req CreateThreadRequest) (string, error) {
    // 逻辑实现
}
```

### 5. 数据访问层 (`store.go`)

产品持久化默认走 SQLite + sqlc。`store.go` 负责将 `sqlc` 的 row / nullable / `Column1` 等生成形状封装为领域友好的防腐层，禁止将 `sqlc.Queries` 直接泄露给 Service。`hookstore` / `dbquery` 这类手写 SQL 是源码已存在的显式例外，新增例外必须有契约和测试理由。

### 6. 依赖注入 (`module.go`)

确保构造函数被加入 `fx.Provide`：

```go
var Module = fx.Module("thread",
    fx.Provide(
        newService,
        newStore,
    ),
)
```

### 7. 入口装配

普通产品模块优先接入 `internal/app/modules.go` 的根 `app.Module`。只有新的独立 binary / MCP peer 才在 `cmd/*` 入口构造自己的 Fx 图。

```go
var Module = fx.Options(
    store.Module,
    thread.Module,
    // ...
)
```

### 8. 验证

文档/技能改动至少运行 `python3 scripts/validate_super_agent_skills.py` 和 `git diff --check`。Go 后端改动按影响面运行 `./scripts/test_with_guard.sh <packages> -count=1`；跨模块边界、guard 或架构契约改动追加 `./scripts/test_with_guard.sh ./internal/archtest -count=1` 和 `make guard`。
