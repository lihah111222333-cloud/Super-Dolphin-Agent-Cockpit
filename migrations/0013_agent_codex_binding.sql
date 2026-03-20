-- 0013_agent_codex_binding.sql — Agent ↔ Codex Thread 1:1 共生绑定表。
--
-- ╔══════════════════════════════════════════════════════════════════════╗
-- ║  ⚠️  根基约束 — 不允许修改本表的绑定逻辑和约束  ⚠️               ║
-- ║                                                                    ║
-- ║  agent_id 与 codex_thread_id 是 1:1 共生关系:                      ║
-- ║    - 一个 agent 有且仅有一个 codex thread                          ║
-- ║    - 一个 codex thread 有且仅有一个 agent                          ║
-- ║    - 创建后不可解绑，只能整行删除 (共生共灭)                       ║
-- ║    - 任何试图"偷偷换 ID"的行为都会被 UNIQUE 约束拦截               ║
-- ║                                                                    ║
-- ║  如需重新绑定: DELETE 旧行 → INSERT 新行 (显式操作, 有审计痕迹)    ║
-- ╚══════════════════════════════════════════════════════════════════════╝

CREATE TABLE IF NOT EXISTS agent_codex_binding (
    -- Go 侧 Agent 标识 (如 thread-1771520663603-2), 全局唯一。
    agent_id          TEXT    NOT NULL,

    -- Codex 侧 Thread 标识 (如 019c7786-26eb-7151-...), 全局唯一。
    codex_thread_id   TEXT    NOT NULL,

    -- Codex rollout 文件路径 (用于 resume 时定位 session 文件)。
    rollout_path      TEXT    NOT NULL DEFAULT '',

    -- 绑定创建时间 (Unix timestamp)。
    created_at        BIGINT  NOT NULL DEFAULT 0,

    -- 最后更新时间 (Unix timestamp)。
    updated_at        BIGINT  NOT NULL DEFAULT 0,

    -- ========== 约束: 锁死 1:1 关系 ==========

    -- 主键: 一个 agent 只能有一条绑定记录。
    CONSTRAINT pk_agent_codex_binding      PRIMARY KEY (agent_id),

    -- 唯一约束: 一个 codex_thread_id 只能绑定一个 agent。
    CONSTRAINT uq_codex_thread_id          UNIQUE (codex_thread_id)
);

-- 按 codex_thread_id 快速查找。
CREATE INDEX IF NOT EXISTS idx_acb_codex_thread
    ON agent_codex_binding(codex_thread_id);
