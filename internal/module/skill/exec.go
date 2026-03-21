package skill

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

var blockedCommands = map[string]bool{
	"chmod": true, "chown": true, "curl": true, "dd": true, "fdisk": true, "kill": true,
	"killall": true, "mkfs": true, "mount": true, "passwd": true, "pkill": true, "reboot": true,
	"rm": true, "rmdir": true, "shutdown": true, "su": true, "sudo": true, "umount": true, "useradd": true,
	"userdel": true, "wget": true, "iptables": true,
}

var shellInterpreters = map[string]bool{
	"bash": true, "cmd": true, "cmd.exe": true, "dash": true, "fish": true, "ksh": true,
	"powershell": true, "powershell.exe": true, "pwsh": true, "pwsh.exe": true, "sh": true, "zsh": true,
}

var readCommands = map[string]bool{
	"ag": true, "awk": true, "bat": true, "cat": true, "fd": true, "find": true, "grep": true,
	"head": true, "less": true, "more": true, "rg": true, "sed": true, "tail": true, "tree": true, "wc": true,
}

var execBaseEnvKeys = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "TERM",
}

var execAllowedEnvPrefixes = []string{
	"OPENAI_", "ANTHROPIC_", "CODEX_", "DYN_TOOL_", "MODEL", "LOG_LEVEL", "AGENT_", "MCP_", "APP_", "STRESS_TEST_", "TEST_E2E_",
}

const lspPreferenceHint = "[LSP提示] 优先用 LSP 工具读代码：lsp_file lsp_inspect lsp_xref lsp_grep lsp_structure lsp_edit lsp_completion。\n"

func (s *service) execShell(ctx context.Context, shellCmd, cwd string) (ExecResult, error) {
	return s.execCommand(ctx, "sh", []string{"-lc", shellCmd}, cwd, nil, true)
}

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
	execCtx, cancel := platformconfig.WithRPCRequestTimeout(ctx)
	defer cancel()
	return runExecCommand(execCtx, name, base, args, dir, buildExecEnv(dir, env))
}

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
	default:
		return name, base, nil
	}
}

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
	for _, prefix := range execAllowedEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= b.buf.Len() {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }
