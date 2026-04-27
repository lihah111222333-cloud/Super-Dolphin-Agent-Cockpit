package codexapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/dreamexec"
)

const (
	// dreamBinaryEnv 覆盖 codex binary 路径，未设走 PATH 默认 "codex"。
	dreamBinaryEnv = "DREAM_CODEX_BIN"
	// dreamModelEnv dream 调用 codex 可选 model override。
	dreamModelEnv = "DREAM_CODEX_MODEL"
	// defaultCodexBin 默认 binary 名。
	defaultCodexBin = "codex"
	// dreamMaxStdoutBytes 与 dispatcher prompt cap 对齐。
	dreamMaxStdoutBytes int64 = 256 * 1024
	// dreamMaxRetries: parse 失败重试一次，dispatcher failover 兑底。
	dreamMaxRetries = 1
)

type dreamExecutor struct {
	commander dreamexec.Commander
	binary    string
	model     string
}

// newDreamExecutor 构造 dream provider。commander==nil 走 NewRealCommander，
// binary 为空走 resolveDreamBinary()。
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

func (e dreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// codex exec 是 non-interactive 子命令（见 `codex --help` "Commands: exec"）
	args := []string{"exec"}
	if e.model != "" {
		args = append(args, "--model", e.model)
	}
	raw, err := dreamexec.Run(ctx, e.commander, dreamexec.RunOptions{
		Binary:         e.binary,
		Args:           args,
		Prompt:         prompt,
		MaxStdoutBytes: dreamMaxStdoutBytes,
		MaxRetries:     dreamMaxRetries,
	})
	if err != nil {
		if errors.Is(err, dreamexec.ErrBinaryNotAvailable) {
			return "", fmt.Errorf("%w: codex binary %q not available", contract.ErrDreamExecutorNotConfigured, e.binary)
		}
		return "", err
	}
	return raw, nil
}
