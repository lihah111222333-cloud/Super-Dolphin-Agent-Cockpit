package unified

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// defaultDreamTimeout 是 dispatcher 兜底的单次 dream 蒸馏超时。
// 上层 ctx 若已有更短 deadline，context.WithTimeout 取较近者，本常量不会覆盖。
// 上层 auto_dream_task.go startDreamTask 当前只 cancel 不带 deadline，
// 此处兜底防止单次真 LLM 调用无界 hang。
const defaultDreamTimeout = 5 * time.Minute

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
		name := strings.TrimSpace(provider.Name)
		if name == "" || provider.Executor == nil {
			continue
		}
		if _, exists := resolver.executors[name]; !exists {
			resolver.order = append(resolver.order, name)
		}
		resolver.executors[name] = provider.Executor
	}
	sort.Strings(resolver.order)
	logger.Debug("dream executor registered", "providers", resolver.order)
	return resolver
}

func (e *dreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("dream prompt is empty")
	}
	if e.maxPromptBytes > 0 && len(prompt) > e.maxPromptBytes {
		e.logger.Warn("dream prompt exceeds size limit",
			"size_bytes", len(prompt),
			"max_bytes", e.maxPromptBytes,
		)
		return "", fmt.Errorf("dream prompt too large: %d bytes exceeds %d", len(prompt), e.maxPromptBytes)
	}
	if e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	var lastNotConfigured error
	for _, name := range e.order {
		executor := e.executors[name]
		if executor == nil {
			continue
		}
		result, err := executor.ExecuteDream(ctx, prompt)
		if err == nil {
			e.logger.Info("dream executor succeeded", "provider", name, "size_bytes", len(result))
			return result, nil
		}
		if errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
			e.logger.Debug("dream executor skipped (not configured)", "provider", name)
			lastNotConfigured = err
			continue
		}
		e.logger.Warn("dream executor failed", "provider", name, "error", err)
		return "", err
	}
	if lastNotConfigured != nil {
		e.logger.Warn("all dream executors not configured", "providers", e.order)
		return "", lastNotConfigured
	}
	return "", fmt.Errorf("%w: no provider dream executors registered", contract.ErrDreamExecutorNotConfigured)
}
