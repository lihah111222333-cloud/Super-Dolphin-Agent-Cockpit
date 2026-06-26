package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
)

var blockedCommands = map[string]bool{
	"chmod": true, "chown": true, "curl": true, "dd": true, "fdisk": true, "kill": true,
	"killall": true, "mkfs": true, "mount": true, "passwd": true, "pkill": true, "reboot": true,
	"rm": true, "rmdir": true, "shutdown": true, "su": true, "sudo": true, "umount": true, "useradd": true,
	"userdel": true, "wget": true, "iptables": true,
}

var shellInterpreters = map[string]bool{"bash": true, "cmd": true, "cmd.exe": true, "dash": true, "fish": true, "ksh": true, "powershell": true, "powershell.exe": true, "pwsh": true, "pwsh.exe": true, "sh": true, "zsh": true}

var codeExecutionCommands = map[string]bool{"bun": true, "bun.exe": true, "deno": true, "deno.exe": true, "dotnet": true, "dotnet.exe": true, "go": true, "go.exe": true, "java": true, "java.exe": true, "node": true, "node.exe": true, "npm": true, "npm.cmd": true, "npx": true, "npx.cmd": true, "perl": true, "perl.exe": true, "php": true, "php.exe": true, "py": true, "py.exe": true, "python": true, "python.exe": true, "python3": true, "python3.exe": true, "ruby": true, "ruby.exe": true}

var readCommands = map[string]bool{"ag": true, "awk": true, "bat": true, "cat": true, "fd": true, "find": true, "grep": true, "head": true, "less": true, "more": true, "rg": true, "sed": true, "tail": true, "tree": true, "wc": true}

var execBaseEnvKeys = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "TERM",
}

var execAllowedEnvPrefixes = []string{"DYN_TOOL_", "STRESS_TEST_", "TEST_E2E_"}

var execAllowedEnvKeys = map[string]bool{"LOG_LEVEL": true}

const lspPreferenceHint = "[LSP提示] 优先用 LSP 工具读代码：file inspect xref grep structure edit completion。\n"

type execParams struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"-"`
}

type execParamsWire struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
	Argv    []string          `json:"argv,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// UnmarshalJSON 解码命令执行 RPC 入参。
// 新 wire 格式使用 command/args；argv 仅用于兼容旧调用方，并会拆成相同的内部结构。
func (p *execParams) UnmarshalJSON(data []byte) error {
	var wire execParamsWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	next := execParams{
		Command: wire.Command,
		Args:    append([]string(nil), wire.Args...),
		CWD:     wire.CWD,
		Env:     cloneExecEnv(wire.Env),
	}
	if strings.TrimSpace(next.Command) == "" && len(wire.Argv) > 0 {
		next.Command, next.Args = splitLegacyArgv(wire.Argv)
	}
	*p = next
	return nil
}

func splitLegacyArgv(argv []string) (string, []string) {
	if len(argv) == 0 {
		return "", nil
	}
	return argv[0], append([]string(nil), argv[1:]...)
}

func cloneExecEnv(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

// ExecCommand 执行受限的只读类外部命令。
// 默认禁止 shell、代码运行时和高风险系统命令，失败时直接返回错误而不降级执行。
func (s *service) ExecCommand(ctx context.Context, command string, args []string, cwd string, env map[string]string) (ExecResult, error) {
	return s.execCommand(ctx, command, args, cwd, env, false)
}

func (s *service) execCommand(ctx context.Context, command string, args []string, cwd string, env map[string]string, allowShell bool) (ExecResult, error) {
	name, base, err := validateExecCommand(command, allowShell)
	if err != nil {
		return ExecResult{}, err
	}
	if err := validateExecPayload(base, args, allowShell); err != nil {
		return ExecResult{}, err
	}
	dir := resolveExecCWD(cwd, s.projectRoot)
	execCtx, cancel := ctxutil.WithRPCRequestTimeout(ctx)
	defer cancel()
	return runExecCommand(execCtx, name, base, args, dir, buildExecEnv(dir, env))
}

// validateExecCommand 校验命令 basename 是否允许执行。
// allowShell 仅供内部受控路径使用，普通 RPC 入口必须阻断 shell 解释器和代码运行时。
func validateExecCommand(command string, allowShell bool) (string, string, error) {
	name := strings.TrimSpace(command)
	base := normalizeExecToken(name)
	switch {
	case base == "." || base == "":
		return "", "", errors.New("command is required")
	case blockedCommands[base]:
		return "", "", errors.New("command is blocked for security")
	case !allowShell && shellInterpreters[base]:
		return "", "", errors.New("shell interpreters are not allowed")
	case !allowShell && codeExecutionCommands[base]:
		return "", "", errors.New("code execution runtimes are not allowed")
	default:
		return name, base, nil
	}
}

// validateExecPayload 校验命令参数中是否隐藏危险 token。
// 非 shell 路径拒绝元字符；shell 路径会 tokenize 后继续检查包裹命令链。
func validateExecPayload(base string, args []string, allowShell bool) error {
	if !allowShell {
		if err := validateExecArgs(args); err != nil {
			return err
		}
		if blocked := detectDangerousTokens(literalExecTokens(base, args)); blocked != "" {
			return fmt.Errorf("command is blocked for security: %s", blocked)
		}
		return nil
	}
	shellCmd, ok := shellCommandArg(base, args)
	if !ok {
		return errors.New("shell command is required")
	}
	if blocked := detectDangerousTokens(tokenizeShellCommand(shellCmd)); blocked != "" {
		return fmt.Errorf("command is blocked for security: %s", blocked)
	}
	return nil
}

func validateExecArgs(args []string) error {
	for _, arg := range args {
		if strings.ContainsAny(arg, "|;&$`") {
			return errors.New("shell metacharacters are not allowed in args")
		}
	}
	return nil
}

func runExecCommand(ctx context.Context, name, base string, args []string, cwd string, env []string) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = strings.TrimSpace(cwd)
	cmd.Env = env
	stdout, stderr := &limitedBuffer{limit: maxSkillFileBytes}, &limitedBuffer{limit: maxSkillFileBytes}
	cmd.Stdout, cmd.Stderr = io.MultiWriter(stdout), io.MultiWriter(stderr)
	result := ExecResult{Command: name, CWD: cmd.Dir}
	err := cmd.Run()
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	if readCommands[base] && !strings.HasPrefix(result.Stdout, lspPreferenceHint) {
		result.Stdout = lspPreferenceHint + result.Stdout
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return ExecResult{}, err
}

func resolveExecCWD(cwd, projectRoot string) string {
	if dir := strings.TrimSpace(cwd); dir != "" {
		return dir
	}
	return strings.TrimSpace(projectRoot)
}

func buildExecEnv(cwd string, overlay map[string]string) []string {
	env := baseExecEnv()
	if dir := strings.TrimSpace(cwd); dir != "" {
		env = mergeExecEnv(env, map[string]string{"PWD": dir})
	}
	env = append(env, allowedPrefixedExecEnv()...)
	return mergeExecEnv(env, overlay)
}

func baseExecEnv() []string {
	env := make([]string, 0, len(execBaseEnvKeys))
	for _, key := range execBaseEnvKeys {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func allowedPrefixedExecEnv() []string {
	env := make([]string, 0, 8)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && isAllowedExecEnvKey(key) {
			env = append(env, entry)
		}
	}
	return env
}

// mergeExecEnv 合并执行环境变量白名单。
// 只允许 PWD、固定 key 和受控前缀，避免调用方通过环境变量绕过命令限制。
func mergeExecEnv(base []string, overlay map[string]string) []string {
	if len(overlay) == 0 {
		return base
	}
	index := execEnvIndex(base)
	for key, value := range overlay {
		name := strings.TrimSpace(key)
		if name == "" || (name != "PWD" && !isAllowedExecEnvKey(name)) {
			continue
		}
		entry := name + "=" + value
		upper := strings.ToUpper(name)
		if pos, ok := index[upper]; ok {
			base[pos] = entry
			continue
		}
		index[upper] = len(base)
		base = append(base, entry)
	}
	return base
}

func execEnvIndex(env []string) map[string]int {
	index := make(map[string]int, len(env))
	for i, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			index[strings.ToUpper(strings.TrimSpace(key))] = i
		}
	}
	return index
}

func isAllowedExecEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if execAllowedEnvKeys[upper] {
		return true
	}
	for _, prefix := range execAllowedEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func isBlockedCommand(name string) bool { return blockedCommands[name] }

func isShellInterpreter(name string) bool { return shellInterpreters[name] }

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

// Write 写入受限缓冲区。
// 超过 limit 的内容会被截断但仍返回已消费长度，防止子进程因 pipe 回压卡住。
func (b *limitedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if b.limit <= b.buf.Len() {
		return written, nil
	}
	remaining := b.limit - b.buf.Len()
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return written, nil
}

// String 返回当前缓冲区内容。
func (b *limitedBuffer) String() string { return b.buf.String() }
