package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

const lspPreferenceHint = "[LSP提示] 优先用 LSP 工具读代码：file inspect xref grep structure edit completion。\n"

type execCommandHandler func(context.Context, execCommandRequest) (ExecResult, error)

type execCommandRequest struct {
	command string
	base    string
	args    []string
	root    string
	cwd     string
}

var execCommandAllowlist = map[string]execCommandHandler{
	"cat": runInternalCat,
	"pwd": runInternalPWD,
}

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
	return s.execCommand(ctx, command, args, cwd, env)
}

func (s *service) execCommand(ctx context.Context, command string, args []string, cwd string, env map[string]string) (ExecResult, error) {
	if len(env) > 0 {
		return ExecResult{}, errors.New("env overrides are not supported for skill command execution")
	}
	name, base, handler, err := validateExecCommand(command)
	if err != nil {
		return ExecResult{}, err
	}
	if err := validateExecPayload(base, args); err != nil {
		return ExecResult{}, err
	}
	root, dir, err := resolveExecWorkspace(cwd, s.projectRoot)
	if err != nil {
		return ExecResult{}, err
	}
	execCtx, cancel := ctxutil.WithRPCRequestTimeout(ctx)
	defer cancel()
	return handler(execCtx, execCommandRequest{
		command: name,
		base:    base,
		args:    append([]string(nil), args...),
		root:    root,
		cwd:     dir,
	})
}

// validateExecCommand 校验命令必须命中内部 allowlist。
// command/exec 不再信任外部可执行文件查找，避免 PATH、cwd 或包装器影响执行边界。
func validateExecCommand(command string) (string, string, execCommandHandler, error) {
	name := strings.TrimSpace(command)
	base := normalizeExecToken(name)
	switch {
	case base == "." || base == "":
		return "", "", nil, errors.New("command is required")
	case name != base:
		return "", "", nil, fmt.Errorf("command must be an allowlisted basename: %s", command)
	case blockedCommands[base]:
		return "", "", nil, errors.New("command is blocked for security")
	case shellInterpreters[base]:
		return "", "", nil, errors.New("shell interpreters are not allowed")
	case codeExecutionCommands[base]:
		return "", "", nil, errors.New("code execution runtimes are not allowed")
	}
	handler, ok := execCommandAllowlist[base]
	if !ok {
		return "", "", nil, fmt.Errorf("command is not allowlisted: %s", base)
	}
	return name, base, handler, nil
}

// validateExecPayload 先做跨命令的轻量 token 检查；具体 argv 规则由每个 handler 再收紧。
func validateExecPayload(base string, args []string) error {
	if err := validateExecArgs(args); err != nil {
		return err
	}
	if blocked := detectDangerousTokens(literalExecTokens(base, args)); blocked != "" {
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

// resolveExecWorkspace 解析 command/exec 的可信根和 cwd。
// cwd 只能为空、相对 project root，或 realpath 后仍在 project root 内的绝对路径。
func resolveExecWorkspace(cwd, projectRoot string) (string, string, error) {
	root, err := trustedExecRoot(projectRoot)
	if err != nil {
		return "", "", err
	}
	dir := strings.TrimSpace(cwd)
	if dir == "" {
		return root, root, nil
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	resolved, err := realpathAwareCleanPath(dir)
	if err != nil {
		return "", "", err
	}
	if err := ensureExecPathInRoot(root, resolved); err != nil {
		return "", "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("cwd is not a directory: %s", cwd)
	}
	return root, resolved, nil
}

func trustedExecRoot(projectRoot string) (string, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return "", errors.New("trusted workspace root is required for command execution")
	}
	root, err := realpathAwareCleanPath(projectRoot)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("trusted workspace root is not a directory: %s", projectRoot)
	}
	return root, nil
}

func ensureExecPathInRoot(root, target string) error {
	escapes, err := pathEscapesRoot(root, target)
	if err != nil {
		return err
	}
	if escapes {
		return fmt.Errorf("path escapes workspace root: %s", target)
	}
	return nil
}

func runInternalPWD(ctx context.Context, req execCommandRequest) (ExecResult, error) {
	if err := checkExecContext(ctx); err != nil {
		return ExecResult{}, err
	}
	if len(req.args) != 0 {
		return ExecResult{}, errors.New("pwd does not accept args")
	}
	return ExecResult{Command: req.command, CWD: req.cwd, Stdout: req.cwd + "\n"}, nil
}

// runInternalCat 读取已验证在 workspace 内的文件内容。
// 这里不能退回外部 cat 命令，避免 PATH、cwd 或参数解析差异重新扩大读取边界。
func runInternalCat(ctx context.Context, req execCommandRequest) (ExecResult, error) {
	if err := checkExecContext(ctx); err != nil {
		return ExecResult{}, err
	}
	if len(req.args) == 0 {
		return ExecResult{}, errors.New("cat requires at least one file path")
	}
	stdout := &limitedBuffer{limit: maxSkillFileBytes}
	for _, arg := range req.args {
		path, err := resolveExecReadPath(req.root, req.cwd, arg)
		if err != nil {
			return ExecResult{}, err
		}
		data, err := readExecFile(path)
		if err != nil {
			return ExecResult{}, err
		}
		if _, err := stdout.Write(data); err != nil {
			return ExecResult{}, err
		}
	}
	result := ExecResult{Command: req.command, CWD: req.cwd, Stdout: stdout.String()}
	if !strings.HasPrefix(result.Stdout, lspPreferenceHint) {
		result.Stdout = lspPreferenceHint + result.Stdout
	}
	return result, nil
}

// resolveExecReadPath 将 cat 参数解析成真实文件路径。
// 解析会跟随符号链接并再次确认没有逃出可信 workspace root，拒绝用相对路径或绝对路径绕边界。
func resolveExecReadPath(root, cwd, arg string) (string, error) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return "", errors.New("file path is required")
	}
	if strings.HasPrefix(trimmed, "-") {
		return "", fmt.Errorf("cat flags are not allowed: %s", arg)
	}
	target := trimmed
	if !filepath.IsAbs(target) {
		target = filepath.Join(cwd, target)
	}
	resolved, err := realpathAwareCleanPath(target)
	if err != nil {
		return "", err
	}
	if err := ensureExecPathInRoot(root, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func readExecFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file: %s", path)
	}
	if info.Size() > maxSkillFileBytes {
		return nil, fmt.Errorf("file too large: %d bytes", info.Size())
	}
	return os.ReadFile(path)
}

func checkExecContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
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
