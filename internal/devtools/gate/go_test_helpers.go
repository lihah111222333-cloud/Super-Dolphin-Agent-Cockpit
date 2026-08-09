package gate

import "slices"

// CanonicalGoTestHelperTargets 返回显式子进程入口的稳定副本。
// 这些入口只服务于测试进程内部的 exec.Command，不得进入默认 inventory、
// compile group 或普通/race executor。
func CanonicalGoTestHelperTargets() []GoTestTarget {
	return slices.Clone([]GoTestTarget{
		{Package: AtomicCodexAppPackageTarget, Name: "TestCodexHelperProcess"},
		{Package: AtomicUpdaterPackageTarget, Name: "TestProbationCandidateProcess"},
		{Package: AtomicTaskDAGPackageTarget, Name: "TestWakeupSQLiteClaimChildProcess"},
		{Package: AtomicGatePackageTarget, Name: "TestCompileGroup"},
		{Package: AtomicGatePackageTarget, Name: "TestCompileGroupSecond"},
		{Package: AtomicGatePackageTarget, Name: "TestGoBuildCacheProxyHelper"},
		{Package: AtomicMcpLSPPackageTarget, Name: "TestFakePyrightLangserverHelper"},
		{Package: AtomicMcpLSPPackageTarget, Name: "TestFakeJSTSLangserverHelper"},
		{Package: AtomicMcpLSPPackageTarget, Name: "TestFakeJDTLSLangserverHelper"},
		{Package: AtomicMcpLSPPackageTarget, Name: "TestFakeGoplsShutdownWarningHelper"},
		{Package: AtomicMcpLSPPackageTarget, Name: "TestFakeBashLanguageServerHelper"},
		{Package: AtomicMcpLSPPackageTarget, Name: "TestFakeMultilangDiagnosticsLangserverHelper"},
		{Package: AtomicMcpLSPPackageTarget, Name: "TestResourceCohortE2ELanguageServerHelper"},
		{Package: AtomicSuperDolphinGatePackageTarget, Name: "TestRemoteHookConcurrentProcessHelper"},
		{Package: AtomicSQLitePackageTarget, Name: "TestSQLiteMixedWritePressureChild"},
	})
}

// IsCanonicalGoTestHelper 报告 exact Go test target 是否是受控子进程入口。
func IsCanonicalGoTestHelper(target GoTestTarget) bool {
	for _, helper := range CanonicalGoTestHelperTargets() {
		if helper == target {
			return true
		}
	}
	return false
}
