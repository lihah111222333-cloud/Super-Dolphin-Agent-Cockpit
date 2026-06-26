package uipreference

import (
	"context"
	"encoding/json"
	"time"
)

// Store 定义按项目工作目录保存 UI 偏好的持久化接口。
// Value 以原始 JSON 透传给前端，store 只负责项目隔离、合法 JSON 和更新时间边界。
type Store interface {
	// GetValue 读取指定项目和 key 的偏好 JSON，未命中由实现返回 store not found 语义。
	GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error)
	// Upsert 覆盖写入单个偏好值，Value 必须在进入持久化前保持合法 JSON。
	Upsert(ctx context.Context, params UpsertParams) error
	// List 返回项目下所有偏好，用于前端一次性恢复本地 UI 状态。
	List(ctx context.Context, cwd string) ([]UIPreference, error)
}

// UpsertParams 承载 UI 偏好写入参数，Value 必须是合法 JSON。
type UpsertParams struct {
	Cwd   string
	Key   string
	Value json.RawMessage
}

// UIPreference 表示跨 UI RPC 返回的单条偏好记录。
// Value 保持 json.RawMessage，避免后端在不同前端版本之间解释未知字段。
type UIPreference struct {
	Key       string
	Value     json.RawMessage
	UpdatedAt time.Time
	Cwd       string
}
