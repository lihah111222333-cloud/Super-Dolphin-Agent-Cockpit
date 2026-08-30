package personalization

import (
	"context"
	"encoding/json"
)

// PreferenceStore 是 personalization 需要的最小偏好持久化端口。
type PreferenceStore interface {
	GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error)
}
