package shared

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
)

// NonNilContext 返回非 nil context，保持 shared 包旧入口兼容。
func NonNilContext(ctx context.Context) context.Context { return util.NonNilContext(ctx) }

// CheckCtx 返回 context 当前错误；nil context 视为未取消。
func CheckCtx(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
