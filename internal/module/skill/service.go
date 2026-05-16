package skill

import (
	"context"
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
	"github.com/anthropic-ai/super-agent-v3/internal/store/skillcandidate"
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
	disclosureTiers    contract.SkillDisclosureTierSource
	approvalRequester  contract.ApprovalRequester
	readConfigState    func(context.Context, string) (any, error)
	emitSkillsChanged  skillsChangedEmitter
	skillsChangedMu    sync.Mutex
	skillsChangedNext  uidto.SkillsChanged
	skillsChangedQueue []uidto.SkillsChanged
	skillsChangedSeq   uint64
	// approval 是 P20 Phase 1 新增的审批缓存指针。Phase 1 不涉及调用，预留给 Phase 6
	// skill_expand RPC 集成时使用 (s.approval.Lookup / Approve / Revoke)。初始化失败时
	// 降级为 nil；调用方必须先 nil-check。
	approval *ApprovalCache
	// sessionApprovals 仅缓存 scope=session 的整份 SKILL.md 审批，不落盘。
	sessionApprovals   map[string]ApprovalEntry
	sessionApprovalsMu sync.RWMutex
	approvalCallSeq    uint64
	// candidateStore is the optional P0b Step 5 wiring for the review
	// gate. nil means review-gate RPCs return errCandidateStoreUnavailable;
	// extractor + lookup paths short-circuit early.
	candidateStore skillcandidate.Store
	// auditStore is required in Fx wiring for mutation audit. Direct tests that
	// construct service manually may leave it nil; mutation paths then fail closed.
	auditStore    auditstore.Store
	mirrorTargets []SkillMirrorTarget
}

var _ Service = (*service)(nil)
var _ contract.ApprovalSource = (*service)(nil)
var _ contract.SkillMirrorReconciler = (*service)(nil)

type approvalScope string

const (
	approvalScopeSession approvalScope = "session"
	approvalScopeProject approvalScope = "project"
)

var (
	// ErrSkillApprovalDenied lets host-direct callers structure approval-denied
	// tool results without depending on package-private service internals.
	ErrSkillApprovalDenied               = errors.New("skill expand approval denied")
	errSkillApprovalDenied               = ErrSkillApprovalDenied
	errSkillApprovalRequesterUnavailable = errors.New("skill approval requester is not configured")
	errSkillApprovalProjectCacheMissing  = errors.New("project approval cache is not configured")
	errSkillApprovalRequired             = errors.New("skill artifact approval required")
)

// SkillApprovalRequiredError is an alias for the contract-level type so that
// both skill and toolbridge consumers can errors.As the same concrete type
// without toolbridge importing the module layer.
type SkillApprovalRequiredError = contract.SkillApprovalRequiredError

type skillApprovalDeniedError struct {
	reason string
}

func (e skillApprovalDeniedError) Error() string {
	reason := strings.TrimSpace(e.reason)
	if reason == "" {
		reason = "decline"
	}
	return fmt.Sprintf("%s: %s", errSkillApprovalDenied, reason)
}

func (e skillApprovalDeniedError) Unwrap() error { return errSkillApprovalDenied }

func (e skillApprovalDeniedError) SkillApprovalDenied() bool { return true }

func (s *service) LookupArtifactApproval(_ context.Context, req contract.ArtifactApprovalRequest) (bool, error) {
	if s == nil || s.approval == nil {
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

func (s *service) ApprovalRevision() uint64 {
	if s == nil || s.approval == nil {
		return 0
	}
	return s.approval.Revision()
}

func (s *service) SkillRevision() uint64 {
	if s == nil {
		return 0
	}
	return atomic.LoadUint64(&s.skillsChangedSeq)
}

func (s *service) TrustRevision() uint64 { return s.SkillRevision() }

func NewService(projectRoot string) Service {
	pr := strings.TrimSpace(projectRoot)
	// P20 Phase 1: 尝试加载审批缓存。文件不存在时返回空 cache（正常）；文件损坏时
	// NewApprovalCache 返回空 cache + err，此处处于构造期，无法抓 err 回调日志；
	// 统一降级为空 cache——下次 skill_expand 调用会当作“未审批”重新弹审批流。
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
	if configuredRoot := strings.TrimSpace(s.projectRoot); configuredRoot != "" && strings.TrimSpace(s.projectSkillsRoot) != "" {
		if resolvedConfigured, err := canonicalProjectPath(configuredRoot); err == nil {
			if resolvedCWD, err := canonicalProjectPath(cwd); err == nil && resolvedConfigured == resolvedCWD {
				return strings.TrimSpace(s.projectSkillsRoot)
			}
		}
	}
	if resolved, err := canonicalProjectPath(cwd); err == nil && strings.TrimSpace(resolved) != "" {
		return defaultProjectSkillsRoot(resolved)
	}
	return defaultProjectSkillsRoot(cwd)
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

func writableSkillFileMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	switch {
	case err == nil:
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

func (s *service) expandWithApproval(ctx context.Context, p skillExpandParams) (skillExpandResult, error) {
	prepared, err := s.prepareSkillExpand(ctx, p)
	if err != nil {
		return skillExpandResult{}, err
	}
	if err := s.ensureExpandApproved(ctx, p, prepared); err != nil {
		return skillExpandResult{}, err
	}
	return prepared.result, nil
}

func (s *service) ensureExpandApproved(ctx context.Context, p skillExpandParams, prepared skillExpandPrepared) error {
	if prepared.record.info.Trust.Trusted() {
		return nil
	}
	scope, err := resolveSkillExpandApprovalScope(p, prepared.cacheable)
	if err != nil {
		return err
	}
	if prepared.cacheable {
		if _, ok := s.lookupFullSkillApproval(prepared.record.info.Name, prepared.result.ContentHash, scope); ok {
			return nil
		}
	}
	if s.approvalRequester == nil {
		return errSkillApprovalRequesterUnavailable
	}
	decision, err := s.approvalRequester.RequestApproval(ctx, s.buildSkillExpandApprovalRequest(p, prepared, scope))
	if err != nil {
		return err
	}
	if decision.Approved == nil || !*decision.Approved {
		return deniedSkillApproval(decision)
	}
	if !prepared.cacheable {
		return nil
	}
	return s.persistFullSkillApproval(
		prepared.record.info.Name,
		prepared.result.ContentHash,
		prepared.record.info.Trust,
		approvalApprovedBy(decision),
		approvalScopeFromDecision(scope, decision, prepared.cacheable),
	)
}

func resolveSkillExpandApprovalScope(p skillExpandParams, cacheable bool) (approvalScope, error) {
	requested, err := requestedSkillExpandApprovalScope(p)
	if err != nil {
		return "", err
	}
	if !cacheable {
		return approvalScopeSession, nil
	}
	if requested == "" {
		return approvalScopeProject, nil
	}
	return requested, nil
}

func requestedSkillExpandApprovalScope(p skillExpandParams) (approvalScope, error) {
	explicit := strings.TrimSpace(p.ApprovalScope)
	alias := strings.TrimSpace(p.Scope)
	switch {
	case explicit == "":
		explicit = alias
	case alias != "" && !strings.EqualFold(explicit, alias):
		return "", fmt.Errorf("%w: approval scope mismatch", errInvalidSkillExpandParam)
	}
	switch strings.ToLower(explicit) {
	case "":
		return "", nil
	case string(approvalScopeSession):
		return approvalScopeSession, nil
	case string(approvalScopeProject):
		return approvalScopeProject, nil
	default:
		return "", fmt.Errorf("%w: approval_scope must be \"session\" or \"project\"", errInvalidSkillExpandParam)
	}
}

func (s *service) buildSkillExpandApprovalRequest(p skillExpandParams, prepared skillExpandPrepared, scope approvalScope) contract.ApprovalRequest {
	payload := map[string]any{
		"name":           prepared.result.Name,
		"section":        prepared.result.Section,
		"content_hash":   prepared.result.ContentHash,
		"trust":          prepared.result.Trust,
		"approval_scope": string(scope),
		"skills_dir":     prepared.record.info.Dir,
		"project_root":   strings.TrimSpace(s.projectRoot),
	}
	if value := strings.TrimSpace(p.AgentID); value != "" {
		payload["agentId"] = value
	}
	if value := strings.TrimSpace(p.ThreadID); value != "" {
		payload["threadId"] = value
	}
	if value := strings.TrimSpace(p.SessionID); value != "" {
		payload["sessionId"] = value
	}
	if value := strings.TrimSpace(p.TurnID); value != "" {
		payload["turnId"] = value
	}
	return contract.ApprovalRequest{
		CallID:       s.nextApprovalCallID(prepared.result.Name),
		ToolName:     "skill/expand",
		AgentID:      strings.TrimSpace(p.AgentID),
		ThreadID:     strings.TrimSpace(p.ThreadID),
		TurnID:       strings.TrimSpace(p.TurnID),
		Reason:       "skill expand requires approval",
		Kind:         "skill",
		SourceMethod: "skill/expand",
		Payload:      payload,
	}
}

func (s *service) nextApprovalCallID(name string) string {
	seq := atomic.AddUint64(&s.approvalCallSeq, 1)
	return fmt.Sprintf("skill-expand:%s:%d", strings.ToLower(strings.TrimSpace(name)), seq)
}

func (s *service) lookupFullSkillApproval(name, contentHash string, scope approvalScope) (ApprovalEntry, bool) {
	if scope == approvalScopeSession {
		if entry, ok := s.lookupSessionSkillApproval(name, contentHash); ok {
			return entry, true
		}
	}
	return s.approval.Lookup(name, contentHash)
}

func (s *service) lookupSessionSkillApproval(name, contentHash string) (ApprovalEntry, bool) {
	s.sessionApprovalsMu.RLock()
	defer s.sessionApprovalsMu.RUnlock()
	if len(s.sessionApprovals) == 0 {
		return ApprovalEntry{}, false
	}
	entry, ok := s.sessionApprovals[skillApprovalSessionKey(name, contentHash)]
	return entry, ok
}

func (s *service) persistFullSkillApproval(name, contentHash string, trust TrustScope, approvedBy string, scope approvalScope) error {
	switch scope {
	case approvalScopeSession:
		s.rememberSessionSkillApproval(name, contentHash, trust, approvedBy)
		return nil
	case approvalScopeProject:
		if s.approval == nil {
			return errSkillApprovalProjectCacheMissing
		}
		_, err := s.approval.Approve(name, contentHash, trust, approvedBy)
		return err
	default:
		return fmt.Errorf("%w: unsupported approval scope %q", errInvalidSkillExpandParam, scope)
	}
}

func (s *service) rememberSessionSkillApproval(name, contentHash string, trust TrustScope, approvedBy string) {
	s.sessionApprovalsMu.Lock()
	defer s.sessionApprovalsMu.Unlock()
	if s.sessionApprovals == nil {
		s.sessionApprovals = make(map[string]ApprovalEntry)
	}
	s.sessionApprovals[skillApprovalSessionKey(name, contentHash)] = ApprovalEntry{
		Name:            strings.ToLower(strings.TrimSpace(name)),
		ArtifactKind:    ArtifactKindBody,
		ArtifactLocator: "SKILL.md",
		ContentHash:     strings.ToLower(strings.TrimSpace(contentHash)),
		Trust:           trust,
		ApprovedAt:      time.Now().UTC(),
		ApprovedBy:      strings.TrimSpace(approvedBy),
	}
}

func skillApprovalSessionKey(name, contentHash string) string {
	return strings.ToLower(strings.TrimSpace(name)) + "@" + strings.ToLower(strings.TrimSpace(contentHash))
}

func approvalScopeFromDecision(fallback approvalScope, decision contract.ApprovalDecision, cacheable bool) approvalScope {
	if !cacheable {
		return approvalScopeSession
	}
	if scope := approvalScopeFromDetail(decision.Detail); scope != "" {
		return scope
	}
	if strings.EqualFold(strings.TrimSpace(decision.Reason), "acceptForSession") {
		return approvalScopeSession
	}
	return fallback
}

func approvalScopeFromDetail(raw json.RawMessage) approvalScope {
	payload := approvalDecisionPayload(raw)
	for _, key := range []string{"approval_scope", "scope"} {
		value, _ := payload[key].(string)
		switch strings.ToLower(strings.TrimSpace(value)) {
		case string(approvalScopeSession):
			return approvalScopeSession
		case string(approvalScopeProject):
			return approvalScopeProject
		}
	}
	return ""
}

func approvalApprovedBy(decision contract.ApprovalDecision) string {
	payload := approvalDecisionPayload(decision.Detail)
	for _, key := range []string{"approved_by", "approvedBy", "reviewed_by", "reviewedBy"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func approvalDecisionPayload(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload
}

func deniedSkillApproval(decision contract.ApprovalDecision) error {
	return skillApprovalDeniedError{reason: decision.Reason}
}
