package remoteci

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const timingNotMeasured = "not_measured"

// RenderHumanTimingLedger 将最终 remote CI timing evidence 稳定投影为供人阅读的账本。
// 它只读取 RunResult，因此既不改变采集，也不推断缺失观测。
func RenderHumanTimingLedger(destination io.Writer, result RunResult) error {
	if destination == nil {
		return fmt.Errorf("human timing ledger destination is required")
	}
	if _, err := fmt.Fprintf(destination, "REMOTE_CI_TIMING_LEDGER job_id=%s status=%s\n", result.JobID, result.Status); err != nil {
		return err
	}
	if err := renderPackageTimingLedger(destination, result.CandidateTestBinaryBuilds); err != nil {
		return err
	}
	if err := renderTestTimingLedger(destination, result.GateExecutions); err != nil {
		return err
	}
	return renderShardTimingLedger(destination, result.Shards)
}

func renderPackageTimingLedger(destination io.Writer, builds []CandidateTestBinaryBuilderBuild) error {
	if len(builds) == 0 {
		_, err := fmt.Fprintln(destination, "REMOTE_CI_TIMING_LEDGER package=not_measured build_wall_ms=not_measured go_list_wall_ms=not_measured compile_action_ms=not_measured link_action_ms=not_measured compile_critical_wall_ms=not_measured cache_private_hits=not_measured cache_baseline_generation_hits=not_measured cache_misses=not_measured cache_puts=not_measured")
		return err
	}
	ordered := append([]CandidateTestBinaryBuilderBuild(nil), builds...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].Artifact.Package != ordered[right].Artifact.Package {
			return ordered[left].Artifact.Package < ordered[right].Artifact.Package
		}
		if ordered[left].Artifact.Mode != ordered[right].Artifact.Mode {
			return ordered[left].Artifact.Mode < ordered[right].Artifact.Mode
		}
		if ordered[left].Artifact.ManifestKey != ordered[right].Artifact.ManifestKey {
			return ordered[left].Artifact.ManifestKey < ordered[right].Artifact.ManifestKey
		}
		return ordered[left].Artifact.BinaryKey < ordered[right].Artifact.BinaryKey
	})
	for _, build := range ordered {
		metrics := build.Metrics
		if _, err := fmt.Fprintf(destination, "REMOTE_CI_TIMING_LEDGER package=%s mode=%s go_list_wall_ms=%d build_wall_ms=%d compile_action_ms=%d link_action_ms=%d compile_critical_wall_ms=%d cache_private_hits=%d cache_baseline_generation_hits=%s cache_misses=%d cache_puts=%d\n", build.Artifact.Package, build.Artifact.Mode, metrics.GoListWallMS, metrics.BuildWallMS, metrics.CompileActionMS, metrics.LinkActionMS, metrics.CompileCriticalWallMS, metrics.GOCachePrivateHits, formatBaselineGenerationHits(metrics.GOCacheBaselineHitsByGeneration), metrics.GOCacheMisses, metrics.GOCachePuts); err != nil {
			return err
		}
	}
	return nil
}

func formatBaselineGenerationHits(hits []CandidateTestBinaryCacheGenerationHit) string {
	if len(hits) == 0 {
		return timingNotMeasured
	}
	ordered := append([]CandidateTestBinaryCacheGenerationHit(nil), hits...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].Generation != ordered[right].Generation {
			return ordered[left].Generation < ordered[right].Generation
		}
		if ordered[left].AnchorGeneration != ordered[right].AnchorGeneration {
			return ordered[left].AnchorGeneration < ordered[right].AnchorGeneration
		}
		return ordered[left].ManifestDigest < ordered[right].ManifestDigest
	})
	values := make([]string, 0, len(ordered))
	for _, hit := range ordered {
		values = append(values, strconv.FormatUint(hit.Generation, 10)+":"+strconv.FormatUint(hit.Hits, 10))
	}
	return strings.Join(values, ",")
}

func renderTestTimingLedger(destination io.Writer, executions []gate.PlanGateExecution) error {
	if len(executions) == 0 {
		_, err := fmt.Fprintln(destination, "REMOTE_CI_TIMING_LEDGER test=not_measured test_body_ms=not_measured startup_ms=not_measured total_ms=not_measured")
		return err
	}
	ordered := append([]gate.PlanGateExecution(nil), executions...)
	sort.SliceStable(ordered, func(left, right int) bool { return ordered[left].GateID < ordered[right].GateID })
	for _, execution := range ordered {
		profile := execution.ExecutionProfile
		if _, err := fmt.Fprintf(destination, "REMOTE_CI_TIMING_LEDGER test=%s test_body_ms=%d startup_ms=%d total_ms=%d\n", execution.GateID, profile.TestBodyMS, profile.StartupMS, profile.TotalMS); err != nil {
			return err
		}
		if profile.Frontend != nil {
			frontend := profile.Frontend
			if _, err := fmt.Fprintf(destination, "REMOTE_CI_TIMING_LEDGER test=%s node_modules_seed_hit=%t node_modules_seed_not_applicable_reason=%s npm_cache_hit=%t npm_cache_not_applicable_reason=%s playwright_browser_hit=%t playwright_browser_not_applicable_reason=%s vite_cache_hit=%t vite_cache_not_applicable_reason=%s setup_ms=%d body_ms=%d total_ms=%d\n", execution.GateID, frontend.NodeModulesSeedHit, frontend.NodeModulesSeedNotApplicableReason, frontend.NPMCacheHit, frontend.NPMCacheNotApplicableReason, frontend.PlaywrightBrowserHit, frontend.PlaywrightBrowserNotApplicableReason, frontend.ViteCacheHit, frontend.ViteCacheNotApplicableReason, frontend.SetupMS, frontend.BodyMS, frontend.TotalMS); err != nil {
				return err
			}
		}
		if err := renderIndividualTestTimings(destination, execution); err != nil {
			return err
		}
	}
	return nil
}

func renderIndividualTestTimings(destination io.Writer, execution gate.PlanGateExecution) error {
	timings := append([]gate.GoTestTiming(nil), execution.TestTimings...)
	sort.SliceStable(timings, func(left, right int) bool { return timings[left].Name < timings[right].Name })
	for _, timing := range timings {
		if _, err := fmt.Fprintf(destination, "REMOTE_CI_TIMING_LEDGER test=%s test_name=%s test_duration_ms=%d status=%s\n", execution.GateID, timing.Name, timing.DurationMS, timing.Status); err != nil {
			return err
		}
	}
	return nil
}

func renderShardTimingLedger(destination io.Writer, shards []ShardResult) error {
	if len(shards) == 0 {
		_, err := fmt.Fprintln(destination, "REMOTE_CI_TIMING_LEDGER shard=not_measured artifact=not_measured download_ms=not_measured verify_ms=not_measured install_ms=not_measured materialize_ms=not_measured")
		return err
	}
	ordered := append([]ShardResult(nil), shards...)
	sort.SliceStable(ordered, func(left, right int) bool { return ordered[left].ShardIdentity < ordered[right].ShardIdentity })
	for _, shard := range ordered {
		if shard.MaterializationTiming.ShardIdentity == "" {
			if _, err := fmt.Fprintf(destination, "REMOTE_CI_TIMING_LEDGER shard=%s artifact=not_measured download_ms=not_measured verify_ms=not_measured install_ms=not_measured materialize_ms=not_measured\n", shard.ShardIdentity); err != nil {
				return err
			}
			continue
		}
		for _, artifact := range []struct {
			name   string
			timing gate.MaterializationPhaseTiming
		}{
			{name: "source", timing: shard.MaterializationTiming.Source},
			{name: "baseline", timing: shard.MaterializationTiming.Baseline},
			{name: "candidate_cli", timing: shard.MaterializationTiming.CandidateCLI},
			{name: "candidate_test_binaries", timing: shard.MaterializationTiming.CandidateTestBinaries},
		} {
			if _, err := fmt.Fprintf(destination, "REMOTE_CI_TIMING_LEDGER shard=%s artifact=%s download_ms=%d verify_ms=%d install_ms=%d materialize_ms=%d\n", shard.ShardIdentity, artifact.name, artifact.timing.DownloadMS, artifact.timing.VerifyMS, artifact.timing.InstallMS, artifact.timing.MaterializeMS); err != nil {
				return err
			}
		}
	}
	return nil
}
