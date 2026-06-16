package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

const npmCommand = "npm"

type postgresInstaller interface {
	EnsureInstalled(context.Context) error
}

type npmPostgresInstaller struct {
	lookPath func(string) (string, error)
	run      func(context.Context, string, ...string) ([]byte, error)
}

func newNPMPostgresInstaller() postgresInstaller {
	return npmPostgresInstaller{
		lookPath: exec.LookPath,
		run:      runCommandCombinedOutput,
	}
}

// EnsureInstalled 确保 postgres MCP server 的 npm 全局命令可直接启动。
// 本地命令不存在时才执行 npm install -g，安装后仍不可见就报错。
func (i npmPostgresInstaller) EnsureInstalled(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if i.lookPath == nil {
		return errors.New("mcp_server: postgres installer lookPath is not configured")
	}
	if _, err := i.lookPath(defaultPostgresCommand); err == nil {
		return nil
	}
	npmPath, err := i.lookPath(npmCommand)
	if err != nil {
		return fmt.Errorf("mcp_server: npm is required to install %s: %w", defaultPostgresPackage, err)
	}
	if i.run == nil {
		return errors.New("mcp_server: postgres installer runner is not configured")
	}
	output, err := i.run(ctx, npmPath, "install", "-g", defaultPostgresPackage)
	if err != nil {
		return fmt.Errorf("mcp_server: install %s with npm -g: %w\nOutput: %s", defaultPostgresPackage, err, string(output))
	}
	if _, err := i.lookPath(defaultPostgresCommand); err != nil {
		return fmt.Errorf("mcp_server: install %s succeeded but %s is not available on PATH: %w", defaultPostgresPackage, defaultPostgresCommand, err)
	}
	return nil
}

func runCommandCombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
