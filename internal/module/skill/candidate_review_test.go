package skill

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/skillcandidate"
)

const validReviewFP = "0123456789abcdef0123456789abcdef"

// fakeCandidateStore is a minimal in-memory skillcandidate.Store double
// scoped to a single review-gate test. Each *Fn override lets the test
// inject the exact behaviour it cares about; sensible defaults keep
// individual cases short.
type fakeCandidateStore struct {
	mu           sync.Mutex
	getByID      func(int64) (skillcandidate.Candidate, error)
	approve      func(int64, string, string, time.Time) (skillcandidate.Candidate, error)
	reject       func(int64, string) (skillcandidate.Candidate, error)
	promote      func(int64) (skillcandidate.Candidate, error)
	listFn       func(int32, int32) ([]skillcandidate.Candidate, error)
	lookupFn     func(scope, slug, hash, fp string) (*skillcandidate.Candidate, error)
	approveCalls []struct {
		ID         int64
		ApprovedBy string
		Reason     string
		ApprovedAt time.Time
	}
	rejectCalls []struct {
		ID     int64
		Reason string
	}
	promoteCalls []int64
	lookupCalls  []struct {
		Scope, Slug, Hash, FP string
	}
}

func (f *fakeCandidateStore) Insert(_ context.Context, _ skillcandidate.InsertParams) (skillcandidate.Candidate, error) {
	return skillcandidate.Candidate{}, errors.New("Insert not used")
}

func (f *fakeCandidateStore) GetByID(_ context.Context, id int64) (skillcandidate.Candidate, error) {
	if f.getByID != nil {
		return f.getByID(id)
	}
	return skillcandidate.Candidate{}, errors.New("GetByID not configured")
}

func (f *fakeCandidateStore) ListPending(_ context.Context, _ string, limit, offset int32) ([]skillcandidate.Candidate, error) {
	if f.listFn != nil {
		return f.listFn(limit, offset)
	}
	return nil, nil
}

func (f *fakeCandidateStore) MarkSuperseded(context.Context, string, string, string, int64) (int64, error) {
	return 0, nil
}

func (f *fakeCandidateStore) Approve(_ context.Context, id int64, approvedBy, reason string, at time.Time) (skillcandidate.Candidate, error) {
	f.mu.Lock()
	f.approveCalls = append(f.approveCalls, struct {
		ID         int64
		ApprovedBy string
		Reason     string
		ApprovedAt time.Time
	}{id, approvedBy, reason, at})
	f.mu.Unlock()
	if f.approve != nil {
		return f.approve(id, approvedBy, reason, at)
	}
	// Default: derive from getByID so Slug/SkillMD/Scope/RepoFingerprint
	// are preserved. ApproveCandidate -> CreateSkill needs Slug + SkillMD
	// downstream, so a bare stub here would crash with "invalid skill name".
	base := skillcandidate.Candidate{ID: id}
	if f.getByID != nil {
		if got, err := f.getByID(id); err == nil {
			base = got
		}
	}
	base.Status = skillcandidate.StatusApproved
	base.ApprovedBy = approvedBy
	base.Reason = reason
	base.ApprovedAt = &at
	return base, nil
}

func (f *fakeCandidateStore) Reject(_ context.Context, id int64, reason string) (skillcandidate.Candidate, error) {
	f.mu.Lock()
	f.rejectCalls = append(f.rejectCalls, struct {
		ID     int64
		Reason string
	}{id, reason})
	f.mu.Unlock()
	if f.reject != nil {
		return f.reject(id, reason)
	}
	return skillcandidate.Candidate{ID: id, Status: skillcandidate.StatusRejected, Reason: reason}, nil
}

func (f *fakeCandidateStore) MarkPromoted(_ context.Context, id int64) (skillcandidate.Candidate, error) {
	f.mu.Lock()
	f.promoteCalls = append(f.promoteCalls, id)
	f.mu.Unlock()
	if f.promote != nil {
		return f.promote(id)
	}
	return skillcandidate.Candidate{ID: id, Status: skillcandidate.StatusPromoted}, nil
}

func (f *fakeCandidateStore) LookupApproval(_ context.Context, scope, slug, hash, fp string) (*skillcandidate.Candidate, error) {
	f.mu.Lock()
	f.lookupCalls = append(f.lookupCalls, struct {
		Scope, Slug, Hash, FP string
	}{scope, slug, hash, fp})
	f.mu.Unlock()
	if f.lookupFn != nil {
		return f.lookupFn(scope, slug, hash, fp)
	}
	return nil, nil
}

// fakeAuditStore captures Insert calls for assertion. List is unused
// by review-gate code paths.
type fakeAuditStore struct {
	mu      sync.Mutex
	inserts []auditstore.InsertParams
}

func (f *fakeAuditStore) List(_ context.Context, _ auditstore.ListFilter) ([]auditstore.AuditEvent, error) {
	return nil, nil
}

func (f *fakeAuditStore) Insert(_ context.Context, p auditstore.InsertParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inserts = append(f.inserts, p)
	return nil
}

// newReviewService constructs a *service wired to the supplied review-
// gate fakes. CreateSkill writes land under a per-test temp project root
// so the assertions can inspect the on-disk SKILL.md without polluting
// the developer machine.
func newReviewService(t *testing.T, cs skillcandidate.Store, as auditstore.Store) (*service, string) {
	t.Helper()
	systemRoot := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "repo-review")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	svc := &service{
		root:              systemRoot,
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		http:              &http.Client{},
		candidateStore:    cs,
		auditStore:        as,
	}
	return svc, projectRoot
}

func TestReview_RequiresApprovedBy(t *testing.T) {
	t.Parallel()
	cs := &fakeCandidateStore{}
	svc, projectRoot := newReviewService(t, cs, nil)
	_, err := svc.ApproveCandidate(skillTestContext(projectRoot), ApproveCandidateParams{
		CandidateID: 1,
		ApprovedBy:  "   ",
	})
	if !errors.Is(err, ErrCandidateApprovedByRequired) {
		t.Fatalf("err = %v, want ErrCandidateApprovedByRequired", err)
	}
	if len(cs.approveCalls) != 0 {
		t.Fatalf("store.Approve must not be invoked when approved_by is blank: %d calls", len(cs.approveCalls))
	}
}

func TestReview_ProjectScopeRequiresRepoFingerprint(t *testing.T) {
	t.Parallel()
	cs := &fakeCandidateStore{
		getByID: func(id int64) (skillcandidate.Candidate, error) {
			return skillcandidate.Candidate{
				ID:              id,
				Scope:           skillcandidate.ScopeProject,
				Slug:            "noisy",
				Status:          skillcandidate.StatusPendingReview,
				RepoFingerprint: "   ",
				SkillMD:         "# noisy\n",
			}, nil
		},
	}
	svc, projectRoot := newReviewService(t, cs, nil)
	_, err := svc.ApproveCandidate(skillTestContext(projectRoot), ApproveCandidateParams{
		CandidateID:           7,
		ApprovedBy:            "alice",
		CallerRepoFingerprint: validReviewFP,
	})
	if !errors.Is(err, ErrCandidateMissingFingerprint) {
		t.Fatalf("err = %v, want ErrCandidateMissingFingerprint", err)
	}
	if len(cs.approveCalls) != 0 {
		t.Fatalf("store.Approve must not be invoked when fingerprint missing: %d calls", len(cs.approveCalls))
	}
}

func TestReview_RejectsRepoFingerprintMismatch(t *testing.T) {
	t.Parallel()
	cs := &fakeCandidateStore{
		getByID: func(id int64) (skillcandidate.Candidate, error) {
			return skillcandidate.Candidate{
				ID:              id,
				Scope:           skillcandidate.ScopeProject,
				Slug:            "demo",
				RepoFingerprint: validReviewFP,
				Status:          skillcandidate.StatusPendingReview,
				SkillMD:         "# demo\n",
			}, nil
		},
	}
	svc, projectRoot := newReviewService(t, cs, nil)
	_, err := svc.ApproveCandidate(skillTestContext(projectRoot), ApproveCandidateParams{
		CandidateID:           8,
		ApprovedBy:            "alice",
		CallerRepoFingerprint: "fedcba9876543210fedcba9876543210",
	})
	if !errors.Is(err, ErrRepoFingerprintMismatch) {
		t.Fatalf("err = %v, want ErrRepoFingerprintMismatch", err)
	}
	if len(cs.approveCalls) != 0 {
		t.Fatalf("store.Approve must not be invoked on repo mismatch: %d calls", len(cs.approveCalls))
	}
}

func TestReview_RequiresCallerRepoFingerprint(t *testing.T) {
	t.Parallel()
	cs := &fakeCandidateStore{
		getByID: func(id int64) (skillcandidate.Candidate, error) {
			return skillcandidate.Candidate{
				ID:              id,
				Scope:           skillcandidate.ScopeProject,
				Slug:            "demo",
				RepoFingerprint: validReviewFP,
				Status:          skillcandidate.StatusPendingReview,
				SkillMD:         "# demo\n",
			}, nil
		},
	}
	svc, projectRoot := newReviewService(t, cs, nil)
	_, err := svc.ApproveCandidate(skillTestContext(projectRoot), ApproveCandidateParams{CandidateID: 9, ApprovedBy: "alice"})
	if !errors.Is(err, ErrCallerFingerprintRequired) {
		t.Fatalf("err = %v, want ErrCallerFingerprintRequired", err)
	}
	if len(cs.approveCalls) != 0 {
		t.Fatalf("store.Approve must not be invoked without caller fingerprint: %d calls", len(cs.approveCalls))
	}
}

func TestReview_NotPendingCannotApprove(t *testing.T) {
	t.Parallel()
	cs := &fakeCandidateStore{
		getByID: func(id int64) (skillcandidate.Candidate, error) {
			return skillcandidate.Candidate{
				ID:              id,
				Scope:           skillcandidate.ScopeProject,
				Slug:            "demo",
				RepoFingerprint: validReviewFP,
				Status:          skillcandidate.StatusApproved, // already past pending
				SkillMD:         "# demo\n",
			}, nil
		},
	}
	svc, projectRoot := newReviewService(t, cs, nil)
	_, err := svc.ApproveCandidate(skillTestContext(projectRoot), ApproveCandidateParams{
		CandidateID:           1,
		ApprovedBy:            "alice",
		CallerRepoFingerprint: validReviewFP,
	})
	if !errors.Is(err, ErrCandidateNotPending) {
		t.Fatalf("err = %v, want ErrCandidateNotPending", err)
	}
	if len(cs.approveCalls) != 0 {
		t.Fatalf("store.Approve must not be invoked when status != pending_review")
	}
}

func TestReview_HappyPathCallsCreateSkillAndAudit(t *testing.T) {
	t.Parallel()
	const slug = "demo-skill"
	const md = "# demo\nbody"
	cs := &fakeCandidateStore{
		getByID: func(id int64) (skillcandidate.Candidate, error) {
			return skillcandidate.Candidate{
				ID:              id,
				Scope:           skillcandidate.ScopeProject,
				Slug:            slug,
				RepoFingerprint: validReviewFP,
				Status:          skillcandidate.StatusPendingReview,
				SkillMD:         md,
				ContentHash:     "sha:1",
			}, nil
		},
	}
	as := &fakeAuditStore{}
	svc, projectRoot := newReviewService(t, cs, as)
	res, err := svc.ApproveCandidate(skillTestContext(projectRoot), ApproveCandidateParams{
		CandidateID:           11,
		ApprovedBy:            "alice",
		CallerRepoFingerprint: validReviewFP,
		Reason:                "lgtm",
	})
	if err != nil {
		t.Fatalf("ApproveCandidate error = %v", err)
	}
	if !res.OK {
		t.Fatalf("result OK = false, want true: %+v", res)
	}
	wantPath := filepath.Join(projectRoot, ".agent", "skills", slug, skillMainFile)
	assertApprovedSkillFile(t, res.SkillPath, wantPath, md)
	assertApproveStoreCalls(t, cs)
	assertApproveAuditRow(t, as, slug)
}

func assertApprovedSkillFile(t *testing.T, gotPath, wantPath, wantBody string) {
	t.Helper()
	if gotPath != wantPath {
		t.Fatalf("skill path = %q, want %q", gotPath, wantPath)
	}
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", wantPath, err)
	}
	if string(body) != wantBody {
		t.Fatalf("on-disk content = %q, want %q", body, wantBody)
	}
}

func assertApproveStoreCalls(t *testing.T, cs *fakeCandidateStore) {
	t.Helper()
	if len(cs.approveCalls) != 1 || cs.approveCalls[0].ApprovedBy != "alice" || cs.approveCalls[0].Reason != "lgtm" {
		t.Fatalf("approve calls = %+v", cs.approveCalls)
	}
	if len(cs.promoteCalls) != 1 || cs.promoteCalls[0] != 11 {
		t.Fatalf("promote calls = %v, want [11]", cs.promoteCalls)
	}
}

func assertApproveAuditRow(t *testing.T, as *fakeAuditStore, slug string) {
	t.Helper()
	if len(as.inserts) != 1 || as.inserts[0].Action != "approve_succeeded" {
		t.Fatalf("audit inserts = %+v, want one approve_succeeded row", as.inserts)
	}
	// Audit row carries the structured payload (slug + content_hash + fp + approver).
	var extra map[string]any
	if err := json.Unmarshal(as.inserts[0].Extra, &extra); err != nil {
		t.Fatalf("audit extra json: %v", err)
	}
	if extra["slug"] != slug || extra["content_hash"] != "sha:1" || extra["repo_fingerprint"] != validReviewFP {
		t.Fatalf("audit extra = %+v", extra)
	}
	if extra["approved_by"] != "alice" {
		t.Fatalf("audit extra missing approved_by: %+v", extra)
	}
}

func TestReview_CreateSkillFailureKeepsApproved(t *testing.T) {
	t.Parallel()
	cs := &fakeCandidateStore{
		getByID: func(id int64) (skillcandidate.Candidate, error) {
			return skillcandidate.Candidate{
				ID:              id,
				Scope:           skillcandidate.ScopeProject,
				Slug:            "Bad/Slug", // CreateSkill rejects via validateSkillName.
				RepoFingerprint: validReviewFP,
				Status:          skillcandidate.StatusPendingReview,
				SkillMD:         "# bad\n",
			}, nil
		},
	}
	as := &fakeAuditStore{}
	svc, projectRoot := newReviewService(t, cs, as)
	_, err := svc.ApproveCandidate(skillTestContext(projectRoot), ApproveCandidateParams{
		CandidateID:           9,
		ApprovedBy:            "alice",
		CallerRepoFingerprint: validReviewFP,
		Reason:                "try",
	})
	if err == nil {
		t.Fatal("expected CreateSkill failure to surface as error")
	}
	if !errors.Is(err, ErrInvalidSkillName) {
		t.Fatalf("err = %v, want ErrInvalidSkillName chain", err)
	}
	if len(cs.approveCalls) != 1 {
		t.Fatalf("Approve must run before CreateSkill failure: %d calls", len(cs.approveCalls))
	}
	if len(cs.promoteCalls) != 0 {
		t.Fatalf("MarkPromoted MUST NOT run after CreateSkill failure: %v", cs.promoteCalls)
	}
	if len(as.inserts) != 1 || as.inserts[0].Action != "approve_promote_failed" {
		t.Fatalf("audit inserts = %+v, want approve_promote_failed", as.inserts)
	}
}

func TestReview_RejectWritesAudit(t *testing.T) {
	t.Parallel()
	cs := &fakeCandidateStore{
		getByID: func(id int64) (skillcandidate.Candidate, error) {
			return skillcandidate.Candidate{
				ID:              id,
				Scope:           skillcandidate.ScopeProject,
				Slug:            "noisy",
				Status:          skillcandidate.StatusPendingReview,
				RepoFingerprint: validReviewFP,
			}, nil
		},
		reject: func(id int64, reason string) (skillcandidate.Candidate, error) {
			return skillcandidate.Candidate{
				ID:     id,
				Scope:  skillcandidate.ScopeProject,
				Slug:   "noisy",
				Status: skillcandidate.StatusRejected,
				Reason: reason,
			}, nil
		},
	}
	as := &fakeAuditStore{}
	svc, _ := newReviewService(t, cs, as)
	if err := svc.RejectCandidate(context.Background(), RejectCandidateParams{CandidateID: 4, Reason: "duplicate", CallerRepoFingerprint: validReviewFP}); err != nil {
		t.Fatalf("RejectCandidate error = %v", err)
	}
	if len(cs.rejectCalls) != 1 || cs.rejectCalls[0].Reason != "duplicate" {
		t.Fatalf("reject calls = %+v", cs.rejectCalls)
	}
	if len(as.inserts) != 1 || as.inserts[0].Action != "reject" {
		t.Fatalf("audit inserts = %+v, want one reject row", as.inserts)
	}
	if as.inserts[0].Detail != "duplicate" {
		t.Fatalf("audit detail = %q, want %q", as.inserts[0].Detail, "duplicate")
	}
}

func TestReview_ListPendingExcludesRedactedSample(t *testing.T) {
	t.Parallel()
	cs := &fakeCandidateStore{
		listFn: func(limit, offset int32) ([]skillcandidate.Candidate, error) {
			return []skillcandidate.Candidate{{
				ID:              42,
				Scope:           skillcandidate.ScopeProject,
				Slug:            "demo",
				ContentHash:     "sha:demo",
				RepoFingerprint: validReviewFP,
				Status:          skillcandidate.StatusPendingReview,
				RedactedSample:  "should-not-leave-list",
				CreatedAt:       time.Unix(1700000000, 0).UTC(),
			}}, nil
		},
	}
	svc, _ := newReviewService(t, cs, nil)
	rows, err := svc.ListPendingCandidates(context.Background(), validReviewFP, 10, 0)
	if err != nil {
		t.Fatalf("ListPendingCandidates error = %v", err)
	}
	if len(rows) != 1 || rows[0].Slug != "demo" {
		t.Fatalf("rows = %+v", rows)
	}
	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	if strings.Contains(string(body), "redacted_sample") || strings.Contains(string(body), "should-not-leave-list") {
		t.Fatalf("list response leaked RedactedSample: %s", body)
	}
}

func TestReview_LookupApprovalScopedByFingerprint(t *testing.T) {
	t.Parallel()
	var captured []struct{ Scope, Slug, Hash, FP string }
	cs := &fakeCandidateStore{
		lookupFn: func(scope, slug, hash, fp string) (*skillcandidate.Candidate, error) {
			captured = append(captured, struct{ Scope, Slug, Hash, FP string }{scope, slug, hash, fp})
			return nil, nil
		},
	}
	svc, _ := newReviewService(t, cs, nil)
	if _, err := svc.LookupApproval(context.Background(), "project", "demo", "sha:abc", "fp:repo-A"); err != nil {
		t.Fatalf("LookupApproval A: %v", err)
	}
	if _, err := svc.LookupApproval(context.Background(), "project", "demo", "sha:abc", ""); err != nil {
		t.Fatalf("LookupApproval B: %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("captured calls = %d, want 2", len(captured))
	}
	if captured[0].FP != "fp:repo-A" {
		t.Fatalf("first call fingerprint = %q, want fp:repo-A", captured[0].FP)
	}
	if captured[1].FP != "" {
		t.Fatalf("second call must keep empty fingerprint literal, got %q", captured[1].FP)
	}
}

func TestReview_SignedSkillStillRequiresApproval(t *testing.T) {
	t.Parallel()
	const slug = "signed-skill"
	const md = "---\nname: signed-skill\ntrust: signed\n---\n# signed body\n"
	cs := &fakeCandidateStore{
		getByID: func(id int64) (skillcandidate.Candidate, error) {
			return skillcandidate.Candidate{
				ID:              id,
				Scope:           skillcandidate.ScopeProject,
				Slug:            slug,
				RepoFingerprint: validReviewFP,
				Status:          skillcandidate.StatusPendingReview,
				SkillMD:         md,
				ContentHash:     "sha:signed",
			}, nil
		},
	}
	as := &fakeAuditStore{}
	svc, projectRoot := newReviewService(t, cs, as)
	res, err := svc.ApproveCandidate(skillTestContext(projectRoot), ApproveCandidateParams{
		CandidateID:           21,
		ApprovedBy:            "alice",
		CallerRepoFingerprint: validReviewFP,
	})
	if err != nil {
		t.Fatalf("ApproveCandidate signed: %v", err)
	}
	if !res.OK {
		t.Fatalf("signed approval should still complete OK")
	}
	// The full pipeline must run: Approve, CreateSkill (on-disk), MarkPromoted, audit.
	if len(cs.approveCalls) != 1 {
		t.Fatalf("Approve calls = %d, want 1 (signed must NOT short-circuit)", len(cs.approveCalls))
	}
	if len(cs.promoteCalls) != 1 {
		t.Fatalf("MarkPromoted calls = %d, want 1", len(cs.promoteCalls))
	}
	if len(as.inserts) != 1 || as.inserts[0].Action != "approve_succeeded" {
		t.Fatalf("audit inserts = %+v, want approve_succeeded", as.inserts)
	}
	wantPath := filepath.Join(projectRoot, ".agent", "skills", slug, skillMainFile)
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("ReadFile signed: %v", err)
	}
	if !strings.Contains(string(body), "trust: signed") {
		t.Fatalf("on-disk content missing signed front-matter: %q", body)
	}
}
