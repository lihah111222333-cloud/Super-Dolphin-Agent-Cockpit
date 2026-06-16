package turn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

const (
	extractTimeout      = 90 * time.Second
	redactedSampleLimit = 1024
	runnerTickInterval  = 5 * time.Second
)

// ExtractedSkill is the LLM-distilled, second-pass-redacted SKILL.md plus
// legacy bookkeeping. V1 no longer writes this into candidate storage.
type ExtractedSkill struct {
	Slug            string
	SKILLMd         string
	ContentHash     string
	Sample          string
	Scope           string
	RepoFingerprint string
}

// Extractor distills an eligible Trajectory into a redacted skill-shaped
// artifact. Implementations MUST be called from a runner worker
// (never from a bus callback) so the bus dispatcher is not blocked on the
// LLM round-trip.
type Extractor interface {
	Extract(ctx context.Context, t Trajectory) (*ExtractedSkill, error)
}

type configuredExtractor interface {
	ensureConfigured() error
}

// ExtractorMetrics is the observable counter set used by tests to assert
// the extractor took the expected path. Production deployments can attach
// these to a prometheus collector by reading the atomic ints; the type
// is intentionally tiny so we do not couple the extractor to a metric SDK.
type ExtractorMetrics struct {
	DreamNotConfigured int64
	DreamFailed        int64
	RedactionFailed    int64
	PromotedDropped    int64 // second-pass scan still hit a pattern
	DedupHit           int64
	InsertFailed       int64
	Promoted           int64 // candidate row written
}

func (m *ExtractorMetrics) incDreamNotConfigured() { atomic.AddInt64(&m.DreamNotConfigured, 1) }
func (m *ExtractorMetrics) incDreamFailed()        { atomic.AddInt64(&m.DreamFailed, 1) }
func (m *ExtractorMetrics) incRedactionFailed()    { atomic.AddInt64(&m.RedactionFailed, 1) }
func (m *ExtractorMetrics) incResidualSecret()     { atomic.AddInt64(&m.PromotedDropped, 1) }

// DefaultExtractor distills a Trajectory without writing to the removed
// legacy candidate backend. dream is required so missing distillation
// infrastructure fails before trajectories are silently dropped.
type DefaultExtractor struct {
	dream     contract.DreamExecutor // optional: nil skips
	redactor  Redactor
	evaluator Evaluator
	logger    *pkglogger.Logger
	Metrics   *ExtractorMetrics
	nowFn     func() time.Time
}

// NewDefaultExtractor constructs the extractor. dream is fx optional and may
// be nil. The second parameter is ignored to preserve stale call sites while
// the live old candidate writer remains disabled. redactor / evaluator / logger fall back to
// package defaults when nil so tests and partial wiring stay simple.
// NewDefaultExtractor 创建defaultextractor。
func NewDefaultExtractor(
	dream contract.DreamExecutor,
	_ any,
	redactor Redactor,
	evaluator Evaluator,
	logger *pkglogger.Logger,
) *DefaultExtractor {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if redactor == nil {
		redactor = NewDefaultRedactor()
	}
	if evaluator == nil {
		evaluator = NewDefaultEvaluator()
	}
	return &DefaultExtractor{
		dream:     dream,
		redactor:  redactor,
		evaluator: evaluator,
		logger:    logger,
		Metrics:   &ExtractorMetrics{},
		nowFn:     time.Now,
	}
}

// Extract runs the full distillation pipeline. Any per-step failure is
// logged + counted, then surfaced as an error so the caller (the runner)
// can decide whether to drop or retry; the runner does NOT stop on a
// single trajectory failure.
//
// Pipeline: evaluate -> buildPrompt(redact-first) -> ExecuteDream ->
// Redact -> residual scan -> content_hash -> repo_fingerprint ->
// return the redacted artifact. The removed old candidate backend is not
// called.
// Extract 提取turn。
func (e *DefaultExtractor) Extract(ctx context.Context, t Trajectory) (*ExtractedSkill, error) {
	if e == nil {
		return nil, errors.New("extractor: nil receiver")
	}
	if err := e.ensureConfigured(); err != nil {
		return nil, err
	}
	if !e.readyToExtract(t) {
		return nil, nil
	}
	prompt, err := e.redactedTrajectoryPrompt(t)
	if err != nil {
		return nil, err
	}
	rawSkillMd, err := e.executeDream(ctx, t, prompt)
	if err != nil {
		if errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
			return nil, err
		}
		return nil, err
	}
	cleaned, err := e.redactDreamOutput(t, rawSkillMd)
	if err != nil {
		return nil, err
	}
	extracted := newExtractedSkill(t, cleaned)
	_ = ctx
	return extracted, nil
}

func (e *DefaultExtractor) ensureConfigured() error {
	if e.dream == nil {
		e.Metrics.incDreamNotConfigured()
		return contract.ErrDreamExecutorNotConfigured
	}
	return nil
}

func (e *DefaultExtractor) readyToExtract(t Trajectory) bool {
	verdict := e.evaluator.Evaluate(t)
	if !verdict.Eligible {
		e.logger.Debug("extractor: trajectory ineligible", "turn_id", t.TurnID, "reason", verdict.Reason)
		return false
	}
	return true
}

func (e *DefaultExtractor) redactedTrajectoryPrompt(t Trajectory) (string, error) {
	prompt, err := e.buildRedactedPrompt(t)
	if err != nil {
		e.Metrics.incRedactionFailed()
		e.logger.Error("extractor: prompt redaction failed", "turn_id", t.TurnID, "error", err)
		return "", err
	}
	return prompt, nil
}

func (e *DefaultExtractor) executeDream(ctx context.Context, t Trajectory, prompt string) (string, error) {
	callCtx, cancel := kernel.WithTimeout(ctx, extractTimeout)
	defer cancel()
	rawSkillMd, err := e.dream.ExecuteDream(callCtx, prompt)
	if err != nil {
		e.recordDreamError(t, err)
		return "", err
	}
	return rawSkillMd, nil
}

func (e *DefaultExtractor) recordDreamError(t Trajectory, err error) {
	if errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		e.Metrics.incDreamNotConfigured()
		e.logger.Info("extractor: dream executor not configured at call time", "turn_id", t.TurnID)
		return
	}
	e.Metrics.incDreamFailed()
	e.logger.Warn("extractor: dream execute failed", "turn_id", t.TurnID, "error", err)
}

func (e *DefaultExtractor) redactDreamOutput(t Trajectory, rawSkillMd string) (string, error) {
	cleaned, _, err := e.redactor.Redact(rawSkillMd)
	if err != nil {
		e.Metrics.incRedactionFailed()
		e.logger.Error("extractor: post-llm redaction errored, dropping", "turn_id", t.TurnID, "error", err)
		return "", err
	}
	if err := e.rejectResidualSecrets(t, cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

func (e *DefaultExtractor) rejectResidualSecrets(t Trajectory, cleaned string) error {
	_, residualHits, err := e.redactor.Redact(cleaned)
	if err != nil {
		e.Metrics.incRedactionFailed()
		return err
	}
	if len(residualHits) == 0 {
		return nil
	}
	e.Metrics.incResidualSecret()
	e.logger.Error("extractor: residual secret after first pass, dropping",
		"turn_id", t.TurnID, "hits", residualHits)
	return fmt.Errorf("extractor: residual secret hits %v", residualHits)
}

func newExtractedSkill(t Trajectory, cleaned string) *ExtractedSkill {
	sum := sha256.Sum256([]byte(cleaned))
	contentHash := hex.EncodeToString(sum[:])
	return &ExtractedSkill{
		Slug:            slugFromTrajectory(t),
		SKILLMd:         cleaned,
		ContentHash:     contentHash,
		Sample:          truncateRedactedSample(cleaned),
		Scope:           "project",
		RepoFingerprint: RepoFingerprint(t.Cwd),
	}
}

func truncateRedactedSample(sample string) string {
	if len(sample) > redactedSampleLimit {
		return sample[:redactedSampleLimit]
	}
	return sample
}

// buildRedactedPrompt serialises the trajectory tool calls into a prompt
// and runs the redactor over the result before handing it to the LLM.
// The exact prompt template is intentionally simple at this step; the
// security boundary is what we are defending here.
// buildRedactedPrompt 构建redactedprompt。
func (e *DefaultExtractor) buildRedactedPrompt(t Trajectory) (string, error) {
	var b strings.Builder
	b.WriteString("You are summarizing a successful agent turn into a reusable Skill.\n")
	b.WriteString("Summarize the trajectory below into a SKILL.md (frontmatter + body).\n\n")
	b.WriteString("---TRAJECTORY---\n")
	fmt.Fprintf(&b, "thread_id: %s\nagent_id: %s\nturn_id: %s\nterminal: %s\n",
		t.ThreadID, t.AgentID, t.TurnID, t.TerminalState)
	if len(t.SkillsSelected) > 0 {
		fmt.Fprintf(&b, "skills_selected: %s\n", strings.Join(t.SkillsSelected, ","))
	}
	b.WriteString("\ntool_calls:\n")
	for i, tc := range t.ToolCalls {
		fmt.Fprintf(&b, "  [%d] %s\n", i+1, tc.Name)
		if tc.Args != "" {
			fmt.Fprintf(&b, "      args: %s\n", tc.Args)
		}
		if tc.Result != "" {
			fmt.Fprintf(&b, "      result: %s\n", tc.Result)
		}
		if tc.Failed {
			fmt.Fprintf(&b, "      failed: true error=%q\n", tc.Error)
		}
	}
	raw := b.String()
	redacted, _, err := e.redactor.Redact(raw)
	if err != nil {
		return "", err
	}
	return redacted, nil
}

// slugFromTrajectory is a placeholder that derives a stable slug from the
// turn id. It is good enough to give the (scope, slug, content_hash,
// repo_fingerprint) unique key a deterministic key; a later step can swap
// in the LLM's frontmatter `name` field once we trust it.
func slugFromTrajectory(t Trajectory) string {
	base := strings.TrimSpace(t.TurnID)
	if base == "" {
		base = strings.TrimSpace(t.LocalTurnID)
	}
	if base == "" {
		base = fmt.Sprintf("anon-%d", time.Now().UnixNano())
	}
	sanitized := slugSanitize.ReplaceAllString(base, "-")
	sanitized = strings.Trim(sanitized, "-")
	if sanitized == "" {
		sanitized = "trajectory"
	}
	if len(sanitized) > 64 {
		sanitized = sanitized[:64]
	}
	return strings.ToLower(sanitized)
}

var slugSanitize = regexp.MustCompile(`[^A-Za-z0-9]+`)

// Compile-time assertion.
var _ Extractor = (*DefaultExtractor)(nil)

// ExtractorRunner pumps drained trajectories from the collector into the
// extractor on a fixed tick. It is registered into the root
// `group:"runners"` aggregation so its lifecycle is tied to the run.Group
// supervisor (NOT to fx OnStart fire-and-forget goroutines). The runner
// exits cleanly when its parent ctx is cancelled.
type ExtractorRunner struct {
	collector *Collector
	extractor Extractor
	logger    *pkglogger.Logger
	interval  time.Duration
}

// NewExtractorRunner builds the runner. collector or extractor == nil is
// tolerated (Run blocks on ctx.Done with no work) so deployments that
// have not enabled P0b can still satisfy the runner group constraint.
// NewExtractorRunner 创建extractorrunner。
func NewExtractorRunner(collector *Collector, extractor Extractor, logger *pkglogger.Logger) *ExtractorRunner {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &ExtractorRunner{
		collector: collector,
		extractor: extractor,
		logger:    logger,
		interval:  runnerTickInterval,
	}
}

// Run implements platformrunner.Runner.
// Run 启动turn后台流程。
func (r *ExtractorRunner) Run(ctx context.Context) error {
	if r == nil || r.collector == nil || r.extractor == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	if validator, ok := r.extractor.(configuredExtractor); ok {
		if err := validator.ensureConfigured(); err != nil {
			return err
		}
	}
	tick := time.NewTicker(r.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			r.flushOnce(ctx)
		}
	}
}

func (r *ExtractorRunner) flushOnce(ctx context.Context) {
	drained := r.collector.Drain()
	for _, traj := range drained {
		if ctx.Err() != nil {
			return
		}
		// A panic from a single trajectory must not poison the worker -
		// recover here so the runner keeps draining the rest of the batch.
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					r.logger.Error("extractor: panic in extract", "turn_id", traj.TurnID, "panic", rec)
				}
			}()
			if _, err := r.extractor.Extract(ctx, traj); err != nil {
				r.logger.Error("extractor: extract failed", "turn_id", traj.TurnID, "error", err)
			}
		}()
	}
}
