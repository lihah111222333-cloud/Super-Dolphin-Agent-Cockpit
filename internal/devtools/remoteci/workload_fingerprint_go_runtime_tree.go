package remoteci

import (
	"path"
	"strings"
)

// addGoProductionRuntimeTreeEntries 绑定生产运行时可能读取的完整生产树，
// 并保留目标测试包本身；其他包的 _test.go 由各自 selector 闭包负责。
func (snapshot *remoteGitTreeSnapshot) addGoProductionRuntimeTreeEntries(targetDirectory string, selected map[string]remoteGitTreeEntry) {
	for _, entry := range snapshot.entries {
		if strings.HasSuffix(entry.path, "_test.go") && path.Dir(entry.path) != targetDirectory {
			continue
		}
		selected[entry.path] = entry
	}
}
