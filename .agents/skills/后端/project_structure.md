# V3 架构项目结构与开发 SOP

> **加载条件**: 新建模块/文件、组织代码目录、添加新 API 时加载。

---

## 核心目录结构

遵循 V3 的 `modularity-convention.md` 契约，代码分为严格的层级：

| 路径 | 说明 | 规则与契约 |
|------|------|----------|
| `cmd/` | 程序入口与 sidecar | `cmd/agent-terminal`、`cmd/mcp-orch`、`cmd/mcp-lsp`、`cmd/mcp-ida` 可包含入口、运行时、工具注册和同包测试。 |
| `internal/module/` | 核心业务领域 | **严禁依赖 cmd 或外部框架细节**。按领域划分（如 `thread`, `prompt`, `memory`, `skill`）。 |
| `internal/platform/` | 基础设施平台 | 提供底层技术支撑，如 `db`, `rpc`, `config`, `toolbridge`。 |
| `internal/mcpserver/` | MCP 协议适配层 | 将 `internal/module` 的能力通过 MCP 协议暴露。 |
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

数据库入口是 `internal/platform/db`，使用 `database/sql` 和 `modernc.org/sqlite` 打开 SQLite、执行迁移并校验 schema。

```text
internal/platform/
├── db/              # SQLite 打开、迁移与 schema 校验 (database/sql + modernc.org/sqlite)
├── rpc/             # jrpc2 核心装配与路由分发
├── config/          # 全局配置加载
├── runtime/         # 运行时安全与守卫
└── toolbridge/      # provider/tool 调用桥接
```

---

## 添加新功能（API 或 Service）的 SOP

遵循 **Interface → DTO → 私有实现 → 暴露 fx.Provide → 依赖注入** 顺序：

### 1. 定义 DTO (`dto.go`)

使用具体的 Struct，禁止使用 `map[string]any` 乱传数据。
如果涉及外部 RPC 暴露，需添加 `json` tag，并可实现 `Validate() error` 方法。

```go
type CreateThreadRequest struct {
    Title string `json:"title"`
}
```

### 2. 定义 Service 接口 (`interface.go`)

在模块根目录定义领域接口。

```go
type Service interface {
    CreateThread(ctx context.Context, req CreateThreadRequest) (string, error)
}
```

### 3. 实现 Service (`service.go`)

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

### 4. 数据访问层 (`store.go`)

**V3 规定**：必须通过 `sqlc` 生成底层查询代码，`store.go` 负责将其封装为领域友好的防腐层，禁止将 `sqlc.Queries` 直接泄露给 `Service`。

### 5. 依赖注入 (`module.go`)

确保构造函数被加入 `fx.Provide`：

```go
var Module = fx.Module("thread",
    fx.Provide(
        newService,
        newStore,
    ),
)
```

### 6. 入口装配 (`cmd/*/main.go`)

在执行入口的 `fx.New()` 中引入该模块。

```go
fx.New(
    platform.Module,
    thread.Module,
    // ...
).Run()
```
