package standalone

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/processctl"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/codexwire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

// ErrProviderProbe 表示 provider 无法在独立服务发布 ready 前证明其机器协议。
var ErrProviderProbe = errors.New("orch provider conformance probe failed")

const providerProbeTimeout = 5 * time.Second

// ProviderProbe 只记录有界且不含秘密的能力观测，不是 capability lease 或授权判决。
type ProviderProbe struct {
	Name       string
	Executable string
	Protocol   string
}

// ProbeConfiguredProviders 校验全部 provider 并执行确定性的无模型握手；不变更配置、不启动模型任务，也不发布 ready 文件。
func ProbeConfiguredProviders(ctx context.Context, cfg Config) ([]ProviderProbe, error) {
	if err := requireExecutionContext(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProviderProbe, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%w: validate config: %v", ErrProviderProbe, err)
	}
	probeCtx, cancel, err := boundedProbeContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProviderProbe, err)
	}
	defer cancel()
	results := make([]ProviderProbe, 0, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		result, err := probeProvider(probeCtx, cfg.WorkspaceRoot, provider)
		if err != nil {
			return nil, fmt.Errorf("%w: provider %s: %v", ErrProviderProbe, provider.Name, err)
		}
		results = append(results, result)
	}
	return results, nil
}

// ProbeProviders 是 bootstrap 调用方使用的简短入口，与 ProbeConfiguredProviders 保持相同的 fail-fast 语义。
func ProbeProviders(ctx context.Context, cfg Config) ([]ProviderProbe, error) {
	return ProbeConfiguredProviders(ctx, cfg)
}

func boundedProbeContext(parent context.Context) (context.Context, context.CancelFunc, error) {
	if err := requireExecutionContext(parent); err != nil {
		return nil, nil, err
	}
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= providerProbeTimeout {
		ctx, cancel := context.WithCancel(parent)
		return ctx, cancel, nil
	}
	ctx, cancel := context.WithTimeout(parent, providerProbeTimeout)
	return ctx, cancel, nil
}

// probeProvider 按显式 provider 名称选择唯一的机器协议探针，不按 PATH 或默认实现降级。
func probeProvider(ctx context.Context, cwd string, provider Provider) (ProviderProbe, error) {
	if err := requireExecutionContext(ctx); err != nil {
		return ProviderProbe{}, err
	}
	if err := provider.validate(); err != nil {
		return ProviderProbe{}, err
	}
	switch provider.Name {
	case "codex":
		if err := probeCodex(ctx, cwd, provider); err != nil {
			return ProviderProbe{}, err
		}
		return ProviderProbe{Name: provider.Name, Executable: provider.Executable, Protocol: "codex-app-server-jsonl"}, nil
	case "claude":
		if err := probeClaude(ctx, cwd, provider); err != nil {
			return ProviderProbe{}, err
		}
		return ProviderProbe{Name: provider.Name, Executable: provider.Executable, Protocol: "claude-stream-json"}, nil
	default:
		return ProviderProbe{}, errors.New("unsupported provider")
	}
}

// probeCodex 只执行 app-server initialize，不创建 thread、turn、model 或任务；JSONL 帧和握手由 codexwire 统一负责。
func probeCodex(ctx context.Context, cwd string, provider Provider) error {
	if err := requireExecutionContext(ctx); err != nil {
		return err
	}
	process, err := codexwire.Start(ctx, provider.Executable, cwd, cliEnvironment(provider), nil)
	if err != nil {
		return fmt.Errorf("Codex app-server initialize: %w", err)
	}
	if err := process.Close(); err != nil {
		return fmt.Errorf("close Codex probe: %w", err)
	}
	return nil
}

// probeClaude 只发送 control initialize 验证 CLI 控制协议，不发送 user 消息或启动模型 turn。
func probeClaude(ctx context.Context, cwd string, provider Provider) (probeErr error) {
	if err := requireExecutionContext(ctx); err != nil {
		return err
	}
	sessionID, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("create Claude probe session ID: %w", err)
	}
	requestID, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("create Claude probe request ID: %w", err)
	}
	process, err := startClaudeProbe(ctx, cwd, provider, sessionID.String())
	if err != nil {
		return err
	}
	defer func() {
		cleanupErr := process.cleanup()
		if cleanupErr != nil {
			probeErr = errors.Join(probeErr, fmt.Errorf("cleanup Claude probe: %w", cleanupErr))
		}
	}()
	if err := writeClaudeProbeInitialize(process.input, requestID.String()); err != nil {
		return err
	}
	return readClaudeProbeInitialize(process.output, requestID.String())
}

type claudeProbeProcess struct {
	cmd    *exec.Cmd
	guard  *processctl.Guard
	input  io.WriteCloser
	output io.ReadCloser
}

func startClaudeProbe(ctx context.Context, cwd string, provider Provider, sessionID string) (*claudeProbeProcess, error) {
	if err := requireExecutionContext(ctx); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, provider.Executable, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--model", provider.Models[0], "--session-id", sessionID, "--permission-mode", "dontAsk")
	cmd.Dir, cmd.Env, cmd.Stderr = cwd, cliEnvironment(provider), io.Discard
	processctl.Configure(cmd)
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Join(err, input.Close())
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(err, input.Close(), output.Close())
	}
	return &claudeProbeProcess{cmd: cmd, guard: processctl.Attach(cmd, nil), input: input, output: output}, nil
}

// cleanup 关闭协议管道并回收整个进程组；任一清理步骤失败都向调用方报告。
func (p *claudeProbeProcess) cleanup() error {
	if p == nil {
		return errors.New("Claude probe process is nil")
	}
	var cleanupErr error
	cleanupErr = errors.Join(cleanupErr, closeClaudeProbePipe(p.input))
	cleanupErr = errors.Join(cleanupErr, closeClaudeProbePipe(p.output))
	if p.cmd == nil {
		return errors.Join(cleanupErr, errors.New("Claude probe command is nil"))
	}
	cleanupErr = errors.Join(cleanupErr, stopClaudeProbeCommand(p.cmd, p.guard))
	if p.guard != nil {
		p.guard.Close()
	}
	return cleanupErr
}

func stopClaudeProbeCommand(cmd *exec.Cmd, guard *processctl.Guard) error {
	var stopErr error
	termRequested := false
	if cmd.Process != nil {
		stopErr = processctl.RequestStop(cmd, guard)
		termRequested = stopErr == nil
	}
	waitCh := make(chan error, 1)
	runtimesafe.SafeGo(context.Background(), nil, "standalone.provider-probe.wait", func(context.Context) {
		waitClaudeProbeCommand(cmd, waitCh)
	})
	select {
	case err := <-waitCh:
		return errors.Join(stopErr, finishClaudeProbeWait(cmd, guard, err, termRequested, false))
	case <-time.After(3 * time.Second):
		forceErr := processctl.ForceStop(cmd, guard)
		return errors.Join(stopErr, forceErr, finishClaudeProbeWait(cmd, guard, <-waitCh, termRequested, forceErr == nil))
	}
}

// waitClaudeProbeCommand 将 cmd.Wait 结果写入有界通道，并把异常转成可回收错误。
func waitClaudeProbeCommand(cmd *exec.Cmd, result chan<- error) {
	defer func() {
		if recover() != nil {
			result <- errors.New("Claude probe command wait panicked")
		}
	}()
	result <- cmd.Wait()
}

func finishClaudeProbeWait(cmd *exec.Cmd, guard *processctl.Guard, waitErr error, termRequested, killRequested bool) error {
	if waitErr != nil && !expectedClaudeProbeExit(waitErr, termRequested, killRequested) {
		return errors.Join(waitErr, processctl.ForceStop(cmd, guard))
	}
	return processctl.ForceStop(cmd, guard)
}

// expectedClaudeProbeExit 只归一化本探针主动关停对应的 Unix 信号；CLI 的 143 是已观测的 TERM 映射。
func expectedClaudeProbeExit(err error, termRequested, killRequested bool) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ProcessState == nil {
		return false
	}
	if exitErr.ProcessState.ExitCode() == 143 {
		return termRequested
	}
	status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return false
	}
	switch status.Signal() {
	case syscall.SIGTERM:
		return termRequested
	case syscall.SIGKILL:
		return killRequested
	default:
		return false
	}
}

func closeClaudeProbePipe(pipe io.Closer) error {
	if pipe == nil {
		return nil
	}
	err := pipe.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
