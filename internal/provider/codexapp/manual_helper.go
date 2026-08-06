//go:build manual

package codexapp

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"

// NewDreamExecutorProviderForManualTest 仅向 manual 构建暴露 Codex dream provider。
func NewDreamExecutorProviderForManualTest() contract.DreamExecutorProvider {
	return provideDreamExecutorProvider()
}
