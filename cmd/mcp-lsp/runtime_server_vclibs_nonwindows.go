//go:build !windows

package main

// runtimeServerPlatformDependencyEnvironment 在非 Windows 平台保持现有语言服务器
// 环境；Windows VC++ 应用本地依赖不能改变 Linux、Darwin 或其他平台的启动策略。
func runtimeServerPlatformDependencyEnvironment(_ string, _ string, env []string) ([]string, error) {
	return append([]string(nil), env...), nil
}

// runtimeServerPlatformProcessBinary 在非 Windows 平台保持原始 server 路径。
func runtimeServerPlatformProcessBinary(serverBinary string) (string, error) {
	return serverBinary, nil
}
