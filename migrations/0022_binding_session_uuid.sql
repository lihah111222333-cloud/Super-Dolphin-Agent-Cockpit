-- 0022_binding_session_uuid.sql
--
-- Add mutable session_uuid column to agent_provider_binding.
-- Stores the real provider session UUID (e.g. Claude session_id).
-- NOT protected by the immutable trigger — can be updated after initial creation.

ALTER TABLE agent_provider_binding
  ADD COLUMN IF NOT EXISTS session_uuid TEXT NOT NULL DEFAULT '';
