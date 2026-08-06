// Package cronmetrics 提供 Cron 恢复路径的实例计数源，供调度器写入并由 Prometheus 导出层读取。
package cronmetrics

import "sync/atomic"

// Metrics 保存单个 cron 调度器的恢复路径计数器。
type Metrics struct {
	recoveryFinalizeConflictTotal atomic.Uint64
	recoveryFinalizeErrorTotal    atomic.Uint64
}

// New 创建独立的 Cron 恢复指标 owner。
func New() *Metrics { return &Metrics{} }

// IncRecoveryFinalizeConflict 记录一次被 fence 拒绝的恢复终态冲突。
func (m *Metrics) IncRecoveryFinalizeConflict() { m.recoveryFinalizeConflictTotal.Add(1) }

// IncRecoveryFinalizeError 记录一次经幂等复核后仍失败的恢复终态操作。
func (m *Metrics) IncRecoveryFinalizeError() { m.recoveryFinalizeErrorTotal.Add(1) }

// Snapshot 是 Cron 恢复计数器的时点快照；并发增量可能出现在下一次读取中。
type Snapshot struct {
	RecoveryFinalizeConflictTotal uint64
	RecoveryFinalizeErrorTotal    uint64
}

// Read 返回当前 Cron 恢复指标快照。
func (m *Metrics) Read() Snapshot {
	return Snapshot{
		RecoveryFinalizeConflictTotal: m.recoveryFinalizeConflictTotal.Load(),
		RecoveryFinalizeErrorTotal:    m.recoveryFinalizeErrorTotal.Load(),
	}
}

// ResetForTesting 仅供测试将全部 Cron 恢复计数器归零。
func (m *Metrics) ResetForTesting() {
	m.recoveryFinalizeConflictTotal.Store(0)
	m.recoveryFinalizeErrorTotal.Store(0)
}
