// Package skilltool owns SQLite persistence for the Skill tool catalog.
package skilltool

import "go.uber.org/fx"

// Module registers the concrete Skill tool persistence implementation.
var Module = fx.Module("store.skilltool", fx.Provide(New))
