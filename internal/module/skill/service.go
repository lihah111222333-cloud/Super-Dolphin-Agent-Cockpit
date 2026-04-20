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
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	maxSkillFileBytes = 1 << 20
	skillMainFile     = "SKILL.md"
)

type service struct {
	root              string
	projectRoot       string
	projectSkillsRoot string
	http              *http.Client
	approvalRequester contract.ApprovalRequester
	readConfigState   func(context.Context, string) (any, error)
	emitSkillsChanged skillsChangedEmitter
	skillsChangedMu   sync.Mutex
	skillsChangedNext uidto.SkillsChanged
	skillsChangedSeq  uint64
	// approval 是 P20 Phase 1 新增的审批缓存指针。Phase 1 不涉及调用，预留给 Phase 6
	// skill_expand RPC 集成时使用 (s.approval.Lookup / Approve / Revoke)。初始化失败时
	// 降级为 nil；调用方必须先 nil-check。
	approval *ApprovalCache
	// sessionApprovals 仅缓存 scope=session 的整份 SKILL.md 审批，不落盘。
	sessionApprovals   map[string]ApprovalEntry
	sessionApprovalsMu sync.RWMutex
	approvalCallSeq    uint64
}

var _ Service = (*service)(nil)

type approvalScope string

const (
	approvalScopeSession approvalScope = "session"
	approvalScopeProject approvalScope = "project"
)

var (
	errSkillApprovalDenied               = errors.New("skill expand approval denied")
	errSkillApprovalRequesterUnavailable = errors.New("skill approval requester is not configured")
	errSkillApprovalProjectCacheMissing  = errors.New("project approval cache is not configured")
)

func NewService(projectRoot string) Service {
	pr := strings.TrimSpace(projectRoot)
	// P20 Phase 1: 尝试加载审批缓存。文件不存在时返回空 cache（正常）；文件损坏时
	// NewApprovalCache 返回空 cache + err，此处处于构造期，无法抓 err 回调日志；
	// 统一降级为空 cache——下次 skill_expand 调用会当作“未审批”重新弹审批流。
	approvalCache, _ := NewApprovalCache(DefaultApprovalCachePath())
	return &service{
		root:              defaultSkillsRoot(),
		projectRoot:       pr,
		projectSkillsRoot: defaultProjectSkillsRoot(pr),
		http:              &http.Client{Timeout: 15 * time.Second},
		approval:          approvalCache,
	}
}

func defaultSkillsRoot() string {
	if override := strings.TrimSpace(os.Getenv("SKILLS_ROOT")); override != "" {
		return override
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".multi-agent", "skills")
	}
	// UserHomeDir 失败（如无 $HOME 的受限环境）时兜底到临时目录，
	// 避免 s.root 为空导致整个技能功能静默失效。
	return filepath.Join(os.TempDir(), "multi-agent-skills")
}

func defaultProjectSkillsRoot(projectRoot string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return ""
	}
	return filepath.Join(projectRoot, ".agent", "skills")
}

// skillRoots 返回扫描/校验时按优先级排列的技能根目录：
// 项目根（若有）优先于系统根。空根会被过滤。
func (s *service) skillRoots(cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	roots := make([]string, 0, 2)
	if v := strings.TrimSpace(s.projectSkillsRootForCWD(cwd)); v != "" {
		roots = append(roots, v)
	}
	if v := strings.TrimSpace(s.root); v != "" {
		if projectKey := platformshared.ProjectKeyFromCwd(cwd); projectKey != "" {
			roots = append(roots, filepath.Join(v, projectKey))
		}
	}
	return roots
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
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = "decline"
	}
	return fmt.Errorf("%w: %s", errSkillApprovalDenied, reason)
}
