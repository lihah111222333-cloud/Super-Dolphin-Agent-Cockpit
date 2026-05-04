ALTER TABLE agent_threads
    ADD COLUMN IF NOT EXISTS manually_renamed boolean NOT NULL DEFAULT false;
