ALTER TABLE hook_pending_reviews
    ADD COLUMN IF NOT EXISTS resolved_by TEXT NOT NULL DEFAULT '';
