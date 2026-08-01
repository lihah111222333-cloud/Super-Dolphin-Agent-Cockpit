package gateprivate

import (
	"context"
	"time"
)

// WithTimeout 统一拥有独立门禁依赖树中的超时上下文构造。
func WithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, timeout)
}
