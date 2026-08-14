//go:build windows

package multilsp

// WindowsGoplsRootRSSLimitBytes 严格读取 Windows gopls root Job 的唯一 RSS 上限。
func WindowsGoplsRootRSSLimitBytes() (uint64, error) {
	value, configured, err := strictRSSLimitBytesFromEnv(lspGoRSSLimitEnv)
	if err != nil {
		return 0, err
	}
	if configured {
		return value, nil
	}
	return defaultGoWindowsRSSLimitBytes, nil
}

// refreshStaleResourceCohortRSS 在 Windows 上按创建期进程上限保守计账。
// 远端 owner 的 Job Object 句柄不可重建，因此不伪造跨进程实时采样。
func refreshStaleResourceCohortRSS(member resourceCohortMember) (uint64, error) {
	return max(member.RSSBytes, member.ProcessRSSLimitBytes), nil
}
