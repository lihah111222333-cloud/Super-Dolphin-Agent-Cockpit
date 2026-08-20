package eci

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// artifactBuilderClient adapts the existing ECI control-plane boundary without
// sharing normal shard Runtime or its three-volume/init-container request.
type artifactBuilderClient struct {
	client   *Client
	mu       sync.RWMutex
	requests map[string]ArtifactBuilderRequest
}

var _ ArtifactBuilderActor = (*artifactBuilderClient)(nil)

// NewArtifactBuilderActor 创建绑定现有 ECI 控制面的 portable artifact builder actor。
func NewArtifactBuilderActor(client *Client) ArtifactBuilderActor {
	return &artifactBuilderClient{client: client, requests: make(map[string]ArtifactBuilderRequest)}
}

func (actor *artifactBuilderClient) requireClient() (*Client, error) {
	if actor == nil || actor.client == nil {
		return nil, errors.New("artifact builder ECI client is required")
	}
	return actor.client, nil
}

// Create 创建 builder group 后按真实 ID 回读 live identity。
func (actor *artifactBuilderClient) Create(ctx context.Context, request ArtifactBuilderRequest) (ArtifactBuilderGroup, error) {
	client, err := actor.requireClient()
	if err != nil {
		return ArtifactBuilderGroup{}, err
	}
	if err := request.Validate(); err != nil {
		return ArtifactBuilderGroup{}, err
	}
	tags, err := artifactBuilderExpectedTags(request)
	if err != nil {
		return ArtifactBuilderGroup{}, err
	}
	args, err := artifactBuilderCreateArgs(client, request, tags)
	if err != nil {
		return ArtifactBuilderGroup{}, err
	}
	output, err := client.run(ctx, "CreateContainerGroup", args...)
	if err != nil {
		live, reconcileErr := client.reconcileCreatedContainerGroup(ctx, request.ContainerGroupName, tags, err)
		if reconcileErr != nil {
			return ArtifactBuilderGroup{}, reconcileErr
		}
		return actor.bindCreatedGroup(request, live)
	}
	created, err := decodeCreatedContainerGroup(output, request.ContainerGroupName)
	if err != nil {
		live, reconcileErr := client.reconcileCreatedContainerGroup(ctx, request.ContainerGroupName, tags, err)
		if reconcileErr != nil {
			return ArtifactBuilderGroup{}, reconcileErr
		}
		return actor.bindCreatedGroup(request, live)
	}
	live, err := actor.describeLive(ctx, created.ID)
	if err != nil {
		return ArtifactBuilderGroup{}, err
	}
	return actor.bindCreatedGroup(request, live)
}

func (actor *artifactBuilderClient) bindCreatedGroup(request ArtifactBuilderRequest, live ContainerGroup) (ArtifactBuilderGroup, error) {
	group, err := mapArtifactBuilderGroup(live)
	if err != nil {
		return ArtifactBuilderGroup{}, err
	}
	lifecycle, err := NewArtifactBuilderLifecycle(request)
	if err != nil {
		return ArtifactBuilderGroup{}, err
	}
	if err := lifecycle.ObserveCreated(group); err != nil {
		return ArtifactBuilderGroup{}, err
	}
	actor.mu.Lock()
	actor.requests[group.ID] = request
	actor.mu.Unlock()
	return group, nil
}

// describeLive 按真实容器组 ID 读取 provider 状态，不从 request 补字段。
func (actor *artifactBuilderClient) describeLive(ctx context.Context, id string) (ContainerGroup, error) {
	client, err := actor.requireClient()
	if err != nil {
		return ContainerGroup{}, err
	}
	if strings.TrimSpace(id) == "" {
		return ContainerGroup{}, errors.New("artifact builder container group ID is required")
	}
	groups, err := client.DescribeContainerGroups(ctx, id)
	if err != nil {
		return ContainerGroup{}, err
	}
	if len(groups) != 1 || groups[0].ID != id {
		return ContainerGroup{}, fmt.Errorf("artifact builder Describe returned %d groups for requested identity", len(groups))
	}
	return groups[0], nil
}

// Describe 只映射 provider 回读字段，绝不把 request 值伪装成 live state。
func (actor *artifactBuilderClient) Describe(ctx context.Context, id string) (ArtifactBuilderGroup, error) {
	live, err := actor.describeLive(ctx, id)
	if err != nil {
		return ArtifactBuilderGroup{}, err
	}
	group, err := mapArtifactBuilderGroup(live)
	if err != nil {
		return ArtifactBuilderGroup{}, err
	}
	actor.mu.RLock()
	request, known := actor.requests[id]
	actor.mu.RUnlock()
	if known {
		lifecycle, lifecycleErr := NewArtifactBuilderLifecycle(request)
		if lifecycleErr != nil {
			return ArtifactBuilderGroup{}, lifecycleErr
		}
		if lifecycleErr := lifecycle.ObserveCreated(group); lifecycleErr != nil {
			return ArtifactBuilderGroup{}, lifecycleErr
		}
	}
	return group, nil
}

// Logs 复用现有 client 的有界日志查询。
func (actor *artifactBuilderClient) Logs(ctx context.Context, groupID, containerName string) (string, error) {
	client, err := actor.requireClient()
	if err != nil {
		return "", err
	}
	return client.DescribeContainerLog(ctx, groupID, containerName)
}

// Delete 复用现有 client 删除容器组。
func (actor *artifactBuilderClient) Delete(ctx context.Context, groupID string) error {
	client, err := actor.requireClient()
	if err != nil {
		return err
	}
	return client.DeleteContainerGroup(ctx, groupID)
}

// ConfirmAbsent 复用现有 client 的 provider absence proof。
func (actor *artifactBuilderClient) ConfirmAbsent(ctx context.Context, groupID string) (bool, error) {
	client, err := actor.requireClient()
	if err != nil {
		return false, err
	}
	absent, err := client.ConfirmContainerGroupAbsent(ctx, groupID)
	if err == nil && absent {
		actor.mu.Lock()
		delete(actor.requests, groupID)
		actor.mu.Unlock()
	}
	return absent, err
}

// artifactBuilderCreateArgs 生成无 init、无卷、无 workload 的 builder 专用 ECI argv。
func artifactBuilderCreateArgs(client *Client, request ArtifactBuilderRequest, tags map[string]string) ([]string, error) {
	commandLine, err := request.CommandLine()
	if err != nil {
		return nil, err
	}
	args := []string{
		"--VSwitchId", joinedVSwitchIDs(request.VSwitches),
		"--ScheduleStrategy", request.ScheduleStrategy,
		"--SecurityGroupId", request.SecurityGroupID,
		"--RamRoleName", request.RAMRoleName,
		"--Cpu", formatResource(request.CPU),
		"--Memory", formatResource(request.MemoryGiB),
		"--EphemeralStorage", strconv.FormatInt(request.EphemeralStorageGiB, 10),
		"--SpotStrategy", client.config.SpotStrategy,
		"--RestartPolicy", "Never",
		"--ActiveDeadlineSeconds", strconv.FormatInt(int64(client.config.Deadline/time.Second), 10),
		"--ContainerGroupName", request.ContainerGroupName,
		"--ImageSnapshotId", request.ImageSnapshotID,
		"--Container.1.Name", request.ContainerName,
		"--Container.1.Image", request.Image,
		"--Container.1.Cpu", formatResource(request.CPU),
		"--Container.1.Memory", formatResource(request.MemoryGiB),
		"--Container.1.ImagePullPolicy", "Never",
		"--Container.1.SecurityContext.ReadOnlyRootFilesystem", "true",
		"--Container.1.SecurityContext.RunAsUser", strconv.Itoa(cicontract.RemoteWorkerUID),
		"--Container.1.SecurityContextRunAsGroup", strconv.Itoa(cicontract.RemoteWorkerGID),
	}
	if client.config.SpotStrategy != SpotStrategyNoSpot {
		args = append(args, "--SpotDuration", strconv.FormatInt(client.config.SpotDurationHours, 10))
	}
	args = appendIndexedValues(args, "--Container.1.Command", request.Command[:1])
	containerArgs := append([]string{request.Command[1]}, commandLine[2:]...)
	args = appendIndexedValues(args, "--Container.1.Arg", containerArgs)
	return appendIndexedMap(args, "--Tag", tags), nil
}
