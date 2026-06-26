package hiddenexec

import (
	"context"
	"os/exec"
)

// Command 构造普通命令并套用平台隐藏窗口配置。
// Windows 下避免 LSP/安装器弹出控制台窗口，其他平台保持 exec 默认行为。
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	configureCommand(cmd)
	return cmd
}

// CommandContext 构造可取消命令并套用平台隐藏窗口配置。
// 调用方仍负责检查 ctx 和命令退出错误，避免吞掉安装或探测失败。
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommand(cmd)
	return cmd
}
