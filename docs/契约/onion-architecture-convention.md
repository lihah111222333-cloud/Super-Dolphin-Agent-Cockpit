# 洋葱架构契约 (Onion Architecture Convention)

## 1. 核心理念

`super-agent-v3` 系统严格遵循**洋葱架构（Onion Architecture）**及整洁架构（Clean Architecture）的核心原则：**依赖反转（Dependency Inversion）**与**单向依赖**。

架构的核心目的是将核心业务逻辑与外部环境（如数据库、UI、网络协议、外部 API 等）完全隔离。
**绝对准则**：代码的依赖必须始终**由外向内**指向。内层代码绝对不允许知道外层代码的任何细节，也不能引入外层包。

---

## 2. V3 系统的层级映射

我们的目录结构直接映射了洋葱架构的分层：

### 2.1 核心领域层 (Domain / Application Layer)
*   **位置**: `internal/module/*`
*   **职责**: 封装系统最核心的业务逻辑（如 Turn 编排、Memory 处理、Skill 执行）。
*   **契约规则**:
    *   **零外层实现依赖**：严禁 `import` 任何 `internal/mcpserver`、provider concrete driver 或 `cmd` 包；只允许已登记的 provider 语义端口。
    *   **零持久化实现依赖**：严禁 `import` 任何 `internal/store` 包；Module 只依赖 `contract`、`dto`、允许的 `platform` 能力、provider 语义端口和模块自有 Port。
    *   **Port 所有权**：单一 Module 私有的持久化 Port 与领域 DTO 由消费它的 Module 定义，不下沉到 `internal/contract`。
    *   **接口隔离**：领域层只向外暴露 `Interface`（服务契约）和 `DTO`（数据传输对象），所有具体的实现细节（如 `type service struct`）必须保持私有。
    *   **不可知性**：领域层不关心请求是通过 HTTP、CLI 还是 MCP 协议进来的。

### 2.2 防腐层 (Anti-Corruption Layer)
*   **位置**: `internal/store/*`
*   **职责**: 负责领域层抽象与具体数据库操作之间的转译。
*   **契约规则**:
    *   屏蔽底层持久化细节，严禁将由 `sqlc` 生成的底层数据类型或 `*sqlc.Queries` 泄露给 `internal/module`。
    *   Store 不得反向 import Module；Store DTO 与 Module DTO 的转换由组合根中的 adapter 负责。

### 2.3 基础设施层 (Infrastructure Platform Layer)
*   **位置**: `internal/platform/*`
*   **职责**: 提供所有跨模块共享的纯粹底层基础技术能力（如进程内事件总线 `bus`、网络发现 `discovery`、配置加载 `config`）。
*   **契约规则**:
    *   作为底层平台，严禁反向依赖高层的业务逻辑和特定的外部适配器。
    *   严禁 `import` 任何 `internal/mcpserver`、`internal/provider` 包。如果底层平台需要高层应用的特定数据，应通过 `internal/contract` 暴露接口让高层实现并注入。

### 2.4 外部适配层与表现层 (Adapter / Presentation Layer)
*   **位置**: `internal/mcpserver/*`, `internal/provider/*`, `internal/ui/*`
*   **职责**: 负责连接外部世界。把来自宿主应用（Codex、Claude）或外部协议（MCP）的输入转换为对 `internal/module` 接口的调用。
*   **契约规则**:
    *   位于最外层，可以安全地引入所有内层（Domain、Platform）的包，但同级适配器之间尽量避免横向交叉污染（例如 `provider` 避免调用 `mcpserver` 内的工具类）。

### 2.5 启动与装配层 (Bootstrapping / Entrypoint Layer)
*   **位置**: `cmd/*`
*   **职责**: 应用程序的执行入口。
*   **契约规则**:
    *   桌面进程以 `internal/app` 为组合根，独立服务以各自 `cmd` 为组合根；通过 `go.uber.org/fx` 将 Store 实现包装为 Module-owned Port 后注入 Module。
    *   App adapter 负责 Module DTO 与 Store DTO 的逐字段转换，未知或缺失的必需依赖必须使装配失败。

---

## 3. 架构的自动化守护

在 `internal/archtest/dependency_direction_test.go` 中，我们编写了一系列自动化测试来锁定洋葱架构的契约边界。

任何尝试破坏单向依赖（如 `module` 引入了 `mcpserver`，或者 `platform` 引入了 `provider`）的改动，都会在 CI 构建和本地测试阶段被直接拦截并报错。开发者必须通过**能力下沉**（Sink Down）或**接口反转**（Dependency Inversion）来保持架构的纯洁。
