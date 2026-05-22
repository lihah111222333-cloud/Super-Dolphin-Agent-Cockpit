Migration 规则：编号保持单调，不重编已出现的缺号；每个 migration 必须可重复运行或明确依赖前序状态。

写法：
- DDL 用 `IF NOT EXISTS` / `IF EXISTS`，数据 seed 用 `INSERT ... ON CONFLICT` 或 guarded `UPDATE`。
- 不要在 Codex 执行任务时直接 apply 生产/本地 DB migration，除非用户明确要求。
- 回滚说明要具体到 DELETE/UPDATE/ALTER 的反向动作。
- seed 数据不要覆盖用户手工编辑；需要更新时加 `WHERE manually_edited = FALSE` 或仅填空值。
