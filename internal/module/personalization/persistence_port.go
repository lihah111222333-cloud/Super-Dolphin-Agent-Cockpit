package personalization

import (
	"context"
	"encoding/json"
)

// PreferenceStore 是 personalization 需要的最小偏好持久化端口。
type PreferenceStore interface {
	GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error)
	Upsert(ctx context.Context, params PreferenceUpsertParams) error
}

// PreferenceUpsertParams 是个性化资料写入偏好存储所需的领域参数。
type PreferenceUpsertParams struct {
	Cwd   string
	Key   string
	Value json.RawMessage
}
