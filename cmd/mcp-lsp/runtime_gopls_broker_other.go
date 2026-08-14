//go:build !windows

package main

// runWindowsGoplsBrokerIfRequested 在非 Windows 平台保持 broker 模式不可达。
func runWindowsGoplsBrokerIfRequested([]string) (bool, int) {
	return false, 0
}
