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
func WithCWD(ctx context.Context, cwd string) context.Context {
	return contract.WithSkillCWD(ctx, cwd)
}

func cwdFromContext(ctx context.Context) string {
	return contract.SkillCWDFromContext(ctx)
}

func requireCWD(ctx context.Context) (string, error) {
	return contract.RequireSkillCWD(ctx)
}

// Service is the backwards-compatible aggregate for the skill module itself
// and the RPC handler surface. Cross-module consumers should depend on the
// narrow ports below instead of this full method set.
type Service interface {
	contract.ApprovalSource
	SkillCommandExecutor
	SkillLister
	SkillInventoryLister
	SkillHydrationSource
	skillLocalMutationStore
	skillRemoteStore
	skillConfigStore
	skillPreviewer
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

// SkillInventoryLister is a type alias for contract.SkillInventoryLister.
// It is only for management inventory; runtime consumers should keep using
// SkillLister so unresolved conflicts still fail closed.
type SkillInventoryLister = contract.SkillInventoryLister

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

func (s *service) ReconcileProviderMirrors(ctx context.Context, cwd string, targets []contract.SkillProviderMirrorTarget) (contract.SkillMirrorReport, error) {
	var report SkillMirrorReport
	if s == nil {
		return report, errors.New("skill service is not configured")
	}
	projectRoot, err := s.reconcileMirrorProjectRoot(ctx, cwd)
	if err != nil {
		return report, err
	}
	mirrorTargets, err := s.providerMirrorTargets(projectRoot, targets)
	if err != nil {
		return report, err
	}
	store := newCanonicalStoreForOwner(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
	cleanupReport, err := s.cleanupProjectSuppressedPersonalMirrors(projectRoot, mirrorTargets, store)
	appendSkillMirrorReport(&report, cleanupReport)
	if err != nil {
		return report, err
	}
	for _, scope := range []string{skillScopeProject, skillScopePersonal} {
		if err := reconcileProviderMirrorScope(ctx, &report, store, projectRoot, mirrorTargets, scope); err != nil {
			return report, err
		}
	}
	return report, nil
}

func (s *service) reconcileMirrorProjectRoot(ctx context.Context, cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = cwdFromContext(ctx)
	}
	if cwd == "" {
		cwd = strings.TrimSpace(s.projectRoot)
	}
	if cwd == "" {
		return "", ErrMissingCWD
	}
	return reconcileProjectRoot(cwd, s.projectRoot), nil
}

func reconcileProviderMirrorScope(ctx context.Context, report *SkillMirrorReport, store *canonicalStore, projectRoot string, targets []SkillMirrorTarget, scope string) error {
	group := mirrorTargetsForScope(targets, scope)
	records, conflicts, err := mirrorRecordsForScope(ctx, store, projectRoot, scope)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		appendCanonicalConflictReportItems(report, group, conflicts)
	}
	targetReport, err := PublishSkillMirrors(ctx, records, group)
	appendSkillMirrorReport(report, targetReport)
	return err
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
	records, conflicts, err := store.EffectiveSet(ctx, cwd)
	if err != nil {
		return nil, nil, err
	}
	return filterCanonicalRecordsForScope(records, scope), conflicts, nil
}

func reconcileProjectRoot(cwd, configured string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = strings.TrimSpace(configured)
	}
	resolved, err := canonicalProjectPath(cwd)
	if err != nil {
		return cwd
	}
	if root, err := nearestProjectRoot(resolved); err == nil {
		return root
	}
	return resolved
}

func nearestProjectRoot(dir string) (string, error) {
	original := dir
	for {
		gitPath := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			if filepath.Clean(dir) == filepath.Clean(os.TempDir()) && filepath.Clean(original) != filepath.Clean(dir) {
				return original, nil
			}
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return original, nil
		}
		dir = parent
	}
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
		targetKind := s.providerPersonalMirrorTargetKind(provider, target)
		if targetKind == "" {
			return nil, fmt.Errorf("provider mirror target is not allowed: provider=%s skills_root=%s", provider, strings.TrimSpace(target.SkillsRoot))
		}
		owner, err := resolveOwnerIdentity(s.resolvedSuperDolphinHome(), defaultOwnerOSUID(), defaultAppProfile())
		if err != nil {
			return nil, err
		}
		out = append(out, SkillMirrorTarget{
			TargetID:        string(provider) + ":" + targetKind + ":" + owner.OwnerKey,
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
	home := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME"))
	return sameCleanPath(providerProjectMirrorRoot(provider, strings.TrimSpace(cwd)), skillsRoot) ||
		home != "" && strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_PACKAGED_CODEX_IDENTITY")) == "1" &&
			sameCleanPath(filepath.Join(home, "provider-mirrors", "project", string(provider), "skills"), skillsRoot)
}

func (s *service) providerPersonalMirrorTargetKind(provider SkillProvider, target contract.SkillProviderMirrorTarget) string {
	switch {
	case s.isAppManagedProviderMirrorRoot(provider, target.HomeRoot, target.SkillsRoot):
		return "app-managed"
	case isDefaultProviderMirrorRoot(provider, target.HomeRoot, target.SkillsRoot):
		return "user-global"
	case explicitProviderMirrorRootAllowed(target):
		return "explicit-home"
	default:
		return ""
	}
}

func (s *service) isAppManagedProviderMirrorRoot(provider SkillProvider, homeRoot, skillsRoot string) bool {
	expectedHome := filepath.Join(s.resolvedSuperDolphinHome(), "providers", string(provider))
	return sameCleanPath(expectedHome, homeRoot) && sameCleanPath(filepath.Join(expectedHome, "skills"), skillsRoot)
}

func isDefaultProviderMirrorRoot(provider SkillProvider, homeRoot, skillsRoot string) bool {
	expectedSkills := providerPersonalMirrorRoot(provider)
	if expectedSkills == "" {
		return false
	}
	return sameCleanPath(filepath.Dir(expectedSkills), homeRoot) && sameCleanPath(expectedSkills, skillsRoot)
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
