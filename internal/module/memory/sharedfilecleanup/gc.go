package sharedfilecleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	defaultWorkTTLDays int   = 30
	defaultListLimit   int32 = 500
	maxListLimit       int32 = 2000
	dagScanLimit       int   = 500
	runScanLimit       int32 = 100
)

// Deps carries shared-file cleanup ports and runtime hooks.
type Deps struct {
	Reader     contract.SharedFileReader
	Deleter    contract.SharedFileDeleter
	DAGRuntime contract.DAGRuntime
	Now        func() time.Time
}

// Params configures shared-file cleanup planning and deletion.
type Params struct {
	WorkTTLDays int      `json:"workTtlDays,omitempty"`
	Limit       int32    `json:"limit,omitempty"`
	PinnedPaths []string `json:"pinnedPaths,omitempty"`
}

// Result reports the shared-file cleanup plan or apply outcome.
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

// Item describes one shared file's cleanup classification.
type Item struct {
	Path             string    `json:"path"`
	UpdatedAt        time.Time `json:"updatedAt"`
	AgeDays          int       `json:"ageDays"`
	CleanupCandidate bool      `json:"cleanupCandidate"`
	Protected        bool      `json:"protected"`
	Reason           string    `json:"reason"`
	Deleted          bool      `json:"deleted,omitempty"`
}

// Preview 处理preview。
func Preview(ctx context.Context, deps Deps, params Params) (Result, error) {
	return buildPlan(ctx, deps, params)
}

// Apply 应用记忆。
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

// buildPlan 构建plan。
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

func listSharedFiles(ctx context.Context, reader contract.SharedFileReader, limit int32) ([]contract.SharedFile, error) {
	files, err := reader.List(ctx, contract.SharedFileListFilter{Limit: limit})
	if err != nil {
		return nil, err
	}
	if len(files) >= int(limit) {
		return nil, fmt.Errorf("shared file scan reached safety limit %d", limit)
	}
	return files, nil
}

// buildResult 构建结果。
func buildResult(
	files []contract.SharedFile,
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

// normalizeParams 规范化params。
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

// classifyFile 分类文件。
func classifyFile(
	now time.Time,
	file contract.SharedFile,
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

// collectDAGProtectedPaths 收集DAGprotected路径。
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
		if ref, ok := contract.FinalOutputFileFromRunMetadata(run.Metadata); ok {
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

// sharedfileOutputPathFromNodeConfig 从节点配置处理sharedfileoutput路径。
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
