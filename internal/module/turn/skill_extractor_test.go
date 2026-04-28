package turn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/skillcandidate"
)

// fakeDreamExecutor exposes both the prompt it received and the call count
// so tests can assert "did the extractor leak a raw secret into the
// prompt" and "was the executor invoked at all".
type fakeDreamExecutor struct {
	out        string
	err        error
	lastPrompt atomic.Value // string
	calls      atomic.Int32
}

func (f *fakeDreamExecutor) ExecuteDream(_ context.Context, prompt string) (string, error) {
	f.calls.Add(1)
	f.lastPrompt.Store(prompt)
	return f.out, f.err
}

// fakeStore records every Insert and lets a test override its return.
type fakeStore struct {
	inserts        []skillcandidate.InsertParams
	supersedeCalls []struct {
		Scope, Slug, RepoFingerprint string
		KeepID                       int64
	}
	insertErr error
}

func (s *fakeStore) Insert(_ context.Context, p skillcandidate.InsertParams) (skillcandidate.Candidate, error) {
	if s.insertErr != nil {
		return skillcandidate.Candidate{}, s.insertErr
	}
	s.inserts = append(s.inserts, p)
	return skillcandidate.Candidate{
		ID:              int64(len(s.inserts)),
		Scope:           p.Scope,
		Slug:            p.Slug,
		ContentHash:     p.ContentHash,
		RepoFingerprint: p.RepoFingerprint,
		SkillMD:         p.SkillMD,
		RedactedSample:  p.RedactedSample,
		Status:          skillcandidate.StatusPendingReview,
	}, nil
}
func (s *fakeStore) GetByID(context.Context, int64) (skillcandidate.Candidate, error) {
	return skillcandidate.Candidate{}, errors.New("not used")
}
func (s *fakeStore) ListPending(context.Context, string, int32, int32) ([]skillcandidate.Candidate, error) {
	return nil, nil
}
func (s *fakeStore) MarkSuperseded(_ context.Context, scope, slug, repoFingerprint string, keepID int64) (int64, error) {
	s.supersedeCalls = append(s.supersedeCalls, struct {
		Scope, Slug, RepoFingerprint string
		KeepID                       int64
	}{scope, slug, repoFingerprint, keepID})
	return 0, nil
}
func (s *fakeStore) Approve(context.Context, int64, string, string, time.Time) (skillcandidate.Candidate, error) {
	return skillcandidate.Candidate{}, errors.New("not used")
}
func (s *fakeStore) Reject(context.Context, int64, string) (skillcandidate.Candidate, error) {
	return skillcandidate.Candidate{}, errors.New("not used")
}
func (s *fakeStore) MarkPromoted(context.Context, int64) (skillcandidate.Candidate, error) {
	return skillcandidate.Candidate{}, errors.New("not used")
}
func (s *fakeStore) LookupApproval(context.Context, string, string, string, string) (*skillcandidate.Candidate, error) {
	return nil, nil
}

// Compile-time assertion that fakeStore satisfies the skillcandidate.Store
// contract; if Step 1 ever extends the interface this test goes red first.
var _ skillcandidate.Store = (*fakeStore)(nil)

func eligibleTrajectory() Trajectory {
	success := true
	return Trajectory{
		TurnID:        "t-eligible",
		ThreadID:      "thr-1",
		AgentID:       "agent-1",
		Cwd:           "/tmp/repo",
		TerminalState: "completed",
		Success:       &success,
		ToolCalls: []ToolCall{
			{CallID: "c1", Name: "read_file", Args: "/etc/profile", Failed: false},
			{CallID: "c2", Name: "write_file", Args: "x.txt", Failed: false},
		},
	}
}

func TestExtractor_GoldenRedactsSecrets(t *testing.T) {
	leakyTrajectory := eligibleTrajectory()
	leakyTrajectory.ToolCalls[0].Args = "Authorization: Bearer abc.def-secret_value"
	leakyTrajectory.ToolCalls[1].Result = "OPENAI_API_KEY=sk-1234567890abcdef\ntoken=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.signature"

	dream := &fakeDreamExecutor{
		// Simulate an LLM that reflects trajectory secrets back into SKILL.md.
		out: "---\nname: leaky-skill\n---\nUse Bearer abc.def-secret_value to call API.\nSet OPENAI_API_KEY=sk-1234567890abcdef.\nToken eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.signature.",
	}
	store := &fakeStore{}
	e := NewDefaultExtractor(dream, store, NewDefaultRedactor(), NewDefaultEvaluator(), nil)

	extracted, err := e.Extract(context.Background(), leakyTrajectory)
	if err != nil {
		t.Fatalf("Extract returned err: %v", err)
	}
	if extracted == nil {
		t.Fatal("expected ExtractedSkill, got nil")
	}
	forbidden := []string{"abc.def-secret_value", "sk-1234567890abcdef", "eyJhbGciOiJIUzI1NiJ9", "eyJzdWIiOiJ4In0"}
	for _, f := range forbidden {
		if strings.Contains(extracted.SKILLMd, f) {
			t.Fatalf("SKILLMd still contains %q: %s", f, extracted.SKILLMd)
		}
		if strings.Contains(extracted.Sample, f) {
			t.Fatalf("Sample still contains %q: %s", f, extracted.Sample)
		}
	}
	if len(store.inserts) != 1 {
		t.Fatalf("expected 1 insert, got %d", len(store.inserts))
	}
	if store.inserts[0].Scope != skillcandidate.ScopeProject {
		t.Fatalf("scope want project, got %q", store.inserts[0].Scope)
	}
	if e.Metrics.Promoted != 1 {
		t.Fatalf("expected 1 Promoted, got %d", e.Metrics.Promoted)
	}
	if len(store.supersedeCalls) != 1 {
		t.Fatalf("expected 1 supersede call, got %d", len(store.supersedeCalls))
	}
	if store.supersedeCalls[0].Scope != extracted.Scope || store.supersedeCalls[0].Slug != extracted.Slug || store.supersedeCalls[0].RepoFingerprint != extracted.RepoFingerprint || store.supersedeCalls[0].KeepID != 1 {
		t.Fatalf("supersede call = %+v, extracted = %+v", store.supersedeCalls[0], extracted)
	}
}

func TestExtractor_PromptDoesNotLeakRawSecrets(t *testing.T) {
	leaky := eligibleTrajectory()
	leaky.ToolCalls[0].Args = "Bearer abc.def-secret_value"

	dream := &fakeDreamExecutor{out: "## Skill\nclean output"}
	e := NewDefaultExtractor(dream, &fakeStore{}, NewDefaultRedactor(), NewDefaultEvaluator(), nil)

	_, err := e.Extract(context.Background(), leaky)
	if err != nil {
		t.Fatal(err)
	}
	prompt, _ := dream.lastPrompt.Load().(string)
	if strings.Contains(prompt, "abc.def-secret_value") {
		t.Fatalf("prompt leaked raw secret: %s", prompt)
	}
	if !strings.Contains(prompt, "[REDACTED:") {
		t.Fatalf("prompt missing redaction marker: %s", prompt)
	}
}

func TestExtractor_DreamExecutorMissingSkips(t *testing.T) {
	e := NewDefaultExtractor(nil, &fakeStore{}, NewDefaultRedactor(), NewDefaultEvaluator(), nil)
	extracted, err := e.Extract(context.Background(), eligibleTrajectory())
	if err != nil {
		t.Fatalf("nil dream should not error, got %v", err)
	}
	if extracted != nil {
		t.Fatal("expected nil ExtractedSkill when dream is missing")
	}
	if e.Metrics.DreamNotConfigured != 1 {
		t.Fatalf("expected DreamNotConfigured=1, got %d", e.Metrics.DreamNotConfigured)
	}
}

func TestExtractor_DreamExecutorReturnsNotConfiguredErr(t *testing.T) {
	dream := &fakeDreamExecutor{err: contract.ErrDreamExecutorNotConfigured}
	e := NewDefaultExtractor(dream, &fakeStore{}, NewDefaultRedactor(), NewDefaultEvaluator(), nil)
	extracted, err := e.Extract(context.Background(), eligibleTrajectory())
	if err != nil {
		t.Fatalf("not-configured sentinel should be skip, got %v", err)
	}
	if extracted != nil {
		t.Fatal("expected nil ExtractedSkill on not-configured sentinel")
	}
	if e.Metrics.DreamNotConfigured != 1 {
		t.Fatalf("DreamNotConfigured = %d", e.Metrics.DreamNotConfigured)
	}
}

// stubFailingRedactor models a redactor that errors on every call.
type stubFailingRedactor struct{}

func (stubFailingRedactor) Redact(string) (string, []string, error) {
	return "", nil, errors.New("redactor exploded")
}

func TestExtractor_RedactionFailureDropsCandidate(t *testing.T) {
	dream := &fakeDreamExecutor{out: "anything"}
	store := &fakeStore{}
	e := NewDefaultExtractor(dream, store, stubFailingRedactor{}, NewDefaultEvaluator(), nil)
	_, err := e.Extract(context.Background(), eligibleTrajectory())
	if err == nil {
		t.Fatal("expected redaction error, got nil")
	}
	if len(store.inserts) != 0 {
		t.Fatalf("expected 0 inserts, got %d", len(store.inserts))
	}
	if e.Metrics.RedactionFailed == 0 {
		t.Fatal("RedactionFailed counter not bumped")
	}
}

// stubResidualRedactor reports a hit on every call so the residual scan
// always fires - exercising the second-pass drop branch.
type stubResidualRedactor struct{}

func (stubResidualRedactor) Redact(input string) (string, []string, error) {
	out := strings.ReplaceAll(input, "first-secret", "[REDACTED:first]")
	return out, []string{"first"}, nil
}

func TestExtractor_ResidualSecretDropsCandidate(t *testing.T) {
	dream := &fakeDreamExecutor{out: "leaks first-secret and first-secret again"}
	store := &fakeStore{}
	e := NewDefaultExtractor(dream, store, stubResidualRedactor{}, NewDefaultEvaluator(), nil)
	_, err := e.Extract(context.Background(), eligibleTrajectory())
	if err == nil {
		t.Fatal("expected residual error")
	}
	if !strings.Contains(err.Error(), "residual secret") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.inserts) != 0 {
		t.Fatalf("expected no insert, got %d", len(store.inserts))
	}
	if e.Metrics.PromotedDropped == 0 {
		t.Fatal("PromotedDropped counter not bumped")
	}
}

func TestExtractor_HappyPathInsertsCandidate(t *testing.T) {
	dream := &fakeDreamExecutor{out: "## clean skill body"}
	store := &fakeStore{}
	e := NewDefaultExtractor(dream, store, NewDefaultRedactor(), NewDefaultEvaluator(), nil)
	extracted, err := e.Extract(context.Background(), eligibleTrajectory())
	if err != nil || extracted == nil {
		t.Fatalf("expected success, got err=%v extracted=%v", err, extracted)
	}
	if len(store.inserts) != 1 {
		t.Fatalf("expected 1 insert, got %d", len(store.inserts))
	}
	p := store.inserts[0]
	if p.Scope != skillcandidate.ScopeProject {
		t.Fatalf("scope=%q", p.Scope)
	}
	if p.RepoFingerprint == "" {
		t.Fatal("repo_fingerprint should be non-empty when cwd is set")
	}
	if p.ContentHash == "" {
		t.Fatal("content_hash empty")
	}
	if p.SkillMD == "" {
		t.Fatal("skill_md empty")
	}
}

// uniqueViolationStore returns an ErrConflict-wrapped StoreError so the
// extractor's dedup-hit branch fires.
type uniqueViolationStore struct{ fakeStore }

func (s *uniqueViolationStore) Insert(_ context.Context, _ skillcandidate.InsertParams) (skillcandidate.Candidate, error) {
	return skillcandidate.Candidate{}, &platformdb.StoreError{
		Operation: "insert",
		Entity:    "skill_candidate",
		Kind:      platformdb.ErrConflict,
		Err:       platformdb.ErrConflict,
	}
}

func TestExtractor_DedupHitDoesNotPropagateError(t *testing.T) {
	dream := &fakeDreamExecutor{out: "## clean skill"}
	store := &uniqueViolationStore{}
	e := NewDefaultExtractor(dream, store, NewDefaultRedactor(), NewDefaultEvaluator(), nil)
	extracted, err := e.Extract(context.Background(), eligibleTrajectory())
	if err != nil {
		t.Fatalf("dedup hit should not propagate error, got %v", err)
	}
	if extracted == nil {
		t.Fatal("dedup hit should still return ExtractedSkill")
	}
	if e.Metrics.DedupHit != 1 {
		t.Fatalf("DedupHit=%d", e.Metrics.DedupHit)
	}
	if e.Metrics.Promoted != 0 {
		t.Fatal("Promoted should not bump on dedup hit")
	}
}

func TestExtractor_IneligibleTrajectoryDoesNotCallDream(t *testing.T) {
	dream := &fakeDreamExecutor{out: "should not be called"}
	e := NewDefaultExtractor(dream, &fakeStore{}, NewDefaultRedactor(), NewDefaultEvaluator(), nil)
	interrupted := eligibleTrajectory()
	interrupted.TerminalState = "interrupted"
	_, err := e.Extract(context.Background(), interrupted)
	if err != nil {
		t.Fatal(err)
	}
	if dream.calls.Load() != 0 {
		t.Fatalf("Dream should not be called for ineligible, calls=%d", dream.calls.Load())
	}
}

func TestExtractorRunner_StopsOnContextDone(t *testing.T) {
	runner := NewExtractorRunner(nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := runner.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ctx err, got %v", err)
	}
}

func TestIsUniqueViolation(t *testing.T) {
	if !isUniqueViolation(&platformdb.StoreError{Kind: platformdb.ErrConflict, Err: platformdb.ErrConflict}) {
		t.Fatal("should recognize StoreError with ErrConflict")
	}
	if !isUniqueViolation(fmt.Errorf("pq: duplicate key value violates unique constraint")) {
		t.Fatal("should recognize unique constraint message")
	}
	if !isUniqueViolation(fmt.Errorf("sqlstate=23505")) {
		t.Fatal("should recognize 23505 sqlstate")
	}
	if isUniqueViolation(errors.New("connection refused")) {
		t.Fatal("should not match unrelated error")
	}
	if isUniqueViolation(nil) {
		t.Fatal("nil should be false")
	}
}
