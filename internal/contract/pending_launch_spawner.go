package contract

import (
	"context"

	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
)

// PendingLaunchSpawner 是 pending_launch thread 首次收到 turn 时延迟启动 provider CLI 的 owner-side 边界。
// threadID 定位待启动线程；userInputForRouter 供路由选择 prompt；requestCWD 只做校验，
// 实现必须使用 pending_launch 记录里的 cwd 启动，并在 cwd 不一致时先失败再产生 provider 副作用。
// launched=false 表示线程已运行或无需启动；routing 只在实际启动时返回给 UI 展示。
type PendingLaunchSpawner interface {
	SpawnIfNeeded(ctx context.Context, threadID, userInputForRouter, requestCWD string) (launched bool, routing threaddto.SpawnRouting, err error)
}

// LaunchIntentCompleter 是 thread 启动意图完成后的回写边界。
// 调用方只传 threadID，具体幂等和持久化状态由 thread 模块负责。
type LaunchIntentCompleter interface {
	CompleteLaunchIntent(ctx context.Context, threadID string)
}
