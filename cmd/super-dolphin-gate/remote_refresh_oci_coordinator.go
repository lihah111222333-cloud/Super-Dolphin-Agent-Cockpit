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
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// coordinateRemoteOCIBuild transports one canonical BuildKit context through a
// per-build OSS prefix, then accepts only a strict receipt observed from the
// terminal ECI worker log. Neither the coordinator nor OSS is a cache layer.
func coordinateRemoteOCIBuild(ctx context.Context, config remoteRunConfig, runtime remoteOCIBuilderRuntime, request remoteci.OCIBaselineBuilderRequest, contextArchive []byte) (result remoteci.OCIBaselineBuilderResult, returnErr error) {
	if ctx == nil || runtime == nil {
		return remoteci.OCIBaselineBuilderResult{}, errors.New("remote OCI build coordinator context and runtime are required")
	}
	if err := request.Validate(); err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("validate remote OCI builder request: %w", err)
	}
	if int64(len(contextArchive)) != request.SourceArchiveSize {
		return remoteci.OCIBaselineBuilderResult{}, errors.New("remote OCI builder context size does not match request")
	}
	store, err := oss.NewCLI(oss.Config{Binary: config.AliyunCLI, Bucket: config.OSS.Bucket, Endpoint: config.OSS.Endpoint, Profile: config.CredentialProfile, Prefix: config.OSS.BaselinePrefix})
	if err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("create remote OCI OSS client: %w", err)
	}
	contextKey, requestKey := request.ContextKey, request.JobKey
	groupID := ""
	defer func() {
		cleanupErr := cleanupRemoteOCIBuild(groupID, runtime)
		if cleanupErr != nil {
			returnErr = errors.Join(returnErr, cleanupErr)
		}
	}()

	tempRoot, err := os.MkdirTemp("", "super-dolphin-remote-oci-*")
	if err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("create remote OCI request workspace: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(tempRoot)) }()
	contextPath := filepath.Join(tempRoot, "context.context.tar")
	if err := os.WriteFile(contextPath, contextArchive, 0o600); err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("write canonical remote OCI context: %w", err)
	}
	if err := store.Create(ctx, contextPath, contextKey); err != nil {
		return remoteci.OCIBaselineBuilderResult{}, fmt.Errorf("create remote OCI context object: %w", err)
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
	group, err := runtime.CreateContainerGroup(ctx, remoteOCIBuildCreateRequest(config, request, requestKey, contextKey))
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

func remoteOCIBuildCreateRequest(config remoteRunConfig, request remoteci.OCIBaselineBuilderRequest, requestKey, contextKey string) eci.CreateRequest {
	bootstrapPath := "/" + path.Dir(requestKey)
	return eci.CreateRequest{
		ContainerGroupName: "sdoci-" + strings.TrimPrefix(request.JobID, "oci-"),
		ContainerName:      "worker",
		ImageCacheID:       request.ImageCacheSnapshotID,
		MainImage:          config.OCICache.RemoteBuilderImage,
		InitImage:          request.ParentImage,
		Resources:          eci.Resources{CPU: 8, MemoryGiB: 32},
		Command:            []string{"/opt/super-dolphin-gate/bin/super-dolphin-gate"},
		Args:               []string{"_remote-build-oci-baseline"},
		Environment: map[string]string{
			remoteOCIBuildRequestPathEnv: "/source-data/request.json",
			remoteOCIBuildContextPathEnv: "/source-data/context.tar",
		},
		Tags:            map[string]string{"super-dolphin-oci-build": request.JobID},
		InitContainer:   eci.InitContainer{Name: "materializer", Command: []string{"/bin/sh"}, Args: []string{"-ceu", "mkdir -p /opt/super-dolphin-gate/bin; cp /super-dolphin-gate /opt/super-dolphin-gate/bin/super-dolphin-gate; chmod 0555 /opt/super-dolphin-gate/bin/super-dolphin-gate; cp /current-gate/" + path.Base(requestKey) + " /source-data/request.json; cp /current-gate/" + path.Base(contextKey) + " /source-data/context.tar"}},
		BootstrapVolume: eci.OSSVolume{Bucket: config.OSS.Bucket, Endpoint: strings.TrimPrefix(config.OSS.InternalEndpoint, "https://"), Path: bootstrapPath, RoleName: config.WorkerRoleName},
		ExpandedVolume:  eci.EmptyDirVolume{Name: "expanded-data"}, SourceVolume: eci.EmptyDirVolume{Name: "source-data"}, WorkVolume: eci.EmptyDirVolume{Name: "work-data"}, TempVolume: eci.EmptyDirVolume{Name: "temp-data"},
		MainVolumeMounts: []eci.VolumeMount{{Name: "expanded-data", MountPath: "/opt/super-dolphin-gate", ReadOnly: true}, {Name: "source-data", MountPath: "/source-data", ReadOnly: true}, {Name: "work-data", MountPath: "/work-data"}, {Name: "temp-data", MountPath: "/tmp"}},
		InitVolumeMounts: []eci.VolumeMount{{Name: "current-gate", MountPath: "/current-gate", ReadOnly: true}, {Name: "expanded-data", MountPath: "/opt/super-dolphin-gate"}, {Name: "source-data", MountPath: "/source-data"}, {Name: "work-data", MountPath: "/work-data"}, {Name: "temp-data", MountPath: "/tmp"}},
	}
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
