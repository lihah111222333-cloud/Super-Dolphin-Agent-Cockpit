// Package bus 提供基于 kelindar/event 的进程内事件总线，封装 Dispatcher 的创建、
// 订阅生命周期管理和结构化日志追踪。
package bus

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"github.com/kelindar/event"
)

// ResilientSubscribe 委托给 contract.ResilientSubscribe，保留向后兼容；新代码应直接使用 contract。
func ResilientSubscribe[T event.Event](dispatcher *event.Dispatcher, fn func(T), logger *pkglogger.Logger) context.CancelFunc {
	return contract.ResilientSubscribe(dispatcher, fn, logger)
}
