package feedbackadapter

import "go.uber.org/fx"

// Module 提供 feedback 领域拥有的 Store adapter。
var Module = fx.Module("app.storeadapter.feedback",
	fx.Provide(provideFeedbackWriter),
)
