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
// 未列出的按字母序补后。未识别的 provider 名会阻断启动。
const dreamProviderOrderEnv = "DREAM_PROVIDER_ORDER"

// defaultDreamTimeout 是统一 dream 调度的单次调用上限。
// 调用方 ctx 更短时优先尊重调用方，否则用该值防止 provider 调用无界等待。
const defaultDreamTimeout = platformconfig.DreamConsolidationTimeout

// defaultMaxPromptBytes 是统一 dream 调度的单次 prompt 大小上限。
// dream 输入可能由多份上下文拼接而成，超过上限时 fail-fast 以控制成本、延迟和输出质量。
const defaultMaxPromptBytes = 256 * 1024

// dreamExecutor 按 provider 顺序执行 dream 蒸馏，并在未配置 provider 时做受控 failover。
type dreamExecutor struct {
	order          []string
	executors      map[string]contract.DreamExecutor
	logger         *slog.Logger
	timeout        time.Duration
	maxPromptBytes int
}

// NewDreamExecutor 汇总所有 provider 的 dream executor，并解析环境变量覆盖后的 failover 顺序。
func NewDreamExecutor(providers []contract.DreamExecutorProvider, logger *slog.Logger) contract.DreamExecutor {
	executor, err := newDreamExecutor(providers, logger)
	if err != nil {
		return dreamExecutorConfigError{err: err}
	}
	return executor
}

// newDreamExecutor 是 Fx 装配使用的显式错误构造路径，配置错误必须在启动期返回。
func newDreamExecutor(providers []contract.DreamExecutorProvider, logger *slog.Logger) (contract.DreamExecutor, error) {
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
	order, err := resolveProviderOrder(resolver.order, override)
	if err != nil {
		return nil, err
	}
	resolver.order = order
	logger.Debug("dream executor registered", "providers", resolver.order)
	if strings.TrimSpace(override) != "" {
		logger.Info("dream provider order overridden",
			"env", override,
			"resolved", resolver.order,
		)
	}
	return resolver, nil
}

type dreamExecutorConfigError struct {
	err error
}

// ExecuteDream 返回构造期配置错误，兼容旧构造入口但不继续执行 provider 调用。
func (e dreamExecutorConfigError) ExecuteDream(context.Context, string) (string, error) {
	return "", e.err
}

// ExecuteDreamWithOptions 返回构造期配置错误，避免带选项入口绕过启动配置校验。
func (e dreamExecutorConfigError) ExecuteDreamWithOptions(context.Context, string, contract.DreamOptions) (string, error) {
	return "", e.err
}

// resolveProviderOrder 解析 DREAM_PROVIDER_ORDER 覆盖。override 空 → 全部字母序。
// 非空时列出且已注册的 provider 按 CSV 顺序在前，剩下的字母序补后。
// 未识别名会返回错误阻断启动，重复名/空项仍按幂等输入处理。纯函数便于单测。
func resolveProviderOrder(registered []string, override string) ([]string, error) {
	out := append([]string(nil), registered...)
	if strings.TrimSpace(override) == "" {
		sort.Strings(out)
		return out, nil
	}
	inSet := make(map[string]bool, len(out))
	for _, n := range out {
		inSet[n] = true
	}
	used := make(map[string]bool, len(out))
	var ordered []string
	for raw := range strings.SplitSeq(override, ",") {
		n := normalizeDreamProviderName(raw)
		if n == "" || used[n] {
			continue
		}
		if !inSet[n] {
			return nil, fmt.Errorf("unknown %s provider %q", dreamProviderOrderEnv, n)
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
	return append(ordered, rest...), nil
}

// ExecuteDream 使用默认选项执行 dream 蒸馏，实际 provider 选择交给 ExecuteDreamWithOptions。
func (e *dreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	return e.ExecuteDreamWithOptions(ctx, prompt, contract.DreamOptions{})
}

// ExecuteDreamWithOptions 先做输入守门和超时绑定，再按配置或选项选择 provider 执行。
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

// preflight 检查入参 prompt 和大小上限。ctx 检查放在 ExecuteDream
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

// providerOrderForOptions 返回本次执行使用的 provider 顺序。
// 显式指定 provider 时只允许命中已注册项，避免悄悄落到其它 provider。
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

// executeProviderDream 按 provider 能力选择带选项或基础接口，保持旧 provider 的兼容性。
func executeProviderDream(ctx context.Context, executor contract.DreamExecutor, prompt string, options contract.DreamOptions) (string, error) {
	if withOptions, ok := executor.(contract.DreamExecutorWithOptions); ok {
		return withOptions.ExecuteDreamWithOptions(ctx, prompt, options)
	}
	return executor.ExecuteDream(ctx, prompt)
}

// normalizeDreamProviderName 标准化 provider 名称，环境变量和注册名共用同一规则。
func normalizeDreamProviderName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
