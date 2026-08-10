//go:build !darwin && !linux

package hiddenexec

import "os/exec"

type nativeSupervisedCommand struct {
	cmd *exec.Cmd
}

// NewPlatformSupervisedCommand 在非 Darwin 平台保留原生 pidfd 或 Job Object owner。
func NewPlatformSupervisedCommand(name string, args ...string) (SupervisedProcessCommand, error) {
	return &nativeSupervisedCommand{cmd: Command(name, args...)}, nil
}

// Command 返回非 Darwin 平台的原生命令。
func (c *nativeSupervisedCommand) Command() *exec.Cmd {
	return c.cmd
}

// StartProcessTree 使用非 Darwin 平台的原生 owner 启动命令。
func (c *nativeSupervisedCommand) StartProcessTree() (*ProcessTree, error) {
	return StartProcessTree(c.cmd)
}

// Close 在非 Darwin 平台没有额外控制资源需要释放。
func (c *nativeSupervisedCommand) Close() error {
	return nil
}

// runProcessSupervisor 在非 Darwin 平台拒绝内部监管模式；这些平台继续使用 pidfd 或 Job Object owner。
func runProcessSupervisor([]string) int {
	return 2
}
