// Package main provides the standalone Super Dolphin gate coordinator and worker CLI.
package main

import (
	"fmt"
	"io"
	"os"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	mcplspcatalogguard "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/mcplspguardcore"
)

var (
	gateSourceDigest    string
	gateToolchainDigest string
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

// runCLI 统一执行 CLI、映射退出码并保持 stderr 协议边界。
func runCLI(args []string, stdout, stderr io.Writer) int {
	if topLevelHelpRequested(args) {
		if err := writeTopLevelUsage(stdout); err != nil {
			return int(gatecontract.ExitInfrastructure)
		}
		return int(gatecontract.ExitOK)
	}
	if len(args) > 0 && args[0] == "worker" {
		return runWorkerCLI(args[1:], stdout, stderr)
	}
	err := dispatchCLI(args, stdout)
	if err == nil {
		return int(gatecontract.ExitOK)
	}
	exitCode := gatecontract.ExitCodeOf(err)
	if writeErr := writeCLIError(stderr, err); writeErr != nil {
		return int(gatecontract.ExitInfrastructure)
	}
	return int(exitCode)
}

// topLevelHelpRequested 只识别 CLI 根命令帮助，避免进入配置、token 或执行分派。
func topLevelHelpRequested(args []string) bool {
	return len(args) > 0 && (args[0] == "--help" || args[0] == "-h")
}

// writeTopLevelUsage 在执行前给出本地 Gate 的最小命令入口。
func writeTopLevelUsage(stdout io.Writer) error {
	_, err := fmt.Fprint(stdout, `Usage: super-dolphin-gate <command> [flags]

Commands:
  super-dolphin-gate codemap|capability-contract|project-map ...
      Check or refresh the repository's generated maps and contracts.
  super-dolphin-gate closure ...
      Check or refresh the trusted local Gate closure.
  super-dolphin-gate launcher ...
      Build or verify a trusted local Gate launcher.
  super-dolphin-gate worker mcp-lsp-catalog-guard ...
      Run the trusted MCP/LSP catalog guard.
`)
	return err
}

func writeCLIError(stderr io.Writer, commandErr error) error {
	if _, err := fmt.Fprintf(stderr, "super-dolphin-gate: %v\n", commandErr); err != nil {
		return fmt.Errorf("write CLI error: %w", err)
	}
	return nil
}

// runWorkerCLI 只暴露 hook 消费的 MCP/LSP catalog guard worker。
func runWorkerCLI(args []string, stdout, stderr io.Writer) int {
	if handled, exitCode := runWorkerBuiltinCommand(args, stdout, stderr); handled {
		return exitCode
	}
	if len(args) == 0 {
		return workerBuiltinErrorExitCode(stderr, protocolError("worker command is required (mcp-lsp-catalog-guard)"))
	}
	return workerBuiltinErrorExitCode(stderr, protocolError("unknown worker command %q", args[0]))
}

// runWorkerBuiltinCommand 处理唯一保留的 catalog guard worker 子命令。
func runWorkerBuiltinCommand(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	if args[0] == "mcp-lsp-catalog-guard" {
		return true, runMcpLSPCatalogGuardWorker(args[1:], stdout, stderr)
	}
	return false, 0
}

func runMcpLSPCatalogGuardWorker(args []string, stdout, stderr io.Writer) int {
	if err := mcplspcatalogguard.Run(args, stdout); err != nil {
		return workerBuiltinErrorExitCode(stderr, err)
	}
	return int(gatecontract.ExitOK)
}

func workerBuiltinErrorExitCode(stderr io.Writer, commandErr error) int {
	if err := writeCLIError(stderr, commandErr); err != nil {
		return int(gatecontract.ExitInfrastructure)
	}
	return int(gatecontract.ExitCodeOf(commandErr))
}

// dispatchCLI 将固定命令面分派到已接线的 scheduler 边界。
func dispatchCLI(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return protocolError("subcommand is required (codemap, capability-contract, project-map, closure, frontend-code-size, launcher, worker)")
	}
	if handled, err := dispatchPrimaryCLI(args, stdout); handled {
		return err
	}
	return protocolError("unknown subcommand %q", args[0])
}

// dispatchPrimaryCLI 分派 coordinator 命令空间之外的固定子命令。
func dispatchPrimaryCLI(args []string, stdout io.Writer) (bool, error) {
	switch args[0] {
	case "codemap", "capability-contract", "project-map":
		return true, runGeneratedMapCLI(args[0], args[1:], stdout)
	case "closure", "frontend-code-size":
		return true, runLocalGuardCLI(args, stdout)
	case "launcher":
		return true, runLauncherCLI(args[1:])
	default:
		return false, nil
	}
}

// runGeneratedMapCLI 分派精确树绑定的生成物命令。
func runGeneratedMapCLI(command string, args []string, stdout io.Writer) error {
	if command == "codemap" {
		return runCodemapCLI(args, stdout)
	}
	if command == "capability-contract" {
		return runCapabilityContractCLI(args, stdout)
	}
	return runProjectMapCLI(args, stdout)
}
func protocolError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitProtocol, fmt.Errorf(format, args...))
}

func sourceError(format string, args ...any) error {
	return gatecontract.WithExitCode(gatecontract.ExitSourceMismatch, fmt.Errorf(format, args...))
}
