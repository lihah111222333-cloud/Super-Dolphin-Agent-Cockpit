//go:build !darwin

package hiddenexec

// processStartIdentityPredatesCurrentBoot 保守拒绝没有绝对进程启动时间与
// 系统 boot time 可比证明的平台；调用方不得仅凭重启推断退役，仍可继续
// 使用 PID+start identity 的明确死亡或复用证明。
func processStartIdentityPredatesCurrentBoot(string) (bool, error) {
	return false, nil
}
