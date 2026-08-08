package gate

// 终态证据采用规范化表，使 SQLite projection 保持唯一查询来源；禁止增加 terminal_evidence_json 列。
const durationLedgerRemoteCITerminalContainersTableSchema = `
CREATE TABLE IF NOT EXISTS ci_shard_terminal_containers (
	job_id TEXT NOT NULL,
	shard_identity TEXT NOT NULL,
	container_kind TEXT NOT NULL CHECK (container_kind IN ('container', 'init')),
	ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
	name TEXT NOT NULL CHECK (length(trim(name)) > 0),
	state TEXT NOT NULL,
	exit_code INTEGER,
	reason TEXT NOT NULL,
	message TEXT NOT NULL,
	PRIMARY KEY (job_id, shard_identity, container_kind, ordinal),
	FOREIGN KEY (job_id, shard_identity)
		REFERENCES ci_shards(job_id, shard_identity) ON DELETE CASCADE
);`

const durationLedgerRemoteCITerminalEventsTableSchema = `
CREATE TABLE IF NOT EXISTS ci_shard_terminal_events (
	job_id TEXT NOT NULL,
	shard_identity TEXT NOT NULL,
	ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
	type TEXT NOT NULL,
	reason TEXT NOT NULL,
	message TEXT NOT NULL,
	count INTEGER NOT NULL CHECK (count >= 0),
	last_timestamp TEXT NOT NULL,
	PRIMARY KEY (job_id, shard_identity, ordinal),
	FOREIGN KEY (job_id, shard_identity)
		REFERENCES ci_shards(job_id, shard_identity) ON DELETE CASCADE
);`

func durationLedgerRemoteCITerminalEvidenceSchemaStatements() []string {
	return []string{
		durationLedgerRemoteCITerminalContainersTableSchema,
		durationLedgerRemoteCITerminalEventsTableSchema,
	}
}
