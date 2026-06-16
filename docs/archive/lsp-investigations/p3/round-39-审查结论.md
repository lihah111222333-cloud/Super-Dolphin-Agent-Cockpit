# 第 39 轮审查结论

## 审查范围

- `cmd/mcp-orch/store/sqlc/command_card.sql.go`（DeleteCommandCard、GetCommandCard、InsertCommandCardVersion、ListCommandCardVersions、ListCommandCards、UpsertCommandCard）
- `cmd/mcp-orch/store/sqlc/shared_file.sql.go`（DeleteSharedFile、GetSharedFile、ListSharedFiles、UpsertSharedFile）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `command_card.sql.go:131-147` ListCommandCards | 性能/资源 | LEFT JOIN subquery `SELECT card_key, MAX(...), COUNT(*) FROM command_card_runs GROUP BY card_key` 无 WHERE 限制 | 每次 list 查询都对 command_card_runs 全表 GROUP BY；表大时 query 慢且内存高 | 改为窗口函数 + LIMIT 后再 JOIN；或物化视图缓存 stats |
| `command_card.sql.go:140-144` ILIKE 4 列 OR | 性能 | `card_key/title/description/command_template` 4 列 OR ILIKE | 4 次 lower compare + seq scan；> 10K 行时慢 | 加 trigram GIN 索引 |
| `command_card.sql.go:146` LIMIT $2 | 弱契约 | LIMIT 由 caller 控制无硬上限 | 同 round 38 OOM 风险 | SQL 层加 `LIMIT LEAST($2, 1000)` |
| `command_card.sql.go:89-95` ListCommandCardVersions | 性能/资源 | 无 LIMIT —— 返回某 card 的全部 version | 长期使用的 card 可能积累成百上千 version；返回所有版本 | 加 LIMIT + offset 分页 |
| `shared_file.sql.go:44-50` ListSharedFiles | 弱契约 | LIMIT $2 由 caller 控制无硬上限 | 同上 | SQL 层加硬上限 |
| `shared_file.sql.go:47` ILIKE path | 性能 | 单列 ILIKE %prefix% —— 无法用 B-tree 前缀索引 | 大量文件时 seq scan | 改为 `path LIKE $1 \|\| '%'`（前缀匹配走 B-tree）；或 path 加 trigram |
| `command_card.sql.go:14-25` DeleteCommandCard | 弱契约 | 返回 `(int64, error)`，0 行不报错 | caller 删除不存在的 card 不知道（需自己判断 0 行）| 调用方明确「找不到也 OK」时无问题；但与 store 层语义需对齐 |
| `shared_file.sql.go:12-22` DeleteSharedFile | 弱契约 | 同上 | 同上（且第34轮已发现：盘删失败但 DB 删成功语义不清）| 同上 |
| `command_card.sql.go:53-87` InsertCommandCardVersion | 弱契约 | 无幂等性保证 | caller 重复调用会插入重复 version row | 加 unique 约束（card_key + source_updated_at）或 ON CONFLICT |
| `command_card.sql.go:206-220` UpsertCommandCard | 弱契约 | ON CONFLICT 时 `created_by` 不更新（line 211-218 SET 中无 created_by），保留原 creator | 这是有意的——但 caller 不知道 created_by 在 update 路径被静默忽略 | 文档化；或在 store 层校验 caller 不应在 update 时传不同 created_by |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `command_card.sql.go:131-147` ListCommandCards | LEFT JOIN GROUP BY 在 command_card_runs 全表 | 加 EXPLAIN ANALYZE 监控；运行时记录 query duration > 100ms 打 Warn |
| `command_card.sql.go:89-95` ListCommandCardVersions 无 LIMIT | 大版本数 card 的 list 慢 | 行数监控；> 1000 时拒绝（Fail-Fast） |
| `shared_file.sql.go:47` 中缀 ILIKE | %x% 模式无法用前缀索引 | trigram 索引 + EXPLAIN |
| `command_card.sql.go:53-58` InsertCommandCardVersion | 无 ON CONFLICT，重复插入引发 unique violation 错误 | 应用层加先 SELECT 检查（但有竞态）；或 SQL 加 ON CONFLICT DO NOTHING |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `command_card.sql.go:140-144` | filter $1 空字符串视为「不过滤」 |
| `shared_file.sql.go:47` | 同上 |
| `command_card.sql.go:14-25` | DeleteCommandCard 0 行不报错 |
| `shared_file.sql.go:12-22` | 同上 |
| `command_card.sql.go:206-220` | UpsertCommandCard ON CONFLICT 时 created_by 不更新（静默保留旧值） |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `command_card.sql.go:149-152, shared_file.sql.go:52-55` | Column1 命名无业务含义 |
| `command_card.sql.go:223-233` UpsertCommandCardParams | Column5 无业务命名（实际是 ArgsSchema） |
| `command_card.sql.go:60-71` InsertVersionParams | 同上 Column5 |
| `command_card.sql.go:53-58` InsertCommandCardVersion | 无 ON CONFLICT，幂等性靠调用方保证 |
| `command_card.sql.go:14-25, shared_file.sql.go:12-22` Delete | 0 行 vs error 区分靠调用方判断 |

## 修复优先级

### P0（必须本周修）
1. **`command_card.sql.go:131-147` ListCommandCards 全表 GROUP BY**——每次 list 都对 command_card_runs 全表聚合。command_card_runs 是高写入率表（每次 command 执行加一行），生产环境会快速积累。LEFT JOIN 在主查询每行触发一次 subquery（PostgreSQL 优化器可能优化但无保证）。改为：①窗口函数；②命中索引的 lateral join；③物化视图。
2. **`command_card.sql.go:89-95` ListCommandCardVersions 无 LIMIT**——单 card 的 versions 可能积累很多（每次 update 一个 version）。无 LIMIT 直接返全部。生产用户 30 天后可能有几百个 version，每次 list 拖慢应用。

### P1（本月）
3. `command_card.sql.go:131, shared_file.sql.go:44` 加 SQL 层硬 LIMIT
4. `command_card.sql.go:53-58` 加 ON CONFLICT 或 unique 约束
5. `shared_file.sql.go:47` ILIKE 改前缀匹配 + B-tree 索引；或 trigram
6. `command_card.sql.go:140-144` 4 列 ILIKE 加 trigram 索引

### P2（下个 sprint）
7. `command_card.sql.go:206-220` ON CONFLICT created_by 行为文档化
8. `Column1/5` 命名通过 sqlc.yaml 配置或 SQL 命名占位符改善
9. `Delete*` 0 行语义在 store 层统一处理

## 边界条件

1. **`command_card.sql.go:131-147` LEFT JOIN 设计意图**：subquery 计算每个 card 的 last_run_at 和 run_count 是 UI 展示需要。从 UX 角度合理（用户想看每个 card 的运行统计）。但从性能角度，stats 聚合应该缓存——不应在每次 list 时即时计算。**P0 因为这是 list_command_cards 的 hot path，UI 列表频繁触发**。生产环境 command_card_runs 增长后 list 性能急剧恶化。
2. **`command_card.sql.go:89-95` ListCommandCardVersions 无 LIMIT 的设计取舍**：调用方（store/commandcard/store.go:78-88 ListVersions）只接受一个 cardKey 参数，没有 limit/offset 入参。这是 contract 层（contract.go:40 `ListVersions(ctx, cardKey)`）就没设计分页。补救：①contract 改加 limit/offset（破坏性变更）；②SQL 加默认 LIMIT 100（可能截断）。建议第一种，因为分页能力是基本需求。
3. **`command_card.sql.go:206-220` UpsertCommandCard ON CONFLICT 不更新 created_by 的合理性**：这是 audit trail 的标准模式——created_by 一旦设置不应改变。SQL `SET title = ..., updated_by = ...` 中故意没有 `created_by`。这是合理设计，但**对调用方不透明**——caller 在 update 路径传任意 created_by 都会被静默忽略。建议加注释或 store 层校验。
4. **`shared_file.sql.go:47` ILIKE 中缀匹配的设计意图**：%pattern% 让用户能搜索路径中任何位置的关键词（如搜 "config" 找到 ".agnet/shared/config.json"）。这是 UX 友好的，但牺牲性能。生产环境用户主要按前缀搜索（搜目录），中缀搜索是少数场景。可以用复合策略：默认前缀匹配（B-tree 索引快），feature flag 启用中缀（接受性能成本）。
5. **`command_card.sql.go:53-58` InsertCommandCardVersion 缺幂等性**：用例「version 已存在不重新插入」需要应用层先 SELECT 检查（有竞态）或 SQL 加 ON CONFLICT。当前实现下，调用方重复插入会得到 unique violation 错误（如果有 unique 约束）或重复行（如果没有约束）。从 SQL 看不出有 unique 约束，所以可能是后者——重复行污染数据。
6. **shared_file 与 command_card 的对称性**：两个 sql 文件结构高度对称（Get/List/Upsert/Delete），都用相同模式。这是 sqlc 项目的良好风格——统一的 CRUD 模板。**正面案例**——但 contract 层（commandcard/contract.go vs sharedfile/contract.go）的语义统一可以再加强：commandcard 有 `Get` 返回 `(*CommandCard, error)`，sharedfile 也是 `Get` 返回 `(*SharedFile, error)`，但 `Delete` 返回类型不同（commandcard error，sharedfile (int64, error)）。建议统一。

---

**本轮总结**：发现 2 个 P0 性能/资源问题：①ListCommandCards 全表 GROUP BY 是 list 慢的核心原因；②ListCommandCardVersions 无 LIMIT 直接返全部 version。`command_card.sql.go` 和 `shared_file.sql.go` 结构对称是 sqlc 良好实践模板。Upsert ON CONFLICT 不更新 created_by 是合理 audit trail 但需文档化。

**累计进度**：39 轮完成。cron `fd4b4728` 继续推进。
