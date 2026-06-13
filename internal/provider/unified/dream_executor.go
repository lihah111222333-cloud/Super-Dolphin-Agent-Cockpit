package unified

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/pkg/dreammetrics"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// dreamProviderOrderEnv 是 failover 顺序覆盖环境变量。
// CSV 格式（如 "codex,claude"），列出的 provider 按指定顺序在前，
// 未列出的按字母序补后。未识别的 provider 名忽略。
const dreamProviderOrderEnv = "DREAM_PROVIDER_ORDER"

// defaultDreamTimeout 是 dispatcher 兜底的单次 dream 蒸馏超时。
// 上层 ctx 若已有更短 deadline，platformconfig.WithTimeout 取较近者，本常量不会覆盖。
// 上层 auto_dream_task.go startDreamTask 当前只 cancel 不带 deadline，
// 此处兜底防止单次真 LLM 调用无界 hang。
// 实际值集中在 platform/config/timeouts.go (TimeoutLocality 规范)。
const defaultDreamTimeout = platformconfig.DreamConsolidationTimeout

// defaultMaxPromptBytes 是 dispatcher 兜底的单次 prompt 大小上限（256KB ≈ 64k UTF-8 字符，
// 约占 200k token context window 的 32%）。
// consolidation_prompt.go 拼接全部 topic + log 文件，无上层 cap，
// 此处 fail-fast 防止误传整库 prompt 把成本/延迟/质量同时打崩。
const defaultMaxPromptBytes = 256 * 1024

type dreamExecutor struct {
	order          []string
	executors      map[string]contract.DreamExecutor
	logger         *slog.Logger
	timeout        time.Duration
	maxPromptBytes int
}

// NewDreamExecutor 创建dreamexecutor。
func NewDreamExecutor(providers []contract.DreamExecutorProvider, logger *slog.Logger) contract.DreamExecutor {
	if logger == nil {
		logger = pkglogger.Get()
	}
	resolver := &dreamExecutor{
		executors:      make(map[string]contract.DreamExecutor, len(providers)),
		logger:         logger,
		timeout:        defaultDreamTimeout,
		maxPromptBytes: defaultMaxPromptBytes,
	}
	for _, provider := range providers {
		name := normalizeDreamProviderName(provider.Name)
		if name == "" || provider.Executor == nil {
			continue
		}
		if _, exists := resolver.executors[name]; !exists {
			resolver.order = append(resolver.order, name)
		}
		resolver.executors[name] = provider.Executor
	}
	override := os.Getenv(dreamProviderOrderEnv)
	resolver.order = resolveProviderOrder(resolver.order, override)
	logger.Debug("dream executor registered", "providers", resolver.order)
	if strings.TrimSpace(override) != "" {
		logger.Info("dream provider order overridden",
			"env", override,
			"resolved", resolver.order,
		)
	}
	return resolver
}

// resolveProviderOrder 解析 DREAM_PROVIDER_ORDER 覆盖。override 空 → 全部字母序。
// 非空时列出且已注册的 provider 按 CSV 顺序在前，剩下的字母序补后。
// 未识别名/重复名/空项均忽略。纯函数便于单测。
func resolveProviderOrder(registered []string, override string) []string {
	out := append([]string(nil), registered...)
	if strings.TrimSpace(override) == "" {
		sort.Strings(out)
		return out
	}
	inSet := make(map[string]bool, len(out))
	for _, n := range out {
		inSet[n] = true
	}
	used := make(map[string]bool, len(out))
	var ordered []string
	for raw := range strings.SplitSeq(override, ",") {
		n := normalizeDreamProviderName(raw)
		if n == "" || !inSet[n] || used[n] {
			continue
		}
		ordered = append(ordered, n)
		used[n] = true
	}
	var rest []string
	for _, n := range out {
		if !used[n] {
			rest = append(rest, n)
		}
	}
	sort.Strings(rest)
	return append(ordered, rest...)
}

// ExecuteDream 执行dream。
func (e *dreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	return e.ExecuteDreamWithOptions(ctx, prompt, contract.DreamOptions{})
}

// ExecuteDreamWithOptions 执行带选项的dream。
func (e *dreamExecutor) ExecuteDreamWithOptions(ctx context.Context, prompt string, options contract.DreamOptions) (string, error) {
	if err := e.preflight(prompt); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = platformconfig.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	return e.runFailover(ctx, prompt, options)
}

// preflight 检查入参 prompt + size cap。ctx 检查放在 ExecuteDream
// 避免 preflight 依赖 ctx（主要责任是输入质量守门）。
func (e *dreamExecutor) preflight(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("dream prompt is empty")
	}
	if e.maxPromptBytes > 0 && len(prompt) > e.maxPromptBytes {
		e.logger.Warn("dream prompt exceeds size limit",
			"size_bytes", len(prompt),
			"max_bytes", e.maxPromptBytes,
		)
		dreammetrics.IncPromptOversize()
		return fmt.Errorf("dream prompt too large: %d bytes exceeds %d", len(prompt), e.maxPromptBytes)
	}
	return nil
}

// runFailover 依次调用注册的 provider，遇 NotConfigured 跳过，
// 遇真错误立即短路，全链路未配置返回最后一个 NotConfigured。
func (e *dreamExecutor) runFailover(ctx context.Context, prompt string, options contract.DreamOptions) (string, error) {
	order, requestedProvider, err := e.providerOrderForOptions(options)
	if err != nil {
		return "", err
	}
	var lastNotConfigured error
	for _, name := range order {
		executor := e.executors[name]
		if executor == nil {
			continue
		}
		result, err := executeProviderDream(ctx, executor, prompt, options)
		if err == nil {
			e.logger.Info("dream executor succeeded", "provider", name, "size_bytes", len(result))
			dreammetrics.IncSuccess()
			return result, nil
		}
		if requestedProvider {
			e.logger.Warn("requested dream executor failed", "provider", name, "error", err)
			dreammetrics.IncProviderFailed()
			return "", fmt.Errorf("dream executor provider %q failed: %w", name, err)
		}
		if errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
			e.logger.Debug("dream executor skipped (not configured)", "provider", name)
			dreammetrics.IncProviderSkipped()
			lastNotConfigured = err
			continue
		}
		e.logger.Warn("dream executor failed", "provider", name, "error", err)
		dreammetrics.IncProviderFailed()
		return "", err
	}
	if lastNotConfigured != nil {
		e.logger.Warn("all dream executors not configured", "providers", order)
		dreammetrics.IncAllNotConfigured()
		return "", lastNotConfigured
	}
	return "", fmt.Errorf("%w: no provider dream executors registered", contract.ErrDreamExecutorNotConfigured)
}

func (e *dreamExecutor) providerOrderForOptions(options contract.DreamOptions) ([]string, bool, error) {
	provider := normalizeDreamProviderName(options.Provider)
	if provider == "" {
		return append([]string(nil), e.order...), false, nil
	}
	if _, ok := e.executors[provider]; !ok {
		return nil, true, fmt.Errorf("dream executor provider %q is not registered", provider)
	}
	return []string{provider}, true, nil
}

func executeProviderDream(ctx context.Context, executor contract.DreamExecutor, prompt string, options contract.DreamOptions) (string, error) {
	if withOptions, ok := executor.(contract.DreamExecutorWithOptions); ok {
		return withOptions.ExecuteDreamWithOptions(ctx, prompt, options)
	}
	return executor.ExecuteDream(ctx, prompt)
}

func normalizeDreamProviderName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
