package claudecli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/dreamexec"
	"github.com/anthropic-ai/super-agent-v3/pkg/dreammetrics"
)

// dreamModelEnv 是 dream 调用 claude 时可选的 model env override。未设则走 binary 默认 model。
const dreamModelEnv = "DREAM_CLAUDE_MODEL"

// dreamMaxStdoutBytes 与 dispatcher prompt size cap 对齐，dream 输出不应超 256KB。
const dreamMaxStdoutBytes int64 = 256 * 1024

// dreamMaxRetries: parse 失败重试一次就足够，dispatcher failover 兑底第二 provider。
const dreamMaxRetries = 1

type dreamExecutor struct {
	commander dreamexec.Commander
	binary    string
	model     string
}

// newDreamExecutor 构造 dream provider。commander==nil 走 NewRealCommander，
// binary 为空走 resolveBinaryPath()，便于测试注入。
func newDreamExecutor(commander dreamexec.Commander, binary, model string) dreamExecutor {
	if commander == nil {
		commander = dreamexec.NewRealCommander()
	}
	if strings.TrimSpace(binary) == "" {
		binary = resolveBinaryPath()
	}
	return dreamExecutor{
		commander: commander,
		binary:    binary,
		model:     strings.TrimSpace(model),
	}
}

// provideDreamExecutorProvider 将 Claude dream executor 注册到统一 dream provider 列表。
func provideDreamExecutorProvider() contract.DreamExecutorProvider {
	return contract.DreamExecutorProvider{
		Name:     "claude",
		Executor: newDreamExecutor(nil, "", os.Getenv(dreamModelEnv)),
	}
}

// ExecuteDream 使用默认选项执行一次 Claude dream 请求。
func (e dreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	return e.ExecuteDreamWithOptions(ctx, prompt, contract.DreamOptions{})
}

// ExecuteDreamWithOptions 通过 `claude -p --output-format json` 执行 dream，并记录 token usage。
func (e dreamExecutor) ExecuteDreamWithOptions(ctx context.Context, prompt string, options contract.DreamOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// --output-format json 让 claude -p 输出 envelope JSON（含 result + usage），
	// dreamexec.Run 自动探测后走 ExtractClaudeEnvelope，usage 由 OnUsage 路由到 dreammetrics。
	args := []string{"-p", "--output-format", "json"}
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
			return "", fmt.Errorf("%w: claude binary %q not available", contract.ErrDreamExecutorNotConfigured, e.binary)
		}
		if errors.Is(err, dreamexec.ErrModelUnavailable) {
			return "", fmt.Errorf("%w: claude model unavailable: %v", contract.ErrDreamExecutorNotConfigured, err)
		}
		return "", err
	}
	return raw, nil
}
