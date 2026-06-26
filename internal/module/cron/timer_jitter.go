// Package cron 提供定时任务的 CRUD 接口和调度运行时。
// service 先把坏配置挡住；scheduler 只推进已入库的 job，并靠 CAS 与 claim_token 防止重复执行。
package cron

import (
	"math/rand"
	"time"
)

// timerDelayWithJitter 在 interval 基础上叠加最多 25% 的随机抖动，避免多实例同时触发。
func timerDelayWithJitter(interval time.Duration) time.Duration {
	if interval <= 0 {
		return interval
	}
	maxJitter := interval / 4
	if maxJitter <= 0 {
		return interval
	}
	return interval + time.Duration(rand.Int63n(int64(maxJitter)))
}
