package insight

import "go.uber.org/fx"

// Module provides the insight Store into the core Fx tree. The collector
// and dashboard API that consume it land in Track F phase 2.
var Module = fx.Module("store.insight",
	fx.Provide(NewStore),
)
