//go:build manual

// 仅 manual tag 编译，不污染生产 export 表面。
// 用途：允许端到端 manual integration test 跨包构造 provider。
package claudecli

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// NewDreamExecutorProviderForManualTest 与 provideDreamExecutorProvider 等价，
// 仅供 build tag manual 的 integration test 调用。
func NewDreamExecutorProviderForManualTest() contract.DreamExecutorProvider {
	return provideDreamExecutorProvider()
}
