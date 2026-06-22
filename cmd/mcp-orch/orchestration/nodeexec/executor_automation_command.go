package nodeexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

type ShellCommandRunner struct{}

type preparedAutomationCommand struct {
	cmd            *exec.Cmd
	stdout         *commandOutputBuffer
	stderr         *commandOutputBuffer
	command        string
	normalizedArgs json.RawMessage
}

// NewShellCommandRunner 创建 shell 命令 runner。
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
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), allowedAutomationCommandEnv(env)...)
	}
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
		return AutomationCommandRunOptions{}, nil
	case 1:
		return opts[0], validateAutomationCommandEnv(opts[0].Env)
	default:
		return AutomationCommandRunOptions{}, errors.New("automation command runner accepts one options object")
	}
}

func resolveAutomationCommandCWD(opts AutomationCommandRunOptions) (string, error) {
	cwd := strings.TrimSpace(opts.CWD)
	if cwd == "" && len(opts.WorkspaceRoots) == 0 {
		return "", nil
	}
	if cwd == "" {
		cwd = opts.WorkspaceRoots[0]
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

// Write 写入编排。
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

// String 返回字符串表示。
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

// Error 返回错误文本。
func (e CommandExitError) Error() string {
	return fmt.Sprintf("command exited with code %d: %v", e.ExitCode, e.Err)
}

// Unwrap 返回底层错误。
func (e CommandExitError) Unwrap() error { return e.Err }
