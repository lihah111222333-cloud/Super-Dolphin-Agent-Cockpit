-- CWD 实例锁: 防止两个 agent-terminal 实例同时使用同一个工作目录。
-- 使用 heartbeat 机制检测崩溃实例的过期锁。

CREATE TABLE IF NOT EXISTS cwd_instance_locks (
    cwd           TEXT        NOT NULL PRIMARY KEY,
    instance_id   TEXT        NOT NULL,
    pid           INTEGER     NOT NULL DEFAULT 0,
    acquired_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    heartbeat_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cwd_instance_locks_heartbeat ON cwd_instance_locks (heartbeat_at);
