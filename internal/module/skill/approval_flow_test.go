package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type stubApprovalRequester struct {
	mu        sync.Mutex
	calls     []contract.ApprovalRequest
	decisions []contract.ApprovalDecision
}

func (s *stubApprovalRequester) RequestApproval(_ context.Context, req contract.ApprovalRequest) (contract.ApprovalDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	if len(s.decisions) == 0 {
		return approvedSkillDecision(nil), nil
	}
	decision := s.decisions[0]
	s.decisions = s.decisions[1:]
	return decision, nil
}

func (s *stubApprovalRequester) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func approvedSkillDecision(detail map[string]any) contract.ApprovalDecision {
	raw, _ := json.Marshal(detail)
	approved := true
	return contract.ApprovalDecision{Approved: &approved, Reason: "approved", Detail: raw}
}

func newApprovalFlowService(t *testing.T) (*service, string, *stubApprovalRequester) {
	t.Helper()
	tmp := t.TempDir()
	projectRoot := filepath.Join(tmp, "proj")
	skillsRoot := filepath.Join(projectRoot, ".agent", "skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		t.Fatalf("mkdir skills root: %v", err)
	}
	svc := NewService(projectRoot).(*service)
	cache, err := NewApprovalCache(filepath.Join(tmp, "skills-trust.json"))
	if err != nil {
		t.Fatalf("NewApprovalCache() error = %v", err)
	}
	requester := &stubApprovalRequester{}
	svc.approval = cache
	svc.approvalRequester = requester
	return svc, skillsRoot, requester
}

func TestExpandWithApprovalTrustedBypass(t *testing.T) {
	svc, root, requester := newApprovalFlowService(t)
	writeExpandTestSkill(t, root, "demo", "---\nname: demo\ntrust: user\n---\ntrusted body")
	if _, err := svc.expandWithApproval(context.Background(), skillExpandParams{Name: "demo"}); err != nil {
		t.Fatalf("expandWithApproval() error = %v", err)
	}
	if got := requester.callCount(); got != 0 {
		t.Fatalf("approval calls = %d, want 0", got)
	}
}

func TestExpandWithApprovalProjectScopePersistsCache(t *testing.T) {
	svc, root, requester := newApprovalFlowService(t)
	writeExpandTestSkill(t, root, "demo", "---\nname: demo\n---\nproject body")
	requester.decisions = []contract.ApprovalDecision{approvedSkillDecision(map[string]any{
		"approval_scope": "project",
		"approved_by":    "user@local",
	})}
	res, err := svc.expandWithApproval(context.Background(), skillExpandParams{Name: "demo", ApprovalScope: "project"})
	if err != nil {
		t.Fatalf("expandWithApproval() error = %v", err)
	}
	if got := requester.callCount(); got != 1 {
		t.Fatalf("approval calls = %d, want 1", got)
	}
	if entry, ok := svc.approval.Lookup("demo", res.ContentHash); !ok || entry.ApprovedBy != "user@local" {
		t.Fatalf("project cache miss or approved_by mismatch: %+v, ok=%v", entry, ok)
	}
}

func TestExpandWithApprovalFullSkillCacheHitSkipsApproval(t *testing.T) {
	svc, root, requester := newApprovalFlowService(t)
	writeExpandTestSkill(t, root, "demo", "---\nname: demo\n---\ncache me")
	if _, err := svc.expandWithApproval(context.Background(), skillExpandParams{Name: "demo", ApprovalScope: "project"}); err != nil {
		t.Fatalf("first expandWithApproval() error = %v", err)
	}
	if _, err := svc.expandWithApproval(context.Background(), skillExpandParams{Name: "demo", ApprovalScope: "project"}); err != nil {
		t.Fatalf("second expandWithApproval() error = %v", err)
	}
	if got := requester.callCount(); got != 1 {
		t.Fatalf("approval calls = %d, want 1", got)
	}
}

func TestExpandWithApprovalHashChangeRequestsAgain(t *testing.T) {
	svc, root, requester := newApprovalFlowService(t)
	writeExpandTestSkill(t, root, "demo", "---\nname: demo\n---\nversion one")
	requester.decisions = []contract.ApprovalDecision{approvedSkillDecision(nil), approvedSkillDecision(nil)}
	if _, err := svc.expandWithApproval(context.Background(), skillExpandParams{Name: "demo", ApprovalScope: "project"}); err != nil {
		t.Fatalf("first expandWithApproval() error = %v", err)
	}
	writeExpandTestSkill(t, root, "demo", "---\nname: demo\n---\nversion two")
	if _, err := svc.expandWithApproval(context.Background(), skillExpandParams{Name: "demo", ApprovalScope: "project"}); err != nil {
		t.Fatalf("second expandWithApproval() error = %v", err)
	}
	if got := requester.callCount(); got != 2 {
		t.Fatalf("approval calls = %d, want 2", got)
	}
}

func TestExpandWithApprovalSectionAndResourceStayPerCall(t *testing.T) {
	svc, root, requester := newApprovalFlowService(t)
	writeExpandTestSkill(t, root, "demo", "---\nname: demo\n---\n## Usage\nhello")
	resourceDir := filepath.Join(root, "demo", "references")
	if err := os.MkdirAll(resourceDir, 0o755); err != nil {
		t.Fatalf("mkdir resource dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resourceDir, "api.md"), []byte("resource body"), 0o644); err != nil {
		t.Fatalf("write resource: %v", err)
	}
	requester.decisions = []contract.ApprovalDecision{
		approvedSkillDecision(nil), approvedSkillDecision(nil),
		approvedSkillDecision(nil), approvedSkillDecision(nil),
	}
	for _, tc := range []skillExpandParams{
		{Name: "demo", Section: "## Usage", ApprovalScope: "project"},
		{Name: "demo", Section: "references/api.md", ApprovalScope: "project"},
	} {
		before := requester.callCount()
		for i := 0; i < 2; i++ {
			if _, err := svc.expandWithApproval(context.Background(), tc); err != nil {
				t.Fatalf("expandWithApproval(%q) error = %v", tc.Section, err)
			}
		}
		if got := requester.callCount() - before; got != 2 {
			t.Fatalf("approval calls for %q = %d, want 2", tc.Section, got)
		}
	}
	if entries := svc.approval.Entries(); len(entries) != 0 {
		t.Fatalf("persistent cache entries = %d, want 0", len(entries))
	}
}

func TestExpandWithApprovalSessionScopeStaysInMemory(t *testing.T) {
	svc, root, requester := newApprovalFlowService(t)
	writeExpandTestSkill(t, root, "demo", "---\nname: demo\n---\nsession only")
	requester.decisions = []contract.ApprovalDecision{approvedSkillDecision(map[string]any{"approval_scope": "session"})}
	for i := 0; i < 2; i++ {
		if _, err := svc.expandWithApproval(context.Background(), skillExpandParams{Name: "demo", ApprovalScope: "session"}); err != nil {
			t.Fatalf("expandWithApproval() error = %v", err)
		}
	}
	if got := requester.callCount(); got != 1 {
		t.Fatalf("approval calls = %d, want 1", got)
	}
	if entries := svc.approval.Entries(); len(entries) != 0 {
		t.Fatalf("persistent cache entries = %d, want 0", len(entries))
	}
}
