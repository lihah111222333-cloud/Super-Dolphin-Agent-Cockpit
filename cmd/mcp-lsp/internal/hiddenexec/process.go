package hiddenexec

import (
	"context"
	"errors"
	"os/exec"
)

// processTreeController 隐藏平台进程树句柄与终止细节。
type processTreeController interface {
	terminate() error
	release() error
	rssBytes() (uint64, error)
}

// ProcessTree 显式持有一次子进程启动所对应的平台进程树 owner。
// Windows owner 持有 Job Object；Unix owner 绑定命令的独立进程组。
type ProcessTree struct {
	controller processTreeController
}

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
	cmd.Cancel = func() error {
		return KillProcessTree(cmd)
	}
	return cmd
}

// StartProcessTree 启动命令并在子进程执行用户代码前建立平台进程树所有权。
// Windows 使用受控 suspended 启动；任一绑定或恢复步骤失败都会终止并回收已创建进程。
func StartProcessTree(cmd *exec.Cmd) (*ProcessTree, error) {
	return startProcessTree(cmd)
}

// Terminate 强制终止 owner 管理的全部进程。
func (p *ProcessTree) Terminate() error {
	if p == nil || p.controller == nil {
		return errors.New("process-tree owner is nil")
	}
	return p.controller.terminate()
}

// Release 释放平台进程树 owner；该操作不会隐式降级到按 PID 回收。
func (p *ProcessTree) Release() error {
	if p == nil || p.controller == nil {
		return errors.New("process-tree owner is nil")
	}
	return p.controller.release()
}

// RSSBytes 返回 owner 当前全部成员的 RSS。
func (p *ProcessTree) RSSBytes() (uint64, error) {
	if p == nil || p.controller == nil {
		return 0, errors.New("process-tree owner is nil")
	}
	return p.controller.rssBytes()
}

// ProcessTreeRSSBytes 汇总指定语言服务器根 PID 的平台进程组 RSS。
// Windows 必须改用显式 ProcessTree owner，避免 PID 复用和 ParentProcessID 图误计。
func ProcessTreeRSSBytes(pid int) (uint64, error) {
	return processTreeRSSBytes(pid)
}

// ProcessRSSBytes 返回指定单个 PID 的当前 RSS，不包含后代。
func ProcessRSSBytes(pid int) (uint64, error) {
	return processRSSBytes(pid)
}

// ProcessAlive 报告 PID 是否仍指向活动进程。
func ProcessAlive(pid int) (bool, error) {
	return processAlive(pid)
}

// ProcessStartIdentity 返回可区分 PID 复用的进程启动身份。
func ProcessStartIdentity(pid int) (string, error) {
	return processStartIdentity(pid)
}

// KillProcessTree 强制终止命令及其派生进程。
// 语言服务器经常再启动 worker、tsserver 或编译器子进程，调用方不能只回收父 PID。
func KillProcessTree(cmd *exec.Cmd) error {
	return killProcessTree(cmd)
}
