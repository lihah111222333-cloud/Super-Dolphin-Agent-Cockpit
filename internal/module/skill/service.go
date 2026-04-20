package skill

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

const (
	maxSkillFileBytes = 1 << 20
	skillMainFile     = "SKILL.md"
)

var (
	errSkillApprovalDenied      = errors.New("skill approval denied")
	errSkillApprovalUnavailable = errors.New("skill approval unavailable")
)

type skillApprovalRequester func(context.Context, platformrpc.ApprovalRequest) (contract.ApprovalDecision, error)

type skillExpandApprovalMeta struct {
	Scope  SkillApprovalScope
	Source SkillApprovalSource
	Result SkillApprovalResult
}

type service struct {
	root              string
	projectRoot       string
	projectSkillsRoot string
	http              *http.Client
	readConfigState   func(context.Context, string) (any, error)
	emitSkillsChanged skillsChangedEmitter
	skillsChangedMu   sync.Mutex
	skillsChangedNext uidto.SkillsChanged
	skillsChangedSeq  uint64
	approval          *ApprovalCache
	requestApproval   skillApprovalRequester
	sessionApprovalMu sync.RWMutex
	sessionApprovals  map[string]ApprovalEntry
	sections          contract.SectionInvalidator
	policy            SkillPolicy
	metrics           SkillMetrics
}

var _ Service = (*service)(nil)

func NewService(projectRoot string) Service {
	return newServiceWithDeps(projectRoot, nil, nil)
}

func newServiceWithDeps(projectRoot string, policy SkillPolicy, metrics SkillMetrics, requesters ...skillApprovalRequester) Service {
	pr := strings.TrimSpace(projectRoot)
	if policy == nil {
		policy = NewDefaultSkillPolicy(nil)
	}
	if metrics == nil {
		metrics = NewNoopSkillMetrics()
	}
	approvalCache, _ := NewApprovalCache(DefaultApprovalCachePath())
	requestApproval := skillApprovalRequester(nil)
	if len(requesters) != 0 {
		requestApproval = requesters[0]
	}
	if requestApproval == nil {
		requestApproval = newSkillApprovalRequester()
	}
	return &service{
		root:              defaultSkillsRoot(),
		projectRoot:       pr,
		projectSkillsRoot: defaultProjectSkillsRoot(pr),
		http:              &http.Client{Timeout: 15 * time.Second},
		approval:          approvalCache,
		requestApproval:   requestApproval,
		sessionApprovals:  make(map[string]ApprovalEntry),
		policy:            policy,
		metrics:           metrics,
	}
}

func newSkillApprovalRequester() skillApprovalRequester {
	manager := platformrpc.NewApprovalManager(nil, nil)
	bridge := platformrpc.NewPushBridge(nil, nil)
	return func(ctx context.Context, req platformrpc.ApprovalRequest) (contract.ApprovalDecision, error) {
		server := jrpc2.ServerFromContext(ctx)
		if server == nil {
			return contract.ApprovalDecision{}, errSkillApprovalUnavailable
		}
		return manager.RequestApproval(platformrpc.WithApprovalAutoDeclineOnCancel(ctx), bridge, server, req)
	}
}

func (s *service) ensureExpandApproved(ctx context.Context, record skillRecord, p SkillExpandParams) (skillExpandApprovalMeta, error) {
	scope, err := normalizeSkillApprovalScope(p.Scope)
	if err != nil {
		return skillExpandApprovalMeta{}, err
	}
	if scope == "" {
		return skillExpandApprovalMeta{}, nil
	}
	if meta, ok := s.lookupExpandApprovalMeta(record, scope); ok {
		return meta, nil
	}
	return s.requestExpandApproval(ctx, record, p, scope)
}

func (s *service) lookupExpandApprovalMeta(record skillRecord, scope SkillApprovalScope) (skillExpandApprovalMeta, bool) {
	meta := skillExpandApprovalMeta{Scope: scope}
	switch {
	case record.info.Trust.Trusted():
		meta.Source = SkillApprovalSourceTrusted
		meta.Result = SkillApprovalResultBypassed
		return meta, true
	case s.hasPersistedApproval(record.info.Name, record.info.ContentHash):
		meta.Source = SkillApprovalSourceProjectCache
		meta.Result = SkillApprovalResultCached
		return meta, true
	case scope == SkillApprovalScopeSession && s.hasSessionApproval(record.info.Name, record.info.ContentHash):
		meta.Source = SkillApprovalSourceSessionCache
		meta.Result = SkillApprovalResultCached
		return meta, true
	default:
		return skillExpandApprovalMeta{}, false
	}
}

func (s *service) requestExpandApproval(ctx context.Context, record skillRecord, p SkillExpandParams, scope SkillApprovalScope) (skillExpandApprovalMeta, error) {
	if s == nil || s.requestApproval == nil {
		return skillExpandApprovalMeta{}, errSkillApprovalUnavailable
	}
	callID := skillApprovalCallID(record.info.Name, record.info.ContentHash, scope)
	decision, err := s.requestApproval(ctx, platformrpc.ApprovalRequest{
		CallID:       callID,
		ApprovalID:   callID,
		ToolName:     "skill_expand",
		ThreadID:     strings.TrimSpace(p.ThreadID),
		Reason:       fmt.Sprintf("approve skill expand: %s", record.info.Name),
		Kind:         "skill",
		SourceMethod: "skill/requestApproval",
		Payload:      buildSkillApprovalPayload(s, record, p, scope),
	})
	if err != nil {
		return skillExpandApprovalMeta{}, err
	}
	if decision.Approved == nil || !*decision.Approved {
		return skillExpandApprovalMeta{}, fmt.Errorf("%w: %s", errSkillApprovalDenied, record.info.Name)
	}
	if err := s.storeExpandApproval(record, p, scope, decision); err != nil {
		return skillExpandApprovalMeta{}, err
	}
	return skillExpandApprovalMeta{
		Scope:  scope,
		Source: SkillApprovalSourceRequest,
		Result: SkillApprovalResultApproved,
	}, nil
}

func (s *service) storeExpandApproval(record skillRecord, p SkillExpandParams, scope SkillApprovalScope, decision contract.ApprovalDecision) error {
	approvedBy := resolveSkillApprovalApprovedBy(decision, p)
	if scope == SkillApprovalScopeProject {
		if s.approval == nil {
			return nil
		}
		_, err := s.approval.Approve(record.info.Name, record.info.ContentHash, record.info.Trust, approvedBy)
		return err
	}
	s.rememberSessionApproval(record.info.Name, record.info.ContentHash, record.info.Trust, approvedBy)
	return nil
}

func normalizeSkillApprovalScope(raw SkillApprovalScope) (SkillApprovalScope, error) {
	switch strings.ToLower(strings.TrimSpace(string(raw))) {
	case "":
		return "", nil
	case string(SkillApprovalScopeSession):
		return SkillApprovalScopeSession, nil
	case string(SkillApprovalScopeProject):
		return SkillApprovalScopeProject, nil
	default:
		return "", wrapInvalidExpand(fmt.Errorf("scope must be %q or %q", SkillApprovalScopeSession, SkillApprovalScopeProject))
	}
}

func buildSkillApprovalPayload(s *service, record skillRecord, p SkillExpandParams, scope SkillApprovalScope) map[string]any {
	payload := map[string]any{
		"name":         record.info.Name,
		"content_hash": record.info.ContentHash,
		"trust":        record.info.Trust,
		"scope":        scope,
		"skills_dir":   strings.TrimSpace(record.info.Dir),
		"project_root": strings.TrimSpace(s.projectRoot),
		"threadId":     strings.TrimSpace(p.ThreadID),
		"sessionId":    strings.TrimSpace(p.SessionID),
	}
	if source := strings.TrimSpace(p.Source); source != "" {
		payload["source"] = source
	}
	return payload
}

func resolveSkillApprovalApprovedBy(decision contract.ApprovalDecision, p SkillExpandParams) string {
	if source := strings.TrimSpace(p.Source); source != "" {
		return source
	}
	if sessionID := strings.TrimSpace(p.SessionID); sessionID != "" {
		return sessionID
	}
	if reason := strings.TrimSpace(decision.Reason); reason != "" && !strings.EqualFold(reason, "approved") {
		return reason
	}
	return ""
}

func (s *service) lookupPersistedApproval(name, contentHash string) (ApprovalEntry, bool) {
	if s == nil || s.approval == nil {
		return ApprovalEntry{}, false
	}
	return s.approval.Lookup(name, contentHash)
}

func (s *service) hasPersistedApproval(name, contentHash string) bool {
	_, ok := s.lookupPersistedApproval(name, contentHash)
	return ok
}

func (s *service) lookupSessionApproval(name, contentHash string) (ApprovalEntry, bool) {
	if s == nil {
		return ApprovalEntry{}, false
	}
	key := skillSessionApprovalKey(name, contentHash)
	s.sessionApprovalMu.RLock()
	defer s.sessionApprovalMu.RUnlock()
	entry, ok := s.sessionApprovals[key]
	if !ok || !strings.EqualFold(entry.ContentHash, contentHash) {
		return ApprovalEntry{}, false
	}
	return entry, true
}

func (s *service) hasSessionApproval(name, contentHash string) bool {
	_, ok := s.lookupSessionApproval(name, contentHash)
	return ok
}

func (s *service) rememberSessionApproval(name, contentHash string, trust TrustScope, approvedBy string) {
	if s == nil {
		return
	}
	entry := ApprovalEntry{
		Name:            strings.TrimSpace(name),
		ArtifactKind:    ArtifactKindBody,
		ArtifactLocator: skillMainFile,
		ContentHash:     strings.ToLower(strings.TrimSpace(contentHash)),
		Trust:           trust,
		ApprovedAt:      time.Now().UTC(),
		ApprovedBy:      strings.TrimSpace(approvedBy),
	}
	key := skillSessionApprovalKey(name, contentHash)
	s.sessionApprovalMu.Lock()
	if s.sessionApprovals == nil {
		s.sessionApprovals = make(map[string]ApprovalEntry)
	}
	s.sessionApprovals[key] = entry
	s.sessionApprovalMu.Unlock()
}

func skillSessionApprovalKey(name, contentHash string) string {
	return artifactApprovalKey(ApprovalRequest{
		Name:            name,
		ArtifactKind:    ArtifactKindBody,
		ArtifactLocator: skillMainFile,
		ContentHash:     contentHash,
	})
}

func skillApprovalCallID(name, contentHash string, scope SkillApprovalScope) string {
	name = strings.ToLower(strings.TrimSpace(name))
	hash := strings.ToLower(strings.TrimSpace(contentHash))
	if len(hash) > 12 {
		hash = hash[:12]
	}
	return fmt.Sprintf("skill-expand:%s:%s:%s", scope, name, hash)
}

func (s *service) invalidateSkillCatalog() {
	if s == nil || s.sections == nil {
		return
	}
	s.sections.InvalidateSections(contract.InvalidateSkillCatalogWrite, contract.DynamicSectionSkillCatalog)
}

func defaultSkillsRoot() string {
	if override := strings.TrimSpace(os.Getenv("SKILLS_ROOT")); override != "" {
		return override
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".multi-agent", "skills")
	}
	return filepath.Join(os.TempDir(), "multi-agent-skills")
}

func defaultProjectSkillsRoot(projectRoot string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return ""
	}
	return filepath.Join(projectRoot, ".agent", "skills")
}

func (s *service) skillRoots() []string {
	roots := make([]string, 0, 2)
	if v := strings.TrimSpace(s.projectSkillsRoot); v != "" {
		roots = append(roots, v)
	}
	if v := strings.TrimSpace(s.root); v != "" {
		roots = append(roots, v)
	}
	return roots
}
