package bus

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"github.com/kelindar/event"
)

// ResilientSubscribe delegates to contract.ResilientSubscribe.
// Kept for backward compatibility; new code should use contract directly.
// ResilientSubscribe 处理resilientsubscribe。
func ResilientSubscribe[T event.Event](dispatcher *event.Dispatcher, fn func(T), logger *pkglogger.Logger) context.CancelFunc {
	return contract.ResilientSubscribe(dispatcher, fn, logger)
}
