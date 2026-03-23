-- hook_pending_reviews: stores hook calls awaiting human/subscriber review.
-- Status lifecycle: pending -> resolved | cancelled | expired

CREATE TABLE IF NOT EXISTS hook_pending_reviews (
    hook_call_id    TEXT PRIMARY KEY,
    topic           TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    thread_id       TEXT NOT NULL DEFAULT '',
    turn_id         TEXT NOT NULL DEFAULT '',
    subscriber_lease TEXT NOT NULL DEFAULT '',
    payload         TEXT NOT NULL DEFAULT '{}',
    decision        TEXT NOT NULL DEFAULT '',
    reason          TEXT NOT NULL DEFAULT '',
    default_action  TEXT NOT NULL DEFAULT 'reject',
    status          TEXT NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deadline_at     TIMESTAMPTZ NOT NULL,
    resolved_at     TIMESTAMPTZ,
    idempotency_key TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_hook_pending_agent ON hook_pending_reviews(agent_id, status);
CREATE INDEX IF NOT EXISTS idx_hook_pending_deadline ON hook_pending_reviews(deadline_at) WHERE status = 'pending';
