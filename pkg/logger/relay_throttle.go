package logger

import "sync/atomic"

// RelayFailureThrottle 抑制重复 relay 失败日志。
// 它报告前 3 次失败，之后每 20 次报告一次，避免 relay 故障刷屏。
type RelayFailureThrottle struct {
	count atomic.Int64
}

// ShouldReport 判断当前失败次数是否应该输出日志。
func (t *RelayFailureThrottle) ShouldReport() bool {
	n := t.count.Add(1)
	return n <= 3 || n%20 == 0
}
