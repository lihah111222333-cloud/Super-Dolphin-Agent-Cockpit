package heteroci

// PackingPlanSchemaVersion 是异构 CI 装箱规划 wire schema 的冻结版本。
const PackingPlanSchemaVersion = "heteroci-packing-plan/v2"

// ExecutionWave 描述同一执行波次中的工作单元与资源估计。
type ExecutionWave struct {
	// Wave 是执行波次的稳定序号。
	Wave int `json:"wave"`
	// UnitIDs 是本波次包含的工作单元标识。
	UnitIDs []string `json:"unit_ids"`
	// CPUMilli 是本波次所需的 CPU 毫核数。
	CPUMilli int64 `json:"cpu_milli"`
	// MemoryBytes 是本波次所需的内存字节数。
	MemoryBytes int64 `json:"memory_bytes"`
	// Concurrency 是本波次的并发度。
	Concurrency int `json:"concurrency"`
	// EstimatedDurationMS 是本波次的估计耗时毫秒数。
	EstimatedDurationMS int64 `json:"estimated_duration_ms"`
}

// NodeShard 描述一个节点上的有序执行波次及其估计关键路径。
type NodeShard struct {
	// NodeID 是承载该分片的节点标识。
	NodeID string `json:"node_id"`
	// Waves 是该节点上按波次顺序执行的规划项。
	Waves []ExecutionWave `json:"waves"`
	// EstimatedMakespanMS 是该节点分片的估计总关键路径毫秒数。
	EstimatedMakespanMS int64 `json:"estimated_makespan_ms"`
}

// PackingPlan 描述异构 CI 工作单元到节点分片的冻结装箱规划。
type PackingPlan struct {
	// SchemaVersion 是本规划使用的 wire schema 版本。
	SchemaVersion string `json:"schema_version"`
	// Authority 是生成该规划的权威标识。
	Authority string `json:"authority"`
	// SnapshotCapturedAtUnixMilli 是规划快照采集时刻的 Unix 毫秒时间戳。
	SnapshotCapturedAtUnixMilli int64 `json:"snapshot_captured_at_unix_milli"`
	// NodeShards 是节点分片规划列表。
	NodeShards []NodeShard `json:"node_shards"`
	// UnassignedUnitIDs 是未能分配到节点分片的工作单元标识列表。
	UnassignedUnitIDs []string `json:"unassigned_unit_ids"`
}
