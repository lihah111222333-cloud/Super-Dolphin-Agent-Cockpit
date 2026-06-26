package uipreference

import (
	"context"
	"encoding/json"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier 描述 UI preference store 依赖的 sqlc 查询集合。
// 测试可替换为 fake querier，生产路径仍由 NewStore 注入完整 *sqlc.Queries。
type querier interface {
	GetUIPreferenceValue(ctx context.Context, arg sqlc.GetUIPreferenceValueParams) (json.RawMessage, error)
	UpsertUIPreference(ctx context.Context, arg sqlc.UpsertUIPreferenceParams) error
	ListUIPreferences(ctx context.Context, arg sqlc.ListUIPreferencesParams) ([]sqlc.ListUIPreferencesRow, error)
}

// store 实现 UI preference 的 SQLite 持久化边界。
type store struct {
	q querier
}

// NewStore 创建 sqlc 支撑的 UI preference store。
// 调用方必须传入已初始化的查询器，这里不做 nil 兜底，避免 UI 偏好读写静默丢失。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// GetValue 读取指定 cwd/key 的偏好 JSON 值。
// 返回值保持原始 JSON，避免 store 绑定某个前端版本的偏好结构。
func (s *store) GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error) {
	value, err := s.q.GetUIPreferenceValue(ctx, sqlc.GetUIPreferenceValueParams{
		CWD: cwd,
		Key: key,
	})
	if err != nil {
		return nil, wrapUIPreferenceError(err, "get")
	}
	return value, nil
}

// Upsert 写入 UI 偏好，入库前先校验 Value 是合法 JSON。
// Cwd 和 Key 作为隔离维度直接交给 SQL 约束，避免跨项目覆盖同名偏好。
func (s *store) Upsert(ctx context.Context, params UpsertParams) error {
	if err := platformdb.ValidateJSONRaw(params.Value); err != nil {
		return wrapUIPreferenceError(err, "upsert")
	}
	return wrapUIPreferenceError(s.q.UpsertUIPreference(ctx, sqlc.UpsertUIPreferenceParams{
		CWD:       params.Cwd,
		Key:       params.Key,
		Value:     params.Value,
		UpdatedAt: platformdb.Millis(time.Now().UTC()),
	}), "upsert")
}

// List 列出指定 cwd 下的 UI 偏好记录；cwd 为空时由 SQL 查询决定是否返回全量。
// 该路径主要服务前端状态恢复，Value 不在后端展开解释。
func (s *store) List(ctx context.Context, cwd string) ([]UIPreference, error) {
	rows, err := s.q.ListUIPreferences(ctx, sqlc.ListUIPreferencesParams{CWDFilter: cwd})
	if err != nil {
		return nil, wrapUIPreferenceError(err, "list")
	}
	result := make([]UIPreference, len(rows))
	for i, row := range rows {
		result[i] = UIPreference{
			Key:       row.Key,
			Value:     row.Value,
			UpdatedAt: platformdb.TimeFromMillis(row.UpdatedAt),
			Cwd:       row.CWD,
		}
	}
	return result, nil
}

// wrapUIPreferenceError 统一给 UI preference store 错误补充操作名和存储名。
func wrapUIPreferenceError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "ui_preference")
}
