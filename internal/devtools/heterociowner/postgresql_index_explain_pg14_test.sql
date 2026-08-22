-- PostgreSQL 14 executable index-plan fixture.
-- Apply heterociowner.PostgreSQLOwnerDDL and heterociexec.PostgreSQLReceiptDDL first.
-- The fixture performs EXPLAIN only; it does not execute the mutations.
BEGIN;
SET LOCAL enable_seqscan = off;

CREATE FUNCTION pg_temp.assert_index_plan(label TEXT, query_text TEXT, expected_index TEXT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    plan JSON;
BEGIN
    EXECUTE 'EXPLAIN (FORMAT JSON, COSTS OFF) ' || query_text INTO plan;
    IF position(expected_index IN plan::text) = 0 THEN
        RAISE EXCEPTION '% did not use %: %', label, expected_index, plan::text;
    END IF;
END;
$$;

SELECT pg_temp.assert_index_plan(
    'retire active owner epoch',
    $query$UPDATE heteroci_owner.owner_epochs
           SET retired_at_unix_milli = 1
           WHERE scope = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'
             AND retired_at_unix_milli IS NULL$query$,
    'idx_heteroci_owner_epochs_active_scope');

SELECT pg_temp.assert_index_plan(
    'read active reservations',
    $query$SELECT node_id, probe_identity, probe_boot_id, cpu_milli, memory_bytes, max_concurrency
           FROM heteroci_owner.routing_reservations
           WHERE scope = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'
             AND owner_epoch = 1
             AND state IN ('planned', 'started')$query$,
    'idx_heteroci_owner_routing_reservations_active_scope_epoch');

SELECT pg_temp.assert_index_plan(
    'locate run across epochs',
    $query$SELECT owner_epoch, run_id
           FROM heteroci_owner.routing_runs
           WHERE scope = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'
             AND run_id = 'run'
           ORDER BY owner_epoch$query$,
    'idx_heteroci_owner_routing_runs_scope_run_epoch');

SELECT pg_temp.assert_index_plan(
    'read exact planned lease',
    $query$SELECT lease_bytes, state, started_bytes, terminal_bytes, last_revision
           FROM heteroci_owner.routing_leases
           WHERE scope = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'
             AND owner_epoch = 1 AND run_id = 'run' AND unit_id = 'unit'
             AND state = 'planned'$query$,
    'routing_leases_pkey');

SELECT pg_temp.assert_index_plan(
    'read run terminal state in unit order',
    $query$SELECT state
           FROM heteroci_owner.routing_leases
           WHERE scope = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'
             AND owner_epoch = 1 AND run_id = 'run'
           ORDER BY unit_id$query$,
    'routing_leases_pkey');

SELECT pg_temp.assert_index_plan(
    'read exact receipt',
    $query$SELECT schema_version, command_digest, state, claim_bytes, claim_digest, result_bytes, result_digest
           FROM heteroci_receipt.batch_receipts
           WHERE batch_id = '0000000000000000000000000000000000000000000000000000000000000000'
             AND material_digest = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'$query$,
    'batch_receipts_pkey');

SELECT pg_temp.assert_index_plan(
    'write exact terminal receipt',
    $query$UPDATE heteroci_receipt.batch_receipts
           SET state = 'terminal', result_bytes = decode('00', 'hex'),
               result_digest = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'
           WHERE batch_id = '0000000000000000000000000000000000000000000000000000000000000000'
             AND material_digest = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'
             AND command_digest = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'
             AND state = 'claimed' AND result_bytes IS NULL AND result_digest IS NULL$query$,
    'batch_receipts_pkey');

ROLLBACK;
