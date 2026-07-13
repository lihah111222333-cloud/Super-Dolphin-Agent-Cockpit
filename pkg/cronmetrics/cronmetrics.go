// Package cronmetrics 提供 Cron 恢复路径的进程内计数源，供调度器写入并由 Prometheus 导出层读取。
package cronmetrics

import "sync/atomic"

var (
	recoveryFinalizeConflictTotal atomic.Uint64
	recoveryFinalizeErrorTotal    atomic.Uint64
)

// IncRecoveryFinalizeConflict 记录一次被 fence 拒绝的恢复终态冲突。
func IncRecoveryFinalizeConflict() { recoveryFinalizeConflictTotal.Add(1) }

// IncRecoveryFinalizeError 记录一次经幂等复核后仍失败的恢复终态操作。
func IncRecoveryFinalizeError() { recoveryFinalizeErrorTotal.Add(1) }

// Snapshot 是 Cron 恢复计数器的时点快照；并发增量可能出现在下一次读取中。
type Snapshot struct {
	RecoveryFinalizeConflictTotal uint64
	RecoveryFinalizeErrorTotal    uint64
}

// Read 返回当前 Cron 恢复指标快照。
func Read() Snapshot {
	return Snapshot{
		RecoveryFinalizeConflictTotal: recoveryFinalizeConflictTotal.Load(),
		RecoveryFinalizeErrorTotal:    recoveryFinalizeErrorTotal.Load(),
	}
}

// ResetForTesting 仅供测试将全部 Cron 恢复计数器归零。
func ResetForTesting() {
	recoveryFinalizeConflictTotal.Store(0)
	recoveryFinalizeErrorTotal.Store(0)
}
