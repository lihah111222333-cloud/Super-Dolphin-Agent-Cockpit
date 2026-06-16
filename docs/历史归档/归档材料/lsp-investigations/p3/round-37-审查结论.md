# 第 37 轮审查结论

## 审查范围

- `internal/sidecar/lsp/manager/scope.go`（ToolScope、ResolvedToolScope、ScopedManager、ScopedManagerResolver、WithResolvedToolScope、ResolvedToolScopeFromContext、ManagerWithResolvedScope、resolvedScopeManager 18 个 method 装饰）
- `internal/sidecar/lsp/multilsp/registry_scope.go`（registryScopedResolver、ForToolScope、CurrentManagersForToolScope、resolveRegistryScope、adapterForLanguage、registryBaseScope、registryTargetURI、registryLanguageID、normalizeRegistryWorkspaceRoot、registryScopeKey、managerScopedManager、managerToolScope）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `manager/scope.go:69-77` WithResolvedToolScope | 静默 | `ctx == nil` 时静默 fallback `context.Background()`；scope 全空时静默 return 原 ctx | 上游 nil ctx 是 bug，被静默掩盖；空 scope 不报错让 caller 不知道注入失败 | nil ctx 改为 panic（开发期）；空 scope return error |
| `manager/scope.go:79-88` ResolvedToolScopeFromContext | 弱契约 | `(ResolvedToolScope, false)` 在 nil ctx / 类型不匹配 / scope 全空三种情况返同样结果 | 调用方无法区分「未注入」vs「注入了空 scope」 | 拆为两个 API：`MustGet`（panic）+ `TryGet`（bool） |
| `manager/scope.go:90-95` ManagerWithResolvedScope | 静默 | manager==nil 或 scope 全空时返回原 manager（不包装） | 调用方期望返回的 manager 携带 scope；当前可能返回未包装版本，scope 静默丢失 | scope 全空时 panic；nil manager 应在更上游拒绝 |
| `registry_scope.go:20-26` NewRegistryScopedResolver | 静默 | manager 类型断言失败 / pool nil 时返 nil resolver | 上游 fx 注入 bug 时 resolver 是 nil，下游调用 panic 或 silent skip | 改为返回 `(resolver, error)` |
| `registry_scope.go:111-121` adapterForLanguage | 兜底 | `pool.primary == nil` 时 `NewDefaultLanguageAdapterRegistry()` 现场新建 | 现场新建导致 cache miss + 配置漂移（与 pool 配置不一致） | pool.primary nil 是严重 bug，应 fail-fast |
| `registry_scope.go:76-82` currentWorkspaceRoots | 静默 fallback | cwd 空时 fallback `r.pool.primary.workspaceRoot` | 三层 fallback 链（input→primary→空），caller 不知最终用了哪个 | 加 Debug 日志带最终选定值 |
| `registry_scope.go:123-127` registryBaseScope | 静默 fallback | 同上 cwd fallback | 同上 | 同上 |
| `registry_scope.go:152-155` 切片复制 | 性能 | `append([]string(nil), workspaceRoots...)` 在 hot path 上每次复制 | scope 解析每次 LSP 工具调用一次；roots 通常 1-3 个，影响小但累积 | 测量后决定（可能需要不可变 slice） |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `registry_scope.go:84-109` resolveRegistryScope | 调用 adapter.ResolveRoot —— 可能涉及磁盘 stat 寻找 go.mod / package.json 等项目根 | 加 `time.Now()` 计时；ResolveRoot >100ms 打 Warn |
| `registry_scope.go:43-74` CurrentManagersForToolScope | `r.pool.SnapshotManagers()` 是同步快照；snapshot 大时遍历慢 | snapshot 大小监控；>1000 manager 时改流式 |
| `registry_scope.go:131` selectWorkspaceRootForTarget | 第31轮已发现 ContainsPath 可能涉及 stat | 同前轮建议（按长度预排序短路） |
| `manager/scope.go:102-104, 110-208` resolvedScopeManager 18 个 method | 每个 method 都包装 ctx；如果 ctx 链已很深，每层包装增加查找成本 | ctx.Value 调用次数监控；深度 >10 加 Warn |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `manager/scope.go:70-72` | nil ctx 静默 fallback Background |
| `manager/scope.go:73-75` | 空 scope 静默 return 原 ctx |
| `manager/scope.go:79-88` | nil ctx / 类型断言失败 / 空 scope 都返 false |
| `manager/scope.go:91-93` | nil manager 或空 scope 返原 manager |
| `registry_scope.go:21-25` | 类型断言失败 / pool nil 静默返 nil resolver |
| `registry_scope.go:78-79, 125-126` | cwd 空时 silent fallback primary |
| `registry_scope.go:55-57` SnapshotManagers loop | snapshot.base / nil manager 静默 continue |
| `registry_scope.go:115-120` adapterForLanguage | pool.primary nil 时静默走默认 registry |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `manager/scope.go:13-34` ToolScope | 18 个字段中有 6 个含 fallback 关系（CWD / WorkspaceRoots / WorkspaceRoot / LanguageWorkspaceRoot / ProjectRoot / RootKind） |
| `manager/scope.go:39-46` ResolvedToolScope | 嵌入 ToolScope + 4 个 key 字段，调用方需知道 key 派生关系 |
| `manager/scope.go:69-77` WithResolvedToolScope | scope 是否注入靠 `WorkspaceKey \|\| ManagerKey` 非空判断 |
| `manager/scope.go:90-95` ManagerWithResolvedScope | 「nil manager 返 nil」与「nil scope 返原 manager」语义不同但同函数 |
| `registry_scope.go:43-74` | snapshot 过滤条件多重（base / nil / scopeKey / workspace roots / 去重）无文档 |

## 修复优先级

### P0（必须本周修）
1. **`manager/scope.go:70-72` ctx nil silent fallback**——这是 18 个 method 装饰器的入口路径。如果上游传 nil ctx，silently 用 Background 会丢失 trace ID、deadline、cancellation。所有 LSP 操作变成无 trace、无超时的「裸调」，运维无法 trace 慢请求。改为 panic 或返回 ErrNilContext。
2. **`registry_scope.go:115-120` pool.primary nil 静默走默认 registry**——pool.primary 是 LSP manager pool 的核心。nil 表示初始化未完成或注入失败。当前代码现场创建 default registry，与 pool 配置漂移：default registry 的语言列表、缓存策略、root 解析规则都可能与 pool 不一致 → 同一文件被两个 manager 处理（pool primary 和 default registry），缓存失效。改为 nil 时 panic。

### P1（本月）
3. `manager/scope.go:79-88` ResolvedToolScopeFromContext 拆 MustGet/TryGet
4. `manager/scope.go:90-95` ManagerWithResolvedScope nil 路径明确化
5. `registry_scope.go:20-26` NewRegistryScopedResolver 改 (resolver, error)
6. `registry_scope.go:76-82` cwd fallback 加 Debug 日志

### P2（下个 sprint）
7. `registry_scope.go:84-109` resolveRegistryScope 加 duration 监控
8. `manager/scope.go:13-34` ToolScope 6 字段 fallback 关系文档化
9. `registry_scope.go:152-155` slice 复制评估改不可变 slice

## 边界条件

1. **`manager/scope.go` 装饰器模式是项目正面案例**：18 个 method 都通过 `m.scoped(ctx)` 注入 scope 到 ctx——这是把横切关注点（scope tracking）从业务逻辑中分离的良好实践。LSP 各操作 method 不需要直接知道 scope，只通过 ctx 传递。装饰器的所有 method 都是 1-line 转发，无逻辑漂移风险。**正面架构案例**。
2. **`registry_scope.go:84-109` resolveRegistryScope 的两遍流程**：①`registryBaseScope` 处理 cwd/target/path；②`adapter.ResolveRoot` 找具体语言根（go.mod / package.json）。两步分离让通用路径校验（workspaceRoots 包含性）与语言特定根解析解耦。架构清晰但文档不足——`adapter.ResolveRoot` 内部行为对 reviewer 不透明，需读 LanguageAdapter 接口。
3. **`registry_scope.go:43-74` CurrentManagersForToolScope 的去重设计**：line 64-67 用 `seen map[lspmanager.Manager]struct{}{}` 去重——同一 Manager 可能被多个 ResolvedScope 引用（如同一 workspace 被多个 turn 共用）。这是 fan-out 场景下的合理去重。但 `seen` 用 interface 作 key 依赖 Manager 实现的 hash 行为；如果 Manager 是 struct 嵌入（非 pointer），hash 可能基于值导致同 manager 不同 key。**潜在 bug，但当前代码 Manager 实现都是 pointer，OK**。
4. **`registry_scope.go:152-155` 的 nil-prefix slice 复制模式**：`append([]string(nil), src...)` 是 Go 社区常见的「只读切片」防御性复制。如果上层不修改 src，可省略。这里在 hot path 上可能有性能影响，但安全 > 性能（避免 caller 修改 source 切片污染 scope）。建议测量后决定。
5. **`manager/scope.go:73-75` 空 scope 的合理性**：当 LSP 工具被非 scoped caller 调用（如内部诊断、init 时）时，scope 是空的。这种情况下 ctx 不应被污染。当前 silent return 是合理的——但应加 Debug 日志「scope empty, skipping ctx injection」让排错时可见。
6. **`registry_scope.go` 与 `multilsp/scope.go`（第31轮审查）的关系**：本文件是 `multilsp` 包对 `manager` 包暴露的适配器（line 16-19 注释明确）。两层 scope 类型（lspmanager.ToolScope vs multilsp.LSPToolScope）通过 `managerToolScope`/`managerScopedManager` 互相转换。这是为了避免循环导入（manager→multilsp 会循环）。架构合理但增加了类型映射成本——每次 scope 解析都做一次 struct 字段拷贝（line 216-234）。

---

**本轮总结**：发现 2 个 P0 问题：①manager/scope.go:70 ctx nil silent fallback 影响 18 个 LSP 操作的 trace/超时；②registry_scope.go:115-120 pool.primary nil 静默走默认 registry 会与 pool 配置漂移。`manager/scope.go` 的装饰器模式是项目正面架构案例。两层 scope 类型（manager vs multilsp）转换是为避免循环导入的合理代价。

**累计进度**：37 轮完成。cron `fd4b4728` 继续推进。
