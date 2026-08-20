// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rlimit"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// binaryName 和 binaryVersion 标识本 sidecar 进程名称和版本。
const (
	binaryName    = "mcp-lsp"
	binaryVersion = "dev"
)

// main 初始化 sidecar 运行环境，保护 MCP stdout 通道后启动服务，异常时以非零码退出。
func main() {
	// 所有模式都先限制调度线程；内部监管路径异常时也不能占满宿主 CPU。
	if runtime.GOMAXPROCS(0) > 2 {
		runtime.GOMAXPROCS(2)
	}
	if handled, exitCode := hiddenexec.RunProcessSupervisorIfRequested(os.Args); handled {
		os.Exit(exitCode)
	}
	if handled, exitCode := runWindowsGoplsBrokerIfRequested(os.Args); handled {
		os.Exit(exitCode)
	}
	rlimit.Init()
	stdout := os.Stdout
	os.Stdout = os.Stderr
	logRuntime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{ServiceName: binaryName, ServiceVersion: binaryVersion, LogFilePrefix: binaryName})

	homeDir, err := os.UserHomeDir()
	if err != nil {
		_, _ = os.Stderr.WriteString("mcp-lsp resolve user home failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	ownerID, err := sidecarOwnerID()
	if err != nil {
		_, _ = os.Stderr.WriteString("mcp-lsp resolve sidecar owner failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	runtimeStateDir := sidecarRuntimeDir(homeDir, ownerID)
	if err := os.Setenv("SUPER_DOLPHIN_SIDECAR_RUNTIME_DIR", runtimeStateDir); err != nil {
		_, _ = os.Stderr.WriteString("mcp-lsp set sidecar runtime dir failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	loggerGate, err := prepareSidecarFileLoggerAt(logRuntime, filepath.Join(runtimeStateDir, "log", binaryName), os.Stderr)
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	if err := os.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "sidecar"); err != nil {
		logRuntime.Get().Error("mcp-lsp startup env failed", pkglogger.FieldError, err)
		os.Exit(1)
	}
	if err := runtimeenv.ConfigureSidecarRuntime(); err != nil {
		logRuntime.Get().Error("mcp-lsp sidecar runtime env failed", pkglogger.FieldError, err)
		os.Exit(1)
	}
	pkglogger.Info("mcp-lsp startup contract",
		"owner_id", ownerID,
		"parent_launch_contract", strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_SIDECAR_OWNER_ID")) != "",
		"runtime_state_dir", runtimeStateDir,
		"logger_path", summarizeStartupPath(logRuntime.CurrentLogFilePath()),
		"runtime_mode", strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_RUNTIME_MODE")),
		"runtime_resources_dir", summarizeStartupPath(os.Getenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR")),
		"sqlite_configured", strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_SQLITE_PATH")) != "",
		"stdio_lease", "per_process",
		"owner_acl", "private",
	)
	exitCode := runMain(stdout, logRuntime, loggerGate)
	logRuntime.ShutdownFileHandler()
	os.Exit(exitCode)
}

func sidecarOwnerID() (string, error) {
	owner := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_SIDECAR_OWNER_ID"))
	if owner == "" {
		return fmt.Sprintf("pid-%d", os.Getpid()), nil
	}
	if owner == "." || owner == ".." || len(owner) > 96 {
		return "", fmt.Errorf("invalid owner identity")
	}
	for _, r := range owner {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return "", fmt.Errorf("invalid owner identity")
	}
	return owner, nil
}

func sidecarLogDir(homeDir, ownerID string) string {
	return filepath.Join(sidecarRuntimeDir(homeDir, ownerID), "log", binaryName)
}

func sidecarRuntimeDir(homeDir, ownerID string) string {
	root := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME"))
	if root == "" {
		root = filepath.Join(homeDir, ".super-dolphin")
	}
	return filepath.Join(root, "runtime-state", "sidecars", ownerID)
}

func summarizeStartupPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "unset"
	}
	return filepath.Clean(path)
}

// initSidecarFileLogger 将 mcp-lsp 日志同时写入 stderr 和进程自有的私有文件。
func initSidecarFileLogger(logRuntime *pkglogger.Runtime, homeDir string, console io.Writer) error {
	return initSidecarFileLoggerAt(logRuntime, filepath.Join(homeDir, ".super-dolphin", "log", binaryName), console)
}

// initSidecarFileLoggerAt 只接受唯一的私有日志目录；权限或路径错误必须保留并阻断启动，
// 不能换目录重试，否则 Windows ACL 5/1314 会被伪装成一次成功初始化。
func initSidecarFileLoggerAt(logRuntime *pkglogger.Runtime, logDir string, console io.Writer) error {
	if logRuntime == nil {
		return fmt.Errorf("initialize mcp-lsp file logger: logger runtime is required")
	}
	// pkg/logger 是公共包，受架构门禁约束不能依赖 internal/securefs；sidecar 在平台边界
	// 先保护目录、再创建日志并保护文件，Windows 因而校验 DACL 而不是无意义的 POSIX mode。
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("initialize mcp-lsp file logger: create private log directory: %w", wrapSidecarLogPathError("create_private_log_directory", logDir, err))
	}
	if err := securefs.RestrictPrivateOwnerOnly(logDir, 0o700); err != nil {
		return fmt.Errorf("initialize mcp-lsp file logger: protect private log directory: %w", err)
	}
	if err := logRuntime.InitWithFileOptions(logDir, pkglogger.FileOptions{
		Prefix:        binaryName,
		ConsoleWriter: console,
	}); err != nil {
		return fmt.Errorf("initialize mcp-lsp file logger: create private log file: %w", wrapSidecarLogPathError("create_private_log_file", logDir, err))
	}
	logPath := logRuntime.CurrentLogFilePath()
	if logPath == "" {
		logRuntime.ShutdownFileHandler()
		return fmt.Errorf("initialize mcp-lsp file logger: logger returned an empty path")
	}
	if err := securefs.RestrictPrivateOwnerOnly(logPath, 0o600); err != nil {
		logRuntime.ShutdownFileHandler()
		return fmt.Errorf("initialize mcp-lsp file logger: protect private log file: %w", err)
	}
	logRuntime.BindDefault()
	return nil
}

// runMain 启动 LSP sidecar 并把错误转换为进程退出码。
// 日志写 stderr，stdout 继续留给 MCP JSON-RPC 帧。
func runMain(stdout *os.File, logRuntime *pkglogger.Runtime, loggerGate *sidecarFileLoggerGate) int {
	if logRuntime == nil {
		_, _ = os.Stderr.WriteString("mcp-lsp logger runtime is required\n")
		return 1
	}
	if err := run(stdout, logRuntime, loggerGate); err != nil {
		logRuntime.Get().Error("mcp-lsp failed", "error", err)
		return 1
	}
	return 0
}
