package app

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// sharedFileAdapterModule 提供 UISharedFilesChanged 事件适配器。
// 底层 sharedfile store 只依赖注入的发送函数，不直接导入 UI dispatcher 或 contract 层。
func sharedFileAdapterModule() fx.Option {
	return fx.Provide(provideUISharedFilesChangedEmitter)
}

// provideUISharedFilesChangedEmitter 提供 UI shared-file 变更事件发送函数。
// dispatcher 为空时返回 no-op，避免底层 store 直接依赖 UI 层。
func provideUISharedFilesChangedEmitter(dispatcher *event.Dispatcher) func(uidto.UISharedFilesChanged) {
	if dispatcher == nil {
		return func(uidto.UISharedFilesChanged) {}
	}
	return contract.NewEmitter[uidto.UISharedFilesChanged](dispatcher)
}
