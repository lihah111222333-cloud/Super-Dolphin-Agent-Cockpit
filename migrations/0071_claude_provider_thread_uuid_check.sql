-- Enforce Claude provider_thread_id hygiene without constraining Codex.
--
-- provider_thread_id is the provider-native resume identifier. Claude CLI
-- accepts only canonical session UUIDs for --resume; agent_id/public thread
-- placeholders make Claude start a fresh session and appear to lose history.
--
-- This migration first repairs rows that can be repaired from session_uuid,
-- then clears unrecoverable agent_id placeholders, and finally adds a
-- Claude-only CHECK constraint. Codex rows are intentionally left untouched.

BEGIN;

UPDATE agent_provider_binding
SET provider_thread_id = session_uuid
WHERE provider = 'claude'
  AND (provider_thread_id = '' OR provider_thread_id = agent_id)
  AND session_uuid ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';

UPDATE agent_provider_binding
SET provider_thread_id = ''
WHERE provider = 'claude'
  AND provider_thread_id = agent_id
  AND NOT (
      session_uuid ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  );

ALTER TABLE agent_provider_binding
ADD CONSTRAINT chk_claude_provider_thread_uuid
CHECK (
  provider <> 'claude'
  OR provider_thread_id = ''
  OR provider_thread_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
);

COMMIT;
