# P26-01: 核心与平台层依赖违规修复

## 1. 背景与违规发现

在执行 P26 洋葱架构审计时，通过依赖扫描（`go list -f '{{.ImportPath}}: {{join .Imports ", "}}' ./internal/...`），发现了以下三类违反架构单向依赖的现象：

### 违规 1：核心领域层依赖了外层协议适配层
*   **违规文件**: `internal/module/turn/manifest.go`
*   **违规点**: `module/turn` 依赖了 `internal/mcpserver/common` 中的 `DiscoverPeerHTTPAddr`。
*   **冲突说明**: `module` 属于洋葱的最内层核心业务，决不能知晓外部服务（MCP Server）是如何暴露或发现的。这导致了核心业务与特定的服务发现机制强耦合。

### 违规 2：基础设施层反向依赖了高层应用层
*   **违规模块**: `internal/platform/toolbridge`
*   **违规点**: 依赖了 `internal/mcpserver/common` 以及 `internal/provider/codexapp` 的专属协议。
*   **冲突说明**: 基础设施（Platform）应提供纯粹的底层支持，反向依赖特定客户端（Codex App）或特定的外部通信网关，会导致循环依赖并破坏 Platform 的泛用性。

### 违规 3：外部适配器之间的横向污染
*   **违规模块**: `internal/provider/claudecli` 与 `internal/provider/codexapp`
*   **违规点**: 同级适配器之间通过 `mcpserver/common` 发生粘连。
*   **冲突说明**: `mcpserver/common` 承担了不属于它的公共基础设施职能（如探活与发现），导致了适配层之间的横向污染。

---

## 2. 修复方案与执行路径

修复的核心思路是**接口隔离（Dependency Inversion）**与**公共能力下沉（Sink down）**。

### Phase 1: 抽象与下沉 Discovery 能力 (已完成)
服务发现机制（Peer Discovery）本质是基础设施能力，不该绑定在 `mcpserver` 下。
- **行动**：将 `mcpserver/common/discovery.go` 剥离，新建并移动至 `internal/platform/discovery`。
- **行动**：全局替换所有对 `common.DiscoverPeerHTTPAddr` 的引用，修改为 `discovery.DiscoverPeerHTTPAddr`。

### Phase 2: 解耦 Toolbridge (已完成)
解决 `toolbridge` 反向依赖 `codexapp` 和 `mcpserver` 的问题。
- **行动**：排查 `internal/platform/toolbridge` 对 `codexapp` 特定协议结构的硬编码引用。
- **行动**：引入一层内部契约接口（如在 `internal/contract/mcp` 或 `dto` 层面），让 `codexapp` 来实现该接口并注入，而非 `toolbridge` 主动调用。
- **行动**：将涉及到的所有 MCP 通用常量下沉至 Platform 层。

### Phase 3: 架构守护测试增强 (已完成)
代码重构完成后，通过增加自动化测试确保未来不会发生类似违规。
- **行动**：扩展 `internal/archtest/dependency_direction_test.go`。
- **行动**：在现有的 `assertPlatformIsolationRules` 中添加新断言：拦截所有从 `internal/platform` 指向 `internal/provider` 和 `internal/mcpserver` 的 `import`。
- **行动**：添加新断言：拦截所有从 `internal/module` 指向 `internal/mcpserver` 的 `import`。

---

## 3. 验收标准

重构完成后，在命令行运行以下测试，确保系统没有任何违反依赖规则的引用：

```bash
# 检查领域层没有依赖 mcpserver
go list -f '{{.ImportPath}}: {{join .Imports "\n"}}' ./internal/module/... | grep -E 'mcpserver'

# 检查平台层没有依赖 provider 和 mcpserver
go list -f '{{.ImportPath}}: {{join .Imports "\n"}}' ./internal/platform/... | grep -E 'provider|mcpserver'

# 确保架构守护断言全部通过
go test ./internal/archtest/... -v
```
