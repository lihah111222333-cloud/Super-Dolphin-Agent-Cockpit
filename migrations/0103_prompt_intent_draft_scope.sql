BEGIN;

ALTER TABLE prompt_intent_drafts
    ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'project';

UPDATE prompt_intent_drafts
SET scope = 'project'
WHERE scope NOT IN ('project', 'global');

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'prompt_intent_drafts_scope_check'
          AND conrelid = 'prompt_intent_drafts'::regclass
    ) THEN
        ALTER TABLE prompt_intent_drafts
            ADD CONSTRAINT prompt_intent_drafts_scope_check
            CHECK (scope IN ('project', 'global'));
    END IF;
END $$;

COMMIT;
