package skill

import (
	"context"
	"crypto/sha256"
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
	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
)

const (
	maxSkillFileBytes = 1 << 20
	skillMainFile     = "SKILL.md"
)

type service struct {
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

	resolutionPreviewMu sync.Mutex
	resolutionPreviews  map[string]skillResolutionStoredPreview
}

var _ Service = (*service)(nil)
var _ contract.SkillMirrorReconciler = (*service)(nil)

type SkillApprovalRequiredError = contract.SkillApprovalRequiredError

// resolutionPreviewHash 处理resolutionpreviewhash。
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
		// archguard:ignore panic_count -- resolution envelopes are JSON-safe internal DTOs.
		panic("skill: hashResolutionEnvelope: " + err.Error())
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// LookupArtifactApproval 处理lookup产物审批。
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

// ApprovalRevision 处理审批revision。
func (s *service) ApprovalRevision() uint64 {
	if s.approval == nil {
		return 0
	}
	return s.approval.Revision()
}

// SkillRevision 处理技能revision。
func (s *service) SkillRevision() uint64 {
	return atomic.LoadUint64(&s.skillsChangedSeq)
}

// TrustRevision 处理trustrevision。
func (s *service) TrustRevision() uint64 { return s.SkillRevision() }

// NewService 创建服务。
func NewService(projectRoot string) Service {
	pr := strings.TrimSpace(projectRoot)
	if pr != "" {
		pr = filepath.Clean(pr)
	}
	// Load the artifact approval cache for read-only trust probes. If loading
	// fails during construction, degrade to an empty cache.
	approvalCache, _ := NewApprovalCache(DefaultApprovalCachePath())
	return &service{
		projectRoot:       pr,
		projectSkillsRoot: defaultProjectSkillsRoot(pr),
		superDolphinHome:  defaultSuperDolphinHome(),
		http:              &http.Client{Timeout: 15 * time.Second},
		approval:          approvalCache,
	}
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
	if personalType == "" {
		return ""
	}
	return filepath.Join(s.resolvedSuperDolphinHome(), "skills", "personal", personalType)
}

// canonicalRootForTarget 为target处理canonical根目录。
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

// writableSkillFileMode 处理writable技能文件模式。
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

// resolveScopedSkillPath 解析scoped技能路径。
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
