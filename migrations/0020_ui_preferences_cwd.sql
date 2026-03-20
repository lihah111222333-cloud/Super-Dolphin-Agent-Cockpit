-- 0020_ui_preferences_cwd.sql — 为 UI 偏好增加 cwd 维度，按项目路径隔离。

ALTER TABLE ui_preferences
    ADD COLUMN IF NOT EXISTS cwd TEXT NOT NULL DEFAULT '';

UPDATE ui_preferences
SET cwd = ''
WHERE cwd IS NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'ui_preferences_pkey'
          AND conrelid = 'ui_preferences'::regclass
    ) THEN
        ALTER TABLE ui_preferences DROP CONSTRAINT ui_preferences_pkey;
    END IF;
END $$;

DO $$
BEGIN
    ALTER TABLE ui_preferences
        ADD CONSTRAINT ui_preferences_pkey PRIMARY KEY (cwd, key);
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS idx_ui_preferences_key ON ui_preferences(key);
