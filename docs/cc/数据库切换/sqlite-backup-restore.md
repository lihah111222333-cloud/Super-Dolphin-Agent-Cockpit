# SQLite 备份与恢复

本文件是 SQLite 切换 Task14 的发布前备份/恢复约束。SQLite 运行在 WAL 模式时，备份不能只复制主 `.db` 文件，除非已经先完成一致性 checkpoint。

## 备份

推荐顺序：

1. 如果应用仍在写入，优先使用 SQLite Online Backup API 生成一致快照。
2. 如果不能使用 Online Backup API，必须先 quiesce writes 或持有备份锁。
3. 对源库执行 `PRAGMA wal_checkpoint(TRUNCATE)` 并确认 `busy = 0`。
4. checkpoint 成功后可以复制主 `.db` 文件；若没有 checkpoint，则必须把 `.db/.wal/.shm` 作为同一快照一起复制。

禁止把“只复制 `.db`”作为运行中应用的默认备份方案。该方案可能遗漏 WAL 中尚未 checkpoint 的提交。

## 恢复 Smoke

恢复后的 SQLite 文件必须先按下列顺序验证，再进入业务读写：

1. `PRAGMA integrity_check` 必须返回 `ok`。
2. `PRAGMA foreign_key_check` 必须没有结果行。
3. schema version gate 必须满足当前最低版本。
4. 读写 smoke 必须覆盖 thread、prompt、cron、DAG，并确认 DAG wakeup/session insight/system log 等发布 gate 关键表仍可查询。

当前自动化 smoke 在 `internal/platform/db/sqlite/backup_restore_smoke_test.go` 中执行上述顺序：先写入 fixture，checkpoint 后复制，恢复打开，再验证 integrity、foreign key、schema floor，最后写入并读取 thread、prompt、cron 和 DAG 基础数据。

## 发布判定

备份/恢复属于 G14 P1 gate。P0 gate 全部通过前不得进入 RC；G14 未关闭前不得正式发布。任何 checkpoint busy、integrity failure、foreign key violation、schema gate failure 或恢复后基础读写失败都必须 fail-fast，不允许静默降级到空库或 PostgreSQL。
