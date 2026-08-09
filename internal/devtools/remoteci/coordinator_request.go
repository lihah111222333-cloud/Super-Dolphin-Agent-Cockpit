package remoteci

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

// buildShardRequestsWithCompileGroups 将冻结计划中的 compile group 精确投影
// 到所属 shard，并把同一份 manifest 身份写入 request digest。group 不得跨
// shard，也不得让 exact selector 静默退回逐 selector go test。
func buildShardRequestsWithCompileGroups(
	sourcePrefix string,
	jobID string,
	shards []gate.ContainerShard,
	resources []shardresource.Class,
	materialization SourceMaterialization,
	bundleKey string,
	bundleDigest string,
	bundleSize int64,
	manifestKey string,
	manifestDigest string,
	input RunInput,
	compileGroups []gate.CompileGroup,
) ([]ShardRequest, []string, []string, error) {
	if len(resources) != len(shards) {
		return nil, nil, nil, errors.New("remote CI shard resource classes are incomplete")
	}
	if err := validateCompileGroupPlan(compileGroups); err != nil {
		return nil, nil, nil, err
	}
	requests := make([]ShardRequest, len(shards))
	keys := make([]string, len(shards))
	bootstrapKeys := make([]string, len(shards))
	jobPrefix := sourcePrefix + jobID + "/"
	for index, shard := range shards {
		projectedGroups, err := projectCompileGroupsForShard(shard, compileGroups)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("remote shard %d compile groups: %w", shard.Index, err)
		}
		manifestRequest := ShardRequest{
			Profile: shard.Profile, PlanDigest: shard.PlanDigest, ShardIdentity: shard.IdentityDigest,
			SourceTreeSHA: shard.SourceTreeSHA, GateIDs: slices.Clone(shard.GateIDs), CompileGroups: projectedGroups,
		}
		shardManifestDigest, err := manifestRequest.ComputeShardExecutionManifestDigest()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("remote shard %d execution manifest: %w", shard.Index, err)
		}
		requests[index] = ShardRequest{
			SchemaVersion: ShardRequestSchemaVersion, JobID: jobID, ShardIdentity: shard.IdentityDigest,
			AgentTokenDigest: input.AgentTokenDigest,
			Profile:          shard.Profile, PlanDigest: shard.PlanDigest, SourceTreeSHA: shard.SourceTreeSHA,
			BaselineManifest:     input.BaselineManifestDigest,
			ImageCacheSnapshotID: input.ImageCacheSnapshotID,
			OCIProjectCache:      cloneBaselineOCIProjectCache(input.OCIProjectCache),
			RunnerBaseTree:       input.RunnerBaseTree,
			BaselineRuntimeImage: input.RunnerImage, BaselineToolchainDigest: input.ToolchainDigest,
			Source:                       materialization.Manifest.Source,
			SourceBundleKey:              bundleKey,
			SourceBundleSHA256:           bundleDigest,
			SourceBundleSize:             bundleSize,
			ManifestKey:                  manifestKey,
			ManifestSHA256:               manifestDigest,
			CandidateGateSourceSHA256:    input.CandidateGateSourceSHA256,
			CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256,
			GateIDs:                      slices.Clone(shard.GateIDs),
			CompileGroups:                projectedGroups,
			ShardExecutionManifestDigest: shardManifestDigest,
			Calibration:                  input.Calibration,
			ResourceClass:                resources[index],
		}
		if input.Calibration {
			class := input.CalibrationResource
			requests[index].CalibrationResource = &class
		}
		if err := requests[index].Validate(); err != nil {
			return nil, nil, nil, err
		}
		_, requestDigest, err := EncodeShardRequest(requests[index])
		if err != nil {
			return nil, nil, nil, fmt.Errorf("remote shard %d request: %w", shard.Index, err)
		}
		_, bootstrapDigest, err := EncodeBootstrapShardRequest(requests[index])
		if err != nil {
			return nil, nil, nil, fmt.Errorf("remote shard %d bootstrap request: %w", shard.Index, err)
		}
		keys[index] = fmt.Sprintf("%s%s.request.json", jobPrefix, requestDigest)
		bootstrapKeys[index] = fmt.Sprintf("%s%s.bootstrap.request.json", jobPrefix, bootstrapDigest)
	}
	return requests, keys, bootstrapKeys, nil
}

// validateCompileGroupPlan 校验 compile group 身份和全局 workload 唯一覆盖。
func validateCompileGroupPlan(groups []gate.CompileGroup) error {
	seenGroups := make(map[string]struct{}, len(groups))
	seenWorkloads := make(map[gate.GateID]string)
	for index, group := range groups {
		if err := group.Validate(); err != nil {
			return fmt.Errorf("compile_groups[%d]: %w", index, err)
		}
		if _, duplicate := seenGroups[group.GroupID]; duplicate {
			return fmt.Errorf("compile_groups[%d] duplicates group %q", index, group.GroupID)
		}
		seenGroups[group.GroupID] = struct{}{}
		for _, workloadID := range group.WorkloadIDs {
			if previous, duplicate := seenWorkloads[workloadID]; duplicate {
				return fmt.Errorf("compile workload %q belongs to groups %q and %q", workloadID, previous, group.GroupID)
			}
			seenWorkloads[workloadID] = group.GroupID
		}
	}
	return nil
}

// projectCompileGroupsForShard 返回一个 shard 完整拥有的有序 compile group。
func projectCompileGroupsForShard(shard gate.ContainerShard, groups []gate.CompileGroup) ([]gate.CompileGroup, error) {
	gateSet := make(map[gate.GateID]struct{}, len(shard.GateIDs))
	for _, gateID := range shard.GateIDs {
		gateSet[gateID] = struct{}{}
	}
	projected := make([]gate.CompileGroup, 0, len(groups))
	grouped := make(map[gate.GateID]struct{})
	for _, group := range gate.SortCompileGroupsByID(groups) {
		projectedGroup, included, err := projectCompileGroupForShard(group, gateSet)
		if err != nil {
			return nil, err
		}
		if !included {
			continue
		}
		projected = append(projected, projectedGroup)
		markProjectedCompileGroupWorkloads(grouped, projectedGroup)
	}
	if err := validateProjectedCompileGroupCoverage(shard.GateIDs, grouped); err != nil {
		return nil, err
	}
	if len(projected) > 1 {
		return nil, fmt.Errorf("shard %d must contain exactly one compile group (found %d)", shard.Index, len(projected))
	}
	return projected, nil
}

func projectCompileGroupForShard(group gate.CompileGroup, gateSet map[gate.GateID]struct{}) (gate.CompileGroup, bool, error) {
	memberCount := 0
	for _, workloadID := range group.WorkloadIDs {
		if _, ok := gateSet[workloadID]; ok {
			memberCount++
		}
	}
	if memberCount == 0 {
		return gate.CompileGroup{}, false, nil
	}
	if memberCount != len(group.WorkloadIDs) {
		return gate.CompileGroup{}, false, fmt.Errorf("group %q crosses shard boundary", group.GroupID)
	}
	return group, true, nil
}

func markProjectedCompileGroupWorkloads(grouped map[gate.GateID]struct{}, group gate.CompileGroup) {
	for _, workloadID := range group.WorkloadIDs {
		grouped[workloadID] = struct{}{}
	}
}

func validateProjectedCompileGroupCoverage(gateIDs []gate.GateID, grouped map[gate.GateID]struct{}) error {
	for _, gateID := range gateIDs {
		if !gate.CompileGroupWorkloadSupported(gateID) {
			continue
		}
		if _, ok := grouped[gateID]; !ok {
			return fmt.Errorf("exact selector %q has no projected compile group", gateID)
		}
	}
	return nil
}

const (
	remoteShardPrivateCachePath    = "/workspace/work/go-cache"
	remoteShardCacheMetricsMarker  = "{{CACHE_METRICS_PATH}}"
	remoteShardBootstrapVolumeName = "bootstrap-config"
	remoteShardBootstrapMountPath  = "/run/super-dolphin/bootstrap"
	remoteShardBootstrapFilePath   = "bootstrap.sh"
	remoteShardBootstrapPath       = remoteShardBootstrapMountPath + "/" + remoteShardBootstrapFilePath
	remoteCandidateGateSourceEnv   = "SUPER_DOLPHIN_REMOTE_CANDIDATE_GATE_SOURCE_SHA256"
	remoteCandidateGateToolEnv     = "SUPER_DOLPHIN_REMOTE_CANDIDATE_GATE_TOOLCHAIN_SHA256"
	remoteShardBootstrapTemplateSH = `set -eu
accepted_gate="/super-dolphin-gate"
"$accepted_gate" _remote-materialize
chmod -R a+rX /workspace/source
private_cache="/workspace/work/go-cache"
cache_metrics="{{CACHE_METRICS_PATH}}"
private_mod_cache="/tmp/init-go-mod-cache"
built_gate="/workspace/work/bin/super-dolphin-gate"
candidate_gate_source="${SUPER_DOLPHIN_REMOTE_CANDIDATE_GATE_SOURCE_SHA256:?}"
candidate_gate_toolchain="${SUPER_DOLPHIN_REMOTE_CANDIDATE_GATE_TOOLCHAIN_SHA256:?}"
mkdir -p "$private_cache" "$private_mod_cache" "$(dirname "$built_gate")"
chmod 0700 "$private_cache" "$private_mod_cache"
"$accepted_gate" worker go-module-overlay /opt/super-dolphin-gate/runtime/go-mod-cache "$private_mod_cache"
started="$(date +%s%3N)"
cd /workspace/source
cache_proxy="$accepted_gate worker go-cache-proxy --seed /opt/super-dolphin/cache/go-build --private $private_cache --metrics $cache_metrics"
gate_ldflags="-X main.gateSourceDigest=$candidate_gate_source -X main.gateToolchainDigest=$candidate_gate_toolchain"
env GOCACHE="$private_cache" GOCACHEPROG="$cache_proxy" GOMODCACHE="$private_mod_cache" GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=amd64 /usr/local/go/bin/go build -mod=mod -trimpath -buildvcs=false -ldflags "$gate_ldflags" -o "$built_gate" ./cmd/super-dolphin-gate
test -x "$built_gate"
candidate_gate_identity="$("$built_gate" worker cli-identity; identity_rc=$?; printf x; exit "$identity_rc")"
candidate_gate_identity="${candidate_gate_identity%x}"
expected_gate_identity="$(printf 'gate_source_sha256=%s\nplatform=linux/amd64\ntoolchain_digest=%s\nx' "$candidate_gate_source" "$candidate_gate_toolchain")"
expected_gate_identity="${expected_gate_identity%x}"
test "$candidate_gate_identity" = "$expected_gate_identity"
finished="$(date +%s%3N)"
printf 'SUPER_DOLPHIN_SHARD_COMPILE started_at_unix_ms=%s completed_at_unix_ms=%s duration_ms=%s cache_metrics=%s\n' "$started" "$finished" "$((finished-started))" "$cache_metrics"
test -n "${SUPER_DOLPHIN_REMOTE_FULL_REQUEST_KEY:-}"
test -n "${SUPER_DOLPHIN_REMOTE_FULL_REQUEST_SHA256:-}"
test -n "${SUPER_DOLPHIN_REMOTE_FULL_MANIFEST_DIGEST:-}"
"$built_gate" _remote-install-manifest`
	remoteWritableTempMountPath = "/tmp"
	remoteInitSearchPath        = gate.ExecutorRuntimeSeedRoot + "/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
)

func remoteShardBootstrapSH() string {
	return strings.ReplaceAll(
		remoteShardBootstrapTemplateSH,
		remoteShardCacheMetricsMarker,
		gate.GoBuildCacheProxyMetricsPath(remoteShardPrivateCachePath),
	)
}

// validateContentAddressedShardRequestKey 确保请求对象键精确绑定 job、摘要和固定后缀。
func validateContentAddressedShardRequestKey(jobID, key, digest, suffix string) error {
	if path.Base(path.Dir(key)) != jobID || path.Base(key) != digest+suffix {
		return fmt.Errorf("remote shard request key %q is not content addressed for job %q", key, jobID)
	}
	return nil
}

// createRequest 将已校验的分片请求、候选镜像及资源绑定为唯一的 OCI-backed ECI 容器组。
// init 容器只负责物化和构建候选 CLI；worker 使用只读源码与同一 ImageCache 快照执行该分片。
func (coordinator *Coordinator) createRequest(
	jobID string,
	shard gate.ContainerShard,
	resources shardresource.Class,
	bootstrapRequestKey string,
	bootstrapRequestDigest string,
	fullRequestKey string,
	fullRequestDigest string,
	manifestDigest string,
	input RunInput,
) eci.CreateRequest {
	groupName := fmt.Sprintf("sdci-%s-s%02d", strings.TrimPrefix(jobID, "job-"), shard.Index)
	initContainer := eci.InitContainer{
		Name:    "materializer",
		Command: []string{"/bin/sh"},
		Args:    []string{remoteShardBootstrapPath},
		Environment: map[string]string{
			"PATH":                                   remoteInitSearchPath,
			"SUPER_DOLPHIN_RUNTIME_ROOT":             gate.ExecutorRuntimeSeedRoot,
			"SUPER_DOLPHIN_REMOTE_WORKER_ROLE":       coordinator.config.WorkerRoleName,
			"SUPER_DOLPHIN_REMOTE_OSS_ENDPOINT":      coordinator.config.InternalOSSEndpoint,
			"SUPER_DOLPHIN_REMOTE_OSS_BUCKET":        coordinator.config.Bucket,
			"SUPER_DOLPHIN_REMOTE_REQUEST_KEY":       bootstrapRequestKey,
			"SUPER_DOLPHIN_REMOTE_REQUEST_SHA256":    bootstrapRequestDigest,
			FullRequestKeyEnvironment:                fullRequestKey,
			FullRequestSHA256Environment:             fullRequestDigest,
			FullManifestDigestEnvironment:            manifestDigest,
			"SUPER_DOLPHIN_REMOTE_SHARD_IDENTITY":    shard.IdentityDigest,
			gate.ExecutorAgentTokenDigestEnvironment: input.AgentTokenDigest,
			remoteCandidateGateSourceEnv:             input.CandidateGateSourceSHA256,
			remoteCandidateGateToolEnv:               input.CandidateGateToolchainSHA256,
			"TMPDIR":                                 remoteWritableTempMountPath,
		},
	}
	initMounts := []eci.VolumeMount{
		{Name: "source-data", MountPath: gate.ExecutorSourcePath},
		{Name: "work-data", MountPath: gate.ExecutorWorkRoot},
		{Name: "temp-data", MountPath: remoteWritableTempMountPath},
		{Name: remoteShardBootstrapVolumeName, MountPath: remoteShardBootstrapMountPath, ReadOnly: true},
	}
	mainEnvironment := remoteWorkerEnvironment(coordinator.config.WorkerTimeout, input.AgentTokenDigest)
	mainMounts := []eci.VolumeMount{
		{Name: "source-data", MountPath: gate.ExecutorSourcePath, ReadOnly: true},
		{Name: "work-data", MountPath: gate.ExecutorWorkRoot},
		{Name: "temp-data", MountPath: remoteWritableTempMountPath},
	}
	return eci.CreateRequest{
		ContainerGroupName: groupName, ContainerName: "worker",
		ImageCacheSnapshotID: input.ExecutionImageCacheSnapshotID,
		MainImage:            input.ExecutionRunnerImage,
		InitImage:            input.ExecutionRunnerImage,
		ImageCacheOnly:       input.ImageCacheOnly,
		Resources:            eci.Resources{CPU: resources.VCPU, MemoryGiB: resources.MemoryGiB},
		Command:              remoteWorkerSupervisorCommand(gate.ExecutorWorkRoot + "/bin/super-dolphin-gate"),
		Args: []string{
			"worker", "run-shard", "--profile", string(shard.Profile), "--plan-digest", shard.PlanDigest,
			"--manifest-path", gate.ExecutorShardExecutionManifestPath,
			"--manifest-digest", manifestDigest,
		},
		Environment:   mainEnvironment,
		Tags:          map[string]string{"super-dolphin-job": jobID, "super-dolphin-shard": fmt.Sprintf("%d", shard.Index)},
		InitContainer: initContainer,
		SourceVolume:  eci.EmptyDirVolume{Name: "source-data"},
		WorkVolume:    eci.EmptyDirVolume{Name: "work-data"},
		TempVolume:    eci.EmptyDirVolume{Name: "temp-data"},
		ConfigFileVolumes: []eci.ConfigFileVolume{{
			Name:        remoteShardBootstrapVolumeName,
			DefaultMode: eci.ConfigFileVolumeSafeMode,
			ConfigFileToPath: []eci.ConfigFileToPath{{
				Path:    remoteShardBootstrapFilePath,
				Content: remoteShardBootstrapSH(),
				Mode:    eci.ConfigFileVolumeSafeMode,
			}},
		}},
		MainVolumeMounts: mainMounts,
		InitVolumeMounts: initMounts,
	}
}

// validateShardResourceBinding 确保请求声明的实际资源类与 ECI 规格严格一致。
// 校准模式还必须绑定固定规格，normal 不得夹带 calibration_resource。
func validateShardResourceBinding(resources shardresource.Class, request ShardRequest) error {
	if request.ResourceClass != resources {
		return errors.New("remote shard request resource_class drifted")
	}
	if !request.Calibration {
		if request.CalibrationResource != nil {
			return errors.New("non-calibration shard request carries calibration resources")
		}
		return nil
	}
	if request.CalibrationResource == nil {
		return errors.New("calibration shard request resources are required")
	}
	class := *request.CalibrationResource
	if class.ID == "" || class != resources {
		return errors.New("calibration shard request resources drifted")
	}
	return nil
}

// remoteWorkerEnvironment 绑定 worker 入口所需的运行时根、固定 temp-data 根与单目标超时。
// compile-group batch 会在该根下进一步创建唯一的短 0700 TMPDIR/GOTMPDIR 子目录。
func remoteWorkerEnvironment(timeout time.Duration, agentTokenDigest string) map[string]string {
	return map[string]string{
		gate.ExecutorWorkloadTimeoutEnvironment:  timeout.String(),
		gate.ExecutorAgentTokenDigestEnvironment: agentTokenDigest,
		"SUPER_DOLPHIN_RUNTIME_ROOT":             gate.ExecutorRuntimeSeedRoot,
		"TMPDIR":                                 remoteWritableTempMountPath,
	}
}
