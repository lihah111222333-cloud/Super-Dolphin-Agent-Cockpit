//go:build windows

package multilsp

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// lspStartupDiagnosticFields 只记录 Windows LSP 启动失败所需的统计事实。
// 参数、环境值和完整路径永远不写入日志；Job flags 由 hiddenexec 的 Windows
// suspended-start 日志记录，这里补充同一 child 的 PID/start identity。
func lspStartupDiagnosticFields(options transportOptions, cmd *exec.Cmd) []any {
	args := []string(nil)
	if cmd != nil {
		args = cmd.Args
	}
	env := os.Environ()
	if cmd != nil && cmd.Env != nil {
		env = cmd.Env
	}
	fields := []any{
		"startup_diagnostic", true,
		"startup_argument_count", len(args),
		"startup_argument_utf16_bytes", utf16Bytes(args),
		"startup_env_count", len(env),
		"startup_env_utf16_bytes", utf16Bytes(env),
		"startup_working_directory_bytes", utf16Bytes([]string{options.Dir}),
		"startup_working_directory_sha256", sha256Hex(options.Dir),
	}
	fields = append(fields, startupPathDiagnosticFields(env)...)
	if cmd == nil || cmd.Process == nil {
		fields = append(fields, "startup_child_pid", 0, "startup_child_start_identity_sha256", "")
		return fields
	}
	start, err := hiddenexec.ProcessStartIdentity(cmd.Process.Pid)
	if err != nil {
		start = ""
	}
	fields = append(fields,
		"startup_child_pid", cmd.Process.Pid,
		"startup_child_start_identity_sha256", sha256Hex(start),
	)
	fields = append(fields, "startup_binary_sha256", sha256File(cmd.Path))
	return fields
}

// startupPathDiagnosticFields 只记录 PATH 的规模、目录 basename 和目录值哈希，
// 便于区分环境块膨胀与路径污染；绝不输出 PATH 的绝对值。目录顺序保持原样，
// 让同一启动边界的诊断可复核且不改变子进程环境。
func startupPathDiagnosticFields(env []string) []any {
	var raw string
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(key, "PATH") {
			raw = value
			break
		}
	}
	if raw == "" {
		return []any{"startup_path_entry_count", 0, "startup_path_utf16_bytes", 0, "startup_path_entry_basenames", "", "startup_path_entry_hashes", ""}
	}
	entries := strings.Split(raw, string(os.PathListSeparator))
	basenames := make([]string, 0, len(entries))
	hashes := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		basenames = append(basenames, filepath.Base(entry))
		hashes = append(hashes, sha256Hex(entry))
	}
	return []any{
		"startup_path_entry_count", len(basenames),
		"startup_path_utf16_bytes", utf16Bytes(entries),
		"startup_path_entry_basenames", strings.Join(basenames, "|"),
		"startup_path_entry_hashes", strings.Join(hashes, ","),
	}
}

func utf16Bytes(values []string) int {
	total := 0
	for _, value := range values {
		total += len(utf16.Encode([]rune(value))) * 2
	}
	return total
}

func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func sha256File(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// lspTransportWriteFailureFields 只记录管道写失败的统计量、错误摘要和 stderr 摘要，
// 不输出完整参数、环境值或工作区路径；这使 Windows pipe closed 可与 Win122 分开裁决。
func lspTransportWriteFailureFields(t *transport, stage string, writeErr error) []any {
	fields := []any{
		"write_stage", stage,
		"write_elapsed_ms", time.Since(t.startedAt).Milliseconds(),
		"stdin_present", t.stdin != nil,
		"stdout_present", t.stdout != nil,
		"stderr_present", t.stderr != nil,
	}
	fields = append(fields, platformshared.SafePayloadLogFields("write_error", writeErr.Error())...)
	if t.stderr != nil {
		stderr, totalBytes, truncated := t.stderr.Snapshot()
		fields = append(fields, "stderr_total_bytes", totalBytes, "stderr_truncated", truncated, "stderr_sha256", sha256Hex(stderr))
		fields = append(fields, platformshared.SafePayloadLogFields("stderr_tail", stderr)...)
	}
	return fields
}
