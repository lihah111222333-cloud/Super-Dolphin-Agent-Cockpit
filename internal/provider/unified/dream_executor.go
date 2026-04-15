package unified

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type dreamExecutor struct {
	order     []string
	executors map[string]contract.DreamExecutor
}

func NewDreamExecutor(providers []contract.DreamExecutorProvider) contract.DreamExecutor {
	resolver := &dreamExecutor{executors: make(map[string]contract.DreamExecutor, len(providers))}
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
			return result, nil
		}
		if errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
			lastNotConfigured = err
			continue
		}
		return "", err
	}
	if lastNotConfigured != nil {
		return "", lastNotConfigured
	}
	return "", fmt.Errorf("%w: no provider dream executors registered", contract.ErrDreamExecutorNotConfigured)
}
