package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	goBuildCacheProxyMetricsSchemaVersion uint32 = 1
	goBuildCacheProxyMetricsFilePrefix           = ".super-dolphin-go-cache-metrics-"
	goBuildCacheProxyMetricsFileName             = goBuildCacheProxyMetricsFilePrefix + "default.json"
)

// GoBuildCacheProxyMetrics 是一个 Go worker 对共享 build-cache 的有界观察。
// 它只描述 GOCACHEPROG 协议请求，不能被当作 Go test-result cache 的证据。
type GoBuildCacheProxyMetrics struct {
	SchemaVersion           uint32            `json:"schema_version"`
	PrivateHitCount         uint64            `json:"private_hit_count"`
	BaselineHitCount        uint64            `json:"baseline_hit_count"`
	BaselineHitByGeneration map[string]uint64 `json:"baseline_hit_by_generation"`
	MissCount               uint64            `json:"miss_count"`
	PutCount                uint64            `json:"put_count"`
	SeedRoots               []string          `json:"seed_roots"`
}

func newGoBuildCacheProxyMetrics(seedRoots []string) GoBuildCacheProxyMetrics {
	metrics := GoBuildCacheProxyMetrics{
		SchemaVersion:           goBuildCacheProxyMetricsSchemaVersion,
		BaselineHitByGeneration: make(map[string]uint64),
		SeedRoots:               append([]string(nil), seedRoots...),
	}
	return metrics
}

func (metrics *GoBuildCacheProxyMetrics) recordHit(layer int) {
	if layer == 0 {
		metrics.PrivateHitCount++
		return
	}
	metrics.BaselineHitCount++
	generation := filepath.Base(metrics.SeedRoots[layer-1])
	metrics.BaselineHitByGeneration[generation]++
}

func (metrics *GoBuildCacheProxyMetrics) recordMiss() {
	metrics.MissCount++
}

func (metrics *GoBuildCacheProxyMetrics) recordPut() {
	metrics.PutCount++
}

// GoBuildCacheProxyMetricsPath 返回唯一允许 proxy 落盘观察结果的私有缓存文件。
func GoBuildCacheProxyMetricsPath(privateRoot string) string {
	return filepath.Join(privateRoot, goBuildCacheProxyMetricsFileName)
}

// GoBuildCacheProxyMetricsPathForInvocation 为共享私有 GOCACHE 中的一个 gate 生成不冲突的观察文件。
func GoBuildCacheProxyMetricsPathForInvocation(privateRoot string, invocation string) (string, error) {
	if invocation == "" || invocation != filepath.Base(invocation) || strings.ContainsAny(invocation, "\\/\x00\r\n") {
		return "", errors.New("Go build cache proxy metrics invocation is invalid")
	}
	return filepath.Join(privateRoot, goBuildCacheProxyMetricsFilePrefix+invocation+".json"), nil
}

func validGoBuildCacheProxyMetricsPath(privateRoot string, metricsPath string) bool {
	base := filepath.Base(metricsPath)
	return filepath.Dir(metricsPath) == privateRoot && strings.HasPrefix(base, goBuildCacheProxyMetricsFilePrefix) &&
		strings.HasSuffix(base, ".json") && base != goBuildCacheProxyMetricsFilePrefix+".json"
}

func writeGoBuildCacheProxyMetrics(path string, metrics GoBuildCacheProxyMetrics) error {
	if err := validateGoBuildCacheProxyMetrics(metrics); err != nil {
		return err
	}
	encoded, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	return writeAtomicGoBuildCacheFile(path, append(encoded, '\n'))
}

// LoadGoBuildCacheProxyMetrics 只接受由当前私有 GOCACHE 根生成的、严格结构化的 proxy 观察文件。
func LoadGoBuildCacheProxyMetrics(privateRoot string, seedRoots []string) (GoBuildCacheProxyMetrics, error) {
	return LoadGoBuildCacheProxyMetricsAt(privateRoot, GoBuildCacheProxyMetricsPath(privateRoot), seedRoots)
}

// LoadGoBuildCacheProxyMetricsAt 读取一个已由 executor 唯一分配给 gate 的 proxy 观察文件。
func LoadGoBuildCacheProxyMetricsAt(privateRoot string, path string, seedRoots []string) (GoBuildCacheProxyMetrics, error) {
	if !validGoBuildCacheProxyMetricsPath(privateRoot, path) {
		return GoBuildCacheProxyMetrics{}, errors.New("Go build cache proxy metrics path must belong to the private layer")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return GoBuildCacheProxyMetrics{}, fmt.Errorf("read Go build cache proxy metrics: %w", err)
	}
	var metrics GoBuildCacheProxyMetrics
	if err := json.Unmarshal(content, &metrics); err != nil {
		return GoBuildCacheProxyMetrics{}, fmt.Errorf("decode Go build cache proxy metrics: %w", err)
	}
	if err := validateGoBuildCacheProxyMetrics(metrics); err != nil {
		return GoBuildCacheProxyMetrics{}, err
	}
	if !sameGoBuildCacheMetricRoots(metrics.SeedRoots, seedRoots) {
		return GoBuildCacheProxyMetrics{}, errors.New("Go build cache proxy metrics seed identity does not match executor")
	}
	return metrics, nil
}

func validateGoBuildCacheProxyMetrics(metrics GoBuildCacheProxyMetrics) error {
	if metrics.SchemaVersion != goBuildCacheProxyMetricsSchemaVersion || len(metrics.SeedRoots) == 0 ||
		len(metrics.SeedRoots) > goBuildCacheProxyMaxSeedRoots || metrics.BaselineHitByGeneration == nil {
		return errors.New("Go build cache proxy metrics are invalid")
	}
	for _, root := range metrics.SeedRoots {
		if !filepath.IsAbs(root) || filepath.Base(root) == "." {
			return errors.New("Go build cache proxy metrics seed roots are invalid")
		}
	}
	for generation, count := range metrics.BaselineHitByGeneration {
		if generation == "" || generation != filepath.Base(generation) || count == 0 {
			return errors.New("Go build cache proxy metrics baseline hit generations are invalid")
		}
	}
	return nil
}

func sameGoBuildCacheMetricRoots(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index]) == "" || left[index] != right[index] {
			return false
		}
	}
	return true
}
