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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	// skill 提炼的超时、样本和后台 runner tick 默认值。
	extractTimeout      = 90 * time.Second
	redactedSampleLimit = 1024
	runnerTickInterval  = 5 * time.Second
)

// ExtractedSkill 是 LLM 提炼并二次脱敏后的 SKILL.md 结果，当前只返回给调用方，不写旧候选存储。
type ExtractedSkill struct {
	Slug            string
	SKILLMd         string
	ContentHash     string
	Sample          string
	Scope           string
	RepoFingerprint string
}

// Extractor 将合格轨迹提炼为已脱敏的 skill 形态结果；必须由后台 runner 调用，不能阻塞 bus 回调。
type Extractor interface {
	Extract(ctx context.Context, t Trajectory) (*ExtractedSkill, error)
}

// configuredExtractor 允许 runner 在启动时提前发现缺失的 LLM 提炼依赖。
type configuredExtractor interface {
	ensureConfigured() error
}

// ExtractorMetrics 保存提炼路径的原子计数，避免 extractor 直接耦合具体指标 SDK。
type ExtractorMetrics struct {
	DreamNotConfigured int64
	DreamFailed        int64
	RedactionFailed    int64
	PromotedDropped    int64 // 二次扫描仍命中敏感规则而丢弃的结果数
	DedupHit           int64
	InsertFailed       int64
	Promoted           int64 // 成功产出并返回给调用方的提炼结果数
}

func (m *ExtractorMetrics) incDreamNotConfigured() { atomic.AddInt64(&m.DreamNotConfigured, 1) }
func (m *ExtractorMetrics) incDreamFailed()        { atomic.AddInt64(&m.DreamFailed, 1) }
func (m *ExtractorMetrics) incRedactionFailed()    { atomic.AddInt64(&m.RedactionFailed, 1) }
func (m *ExtractorMetrics) incResidualSecret()     { atomic.AddInt64(&m.PromotedDropped, 1) }
func (m *ExtractorMetrics) incDedupHit()           { atomic.AddInt64(&m.DedupHit, 1) }
func (m *ExtractorMetrics) incInsertFailed()       { atomic.AddInt64(&m.InsertFailed, 1) }
func (m *ExtractorMetrics) incPromoted()           { atomic.AddInt64(&m.Promoted, 1) }

// DefaultExtractor 执行轨迹提炼和脱敏流程；dream 缺失会 fail-fast，避免后台静默丢弃轨迹。
type DefaultExtractor struct {
	dream     contract.DreamExecutor // LLM 提炼执行器，缺失时启动前失败。
	redactor  Redactor
	evaluator Evaluator
	logger    *pkglogger.Logger
	Metrics   *ExtractorMetrics
	nowFn     func() time.Time
}

// NewDefaultExtractor 创建默认提炼器；dream 缺失会在运行前 fail-fast，旧 candidate writer 参数保留但不使用。
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

// Extract 执行 evaluate、prompt redaction、LLM 提炼、二次 redaction 和残留 secret 扫描。
// 任一步失败都会计数并返回错误；当前实现只返回结果，不在提炼器里写候选存储。
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

// ensureConfigured 确认 LLM 提炼执行器已注入，避免后台 runner 静默丢弃轨迹。
func (e *DefaultExtractor) ensureConfigured() error {
	if e.dream == nil {
		e.Metrics.incDreamNotConfigured()
		return contract.ErrDreamExecutorNotConfigured
	}
	return nil
}

// readyToExtract 调用 evaluator，并在不可提炼时记录稳定原因。
func (e *DefaultExtractor) readyToExtract(t Trajectory) bool {
	verdict := e.evaluator.Evaluate(t)
	if !verdict.Eligible {
		e.logger.Debug("extractor: trajectory ineligible", "turn_id", t.TurnID, "reason", verdict.Reason)
		return false
	}
	return true
}

// redactedTrajectoryPrompt 构造并脱敏发送给 LLM 的 prompt。
func (e *DefaultExtractor) redactedTrajectoryPrompt(t Trajectory) (string, error) {
	prompt, err := e.buildRedactedPrompt(t)
	if err != nil {
		e.Metrics.incRedactionFailed()
		e.logger.Error("extractor: prompt redaction failed", "turn_id", t.TurnID, "error", err)
		return "", err
	}
	return prompt, nil
}

// executeDream 使用固定超时调用 DreamExecutor，避免后台 runner 被单次 LLM 调用长期占住。
func (e *DefaultExtractor) executeDream(ctx context.Context, t Trajectory, prompt string) (string, error) {
	callCtx, cancel := ctxutil.WithTimeout(ctx, extractTimeout)
	defer cancel()
	rawSkillMd, err := e.dream.ExecuteDream(callCtx, prompt)
	if err != nil {
		e.recordDreamError(t, err)
		return "", err
	}
	return rawSkillMd, nil
}

// recordDreamError 按错误类型更新指标，并保留 turnID 便于追查。
func (e *DefaultExtractor) recordDreamError(t Trajectory, err error) {
	if errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		e.Metrics.incDreamNotConfigured()
		e.logger.Info("extractor: dream executor not configured at call time", "turn_id", t.TurnID)
		return
	}
	e.Metrics.incDreamFailed()
	e.logger.Warn("extractor: dream execute failed", "turn_id", t.TurnID, "error", err)
}

// redactDreamOutput 对 LLM 输出做二次脱敏，并拒绝仍包含敏感模式的结果。
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

// rejectResidualSecrets 再跑一遍 redactor 检查残留命中，命中时丢弃该提炼结果。
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

// newExtractedSkill 为已脱敏 SKILL.md 生成稳定 hash、slug 和仓库指纹。
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

// truncateRedactedSample 截取已脱敏样本，避免指标或审计记录携带过长内容。
func truncateRedactedSample(sample string) string {
	if len(sample) > redactedSampleLimit {
		return sample[:redactedSampleLimit]
	}
	return sample
}

// buildRedactedPrompt 将轨迹工具调用序列化成 prompt，并在交给 LLM 前先执行脱敏。
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

// slugFromTrajectory 从 turnID 派生稳定 slug，供提炼结果在当前仓库作用域内去重。
// 暂不信任 LLM frontmatter 名称，避免模型输出直接影响唯一键。
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

// 编译期断言确保 DefaultExtractor 持续满足 Extractor 接口。
var _ Extractor = (*DefaultExtractor)(nil)

// ExtractorRunner 按固定 tick 将 collector 已 drain 的轨迹送入 extractor。
// 它注册到 run.Group 的 runners 聚合，生命周期跟随 supervisor；父 context 取消时会干净退出。
type ExtractorRunner struct {
	collector *Collector
	extractor Extractor
	logger    *pkglogger.Logger
	interval  time.Duration
}

// NewExtractorRunner 创建后台 runner；collector/extractor 缺失时 Run 只等待上下文取消。
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

// Run 按固定 tick 拉取已完成轨迹，启动时会先校验 extractor 配置。
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

// flushOnce 提取当前已 drain 的轨迹；单条轨迹 panic 或失败不影响同批其他轨迹。
func (r *ExtractorRunner) flushOnce(ctx context.Context) {
	drained := r.collector.Drain()
	for _, traj := range drained {
		if ctx.Err() != nil {
			return
		}
		// 单条轨迹 panic 不能拖垮 runner；这里恢复后继续处理同批剩余轨迹。
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
