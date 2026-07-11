package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
)

const logDetailPreviewBytes = 4096

const dashboardLogDetailQuery = `
SELECT id, ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, span_id, parent_span_id, event_type, tool_name, duration_ms, extra
FROM system_logs
WHERE id = ?
LIMIT 1`

// GetLogDetail 读取单条日志详情，并只返回脱敏后的 raw/extra。
func (s *service) GetLogDetail(ctx context.Context, req LogDetailRequest) (*LogDetail, error) {
	source, err := normalizeLogDetailSource(req.Source)
	if err != nil {
		return nil, err
	}
	if req.ID <= 0 {
		return nil, errors.New("dashboard: log id is required")
	}
	if s == nil || s.dbQueries == nil {
		return nil, errors.New("dashboard: log detail query store is not configured")
	}
	rows, err := s.dbQueries.Query(ctx, dashboardLogDetailQuery, req.ID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("dashboard: log %s/%d not found", source, req.ID)
	}
	return logDetailFromRow(source, rows[0]), nil
}

func normalizeLogDetailSource(source string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case logSourceSystem:
		return logSourceSystem, nil
	case logSourceAI:
		return logSourceAI, nil
	default:
		return "", errors.New("dashboard: unsupported log detail source")
	}
}

func logDetailFromRow(source string, row map[string]any) *LogDetail {
	raw, rawTruncated, rawBytes := sanitizedLogDetailRaw(logDetailString(row, "raw"))
	extra, extraTruncated, extraBytes := sanitizedLogDetailExtra(logDetailString(row, "extra"))
	return &LogDetail{
		Source:         source,
		ID:             logDetailInt64(row, "id"),
		Timestamp:      logDetailTime(row, "ts"),
		Level:          logDetailString(row, "level"),
		Logger:         logDetailString(row, "logger"),
		Message:        logDetailString(row, "message"),
		Raw:            raw,
		RawTruncated:   rawTruncated,
		RawBytes:       rawBytes,
		Component:      logDetailString(row, "component"),
		AgentID:        logDetailString(row, "agent_id"),
		ThreadID:       logDetailString(row, "thread_id"),
		TraceID:        logDetailString(row, "trace_id"),
		SpanID:         logDetailString(row, "span_id"),
		ParentSpanID:   logDetailString(row, "parent_span_id"),
		EventType:      logDetailString(row, "event_type"),
		ToolName:       logDetailString(row, "tool_name"),
		DurationMs:     logDetailDuration(row, "duration_ms"),
		Extra:          extra,
		ExtraTruncated: extraTruncated,
		ExtraBytes:     extraBytes,
	}
}

func sanitizedLogDetailRaw(raw string) (string, bool, int64) {
	preview := observability.SafePreview(raw, logDetailPreviewBytes)
	if preview.Preview != "" {
		return preview.Preview, preview.Truncated, preview.Bytes
	}
	if preview.Truncated {
		return fmt.Sprintf("truncated bytes=%d sha256=%s", preview.Bytes, preview.SHA256), true, preview.Bytes
	}
	return "", false, preview.Bytes
}

// sanitizedLogDetailExtra 把日志 extra 转成可展示的安全 JSON。
// 超限时只返回大小和哈希，无法解析 JSON 时也只展示脱敏预览。
func sanitizedLogDetailExtra(raw string) (json.RawMessage, bool, int64) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false, 0
	}
	preview := observability.SafePreview(raw, logDetailPreviewBytes)
	if preview.Truncated {
		return mustMarshalLogDetail(map[string]any{"truncated": true, "bytes": preview.Bytes, "sha256": preview.SHA256}), true, preview.Bytes
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return mustMarshalLogDetail(preview.Preview), false, preview.Bytes
	}
	safe := make(map[string]any, len(payload))
	for key, value := range payload {
		if sanitized, ok := observability.SafeMetadataValue(key, value, logDetailPreviewBytes); ok {
			safe[key] = sanitized
			continue
		}
		safe[key] = observability.SafePreview(value, logDetailPreviewBytes).Preview
	}
	return mustMarshalLogDetail(safe), false, preview.Bytes
}

func mustMarshalLogDetail(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return raw
}

func logDetailString(row map[string]any, key string) string {
	switch value := row[key].(type) {
	case nil:
		return ""
	case string:
		return value
	case []byte:
		return string(value)
	case fmt.Stringer:
		return value.String()
	default:
		return fmt.Sprint(value)
	}
}

func logDetailTime(row map[string]any, key string) time.Time {
	switch value := row[key].(type) {
	case time.Time:
		return value
	case string:
		parsed, _ := time.Parse(time.RFC3339Nano, value)
		return parsed
	default:
		return time.Time{}
	}
}

func logDetailDuration(row map[string]any, key string) *int32 {
	value := logDetailInt64(row, key)
	if value == 0 {
		return nil
	}
	converted := int32(value)
	return &converted
}

// logDetailInt64 从查询行中读取整数字段。
// 解析失败返回 0，只用于展示字段，不参与运行时状态判定。
func logDetailInt64(row map[string]any, key string) int64 {
	switch value := row[key].(type) {
	case int:
		return int64(value)
	case int32:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed
	default:
		return 0
	}
}
