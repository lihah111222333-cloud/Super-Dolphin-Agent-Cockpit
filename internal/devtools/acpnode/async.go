package acpnode

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

// launchACP 启动带 panic 记录的 ACP 后台任务；调用方必须显式提供生命周期上下文。
func launchACP(ctx context.Context, label string, action func()) {
	safego.Go(ctx, nil, label, func(context.Context) {
		action()
	})
}
