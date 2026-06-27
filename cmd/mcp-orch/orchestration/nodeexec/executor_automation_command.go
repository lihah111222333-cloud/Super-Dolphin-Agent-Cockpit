package nodeexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	automationCommandStdoutLimitBytes = 1024 * 1024
	automationCommandStderrLimitBytes = 256 * 1024
)

var sensitiveCommandTextPattern = regexp.MustCompile(`(?i)\b(token|api[_-]?key|password|secret)=\S+`)

// ShellCommandRunner 执行 automation command 卡片。
// 调用前会校验风险级别、工作区边界和命令文本，失败时不启动 shell。
type ShellCommandRunner struct{}

// preparedAutomationCommand 保存一次命令执行所需的进程和可回传结果。
type preparedAutomationCommand struct {
	cmd            *exec.Cmd            // 已绑定 context/cwd/env 的 shell 进程
	stdout         *commandOutputBuffer // 带上限的 stdout 缓冲区
	stderr         *commandOutputBuffer // 带上限的 stderr 缓冲区
	command        string               // 渲染后的命令，回传前会脱敏
	normalizedArgs json.RawMessage      // 模板渲染后的结构化参数快照
}

// NewShellCommandRunner 创建无状态 shell 命令 runner。
// runner 本身不持有工作区或环境，所有执行边界必须随 RunCommandCard 的 options 显式传入。
func NewShellCommandRunner() *ShellCommandRunner { return &ShellCommandRunner{} }

// RunCommandCard 运行命令卡；shell 执行必须显式标记 high 风险，并限制在允许工作区内。
func (ShellCommandRunner) RunCommandCard(ctx context.Context, card AutomationCommandCard, args json.RawMessage, opts ...AutomationCommandRunOptions) (AutomationCommandResult, error) {
	prepared, err := prepareAutomationCommand(ctx, card, args, opts)
	if err != nil {
		return AutomationCommandResult{}, err
	}
	err = prepared.cmd.Run()
	result := automationCommandResult(card, prepared)
	if err == nil {
		return result, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, ctxErr
	}
	return result, CommandExitError{ExitCode: result.ExitCode, Err: err}
}

// prepareAutomationCommand 渲染命令模板并组装可执行的 shell 进程。
// 所有策略、安全和路径校验都在这里完成，调用方拿到结果后才能真正 Run。
func prepareAutomationCommand(ctx context.Context, card AutomationCommandCard, args json.RawMessage, opts []AutomationCommandRunOptions) (preparedAutomationCommand, error) {
	if err := validateAutomationCommandPolicy(card); err != nil {
		return preparedAutomationCommand{}, err
	}
	runOpts, err := normalizeAutomationRunOptions(opts)
	if err != nil {
		return preparedAutomationCommand{}, err
	}
	cwd, err := resolveAutomationCommandCWD(runOpts)
	if err != nil {
		return preparedAutomationCommand{}, err
	}
	command, normalizedArgs, err := renderCommandTemplate(card.CommandTemplate, args)
	if err != nil {
		return preparedAutomationCommand{}, err
	}
	if err := validateRenderedCommandShellSafety(command); err != nil {
		return preparedAutomationCommand{}, err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	applyAutomationCommandOptions(cmd, cwd, runOpts.Env)
	stdout := newCommandOutputBuffer("stdout", automationCommandStdoutLimitBytes)
	stderr := newCommandOutputBuffer("stderr", automationCommandStderrLimitBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return preparedAutomationCommand{
		cmd:            cmd,
		stdout:         stdout,
		stderr:         stderr,
		command:        command,
		normalizedArgs: normalizedArgs,
	}, nil
}

func applyAutomationCommandOptions(cmd *exec.Cmd, cwd string, env map[string]string) {
	cmd.Dir = cwd
	cmd.Env = allowedAutomationCommandEnv(env)
}

func automationCommandResult(card AutomationCommandCard, prepared preparedAutomationCommand) AutomationCommandResult {
	exitCode := 0
	if prepared.cmd.ProcessState != nil {
		exitCode = prepared.cmd.ProcessState.ExitCode()
	}
	return AutomationCommandResult{
		CardKey:  card.CardKey,
		ExitCode: exitCode,
		Stdout:   redactSensitiveText(prepared.stdout.String()),
		Stderr:   redactSensitiveText(prepared.stderr.String()),
		Command:  redactSensitiveText(prepared.command),
		Args:     prepared.normalizedArgs,
	}
}

func validateAutomationCommandPolicy(card AutomationCommandCard) error {
	if strings.ToLower(strings.TrimSpace(card.RiskLevel)) != "high" {
		return errors.New("shell execution requires high-risk policy")
	}
	return nil
}

func normalizeAutomationRunOptions(opts []AutomationCommandRunOptions) (AutomationCommandRunOptions, error) {
	switch len(opts) {
	case 0:
		return AutomationCommandRunOptions{}, errors.New("automation command runner requires run options with cwd and workspace roots")
	case 1:
		return opts[0], validateAutomationCommandEnv(opts[0].Env)
	default:
		return AutomationCommandRunOptions{}, errors.New("automation command runner accepts one options object")
	}
}

// resolveAutomationCommandCWD 解析命令工作目录并确认它落在允许的 workspace root 内。
// cwd 和 root 都会做 symlink 归一化，避免通过链接路径绕过执行边界。
func resolveAutomationCommandCWD(opts AutomationCommandRunOptions) (string, error) {
	cwd := strings.TrimSpace(opts.CWD)
	if cwd == "" {
		return "", errors.New("command cwd is required")
	}
	canonicalCWD, err := canonicalExistingPath("cwd", cwd)
	if err != nil {
		return "", err
	}
	if len(opts.WorkspaceRoots) == 0 {
		return "", errors.New("command cwd requires allowed workspace roots")
	}
	if !cwdWithinAnyWorkspaceRoot(canonicalCWD, opts.WorkspaceRoots) {
		return "", errors.New("command cwd outside allowed workspace root")
	}
	return canonicalCWD, nil
}

func canonicalExistingPath(label, raw string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("resolve command %s: %w", label, err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve command %s: %w", label, err)
	}
	return filepath.Clean(canonical), nil
}

func cwdWithinAnyWorkspaceRoot(cwd string, roots []string) bool {
	for _, root := range roots {
		canonicalRoot, err := canonicalExistingPath("workspace root", root)
		if err == nil && pathWithinRoot(cwd, canonicalRoot) {
			return true
		}
	}
	return false
}

func pathWithinRoot(pathValue, root string) bool {
	rel, err := filepath.Rel(root, pathValue)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

var automationCommandEnvAllowlist = map[string]struct{}{
	"PATH":        {},
	"HOME":        {},
	"USERPROFILE": {},
	"TEMP":        {},
	"TMP":         {},
}

func validateAutomationCommandEnv(env map[string]string) error {
	for key := range env {
		if _, ok := automationCommandEnvAllowlist[strings.ToUpper(strings.TrimSpace(key))]; !ok {
			return fmt.Errorf("environment variable %q is not allowed for automation command execution", key)
		}
	}
	return nil
}

func allowedAutomationCommandEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, strings.TrimSpace(key)+"="+value)
	}
	return out
}

func redactSensitiveText(text string) string {
	return sensitiveCommandTextPattern.ReplaceAllString(text, "$1=[REDACTED]")
}

func stripAutomationControlFieldsBeforePromptReuse(raw string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &payload); err != nil {
		return raw
	}
	for _, key := range []string{"stderr", "command", "exit_code", "args", "card_key"} {
		delete(payload, key)
	}
	cleaned, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return string(cleaned)
}

type commandOutputBuffer struct {
	label     string
	limit     int
	buf       bytes.Buffer
	total     int
	truncated bool
}

func newCommandOutputBuffer(label string, limit int) *commandOutputBuffer {
	return &commandOutputBuffer{label: label, limit: limit}
}

// Write 写入命令输出缓冲区，并在达到上限后继续报告完整写入长度。
// 这样不会阻塞子进程 stdout/stderr，同时结果里保留截断标记供调用方判断。
func (b *commandOutputBuffer) Write(p []byte) (int, error) {
	b.total += len(p)
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || len(p) > 0
		return len(p), nil
	}
	if len(p) > remaining {
		if _, err := b.buf.Write(p[:remaining]); err != nil {
			return 0, err
		}
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

// String 返回已收集输出；如果发生截断，会追加截断说明。
// 调用方会在回传前统一脱敏，所以这里不直接改写原始缓冲内容。
func (b *commandOutputBuffer) String() string {
	out := b.buf.String()
	if !b.truncated {
		return out
	}
	dropped := b.total - b.buf.Len()
	if dropped < 0 {
		dropped = 0
	}
	return out + fmt.Sprintf(
		"\n[super-dolphin: %s truncated after %d bytes; dropped %d bytes]\n",
		b.label,
		b.limit,
		dropped,
	)
}

type CommandExitError struct {
	ExitCode int
	Err      error
}

// Error 返回带退出码的命令失败描述，供 automation 错误分类识别为 hard failure。
func (e CommandExitError) Error() string {
	return fmt.Sprintf("command exited with code %d: %v", e.ExitCode, e.Err)
}

// Unwrap 返回 exec 层底层错误，保留 errors.Is/As 的判断能力。
func (e CommandExitError) Unwrap() error { return e.Err }
