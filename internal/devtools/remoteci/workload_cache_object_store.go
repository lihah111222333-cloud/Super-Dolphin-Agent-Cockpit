package remoteci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"golang.org/x/sync/errgroup"
)

// listPassedWorkloadCacheKeys 一次列出环境前缀，避免为不存在的目标逐个发起 OSS 请求。
func listPassedWorkloadCacheKeys(
	ctx context.Context,
	store ObjectStore,
	prefix string,
) (map[string]struct{}, error) {
	keys, err := store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list passed workload cache %q: %w", prefix, err)
	}
	available := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			return nil, fmt.Errorf("listed passed workload cache key %q escapes prefix %q", key, prefix)
		}
		available[key] = struct{}{}
	}
	return available, nil
}

// downloadPassedWorkloadCache 自适应并行下载并校验列表中命中的不可变标记。
func downloadPassedWorkloadCache(
	ctx context.Context,
	store ObjectStore,
	tempRoot string,
	entries []remoteWorkloadCacheEntry,
	availableKeys map[string]struct{},
) ([]bool, error) {
	matched := make([]bool, len(entries))
	downloads, downloadCtx := errgroup.WithContext(ctx)
	downloads.SetLimit(remoteCoordinatorParallelism(len(entries)))
	for index := range entries {
		entry := entries[index]
		if _, available := availableKeys[entry.key]; !available {
			continue
		}
		downloads.Go(func() error {
			localPath := filepath.Join(tempRoot, fmt.Sprintf("%06d.pass", index))
			found, err := store.DownloadIfExists(downloadCtx, entry.key, localPath)
			if err != nil {
				return fmt.Errorf("download passed workload cache %q: %w", entry.workloadID, err)
			}
			if !found {
				return nil
			}
			if err := validateRemoteWorkloadCacheMarker(localPath, entry); err != nil {
				return fmt.Errorf("validate passed workload cache %q: %w", entry.workloadID, err)
			}
			matched[index] = true
			return nil
		})
	}
	if err := downloads.Wait(); err != nil {
		return nil, err
	}
	return matched, nil
}

// projectPassedWorkloadCache 将已验证标记投影成零时长复用执行结果。
func projectPassedWorkloadCache(
	observedAt time.Time,
	entries []remoteWorkloadCacheEntry,
	matched []bool,
) map[string]gate.PlanGateExecution {
	cached := make(map[string]gate.PlanGateExecution)
	for index, entry := range entries {
		if !matched[index] {
			continue
		}
		log := []byte("reused passed workload " + entry.identityDigest)
		sum := sha256.Sum256(log)
		cached[entry.workloadID] = gate.PlanGateExecution{
			GateID: gate.GateID(entry.workloadID), Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: observedAt, CompletedAt: observedAt, Log: log,
			LogDigest: "sha256:" + hex.EncodeToString(sum[:]),
		}
	}
	return cached
}

// validateRemoteWorkloadCacheMarker 核对标记文件类型、大小和完整规范内容。
func validateRemoteWorkloadCacheMarker(path string, entry remoteWorkloadCacheEntry) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > remoteWorkloadCacheMarkerMaxBytes {
		return errors.New("remote workload cache marker size or type is invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, encodeRemoteWorkloadCacheMarker(entry)) {
		return errors.New("remote workload cache marker content does not match its identity")
	}
	return nil
}

func validateRemoteWorkloadCacheKey(prefix string, key string) error {
	if !strings.HasPrefix(key, prefix) {
		return errors.New("remote workload cache key escaped its environment prefix")
	}
	name := strings.TrimPrefix(key, prefix)
	digest := strings.TrimSuffix(name, ".pass")
	if name == digest || len(digest) != sha256.Size*2 {
		return fmt.Errorf("remote workload cache key %q is malformed", key)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("remote workload cache key %q is malformed", key)
	}
	return nil
}

// storePassedWorkloadCache 只为本次成功执行的 workload 发布不可变通过标记。
func (coordinator *Coordinator) storePassedWorkloadCache(
	ctx context.Context,
	tempRoot string,
	entries []remoteWorkloadCacheEntry,
	executions map[string]gate.PlanGateExecution,
	ledgerStore *gate.DurationLedgerStore,
) error {
	byWorkload := make(map[string]remoteWorkloadCacheEntry, len(entries))
	for _, entry := range entries {
		byWorkload[entry.workloadID] = entry
	}
	workloadIDs := make([]string, 0, len(executions))
	for workloadID := range executions {
		workloadIDs = append(workloadIDs, workloadID)
	}
	sort.Strings(workloadIDs)
	stored := make(map[string]struct{})
	uploads := make([]passedWorkloadCacheUpload, 0, len(workloadIDs))
	published := make(map[string]gate.PlanGateExecution)
	var buildErr error
	for _, workloadID := range workloadIDs {
		execution := executions[workloadID]
		if !remoteExecutionCanPublishPassMarker(workloadID, execution) {
			continue
		}
		entry, ok := byWorkload[workloadID]
		if !ok {
			buildErr = errors.Join(buildErr, fmt.Errorf("passed workload %q has no cache identity", workloadID))
			continue
		}
		if _, duplicate := stored[entry.key]; duplicate {
			continue
		}
		stored[entry.key] = struct{}{}
		published[workloadID] = execution
		uploads = append(uploads, passedWorkloadCacheUpload{
			workloadID: workloadID,
			prefix:     entry.prefix,
			key:        entry.key,
			data:       encodeRemoteWorkloadCacheMarker(entry),
		})
	}
	if err := errors.Join(
		buildErr,
		uploadPassedWorkloadCache(ctx, coordinator.store, tempRoot, uploads),
	); err != nil {
		return err
	}
	return recordPassedWorkloadCacheProofs(
		ledgerStore,
		entries,
		published,
		coordinator.now().UTC(),
	)
}

type passedWorkloadCacheUpload struct {
	workloadID string
	prefix     string
	key        string
	data       []byte
}

// uploadPassedWorkloadCache 用一次递归 OSS 调用发布同一环境的全部不可变 PASS 标记。
func uploadPassedWorkloadCache(
	ctx context.Context,
	store ObjectStore,
	tempRoot string,
	uploads []passedWorkloadCacheUpload,
) error {
	if len(uploads) == 0 {
		return nil
	}
	publishRoot, err := os.MkdirTemp(tempRoot, "passed-workloads-")
	if err != nil {
		return fmt.Errorf("create passed workload cache publish root: %w", err)
	}
	prefix := uploads[0].prefix
	for _, upload := range uploads {
		if upload.prefix != prefix {
			return errors.New("passed workload cache upload spans multiple environment prefixes")
		}
		name := strings.TrimPrefix(upload.key, prefix)
		if name == upload.key || name == "" || strings.Contains(name, "/") {
			return fmt.Errorf("passed workload cache key %q is outside publish prefix %q", upload.key, prefix)
		}
		localPath := filepath.Join(publishRoot, name)
		if err := os.WriteFile(localPath, upload.data, 0o600); err != nil {
			return fmt.Errorf("write passed workload cache %q: %w", upload.workloadID, err)
		}
	}
	if err := store.UploadDirectory(ctx, publishRoot, prefix, min(len(uploads), 10000)); err != nil {
		return fmt.Errorf("upload %d passed workload cache markers: %w", len(uploads), err)
	}
	return nil
}

// remoteExecutionCanPublishPassMarker 只接受带明确目标通过证明的成功执行。
func remoteExecutionCanPublishPassMarker(workloadID string, execution gate.PlanGateExecution) bool {
	if execution.Status != gate.ResultStatusPassed || execution.ExitCode != 0 {
		return false
	}
	_, kind, target, targeted, err := gate.ParseWorkloadID(workloadID)
	if err != nil {
		return false
	}
	if !targeted || kind != gate.WorkloadTargetGoTest {
		return true
	}
	return passedExactGoTestTarget(target, execution.TestTimings)
}

// passedExactGoTestTarget 只接受精确目标自身的通过时长记录。
func passedExactGoTestTarget(target string, timings []gate.GoTestTiming) bool {
	testTarget, err := gate.ParseGoTestTarget(target)
	if err != nil {
		return false
	}
	for _, timing := range timings {
		if timing.Name == testTarget.Name {
			if timing.Status == gate.GoTestStatusPass {
				return true
			}
		}
	}
	return false
}

func encodeRemoteWorkloadCacheMarker(entry remoteWorkloadCacheEntry) []byte {
	var data []byte
	data = appendRemoteWorkloadCacheField(data, remoteWorkloadCacheHeader, strconv.Itoa(remoteWorkloadCacheSchemaVersion))
	data = appendRemoteWorkloadCacheField(data, "environment", entry.environmentDigest)
	data = appendRemoteWorkloadCacheField(data, "identity", entry.identityDigest)
	data = appendRemoteWorkloadCacheField(data, "execution", entry.executionDigest)
	data = appendRemoteWorkloadCacheField(data, "input", entry.inputDigest)
	data = appendRemoteWorkloadCacheField(data, "status", string(gate.ResultStatusPassed))
	return data
}
