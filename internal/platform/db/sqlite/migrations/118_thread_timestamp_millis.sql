UPDATE agent_threads
SET created_at = CASE
        WHEN created_at >= 1000000000 AND created_at < 1000000000000 THEN created_at * 1000
        ELSE created_at
    END,
    updated_at = CASE
        WHEN updated_at >= 1000000000 AND updated_at < 1000000000000 THEN updated_at * 1000
        ELSE updated_at
    END,
    finished_at = CASE
        WHEN finished_at >= 1000000000 AND finished_at < 1000000000000 THEN finished_at * 1000
        ELSE finished_at
    END;

UPDATE agent_provider_binding
SET created_at = CASE
        WHEN created_at >= 1000000000 AND created_at < 1000000000000 THEN created_at * 1000
        ELSE created_at
    END,
    updated_at = CASE
        WHEN updated_at >= 1000000000 AND updated_at < 1000000000000 THEN updated_at * 1000
        ELSE updated_at
    END;

CREATE TEMP TABLE thread_timestamp_millis_guard (
    valid INTEGER NOT NULL CHECK (valid = 1)
);

INSERT INTO thread_timestamp_millis_guard(valid)
SELECT CASE WHEN NOT EXISTS (
    SELECT 1
    FROM agent_threads
    WHERE (created_at <> 0 AND (created_at < 1000000000000 OR created_at > 253402300799999))
       OR (updated_at <> 0 AND (updated_at < 1000000000000 OR updated_at > 253402300799999))
       OR (finished_at IS NOT NULL AND finished_at <> 0 AND (finished_at < 1000000000000 OR finished_at > 253402300799999))
) AND NOT EXISTS (
    SELECT 1
    FROM agent_provider_binding
    WHERE (created_at <> 0 AND (created_at < 1000000000000 OR created_at > 253402300799999))
       OR (updated_at <> 0 AND (updated_at < 1000000000000 OR updated_at > 253402300799999))
) THEN 1 ELSE 0 END;

DROP TABLE thread_timestamp_millis_guard;
