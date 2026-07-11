// Package sharedfilecleanup 实现 shared file 的垃圾回收：
// 根据 TTL 和 DAG 运行状态保护策略，筛选可删除的过期共享文件。
package sharedfilecleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/sharedfileport"
)

// 清理参数默认值与扫描安全上限。
const (
	defaultWorkTTLDays int   = 30
	defaultListLimit   int32 = 500
	maxListLimit       int32 = 2000
	dagScanLimit       int   = 500
	runScanLimit       int32 = 100
)

// Deps 是 shared file GC 的外部依赖集合。
// Reader/Deleter 连接 store，DAGRuntime 提供 final_output 保护信息，Now 用于测试固定时间。
type Deps struct {
	Reader     sharedfileport.Reader  // 读取 shared file 列表
	Deleter    sharedfileport.Deleter // 执行删除（Preview 时为 nil）
	DAGRuntime contract.DAGRuntime    // 查询 DAG 运行状态以保护活跃文件
	Now        func() time.Time       // 注入时间，便于测试
}

// Params 是单次 GC 调用的 JSON 入参。
// 零值由 normalizeParams 填充默认值，PinnedPaths 用于 UI 或调用方临时保护文件。
type Params struct {
	WorkTTLDays int      `json:"workTtlDays,omitempty"` // 文件保留天数，0 表示使用默认值
	Limit       int32    `json:"limit,omitempty"`       // 最大扫描条数，0 表示使用默认值
	PinnedPaths []string `json:"pinnedPaths,omitempty"` // 手动固定不删除的路径
}

// Result 是单次 GC 的 JSON 响应。
// Preview 与 Apply 共用该结构，DryRun 区分是否执行删除，DeletedPaths 只在真实删除后填充。
type Result struct {
	Items          []Item   `json:"items"`
	WorkTTLDays    int      `json:"workTtlDays"`
	Limit          int32    `json:"limit"`
	DryRun         bool     `json:"dryRun"`
	CandidateCount int      `json:"candidateCount"`
	ProtectedCount int      `json:"protectedCount"`
	DeletedCount   int      `json:"deletedCount"`
	DeletedPaths   []string `json:"deletedPaths,omitempty"`
}

// Item 是单个 shared file 的 GC 判定结果。
// Protected 和 Reason 会告诉 UI 为什么候选没有删除，Deleted 只表示本次 Apply 已删除。
type Item struct {
	Path             string    `json:"path"`
	UpdatedAt        time.Time `json:"updatedAt"`
	AgeDays          int       `json:"ageDays"`
	CleanupCandidate bool      `json:"cleanupCandidate"` // true 表示满足 TTL 条件可删除
	Protected        bool      `json:"protected"`        // true 表示被 DAG 或 pinned 保护
	Reason           string    `json:"reason"`
	Deleted          bool      `json:"deleted,omitempty"`
}

// Preview 生成 GC 预览结果，不执行实际删除。
func Preview(ctx context.Context, deps Deps, params Params) (Result, error) {
	return buildPlan(ctx, deps, params)
}

// Apply 执行实际删除，删除所有满足条件的候选文件。
func Apply(ctx context.Context, deps Deps, params Params) (Result, error) {
	if deps.Deleter == nil {
		return Result{}, errors.New("shared file store is not configured for cleanup deletion")
	}
	result, err := buildPlan(ctx, deps, params)
	if err != nil {
		return Result{}, err
	}
	result.DryRun = false
	for i := range result.Items {
		if !result.Items[i].CleanupCandidate {
			continue
		}
		count, err := deps.Deleter.Delete(ctx, result.Items[i].Path)
		if err != nil {
			return Result{}, fmt.Errorf("delete cleanup candidate shared file: %w", err)
		}
		if count <= 0 {
			continue
		}
		result.Items[i].Deleted = true
		result.DeletedCount++
		result.DeletedPaths = append(result.DeletedPaths, result.Items[i].Path)
	}
	return result, nil
}

// buildPlan 构建 GC 执行计划：校验参数、收集受保护路径、列出文件并分类。
func buildPlan(ctx context.Context, deps Deps, params Params) (Result, error) {
	if deps.Reader == nil {
		return Result{}, errors.New("shared file store is not configured")
	}
	if deps.DAGRuntime == nil {
		return Result{}, errors.New("shared file final_output cleanup guard is unavailable; retry after DAG orchestration is connected")
	}
	normalized, err := normalizeParams(params)
	if err != nil {
		return Result{}, err
	}
	protectedPaths, err := collectProtectedPaths(ctx, deps.DAGRuntime)
	if err != nil {
		return Result{}, err
	}
	files, err := listSharedFiles(ctx, deps.Reader, normalized.Limit)
	if err != nil {
		return Result{}, err
	}
	return buildResult(files, protectedPaths, normalized, deps.Now), nil
}

// listSharedFiles 列出共享文件并检查是否超过安全扫描上限。
func listSharedFiles(ctx context.Context, reader sharedfileport.Reader, limit int32) ([]sharedfileport.File, error) {
	files, err := reader.List(ctx, sharedfileport.ListFilter{Limit: limit})
	if err != nil {
		return nil, err
	}
	if len(files) >= int(limit) {
		return nil, fmt.Errorf("shared file scan reached safety limit %d", limit)
	}
	return files, nil
}

// buildResult 遍历文件列表并调用 classifyFile 分类，汇总候选与保护数量。
func buildResult(
	files []sharedfileport.File,
	protectedPaths map[string]string,
	params Params,
	now func() time.Time,
) Result {
	if now == nil {
		now = time.Now
	}
	pinned := pinnedPathSet(params.PinnedPaths)
	result := Result{
		Items:       make([]Item, 0, len(files)),
		WorkTTLDays: params.WorkTTLDays,
		Limit:       params.Limit,
		DryRun:      true,
	}
	for _, file := range files {
		item, keep := classifyFile(now(), file, params.WorkTTLDays, protectedPaths, pinned)
		if !keep {
			continue
		}
		if item.CleanupCandidate {
			result.CandidateCount++
		}
		if item.Protected {
			result.ProtectedCount++
		}
		result.Items = append(result.Items, item)
	}
	return result
}

// normalizeParams 规范化并校验 GC 参数，填充零值为默认值，拒绝超出上限的配置。
func normalizeParams(params Params) (Params, error) {
	if params.WorkTTLDays < 0 {
		return Params{}, errors.New("workTtlDays must be non-negative")
	}
	if params.Limit < 0 {
		return Params{}, errors.New("limit must be non-negative")
	}
	if params.WorkTTLDays == 0 {
		params.WorkTTLDays = defaultWorkTTLDays
	}
	if params.Limit == 0 {
		params.Limit = defaultListLimit
	}
	if params.Limit > maxListLimit {
		return Params{}, fmt.Errorf("limit must be <= %d", maxListLimit)
	}
	return params, nil
}

// classifyFile 对单个文件进行分类：优先判断 pinned、受保护、内部路径，然后按 TTL 判定是否为清理候选。
func classifyFile(
	now time.Time,
	file sharedfileport.File,
	workTTLDays int,
	protectedPaths map[string]string,
	pinned map[string]struct{},
) (Item, bool) {
	path := strings.TrimSpace(file.Path)
	if path == "" {
		return Item{}, false
	}
	ageDays := max(int(now.Sub(file.UpdatedAt).Hours()/24), 0)
	item := Item{
		Path:      path,
		UpdatedAt: file.UpdatedAt,
		AgeDays:   ageDays,
		Reason:    "older_than_work_ttl",
	}
	if _, ok := pinned[path]; ok {
		item.Protected = true
		item.Reason = "pinned"
		return item, true
	}
	if reason, ok := protectedPaths[path]; ok {
		item.Protected = true
		item.Reason = reason
		return item, true
	}
	if strings.HasPrefix(path, "_internal/") {
		item.Protected = true
		item.Reason = "internal"
		return item, true
	}
	if ageDays < workTTLDays {
		item.Reason = "recent"
		return item, true
	}
	item.CleanupCandidate = true
	return item, true
}

// collectProtectedPaths 遍历所有 DAG 的运行记录，收集不可删除的 shared file 路径及保护原因。
func collectProtectedPaths(ctx context.Context, dagRuntime contract.DAGRuntime) (map[string]string, error) {
	protected := map[string]string{}
	dags, err := dagRuntime.ListDAGs(ctx, contract.ListDAGsFilter{Limit: dagScanLimit})
	if err != nil {
		return nil, fmt.Errorf("list DAGs for shared file cleanup guard: %w", err)
	}
	if len(dags) >= dagScanLimit {
		return nil, fmt.Errorf("DAG scan reached safety limit %d", dagScanLimit)
	}
	for _, dag := range dags {
		if err := collectDAGProtectedPaths(ctx, dagRuntime, dag.DagKey, protected); err != nil {
			return nil, err
		}
	}
	return protected, nil
}

// collectDAGProtectedPaths 读取指定 DAG 的所有运行记录，将 final_output 和活跃运行的输出路径标记为受保护。
func collectDAGProtectedPaths(ctx context.Context, dagRuntime contract.DAGRuntime, dagKey string, protected map[string]string) error {
	dagKey = strings.TrimSpace(dagKey)
	if dagKey == "" {
		return nil
	}
	runs, err := dagRuntime.ListRuns(ctx, contract.ListRunsRequest{
		DagKey: dagKey,
		Limit:  runScanLimit,
	})
	if err != nil {
		return fmt.Errorf("list DAG runs for shared file cleanup guard: %w", err)
	}
	if len(runs.Runs) >= int(runScanLimit) {
		return fmt.Errorf("run scan reached safety limit %d", runScanLimit)
	}
	for _, run := range runs.Runs {
		ref, ok, parseErr := contract.FinalOutputFileFromRunMetadataStrict(run.Metadata)
		if parseErr != nil {
			return fmt.Errorf("parse final_output metadata for run %q: %w", strings.TrimSpace(run.RunKey), parseErr)
		}
		if ok {
			addProtectedPath(protected, ref.Path, "final_output")
		}
		if run.Status == "running" {
			if err := collectRunningRunSharedfileTargets(ctx, dagRuntime, run.RunKey, protected); err != nil {
				return err
			}
		}
	}
	return nil
}

// collectRunningRunSharedfileTargets 读取正在运行的 DAG run 节点配置，将其 to_sharedfile 输出路径标记为受保护。
func collectRunningRunSharedfileTargets(ctx context.Context, dagRuntime contract.DAGRuntime, runKey string, protected map[string]string) error {
	runKey = strings.TrimSpace(runKey)
	if runKey == "" {
		return nil
	}
	detail, err := dagRuntime.GetRun(ctx, contract.GetRunRequest{RunKey: runKey})
	if err != nil {
		return fmt.Errorf("get running DAG run for shared file cleanup guard: %w", err)
	}
	for _, node := range detail.Nodes {
		path, err := sharedfileOutputPathFromNodeConfig(node.Config)
		if err != nil {
			return err
		}
		addProtectedPath(protected, path, "active_run_output")
	}
	return nil
}

// addProtectedPath 向 protected map 写入路径和保护原因；final_output 优先级最高，不可被覆盖。
func addProtectedPath(protected map[string]string, path, reason string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	if protected[path] == "final_output" {
		return
	}
	protected[path] = reason
}

// sharedfileOutputPathFromNodeConfig 从节点配置 JSON 中提取 to_sharedfile 输出路径；
// 支持 outputs.to_sharedfile 和顶级 to_sharedfile 两种结构。
func sharedfileOutputPathFromNodeConfig(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return "", nil
	}
	var config struct {
		Outputs *struct {
			ToSharedfile *struct {
				Path string `json:"path"`
			} `json:"to_sharedfile"`
		} `json:"outputs"`
		ToSharedfile *struct {
			Path string `json:"path"`
		} `json:"to_sharedfile"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", fmt.Errorf("parse running DAG node sharedfile output config: %w", err)
	}
	if config.Outputs != nil && config.Outputs.ToSharedfile != nil {
		return strings.TrimSpace(config.Outputs.ToSharedfile.Path), nil
	}
	if config.ToSharedfile != nil {
		return strings.TrimSpace(config.ToSharedfile.Path), nil
	}
	return "", nil
}

// pinnedPathSet 将 pinned 路径列表转换为 set，用于 O(1) 查找。
func pinnedPathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		out[path] = struct{}{}
	}
	return out
}
