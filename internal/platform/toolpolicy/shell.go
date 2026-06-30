package toolpolicy

import (
	"fmt"
	"strings"
	"unicode"
)

type shellCommandPolicy func(args []string) bool

type shellWordScanner struct {
	words         []string
	current       strings.Builder
	inSingleQuote bool
	tokenStarted  bool
}

type gitBranchArgState struct {
	listMode bool
}

var readOnlyShellCommands = map[string]shellCommandPolicy{
	"cat":  allowPathReaderArgs,
	"git":  allowGitArgs,
	"grep": allowAnyReadOnlyArgs,
	"head": allowAnyReadOnlyArgs,
	"ls":   allowAnyReadOnlyArgs,
	"nl":   allowAnyReadOnlyArgs,
	"pwd":  allowNoArgs,
	"rg":   allowRipgrepArgs,
	"sed":  allowSedArgs,
	"tail": allowAnyReadOnlyArgs,
	"wc":   allowAnyReadOnlyArgs,
}

var deniedShellCommands = map[string]struct{}{
	"bash":      {},
	"bg":        {},
	"disown":    {},
	"exec":      {},
	"fg":        {},
	"fish":      {},
	"jobs":      {},
	"kill":      {},
	"killall":   {},
	"nohup":     {},
	"osascript": {},
	"pkill":     {},
	"screen":    {},
	"setsid":    {},
	"sh":        {},
	"sleep":     {},
	"sudo":      {},
	"su":        {},
	"tmux":      {},
	"wait":      {},
	"zsh":       {},
}

var dangerousShellArgs = map[string]struct{}{
	"-c":             {},
	"-delete":        {},
	"-exec":          {},
	"-i":             {},
	"--command":      {},
	"--config-env":   {},
	"--delete":       {},
	"--exec":         {},
	"--exec-path":    {},
	"--in-place":     {},
	"--out-dir":      {},
	"--output":       {},
	"--outfile":      {},
	"--receive-pack": {},
	"--upload-pack":  {},
}

var dangerousShellArgPrefixes = []string{
	"--config-env=",
	"--exec=",
	"--exec-path=",
	"--out-dir=",
	"--output=",
	"--outfile=",
	"--receive-pack=",
	"--upload-pack=",
}

var gitBranchReadOnlyFlags = map[string]struct{}{
	"--all":          {},
	"--color":        {},
	"--column":       {},
	"--list":         {},
	"--no-color":     {},
	"--no-column":    {},
	"--remotes":      {},
	"--show-current": {},
	"-a":             {},
	"-r":             {},
	"-v":             {},
	"-vv":            {},
}

var gitBranchOptionalValueFlags = map[string]struct{}{
	"--contains":  {},
	"--merged":    {},
	"--no-merged": {},
}

var gitBranchRequiredValueFlags = map[string]struct{}{
	"--format":    {},
	"--points-at": {},
	"--sort":      {},
}

// ClassifyShell 判断一条 shell 字符串是否属于规划/只读阶段可接受的读命令。
// 解析器只支持简单 argv 形态；遇到 shell 语法、后台执行或危险参数会直接拒绝。
func ClassifyShell(command string) Decision {
	words, parseErr := splitShellWords(command)
	if parseErr != nil {
		return deny(CodeShellSyntaxDenied, parseErr.Error())
	}
	if len(words) == 0 {
		return deny(CodeShellEmptyCommand, "toolpolicy: shell command is empty")
	}

	name := words[0]
	args := words[1:]
	if _, denied := deniedShellCommands[name]; denied {
		return deny(CodeShellCommandDenied, fmt.Sprintf("toolpolicy: shell command %q is not read-only", name))
	}
	policy, ok := readOnlyShellCommands[name]
	if !ok {
		return deny(CodeShellCommandDenied, fmt.Sprintf("toolpolicy: shell command %q is not in read-only table", name))
	}
	if badArg := firstDangerousShellArg(args); badArg != "" {
		return deny(CodeShellArgumentDenied, fmt.Sprintf("toolpolicy: shell argument %q is not allowed", badArg))
	}
	if !policy(args) {
		return deny(CodeShellCommandDenied, fmt.Sprintf("toolpolicy: shell command %q is outside read-only table", name))
	}
	return allow("toolpolicy: shell command is read-only")
}

// splitShellWords 只解析简单 argv 形态，任何需要真实 shell 求值的写法都 fail-closed。
func splitShellWords(command string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, nil
	}

	scanner := &shellWordScanner{}
	for _, r := range command {
		if err := scanner.consume(r); err != nil {
			return nil, err
		}
	}
	return scanner.finish()
}

func (s *shellWordScanner) consume(r rune) error {
	if s.inSingleQuote {
		return s.consumeQuoted(r)
	}
	if unicode.IsSpace(r) {
		s.flush()
		return nil
	}
	if r == '\'' {
		s.inSingleQuote = true
		s.tokenStarted = true
		return nil
	}
	if isShellSyntaxRune(r) {
		return fmt.Errorf("toolpolicy: shell syntax %q is not allowed", string(r))
	}
	s.current.WriteRune(r)
	s.tokenStarted = true
	return nil
}

func (s *shellWordScanner) consumeQuoted(r rune) error {
	if r == '\'' {
		s.inSingleQuote = false
		s.tokenStarted = true
		return nil
	}
	if r == '\n' || r == '\r' || r == 0 {
		return fmt.Errorf("toolpolicy: shell quoted argument contains a control character")
	}
	s.current.WriteRune(r)
	return nil
}

func (s *shellWordScanner) flush() {
	if !s.tokenStarted && s.current.Len() == 0 {
		return
	}
	s.words = append(s.words, s.current.String())
	s.current.Reset()
	s.tokenStarted = false
}

func (s *shellWordScanner) finish() ([]string, error) {
	if s.inSingleQuote {
		return nil, fmt.Errorf("toolpolicy: unterminated shell quote")
	}
	s.flush()
	return s.words, nil
}

func isShellSyntaxRune(r rune) bool {
	switch r {
	case '&', ';', '|', '<', '>', '\\', '"', '`', '$', '(', ')', '{', '}', '[', ']', '*', '?', '!', '\n', '\r':
		return true
	default:
		return false
	}
}

// firstDangerousShellArg 找出会切换到命令执行、写文件或 provider 子进程配置的参数。
func firstDangerousShellArg(args []string) string {
	for _, arg := range args {
		if _, denied := dangerousShellArgs[arg]; denied {
			return arg
		}
		for _, prefix := range dangerousShellArgPrefixes {
			if strings.HasPrefix(arg, prefix) {
				return arg
			}
		}
	}
	return ""
}

func allowNoArgs(args []string) bool {
	return len(args) == 0
}

func allowAnyReadOnlyArgs([]string) bool {
	return true
}

func allowPathReaderArgs(args []string) bool {
	return len(args) > 0
}

func allowRipgrepArgs(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--pre") {
			return false
		}
	}
	return true
}

// allowGitArgs 按 git 子命令拆分只读判断，避免 branch 等混合读写子命令整体放行。
func allowGitArgs(args []string) bool {
	args = trimGitReadOnlyGlobalArgs(args)
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "branch":
		return allowGitBranchArgs(args[1:])
	case "diff", "log", "rev-parse", "show", "status":
		return true
	case "worktree":
		return len(args) >= 2 && args[1] == "list"
	default:
		return false
	}
}

// allowGitBranchArgs 只允许 branch 的查看/列举参数；任何默认会创建、删除、重命名或改 upstream 的形态都拒绝。
func allowGitBranchArgs(args []string) bool {
	state := &gitBranchArgState{}
	for i := 0; i < len(args); i++ {
		consumed, ok := state.consume(args[i], args[i+1:])
		if !ok {
			return false
		}
		i += consumed
	}
	return true
}

// consume 消费一个 git branch 参数；返回额外吞掉的 value 数量，并在非只读形态上立即拒绝。
func (s *gitBranchArgState) consume(arg string, rest []string) (int, bool) {
	if arg == "" {
		return 0, false
	}
	if s.consumeReadOnlyFlag(arg) {
		return 0, true
	}
	if consumed, ok, handled := consumeGitBranchOptionalValue(arg, rest); handled {
		return consumed, ok
	}
	if consumed, ok, handled := consumeGitBranchRequiredValue(arg, rest); handled {
		return consumed, ok
	}
	if strings.HasPrefix(arg, "-") {
		return 0, false
	}
	return 0, s.listMode
}

func (s *gitBranchArgState) consumeReadOnlyFlag(arg string) bool {
	if _, ok := gitBranchReadOnlyFlags[arg]; ok {
		s.markListMode(arg)
		return true
	}
	if flag, ok := gitBranchInlineValueFlag(arg); ok {
		s.markListMode(flag)
		return true
	}
	return false
}

func (s *gitBranchArgState) markListMode(flag string) {
	if flag == "--list" {
		s.listMode = true
	}
}

func consumeGitBranchOptionalValue(arg string, rest []string) (int, bool, bool) {
	if _, ok := gitBranchOptionalValueFlags[arg]; !ok {
		return 0, false, false
	}
	if hasGitBranchValue(rest) {
		return 1, true, true
	}
	return 0, true, true
}

func consumeGitBranchRequiredValue(arg string, rest []string) (int, bool, bool) {
	if _, ok := gitBranchRequiredValueFlags[arg]; !ok {
		return 0, false, false
	}
	return 1, hasGitBranchValue(rest), true
}

func hasGitBranchValue(rest []string) bool {
	return len(rest) > 0 && rest[0] != "" && !strings.HasPrefix(rest[0], "-")
}

func gitBranchInlineValueFlag(arg string) (string, bool) {
	for _, flags := range []map[string]struct{}{
		gitBranchOptionalValueFlags,
		gitBranchRequiredValueFlags,
		{"--list": {}},
	} {
		flag, ok := gitBranchInlineValueFlagInMap(arg, flags)
		if ok {
			return flag, true
		}
	}
	return "", false
}

func gitBranchInlineValueFlagInMap(arg string, flags map[string]struct{}) (string, bool) {
	for flag := range flags {
		prefix := flag + "="
		if strings.HasPrefix(arg, prefix) && strings.TrimPrefix(arg, prefix) != "" {
			return flag, true
		}
	}
	return "", false
}

func trimGitReadOnlyGlobalArgs(args []string) []string {
	for len(args) > 0 {
		switch args[0] {
		case "--no-pager":
			args = args[1:]
		default:
			return args
		}
	}
	return args
}

func allowSedArgs(args []string) bool {
	if len(args) < 2 || args[0] != "-n" {
		return false
	}
	return isSedPrintProgram(args[1])
}

// isSedPrintProgram 只接受 `sed -n` 的数字行号打印脚本，避免 s/a/b/ 这类转换脚本混进规划阶段。
func isSedPrintProgram(program string) bool {
	if strings.TrimSpace(program) != program || !strings.HasSuffix(program, "p") {
		return false
	}
	body := strings.TrimSuffix(program, "p")
	if body == "" {
		return false
	}
	parts := strings.Split(body, ",")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || !allDigits(part) {
			return false
		}
	}
	return true
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
