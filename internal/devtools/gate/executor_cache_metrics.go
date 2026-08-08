package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	goBuildCacheProxyMetricsSchemaVersion  uint32 = 1
	goBuildCacheProxyMetricsFilePrefix            = ".super-dolphin-go-cache-metrics-"
	goBuildCacheProxyMetricsFileName              = goBuildCacheProxyMetricsFilePrefix + "default.json"
	goBuildCacheProxyStartedFileSuffix            = ".started"
	goBuildCacheProxyStartedMarkerSuffix          = ".started-"
	goBuildCacheProxyContributionSuffix           = ".helper-"
	goBuildCacheProxyContributionExtension        = ".json"
)

// GoBuildCacheProxyMetrics 是一个 Go worker 对共享 build-cache 的有界观察。
// 它只描述 GOCACHEPROG 协议请求，不能被当作 Go test-result cache 的证据。
type GoBuildCacheProxyMetrics struct {
	SchemaVersion    uint32   `json:"schema_version"`
	PrivateHitCount  uint64   `json:"private_hit_count"`
	BaselineHitCount uint64   `json:"baseline_hit_count"`
	MissCount        uint64   `json:"miss_count"`
	PutCount         uint64   `json:"put_count"`
	SeedRoots        []string `json:"seed_roots"`
}

func newGoBuildCacheProxyMetrics(seedRoot string) GoBuildCacheProxyMetrics {
	metrics := GoBuildCacheProxyMetrics{
		SchemaVersion: goBuildCacheProxyMetricsSchemaVersion,
		SeedRoots:     []string{seedRoot},
	}
	return metrics
}

func (metrics *GoBuildCacheProxyMetrics) recordHit(layer int) {
	if layer == 0 {
		metrics.PrivateHitCount++
		return
	}
	metrics.BaselineHitCount++
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
	if invocation == "" || invocation != filepath.Base(invocation) || strings.ContainsAny(invocation, "\\/.\x00\r\n") {
		return "", errors.New("Go build cache proxy metrics invocation is invalid")
	}
	return filepath.Join(privateRoot, goBuildCacheProxyMetricsFilePrefix+invocation+".json"), nil
}

func validGoBuildCacheProxyMetricsPath(privateRoot string, metricsPath string) bool {
	base := filepath.Base(metricsPath)
	return filepath.Dir(metricsPath) == privateRoot && strings.HasPrefix(base, goBuildCacheProxyMetricsFilePrefix) &&
		strings.HasSuffix(base, ".json") && base != goBuildCacheProxyMetricsFilePrefix+".json"
}

// executeObservedGoBuildCacheProxy 为每个 helper 保留独立生命周期，并合并不可变贡献。
func executeObservedGoBuildCacheProxy(config goBuildCacheProxyConfig, input io.Reader, output io.Writer) error {
	if config.metricsPath == "" {
		return serveGoBuildCacheProxy(config, input, output)
	}
	if config.metrics == nil {
		return errors.New("Go build cache proxy metrics state is required")
	}
	markerPath, err := createGoBuildCacheProxyStartedMarker(config.metricsPath)
	if err != nil {
		return err
	}
	if err := serveGoBuildCacheProxy(config, input, output); err != nil {
		return err
	}
	contributionPath, err := goBuildCacheProxyContributionPath(config.metricsPath, markerPath)
	if err != nil {
		return err
	}
	if err := writeGoBuildCacheProxyMetrics(contributionPath, *config.metrics); err != nil {
		return fmt.Errorf("publish Go build cache proxy helper metrics: %w", err)
	}
	if err := os.Remove(markerPath); err != nil {
		return fmt.Errorf("remove Go build cache proxy helper marker: %w", err)
	}
	return nil
}

// createGoBuildCacheProxyStartedMarker 使用独占创建为 helper 分配唯一生命周期标记。
func createGoBuildCacheProxyStartedMarker(metricsPath string) (string, error) {
	marker, err := os.CreateTemp(filepath.Dir(metricsPath), filepath.Base(metricsPath)+goBuildCacheProxyStartedMarkerSuffix+"*")
	if err != nil {
		return "", fmt.Errorf("mark Go build cache proxy started: %w", err)
	}
	markerPath := marker.Name()
	if _, err := marker.WriteString("started\n"); err != nil {
		closeErr := marker.Close()
		removeErr := os.Remove(markerPath)
		return "", errors.Join(fmt.Errorf("write Go build cache proxy started marker: %w", err), closeErr, removeErr)
	}
	if err := marker.Close(); err != nil {
		removeErr := os.Remove(markerPath)
		return "", errors.Join(fmt.Errorf("close Go build cache proxy started marker: %w", err), removeErr)
	}
	return markerPath, nil
}

// goBuildCacheProxyContributionPath 将 helper 标记映射到同一 gate 的不可变指标贡献。
func goBuildCacheProxyContributionPath(metricsPath string, markerPath string) (string, error) {
	if filepath.Dir(metricsPath) != filepath.Dir(markerPath) {
		return "", errors.New("Go build cache proxy helper marker is outside metrics directory")
	}
	metricsBase, markerBase := filepath.Base(metricsPath), filepath.Base(markerPath)
	markerPrefix := metricsBase + goBuildCacheProxyStartedMarkerSuffix
	if !strings.HasPrefix(markerBase, markerPrefix) {
		return "", errors.New("Go build cache proxy helper marker does not belong to metrics path")
	}
	token := strings.TrimPrefix(markerBase, markerPrefix)
	if token == "" || strings.ContainsAny(token, "\\/\x00\r\n") {
		return "", errors.New("Go build cache proxy helper marker token is invalid")
	}
	return filepath.Join(filepath.Dir(metricsPath), metricsBase+goBuildCacheProxyContributionSuffix+token+goBuildCacheProxyContributionExtension), nil
}

// collectGoBuildCacheProxyContributions 读取并严格求和同一 gate 的每个 helper 贡献。
func collectGoBuildCacheProxyContributions(metricsPath string, seedRoot string) (GoBuildCacheProxyMetrics, int, error) {
	entries, err := os.ReadDir(filepath.Dir(metricsPath))
	if err != nil {
		return GoBuildCacheProxyMetrics{}, 0, fmt.Errorf("read Go build cache proxy metrics directory: %w", err)
	}
	metrics := newGoBuildCacheProxyMetrics(seedRoot)
	count := 0
	prefix := filepath.Base(metricsPath) + goBuildCacheProxyContributionSuffix
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if !strings.HasSuffix(entry.Name(), goBuildCacheProxyContributionExtension) || !entry.Type().IsRegular() {
			return GoBuildCacheProxyMetrics{}, 0, fmt.Errorf("Go build cache proxy helper contribution %q is invalid", entry.Name())
		}
		path := filepath.Join(filepath.Dir(metricsPath), entry.Name())
		part, err := loadGoBuildCacheProxyMetricsFile(path, seedRoot)
		if err != nil {
			return GoBuildCacheProxyMetrics{}, 0, fmt.Errorf("load Go build cache proxy helper contribution %q: %w", entry.Name(), err)
		}
		if err := addGoBuildCacheProxyMetrics(&metrics, part); err != nil {
			return GoBuildCacheProxyMetrics{}, 0, err
		}
		count++
	}
	return metrics, count, nil
}

// addGoBuildCacheProxyMetrics 防止多 helper 计数求和时无声溢出。
func addGoBuildCacheProxyMetrics(total *GoBuildCacheProxyMetrics, part GoBuildCacheProxyMetrics) error {
	for _, item := range []struct {
		name  string
		total *uint64
		part  uint64
	}{
		{name: "private hit", total: &total.PrivateHitCount, part: part.PrivateHitCount},
		{name: "baseline hit", total: &total.BaselineHitCount, part: part.BaselineHitCount},
		{name: "miss", total: &total.MissCount, part: part.MissCount},
		{name: "put", total: &total.PutCount, part: part.PutCount},
	} {
		if ^uint64(0)-*item.total < item.part {
			return fmt.Errorf("Go build cache proxy %s metrics overflow", item.name)
		}
		*item.total += item.part
	}
	return nil
}

// loadGoBuildCacheProxyMetricsFile 解码并校验单个聚合或 helper 指标文件。
func loadGoBuildCacheProxyMetricsFile(path string, seedRoot string) (GoBuildCacheProxyMetrics, error) {
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
	if len(metrics.SeedRoots) != 1 || metrics.SeedRoots[0] != seedRoot {
		return GoBuildCacheProxyMetrics{}, errors.New("Go build cache proxy metrics seed identity does not match executor")
	}
	return metrics, nil
}

// goBuildCacheProxyStartedMarkers 列出 legacy 或每 helper 的生命周期标记。
func goBuildCacheProxyStartedMarkers(metricsPath string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Dir(metricsPath))
	if err != nil {
		return nil, fmt.Errorf("read Go build cache proxy started markers: %w", err)
	}
	base, prefix := filepath.Base(metricsPath), filepath.Base(metricsPath)+goBuildCacheProxyStartedMarkerSuffix
	markers := make([]string, 0)
	for _, entry := range entries {
		if entry.Name() != base+goBuildCacheProxyStartedFileSuffix && !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		markers = append(markers, filepath.Join(filepath.Dir(metricsPath), entry.Name()))
	}
	return markers, nil
}

// goBuildCacheProxyContributionPaths 列出全部 helper 贡献，用于最终完整性校验。
func goBuildCacheProxyContributionPaths(metricsPath string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Dir(metricsPath))
	if err != nil {
		return nil, fmt.Errorf("read Go build cache proxy helper contributions: %w", err)
	}
	prefix := filepath.Base(metricsPath) + goBuildCacheProxyContributionSuffix
	paths := make([]string, 0)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			paths = append(paths, filepath.Join(filepath.Dir(metricsPath), entry.Name()))
		}
	}
	return paths, nil
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

// LoadGoBuildCacheProxyMetricsAt 在 helper 生命周期结束后聚合并消费 gate 专属 proxy 观察贡献。
func LoadGoBuildCacheProxyMetricsAt(privateRoot string, path string, seedRoot string) (GoBuildCacheProxyMetrics, error) {
	if !validGoBuildCacheProxyMetricsPath(privateRoot, path) {
		return GoBuildCacheProxyMetrics{}, errors.New("Go build cache proxy metrics path must belong to the private layer")
	}
	markers, err := goBuildCacheProxyStartedMarkers(path)
	if err != nil {
		return GoBuildCacheProxyMetrics{}, err
	}
	if len(markers) == 0 {
		contributions, err := goBuildCacheProxyContributionPaths(path)
		if err != nil {
			return GoBuildCacheProxyMetrics{}, err
		}
		if len(contributions) != 0 {
			if err := finalizeGoBuildCacheProxyMetrics(path, seedRoot, contributions); err != nil {
				return GoBuildCacheProxyMetrics{}, err
			}
		}
	}
	return loadGoBuildCacheProxyMetricsFile(path, seedRoot)
}

// finalizeGoBuildCacheProxyMetrics 在所有 helper marker 消失后单点发布并消费聚合结果。
func finalizeGoBuildCacheProxyMetrics(metricsPath string, seedRoot string, contributions []string) error {
	metrics, count, err := collectGoBuildCacheProxyContributions(metricsPath, seedRoot)
	if err != nil {
		return err
	}
	if count == 0 || count != len(contributions) {
		return errors.New("Go build cache proxy helper contribution set changed during finalization")
	}
	if err := writeGoBuildCacheProxyMetrics(metricsPath, metrics); err != nil {
		return fmt.Errorf("publish aggregated Go build cache proxy metrics: %w", err)
	}
	markers, err := goBuildCacheProxyStartedMarkers(metricsPath)
	if err != nil {
		return err
	}
	if len(markers) != 0 {
		return errors.New("Go build cache proxy helper started during metrics finalization")
	}
	for _, path := range contributions {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("consume Go build cache proxy helper contribution %q: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func validateGoBuildCacheProxyMetrics(metrics GoBuildCacheProxyMetrics) error {
	if metrics.SchemaVersion != goBuildCacheProxyMetricsSchemaVersion || len(metrics.SeedRoots) != 1 {
		return errors.New("Go build cache proxy metrics are invalid")
	}
	if !filepath.IsAbs(metrics.SeedRoots[0]) || filepath.Base(metrics.SeedRoots[0]) == "." {
		return errors.New("Go build cache proxy metrics seed root is invalid")
	}
	return nil
}
