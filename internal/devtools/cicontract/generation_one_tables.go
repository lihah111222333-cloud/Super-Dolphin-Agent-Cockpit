package cicontract

const (
	// DurationCalibrationsTable 是校准摘要 singleton 的唯一 SQLite 权威表。
	DurationCalibrationsTable = "duration_calibrations"
)

// GenerationOneAuthoritySupportingTables 返回首代 bootstrap 前必须为空的非历史根表。
// 七个历史根由 RetentionRootBindings 单独拥有；这些表只阻止残留的 singleton、catalog
// 或 live warning 越过 schema-only 首代边界，不参与三代 compactor。
func GenerationOneAuthoritySupportingTables() []string {
	return []string{
		DurationCalibrationsTable,
		WorkloadCatalogsTable,
		LiveTimingWarningsTable,
	}
}
