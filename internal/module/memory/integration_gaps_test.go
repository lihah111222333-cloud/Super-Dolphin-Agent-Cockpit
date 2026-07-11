//go:build e2e
// +build e2e

package memory

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt"
)

// 本文件只保留 e2e 测试共享的小 helper。
// 入口注入行为由 memory 包内 entrypoint provider 测试覆盖，避免在 e2e build tag 下重复锁旧路径。

func findResolvedSection(sections []prompt.ResolvedPromptSection, name string) (prompt.ResolvedPromptSection, bool) {
	for _, section := range sections {
		if section.Name == name {
			return section, true
		}
	}
	return prompt.ResolvedPromptSection{}, false
}
