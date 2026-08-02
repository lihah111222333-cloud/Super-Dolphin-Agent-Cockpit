package remoteci

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestRenderHumanTimingLedgerFromAuthorityRejectsMissingAuthority 防止 renderer 从内存结果回退。
func TestRenderHumanTimingLedgerFromAuthorityRejectsMissingAuthority(t *testing.T) {
	if err := RenderHumanTimingLedgerFromAuthority(io.Discard, nil, "job-1"); err == nil {
		t.Fatal("missing SQLite authority was accepted")
	}
	store, err := gate.NewDurationLedgerStore(t.TempDir() + "/duration-ledger.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	var destination strings.Builder
	if err := RenderHumanTimingLedgerFromAuthority(&destination, store, "missing"); err == nil {
		t.Fatal("missing authority record was rendered")
	}
}

// TestRenderAuthorityTimingObservationRendersStructuredCacheEvidence 验证 authority 投影不丢失缓存计数或前端状态。
func TestRenderAuthorityTimingObservationRendersStructuredCacheEvidence(t *testing.T) {
	profile := gate.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: gate.CacheObservationPut, CacheMeasurement: "measured", CachePutCount: 7, Frontend: &gate.FrontendExecutionProfile{NodeModulesSeedHit: true, NPMCacheHit: true, ViteCacheHit: false, PlaywrightBrowserNotApplicableReason: "not_e2e_workload"}}
	startedAt := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	observation := gate.TimingObservation{JobID: "job-1", Scope: cicontract.TimingScopeWorkload, ShardIdentity: "shard-1", WorkloadID: "frontend", Phase: cicontract.TimingTestBody, StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second), DurationMS: 1_000, Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: gate.NewTimingCacheEvidenceFromProfile(profile)}
	var destination strings.Builder
	if err := renderAuthorityTimingObservation(&destination, observation); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"duration_ms=1000", "go_cache_source=go_build_cache", "go_cache_status=put", "go_cache_private_hits=0", "go_cache_baseline_hits=0", "go_cache_misses=0", "go_cache_puts=7", "frontend_node_modules_seed_status=hit", "frontend_npm_status=hit", "frontend_vite_status=miss", "frontend_playwright_status=not_applicable"} {
		if !strings.Contains(destination.String(), want) {
			t.Fatalf("renderer omitted %q: %s", want, destination.String())
		}
	}
}
