package cicontract

const (
	// LocalWorkloadPassEnvironmentSchemaVersion is intentionally independent of
	// remote-workload-pass-environment/v10.  A host run must never reuse an ECI
	// environment merely because the workload command is identical.
	LocalWorkloadPassEnvironmentSchemaVersion = "local-workload-pass-environment/v1"
	LocalWorkloadPassEnvironmentDomain        = "local-canonical-runner/v1"
	LocalWorkloadPassHostContextDomain        = "local-host-context/v1"
	LocalWorkloadRunnerSemanticPolicy         = "local-canonical-runner-semantic-policy/v1"
	LocalExecutorSessionReceiptSchemaVersion  = "local-executor-session-receipt/v1"
	LocalExecutorSessionReceiptDomain         = "local-executor-session-receipt/v1"
	LocalRunnerSourceClosureDomain            = "local-runner-source-closure/v4"
	LocalRunnerSelfSemanticDomain             = "local-runner-self-semantic/v1"
	LocalToolchainClosureDomain               = "local-toolchain-closure/v2"
	LocalDependencyContentDomain              = "local-dependency-content/v1"
	LocalFrontendRuntimeContentDomain         = "frontend-runtime-content/v1"
	LocalDependencyClosureDomain              = "local-dependency-closure/v1"
	LocalRunnerSemanticDigestDomain           = "local-runner-semantic/v1"
	LocalRunnerSandboxPolicy                  = "sandbox-exec-network-off/v1"
	LocalWorkloadPassNamespace                = "local"
	RemoteWorkloadPassNamespace               = "remote"

	// LocalAutoMissCountLimit and LocalAutoDurationLimitMS are the frozen
	// aggregate soft budget for the default auto target.  Explicit local may
	// override this scale budget, while hard host/sandbox gates remain active.
	LocalAutoMissCountLimit        int64 = 64
	LocalAutoDurationLimitMS       int64 = 10 * 60 * 1000
	LocalAutoSingleDurationLimitMS int64 = 5 * 60 * 1000
	LocalHostCPUWindowMS           int64 = 30 * 1000
	LocalHostMinimumCPUSamples     int64 = 7
	LocalHostCPUBusyLimitPercent         = 70.0
)
