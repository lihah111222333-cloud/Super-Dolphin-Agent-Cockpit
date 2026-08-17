//go:build !windows

package main

// runtimeServerProductionNodeVersionResolver 在非 Windows 构建中保留显式 PATH
// resolver；平台选择由本文件的 !windows build tag 完成，不存在 Windows stub 或运行时分支。
func runtimeServerProductionNodeVersionResolver(_ string) runtimeServerNodeVersionResolver {
	return runtimeServerNodeVersion
}
