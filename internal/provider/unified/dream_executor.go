package unified

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type dreamExecutor struct {
	order     []string
	executors map[string]contract.DreamExecutor
	logger    *slog.Logger
}

func NewDreamExecutor(providers []contract.DreamExecutorProvider, logger *slog.Logger) contract.DreamExecutor {
	if logger == nil {
		logger = pkglogger.Get()
	}
	resolver := &dreamExecutor{
		executors: make(map[string]contract.DreamExecutor, len(providers)),
		logger:    logger,
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
