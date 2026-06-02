# 第 22 轮审查结论

## 审查范围

- `cmd/mcp-lsp/manager/manager.go`（Manager 接口定义：LifecycleManager、NavigationManager、XRefManager、StructureManager、CompletionManager、EditManager、DocumentLifecycleManager、DiagnosticsManager）
- `cmd/mcp-lsp/manager/registry.go`（dynamicRegistry：Register、GetManagerForFile/Language、ensureInstalled、scopedManagerForConfig、Close、Diagnostics、DetectLanguageID）
- `cmd/mcp-lsp/manager/scope.go`（ToolScope、ResolvedToolScope、ScopedManager、resolvedScopeManager 代理、WithResolvedToolScope/FromContext）

> 与第 01 轮覆盖的 `cmd/mcp-lsp/fx.go`（registry 使用方）不重复。本轮聚焦 manager 包内部实现。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `registry.go:84-89` `NewRegistry` | 兜底 | `inst == nil` 时调用 `NewRegistryWithInstaller(nil)` | nil installer 是合法 optional（不需要自动安装 LSP server）；但 `NewRegistry(nil)` 与 `NewRegistryWithInstaller(nil)` 行为相同，多一层间接 | 当前合理；可简化为直接调用 |
| `registry.go:91-96` `NewRegistryWithInstaller` | 弱契约 | installer 可为 nil | 合理（optional 依赖） | OK |
| `registry.go:98-117` `Register` / `register` | 弱契约 | `languageID` 不做空值校验；`manager` 不做 nil 校验 | 空 languageID 会注册到 `""` key；nil manager 后续 `config.manager.Close()` 会 panic | 入口校验 languageID 非空 + manager 非 nil |
| `registry.go:147-163` `resolveManagerForTarget` | 兜底 | `lang` 为空时 `r.managers[""]` 查找失败 → `ErrUnsupportedLanguage` | 合理的 fail-fast | OK |
| `registry.go:191-204` `ensureInstalled` | 兜底 | `r.installer == nil \|\| config == nil \|\| config.skipInstaller` 时 return nil | installer nil 是合法 optional；config nil 不应发生（调用方已判 ok） | config nil 应 panic（不可达） |
| `registry.go:199-203` `config.binaryPath = path` | 弱契约 | 在 RLock 外修改 config 字段 | `ensureInstalled` 在 RLock 释放后被调用；`config.binaryPath` 赋值无锁保护 | 用 Lock 保护赋值；或把 binaryPath 改为 atomic |
| `registry.go:217-234` `scopedManagerForConfig` | 兜底 | `config == nil \|\| config.manager == nil` 返回 ErrUnsupportedLanguage | config nil 不应发生（调用方已判）；manager nil 是 Register 时的 bug | 当前合理（defensive） |
| `registry.go:236-248` `Close` | 兜底 | 遍历所有 manager Close；用 `errors.Join` 聚合 | 合理的 fail-fast（正面案例） | OK |
| `registry.go:250-259` `DetectLanguageID` | 兜底 | 未知扩展名时返回 `strings.TrimPrefix(ext, ".")` | 如 `.xyz` 返回 `"xyz"`；后续 `resolveManagerForTarget` 会报 ErrUnsupportedLanguage | 合理（让上层决定） |
| `registry.go:261-275` `Diagnostics` | 兜底 | `managersForDiagnostics` 返回空 map 时返回 nil slice | 合理 | OK |
| `registry.go:300-307` `BootstrapDocument` | 弱契约 | `strings.TrimPrefix(uri, "file://")` 做 URI→path 转换 | 不处理 `file:///` 三斜杠（Windows）或 percent-encoding | 应使用 `url.Parse` + `url.Path` |
| `registry.go:377-391` `groupURIsByManager` | 兜底 | `ErrUnsupportedLanguage` 时 continue（跳过不支持的文件） | 合理（diagnostics 不应因为一个不支持的文件而失败） | OK |
| `registry.go:393-398` `registryToolScope` | 兜底 | `ctx == nil` 兜底 Background；`ToolScopeFromContext` 失败时尝试 `WorkspaceRootFromContextStrict` | nil ctx 是调用方 bug | nil ctx 应 panic |
| `scope.go:69-77` `WithResolvedToolScope` | 兜底 | `ctx == nil` 兜底 Background；scope 的 WorkspaceKey 和 ManagerKey 都为空时返回原 ctx | nil ctx 是调用方 bug；空 scope 不设值是合理的 | nil ctx 应 panic |
| `scope.go:79-88` `ResolvedToolScopeFromContext` | 兜底 | `ctx == nil` 返回 `(zero, false)` | nil ctx 是调用方 bug | panic |
| `scope.go:90-95` `ManagerWithResolvedScope` | 兜底 | `manager == nil` 时返回 nil；scope 空时返回原 manager | nil manager 是调用方 bug | nil manager 应 panic |
| `scope.go:97-208` `resolvedScopeManager` 代理 | 弱契约 | 所有方法都委托到 `m.manager.*`；`m.manager` 为 nil 时会 panic | 构造时 `ManagerWithResolvedScope` 已判 nil；但如果有人直接构造 `&resolvedScopeManager{}` 会 panic | 当前合理（unexported struct） |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `registry.go:199-203` | config.binaryPath 赋值无锁保护（data race） |
| `registry.go:393-398` | registryToolScope ctx==nil 兜底 Background |
| `scope.go:69-77` | WithResolvedToolScope ctx==nil 兜底 |
| `scope.go:79-88` | ResolvedToolScopeFromContext ctx==nil 返回 false |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `registry.go:98-117` | Register languageID 可为空；manager 可为 nil |
| `registry.go:199-203` | config.binaryPath 赋值无锁（data race） |
| `registry.go:300-307` | BootstrapDocument URI→path 用 TrimPrefix 而非 url.Parse |
| `registry.go:393-398` | registryToolScope ctx nil 兜底 |
| `scope.go:69-77` | WithResolvedToolScope ctx nil 兜底 |
| `scope.go:79-88` | ResolvedToolScopeFromContext ctx nil |
| `scope.go:90-95` | ManagerWithResolvedScope manager nil 返回 nil |

## 修复优先级

### P0（必须本周修）
1. **`registry.go:199-203` config.binaryPath 赋值无锁保护**——这是一个 data race：`ensureInstalled` 在 RLock 释放后被调用，多个并发 tool call 可能同时写 `config.binaryPath`。必须用 Lock 保护或改为 atomic。

### P1（本月）
2. `registry.go:98-117` Register 入口校验 languageID 非空 + manager 非 nil
3. `registry.go:300-307` BootstrapDocument URI→path 改用 `url.Parse`
4. `scope.go:69-88` WithResolvedToolScope / ResolvedToolScopeFromContext nil ctx 改 panic
5. `scope.go:90-95` ManagerWithResolvedScope nil manager 改 panic

### P2（下个 sprint）
6. `registry.go:393-398` registryToolScope nil ctx 改 panic
7. `registry.go:84-89` NewRegistry 简化（删除多余间接层）

## 边界条件

1. **`config.binaryPath` data race 的修复方向**：当前 `ensureInstalled` 在 `resolveManagerForTarget` 内被调用，此时 RLock 已释放。修复选项：
   - (a) 把 `config.binaryPath` 改为 `atomic.Value`（最小改动）
   - (b) 在 `ensureInstalled` 内部用 `r.mu.Lock()` 保护赋值（但会把 RLock 升级为 Lock，影响并发）
   - (c) 把 binaryPath 从 config 移到 installer 内部缓存（最干净但改动大）
   建议 (a)。
2. **`BootstrapDocument` 的 URI→path 转换**：当前 `strings.TrimPrefix(uri, "file://")` 对 `file:///C:/foo` 会得到 `/C:/foo`（多一个 `/`）。Windows 路径需要特殊处理。`format.URIToPath` 已有正确实现，应复用。
3. **`Register` 空 languageID 的影响**：如果有人调用 `Register("", manager)`，会注册到 `""` key。后续 `DetectLanguageID` 对未知扩展名返回 `"xyz"` 等非空字符串，不会命中 `""` key。所以空 languageID 注册实际上是死代码——但仍应拒绝。
4. **`resolvedScopeManager` 是 unexported struct**：外部无法直接构造，只能通过 `ManagerWithResolvedScope` 获得。所以 `m.manager == nil` 的 panic 不会在生产路径发生。当前合理。
5. **`Close` 的 errors.Join 是正面案例**：与 round-07/08/09 中多处 `_ = disconnectLease(...)` 形成对比。registry.Close 正确聚合了所有 manager 的 close 错误。
6. **`groupURIsByManager` 跳过 ErrUnsupportedLanguage**：这是为了让 diagnostics 请求中包含 `.md`、`.txt` 等非 LSP 文件时不报错。合理的 graceful degradation（不是 fail-fast 违规）。

---

**本轮总结**：manager 包代码质量较高，接口设计清晰。唯一的 P0 是 `config.binaryPath` 赋值的 data race——这是一个真实的并发 bug，多个 tool call 并发时可能读到半写入的 path。

**累计进度**：22 轮完成。cron `da34430c` 继续推进。
