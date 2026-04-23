-- 0054_seed_main_default_match_when.sql — 让 main/default 参与 match_when 自动路由。
--
-- 语义回顾（与 0053 对齐）:
--   match_when IS NULL  → 不参与 auto-route（opt-out）
--   match_when = '{}'   → 永远匹配（参与竞争，不带筛选条件）
--   priority INT        → 降序排序；越大越先被挑中
--
-- main/default 是系统最兜底的模板；在没有 pin / 分类器命中 / 其它
-- match_when 命中的情况下应该兜住所有新线程。把它设成 match_when='{}'
-- + priority=0，这样 router 的 auto-route 挡位会把它当作「永远可选的
-- 最低优先级候选」——任何其它 priority>0 的命中都会先赢，没人命中时
-- main/default 兜底一条，和 pickRoutedTemplate 里硬编码的最终 fallback
-- 行为一致，但路径统一收束到 auto-route，一目了然。
--
-- Idempotent: 只对 main/default 且 match_when 仍为 NULL 的行做 UPDATE，
-- 不会覆盖操作员后续的手动调整。

BEGIN;

UPDATE public.prompt_templates
   SET match_when = '{}'::jsonb,
       priority   = 0
 WHERE prompt_key = 'main/default'
   AND match_when IS NULL;

COMMIT;
