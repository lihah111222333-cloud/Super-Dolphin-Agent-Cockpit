package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type approvalRecorder struct {
	calls    []platformrpc.ApprovalRequest
	approved bool
	err      error
}

func (r *approvalRecorder) request(_ context.Context, req platformrpc.ApprovalRequest) (contract.ApprovalDecision, error) {
	r.calls = append(r.calls, req)
	if r.err != nil {
		return contract.ApprovalDecision{}, r.err
	}
	return contract.ApprovalDecision{Approved: boolPtr(r.approved), Reason: "approved"}, nil
}

func boolPtr(v bool) *bool { return &v }

func newApprovalFlowService(t *testing.T, recorder *approvalRecorder) (*service, string, string) {
	t.Helper()
	userRoot := t.TempDir()
	trustPath := filepath.Join(t.TempDir(), "skills-trust.json")
	projectRoot := t.TempDir()
	t.Setenv("SKILLS_ROOT", userRoot)
	t.Setenv("SKILLS_TRUST_PATH", trustPath)
	var svc *service
	if recorder == nil {
		svc = NewService(projectRoot).(*service)
	} else {
		svc = newServiceWithDeps(projectRoot, nil, nil, recorder.request).(*service)
	}
	return svc, userRoot, filepath.Join(projectRoot, ".agent", "skills")
}

func TestExpandApprovalFlow_TrustedBypass(t *testing.T) {
	recorder := &approvalRecorder{approved: true}
	svc, userRoot, _ := newApprovalFlowService(t, recorder)
	writeTestSkill(t, userRoot, "trusted-skill", "---\nname: trusted-skill\n---\n# trusted\n")

	result, err := svc.Expand(context.Background(), SkillExpandParams{Name: "trusted-skill", Scope: SkillApprovalScopeProject})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("approval requests = %d, want 0", len(recorder.calls))
	}
	if result.ApprovalSource != SkillApprovalSourceTrusted || result.ApprovalResult != SkillApprovalResultBypassed {
		t.Fatalf("approval meta = %+v", result)
	}
}

func TestExpandApprovalFlow_ProjectMissRequestsAndPersists(t *testing.T) {
	recorder := &approvalRecorder{approved: true}
	svc, _, projectSkillsRoot := newApprovalFlowService(t, recorder)
	writeTestSkill(t, projectSkillsRoot, "project-skill", "---\nname: project-skill\n---\n# project\n")
	record, err := svc.findSkillRecordByName("project-skill")
	if err != nil {
		t.Fatalf("findSkillRecordByName() error = %v", err)
	}

	result, err := svc.Expand(context.Background(), SkillExpandParams{
		Name:      "project-skill",
		Scope:     SkillApprovalScopeProject,
		Source:    "rpc-test",
		ThreadID:  "thread-1",
		SessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("approval requests = %d, want 1", len(recorder.calls))
	}
	call := recorder.calls[0]
	if call.Kind != "skill" || call.SourceMethod != "skill/requestApproval" || call.ToolName != "skill_expand" {
		t.Fatalf("approval request = %+v", call)
	}
	if call.Payload["scope"] != SkillApprovalScopeProject || call.Payload["threadId"] != "thread-1" || call.Payload["sessionId"] != "session-1" {
		t.Fatalf("approval payload = %#v", call.Payload)
	}
	if _, ok := svc.approval.Lookup("project-skill", record.info.ContentHash); !ok {
		t.Fatal("project approval cache miss after approval")
	}
	if result.ApprovalSource != SkillApprovalSourceRequest || result.ApprovalResult != SkillApprovalResultApproved {
		t.Fatalf("approval meta = %+v", result)
	}
}

func TestExpandApprovalFlow_ProjectCacheHitSkipsRequester(t *testing.T) {
	recorder := &approvalRecorder{approved: true}
	svc, _, projectSkillsRoot := newApprovalFlowService(t, recorder)
	writeTestSkill(t, projectSkillsRoot, "cached-skill", "---\nname: cached-skill\n---\n# project\n")
	record, err := svc.findSkillRecordByName("cached-skill")
	if err != nil {
		t.Fatalf("findSkillRecordByName() error = %v", err)
	}
	if _, err := svc.approval.Approve("cached-skill", record.info.ContentHash, record.info.Trust, "seed"); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}

	result, err := svc.Expand(context.Background(), SkillExpandParams{Name: "cached-skill", Scope: SkillApprovalScopeProject})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("approval requests = %d, want 0", len(recorder.calls))
	}
	if result.ApprovalSource != SkillApprovalSourceProjectCache || result.ApprovalResult != SkillApprovalResultCached {
		t.Fatalf("approval meta = %+v", result)
	}
}

func TestExpandApprovalFlow_HashChangeReRequests(t *testing.T) {
	recorder := &approvalRecorder{approved: true}
	svc, _, projectSkillsRoot := newApprovalFlowService(t, recorder)
	skillPath := writeTestSkill(t, projectSkillsRoot, "recheck-skill", "---\nname: recheck-skill\n---\n# v1\n")
	oldRecord, err := svc.findSkillRecordByName("recheck-skill")
	if err != nil {
		t.Fatalf("findSkillRecordByName() error = %v", err)
	}
	if _, err := svc.approval.Approve("recheck-skill", oldRecord.info.ContentHash, oldRecord.info.Trust, "seed"); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: recheck-skill\n---\n# v2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	newRecord, err := svc.findSkillRecordByName("recheck-skill")
	if err != nil {
		t.Fatalf("findSkillRecordByName() error = %v", err)
	}
	if _, ok := svc.approval.Lookup("recheck-skill", newRecord.info.ContentHash); ok {
		t.Fatal("new hash should miss before re-approval")
	}

	if _, err := svc.Expand(context.Background(), SkillExpandParams{Name: "recheck-skill", Scope: SkillApprovalScopeProject}); err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("approval requests = %d, want 1", len(recorder.calls))
	}
	if recorder.calls[0].Payload["content_hash"] != newRecord.info.ContentHash {
		t.Fatalf("approval payload hash = %#v, want %q", recorder.calls[0].Payload["content_hash"], newRecord.info.ContentHash)
	}
	if _, ok := svc.approval.Lookup("recheck-skill", newRecord.info.ContentHash); !ok {
		t.Fatal("new hash should be cached after re-approval")
	}
}

func TestExpandApprovalFlow_SessionScopeStaysInMemory(t *testing.T) {
	recorder := &approvalRecorder{approved: true}
	svc, _, projectSkillsRoot := newApprovalFlowService(t, recorder)
	writeTestSkill(t, projectSkillsRoot, "session-skill", "---\nname: session-skill\n---\n# project\n")

	first, err := svc.Expand(context.Background(), SkillExpandParams{Name: "session-skill", Scope: SkillApprovalScopeSession})
	if err != nil {
		t.Fatalf("first Expand() error = %v", err)
	}
	second, err := svc.Expand(context.Background(), SkillExpandParams{Name: "session-skill", Scope: SkillApprovalScopeSession})
	if err != nil {
		t.Fatalf("second Expand() error = %v", err)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("approval requests = %d, want 1", len(recorder.calls))
	}
	if got := len(svc.approval.Entries()); got != 0 {
		t.Fatalf("persistent approvals = %d, want 0", got)
	}
	if _, err := os.Stat(svc.approval.Path()); !os.IsNotExist(err) {
		t.Fatalf("approval cache file err = %v, want not-exist", err)
	}
	if first.ApprovalSource != SkillApprovalSourceRequest || second.ApprovalSource != SkillApprovalSourceSessionCache {
		t.Fatalf("approval sources = %q, %q", first.ApprovalSource, second.ApprovalSource)
	}
}
