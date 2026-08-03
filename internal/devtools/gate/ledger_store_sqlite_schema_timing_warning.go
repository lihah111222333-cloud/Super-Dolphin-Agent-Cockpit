package gate

import (
	"errors"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const durationLedgerLiveTimingWarningTableSchema = `
CREATE TABLE IF NOT EXISTS ci_live_timing_warnings (
	job_id TEXT NOT NULL CHECK (job_id <> '' AND job_id = trim(job_id)),
	agent_token_digest TEXT NOT NULL CHECK (length(agent_token_digest) = 71 AND substr(agent_token_digest, 1, 7) = 'sha256:' AND substr(agent_token_digest, 8) NOT GLOB '*[^0-9a-f]*'),
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	scope TEXT NOT NULL CHECK (scope = 'shard'),
	shard_identity TEXT NOT NULL CHECK (shard_identity <> '' AND shard_identity = trim(shard_identity)),
	workload_id TEXT NOT NULL DEFAULT '' CHECK (workload_id = ''),
	evidence_kind TEXT NOT NULL CHECK (evidence_kind = 'running'),
	action TEXT NOT NULL CHECK (action = 'warn_and_continue'),
	evidence_started_at_unix_ms INTEGER NOT NULL CHECK (evidence_started_at_unix_ms > 0 AND evidence_started_at_unix_ms <= 9223372036854675807),
	observed_at_unix_ms INTEGER NOT NULL CHECK (observed_at_unix_ms > 0),
	evidence_duration_ms INTEGER NOT NULL CHECK (evidence_duration_ms >= 100000),
	target_ms INTEGER NOT NULL CHECK (target_ms = 100000),
	warning_text TEXT NOT NULL CHECK (length(trim(warning_text)) > 0),
	CHECK (observed_at_unix_ms >= evidence_started_at_unix_ms + target_ms),
	CHECK (evidence_duration_ms = observed_at_unix_ms - evidence_started_at_unix_ms),
	PRIMARY KEY (job_id, scope, shard_identity, workload_id, evidence_kind, target_ms)
)`

const durationLedgerRunTimingWarningTableSchema = `
CREATE TABLE IF NOT EXISTS ci_run_timing_warnings (
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE CHECK (job_id <> '' AND job_id = trim(job_id)),
	agent_token_digest TEXT NOT NULL CHECK (length(agent_token_digest) = 71 AND substr(agent_token_digest, 1, 7) = 'sha256:' AND substr(agent_token_digest, 8) NOT GLOB '*[^0-9a-f]*'),
	accepted_generation TEXT NOT NULL CHECK (accepted_generation <> '' AND accepted_generation NOT GLOB '0*' AND accepted_generation NOT GLOB '*[^0-9]*' AND (length(accepted_generation) < 20 OR (length(accepted_generation) = 20 AND accepted_generation <= '18446744073709551615'))),
	scope TEXT NOT NULL CHECK (scope IN ('shard', 'workload')),
	shard_identity TEXT NOT NULL DEFAULT '',
	workload_id TEXT NOT NULL DEFAULT '',
	evidence_kind TEXT NOT NULL CHECK (evidence_kind IN ('running', 'test_body', 'total')),
	action TEXT NOT NULL CHECK (action = 'warn_and_continue'),
	evidence_started_at_unix_ms INTEGER NOT NULL CHECK (evidence_started_at_unix_ms > 0 AND evidence_started_at_unix_ms <= 9223372036854675807),
	observed_at_unix_ms INTEGER NOT NULL CHECK (observed_at_unix_ms > 0),
	evidence_duration_ms INTEGER NOT NULL CHECK (evidence_duration_ms >= 100000),
	target_ms INTEGER NOT NULL CHECK (target_ms = 100000),
	warning_text TEXT NOT NULL CHECK (length(trim(warning_text)) > 0),
	CHECK ((scope = 'shard' AND evidence_kind = 'running' AND shard_identity <> '' AND shard_identity = trim(shard_identity) AND workload_id = '' AND evidence_duration_ms = observed_at_unix_ms - evidence_started_at_unix_ms AND observed_at_unix_ms >= evidence_started_at_unix_ms + target_ms) OR
		(scope = 'workload' AND evidence_kind IN ('test_body', 'total') AND shard_identity <> '' AND shard_identity = trim(shard_identity) AND workload_id <> '' AND workload_id = trim(workload_id) AND evidence_duration_ms > target_ms AND observed_at_unix_ms > evidence_started_at_unix_ms)),
	PRIMARY KEY (job_id, scope, shard_identity, workload_id, evidence_kind, target_ms)
)`

const durationLedgerLiveTimingWarningIndexSchema = `CREATE INDEX IF NOT EXISTS idx_ci_live_timing_warnings_generation
	ON ci_live_timing_warnings (accepted_generation, job_id)`

const durationLedgerRunTimingWarningIndexSchema = `CREATE INDEX IF NOT EXISTS idx_ci_run_timing_warnings_generation
	ON ci_run_timing_warnings (accepted_generation, job_id)`

func normalizeSQLiteDDL(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "if not exists", "")
	value = strings.TrimSuffix(value, ";")
	return strings.Join(strings.Fields(value), "")
}

func validateRemoteCITimingWarningTableName(table string) error {
	if table != cicontract.LiveTimingWarningsTable && table != cicontract.RunTimingWarningsTable {
		return errors.New("remote CI timing warning table is not canonical")
	}
	return nil
}
