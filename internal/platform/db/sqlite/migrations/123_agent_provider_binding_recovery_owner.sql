-- Go migration owner: migrateAgentProviderBindingRecoveryOwner.
-- It canonicalizes historical provider UUIDs, detects cross-field ownership
-- collisions, adds provider_recovery_home, backfills Codex instance owners,
-- and atomically rebuilds the binding identity trigger.
ALTER TABLE agent_provider_binding
ADD COLUMN provider_recovery_home TEXT NOT NULL DEFAULT '';
