package remoteci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	remoteWorkloadCacheSchemaVersion                  = 3
	remoteWorkloadCacheEnvironmentSchemaVersion       = 4
	remoteWorkloadCacheLegacyEnvironmentSchemaVersion = 2
	remoteWorkloadCacheHeader                         = "SUPER_DOLPHIN_REMOTE_WORKLOAD_CACHE"
	remoteWorkloadCacheMarkerMaxBytes                 = 4 << 10
	remoteCompatiblePassCandidateLimit                = 4
)

type remoteWorkloadCacheEntry struct {
	workloadID        string
	executionDigest   string
	inputDigest       string
	environmentDigest string
	identityDigest    string
	prefix            string
	receiptPrefix     string
	key               string
	receiptKey        string
	receiptNonce      string
	provenance        remoteWorkloadCacheProvenance
}

// remoteWorkloadCacheProvenance records the exact execution context without
// making coordinator-only refreshes part of the reusable PASS identity.
type remoteWorkloadCacheProvenance struct {
	commit                string
	tree                  string
	profile               string
	entrypoint            string
	runnerImage           string
	runnerIdentityDigest  string
	runnerConfigDigest    string
	gateBinarySHA256      string
	runtimeSeedSHA256     string
	policyDigest          string
	baselineManifest      string
	ociProjectCacheImage  string
	ociProjectCacheDigest string
}

func remoteWorkloadCacheProvenanceForInput(input RunInput) remoteWorkloadCacheProvenance {
	return remoteWorkloadCacheProvenance{
		commit:                input.Commit,
		tree:                  input.Tree,
		profile:               string(input.Profile),
		entrypoint:            string(input.Entrypoint),
		runnerImage:           input.RunnerImage,
		runnerIdentityDigest:  input.RunnerIdentityDigest,
		runnerConfigDigest:    input.RunnerConfigDigest,
		gateBinarySHA256:      input.GateBinarySHA256,
		runtimeSeedSHA256:     input.RuntimeSeedSHA256,
		policyDigest:          input.PolicyDigest,
		baselineManifest:      input.BaselineManifestDigest,
		ociProjectCacheImage:  input.OCIProjectCache.Image,
		ociProjectCacheDigest: input.OCIProjectCache.ContentManifestSHA256,
	}
}

// remoteWorkloadCacheEntries 为每个 workload 生成内容寻址的通过缓存对象身份。
func remoteWorkloadCacheEntries(
	prefix string,
	workloads []gate.Workload,
	inputDigests map[string]string,
	input RunInput,
) ([]remoteWorkloadCacheEntry, error) {
	if input.OCIProjectCache == nil {
		return nil, errors.New("remote workload cache OCI project cache is required")
	}
	environmentDigest, err := remoteWorkloadCacheEnvironmentDigest(input)
	if err != nil {
		return nil, err
	}
	return remoteWorkloadCacheEntriesForEnvironment(
		prefix, workloads, inputDigests, environmentDigest, remoteWorkloadCacheProvenanceForInput(input),
	)
}

func remoteWorkloadCacheEntriesForEnvironment(
	prefix string,
	workloads []gate.Workload,
	inputDigests map[string]string,
	environmentDigest string,
	provenance remoteWorkloadCacheProvenance,
) ([]remoteWorkloadCacheEntry, error) {
	environmentPrefix := prefix + strings.TrimPrefix(environmentDigest, "sha256:") + "/"
	receiptPrefix := remoteWorkloadCacheReceiptPrefix(prefix, environmentDigest)
	entries := make([]remoteWorkloadCacheEntry, len(workloads))
	for index, workload := range workloads {
		inputDigest, ok := inputDigests[workload.ID]
		if !ok || !remoteDigestPattern.MatchString(inputDigest) {
			return nil, fmt.Errorf("workload cache input digest for %q is missing", workload.ID)
		}
		executionDigest, err := gate.WorkloadExecutionDigest(workload.ID)
		if err != nil {
			return nil, err
		}
		identityDigest := remoteWorkloadCacheIdentityDigest(environmentDigest, executionDigest, inputDigest)
		entry := remoteWorkloadCacheEntry{
			workloadID: workload.ID, executionDigest: executionDigest, inputDigest: inputDigest,
			environmentDigest: environmentDigest, identityDigest: identityDigest,
			prefix:        environmentPrefix,
			receiptPrefix: receiptPrefix,
			key:           environmentPrefix + strings.TrimPrefix(identityDigest, "sha256:") + ".pass",
			receiptNonce:  remoteWorkloadCacheDefaultReceiptNonce(identityDigest),
			provenance:    provenance,
		}
		entry.receiptKey = remoteWorkloadCacheReceiptKey(entry, remoteWorkloadCacheReceiptDigest(entry))
		entries[index] = entry
	}
	return entries, nil
}

func remoteLegacyWorkloadCacheEntries(
	prefix string,
	entries []remoteWorkloadCacheEntry,
	input RunInput,
) ([]remoteWorkloadCacheEntry, error) {
	environmentDigest, err := remoteLegacyWorkloadCacheEnvironmentDigest(input)
	if err != nil {
		return nil, err
	}
	return remoteWorkloadCacheEntriesForExistingEntries(prefix, entries, environmentDigest), nil
}

func remoteWorkloadCacheEntriesForExistingEntries(
	prefix string,
	entries []remoteWorkloadCacheEntry,
	environmentDigest string,
) []remoteWorkloadCacheEntry {
	environmentPrefix := prefix + strings.TrimPrefix(environmentDigest, "sha256:") + "/"
	receiptPrefix := remoteWorkloadCacheReceiptPrefix(prefix, environmentDigest)
	projected := make([]remoteWorkloadCacheEntry, len(entries))
	for index, entry := range entries {
		identityDigest := remoteWorkloadCacheIdentityDigest(environmentDigest, entry.executionDigest, entry.inputDigest)
		entry.environmentDigest = environmentDigest
		entry.identityDigest = identityDigest
		entry.prefix = environmentPrefix
		entry.receiptPrefix = receiptPrefix
		entry.key = environmentPrefix + strings.TrimPrefix(identityDigest, "sha256:") + ".pass"
		entry.receiptNonce = remoteWorkloadCacheDefaultReceiptNonce(identityDigest)
		entry.receiptKey = remoteWorkloadCacheReceiptKey(entry, remoteWorkloadCacheReceiptDigest(entry))
		projected[index] = entry
	}
	return projected
}

func remoteWorkloadCacheEnvironmentDigest(input RunInput) (string, error) {
	if !validRemoteWorkloadCacheEnvironment(input) {
		return "", errors.New("remote workload cache environment identity is incomplete")
	}
	var material []byte
	material = appendRemoteWorkloadCacheField(material, "schema", strconv.Itoa(remoteWorkloadCacheEnvironmentSchemaVersion))
	material = appendRemoteWorkloadCacheField(material, "platform", input.Platform)
	material = appendRemoteWorkloadCacheField(material, "toolchain", input.ToolchainDigest)
	sum := sha256.Sum256(material)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func remoteLegacyWorkloadCacheEnvironmentDigest(input RunInput) (string, error) {
	if !validRemoteWorkloadCacheEnvironment(input) {
		return "", errors.New("remote workload cache environment identity is incomplete")
	}
	var material []byte
	material = appendRemoteWorkloadCacheField(material, "schema", strconv.Itoa(remoteWorkloadCacheLegacyEnvironmentSchemaVersion))
	material = appendRemoteWorkloadCacheField(material, "platform", input.Platform)
	material = appendRemoteWorkloadCacheField(material, "runner-image", input.RunnerImage)
	material = appendRemoteWorkloadCacheField(material, "worker-execution", input.RunnerIdentityDigest)
	material = appendRemoteWorkloadCacheField(material, "runner-config", input.RunnerConfigDigest)
	material = appendRemoteWorkloadCacheField(material, "policy", input.PolicyDigest)
	material = appendRemoteWorkloadCacheField(material, "gate-binary", input.GateBinarySHA256)
	material = appendRemoteWorkloadCacheField(material, "runtime-seed", input.RuntimeSeedSHA256)
	material = appendRemoteWorkloadCacheField(material, "toolchain", input.ToolchainDigest)
	sum := sha256.Sum256(material)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validRemoteWorkloadCacheEnvironment(input RunInput) bool {
	if strings.TrimSpace(input.Platform) == "" || strings.TrimSpace(input.RunnerImage) == "" {
		return false
	}
	for _, digest := range []string{
		input.RunnerIdentityDigest, input.RunnerConfigDigest, input.PolicyDigest, input.GateBinarySHA256,
		input.RuntimeSeedSHA256, input.ToolchainDigest,
	} {
		if !remoteDigestPattern.MatchString(digest) {
			return false
		}
	}
	return true
}

func remoteWorkloadCacheIdentityDigest(environmentDigest string, executionDigest string, inputDigest string) string {
	var material []byte
	material = appendRemoteWorkloadCacheField(material, "environment", environmentDigest)
	material = appendRemoteWorkloadCacheField(material, "execution", executionDigest)
	material = appendRemoteWorkloadCacheField(material, "input", inputDigest)
	sum := sha256.Sum256(material)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func remoteWorkloadCacheDefaultReceiptNonce(identityDigest string) string {
	sum := sha256.Sum256([]byte("remote-workload-cache-receipt-default\n" + identityDigest))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func appendRemoteWorkloadCacheField(destination []byte, key string, value string) []byte {
	destination = append(destination, key...)
	destination = append(destination, ' ')
	destination = append(destination, value...)
	return append(destination, '\n')
}

// loadPassedWorkloadCache 下载并严格验证当前环境中的通过标记，再投影为复用结果。
func loadPassedWorkloadCache(
	ctx context.Context,
	store ObjectStore,
	now func() time.Time,
	entries []remoteWorkloadCacheEntry,
	forceRerun bool,
) (map[string]gate.PlanGateExecution, error) {
	cached := make(map[string]gate.PlanGateExecution)
	if forceRerun || len(entries) == 0 {
		return cached, nil
	}
	prefix, err := validateRemoteWorkloadCacheEntries(entries)
	if err != nil {
		return nil, err
	}
	availableKeys, err := listPassedWorkloadCacheKeys(ctx, store, prefix)
	if err != nil {
		return nil, err
	}
	tempRoot, err := os.MkdirTemp("", "super-dolphin-pass-cache-*")
	if err != nil {
		return nil, fmt.Errorf("create passed workload cache staging root: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	matched, err := downloadPassedWorkloadCache(ctx, store, tempRoot, entries, availableKeys)
	if err != nil {
		return nil, err
	}
	return projectPassedWorkloadCache(now().UTC(), entries, matched), nil
}

func loadPassedWorkloadCacheWithLegacy(
	ctx context.Context,
	store ObjectStore,
	now func() time.Time,
	entries []remoteWorkloadCacheEntry,
	legacyEntries []remoteWorkloadCacheEntry,
	forceRerun bool,
	fallbackEntries ...[]remoteWorkloadCacheEntry,
) (map[string]gate.PlanGateExecution, error) {
	cached, err := loadPassedWorkloadCache(ctx, store, now, entries, forceRerun)
	if err != nil || forceRerun {
		return cached, err
	}
	fallbacks := append([][]remoteWorkloadCacheEntry{legacyEntries}, fallbackEntries...)
	for _, fallback := range fallbacks {
		misses := make([]remoteWorkloadCacheEntry, 0, len(fallback))
		for _, entry := range fallback {
			if _, ok := cached[entry.workloadID]; !ok {
				misses = append(misses, entry)
			}
		}
		fallbackCached, err := loadPassedWorkloadCache(ctx, store, now, misses, false)
		if err != nil {
			return nil, err
		}
		maps.Copy(cached, fallbackCached)
	}
	return cached, nil
}

func remoteWorkloadCacheMissEntries(
	entries []remoteWorkloadCacheEntry,
	cached map[string]gate.PlanGateExecution,
) []remoteWorkloadCacheEntry {
	misses := make([]remoteWorkloadCacheEntry, 0, len(entries))
	for _, entry := range entries {
		if _, ok := cached[entry.workloadID]; !ok {
			misses = append(misses, entry)
		}
	}
	return misses
}

// validateRemoteWorkloadCacheEntries 拒绝跨环境或重复的缓存身份。
func validateRemoteWorkloadCacheEntries(entries []remoteWorkloadCacheEntry) (string, error) {
	if len(entries) == 0 {
		return "", errors.New("remote workload cache entries are required")
	}
	prefix := entries[0].prefix
	expectedKeys := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.prefix != prefix {
			return "", errors.New("remote workload cache entries span multiple environments")
		}
		if remoteWorkloadCacheIdentityDigest(entry.environmentDigest, entry.executionDigest, entry.inputDigest) != entry.identityDigest {
			return "", fmt.Errorf("remote workload cache entry %q identity does not match its fields", entry.workloadID)
		}
		if entry.key != entry.prefix+strings.TrimPrefix(entry.identityDigest, "sha256:")+".pass" {
			return "", fmt.Errorf("remote workload cache entry %q key does not match its identity", entry.workloadID)
		}
		expectedReceiptPrefix := remoteWorkloadCacheReceiptPrefix(
			strings.TrimSuffix(entry.prefix, strings.TrimPrefix(entry.environmentDigest, "sha256:")+"/"),
			entry.environmentDigest,
		)
		if entry.receiptPrefix != expectedReceiptPrefix {
			return "", fmt.Errorf("remote workload cache entry %q receipt prefix does not match its environment", entry.workloadID)
		}
		if entry.receiptKey != "" && entry.receiptKey != remoteWorkloadCacheReceiptKey(entry, remoteWorkloadCacheReceiptDigest(entry)) {
			return "", fmt.Errorf("remote workload cache entry %q receipt key does not match its identity", entry.workloadID)
		}
		if err := validateRemoteWorkloadCacheKey(entry.prefix, entry.key); err != nil {
			return "", err
		}
		if _, duplicate := expectedKeys[entry.key]; duplicate {
			return "", fmt.Errorf("remote workload cache entry key %q is duplicated", entry.key)
		}
		expectedKeys[entry.key] = struct{}{}
	}
	return prefix, nil
}
