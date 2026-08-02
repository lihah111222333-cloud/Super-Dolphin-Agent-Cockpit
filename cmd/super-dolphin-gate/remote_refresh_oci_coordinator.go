package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// coordinateRemoteOCIBuild transports one accepted-snapshot delta through a
// per-build OSS prefix, then accepts only a strict receipt observed from the
// terminal ECI worker log. Neither the coordinator nor OSS is a cache layer.
func coordinateRemoteOCIBuild(ctx context.Context, config remoteRunConfig, runtime remoteOCIBuilderRuntime, request remoteci.OCIBaselineBuilderRequest, deltaArchive []byte) (result remoteci.OCIBaselineBuilderResult, returnErr error) {
	if ctx == nil || runtime == nil {
		return remoteci.OCIBaselineBuilderResult{}, errors.New("remote OCI build coordinator context and runtime are required")
	}
	if err := request.Validate(); err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("validate remote OCI builder request: %w", err)
	}
	if int64(len(deltaArchive)) != request.DeltaArchiveSize {
		return remoteci.OCIBaselineBuilderResult{}, errors.New("remote OCI builder delta size does not match request")
	}
	store, err := oss.NewCLI(oss.Config{Binary: config.AliyunCLI, Bucket: config.OSS.Bucket, Endpoint: config.OSS.Endpoint, Profile: config.CredentialProfile, Prefix: config.OSS.SourcePrefix})
	if err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("create remote OCI OSS client: %w", err)
	}
	deltaArchiveKey, requestKey := request.DeltaArchiveKey, request.JobKey
	groupID := ""
	defer func() {
		cleanupErr := cleanupRemoteOCIBuild(groupID, runtime)
		cleanupErr = errors.Join(cleanupErr, cleanupRemoteOCIBuildObjects(store, path.Dir(requestKey)))
		if cleanupErr != nil {
			returnErr = errors.Join(returnErr, cleanupErr)
		}
	}()

	tempRoot, err := os.MkdirTemp("", "super-dolphin-remote-oci-*")
	if err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("create remote OCI request workspace: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(tempRoot)) }()
	deltaArchivePath := filepath.Join(tempRoot, "source.snapshot.delta.tar")
	if err := os.WriteFile(deltaArchivePath, deltaArchive, 0o600); err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("write canonical remote OCI source snapshot delta: %w", err)
	}
	if err := store.Create(ctx, deltaArchivePath, deltaArchiveKey); err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("create remote OCI source snapshot delta object: %w", err)
	}
	data, _, err := remoteci.EncodeOCIBaselineBuilderRequest(request)
	if err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("encode remote OCI request: %w", err)
	}
	requestPath := filepath.Join(tempRoot, "request.json")
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("write remote OCI request: %w", err)
	}
	if err := store.Create(ctx, requestPath, requestKey); err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("create remote OCI request object: %w", err)
	}
	group, err := runtime.CreateContainerGroup(ctx, remoteOCIBuildCreateRequest(config, request, requestKey, deltaArchiveKey))
	if err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("create remote OCI builder ECI group: %w", err)
	}
	groupID = group.ID
	if strings.TrimSpace(groupID) == "" {
		return remoteci.OCIBaselineBuilderResult{}, errors.New("remote OCI builder ECI group ID is empty")
	}
	result, err = waitRemoteOCIBuild(ctx, runtime, groupID, request)
	if err != nil {
		return remoteci.OCIBaselineBuilderResult{}, err
	}
	if err := result.ValidateAgainst(request); err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("validate remote OCI build result: %w", err)
	}
	return result, nil
}

func remoteOCIBuildCreateRequest(config remoteRunConfig, request remoteci.OCIBaselineBuilderRequest, requestKey, deltaKey string) eci.CreateRequest {
	acceptedSnapshotDeltaPrefix := "/" + path.Dir(requestKey)
	return eci.CreateRequest{
		ContainerGroupName:   "sdoci-" + strings.TrimPrefix(request.JobID, "oci-"),
		ContainerName:        "worker",
		ImageCacheSnapshotID: request.ParentImageSnapshotID,
		MainImage:            config.OCIRefresh.BuilderWorkerImage,
		InitImage:            request.ParentImage,
		Resources:            eci.Resources{CPU: 8, MemoryGiB: 32},
		Command:              []string{"/opt/super-dolphin-gate/bin/super-dolphin-gate"},
		Args:                 []string{"_remote-build-oci-baseline"},
		Environment: map[string]string{
			remoteOCIBuildRequestPathEnv: "/source-data/request.json",
			remoteOCIBuildDeltaPathEnv:   "/source-data/source.snapshot.delta.tar",
		},
		Tags:            map[string]string{"super-dolphin-oci-build": request.JobID},
		InitContainer:   eci.InitContainer{Name: "materializer", Command: []string{"/bin/sh"}, Args: []string{"-ceu", remoteOCIInitScript(requestKey, deltaKey)}},
		BootstrapVolume: eci.OSSVolume{Bucket: config.OSS.Bucket, Endpoint: strings.TrimPrefix(config.OSS.InternalEndpoint, "https://"), Path: acceptedSnapshotDeltaPrefix, RoleName: config.WorkerRoleName},
		ExpandedVolume:  eci.EmptyDirVolume{Name: "expanded-data"}, SourceVolume: eci.EmptyDirVolume{Name: "source-data"}, WorkVolume: eci.EmptyDirVolume{Name: "work-data"}, TempVolume: eci.EmptyDirVolume{Name: "temp-data"},
		MainVolumeMounts: []eci.VolumeMount{{Name: "expanded-data", MountPath: "/opt/super-dolphin-gate", ReadOnly: true}, {Name: "source-data", MountPath: "/source-data", ReadOnly: true}, {Name: "work-data", MountPath: filepath.Dir(cicontract.SourceSnapshotRootPath)}, {Name: "temp-data", MountPath: "/tmp"}},
		InitVolumeMounts: []eci.VolumeMount{{Name: "current-gate", MountPath: "/current-gate", ReadOnly: true}, {Name: "expanded-data", MountPath: "/opt/super-dolphin-gate"}, {Name: "source-data", MountPath: "/source-data"}, {Name: "work-data", MountPath: "/work-data"}, {Name: "temp-data", MountPath: "/tmp"}},
	}
}

func remoteOCIInitScript(requestKey, deltaKey string) string {
	return fmt.Sprintf(
		"mkdir -p /opt/super-dolphin-gate/bin /work-data; cp /super-dolphin-gate /opt/super-dolphin-gate/bin/super-dolphin-gate; chmod 0555 /opt/super-dolphin-gate/bin/super-dolphin-gate; cp -a %s /work-data/root; cp %s /work-data/manifest.json; chmod -R go-w /work-data; cp /current-gate/%s /source-data/request.json; cp /current-gate/%s /source-data/source.snapshot.delta.tar",
		cicontract.SourceSnapshotRootPath,
		cicontract.SourceSnapshotManifestPath,
		path.Base(requestKey),
		path.Base(deltaKey),
	)
}

func waitRemoteOCIBuild(ctx context.Context, runtime remoteOCIBuilderRuntime, groupID string, request remoteci.OCIBaselineBuilderRequest) (remoteci.OCIBaselineBuilderResult, error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		groups, err := runtime.DescribeContainerGroups(ctx, groupID)
		if err != nil || len(groups) != 1 || groups[0].ID != groupID {
			return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("observe remote OCI builder ECI group: %w", err)
		}
		if groups[0].Status == "Succeeded" {
			log, err := runtime.DescribeContainerLog(ctx, groupID, "worker")
			if err != nil {
				return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("read remote OCI builder result log: %w", err)
			}
			return decodeRemoteOCIBuildResultLog(log, request)
		}
		if terminalRemoteOCIStatus(groups[0].Status) {
			log, logErr := runtime.DescribeContainerLog(ctx, groupID, "worker")
			return remoteci.OCIBaselineBuilderResult{}, errors.Join(fmt.Errorf("remote OCI builder terminal status %q", groups[0].Status), logErr, errors.New(log))
		}
		select {
		case <-ctx.Done():
			return remoteci.OCIBaselineBuilderResult{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func decodeRemoteOCIBuildResultLog(log string, request remoteci.OCIBaselineBuilderRequest) (remoteci.OCIBaselineBuilderResult, error) {
	var encoded string
	for line := range strings.SplitSeq(log, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), remoteOCIBuildResultPrefix); ok {
			if encoded != "" {
				return remoteci.OCIBaselineBuilderResult{}, errors.New("remote OCI builder emitted multiple results")
			}
			encoded = value
		}
	}
	if encoded == "" {
		return remoteci.OCIBaselineBuilderResult{}, errors.New("remote OCI builder result is missing from terminal log")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("decode remote OCI builder result base64: %w", err)
	}
	result, err := remoteci.DecodeOCIBaselineBuilderResult(data, request)
	if err != nil {
		return remoteci.OCIBaselineBuilderResult{}, err
	}
	return result, nil
}

func terminalRemoteOCIStatus(status string) bool {
	return status == "Succeeded" || status == "Failed" || status == "Stopped"
}

func cleanupRemoteOCIBuild(groupID string, runtime remoteOCIBuilderRuntime) error {
	if groupID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := runtime.DeleteContainerGroup(ctx, groupID); err != nil {
		return fmt.Errorf("cleanup remote OCI builder ECI group %s: %w", groupID, err)
	}
	return nil
}

// cleanupRemoteOCIBuildObjects removes the complete job-bound request/delta
// prefix even after the caller context is cancelled. OSS is transport only;
// leaving either object behind would make a cancelled or failed refresh
// replayable by a later worker.
func cleanupRemoteOCIBuildObjects(store interface {
	DeletePrefix(context.Context, string) error
}, prefix string) error {
	if store == nil || prefix == "." || strings.TrimSpace(prefix) == "" {
		return errors.New("remote OCI builder cleanup prefix is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.DeletePrefix(ctx, prefix); err != nil {
		return fmt.Errorf("cleanup remote OCI builder OSS prefix %s: %w", prefix, err)
	}
	return nil
}
