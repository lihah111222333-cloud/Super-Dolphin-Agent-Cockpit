package remoteci

import (
	"bytes"
	"context"
	"crypto/rand"
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
			receiptDigest, err := validateRemoteWorkloadCacheMarker(localPath, entry)
			if err != nil {
				return fmt.Errorf("validate passed workload cache %q: %w", entry.workloadID, err)
			}
			receiptKey := remoteWorkloadCacheReceiptKey(entry, receiptDigest)
			receiptPath := filepath.Join(tempRoot, fmt.Sprintf("%06d.receipt", index))
			found, err = store.DownloadIfExists(downloadCtx, receiptKey, receiptPath)
			if err != nil {
				return fmt.Errorf("download passed workload receipt %q: %w", entry.workloadID, err)
			}
			if !found {
				return nil
			}
			if err := validateRemoteWorkloadCacheReceipt(receiptPath, entry, receiptDigest); err != nil {
				return fmt.Errorf("validate passed workload receipt %q: %w", entry.workloadID, err)
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
func validateRemoteWorkloadCacheMarker(path string, entry remoteWorkloadCacheEntry) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > remoteWorkloadCacheMarkerMaxBytes {
		return "", errors.New("remote workload cache marker size or type is invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) != 8 || lines[7] != "" || !strings.HasPrefix(lines[6], "receipt ") {
		return "", errors.New("remote workload cache marker shape is invalid")
	}
	receiptDigest := strings.TrimPrefix(lines[6], "receipt ")
	if !validRemoteWorkloadCacheDigest(receiptDigest) {
		return "", errors.New("remote workload cache marker receipt digest is invalid")
	}
	if !bytes.Equal(data, encodeRemoteWorkloadCacheMarkerWithReceipt(entry, receiptDigest)) {
		return "", errors.New("remote workload cache marker content does not match its identity")
	}
	return receiptDigest, nil
}

// validateRemoteWorkloadCacheReceipt 在复用 PASS 前验证不可变执行凭据及其内容摘要。
func validateRemoteWorkloadCacheReceipt(path string, entry remoteWorkloadCacheEntry, expectedDigest string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > remoteWorkloadCacheMarkerMaxBytes {
		return errors.New("remote workload cache receipt size or type is invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	expectedKeys := []string{
		remoteWorkloadCacheHeader,
		"identity",
		"commit",
		"tree",
		"profile",
		"entrypoint",
		"runner-image",
		"runner",
		"runner-config",
		"gate-binary",
		"runtime-seed",
		"policy",
		"baseline-manifest",
		"anchor-generation",
		"anchor-manifest",
		"anchor-commit",
		"anchor-tree",
		"receipt-nonce",
	}
	if len(lines) != len(expectedKeys)+1 || lines[len(lines)-1] != "" {
		return errors.New("remote workload cache receipt shape is invalid")
	}
	for index, key := range expectedKeys {
		prefix := key + " "
		if !strings.HasPrefix(lines[index], prefix) {
			return errors.New("remote workload cache receipt fields are invalid")
		}
		value := strings.TrimPrefix(lines[index], prefix)
		if strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("remote workload cache receipt field is invalid")
		}
		if key == remoteWorkloadCacheHeader && value != "receipt/v1" {
			return errors.New("remote workload cache receipt schema is invalid")
		}
		if key == "identity" && value != entry.identityDigest {
			return errors.New("remote workload cache receipt identity does not match marker")
		}
		if key == "receipt-nonce" && !validRemoteWorkloadCacheDigest(value) {
			return errors.New("remote workload cache receipt nonce is invalid")
		}
	}
	sum := sha256.Sum256(data)
	if digest := "sha256:" + hex.EncodeToString(sum[:]); digest != expectedDigest {
		return errors.New("remote workload cache receipt digest does not match marker")
	}
	return nil
}

func validRemoteWorkloadCacheDigest(value string) bool {
	encoded := strings.TrimPrefix(value, "sha256:")
	if encoded == value || len(encoded) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
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
		receiptEntry, err := remoteWorkloadCacheEntryForExecution(entry)
		if err != nil {
			buildErr = errors.Join(buildErr, fmt.Errorf("create passed workload receipt %q: %w", workloadID, err))
			continue
		}
		published[workloadID] = execution
		uploads = append(uploads, passedWorkloadCacheUpload{
			workloadID: workloadID,
			prefix:     receiptEntry.receiptPrefix,
			key:        receiptEntry.receiptKey,
			data:       encodeRemoteWorkloadCacheReceipt(receiptEntry),
		})
		uploads = append(uploads, passedWorkloadCacheUpload{
			workloadID: workloadID,
			prefix:     receiptEntry.prefix,
			key:        receiptEntry.key,
			data:       encodeRemoteWorkloadCacheMarker(receiptEntry),
			commit:     true,
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
	commit     bool
}

// uploadPassedWorkloadCache 先发布内容寻址 receipt，再以 marker 原子提交 PASS。
func uploadPassedWorkloadCache(
	ctx context.Context,
	store ObjectStore,
	tempRoot string,
	uploads []passedWorkloadCacheUpload,
) error {
	if len(uploads) == 0 {
		return nil
	}
	for _, commit := range []bool{false, true} {
		phase := make([]passedWorkloadCacheUpload, 0, len(uploads))
		for _, upload := range uploads {
			if upload.commit == commit {
				phase = append(phase, upload)
			}
		}
		if len(phase) == 0 {
			continue
		}
		prefix := phase[0].prefix
		for _, upload := range phase {
			if upload.prefix != prefix {
				return errors.New("passed workload cache upload phase spans multiple prefixes")
			}
		}
		publishRoot, err := os.MkdirTemp(tempRoot, "passed-workloads-")
		if err != nil {
			return fmt.Errorf("create passed workload cache publish root: %w", err)
		}
		for _, upload := range phase {
			name := strings.TrimPrefix(upload.key, prefix)
			if name == upload.key || name == "" || strings.Contains(name, "/") {
				return fmt.Errorf("passed workload cache key %q is outside publish prefix %q", upload.key, prefix)
			}
			if err := os.WriteFile(filepath.Join(publishRoot, name), upload.data, 0o600); err != nil {
				return fmt.Errorf("write passed workload cache %q: %w", upload.workloadID, err)
			}
		}
		if err := store.UploadDirectory(ctx, publishRoot, prefix, min(len(phase), 10000)); err != nil {
			return fmt.Errorf("upload %d passed workload cache objects: %w", len(phase), err)
		}
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
	return encodeRemoteWorkloadCacheMarkerWithReceipt(entry, remoteWorkloadCacheReceiptDigest(entry))
}

func remoteWorkloadCacheReceiptDigest(entry remoteWorkloadCacheEntry) string {
	sum := sha256.Sum256(encodeRemoteWorkloadCacheReceipt(entry))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// remoteWorkloadCacheEntryForExecution 为每次真实执行生成新 receipt，同时保持可复用的 PASS marker 身份不变。
func remoteWorkloadCacheEntryForExecution(entry remoteWorkloadCacheEntry) (remoteWorkloadCacheEntry, error) {
	var nonce [sha256.Size]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return remoteWorkloadCacheEntry{}, err
	}
	entry.receiptNonce = "sha256:" + hex.EncodeToString(nonce[:])
	entry.receiptKey = remoteWorkloadCacheReceiptKey(entry, remoteWorkloadCacheReceiptDigest(entry))
	return entry, nil
}

func remoteWorkloadCacheReceiptKey(entry remoteWorkloadCacheEntry, receiptDigest string) string {
	return entry.receiptPrefix + strings.TrimPrefix(entry.identityDigest, "sha256:") + "." +
		strings.TrimPrefix(receiptDigest, "sha256:") + ".receipt"
}

func remoteWorkloadCacheReceiptPrefix(prefix, environmentDigest string) string {
	return prefix + "receipts/" + strings.TrimPrefix(environmentDigest, "sha256:") + "/"
}

func encodeRemoteWorkloadCacheMarkerWithReceipt(entry remoteWorkloadCacheEntry, receiptDigest string) []byte {
	var data []byte
	data = appendRemoteWorkloadCacheField(data, remoteWorkloadCacheHeader, strconv.Itoa(remoteWorkloadCacheSchemaVersion))
	data = appendRemoteWorkloadCacheField(data, "environment", entry.environmentDigest)
	data = appendRemoteWorkloadCacheField(data, "identity", entry.identityDigest)
	data = appendRemoteWorkloadCacheField(data, "execution", entry.executionDigest)
	data = appendRemoteWorkloadCacheField(data, "input", entry.inputDigest)
	data = appendRemoteWorkloadCacheField(data, "status", string(gate.ResultStatusPassed))
	data = appendRemoteWorkloadCacheField(data, "receipt", receiptDigest)
	return data
}

// encodeRemoteWorkloadCacheReceipt keeps exact execution provenance separate
// from the reusable semantic PASS marker.
func encodeRemoteWorkloadCacheReceipt(entry remoteWorkloadCacheEntry) []byte {
	var data []byte
	data = appendRemoteWorkloadCacheField(data, remoteWorkloadCacheHeader, "receipt/v1")
	data = appendRemoteWorkloadCacheField(data, "identity", entry.identityDigest)
	data = appendRemoteWorkloadCacheField(data, "commit", entry.provenance.commit)
	data = appendRemoteWorkloadCacheField(data, "tree", entry.provenance.tree)
	data = appendRemoteWorkloadCacheField(data, "profile", entry.provenance.profile)
	data = appendRemoteWorkloadCacheField(data, "entrypoint", entry.provenance.entrypoint)
	data = appendRemoteWorkloadCacheField(data, "runner-image", entry.provenance.runnerImage)
	data = appendRemoteWorkloadCacheField(data, "runner", entry.provenance.runnerIdentityDigest)
	data = appendRemoteWorkloadCacheField(data, "runner-config", entry.provenance.runnerConfigDigest)
	data = appendRemoteWorkloadCacheField(data, "gate-binary", entry.provenance.gateBinarySHA256)
	data = appendRemoteWorkloadCacheField(data, "runtime-seed", entry.provenance.runtimeSeedSHA256)
	data = appendRemoteWorkloadCacheField(data, "policy", entry.provenance.policyDigest)
	data = appendRemoteWorkloadCacheField(data, "baseline-manifest", entry.provenance.baselineManifest)
	data = appendRemoteWorkloadCacheField(data, "anchor-generation", strconv.FormatUint(entry.provenance.anchorGeneration, 10))
	data = appendRemoteWorkloadCacheField(data, "anchor-manifest", entry.provenance.anchorManifest)
	data = appendRemoteWorkloadCacheField(data, "anchor-commit", entry.provenance.anchorCommit)
	data = appendRemoteWorkloadCacheField(data, "anchor-tree", entry.provenance.anchorTree)
	data = appendRemoteWorkloadCacheField(data, "receipt-nonce", entry.receiptNonce)
	return data
}
