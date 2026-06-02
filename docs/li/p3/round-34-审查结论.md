# 第 34 轮审查结论

## 审查范围

- `cmd/mcp-lsp/edit/seeksequence.go`（SeekSequence、seekSequenceBounds、seekSequenceMode、collectSequenceMatches、sequenceMatchAt、lineMatch、normalizeUnicode、normalizeEscape）
- `cmd/mcp-orch/store/sharedfile/store.go`（Upsert、Get、List、Delete、writeDiskAndDecideInline、mapSharedFile）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `sharedfile/store.go:31-56` Upsert | 数据一致性 | 先 `WriteAtomic` 写盘成功 → 后 `UpsertSharedFile` 写 DB；如果 DB 写入失败，磁盘已写但 DB 未更新 | 磁盘有新内容，DB 是旧内容 → Get 路径优先读盘（line 81-85）会返回新内容；但其他 reader 可能依赖 DB | DB 写失败时回滚磁盘（写回旧内容或删除）；或反序：先 DB 后磁盘（DB 失败磁盘不写） |
| `sharedfile/store.go:52-54` Upsert | 静默修补 | 大文件 DB content 为空时，用 `params.Content` 回填给调用方 | 是为兼容调用方期望写啥读啥的契约；但下次别人 Get 同一 path 会读盘——若磁盘已被外部修改，两次读返回不同内容 | 文档化「Upsert 返回是 echo 而非 readback」；或 Upsert 返回值不带 Content |
| `sharedfile/store.go:111-130` Delete | 数据一致性 | DB 删成功后磁盘删失败 → 返回 error 但 count > 0 | 调用方拿到 error，可能重试 → DB 已删除，重试 SQL 找不到 row（idempotent OK） | DB delete 应在事务中包磁盘 delete；或加补偿任务 |
| `sharedfile/store.go:81-94` Get | 静默 stale | 磁盘不存在 → 静默 fallback 到 DB content | 文件被外部进程/管理员删除，store 仍返回 DB 中的旧内容，调用方无感知 | 至少 Warn 日志「disk file missing, falling back to DB cache」 |
| `sharedfile/store.go:150` Upsert | 静默 | `_ = sharedfilegitignore.Ensure(cfg.CWD, nil)` 错误丢弃 | .gitignore 写入失败时 `_internal/` 不被 git 忽略，敏感文件会被 commit；问题被静默 | 至少 Warn 日志（但允许写入继续——这部分注释意图正确） |
| `seeksequence.go:100-102` lineMatch | 静默 | 未知 MatchMode `default: return false` | 添加新 mode 但未更新 switch 时静默 false——pattern 本应匹配但被静默漏掉 | 改 `panic("unhandled match mode: " + string(mode))` |
| `seeksequence.go:115-117` trimRightSpace | 弱契约 | 仅 trim ` \t\r\n` 四种字符 | 未处理 `\v`/`\f`/U+00A0 等空白；与 `strings.TrimSpace` 不一致 | 改为 `strings.TrimRightFunc(value, unicode.IsSpace)` |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `seeksequence.go:22-36` SeekSequence | 5 mode 串行尝试；最坏 5N 次扫描；对大 LLM 生成的 patch 高频调用 | 1) 加 `time.Now()` 计时 + 慢调用 Warn 2) Boyer-Moore 替代线性扫描（exact mode）3) 早期 lines/pattern 哈希预筛 |
| `seeksequence.go:60-77` collectSequenceMatches | 与 SeekSequence 同模式但收集所有匹配；多次扫全文 | 同上 |
| `sharedfile/store.go:31-56` Upsert | 同步路径：path validate → fsync 磁盘 → DB write → restore content；任一阶段慢都阻塞 caller | 加分阶段 duration 日志（validate_ms / disk_write_ms / db_write_ms） |
| `sharedfile/store.go:58-94` Get | 同步：path validate → DB query → ResolveAbs → disk read | 同上分阶段；disk read 阻塞是常见延迟点 |
| `sharedfile/store.go:81` ReadDisk | NFS/网盘读阻塞秒级；当前无 timeout | 加 timeout context；超 1s 打 Warn |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `sharedfile/store.go:81-86` | 磁盘读成功就用磁盘；不存在静默 fallback 到 DB |
| `sharedfile/store.go:150` | sharedfilegitignore.Ensure 错误丢弃 |
| `sharedfile/store.go:81` ReadDisk | 第二个返回值 `_` 丢弃（应是 size/hash）|
| `seeksequence.go:100-102` | unknown mode 静默 false |
| `seeksequence.go:42-44` seekSequenceBounds | start < 0 静默改 0（边界容忍） |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `sharedfile/store.go:31-56` Upsert | 「写完读得到」是隐式契约，但实际是 echo 模式 |
| `sharedfile/store.go:58-94` Get | DB+disk 双源读取的优先级 / 一致性语义无文档 |
| `sharedfile/store.go:111-130` Delete | DB 已删 + 盘删失败时返回 (count>0, err)，调用方需自行决定语义 |
| `seeksequence.go:88-103` lineMatch | 5 种 mode 优先级靠 allSeekModes 顺序，无文档解释为何这个顺序 |
| `seeksequence.go:155-181` normalizeEscape | `\n`→`\n` / `\t`→`\t` 不变换的设计意图注释模糊 |

## 修复优先级

### P0（必须本周修）
1. **`sharedfile/store.go:31-56` Upsert 双写顺序问题**——磁盘成功 + DB 失败导致两边不一致。改为先 DB 后磁盘（DB 失败盘不写），或加事务包装。这是数据一致性根本问题，影响多 worker 协作。
2. **`sharedfile/store.go:111-130` Delete 部分失败**——DB 删成功盘删失败时返回 (count=1, err)。调用方语义模糊。改为：盘删失败时回滚 DB（重新插入），或补偿任务异步清理盘。

### P1（本月）
3. `sharedfile/store.go:81-94` 磁盘 fallback 加 Warn 日志
4. `sharedfile/store.go:150` gitignore 错误加 Warn（保留继续语义）
5. `seeksequence.go:100-102` lineMatch default 改 panic
6. `seeksequence.go:115-117` trimRightSpace 用 unicode.IsSpace

### P2（下个 sprint）
7. `seeksequence.go:22-36` Boyer-Moore 优化 exact mode
8. `sharedfile/store.go:81` ReadDisk 第二个返回值要么用要么改 API
9. `sharedfile/store.go` 整体加分阶段 duration 日志

## 边界条件

1. **`sharedfile/store.go` 双写架构的根本风险**：Phase 3.6 引入磁盘+DB 双写，磁盘是 source of truth。但 Upsert 没有事务包装：磁盘写盘成功但 DB upsert 失败时，已经无法回滚磁盘（虽然 WriteAtomic 是原子的，但回滚到「未上次内容」需要先读 DB——而 DB 此时可能完全不可达）。建议：①如果 DB 是真正的 source of truth，反序（DB 先盘后）；②如果磁盘是 truth，加 reconciliation 任务定期对账。
2. **`sharedfile/store.go:52-54` 的 echo 行为是合理 hack**：大文件 inline threshold 触发后 DB 存空字符串，磁盘存原文。Upsert 返回值若直接用 DB 的 mapped.Content 会是空——但调用方期望「写啥读到啥」。echo 修补是对此契约的兼容。**正面案例**——但应在文档/注释中明确：「Upsert 返回是输入 echo，非 readback」。
3. **`seeksequence.go` 的 5-mode 优先级设计**：exact → trim_right → trim_both → unicode_normalized → escape_normalized。这个顺序是「精确度优先」：先尝试最严格匹配，逐步放宽。但 escape_normalized 放最后是经验之选——根据 line 153 注释「intentionally a last-resort fallback」。建议把这一设计意图提升到包级注释让未来 maintainer 不会调换顺序。
4. **`seeksequence.go` 的协程性能特征**：每次 LSP edit 工具调用都会跑一次 SeekSequence。对 5K 行文件、5 行 pattern、最坏全部 mode 都不命中时：5 * 5000 * 5 = 125K 次字符串比较 + 4 次 normalize（unicode/escape）扫描 ≈ 数 ms。本身不慢，但高并发下累积消耗 CPU。Boyer-Moore 优化能把 exact mode 从 O(NM) 降到 O(N+M)，对长 pattern 收益明显。
5. **`sharedfile/store.go` Get 的 stale read 风险**：磁盘文件被外部删除（备份恢复、磁盘满清理）但 DB 还有 row。当前行为返回 DB content（可能是空字符串如果是大文件）。调用方可能误以为内容为空 + 文件存在。这与 Upsert 的 echo 模式冲突——Upsert 时大文件 echo 完整 content，Get 时大文件磁盘缺失返空。建议加显式状态字段：`SharedFile.ContentSource enum { Disk, DBInline, DBOnly }`。
6. **`seeksequence.go` 整体质量较高**：5 个 mode 的渐进放宽是经过深思的设计；normalizeUnicode 用 NFC 处理 combining characters 是 Unicode 正确性的关键（line 138-140）。这是项目内 LSP edit 的核心算法，**正面案例**。唯一弱点是 lineMatch default 的 silent false（P1 修复）。

---

**本轮总结**：发现 2 个 P0 数据一致性问题集中在 `sharedfile/store.go`：①Upsert 双写无事务导致磁盘/DB 不一致；②Delete 部分失败返回 (count>0, err) 语义模糊。`seeksequence.go` 的 5-mode 渐进匹配是正面算法案例，但 Boyer-Moore 优化可减少协程延迟。Upsert 的 echo 修补（line 52-54）是对契约的合理 hack 但需文档化。

**累计进度**：34 轮完成。cron `fd4b4728` 继续推进。
