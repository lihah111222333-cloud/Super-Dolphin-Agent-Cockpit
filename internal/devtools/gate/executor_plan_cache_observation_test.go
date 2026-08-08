package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPlanGateCacheMetricsTreatsUnstartedProxyAsNotApplicable(t *testing.T) {
	cacheRoot := t.TempDir()
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(cacheRoot, "unused-go-seed")
	if err != nil {
		t.Fatal(err)
	}
	result := PlanGateExecution{ExecutionProfile: measuredNonCacheExecutionProfile()}
	if err := applyPlanGateCacheMetrics(&result, cacheRoot, metricsPath, ExecutorOCIProjectGoBuildCacheSeedRoot); err != nil {
		t.Fatalf("apply absent Go cache metrics: %v", err)
	}
	if result.ExecutionProfile.CacheSource != "none" ||
		result.ExecutionProfile.CacheStatus != CacheObservationNotApplicable ||
		result.ExecutionProfile.CacheMeasurement != "measured" {
		t.Fatalf("absent Go cache metrics profile = %+v, want not applicable", result.ExecutionProfile)
	}
}

func TestApplyPlanGateCacheMetricsRejectsExistingInvalidObservation(t *testing.T) {
	cacheRoot := t.TempDir()
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(cacheRoot, "invalid-observation")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metricsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := PlanGateExecution{ExecutionProfile: measuredNonCacheExecutionProfile()}
	if err := applyPlanGateCacheMetrics(&result, cacheRoot, metricsPath, ExecutorOCIProjectGoBuildCacheSeedRoot); err == nil {
		t.Fatal("existing invalid Go cache metrics unexpectedly accepted")
	}
}

func TestApplyPlanGateCacheMetricsRejectsStartedProxyWithoutFinalMetrics(t *testing.T) {
	cacheRoot := t.TempDir()
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(cacheRoot, "started-without-metrics")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metricsPath+goBuildCacheProxyStartedFileSuffix, []byte("started\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := PlanGateExecution{ExecutionProfile: measuredNonCacheExecutionProfile()}
	if err := applyPlanGateCacheMetrics(&result, cacheRoot, metricsPath, ExecutorOCIProjectGoBuildCacheSeedRoot); err == nil ||
		!strings.Contains(err.Error(), "started but final metrics are missing") {
		t.Fatalf("started proxy without final metrics error = %v", err)
	}
}

func TestApplyPlanGateCacheMetricsRejectsFinalMetricsWithRetainedMarker(t *testing.T) {
	cacheRoot := t.TempDir()
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(cacheRoot, "metrics-with-marker")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeGoBuildCacheProxyMetrics(metricsPath, newGoBuildCacheProxyMetrics(ExecutorOCIProjectGoBuildCacheSeedRoot)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metricsPath+goBuildCacheProxyStartedFileSuffix, []byte("started\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := PlanGateExecution{ExecutionProfile: measuredNonCacheExecutionProfile()}
	if err := applyPlanGateCacheMetrics(&result, cacheRoot, metricsPath, ExecutorOCIProjectGoBuildCacheSeedRoot); err == nil ||
		!strings.Contains(err.Error(), "retained the started marker") {
		t.Fatalf("final metrics with retained marker error = %v", err)
	}
}

func TestApplyPlanGateCacheMetricsRejectsRetainedHelperMarker(t *testing.T) {
	cacheRoot := t.TempDir()
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(cacheRoot, "helper-marker")
	if err != nil {
		t.Fatal(err)
	}
	markerPath, err := createGoBuildCacheProxyStartedMarker(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(markerPath); err != nil {
			t.Errorf("remove helper marker fixture: %v", err)
		}
	})
	if err := writeGoBuildCacheProxyMetrics(metricsPath, newGoBuildCacheProxyMetrics(ExecutorOCIProjectGoBuildCacheSeedRoot)); err != nil {
		t.Fatal(err)
	}
	result := PlanGateExecution{ExecutionProfile: measuredNonCacheExecutionProfile()}
	if err := applyPlanGateCacheMetrics(&result, cacheRoot, metricsPath, ExecutorOCIProjectGoBuildCacheSeedRoot); err == nil ||
		!strings.Contains(err.Error(), "retained the started marker") {
		t.Fatalf("final metrics with helper marker error = %v", err)
	}
}

func TestApplyPlanGateCacheMetricsFinalizesAndConsumesHelperContributions(t *testing.T) {
	cacheRoot := t.TempDir()
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(cacheRoot, "helper-contributions")
	if err != nil {
		t.Fatal(err)
	}
	part := newGoBuildCacheProxyMetrics(ExecutorOCIProjectGoBuildCacheSeedRoot)
	part.PrivateHitCount, part.MissCount, part.PutCount = 2, 3, 1
	contributionPath := filepath.Join(cacheRoot, filepath.Base(metricsPath)+".helper-test.json")
	if err := writeGoBuildCacheProxyMetrics(contributionPath, part); err != nil {
		t.Fatal(err)
	}
	result := PlanGateExecution{ExecutionProfile: measuredNonCacheExecutionProfile()}
	if err := applyPlanGateCacheMetrics(&result, cacheRoot, metricsPath, ExecutorOCIProjectGoBuildCacheSeedRoot); err != nil {
		t.Fatalf("finalize helper contributions: %v", err)
	}
	if result.ExecutionProfile.PrivateHitCount != 2 || result.ExecutionProfile.CacheMissCount != 3 || result.ExecutionProfile.CachePutCount != 1 {
		t.Fatalf("finalized cache profile = %+v", result.ExecutionProfile)
	}
	if _, err := os.Stat(contributionPath); !os.IsNotExist(err) {
		t.Fatalf("helper contribution remained after apply: %v", err)
	}
}
