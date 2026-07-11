package auditlog

import (
	"context"
	"encoding/json"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// querier 是 auditlog store 依赖的 sqlc 查询子集，测试可用窄接口替身覆盖。
type querier interface {
	ListAuditEvents(ctx context.Context, arg sqlc.ListAuditEventsParams) ([]sqlc.AuditEvent, error)
	InsertAuditEvent(ctx context.Context, arg sqlc.InsertAuditEventParams) error
}

// store 实现审计事件的查询和追加，写入路径负责校验 Extra JSON。
type store struct {
	q querier
}

// NewStore 使用生产 sqlc 查询对象创建 auditlog Store。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// newStoreForTest 使用窄 querier 构造测试 Store，避免测试依赖真实数据库池。
func newStoreForTest(q querier) Store { return &store{q: q} }

// List 按审计过滤条件读取事件列表，并统一映射为 AuditEvent DTO。
func (s *store) List(ctx context.Context, filter ListFilter) ([]AuditEvent, error) {
	rows, err := s.q.ListAuditEvents(ctx, sqlc.ListAuditEventsParams{
		EventTypeFilter: filter.EventType,
		ActionFilter:    filter.Action,
		ActorFilter:     filter.Actor,
		Keyword:         filter.Keyword,
		KeywordPattern:  platformdb.LikeContainsFold(filter.Keyword),
		LimitCount:      int64(filter.Limit),
	})
	if err != nil {
		return nil, wrapAuditLogError(err, "list")
	}
	result := make([]AuditEvent, len(rows))
	for i, row := range rows {
		result[i] = mapAuditEvent(row)
	}
	return result, nil
}

// Insert 追加审计事件，Extra 不是合法 JSON 时在进入 sqlc 前失败。
func (s *store) Insert(ctx context.Context, params InsertParams) error {
	if err := platformdb.ValidateJSONRaw(params.Extra); err != nil {
		return wrapAuditLogError(err, "insert")
	}
	return wrapAuditLogError(s.q.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
		Ts:        platformdb.Millis(time.Now().UTC()),
		EventType: params.EventType,
		Action:    params.Action,
		Result:    params.Result,
		Actor:     params.Actor,
		Target:    params.Target,
		Detail:    params.Detail,
		Level:     params.Level,
		Extra:     string(params.Extra),
	}), "insert")
}

// mapAuditEvent 将查询行转换为前端 JSON wire DTO。
func mapAuditEvent(row sqlc.AuditEvent) AuditEvent {
	return AuditEvent{
		ID:        row.ID,
		Ts:        platformdb.TimeFromMillis(row.Ts),
		EventType: row.EventType,
		Action:    row.Action,
		Result:    row.Result,
		Actor:     row.Actor,
		Target:    row.Target,
		Detail:    row.Detail,
		Level:     row.Level,
		Extra:     json.RawMessage(row.Extra),
	}
}

// wrapAuditLogError 统一包装审计日志 store 错误，保留 operation 便于排查。
func wrapAuditLogError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "audit_event")
}
