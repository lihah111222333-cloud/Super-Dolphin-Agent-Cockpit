package multilsp

import (
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// idleTimeoutForTest 是历史生命周期测试的 canonical fixture，不参与生产 manager wiring。
// 生产值始终来自启动时解析的 platformconfig.Config.LSP.IdleTimeout。
func idleTimeoutForTest() time.Duration {
	return platformconfig.DefaultLSPConfig().IdleTimeout
}
