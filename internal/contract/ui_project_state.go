package contract

import "context"

// ProjectsSnapshot is the contract-level DTO for the user's pinned project
// catalog: which paths are known and which one is currently active. It is
// intentionally a flat value type so UI consumers (internal/ui/wails,
// future CLI frontends) can depend on it without reaching back into
// internal/module/uistate.
//
// P22 P4 §1: ui/wails must not directly import internal/module/uistate;
// this DTO plus UIProjectStateFacade are the shared carrier that makes
// that import-direction invariant enforceable.
type ProjectsSnapshot struct {
	Projects []string `json:"projects"`
	Active   string   `json:"active"`
}

// UIProjectStateFacade is the narrow read contract UI frontends rely on
// to enumerate registered project roots. internal/module/uistate supplies
// the production implementation (via a thin adapter over its internal
// Service); tests can substitute any type that returns a
// *ProjectsSnapshot.
//
// This is the "专用 facade" branch of P22 P4 §1 target architecture —
// UI consumers depend on this interface instead of uistate.Service, so
// uistate's private types (Preferences, Sidebar, UIState, ...) never
// leak across the module boundary.
type UIProjectStateFacade interface {
	GetProjects(ctx context.Context) (*ProjectsSnapshot, error)
}
