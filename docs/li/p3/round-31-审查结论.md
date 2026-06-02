# 第 31 轮审查结论

## 审查范围

- `cmd/mcp-lsp/multilsp/scope.go`（ResolveLSPToolScope、canonicalizeLSPToolScope、buildScopeKey、buildWorkspaceKey、canonicalScopePath、canonicalScopeURI、normalizeScopeWorkspaceRoots、selectWorkspaceRootForTarget、ensurePathWithinWorkspaceRoots、shardIndexForKey）
- `cmd/mcp-orch/store/taskdag/store_lease.go`（AcquireWorkerLease、RenewWorkerLease、ReleaseWorkerLease）
- `cmd/mcp-orch/store/sqlc/task_dag_worker_lease.sql.go`（AcquireTaskDagWorkerLease、RenewTaskDagWorkerLease、ReleaseTaskDagWorkerLease SQL）
- `cmd/mcp-orch/store/commandcard/store.go`（Get/Upsert/List/Delete/InsertVersion/ListVersions、timePtr）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `scope.go:185-195` canonicalScopeURI | 静默 | `absolutePathFromURI` 失败时静默返回原 trimmed URI | 无效 URI 被原样保留，下游解析必失败但错误来源被掩盖 | Fail-Fast: 返回 error 让 ResolveLSPToolScope 拒绝错误输入 |
| `scope.go:197-214` canonicalScopePath | 静默+兜底 | 4 层 fallback：file://解析→base join→NormalizeAbsolutePath→filepath.Clean | 最后兜底 `filepath.Clean(trimmed)` 可能返回相对路径，违反「canonical 必返绝对路径」隐式契约 | 加最终断言 `if !filepath.IsAbs(...) return error` |
| `scope.go:220-228` normalizeScopeWorkspaceRoots | 静默 | `add()` 中 `normalized == "" \|\| !filepath.IsAbs(normalized)` 时静默 return | 用户传入的相对/无效 workspace root 被静默丢弃，无日志 | 至少 Warn 日志带丢弃的原值 |
| `store_lease.go:9-21` AcquireWorkerLease | 弱契约 | 返回 int64（rowsAffected），调用方需要知道「1=成功，0=未抢到」 | 隐式契约。新加入开发不知 0 是 normal-fail 而非 error，可能误判为 bug | 改返回 `(acquired bool, err error)` 显式表达；或 named return + doc |
| `store_lease.go:23-35` RenewWorkerLease | 弱契约+静默 | 同上 rowsAffected 隐式契约。SQL `lease_expires_at >= NOW()` 过滤——lease 已过期时 renew 静默返 0 行 | worker 不知道自己的 lease 已被回收，继续以为自己持有锁工作 | 0 行视为 lease lost，应在 store 层判定后返回 `ErrLeaseExpired` |
| `store_lease.go:37-45` ReleaseWorkerLease | 静默 | 只看 SQL error，不检查 rowsAffected | owner_id 错误时 DELETE 0 行但 no error，调用方误以为释放成功 | 0 行返回 `ErrLeaseNotOwned` |
| `task_dag_worker_lease.sql.go:14-22` Acquire SQL | 弱契约 | ON CONFLICT WHERE 同时检查 `lease_expires_at < NOW() OR owner_id = EXCLUDED.owner_id` | 失败时只能区分「没抢到」，不能区分「lease 仍有效持有人不是我」vs「row 不存在但 insert 失败」 | SQL 层加 RETURNING 子句和具体状态码 |
| `commandcard/store.go:144-157` timePtr | 静默 | 类型 switch `default: return nil` | sqlc 升级后 row.LastRunAt 类型变更，timePtr 静默返 nil 不报错 | `default: panic` 或返回 error 让类型不匹配早期暴露 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `scope.go:325-331` canonicalAbsoluteTargetPath | `filepath.EvalSymlinks` 在 NFS / 网络挂载文件系统上可能阻塞秒级 | 加 timeout context + 慢调用 Warn（>100ms） |
| `store_lease.go:9-45` lease 操作 | 每次 lease 操作都是 DB roundtrip；高频 worker（如 100+ 节点）会打爆 DB 连接池 | 加 query duration 直方图；连接池等待时间监控 |
| `commandcard/store.go:46-56` List | filter.Limit 由调用方控制，无 hard cap | 加最大 limit 上限（如 1000），防止 list 大批量 card 阻塞 DB |
| `scope.go:240-258` selectWorkspaceRootForTarget | 线性扫描所有 root + ContainsPath（可能涉及 stat） | roots 少时 OK；roots > 50 时改预排序按长度降序 + 短路 |
| `scope.go:67-91` ResolveLSPToolScope | 每次 LSP 工具调用都走完整 canonicalize + buildKey 流程 | 加 LRU 缓存（key=hash(LSPToolScope)）；命中率应 >90% |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `scope.go:191-194` canonicalScopeURI | absolutePathFromURI 失败静默返原值 |
| `scope.go:210-213` canonicalScopePath | NormalizeAbsolutePath 失败静默走 filepath.Clean fallback |
| `scope.go:221-223` normalizeScopeWorkspaceRoots | 非绝对路径静默丢弃 |
| `scope.go:128-133` canonicalize TargetURI fallback | absolutePathFromURI 失败静默不更新 TargetPath |
| `scope.go:377` shardIndexForKey | `_, _ = hash.Write(...)` 错误丢弃（FNV 不会真错，但 anti-pattern） |
| `store_lease.go:9-21` Acquire | 0 行返回，调用方不知是「他人持有」还是「DB 拒绝」 |
| `store_lease.go:23-35` Renew | lease 已过期时静默返 0 行 |
| `store_lease.go:37-45` Release | owner_id 错配时静默 0 行删除 |
| `commandcard/store.go:144-157` timePtr | unknown type 静默返 nil |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `scope.go:165-167` managerWorkspaceRoot | 4 层 fallback（WorkspaceRoot→LanguageWorkspaceRoot→ProjectRoot→CWD） |
| `scope.go:113-121` canonicalize | WorkspaceRoot/LanguageWorkspaceRoot/ProjectRoot 互相 fallback |
| `store_lease.go:9-45` | 三个 lease 操作返回值语义靠隐式约定（0 vs 1 行） |
| `task_dag_worker_lease.sql.go` | SQL 层 ON CONFLICT 条件无 RETURNING 区分失败原因 |
| `commandcard/store.go:46-56` | List filter.Limit 无上限 |

## 修复优先级

### P0（必须本周修）
1. **`store_lease.go:23-35` RenewWorkerLease 静默 lease lost**——这是分布式锁正确性的根本问题。worker renew 失败应立即停工（避免双 worker 同时操作），但当前静默 0 行让 worker 继续以为持有锁。改为 0 行 → return `ErrLeaseExpired`，让调用方走停工逻辑。
2. **`scope.go:197-214` canonicalScopePath 最终 fallback 可能返非绝对路径**——下游 `buildWorkspaceKey` 把它放进 cache key，相对路径会让缓存失效（同一逻辑路径产生不同 key）。加 `if !filepath.IsAbs(result) { return error }` 守卫。

### P1（本月）
3. `store_lease.go:37-45` ReleaseWorkerLease 0 行 → `ErrLeaseNotOwned`
4. `scope.go:185-195` canonicalScopeURI 失败 fail-fast
5. `scope.go:220-228` normalizeScopeWorkspaceRoots 非法 root 加 Warn
6. `commandcard/store.go:46` List 加 limit 硬上限

### P2（下个 sprint）
7. lease 操作整体改 typed return（`(LeaseResult, error)`），消除 int64 隐式语义
8. `scope.go` ResolveLSPToolScope 加 LRU 缓存（性能优化）
9. `commandcard/store.go:144-157` timePtr default 改 error/panic

## 边界条件

1. **lease 续约的「严重 silent failure」场景**：worker 启动时 Acquire 成功（行=1），跑了 30s 业务，准备 Renew。如果 DB 短暂不可用 + lease_interval=30s，业务跑完时 Renew SQL `lease_expires_at >= NOW()` 已过期 → 静默返 0 行。worker 见 err=nil 继续工作。同时 `WakeupReclaimer` 把这个 lease 释放，新 worker 可能 Acquire 成功 → **双 worker 同时操作**。这是分布式系统的经典失效模式。**根因是 store_lease.go:23-35 的弱契约 + SQL 层 silent miss**。
2. **Acquire 的「我自己 renew」隐藏分支**：SQL `OR owner_id = EXCLUDED.owner_id` 让同一个 owner 重复 Acquire 也能成功。这看似容错，但实际语义模糊——是续约还是抢占？建议拆为两个语义清晰的 SQL：`Acquire`（仅过期 lease）+ `Renew`（仅自己持有的 lease）。
3. **scope canonicalize 的 fallback chain 设计取舍**：line 113-121 多层 fallback 是 LSP 客户端「fault-tolerant」诉求——如果用户没传 ProjectRoot 就降级到 WorkspaceRoot。这与 fail-fast 的张力：用户错误配置可能被掩盖。但考虑到 LSP 工具是 best-effort 辅助，fail-soft 是合理选择。建议加 Debug 日志「fallback used: ProjectRoot empty, using WorkspaceRoot」让排错时可见。
4. **commandcard/store.go 整体质量**：相对其他 store 文件，这个文件结构清晰、错误包装统一（`wrapCommandCardError`）、类型映射清晰（fromCard/fromListRow/fromVersion 三个独立 mapper）。是项目内 store 层正面案例。timePtr 的 type-switch fallback 是唯一弱点。
5. **shardIndexForKey 的 FNV 哈希均匀性**：line 372-378 用 FNV-1a 32-bit 哈希。对 LSP scope key（包含 path、language 等高熵字段）分布应该足够均匀。但如果 LSPToolScope 大量来自同一 workspace，所有 key 共享 prefix，FNV 在 prefix 相同时分布会偏。建议单测覆盖（看 transport_responder_drain_test.go 之类是否已有 shard 分布测试）。
6. **scope.go 的 platformshared.NormalizeAbsolutePath 静默失败**：line 210-213 NormalizeAbsolutePath err != nil 时静默走 filepath.Clean fallback。NormalizeAbsolutePath 一般是对 macOS `/private/var` vs `/var` symlink、Windows 盘符大小写做 normalize。失败通常意味着路径不存在——这是 fall-through 到 filepath.Clean 的语义有意义（路径不存在但 cache key 仍要稳定）。可接受。

---

**本轮总结**：发现 1 个 P0 分布式正确性问题（lease renew 静默 lease-lost 可能导致双 worker），1 个 P0 路径契约问题（canonicalScopePath 最终 fallback 可能返相对路径污染 cache key）。lease 三个 API 整体设计 - int64 rowsAffected 暴露 - 是弱契约典型反例，建议改 typed return 让语义在编译期可读。commandcard/store.go 是项目正面案例。

**累计进度**：31 轮完成。cron `fd4b4728` 继续推进。
