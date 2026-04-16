package contract

// TeamMemoryManager exposes read-only team-memory entrypoints needed by
// sibling memory subpackages during the package split migration.
type TeamMemoryManager interface {
	GetTeamMemPath(buildCtx ...BuildCtx) string
	GetTeamMemEntrypoint(buildCtx ...BuildCtx) string
}
