//go:build manual

// 仅 manual tag 编译，不污染生产 export 表面。
package codexapp

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// NewDreamExecutorProviderForManualTest 与 provideDreamExecutorProvider 等价，
// 仅供 build tag manual 的 integration test 调用。
func NewDreamExecutorProviderForManualTest() contract.DreamExecutorProvider {
	return provideDreamExecutorProvider()
}
