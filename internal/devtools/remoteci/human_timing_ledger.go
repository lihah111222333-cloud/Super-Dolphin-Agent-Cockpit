package remoteci

import (
	"fmt"
	"io"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// RenderHumanTimingLedgerFromAuthority 只投影已提交 SQLite authority 的结构化耗时与缓存证据。
func RenderHumanTimingLedgerFromAuthority(destination io.Writer, store *gate.DurationLedgerStore, jobID string) error {
	if destination == nil {
		return fmt.Errorf("human timing ledger destination is required")
	}
	if store == nil {
		return fmt.Errorf("human timing ledger SQLite authority is required")
	}
	record, err := store.LoadRemoteCIRun(jobID)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(destination, "REMOTE_CI_TIMING_LEDGER job_id=%s status=%s\n", record.JobID, record.Status); err != nil {
		return err
	}
	for _, observation := range record.TimingObservations {
		if err := renderAuthorityTimingObservation(destination, observation); err != nil {
			return err
		}
	}
	return nil
}

func renderAuthorityTimingObservation(destination io.Writer, observation gate.TimingObservation) error {
	goEvidence := observation.CacheEvidence.Go
	frontend := observation.CacheEvidence.Frontend
	if observation.Measurement == "measured" {
		_, err := fmt.Fprintf(destination, "REMOTE_CI_TIMING_OBSERVATION scope=%s shard=%s workload=%s phase=%s measurement=%s started_at=%s completed_at=%s duration_ms=%d aggregation=%s go_cache_source=%s go_cache_status=%s go_cache_measurement=%s go_cache_private_hits=%d go_cache_baseline_hits=%d go_cache_misses=%d go_cache_puts=%d go_cache_not_applicable_reason=%s frontend_node_modules_seed_status=%s frontend_node_modules_seed_not_applicable_reason=%s frontend_npm_status=%s frontend_npm_not_applicable_reason=%s frontend_vite_status=%s frontend_vite_not_applicable_reason=%s frontend_playwright_status=%s frontend_playwright_not_applicable_reason=%s\n", observation.Scope, observation.ShardIdentity, observation.WorkloadID, observation.Phase, observation.Measurement, observation.StartedAt.UTC().Format(time.RFC3339Nano), observation.CompletedAt.UTC().Format(time.RFC3339Nano), observation.DurationMS, observation.Aggregation, goEvidence.Source, goEvidence.Status, goEvidence.Measurement, goEvidence.PrivateHits, goEvidence.BaselineHits, goEvidence.Misses, goEvidence.Puts, goEvidence.NotApplicableReason, frontend.NodeModulesSeed.Status, frontend.NodeModulesSeed.NotApplicableReason, frontend.NPM.Status, frontend.NPM.NotApplicableReason, frontend.Vite.Status, frontend.Vite.NotApplicableReason, frontend.Playwright.Status, frontend.Playwright.NotApplicableReason)
		return err
	}
	_, err := fmt.Fprintf(destination, "REMOTE_CI_TIMING_OBSERVATION scope=%s shard=%s workload=%s phase=%s measurement=not_applicable reason=%s duration_ms=0 aggregation=%s go_cache_source=%s go_cache_status=%s go_cache_measurement=%s go_cache_private_hits=%d go_cache_baseline_hits=%d go_cache_misses=%d go_cache_puts=%d go_cache_not_applicable_reason=%s frontend_node_modules_seed_status=%s frontend_node_modules_seed_not_applicable_reason=%s frontend_npm_status=%s frontend_npm_not_applicable_reason=%s frontend_vite_status=%s frontend_vite_not_applicable_reason=%s frontend_playwright_status=%s frontend_playwright_not_applicable_reason=%s\n", observation.Scope, observation.ShardIdentity, observation.WorkloadID, observation.Phase, observation.Reason, observation.Aggregation, goEvidence.Source, goEvidence.Status, goEvidence.Measurement, goEvidence.PrivateHits, goEvidence.BaselineHits, goEvidence.Misses, goEvidence.Puts, goEvidence.NotApplicableReason, frontend.NodeModulesSeed.Status, frontend.NodeModulesSeed.NotApplicableReason, frontend.NPM.Status, frontend.NPM.NotApplicableReason, frontend.Vite.Status, frontend.Vite.NotApplicableReason, frontend.Playwright.Status, frontend.Playwright.NotApplicableReason)
	return err
}
