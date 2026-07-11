package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// ErrMissingCWD 是 contract sentinel 的兼容别名。
// 调用方无论导入 skill 还是 contract，errors.Is 都能命中同一个错误。
var ErrMissingCWD = contract.ErrSkillMissingCWD

// skill 范围和冲突 sentinel errors，供 RPC 与跨模块调用方用 errors.Is 判断失败类型。
var ErrInvalidSkillScope = errors.New("invalid skill scope")
var ErrSkillSystemScopeRemoved = errors.New("skill system scope has been removed")
var ErrSkillSameNameConflict = contract.ErrSkillSameNameConflict

// SkillProvider 是 provider 标识的本包兼容别名。
type SkillProvider = contract.SkillProvider

// SkillMirrorReport 是 provider mirror 发布结果的本包兼容别名。
type SkillMirrorReport = contract.SkillMirrorReport

// SkillMirrorReportItem 是单个 mirror 发布项结果的本包兼容别名。
type SkillMirrorReportItem = contract.SkillMirrorReportItem

// ExecResult 描述 skill 命令执行后的退出码、输出和运行上下文。
type ExecResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Command  string `json:"command,omitempty"`
	CWD      string `json:"cwd,omitempty"`
}

// SkillInfo 是 contract.SkillInfo 的兼容别名。
// dashboard/prompt 等跨模块消费者应依赖 contract，而不是导入 skill 实现包。
type SkillInfo = contract.SkillInfo

const (
	SkillProviderClaude = contract.SkillProviderClaude
	SkillProviderCodex  = contract.SkillProviderCodex
)

// WithCWD 将技能查询使用的工作目录写入 context，保留旧调用方的 skill.WithCWD 入口。
func WithCWD(ctx context.Context, cwd string) context.Context {
	return contract.WithSkillCWD(ctx, cwd)
}

// cwdFromContext 从 context 读取技能工作目录，缺失时返回空串。
func cwdFromContext(ctx context.Context) string {
	return contract.SkillCWDFromContext(ctx)
}

// requireCWD 从 context 读取技能工作目录，缺失时返回统一 sentinel error。
func requireCWD(ctx context.Context) (string, error) {
	return contract.RequireSkillCWD(ctx)
}

// Service 是 skill 模块和 RPC handler 共用的聚合接口。
// 跨模块消费者应优先依赖下方窄接口，避免把写入、远程和审批能力一并暴露出去。
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

// SkillCommandExecutor 执行 skill 运行命令，并返回结构化输出。
type SkillCommandExecutor interface {
	ExecCommand(ctx context.Context, command string, args []string, cwd string, env map[string]string) (ExecResult, error)
}

// SkillLister 是 contract.SkillLister 的兼容别名。
// 跨模块消费者应依赖 internal/contract，避免直接导入 skill 模块实现面。
type SkillLister = contract.SkillLister

// SkillInventoryLister 是管理库存列表使用的兼容别名。
// 运行时消费者应继续使用 SkillLister，保证未解决冲突时 fail-closed。
type SkillInventoryLister = contract.SkillInventoryLister

// SkillRevisionSource 提供 skill 列表变更版本号。
type SkillRevisionSource interface {
	SkillRevision() uint64
}

// TrustRevisionSource 提供审批/信任状态变更版本号。
type TrustRevisionSource interface {
	TrustRevision() uint64
}

// SkillCatalogSource 是 prompt catalog 兼容面需要的窄依赖集合。
// 它只覆盖 metadata 列表、审批查询和版本失效，生产发现路径仍以 provider mirror 为准。
type SkillCatalogSource interface {
	SkillLister
	contract.ApprovalSource
	SkillRevisionSource
	TrustRevisionSource
}

// SkillHydrationSource 是 contract.SkillHydrationSource 的兼容别名。
// turn 模块通过 contract 依赖该能力，避免反向依赖 skill 实现。
type SkillHydrationSource = contract.SkillHydrationSource

// skillLocalMutationStore 定义本地 skill 文件读写和导入删除能力。
// 这些方法会触及项目或个人 skill 根目录，调用方必须带 cwd/scope 以通过边界校验。
type skillLocalMutationStore interface {
	ListLocalFiles(ctx context.Context, p listSkillFilesParams) (any, error)
	WriteLocal(ctx context.Context, path, content string, scope ...string) (any, error)
	// CreateSkill 是宿主侧项目范围自学习入口；缺少 cwd 时返回 ErrMissingCWD。
	CreateSkill(ctx context.Context, p createSkillParams) (any, error)
	// ImportLocalDir 支持 auto/single/batch；auto 保留单 skill 导入并展开容器目录的直接子 skill。
	ImportLocalDir(ctx context.Context, p importSkillDirParams) (any, error)
	DeleteLocal(ctx context.Context, p DeleteSkillParams) (any, error)
}

// DeleteSkillParams 描述删除 canonical skill 时的目标名称和 scope。
type DeleteSkillParams struct {
	Name         string
	Scope        string
	PersonalType string
}

// skillRemoteStore 定义远程 skill 读写能力，当前用于 RPC 兼容面。
type skillRemoteStore interface {
	ReadRemote(ctx context.Context, url string) (any, error)
	WriteRemote(ctx context.Context, name, content string) (any, error)
}

// skillConfigStore 定义 skill 配置和摘要写入能力。
type skillConfigStore interface {
	ReadConfig(ctx context.Context, agentID string) (any, error)
	WriteSkillContent(ctx context.Context, name, content string) (any, error)
	WriteSummary(ctx context.Context, name, summary string) (any, error)
}

// skillPreviewer 提供 skill 匹配预览能力，供管理 UI 调试候选结果。
type skillPreviewer interface {
	MatchPreview(ctx context.Context, agentID, threadID, text string, input []UserInput) (any, error)
}

// ReconcileProviderMirrors 在 provider 启动前刷新 mirror。
// 它从真实 skill 目录生成 .claude/.agents 等 mirror，不反过来读取 mirror。
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

// reconcileMirrorProjectRoot 解析 provider mirror 发布使用的项目根，缺失 cwd 时 fail-fast。
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

// reconcileProviderMirrorScope 分开处理 project 和 personal。
// 这样清理 mirror 时不会误删另一个来源的内容。
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

// mirrorTargetsForScope 过滤出指定 scope 的 provider mirror 目标。
func mirrorTargetsForScope(targets []SkillMirrorTarget, scope string) []SkillMirrorTarget {
	var out []SkillMirrorTarget
	for _, target := range targets {
		if target.Scope == scope {
			out = append(out, target)
		}
	}
	return out
}

// mirrorRecordsForScope 获取 effective canonical records，并按目标 scope 过滤。
func mirrorRecordsForScope(ctx context.Context, store *canonicalStore, cwd, scope string) ([]canonicalSkillRecord, []canonicalSkillConflict, error) {
	records, conflicts, err := store.EffectiveSet(ctx, cwd)
	if err != nil {
		return nil, nil, err
	}
	return filterCanonicalRecordsForScope(records, scope), conflicts, nil
}

// reconcileProjectRoot 将 cwd/configured 解析为最近项目根，解析失败时保留已知路径。
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

// nearestProjectRoot 从路径向上查找最近的项目根目录。
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

// providerMirrorTargets 计算需要发布的 provider mirror 目标。
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

// normalizeProviderMirrorProvider 校验 provider mirror 仅支持 claude/codex。
func normalizeProviderMirrorProvider(provider string) (SkillProvider, error) {
	normalized := SkillProvider(strings.ToLower(strings.TrimSpace(provider)))
	if normalized != SkillProviderClaude && normalized != SkillProviderCodex {
		return "", fmt.Errorf("unsupported skill mirror provider %q", provider)
	}
	return normalized, nil
}

// isProjectProviderMirrorRoot 判断目标目录是否为当前项目 provider mirror 根。
func isProjectProviderMirrorRoot(cwd string, provider SkillProvider, skillsRoot string) bool {
	home := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME"))
	return sameCleanPath(providerProjectMirrorRoot(provider, strings.TrimSpace(cwd)), skillsRoot) ||
		home != "" && strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_PACKAGED_CODEX_IDENTITY")) == "1" &&
			sameCleanPath(filepath.Join(home, "provider-mirrors", "project", string(provider), "skills"), skillsRoot)
}

// providerPersonalMirrorTargetKind 识别 personal mirror 目标类型，不允许的显式 home 返回空串。
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

// isAppManagedProviderMirrorRoot 判断目标是否为应用托管的 provider home。
func (s *service) isAppManagedProviderMirrorRoot(provider SkillProvider, homeRoot, skillsRoot string) bool {
	expectedHome := filepath.Join(s.resolvedSuperDolphinHome(), "providers", string(provider))
	return sameCleanPath(expectedHome, homeRoot) && sameCleanPath(filepath.Join(expectedHome, "skills"), skillsRoot)
}

// isDefaultProviderMirrorRoot 判断目标是否为 provider 默认个人技能目录。
func isDefaultProviderMirrorRoot(provider SkillProvider, homeRoot, skillsRoot string) bool {
	expectedSkills := providerPersonalMirrorRoot(provider)
	if expectedSkills == "" {
		return false
	}
	return sameCleanPath(filepath.Dir(expectedSkills), homeRoot) && sameCleanPath(expectedSkills, skillsRoot)
}

// explicitProviderMirrorRootAllowed 校验显式 provider home 是否被调用方授权。
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

// sameCleanPath 对两个路径做 realpath-aware 清理后比较。
func sameCleanPath(a, b string) bool {
	aa, errA := realpathAwareCleanPath(a)
	bb, errB := realpathAwareCleanPath(b)
	if errA != nil || errB != nil {
		return false
	}
	return aa == bb
}

// realpathAwareCleanPath 清理路径并尽量保留真实路径语义。
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

// reversePathParts 反转路径后缀片段，供 realpath 拼接使用。
func reversePathParts(parts []string) []string {
	out := make([]string, len(parts))
	for i := range parts {
		out[i] = parts[len(parts)-1-i]
	}
	return out
}

// appendCanonicalConflictReportItems 把 canonical 同名冲突转换为每个目标 mirror 的报告项。
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
