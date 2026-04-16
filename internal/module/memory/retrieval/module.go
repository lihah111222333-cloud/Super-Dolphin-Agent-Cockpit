package retrieval

import "go.uber.org/fx"

var Module = fx.Module("memory.retrieval",
	fx.Provide(
		NewManifestBuilder,
	),
)
