-- Retention candidates are read-only facts used by the event tombstone owner.
-- The candidate scan includes cursors at or below expected_floor so malformed
-- lifecycle rows are not silently ignored; the verifier treats equality as a
-- pin and older cursors as stale non-pins.

-- name: ListOrchestrationRetentionPinCandidates :many
WITH candidates AS (
    SELECT
        'access_wait' AS pin_kind,
        wait_handle_id AS pin_key,
        source_id,
        source_epoch,
        lane,
        after_source_sequence,
        status AS lifecycle_status,
        '' AS operation_status,
        schema_version
    FROM orchestration_event_wait_handles AS h
    WHERE h.source_id = sqlc.arg(source_id)
      AND h.source_epoch = sqlc.arg(source_epoch)
      AND h.lane = sqlc.arg(lane)
      AND h.after_source_sequence <= sqlc.arg(expected_floor)

    UNION ALL

    SELECT
        'wait_operation' AS pin_kind,
        j.target_key AS pin_key,
        j.source_id,
        j.source_epoch,
        j.lane,
        j.after_source_sequence,
        j.phase AS lifecycle_status,
        o.status AS operation_status,
        j.schema_version
    FROM orchestration_wait_operation_target_journal AS j
    LEFT JOIN orchestration_wait_operations AS o
      ON o.operation_key = j.operation_key
     AND o.operation_digest = j.operation_digest
    WHERE j.source_id = sqlc.arg(source_id)
      AND j.source_epoch = sqlc.arg(source_epoch)
      AND j.lane = sqlc.arg(lane)
      AND j.after_source_sequence <= sqlc.arg(expected_floor)
      AND NOT EXISTS (
          SELECT 1
          FROM orchestration_wait_operation_target_journal AS newer
          WHERE newer.operation_key = j.operation_key
            AND newer.target_key = j.target_key
            AND newer.version > j.version
      )
)
SELECT
    pin_kind,
    pin_key,
    source_id,
    source_epoch,
    lane,
    after_source_sequence,
    lifecycle_status,
    operation_status,
    schema_version
FROM candidates
ORDER BY pin_kind ASC, pin_key ASC
LIMIT sqlc.arg(limit);
