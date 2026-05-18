package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ErrMissingCWD aliases the canonical contract sentinel so errors.Is works
// regardless of whether the caller imported skill or contract.
var ErrMissingCWD = contract.ErrSkillMissingCWD
var ErrInvalidSkillScope = errors.New("invalid skill scope")
var ErrSkillSystemScopeRemoved = errors.New("skill system scope has been removed")
var ErrSkillSameNameConflict = contract.ErrSkillSameNameConflict

type SkillProvider = contract.SkillProvider
type SkillMirrorReport = contract.SkillMirrorReport
type SkillMirrorReportItem = contract.SkillMirrorReportItem

type ExecResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Command  string `json:"command,omitempty"`
	CWD      string `json:"cwd,omitempty"`
}

// SkillInfo is a type alias for contract.SkillInfo. The canonical definition
// now lives in internal/contract so that cross-module consumers (dashboard,
// prompt) do not need to import internal/module/skill.
type SkillInfo = contract.SkillInfo

const (
	SkillProviderClaude = contract.SkillProviderClaude
	SkillProviderCodex  = contract.SkillProviderCodex
)

// WithCWD delegates to contract.WithSkillCWD. Kept for backward compatibility
// so existing callers (e.g. dashboard) that import skill.WithCWD keep compiling.
var WithCWD = contract.WithSkillCWD

func cwdFromContext(ctx context.Context) string {
	return contract.SkillCWDFromContext(ctx)
}

func requireCWD(ctx context.Context) (string, error) {
	return contract.RequireSkillCWD(ctx)
}

// RequireCWD delegates to contract.RequireSkillCWD.
var RequireCWD = contract.RequireSkillCWD

// Service is the backwards-compatible aggregate for the skill module itself
// and the RPC handler surface. Cross-module consumers should depend on the
// narrow ports below instead of this full method set.
type Service interface {
	contract.ApprovalSource
	SkillCommandExecutor
	SkillLister
	SkillHydrationSource
	skillLocalMutationStore
	skillRemoteStore
	skillConfigStore
	skillPreviewer
	skillLegacyExpander
	SkillRevisionSource
	TrustRevisionSource
}

type SkillCommandExecutor interface {
	ExecCommand(ctx context.Context, command string, args []string, cwd string, env map[string]string) (ExecResult, error)
}

// SkillLister is a type alias for contract.SkillLister. The canonical
// definition now lives in internal/contract so that cross-module consumers
// (dashboard, prompt) do not need to import internal/module/skill.
type SkillLister = contract.SkillLister

type SkillRevisionSource interface {
	SkillRevision() uint64
}

type TrustRevisionSource interface {
	TrustRevision() uint64
}

// SkillCatalogSource is the legacy prompt-catalog compatibility dependency:
// metadata listing, approval probing, and revision invalidation. Current V1
// production skill discovery is provider-native mirror based; prompt no
// longer wires a skill catalog injector on the hot path.
type SkillCatalogSource interface {
	SkillLister
	contract.ApprovalSource
	SkillRevisionSource
	TrustRevisionSource
}

// SkillHydrationSource is a type alias for contract.SkillHydrationSource.
// The canonical definition now lives in internal/contract so that the turn
// module can depend on contract instead of importing skill directly.
type SkillHydrationSource = contract.SkillHydrationSource

type skillLocalMutationStore interface {
	ListLocalFiles(ctx context.Context, p listSkillFilesParams) (any, error)
	WriteLocal(ctx context.Context, path, content string, scope ...string) (any, error)
	// CreateSkill is the host-side project-scope self-learning entry point.
	// It is a thin wrapper over WriteLocal(..., scope=project) and rejects
	// requests missing cwd with ErrMissingCWD.
	CreateSkill(ctx context.Context, p createSkillParams) (any, error)
	// ImportLocalDir supports mode=auto|single|batch; auto preserves single
	// skill imports and expands container directories into direct child skills.
	ImportLocalDir(ctx context.Context, p importSkillDirParams) (any, error)
	DeleteLocal(ctx context.Context, p DeleteSkillParams) (any, error)
}

type DeleteSkillParams struct {
	Name         string
	Scope        string
	PersonalType string
}

type skillRemoteStore interface {
	ReadRemote(ctx context.Context, url string) (any, error)
	WriteRemote(ctx context.Context, name, content string) (any, error)
}

type skillConfigStore interface {
	ReadConfig(ctx context.Context, agentID string) (any, error)
	WriteSkillContent(ctx context.Context, name, content string) (any, error)
	WriteSummary(ctx context.Context, name, summary string) (any, error)
}

type skillPreviewer interface {
	MatchPreview(ctx context.Context, agentID, threadID, text string, input []UserInput) (any, error)
}

type skillLegacyExpander interface {
	Expand(ctx context.Context, p skillExpandParams) (skillExpandResult, error)
}

func (s *service) ReconcileProviderMirrors(ctx context.Context, cwd string, targets []contract.SkillProviderMirrorTarget) (contract.SkillMirrorReport, error) {
	var report SkillMirrorReport
	if s == nil {
		return report, errors.New("skill service is not configured")
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = cwdFromContext(ctx)
	}
	if cwd == "" {
		cwd = strings.TrimSpace(s.projectRoot)
	}
	if cwd == "" {
		return report, ErrMissingCWD
	}
	projectRoot := reconcileProjectRoot(cwd, s.projectRoot)
	mirrorTargets, err := s.providerMirrorTargets(projectRoot, targets)
	if err != nil {
		return report, err
	}
	store := newCanonicalStoreForOwner(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
	for _, scope := range []string{skillScopeProject, skillScopePersonal} {
		group := mirrorTargetsForScope(mirrorTargets, scope)
		records, conflicts, err := mirrorRecordsForScope(ctx, store, projectRoot, scope)
		if err != nil {
			return report, err
		}
		if len(conflicts) > 0 {
			appendCanonicalConflictReportItems(&report, group, conflicts)
			continue
		}
		targetReport, err := PublishSkillMirrors(ctx, records, group)
		appendSkillMirrorReport(&report, targetReport)
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

func mirrorTargetsForScope(targets []SkillMirrorTarget, scope string) []SkillMirrorTarget {
	var out []SkillMirrorTarget
	for _, target := range targets {
		if target.Scope == scope {
			out = append(out, target)
		}
	}
	return out
}

func mirrorRecordsForScope(ctx context.Context, store *canonicalStore, cwd, scope string) ([]canonicalSkillRecord, []canonicalSkillConflict, error) {
	if scope != skillScopePersonal {
		return store.EffectiveSet(ctx, cwd)
	}
	records, err := store.scan(cwd)
	if err != nil {
		return nil, nil, err
	}
	records, err = store.applyPersonalSkillPolicy(records)
	if err != nil {
		return nil, nil, err
	}
	records = filterCanonicalRecordsForScope(records, skillScopePersonal)
	conflicts := canonicalSameNameConflicts(records)
	if len(conflicts) == 0 {
		return records, nil, nil
	}
	return canonicalRecordsWithoutConflicts(records, conflicts), conflicts, nil
}

func reconcileProjectRoot(cwd, configured string) string {
	if configured = strings.TrimSpace(configured); configured != "" {
		cwd = configured
	}
	if resolved, err := canonicalProjectPath(cwd); err == nil {
		return resolved
	}
	return cwd
}

func (s *service) providerMirrorTargets(cwd string, targets []contract.SkillProviderMirrorTarget) ([]SkillMirrorTarget, error) {
	out := make([]SkillMirrorTarget, 0, len(targets))
	for _, target := range targets {
		provider, err := normalizeProviderMirrorProvider(target.Provider)
		if err != nil {
			return nil, err
		}
		if isProjectProviderMirrorRoot(cwd, provider, target.SkillsRoot) {
			fingerprint := RepoFingerprint(cwd)
			out = append(out, SkillMirrorTarget{
				TargetID:        string(provider) + ":project:" + fingerprint,
				Provider:        provider,
				Scope:           skillScopeProject,
				Root:            strings.TrimSpace(target.SkillsRoot),
				CanonicalRootID: fingerprint,
			})
			continue
		}
		if !s.isAppManagedProviderMirrorRoot(provider, target.HomeRoot, target.SkillsRoot) && !explicitProviderMirrorRootAllowed(target) {
			return nil, fmt.Errorf("provider mirror target is not app-managed: provider=%s skills_root=%s", provider, strings.TrimSpace(target.SkillsRoot))
		}
		owner, err := resolveOwnerIdentity(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
		if err != nil {
			return nil, err
		}
		out = append(out, SkillMirrorTarget{
			TargetID:        string(provider) + ":app-managed:" + owner.OwnerKey,
			Provider:        provider,
			Scope:           skillScopePersonal,
			Root:            strings.TrimSpace(target.SkillsRoot),
			CanonicalRootID: owner.OwnerKey,
		})
	}
	return uniqueMirrorTargets(out), nil
}

func normalizeProviderMirrorProvider(provider string) (SkillProvider, error) {
	normalized := SkillProvider(strings.ToLower(strings.TrimSpace(provider)))
	if normalized != SkillProviderClaude && normalized != SkillProviderCodex {
		return "", fmt.Errorf("unsupported skill mirror provider %q", provider)
	}
	return normalized, nil
}

func isProjectProviderMirrorRoot(cwd string, provider SkillProvider, skillsRoot string) bool {
	expected := filepath.Join(strings.TrimSpace(cwd), "."+string(provider), "skills")
	return sameCleanPath(expected, skillsRoot)
}

func (s *service) isAppManagedProviderMirrorRoot(provider SkillProvider, homeRoot, skillsRoot string) bool {
	expectedHome := filepath.Join(s.resolvedSuperDolphinHome(), "providers", string(provider))
	return sameCleanPath(expectedHome, homeRoot) && sameCleanPath(filepath.Join(expectedHome, "skills"), skillsRoot)
}

func explicitProviderMirrorRootAllowed(target contract.SkillProviderMirrorTarget) bool {
	if !target.AllowExplicitHome {
		return false
	}
	home := strings.TrimSpace(target.HomeRoot)
	if home == "" {
		return false
	}
	return sameCleanPath(filepath.Join(home, "skills"), target.SkillsRoot)
}

func sameCleanPath(a, b string) bool {
	aa, errA := realpathAwareCleanPath(a)
	bb, errB := realpathAwareCleanPath(b)
	if errA != nil || errB != nil {
		return false
	}
	return aa == bb
}

func realpathAwareCleanPath(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(abs)
	if real, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(real), nil
	}
	var suffix []string
	for current := cleaned; ; current = filepath.Dir(current) {
		if real, err := filepath.EvalSymlinks(current); err == nil {
			parts := append([]string{filepath.Clean(real)}, reversePathParts(suffix)...)
			return filepath.Clean(filepath.Join(parts...)), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return cleaned, nil
		}
		suffix = append(suffix, filepath.Base(current))
	}
}

func reversePathParts(parts []string) []string {
	out := make([]string, len(parts))
	for i := range parts {
		out[i] = parts[len(parts)-1-i]
	}
	return out
}

func appendCanonicalConflictReportItems(report *SkillMirrorReport, targets []SkillMirrorTarget, conflicts []canonicalSkillConflict) {
	if report == nil || len(conflicts) == 0 {
		return
	}
	for _, conflict := range conflicts {
		for _, target := range targets {
			report.Conflicts = append(report.Conflicts, SkillMirrorReportItem{
				TargetID:           target.TargetID,
				Provider:           target.Provider,
				Scope:              target.Scope,
				RelativeMirrorPath: skillSlug(conflict.Name),
				ConflictKind:       skillConflictSameName,
				Error:              ErrSkillSameNameConflict.Error(),
			})
		}
	}
}
