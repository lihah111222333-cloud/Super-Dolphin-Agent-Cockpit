package orchestrationcommand

import "go.uber.org/fx"

// Module 注册唯一 command intent/journal owner；不会注册工具、provider 或执行回调。
var Module = fx.Module("store.orchestrationcommand",
	fx.Provide(
		NewStore,
		fx.Annotate(NewUnavailableReceiptVerifier, fx.As(new(ReceiptVerifier))),
		NewCoordinator,
	),
)
