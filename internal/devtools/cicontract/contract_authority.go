package cicontract

import "fmt"

func sqlAuthorityBindingList() []SQLAuthorityBinding {
	return []SQLAuthorityBinding{
		{Domain: SQLDomainAcceptedBaseline, Table: AcceptedBaselineTable},
		{Domain: SQLDomainDurationHistory, Table: DurationSamplesTable},
		{Domain: SQLDomainShardOverhead, Table: DurationShardOverheadsTable},
		{Domain: SQLDomainShardOverheadSample, Table: DurationShardOverheadSamplesTable},
		{Domain: SQLDomainRemoteRun, Table: RemoteRunsTable},
		{Domain: SQLDomainRemoteShard, Table: RemoteShardsTable},
		{Domain: SQLDomainWorkloadExecution, Table: WorkloadExecutionsTable},
		{Domain: SQLDomainRunWarning, Table: RunWarningsTable},
		{Domain: SQLDomainLiveTimingWarning, Table: LiveTimingWarningsTable},
		{Domain: SQLDomainRunTimingWarning, Table: RunTimingWarningsTable},
		{Domain: SQLDomainCalibrationCheckpoint, Table: CalibrationCheckpointsTable},
		{Domain: SQLDomainCheckReceipt, Table: CheckReceiptsTable},
		{Domain: SQLDomainTimingObservation, Table: TimingObservationsTable},
		{Domain: SQLDomainCompileTiming, Table: CompileTimingObservationsTable},
		{Domain: SQLDomainWorkloadCatalog, Table: WorkloadCatalogsTable},
		{Domain: SQLDomainCatalogObservation, Table: CatalogObservationsTable},
		{Domain: SQLDomainCatalogWorkload, Table: CatalogWorkloadsTable},
		{Domain: SQLDomainRunAgentIdentity, Table: RunAgentIdentitiesTable},
		{Domain: SQLDomainShardWorkload, Table: ShardWorkloadsTable},
		{Domain: SQLDomainGateExecution, Table: GateExecutionsTable},
		{Domain: SQLDomainRunWorkloadResult, Table: RunWorkloadResultsTable},
		{Domain: SQLDomainWorkloadPassEvidence, Table: WorkloadPassEvidenceTable},
		{Domain: SQLDomainCalibrationScenario, Table: CalibrationCheckpointScenariosTable},
	}
}

func retentionRootBindingList() []RetentionRootBinding {
	return []RetentionRootBinding{
		{Table: DurationSamplesTable, GenerationColumn: AcceptedGenerationColumn},
		{Table: DurationShardOverheadsTable, GenerationColumn: AcceptedGenerationColumn},
		{Table: DurationShardOverheadSamplesTable, GenerationColumn: AcceptedGenerationColumn},
		{Table: CatalogObservationsTable, GenerationColumn: AcceptedGenerationColumn},
		{Table: RemoteRunsTable, GenerationColumn: AcceptedGenerationColumn},
		{Table: WorkloadPassEvidenceTable, GenerationColumn: AcceptedGenerationColumn},
		{Table: CalibrationCheckpointsTable, GenerationColumn: AcceptedGenerationColumn},
	}
}

// SQLAuthorityBindings 返回所有远程 CI 持久化事实的唯一 SQL 表绑定。
func SQLAuthorityBindings() []SQLAuthorityBinding {
	return sqlAuthorityBindingList()
}

// RetentionRootBindings 返回必须由统一三代 compactor 管理的历史根副本。
func RetentionRootBindings() []RetentionRootBinding {
	return retentionRootBindingList()
}

// ValidateSQLAuthorityBinding 拒绝把一个事实域写入第二张表或非 SQL 真相源。
func ValidateSQLAuthorityBinding(domain SQLDomain, table string) error {
	for _, binding := range sqlAuthorityBindingList() {
		if binding.Domain == domain {
			if table != binding.Table {
				return fmt.Errorf("remote CI SQL domain %q must use table %q", domain, binding.Table)
			}
			return nil
		}
	}
	return fmt.Errorf("remote CI SQL domain %q is unsupported", domain)
}
