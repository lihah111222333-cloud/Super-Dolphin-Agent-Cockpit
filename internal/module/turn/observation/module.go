package observation

import "go.uber.org/fx"

// Module 将 observation 内存实现和事件订阅声明接入 Fx。
// 本模块只发布事实读写契约，不导入 turn/tracker；订阅注册和关闭由 BusModule 统一托管。
var Module = fx.Module("module.turn.observation",
	fx.Provide(
		NewMemory,
		func(m *Memory) Contract { return m },
		NewObservationSubscribers,
	),
)
