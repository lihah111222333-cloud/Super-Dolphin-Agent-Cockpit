package contract

import "errors"

var (
	ErrSessionNotFound = errors.New("session not found")
)

// ---------------------------------------------------------------------------
// Skill error sentinels & types — lifted from internal/module/skill so that
// platform/toolbridge (and other low-level consumers) can match these errors
// without importing the module layer.
// ---------------------------------------------------------------------------

// ErrSkillMissingCWD is the canonical sentinel for "cwd is required" errors
// originating from the skill module. The skill package should set its own
// ErrMissingCWD = contract.ErrSkillMissingCWD so errors.Is works across
// layers.
var ErrSkillMissingCWD = errors.New("cwd is required")

// ErrSkillSameNameConflict is returned when two or more canonical skills share
// the same name and no explicit resolution policy selects a single source.
var ErrSkillSameNameConflict = errors.New("skill same-name conflict")

// SkillApprovalRequiredError signals that a skill artifact requires user
// approval before execution. It carries the ApprovalRequest payload so
// callers can surface the approval UI.
type SkillApprovalRequiredError struct {
	Request ApprovalRequest
}

var errSkillApprovalRequired = errors.New("skill artifact approval required")

// Error 返回错误文本。
func (e SkillApprovalRequiredError) Error() string {
	return errSkillApprovalRequired.Error()
}

// Unwrap 返回底层错误。
func (e SkillApprovalRequiredError) Unwrap() error { return errSkillApprovalRequired }

// ---------------------------------------------------------------------------
// Store-level sentinel errors (was store_errors.go)
// ---------------------------------------------------------------------------

// Store-level sentinel errors shared across modules.
// These mirror the sentinels in platform/db but live in contract so that
// module-layer code never needs to import platform/db directly.
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: conflict")
)

// IsNotFound reports whether err (or any error in its chain) matches
// the store-not-found sentinel.
// IsNotFound 判断notfound是否可用。
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// ---------------------------------------------------------------------------
// Toolbridge runtime sentinels (was toolbridge_runtime_required.go)
// ---------------------------------------------------------------------------

// ErrThreadRuntimeRequired is the sentinel returned when a toolbridge
// tool call cannot resolve a thread identity to apply per-thread policy
// (e.g. spawn_agent policy enforcement).
var ErrThreadRuntimeRequired = errors.New("toolbridge: thread runtime is required")

// ErrPersistentSubagentRuntimeRequired is the sibling sentinel for the
// specific case where thread identity resolves but the thread store has
// no runtime config (so persistent_subagent_default cannot be evaluated).
var ErrPersistentSubagentRuntimeRequired = errors.New("toolbridge: persistent subagent runtime is required")

// ErrPersistentSubagentFlagRequired is returned when a toolbridge
// spawn_agent policy check has a runtime config, but that runtime does
// not explicitly carry the persistent-subagent session flag.
var ErrPersistentSubagentFlagRequired = errors.New("toolbridge: persistent subagent flag is required")
