# P26 洋葱架构审计与重构计划

## 1. 架构现状概览

当前 `super-agent-v3` 系统在宏观层面**高度符合**洋葱架构（Onion Architecture）以及整洁架构（Clean Architecture）的核心理念。系统通过严密的目录层级与契约管理，实现了领域逻辑与底层基础设施的解耦。

### 1.1 架构分层映射

- **核心领域层 (Domain Layer)**：`internal/module/`
  - **原则**：严禁依赖外部框架细节、RPC 协议和具体的基础设施。
  - **实现**：仅暴露 `Interface` 和 `DTO`，业务实体生命周期完全封闭在未导出的 `struct` 中。
- **依赖倒置与装配 (Dependency Inversion)**：`go.uber.org/fx`
  - **原则**：依赖从外向内，内层仅定义接口，由外层完成具体实现的注入。
  - **实现**：所有的模块都通过 `fx.Module` 将自己的提供者（Provider）注册到容器中。
- **基础设施层 (Infrastructure Layer)**：`internal/platform/`
  - **原则**：提供进程间事件总线、数据库连接池、RPC 通信、全局配置等公共技术能力。
- **外部适配器/表现层 (Adapter / Presentation)**：`internal/mcpserver/`, `internal/provider/`, `cmd/`
  - **原则**：负责系统与外界的交互，如 CLI 命令、MCP 协议暴露、以及对接外部提供商（Claude/Codex）。
- **防腐层 (Anti-Corruption Layer)**：`internal/store/`
  - **原则**：隔离数据库具体实现（如 `sqlc`），将 SQL 响应转换为领域 DTO，防止 `sqlc.Queries` 直接泄露给 `Service`。

## 2. 审计结论

尽管主干架构非常健康，并在 `internal/archtest` 目录下建立了一系列架构守护测试（Architecture Guards），我们在最近的审计中依然发现了几处**依赖反转**的违规坏味道：

1. **核心领域对外部协议的越权感知**：`internal/module` 中的代码调用了 `mcpserver/common`。
2. **底层平台对高层适配器的双向依赖**：`internal/platform/toolbridge` 反向依赖了特定的 `provider` 协议细节。

这些“坏味道”破坏了从外向内的单向依赖原则。

## 3. 下一步行动

为了修复这些架构缝隙，我们已建立专门的修复计划。具体请参阅子文档：
- [P26-01: 核心与平台层依赖违规修复](./p26-01-dependency-repair.md)
