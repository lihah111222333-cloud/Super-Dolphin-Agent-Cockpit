package codexapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/dreamexec"
	"github.com/anthropic-ai/super-agent-v3/pkg/dreammetrics"
)

const (
	dreamBinaryEnv            = "DREAM_CODEX_BIN"
	dreamModelEnv             = "DREAM_CODEX_MODEL"
	defaultCodexBin           = "codex"
	dreamMaxStdoutBytes int64 = 256 * 1024
	dreamMaxRetries           = 1
)

type dreamExecutor struct {
	commander dreamexec.Commander
	binary    string
	model     string
}

func newDreamExecutor(commander dreamexec.Commander, binary, model string) dreamExecutor {
	if commander == nil {
		commander = dreamexec.NewRealCommander()
	}
	if strings.TrimSpace(binary) == "" {
		binary = resolveDreamBinary()
	}
	return dreamExecutor{
		commander: commander,
		binary:    binary,
		model:     strings.TrimSpace(model),
	}
}

func resolveDreamBinary() string {
	if bin := strings.TrimSpace(os.Getenv(dreamBinaryEnv)); bin != "" {
		return bin
	}
	return defaultCodexBin
}

func provideDreamExecutorProvider() contract.DreamExecutorProvider {
	return contract.DreamExecutorProvider{
		Name:     "codex",
		Executor: newDreamExecutor(nil, "", os.Getenv(dreamModelEnv)),
	}
}

// ExecuteDream 执行dream。
func (e dreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	return e.ExecuteDreamWithOptions(ctx, prompt, contract.DreamOptions{})
}

// ExecuteDreamWithOptions 执行带选项的dream。
func (e dreamExecutor) ExecuteDreamWithOptions(ctx context.Context, prompt string, options contract.DreamOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	args := []string{"exec", "--json", "--skip-git-repo-check"}
	if modelProvider := strings.TrimSpace(options.ModelProvider); modelProvider != "" {
		args = append(args, "-c", "model_provider="+strconv.Quote(modelProvider))
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = e.model
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	raw, err := dreamexec.Run(ctx, e.commander, dreamexec.RunOptions{
		Binary:         e.binary,
		Args:           args,
		Prompt:         prompt,
		MaxStdoutBytes: dreamMaxStdoutBytes,
		MaxRetries:     dreamMaxRetries,
		OnUsage: func(usage dreamexec.TokenUsage) {
			dreammetrics.AddTokens(usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens)
		},
	})
	if err != nil {
		if errors.Is(err, dreamexec.ErrBinaryNotAvailable) {
			return "", fmt.Errorf("%w: codex binary %q not available", contract.ErrDreamExecutorNotConfigured, e.binary)
		}
		if errors.Is(err, dreamexec.ErrModelUnavailable) {
			return "", fmt.Errorf("%w: codex model unavailable: %w", contract.ErrDreamExecutorNotConfigured, err)
		}
		return "", err
	}
	return raw, nil
}
