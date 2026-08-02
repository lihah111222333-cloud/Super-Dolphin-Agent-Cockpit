package main

import (
	"os"
	"path/filepath"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestReadCandidateTestBinaryActionGraphSeparatesActionSumAndCriticalWall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "action.json")
	data := `[{"Mode":"compile","TimeStart":"2026-08-02T00:00:00Z","TimeDone":"2026-08-02T00:00:10Z"},{"Mode":"compile","TimeStart":"2026-08-02T00:00:05Z","TimeDone":"2026-08-02T00:00:15Z"},{"Mode":"link","TimeStart":"2026-08-02T00:00:16Z","TimeDone":"2026-08-02T00:00:20Z"}]`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readCandidateTestBinaryActionGraph(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.compileActionMS != 20_000 || got.compileCriticalWallMS != 15_000 || got.linkActionMS != 4_000 {
		t.Fatalf("metrics=%+v", got)
	}
}

func TestMergeRemoteBuilderCacheMetricsAccumulatesOCIProjectCacheHits(t *testing.T) {
	left := gatecontract.GoBuildCacheProxyMetrics{PrivateHitCount: 2, BaselineHitCount: 7, MissCount: 3, PutCount: 5}
	right := gatecontract.GoBuildCacheProxyMetrics{PrivateHitCount: 11, BaselineHitCount: 19, MissCount: 13, PutCount: 17}
	got := mergeRemoteBuilderCacheMetrics(left, right)
	if got.PrivateHitCount != 13 || got.BaselineHitCount != 26 || got.MissCount != 16 || got.PutCount != 22 {
		t.Fatalf("metrics=%+v", got)
	}
}
