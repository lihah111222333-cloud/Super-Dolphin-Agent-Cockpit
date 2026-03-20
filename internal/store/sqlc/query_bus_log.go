package sqlc

import "context"

const (
	listBusExceptionLogsSQL = `SELECT ts, category, severity, source, tool_name, message, traceback, extra FROM bus_exception_logs WHERE ($1::text = '' OR category = $1) AND ($2::text = '' OR severity = $2) AND ($3::text = '' OR source ILIKE '%' || $3 || '%' OR tool_name ILIKE '%' || $3 || '%' OR message ILIKE '%' || $3 || '%' OR traceback ILIKE '%' || $3 || '%') ORDER BY ts DESC, id DESC LIMIT $4;`
)

func scanBusExceptionLog(row rowScanner) (BusExceptionLog, error) {
	var item BusExceptionLog
	err := row.Scan(&item.Ts, &item.Category, &item.Severity, &item.Source, &item.ToolName, &item.Message, &item.Traceback, &item.Extra)
	return item, err
}

func (q *Queries) ListBusExceptionLogs(ctx context.Context, arg ListBusExceptionLogsParams) ([]BusExceptionLog, error) {
	return queryMany(ctx, q, listBusExceptionLogsSQL, scanBusExceptionLog, arg.Category, arg.Severity, arg.Keyword, arg.Limit)
}
