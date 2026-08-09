package hiddenexec

import "os/exec"

const processSupervisorModeArgument = "__super_dolphin_lsp_process_supervisor_v1"

// SupervisedProcessCommand 统一 transport 使用的平台命令、启动 owner 与控制资源释放边界。
type SupervisedProcessCommand interface {
	Command() *exec.Cmd
	StartProcessTree() (*ProcessTree, error)
	Close() error
}

// RunProcessSupervisorIfRequested 在 mcp-lsp 正常初始化前识别内部语言服务器监管模式。
// 返回 handled=true 时调用方必须直接以 exitCode 退出，不能继续启动 MCP/Fx runtime。
func RunProcessSupervisorIfRequested(args []string) (handled bool, exitCode int) {
	if len(args) < 2 || args[1] != processSupervisorModeArgument {
		return false, 0
	}
	return true, runProcessSupervisor(args)
}
