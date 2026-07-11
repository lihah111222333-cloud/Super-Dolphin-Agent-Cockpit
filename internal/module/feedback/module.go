// Package feedback 提供用户反馈事件的记录能力，通过 JSON-RPC 接口接收前端事件并持久化。
package feedback

import "go.uber.org/fx"

var Module = fx.Module("feedback",
	fx.Provide(NewService),
	fx.Provide(NewHandlers),
)
