package turn

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
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
	e := NewDefaultExtractor(dream, struct{}{}, NewDefaultRedactor(), NewDefaultEvaluator(), nil)

	extracted, err := e.Extract(context.Background(), leakyTrajectory)
	if err != nil {
		t.Fatalf("Extract returned err: %v", err)
	}
	if extracted == nil {
		t.Fatal("expected ExtractedSkill, got nil")
	}
	forbidden := []string{"abc.def-secret_value", "sk-1234567890abcdef", "eyJhbGciOiJIUzI1NiJ9", "eyJzdWIiOiJ4In0"}
	assertExtractedSkillRedacted(t, extracted, forbidden)
	assertNoLegacyCandidateMetrics(t, e)
}

func assertExtractedSkillRedacted(t *testing.T, extracted *ExtractedSkill, forbidden []string) {
	t.Helper()
	for _, f := range forbidden {
		if strings.Contains(extracted.SKILLMd, f) {
			t.Fatalf("SKILLMd still contains %q: %s", f, extracted.SKILLMd)
		}
		if strings.Contains(extracted.Sample, f) {
			t.Fatalf("Sample still contains %q: %s", f, extracted.Sample)
		}
	}
}

func assertNoLegacyCandidateMetrics(t *testing.T, e *DefaultExtractor) {
	t.Helper()
	if e.Metrics.Promoted != 0 {
		t.Fatalf("expected no legacy promoted metric, got %d", e.Metrics.Promoted)
	}
	if e.Metrics.InsertFailed != 0 {
		t.Fatalf("expected no legacy insert-failed metric, got %d", e.Metrics.InsertFailed)
	}
	if e.Metrics.DedupHit != 0 {
		t.Fatalf("expected no legacy dedup-hit metric, got %d", e.Metrics.DedupHit)
	}
}

func TestExtractor_PromptDoesNotLeakRawSecrets(t *testing.T) {
	leaky := eligibleTrajectory()
	leaky.ToolCalls[0].Args = "Bearer abc.def-secret_value"

	dream := &fakeDreamExecutor{out: "## Skill\nclean output"}
	e := NewDefaultExtractor(dream, struct{}{}, NewDefaultRedactor(), NewDefaultEvaluator(), nil)

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

func TestExtractor_DreamExecutorMissingFailsFast(t *testing.T) {
	e := NewDefaultExtractor(nil, struct{}{}, NewDefaultRedactor(), NewDefaultEvaluator(), nil)
	extracted, err := e.Extract(context.Background(), eligibleTrajectory())
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("nil dream error = %v, want not configured", err)
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
	e := NewDefaultExtractor(dream, struct{}{}, NewDefaultRedactor(), NewDefaultEvaluator(), nil)
	extracted, err := e.Extract(context.Background(), eligibleTrajectory())
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("not-configured sentinel error = %v, want not configured", err)
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

func TestExtractor_RedactionFailureDropsExtraction(t *testing.T) {
	dream := &fakeDreamExecutor{out: "anything"}
	e := NewDefaultExtractor(dream, struct{}{}, stubFailingRedactor{}, NewDefaultEvaluator(), nil)
	_, err := e.Extract(context.Background(), eligibleTrajectory())
	if err == nil {
		t.Fatal("expected redaction error, got nil")
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

func TestExtractor_ResidualSecretDropsExtraction(t *testing.T) {
	dream := &fakeDreamExecutor{out: "leaks first-secret and first-secret again"}
	e := NewDefaultExtractor(dream, struct{}{}, stubResidualRedactor{}, NewDefaultEvaluator(), nil)
	_, err := e.Extract(context.Background(), eligibleTrajectory())
	if err == nil {
		t.Fatal("expected residual error")
	}
	if !strings.Contains(err.Error(), "residual secret") {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Metrics.PromotedDropped == 0 {
		t.Fatal("PromotedDropped counter not bumped")
	}
}

func TestExtractor_HappyPathReturnsExtractedSkillWithoutCandidateWrite(t *testing.T) {
	dream := &fakeDreamExecutor{out: "## clean skill body"}
	e := NewDefaultExtractor(dream, struct{}{}, NewDefaultRedactor(), NewDefaultEvaluator(), nil)
	extracted, err := e.Extract(context.Background(), eligibleTrajectory())
	if err != nil || extracted == nil {
		t.Fatalf("expected success, got err=%v extracted=%v", err, extracted)
	}
	if extracted.Scope != "project" {
		t.Fatalf("scope=%q", extracted.Scope)
	}
	if extracted.RepoFingerprint == "" {
		t.Fatal("repo_fingerprint should be non-empty when cwd is set")
	}
	if extracted.ContentHash == "" {
		t.Fatal("content_hash empty")
	}
	if extracted.SKILLMd == "" {
		t.Fatal("skill_md empty")
	}
	assertNoLegacyCandidateMetrics(t, e)
}

func TestExtractor_LegacyStoreArgumentDoesNotAffectExtraction(t *testing.T) {
	dream := &fakeDreamExecutor{out: "## clean skill"}
	e := NewDefaultExtractor(dream, struct{ legacy string }{legacy: "ignored"}, NewDefaultRedactor(), NewDefaultEvaluator(), nil)
	extracted, err := e.Extract(context.Background(), eligibleTrajectory())
	if err != nil {
		t.Fatalf("legacy store argument should be ignored, got %v", err)
	}
	if extracted == nil {
		t.Fatal("expected ExtractedSkill")
	}
	assertNoLegacyCandidateMetrics(t, e)
}

func TestExtractor_IneligibleTrajectoryDoesNotCallDream(t *testing.T) {
	dream := &fakeDreamExecutor{out: "should not be called"}
	e := NewDefaultExtractor(dream, struct{}{}, NewDefaultRedactor(), NewDefaultEvaluator(), nil)
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

func TestExtractorRunner_FailsFastWhenDefaultExtractorMissingDream(t *testing.T) {
	t.Parallel()

	runner := NewExtractorRunner(NewTrajectoryCollector(nil, nil), NewDefaultExtractor(nil, struct{}{}, nil, nil, nil), nil)
	err := runner.Run(context.Background())
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("Run() error = %v, want missing dream executor", err)
	}
}
