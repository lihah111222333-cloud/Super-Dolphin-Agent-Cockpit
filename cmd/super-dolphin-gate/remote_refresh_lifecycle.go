package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/datacache"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func carryRemoteBaselineHistory(state *remoteci.BaselineState, accepted remoteci.BaselineState) {
	if accepted.SchemaVersion == 0 {
		return
	}
	previousAnchor := accepted.CurrentAnchorRef()
	state.PreviousAnchor = &previousAnchor
	state.PreviousDeltas = accepted.DeltaRefs()
	if accepted.PreviousAnchor != nil && !remoteBaselineAnchorIsLive(*accepted.PreviousAnchor, *state) {
		retiredAnchor := *accepted.PreviousAnchor
		state.RetiredAnchor = &retiredAnchor
	}
	for _, delta := range accepted.PreviousDeltas {
		if !remoteBaselineDeltaIsLive(delta, *state) {
			state.RetiredDeltas = append(state.RetiredDeltas, delta)
		}
	}
}

func remoteBaselineAnchorIsLive(candidate remoteci.BaselineCacheRef, state remoteci.BaselineState) bool {
	if sameRemoteBaselineAnchor(candidate, state.Anchor) {
		return true
	}
	return state.PreviousAnchor != nil && sameRemoteBaselineAnchor(candidate, *state.PreviousAnchor)
}

func sameRemoteBaselineAnchor(left, right remoteci.BaselineCacheRef) bool {
	return left.Generation == right.Generation && left.DataCacheID == right.DataCacheID &&
		left.DataCacheBucket == right.DataCacheBucket && left.DataCachePath == right.DataCachePath
}

// remoteBaselineDeltaIsLive 判断候选 Delta 是否仍被当前或上一条链引用。
func remoteBaselineDeltaIsLive(candidate remoteci.BaselineDeltaRef, state remoteci.BaselineState) bool {
	for _, deltas := range [][]remoteci.BaselineDeltaRef{state.Deltas, state.PreviousDeltas} {
		for _, delta := range deltas {
			if candidate.Generation == delta.Generation && candidate.SourceObjectPrefix == delta.SourceObjectPrefix &&
				candidate.ManifestDigest == delta.ManifestDigest {
				return true
			}
		}
	}
	return false
}

// cleanupUnacceptedRemoteArtifacts 删除未被接受 generation 的 OSS 工件。
func cleanupUnacceptedRemoteArtifacts(resultErr *error, store remoteBaselineOSSStore, prefix string, accepted *bool) {
	if *accepted {
		return
	}
	ctx, cancel := gateprivate.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.DeletePrefix(ctx, prefix); err != nil {
		*resultErr = errors.Join(*resultErr, infrastructureError("delete unaccepted remote baseline artifacts: %v", err))
	}
}

// cleanupRemoteBaselineSeed 删除已结束或失败的 seed 容器组。
func cleanupRemoteBaselineSeed(resultErr *error, runtime *eci.Client, groupID string) {
	ctx, cancel := gateprivate.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := runtime.DeleteContainerGroup(ctx, groupID); err != nil {
		*resultErr = errors.Join(*resultErr, infrastructureError("delete remote baseline seed: %v", err))
	}
}

// cleanupUnacceptedRemoteCache 删除未被接受的 DataCache。
func cleanupUnacceptedRemoteCache(resultErr *error, client remoteBaselineDataCacheClient, cache datacache.DataCache, accepted *bool) {
	if *accepted {
		return
	}
	ctx, cancel := gateprivate.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := client.Delete(ctx, cache.ID, cache.Bucket, cache.Path); err != nil {
		*resultErr = errors.Join(*resultErr, infrastructureError("delete unaccepted remote baseline DataCache: %v", err))
	}
}

// resolveRemoteSqruffArtifact 选择与目标平台匹配的 Sqruff 工件及摘要。
func resolveRemoteSqruffArtifact(args []string, platform string) (string, string, error) {
	suffix := "AMD64"
	if platform == "linux/arm64" {
		suffix = "ARM64"
	}
	values := make(map[string]string, len(args))
	for _, argument := range args {
		key, value, ok := strings.Cut(argument, "=")
		if ok {
			values[key] = value
		}
	}
	artifactURL := values["SQRUFF_ARCHIVE_URL_"+suffix]
	digest := values["SQRUFF_ARCHIVE_SHA256_"+suffix]
	if strings.TrimSpace(artifactURL) == "" || len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
		return "", "", errors.New("remote baseline Sqruff artifact is invalid")
	}
	return artifactURL, digest, nil
}

func newRemoteBaselineOSSStore(config remoteRunConfig) (remoteBaselineOSSStore, error) {
	return oss.NewCLI(oss.Config{Binary: config.AliyunCLI, Bucket: config.OSS.Bucket, Endpoint: config.OSS.Endpoint, Profile: config.CredentialProfile, Prefix: config.OSS.BaselinePrefix})
}

func (lock *remoteBaselineRefreshLock) close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

func resolveRemoteRef(repositoryRoot, remote, ref string) (string, error) {
	output, err := remoteGitOutput(repositoryRoot, "ls-remote", "--exit-code", remote, ref)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(output)
	if len(fields) != 2 || fields[1] != ref {
		return "", errors.New("remote Git ref response is ambiguous")
	}
	return fields[0], nil
}

func remoteBaselineStatePath(configPath, explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	extension := filepath.Ext(configPath)
	return strings.TrimSuffix(configPath, extension) + ".baseline-state.json"
}

func loadAcceptedRemoteBaseline(configPath, statePath, ledgerPath string) (remoteci.BaselineState, error) {
	_ = configPath
	_ = statePath
	state, err := loadRemoteBaselineState(ledgerPath, false)
	if err != nil {
		return remoteci.BaselineState{}, err
	}
	return state, state.Validate()
}

func remoteBaselineResourceName(generation uint64) string {
	return fmt.Sprintf("sdci-baseline-%d", generation)
}

func remoteBaselineSourcePrefix(config remoteRunConfig, generation uint64) string {
	return config.OSS.BaselinePrefix + strconv.FormatUint(generation, 10) + "/"
}

func remoteBaselineInputPrefix(config remoteRunConfig, generation uint64) string {
	return remoteBaselineSourcePrefix(config, generation) + "input/"
}

func remoteBaselineOutputPrefix(config remoteRunConfig, generation uint64) string {
	return remoteBaselineSourcePrefix(config, generation) + "output/"
}

func remoteBaselineCachePath(config remoteRunConfig, generation uint64) string {
	return config.DataCache.PathPrefix + "/" + strconv.FormatUint(generation, 10)
}

// remoteBaselineDirectCachePath 把只读 Go 构建缓存与基线 Anchor 放入彼此隔离的 DataCache 路径。
func remoteBaselineDirectCachePath(config remoteRunConfig, generation uint64) string {
	return config.DataCache.PathPrefix + "/direct-cache/" + strconv.FormatUint(generation, 10)
}

func remoteBaselineDirectCacheOutputPrefix(stage remoteBaselineArtifactStage) string {
	return stage.outputPrefix + "direct-cache/"
}

func remoteBaselineCacheClientToken(generation uint64, sizeGiB int, manifest remoteci.BaselineManifest) (string, error) {
	payload, err := json.Marshal(struct {
		SizeGiB  int                       `json:"size_gib"`
		Manifest remoteci.BaselineManifest `json:"manifest"`
	}{SizeGiB: sizeGiB, Manifest: manifest})
	if err != nil {
		return "", err
	}
	return remoteBaselineClientToken("cache", strconv.FormatUint(generation, 10), payload), nil
}

func remoteBaselineClientToken(kind, generation string, payload []byte) string {
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sdci-%s-%s-%x", kind, generation, sum[:8])
}

func remoteBaselineRenewToken(generation uint64, now time.Time) string {
	return fmt.Sprintf("sdci-renew-%d-%s", generation, now.UTC().Format("20060102"))
}

func remoteBaselineDirectRenewToken(generation uint64, now time.Time) string {
	return fmt.Sprintf("sdci-renew-direct-%d-%s", generation, now.UTC().Format("20060102"))
}
