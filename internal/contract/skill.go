package contract

import (
	"context"
	"strings"
	"time"

	dtoskill "github.com/anthropic-ai/super-agent-v3/internal/dto/skill"
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
func (t TrustScope) Valid() bool {
	switch t {
	case TrustUser, TrustProject, TrustSigned:
		return true
	}
	return false
}

// Trusted reports whether the scope can skip per-invocation approval
// (user / signed are considered trusted).
func (t TrustScope) Trusted() bool {
	return t == TrustUser || t == TrustSigned
}

// ---------------------------------------------------------------------------
// SkillInfo — read-only skill metadata
// ---------------------------------------------------------------------------

// SkillInfo is the read-only metadata projection of a single skill, used by
// dashboard, prompt catalog, and other consumers that do not need to mutate
// skills.
type SkillInfo struct {
	Name         string   `json:"name"`
	Dir          string   `json:"dir"`
	Scope        string   `json:"scope,omitempty"`
	PersonalType string   `json:"personal_type,omitempty"`
	Description  string   `json:"description"`
	Summary      string   `json:"summary"`
	TriggerWords []string `json:"trigger_words,omitempty"`
	ForceWords   []string `json:"force_words,omitempty"`
	// Trust is the trust scope: "user" / "project" / "signed".
	Trust TrustScope `json:"trust,omitempty"`
	// AllowedTools whitelist names (e.g. "Read", "skill_expand"). Empty = inherit session defaults.
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// DisableModelInvocation: when true, the skill is not exposed for model auto-call;
	// only a user-issued slash command can trigger it.
	DisableModelInvocation bool `json:"disable_model_invocation,omitempty"`
	// ContentHash is the SHA-256 of the SKILL.md body (hex lowercase), used by
	// the approval cache to force re-approval when body mutates.
	ContentHash string `json:"content_hash,omitempty"`
	// DisclosureTier is a non-realtime usage-frequency snapshot for UI display.
	DisclosureTier string `json:"disclosure_tier,omitempty"`
}

// ---------------------------------------------------------------------------
// SkillLister — narrow read-only port for skill catalog consumers
// ---------------------------------------------------------------------------

// SkillLister is the read-only skill catalog port used by dashboard and prompt
// consumers that only need skill metadata.
type SkillLister interface {
	ListSkills(ctx context.Context) ([]SkillInfo, error)
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
func SkillCWDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(SkillCWDContextKey{}).(string)
	return strings.TrimSpace(value)
}

// RequireSkillCWD extracts the skill CWD from ctx, returning
// ErrSkillMissingCWD when absent.
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

type SkillProvider string

const (
	SkillProviderClaude SkillProvider = "claude"
	SkillProviderCodex  SkillProvider = "codex"
)

type SkillMirrorReport struct {
	Published []SkillMirrorReportItem `json:"published,omitempty"`
	Skipped   []SkillMirrorReportItem `json:"skipped,omitempty"`
	Deleted   []SkillMirrorReportItem `json:"deleted,omitempty"`
	Conflicts []SkillMirrorReportItem `json:"conflicts,omitempty"`
}

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

type SkillProviderMirrorTarget struct {
	Provider   string `json:"provider"`
	HomeRoot   string `json:"home_root"`
	SkillsRoot string `json:"skills_root"`
}

type SkillMirrorReconciler interface {
	ReconcileProviderMirrors(ctx context.Context, cwd string, targets []SkillProviderMirrorTarget) (SkillMirrorReport, error)
}

// ---------------------------------------------------------------------------
// SkillLibrary — skill library and forge contracts (was skill_library.go)
// ---------------------------------------------------------------------------

// SkillManifestRenderer renders a skill manifest string for inclusion in
// base instructions. The production implementation lives in module/fbsd
// and module/skilllibrary. Returning empty string means "no skills available".
type SkillManifestRenderer interface {
	RenderSkillManifest() string
}

// SkillManifestEntryLister is the read-only skill entry source used by
// manifest renderers. Satisfied by *skilllibrary.Store.
type SkillManifestEntryLister interface {
	List() ([]dtoskill.SkillEntry, error)
}

// SkillDescriptionParser extracts the description field from SKILL.md content.
type SkillDescriptionParser interface {
	Description(skillMD string) string
}

// SkillLibraryConfig holds the subset of skill-library configuration that
// the provider layer needs (currently only the cache directory path).
type SkillLibraryConfig struct {
	CacheDir string
}

// FBSDRecorder is the FBSD (Frequency-Based Skill Disclosure) recording
// interface. The provider uses this to track skill read calls.
// The production implementation lives in module/fbsd.Tracker.
type FBSDRecorder interface {
	Record(name, anchor string)
	Enabled() bool
}

// SkillEntryMeta is the narrow projection of skilllibrary.SkillMeta that
// consumers outside the skilllibrary module need. Adding fields here is
// cheaper than importing the concrete module package.
type SkillEntryMeta struct {
	Name           string
	Disabled       bool
	ReplacesNative map[string][]string
}

// SkillEntry is the narrow projection of skilllibrary.SkillEntry exposed
// through SkillLibraryLister so that consumers (e.g. uistate) can avoid
// importing the concrete skilllibrary package.
type SkillEntry struct {
	Meta *SkillEntryMeta
}

// SkillLibraryLister is the read-only subset of *skilllibrary.Store that
// the uistate module (builtin-tools overlay) actually depends on.
// Satisfied by *skilllibrary.Store.
type SkillLibraryLister interface {
	List() ([]SkillEntry, error)
}

// ---------------------------------------------------------------------------
// SkillForge: 消除 skilllibrary → skillforge 横向依赖的窄接口
// ---------------------------------------------------------------------------

// StagingRecoveryReport 是 RecoverStaging 的结果摘要。
// 只暴露 skilllibrary 真正消费的字段，避免耦合 skillforge 的完整 RecoveryReport。
type StagingRecoveryReport struct {
	Errors []error
}

// SkillForger 封装 skilllibrary 对 skillforge 的全部运行时依赖。
// 生产实现由 skillforge 包提供，通过 fx 注入 skilllibrary.Reconciler。
type SkillForger interface {
	// Forge 把 libDir/<name>/SKILL.md 转换为 cacheDir/<name>/{SKILL.md, references/...}。
	Forge(libDir, cacheDir, name string, summaryOverride map[string]string) error

	// RecoverStaging 扫描 cacheDir，处理 publish 中断遗留的 staging 目录。
	RecoverStaging(cacheDir string) (*StagingRecoveryReport, error)
}

// EmbeddedSkillReader 封装 skilllibrary 对 skillforge 内嵌 skill 资源的只读访问。
// 生产实现由 skillforge 包提供，通过 fx 注入 skilllibrary.SeedBuiltins。
type EmbeddedSkillReader interface {
	// ListNames 返回所有内置 skill 名（升序）。
	ListNames() ([]string, error)

	// Read 返回指定内置 skill 的 SKILL.md 字节内容。
	Read(name string) ([]byte, error)
}

// SkillReplacementAggregator returns the sorted list of native tool names
// that installed skills declare as replaced for the given provider key
// (e.g. "codex"). The production implementation lives in
// module/skilllibrary.Store, which aggregates ReplacesNative metadata
// from all enabled skills.
type SkillReplacementAggregator interface {
	ReplacedNativeTools(provider string) []string
}

// ---------------------------------------------------------------------------
// SkillDisclosure — frequency-based skill disclosure types (was skill_disclosure.go)
// ---------------------------------------------------------------------------

type SkillDisclosureStats map[string]*SkillDisclosureSkillStats

type SkillDisclosureSkillStats struct {
	Calls []time.Time
}

type SkillDisclosureConfig struct {
	HalfLife       time.Duration
	FrozenDuration time.Duration
	WSMinCalls     int
	WSWeight       float64
}

type SkillDisclosureSnapshot struct {
	Workspace SkillDisclosureStats
	Global    SkillDisclosureStats
	Config    SkillDisclosureConfig
}

type SkillDisclosureTierSource interface {
	Enabled() bool
	DisclosureSnapshot() SkillDisclosureSnapshot
}

// ---------------------------------------------------------------------------
// WorkspaceSkillSetup (was workspace_skills.go)
// ---------------------------------------------------------------------------

// WorkspaceSkillSetupFunc sets up skill symlinks in the workspace directory
// so CLI processes can discover cached skills natively. The production
// implementation lives in module/cliadapter.SetupWorkspaceSkills.
type WorkspaceSkillSetupFunc func(workspaceDir, cacheDir string) error
