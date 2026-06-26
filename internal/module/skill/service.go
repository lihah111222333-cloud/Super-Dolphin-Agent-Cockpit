package skill

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/toolstore"
	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
)

const (
	maxSkillFileBytes = 1 << 20
	skillMainFile     = "SKILL.md"
)

type service struct {
	root               string
	projectRoot        string
	projectSkillsRoot  string
	superDolphinHome   string
	http               *http.Client
	readConfigState    func(context.Context, string) (any, error)
	emitSkillsChanged  skillsChangedEmitter
	skillsChangedMu    sync.Mutex
	skillsChangedNext  uidto.SkillsChanged
	skillsChangedQueue []uidto.SkillsChanged
	skillsChangedSeq   uint64
	skillsChangedDelay func()
	approval           *ApprovalCache
	auditStore         auditstore.Store
	mirrorTargets      []SkillMirrorTarget
	skillTools         *toolstore.Store

	resolutionPreviewMu sync.Mutex
	resolutionPreviews  map[string]skillResolutionStoredPreview
}

var _ Service = (*service)(nil)
var _ contract.SkillMirrorReconciler = (*service)(nil)
var _ contract.SkillToolProvider = (*service)(nil)

type SkillApprovalRequiredError = contract.SkillApprovalRequiredError

// resolutionPreviewHash 计算用户确认 preview 时必须回传的稳定 hash。
// hash 覆盖动作、路径、源/目标 hash 和备份路径，防止 preview 后目录内容被换掉仍继续 apply。
func resolutionPreviewHash(item skillResolutionItem, preview skillResolutionPreviewItem, p skillResolutionPreviewParams) string {
	type previewEnvelope struct {
		ConflictID          string `json:"conflict_id"`
		Action              string `json:"action"`
		Provider            string `json:"provider,omitempty"`
		SourceProvider      string `json:"source_provider,omitempty"`
		SourcePathID        string `json:"source_path_id,omitempty"`
		SourcePath          string `json:"source_path"`
		TargetPath          string `json:"target_path"`
		SourceHash          string `json:"source_hash"`
		TargetHash          string `json:"target_hash"`
		NewName             string `json:"new_name,omitempty"`
		KeepSourceID        string `json:"keep_source_id,omitempty"`
		MergeContentHash    string `json:"merge_content_hash,omitempty"`
		DisablePolicyTarget string `json:"disable_policy_target,omitempty"`
		BackupPath          string `json:"backup_path"`
		ConfirmDeleteHash   string `json:"confirm_delete_mirror_hash,omitempty"`
	}
	return "sha256:" + hashResolutionEnvelope(previewEnvelope{
		ConflictID:          item.ConflictID,
		Action:              p.Action,
		Provider:            preview.Provider,
		SourceProvider:      preview.SourceProvider,
		SourcePathID:        preview.SourcePathID,
		SourcePath:          preview.SourcePath,
		TargetPath:          preview.TargetPath,
		SourceHash:          preview.SourceHash,
		TargetHash:          preview.TargetHash,
		NewName:             p.NewName,
		KeepSourceID:        p.KeepSourceID,
		MergeContentHash:    p.MergeContentHash,
		DisablePolicyTarget: p.DisablePolicyTarget,
		BackupPath:          preview.BackupPath,
		ConfirmDeleteHash:   preview.ConfirmDeleteMirrorHash,
	})
}

func hashResolutionEnvelope(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		// archguard:ignore panic_count -- resolution envelope 只包含内部 JSON 安全 DTO。
		panic("skill: hashResolutionEnvelope: " + err.Error())
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// LookupArtifactApproval 查询 artifact 级审批缓存。
// 缓存未配置时返回 false，让调用方继续走审批流程而不是放行。
func (s *service) LookupArtifactApproval(_ context.Context, req contract.ArtifactApprovalRequest) (bool, error) {
	if s.approval == nil {
		return false, nil
	}
	_, ok := s.approval.LookupArtifact(ApprovalRequest{
		RepoFingerprint: req.RepoFingerprint,
		Name:            req.Name,
		ArtifactKind:    req.ArtifactKind,
		ArtifactLocator: req.ArtifactLocator,
		ContentHash:     req.ContentHash,
	})
	return ok, nil
}

// ApprovalRevision 返回审批缓存 revision。
// 缓存未配置时为 0，供前端判断是否需要刷新审批视图。
func (s *service) ApprovalRevision() uint64 {
	if s.approval == nil {
		return 0
	}
	return s.approval.Revision()
}

// SkillRevision 返回技能变更事件序号。
// 所有本地写入和导入都会递增该值，供动态工具面判断缓存是否过期。
func (s *service) SkillRevision() uint64 {
	return atomic.LoadUint64(&s.skillsChangedSeq)
}

// TrustRevision 返回信任相关视图的 revision。
// 当前信任状态随 skill 元数据一起变化，因此复用 SkillRevision。
func (s *service) TrustRevision() uint64 { return s.SkillRevision() }

// NewService 创建无数据库依赖的 skill service。
// 它会尝试加载本地审批缓存；加载失败时保留空缓存，由后续审批查询返回未命中。
func NewService(projectRoot string) Service {
	pr := strings.TrimSpace(projectRoot)
	if pr != "" {
		pr = filepath.Clean(pr)
	}
	// 审批缓存只用于只读 trust probe；构造期加载失败不能阻断整个模块启动。
	approvalCache, _ := NewApprovalCache(DefaultApprovalCachePath())
	return &service{
		projectRoot:       pr,
		projectSkillsRoot: defaultProjectSkillsRoot(pr),
		superDolphinHome:  defaultSuperDolphinHome(),
		http:              &http.Client{Timeout: 15 * time.Second},
		approval:          approvalCache,
	}
}

// NewServiceWithDB 创建带数据库 Skill Tool CRUD 能力的 skill service。
// 生产装配通过 fx 注入同一张 SQLite；测试可直接传内存 DB 验证懒建表行为。
func NewServiceWithDB(projectRoot string, db *sql.DB) Service {
	svc := NewService(projectRoot).(*service)
	svc.skillTools = toolstore.New(db)
	return svc
}

func (s *service) resolveSkillToolCWD(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", ErrMissingCWD
	}
	return s.projectRootForCWD(cwd), nil
}

// ListSkillToolsForSurface 返回当前项目启用的 Skill 工具定义，供 Codex 动态工具面使用。
func (s *service) ListSkillToolsForSurface(ctx context.Context, cwd string) ([]contract.SkillToolSurfaceTool, error) {
	if s == nil {
		return nil, toolstore.ErrStoreNotConfigured
	}
	return toolstore.ListForSurface(ctx, s.skillTools, s.resolveSkillToolCWD, cwd)
}

// CallSkillTool 按方法名读取对应 Skill 的完整 SKILL.md 文本并返回给模型。
func (s *service) CallSkillTool(ctx context.Context, call contract.SkillToolCall) (string, error) {
	if s == nil {
		return "", toolstore.ErrStoreNotConfigured
	}
	return toolstore.CallForSurface(ctx, s.skillTools, s.resolveSkillToolCWD, s.readSkillToolContent, call)
}

func (s *service) readSkillToolContent(ctx context.Context, cwd, name string) (string, error) {
	result, err := s.ReadLocal(WithCWD(ctx, cwd), name)
	if err != nil {
		return "", err
	}
	envelope, _ := result.(map[string]any)
	skillData, _ := envelope["skill"].(map[string]any)
	content, ok := skillData["content"].(string)
	if !ok {
		return "", fmt.Errorf("skill tool read returned invalid content for %s", name)
	}
	return content, nil
}

func (s *service) projectSkillsRootForCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	projectRoot := s.projectRootForCWD(cwd)
	if s != nil && sameProjectRoot(projectRoot, s.projectRoot) && strings.TrimSpace(s.projectSkillsRoot) != "" {
		return strings.TrimSpace(s.projectSkillsRoot)
	}
	return defaultProjectSkillsRoot(projectRoot)
}

func (s *service) projectRootForCWD(cwd string) string {
	configured := ""
	if s != nil {
		configured = s.projectRoot
	}
	root := projectRootForCWD(cwd, configured)
	if sameProjectRoot(root, configured) {
		return strings.TrimSpace(configured)
	}
	return root
}

func projectRootForCWD(cwd, configured string) string {
	return reconcileProjectRoot(cwd, configured)
}

func sameProjectRoot(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	resolvedLeft, leftErr := canonicalProjectPath(left)
	resolvedRight, rightErr := canonicalProjectPath(right)
	if leftErr == nil && rightErr == nil {
		return filepath.Clean(resolvedLeft) == filepath.Clean(resolvedRight)
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func normalizeSkillScope(scope string) (string, error) {
	normalizedScope, _, err := normalizeSkillTarget(scope, "")
	return normalizedScope, err
}
func (s *service) resolvedSuperDolphinHome() string {
	if s != nil && strings.TrimSpace(s.superDolphinHome) != "" {
		return strings.TrimSpace(s.superDolphinHome)
	}
	return defaultSuperDolphinHome()
}

func (s *service) personalSkillsRoot(personalType string) string {
	personalType = strings.TrimSpace(personalType)
	superDolphinHome := strings.TrimSpace(s.resolvedSuperDolphinHome())
	if personalType == "" || superDolphinHome == "" {
		return ""
	}
	return filepath.Join(superDolphinHome, "skills", "personal", personalType)
}

// canonicalRootForTarget 解析指定 scope/personalType 对应的 canonical skill 根目录。
// project 和 personal 均必须有明确根目录，缺失配置时直接返回错误。
func (s *service) canonicalRootForTarget(cwd, scope, personalType string) (string, string, string, error) {
	normalizedScope, normalizedType, err := normalizeSkillTarget(scope, personalType)
	if err != nil {
		return "", "", "", err
	}
	switch normalizedScope {
	case skillScopeProject:
		root := strings.TrimSpace(s.projectSkillsRootForCWD(cwd))
		if root == "" {
			return "", "", "", errors.New("project skills root is not configured")
		}
		return root, normalizedScope, "", nil
	case skillScopePersonal:
		root := strings.TrimSpace(s.personalSkillsRoot(normalizedType))
		if root == "" {
			return "", "", "", errors.New("personal skills root is not configured")
		}
		return root, normalizedScope, normalizedType, nil
	default:
		return "", "", "", fmt.Errorf("invalid skill scope: %s", normalizedScope)
	}
}

func (s *service) resolveScopeRoot(cwd, scope string, personalType ...string) (string, error) {
	root, _, _, err := s.canonicalRootForTarget(cwd, scope, resolveRequestedPersonalType(personalType...))
	if err != nil {
		return "", err
	}
	return root, nil
}

func (s *service) prepareScopedSkillsRoot(cwd, scope string, personalType ...string) (string, error) {
	root, err := s.resolveScopeRoot(cwd, scope, personalType...)
	if err != nil {
		return "", err
	}
	if err := rejectWritableSymlinkComponentIfExists(root); err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

func resolveRequestedSkillScope(scope ...string) string {
	if len(scope) == 0 {
		return ""
	}
	return scope[0]
}

func resolveRequestedPersonalType(personalType ...string) string {
	if len(personalType) == 0 {
		return ""
	}
	return personalType[0]
}

func resolveRequestedSkillTarget(scopeAndType ...string) (string, string) {
	switch len(scopeAndType) {
	case 0:
		return "", ""
	case 1:
		return scopeAndType[0], ""
	default:
		return scopeAndType[0], scopeAndType[1]
	}
}

// writableSkillFileMode 决定写入 skill 文件时应使用的权限位。
// 已存在文件沿用原权限，但 symlink 和目录会被拒绝，防止写入越界或覆盖目录。
func writableSkillFileMode(path string) (os.FileMode, error) {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("skill write path contains symlink: %s", path)
		}
		if info.IsDir() {
			return 0, fmt.Errorf("path is directory: %s", path)
		}
		if mode := info.Mode().Perm(); mode != 0 {
			return mode, nil
		}
		return 0o644, nil
	case errors.Is(err, os.ErrNotExist):
		return 0o644, nil
	default:
		return 0, err
	}
}

func (s *service) resolveSkillPath(target, cwd, scope string, personalType ...string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(target) {
		resolved, err := s.resolveExistingSkillPathTarget(target, cwd)
		if err != nil {
			return "", err
		}
		return resolved.path, nil
	}
	return s.resolveScopedSkillPath(cwd, target, scope, personalType...)
}

// resolveScopedSkillPath 按 scope 解析 skill 目标路径，并阻止越界访问。
func (s *service) resolveScopedSkillPath(cwd, target, scope string, personalType ...string) (string, error) {
	root, err := s.resolveScopeRoot(cwd, scope, personalType...)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(strings.TrimSpace(target))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes skills root: %s", target)
	}
	if strings.EqualFold(filepath.Base(cleaned), skillMainFile) {
		return filepath.Join(root, cleaned), nil
	}
	if strings.Contains(cleaned, string(filepath.Separator)) {
		return filepath.Join(root, cleaned), nil
	}
	return filepath.Join(root, skillSlug(cleaned), skillMainFile), nil
}
