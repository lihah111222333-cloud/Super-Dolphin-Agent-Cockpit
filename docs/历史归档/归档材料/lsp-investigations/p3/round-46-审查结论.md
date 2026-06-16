# 第 46 轮审查结论

## 审查范围

- `internal/sidecar/lsp/protocol/methods.go`（28 个 LSP 方法常量定义）
- `internal/platform/config/lsp.go`（DefaultLSPConfig、lspConfigFromEnv、applyProjectAdapterEnv、envStringSliceOr、cloneLSPConfig、cloneLSPProjectAdapters）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `lsp.go:101-111` envStringSliceOr | 静默 | env 值 split 后为空时静默返 fallback（line 107-109） | 运维设 `LSP_NOISE_DIRS=""` 期望「无 noise dir」但拿到默认列表 | 区分「env 未设置」（fallback）vs「env 设为空」（返空 slice） |
| `lsp.go:79-91` lspConfigFromEnv | 弱契约 | 5 个 adapter 硬编码 env prefix（LSP_JSTS/PYTHON/RUST/JAVA/CSS） | 新增语言需改代码；env 名拼写错（如 `LSP_JSTS_ROOT_MARKER` 少 S）静默无效 | 加 env 名校验或 Warn 日志 |
| `lsp.go:93-99` applyProjectAdapterEnv | 静默 | `adapters[service]` 不存在时返零值 cfg，修改后写回 | 如果 DefaultLSPConfig 未包含某 service（如未来删除 CSS），env 配置仍会创建空 adapter | 加 `if _, ok := adapters[service]; !ok { return }` 守卫 |
| `methods.go:1-31` 整文件 | 弱契约 | 28 个常量无分组注释说明哪些是 request / notification / server-initiated | 调用方需查 LSP spec 才知道哪些方法需要 response | 加分组注释（// Requests / // Notifications / // Server-initiated） |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `lsp.go:79-91` lspConfigFromEnv | 多次 os.Getenv + configutil.SplitConfigStringSlice | 纯内存操作，无延迟风险 |
| `lsp.go:113-136` cloneLSPConfig | 多次 slices.Clone | 启动期一次性，无延迟风险 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `lsp.go:101-111` envStringSliceOr | env 设为空字符串时静默返 fallback（无法表达「清空列表」意图） |
| `lsp.go:93-99` applyProjectAdapterEnv | service 不存在时静默创建空 adapter |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `lsp.go:79-91` | 5 个 adapter env prefix 硬编码 |
| `lsp.go:101-111` | 空 env vs 未设置 env 不可区分 |
| `methods.go:1-31` | 28 个常量无 request/notification 分类 |
| `lsp.go:14-77` DefaultLSPConfig | 大量硬编码目录名/扩展名/marker 无版本化 |

## 修复优先级

### P0（无）
本轮无 P0 问题。`lsp.go` 和 `methods.go` 是配置/常量文件，不涉及运行时逻辑。

### P1（本月）
1. `lsp.go:101-111` envStringSliceOr 区分「未设置」vs「设为空」（如用 sentinel `NONE` 表示清空）
2. `lsp.go:93-99` applyProjectAdapterEnv 加 service 存在性守卫
3. `methods.go:1-31` 加分组注释

### P2（下个 sprint）
4. `lsp.go:79-91` 5 个 adapter env prefix 改为 registry-driven
5. `lsp.go:14-77` DefaultLSPConfig 硬编码值改为可配置 JSON

## 边界条件

1. **`lsp.go` 整体是项目正面案例**：DefaultLSPConfig 提供合理默认值 + lspConfigFromEnv 允许 env 覆盖 + cloneLSPConfig 深拷贝防止共享状态污染。这是「sensible defaults + env override」的良好配置模式。唯一弱点是 envStringSliceOr 无法区分「未设置」和「设为空」。
2. **`methods.go` 是纯常量文件**：28 个 LSP 方法名。无运行时逻辑，无 fail-fast 问题。但缺乏分组注释让 reviewer 难以快速判断哪些方法需要 response（request）vs 不需要（notification）。建议加注释分组。
3. **`lsp.go:14-77` DefaultLSPConfig 的 NoiseDirNames 与 GoDirectoryFilters 重复**：两个列表包含相同目录名（.agent, .git, node_modules 等），但格式不同（NoiseDirNames 是纯名称，GoDirectoryFilters 带 `-` 前缀和 `**/` 通配符）。这是因为两者用于不同场景（NoiseDirNames 用于通用过滤，GoDirectoryFilters 用于 gopls directoryFilters 设置）。但维护时容易遗漏同步——建议从 NoiseDirNames 自动派生 GoDirectoryFilters。
4. **`lsp.go:101-111` envStringSliceOr 的「空 env = fallback」语义**：Go 的 `os.Getenv("")` 返回空字符串，与「env 未设置」不可区分（除非用 `os.LookupEnv`）。当前实现用 `strings.TrimSpace(os.Getenv(key))` 无法区分两者。改用 `os.LookupEnv` 可以区分：未设置 → fallback；设为空 → 返空 slice。这是 Go env 处理的常见改进。
5. **`lsp.go:93-99` applyProjectAdapterEnv 的 map 值语义**：Go map 取不存在的 key 返零值（空 struct）。line 94 `cfg := adapters[service]` 在 service 不存在时 cfg 是零值 LSPProjectAdapterConfig。后续 envStringSliceOr 对零值 slice 返 fallback（env 未设置时）或 env 值。最终 line 98 `adapters[service] = cfg` 写回——如果 env 也未设置，写回的是零值 struct（所有 slice 为 nil）。这不会造成运行时错误（nil slice 在 Go 中可安全 range），但语义上「创建了一个空 adapter」可能让下游误以为该语言已配置。

---

**本轮总结**：本轮无 P0 问题。`lsp.go` 是「sensible defaults + env override + deep clone」的配置正面案例。`methods.go` 是纯常量文件无运行时风险。主要改进点是 envStringSliceOr 应区分「未设置」vs「设为空」（用 os.LookupEnv），以及 methods.go 加分组注释。NoiseDirNames 与 GoDirectoryFilters 的重复维护建议自动派生。

**累计进度**：46 轮完成。cron `fd4b4728` 继续推进。

---

**截至第46轮整体审查状态：**
- 已完成：27（补漏）+ 28-46 = **共 20 轮**
- 累计 P0：**44 个**（本轮无新增）
- 覆盖文件：~60+ 个生产代码文件
- 未覆盖重点区域：`internal/contract/`（接口定义）、`internal/dto/`（数据传输对象）、`internal/module/`（业务模块）、`internal/sidecar/orch/orchestration/runtime*.go`（运行时调度核心）

cron `fd4b4728` 每5分钟继续推进下一轮。
