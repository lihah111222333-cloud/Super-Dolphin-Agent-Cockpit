# Prompt Seed Migration 升级路径规约

> 日期：2026-05-12 | 状态：✅ Accepted | 决策者：项目维护者 | 相关：migrations/0036 / 0038 / 0039 / 0050 / 0052 / 0055 / 0056 / 0057 / 0084、ADR-007 / ADR-009

## 1. 背景

仓库历史 prompt seed migration 在「冲突时怎么办」上**摇摆不定**：

- **DO NOTHING 派**：0036 / 0050 / 0084（含本月 F7.1）。优点：不覆盖人工微调；缺点：seed 内容更新无法生效，必须手工 DELETE + 重跑。
- **DO UPDATE 派**：0038 / 0039 / 0052 / 0055 / 0056 / 0057。优点：seed 升级即生效；缺点：会**悄悄抹掉**人工微调内容。

两条路都用过、都出过事，无统一规约 → 下次再有 prompt seed 时全靠作者口味，定时炸弹。

## 2. 决策

**新增 prompt seed migration 一律走 DO UPDATE + `manually_edited` flag 防覆盖。**

### 2.1 表结构补强（F7.3 前置必需落地，未跟进不能走 DO UPDATE 模板）

已在 `migrations/0086_prompt_template_manually_edited.sql` 落地，状态 **✅ 已实装**。**2026-05-12 编号调整**：原计划 0085，但 F7.2（AI 设计师英文版 seed）已先占 0085，F7.3 的 manually_edited 列让位到 0086、配套 seed 让位到 0087。`prompt_templates` 表加列：

```sql
ALTER TABLE public.prompt_templates
  ADD COLUMN IF NOT EXISTS manually_edited BOOLEAN NOT NULL DEFAULT FALSE;
```

含义：
- `FALSE`（默认）：本 prompt 内容由 seed migration 维护，DO UPDATE 可以覆盖。
- `TRUE`：UI / 后台已经手工微调，seed migration 必须跳过覆盖。

### 2.2 Seed migration 模板

```sql
INSERT INTO public.prompt_templates (
    prompt_key, title, ..., manually_edited, created_at, updated_at
) VALUES (
    'main/xxx_zh', '...', ..., FALSE, NOW(), NOW()
)
ON CONFLICT (prompt_key) DO UPDATE SET
    title       = EXCLUDED.title,
    prompt_text = EXCLUDED.prompt_text,
    ...,
    updated_at  = NOW()
WHERE public.prompt_templates.manually_edited = FALSE;  -- 关键守护
```

- `WHERE manually_edited = FALSE` 让 DO UPDATE 在人工微调后**自动失效**。
- 注意：`ON CONFLICT ... DO UPDATE ... WHERE` 是 PG 标准语法。条件不命中时该行 noop，**不报错**。

### 2.3 UI 写路径同步

UI / 后台改 prompt 时必须把 `manually_edited` 置 TRUE。`cmd/mcp-orch` prompt store 已扩展显式写 flag，root/internal prompt store 与 UI/后台写路径也已同步：更新已有 prompt 时置 TRUE，新增 prompt 保持默认 FALSE。

### 2.4 路由与表字段废弃补况（避免误导 F7.3 作者）

- **`router_priority` 列已被 `0044_drop_router_priority.sql` 移除**。harness 现按 **explicit agent_key** 分发，不再走 keyword router（详 0044 文件头）。**新 seed migration 不要写 router_priority 列**。
- **`tags` jsonb 列当前只供 UI / admin 列表筛选使用，不参与路由命中**。F7.1 / 0084 写的 23 个中文 tag 本质上是文档性存在（archtest 守住内容不被抽干，不代表路由路径走过该集合）。F7.3 仍可写 tags，但不要设计“靠 tag 命中”的集成测试。
- **`variables` jsonb 列未被 `AgentExecutor` 消费**。`prompt_text` 中不要写 `{{var}}` 替换符，会原样照送给 LLM。节点级参数化走 `AgentNodeConfig.first_turn` 覆盖（本次调用一次性指令）或 `cfg.Inputs.FromNodes` / `FromSharedfiles`（上游数据注入）。

## 3. 已有 seed 的处理

- **0084（F7.1）已用 DO NOTHING**：按新规约**不回头改 0084**（DELETE+重跑成本高、当前内容稳定，archtest 已守护关键 keyword）。下次需要刷新 0084 内容时，**写新 migration**（DO UPDATE 模式）覆盖。
- 历史的 DO UPDATE seeds（0038/0039/0052/0055/0056/0057）当前没有 `manually_edited` 守护 → 列入 follow-up：下一 migration 一次性给所有「曾被 DO UPDATE 过的 prompt」加 manually_edited 列 + 回填 FALSE。

## 4. 不选 DO NOTHING 派的理由

DO NOTHING + 「升级走新 migration（DELETE + INSERT）」也是候选方案。拒绝理由：

- 每次升级都要写 DELETE，runtime 期间会有空窗（DELETE 后、INSERT 前 prompt 查不到）。
- DELETE 串行级联：若 prompt 被引用（FK / 历史 prompt_versions），DELETE 会失败或残留孤儿数据。
- DO UPDATE + manually_edited flag 是**幂等的、原子的、可回滚的**——升级失败回滚直接 ROLLBACK 事务，prompt 内容还是旧的，业务无感知。

## 5. 触发再议条件

- 若 manually_edited flag 在 UI 侧无法可靠维护（用户手工改 DB 不走 UI 改了 prompt 但 flag 仍 FALSE）→ 加 trigger 在 UPDATE prompt_templates 时自动置 TRUE。
- 若 prompt seed 数量超 50 条 → 评估走单独的「prompt seed manifest」机制（JSON / YAML 声明式 + go embed + 启动时 reconcile），不再每次写 SQL migration。
