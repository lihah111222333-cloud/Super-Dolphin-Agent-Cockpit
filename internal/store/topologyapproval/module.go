package topologyapproval

import "go.uber.org/fx"

// Module 注册拓扑审批 store 到 Fx 容器。
var Module = fx.Module("store.topologyapproval",
	fx.Provide(NewStore),
)
