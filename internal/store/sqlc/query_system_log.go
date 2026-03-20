package sqlc

import "context"

const (
	insertSystemLogSQL     = `INSERT INTO system_logs (ts, level, logger, message, raw) VALUES (NOW(), $1, $2, $3, $4);`
	listSystemLogsSQL      = `SELECT id, ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra FROM system_logs WHERE ($1::text = '' OR level = $1) AND ($2::text = '' OR logger = $2) AND ($3::text = '' OR source = $3) AND ($4::text = '' OR component = $4) AND ($5::text = '' OR agent_id = $5) AND ($6::text = '' OR thread_id = $6) AND ($7::text = '' OR event_type = $7) AND ($8::text = '' OR tool_name = $8) AND ($9::text = '' OR level ILIKE '%' || $9 || '%' OR logger ILIKE '%' || $9 || '%' OR message ILIKE '%' || $9 || '%' OR raw ILIKE '%' || $9 || '%' OR source ILIKE '%' || $9 || '%' OR component ILIKE '%' || $9 || '%') ORDER BY ts DESC, id DESC LIMIT $10;`
	listAILogSystemLogsSQL = `SELECT id, ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra FROM system_logs WHERE ($1::text = '' OR message ILIKE '%' || $1 || '%') ORDER BY ts DESC, id DESC LIMIT $2;`
)

func scanSystemLog(row rowScanner) (SystemLog, error) {
	var item SystemLog
	err := row.Scan(&item.ID, &item.Ts, &item.Level, &item.Logger, &item.Message, &item.Raw, &item.Source, &item.Component, &item.AgentID, &item.ThreadID, &item.TraceID, &item.EventType, &item.ToolName, &item.DurationMs, &item.Extra)
	return item, err
}

func (q *Queries) InsertSystemLog(ctx context.Context, arg InsertSystemLogParams) error {
	return q.exec(ctx, insertSystemLogSQL, arg.Level, arg.Logger, arg.Message, arg.Raw)
}

func (q *Queries) ListSystemLogs(ctx context.Context, arg ListSystemLogsParams) ([]SystemLog, error) {
	return queryMany(ctx, q, listSystemLogsSQL, scanSystemLog, arg.Level, arg.Logger, arg.Source, arg.Component, arg.AgentID, arg.ThreadID, arg.EventType, arg.ToolName, arg.Keyword, arg.Limit)
}

func (q *Queries) ListAILogSystemLogs(ctx context.Context, arg ListAILogSystemLogsParams) ([]SystemLog, error) {
	return queryMany(ctx, q, listAILogSystemLogsSQL, scanSystemLog, arg.Keyword, arg.Limit)
}
