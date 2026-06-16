package contract

import (
	"context"
	"strings"
)

// ---------------------------------------------------------------------------
// TrustScope — skill trust boundary
// ---------------------------------------------------------------------------

// TrustScope describes the trust boundary of a skill, determining its
// autonomous invocation rights, approval policy, and tool whitelist defaults.
//
// Three tiers:
//   - TrustUser    : located in the user-level skills root, treated as locally
//     trusted; model may invoke autonomously by default.
//   - TrustProject : located in the project-level skills root (typically from
//     git clone), treated as untrusted; first scan requires approval.
//   - TrustSigned  : declared via frontmatter `trust: signed`; signature
//     verification deferred to P21, currently treated like TrustUser.
type TrustScope string

const (
	TrustUnknown TrustScope = ""
	TrustUser    TrustScope = "user"
	TrustProject TrustScope = "project"
	TrustSigned  TrustScope = "signed"
)

// Valid reports whether the scope is a known trust tier.
// Valid 判断跨模块契约是否可用。
func (t TrustScope) Valid() bool {
	switch t {
	case TrustUser, TrustProject, TrustSigned:
		return true
	}
	return false
}

// Trusted reports whether the scope can skip per-invocation approval
// (user / signed are considered trusted).
// Trusted 处理trusted。
func (t TrustScope) Trusted() bool {
	return t == TrustUser || t == TrustSigned
}

// ---------------------------------------------------------------------------
// SkillInfo — read-only skill metadata
// ---------------------------------------------------------------------------

// SkillInfo is the read-only metadata projection of a single skill, used by
// dashboard, legacy catalog compatibility code, and other consumers that do
// not need to mutate skills.
type SkillInfo struct {
	Name         string   `json:"name"`
	DisplayName  string   `json:"display_name,omitempty"`
	Dir          string   `json:"dir"`
	Scope        string   `json:"scope,omitempty"`
	PersonalType string   `json:"personal_type,omitempty"`
	Description  string   `json:"description"`
	Summary      string   `json:"summary"`
	TriggerWords []string `json:"trigger_words,omitempty"`
	ForceWords   []string `json:"force_words,omitempty"`
	// Trust is the trust scope: "user" / "project" / "signed".
	Trust TrustScope `json:"trust,omitempty"`
	// AllowedTools whitelist names (e.g. "Read", "skill_read_section"). Empty = inherit session defaults.
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// DisableModelInvocation: when true, the skill is not exposed for model auto-call;
	// only a user-issued slash command can trigger it.
	DisableModelInvocation bool `json:"disable_model_invocation,omitempty"`
	// ContentHash is the SHA-256 of the SKILL.md body (hex lowercase), used by
	// the approval cache to force re-approval when body mutates.
	ContentHash string `json:"content_hash,omitempty"`
	// ReplacesNative lists provider-native tool IDs this skill semantically
	// replaces. Keys are provider names such as "codex", "claude", or "*".
	ReplacesNative map[string][]string `json:"replaces_native,omitempty"`
}

// ---------------------------------------------------------------------------
// SkillLister — narrow read-only port for skill metadata consumers
// ---------------------------------------------------------------------------

// SkillLister is the read-only skill metadata port used by dashboard and
// compatibility consumers that only need skill metadata.
type SkillLister interface {
	ListSkills(ctx context.Context) ([]SkillInfo, error)
}

// SkillInventoryLister is the management-page view of discoverable skills.
// Unlike SkillLister, it returns raw canonical records so duplicate names and
// policy-hidden sources remain visible for conflict handling.
type SkillInventoryLister interface {
	ListSkillInventory(ctx context.Context) ([]SkillInfo, error)
}

// ---------------------------------------------------------------------------
// Skill CWD context helpers
// ---------------------------------------------------------------------------

// SkillCWDContextKey is the context key for scoping skill requests to a
// specific working directory. Exported so both contract.WithSkillCWD and
// the skill module's internal cwdFromContext share the same key.
type SkillCWDContextKey struct{}

// WithSkillCWD attaches a working-directory scope to ctx for skill requests.
// Empty cwd is a no-op so downstream callers can detect the missing scope.
// WithSkillCWD 设置技能工作目录。
func WithSkillCWD(ctx context.Context, cwd string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ctx
	}
	return context.WithValue(ctx, SkillCWDContextKey{}, cwd)
}

// SkillCWDFromContext extracts the skill working-directory scope from ctx.
// SkillCWDFromContext 从上下文处理技能工作目录。
func SkillCWDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(SkillCWDContextKey{}).(string)
	return strings.TrimSpace(value)
}

// RequireSkillCWD extracts the skill CWD from ctx, returning
// ErrSkillMissingCWD when absent.
// RequireSkillCWD 处理require技能工作目录。
func RequireSkillCWD(ctx context.Context) (string, error) {
	cwd := SkillCWDFromContext(ctx)
	if cwd == "" {
		return "", ErrSkillMissingCWD
	}
	return cwd, nil
}

// ---------------------------------------------------------------------------
// ArtifactKind — skill content artifact classification
// ---------------------------------------------------------------------------

// ArtifactKind constants classify skill content artifacts. Used by the
// approval cache for granularity and by the expanded-state deduplication
// in the turn module.
const (
	// ArtifactKindMetadata refers to skill metadata (name / description / summary).
	ArtifactKindMetadata = "metadata"
	// ArtifactKindBody refers to SKILL.md body text (or anchor slices thereof).
	ArtifactKindBody = "body"
	// ArtifactKindResource refers to resource files within the skill directory
	// (references/* / scripts/* / assets/*).
	ArtifactKindResource = "resource"
)

// IsValidArtifactKind reports whether kind is one of the known artifact kinds.
// IsValidArtifactKind 判断valid产物kind是否可用。
func IsValidArtifactKind(kind string) bool {
	switch kind {
	case ArtifactKindMetadata, ArtifactKindBody, ArtifactKindResource:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// SkillHydrationSource — turn service's minimal skill dependency
// ---------------------------------------------------------------------------

// SkillHydrationSource is the turn service's minimal dependency for resolving
// name-only skill references before provider submission.
type SkillHydrationSource interface {
	SkillLister
	ReadLocal(ctx context.Context, path string) (any, error)
}

// ---------------------------------------------------------------------------
// Skill mirror provider cutover contracts
// ---------------------------------------------------------------------------

// SkillProvider identifies the external skill runtime that receives mirrored skills.
type SkillProvider string

const (
	SkillProviderClaude SkillProvider = "claude"
	SkillProviderCodex  SkillProvider = "codex"
)

// SkillMirrorReport summarizes published, skipped, deleted, and conflicted mirror operations.
type SkillMirrorReport struct {
	Published []SkillMirrorReportItem `json:"published,omitempty"`
	Skipped   []SkillMirrorReportItem `json:"skipped,omitempty"`
	Deleted   []SkillMirrorReportItem `json:"deleted,omitempty"`
	Conflicts []SkillMirrorReportItem `json:"conflicts,omitempty"`
}

// SkillMirrorReportItem describes one provider mirror target outcome.
type SkillMirrorReportItem struct {
	TargetID           string        `json:"target_id"`
	Provider           SkillProvider `json:"provider,omitempty"`
	Scope              string        `json:"scope,omitempty"`
	RelativeMirrorPath string        `json:"relative_mirror_path,omitempty"`
	CanonicalID        string        `json:"canonical_id,omitempty"`
	OldHash            string        `json:"old_hash,omitempty"`
	NewHash            string        `json:"new_hash,omitempty"`
	ConflictKind       string        `json:"conflict_kind,omitempty"`
	Error              string        `json:"error,omitempty"`
}

// SkillProviderMirrorTarget configures one provider skill directory mirror destination.
type SkillProviderMirrorTarget struct {
	Provider          string `json:"provider"`
	HomeRoot          string `json:"home_root"`
	SkillsRoot        string `json:"skills_root"`
	AllowExplicitHome bool   `json:"allow_explicit_home,omitempty"`
}

// SkillMirrorReconciler syncs canonical project skills into provider-specific mirrors.
type SkillMirrorReconciler interface {
	ReconcileProviderMirrors(ctx context.Context, cwd string, targets []SkillProviderMirrorTarget) (SkillMirrorReport, error)
}
