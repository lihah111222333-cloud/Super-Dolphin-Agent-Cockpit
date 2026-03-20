-- name: InsertTaskTrace :one
INSERT INTO task_traces (
    trace_id, span_id, parent_span_id, span_name, component,
    input_payload, output_payload, status, error_text, duration_ms, metadata, started_at, finished_at
) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11::jsonb, NOW(), NULL)
RETURNING id, trace_id, span_id, parent_span_id, span_name, component, status, input_payload, output_payload, error_text, metadata, started_at, finished_at, duration_ms;

-- name: ListTaskTraces :many
SELECT id, trace_id, span_id, parent_span_id, span_name, component, status, input_payload, output_payload, error_text, metadata, started_at, finished_at, duration_ms
FROM task_traces
WHERE ($1::text = '' OR component = $1)
  AND ($2::timestamptz IS NULL OR started_at >= $2)
  AND ($3::text = ''
    OR span_name ILIKE '%' || $3 || '%'
    OR status ILIKE '%' || $3 || '%')
ORDER BY started_at DESC
LIMIT $4;
