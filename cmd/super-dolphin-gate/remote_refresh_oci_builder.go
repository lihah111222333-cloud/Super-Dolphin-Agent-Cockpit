package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

// remoteOCIBuilderRuntime is the existing ECI lifecycle boundary needed by a
// remote OCI builder worker. Keeping it narrow prevents request-side code from
// gaining Docker daemon access.
type remoteOCIBuilderRuntime interface {
	CreateContainerGroup(context.Context, eci.CreateRequest) (eci.ContainerGroup, error)
	DescribeContainerGroups(context.Context, ...string) ([]eci.ContainerGroup, error)
	DescribeContainerLog(context.Context, string, string) (string, error)
	DeleteContainerGroup(context.Context, string) error
}

type remoteOCIBuildRequester func(context.Context, remoteRunConfig, remoteOCIBuilderRuntime, remoteci.OCIBaselineBuilderRequest, []byte) (remoteci.OCIBaselineBuilderResult, error)

// buildRemoteOCIBaseline is the production-only bridge from a fixed main tree
// to an immutable OCI image. Its only output is image@digest identity.
func buildRemoteOCIBaseline(ctx context.Context, config remoteRunConfig, accepted remoteci.BaselineState, input remoteBaselineRefreshInput) (*remoteci.BaselineOCIProjectCache, error) {
	return buildRemoteOCIBaselineWithRequester(ctx, config, accepted, input, requestRemoteOCIBuild)
}

func buildRemoteOCIBaselineWithRequester(ctx context.Context, config remoteRunConfig, accepted remoteci.BaselineState, input remoteBaselineRefreshInput, requester remoteOCIBuildRequester) (*remoteci.BaselineOCIProjectCache, error) {
	if ctx == nil {
		return nil, errors.New("remote OCI baseline build context is required")
	}
	if requester == nil {
		return nil, errors.New("remote OCI baseline build requester is required")
	}
	if input.Identity.Platform != "linux/amd64" {
		return nil, errors.New("remote OCI baseline build requires linux/amd64")
	}
	if len(input.SourceEntries) == 0 {
		return nil, errors.New("remote OCI baseline build source entries are required")
	}
	request, contextArchive, err := prepareRemoteOCIBuildRequest(config, accepted, input)
	if err != nil {
		return nil, err
	}
	runtime, err := newRemoteOCIBuilderRuntime(config)
	if err != nil {
		return nil, fmt.Errorf("create remote OCI ECI runtime: %w", err)
	}
	result, err := requester(ctx, config, runtime, request, contextArchive)
	if err != nil {
		return nil, fmt.Errorf("request remote OCI BuildKit build: %w", err)
	}
	if err := result.ValidateAgainst(request); err != nil {
		return nil, err
	}
	image := result.Image
	return &remoteci.BaselineOCIProjectCache{
		Image: image, ContentManifestSHA256: result.InputDigest,
		MainTree: input.Identity.MainTree, ToolchainDigest: input.Identity.ToolchainDigest,
		Platform: input.Identity.Platform, CachePath: remoteci.OCIProjectGoBuildCachePath,
	}, nil
}

func prepareRemoteOCIBuildRequest(config remoteRunConfig, accepted remoteci.BaselineState, input remoteBaselineRefreshInput) (remoteci.OCIBaselineBuilderRequest, []byte, error) {
	candidate := remoteci.CandidateRequest{SourceTreeSHA: input.Identity.MainTree, PolicyDigest: input.Identity.PolicyDigest, ImageSchemaVersion: "1", SourceEntries: append([]sourceexport.TreeEntry(nil), input.SourceEntries...), Platform: input.Identity.Platform}
	parentImage := input.Identity.RuntimeImage
	if accepted.SchemaVersion != 0 {
		candidate.AcceptedImageReference = accepted.RuntimeImage
		parentImage = accepted.RuntimeImage
	} else {
		return remoteci.OCIBaselineBuilderRequest{}, nil, errors.New("remote OCI baseline incremental build requires an accepted ImageCache")
	}
	_, build, err := remoteci.PrepareOCIBuildContext(candidate)
	if err != nil {
		return remoteci.OCIBaselineBuilderRequest{}, nil, fmt.Errorf("prepare remote OCI build context: %w", err)
	}
	if len(build.ContextTar) == 0 {
		return remoteci.OCIBaselineBuilderRequest{}, nil, errors.New("remote OCI build context is empty")
	}
	contextSHA256 := fmt.Sprintf("sha256:%x", sha256.Sum256(build.ContextTar))
	jobID := "oci-" + strings.TrimPrefix(contextSHA256, "sha256:")[:24]
	prefix := "oci-builds/" + jobID + "/"
	request := remoteci.OCIBaselineBuilderRequest{
		SchemaVersion: remoteci.OCIBaselineBuilderRequestSchemaVersion,
		JobID:         jobID, ContextKey: prefix + "context.context.tar", ContextSHA256: contextSHA256,
		SourceArchiveSize: int64(len(build.ContextTar)), RegistryRepository: config.OCICache.RegistryRepository, ParentImage: parentImage,
		ImageCacheID: accepted.ImageCacheID, ImageCacheSnapshotID: accepted.ImageCacheSnapshotID,
		MainCommit: input.Identity.MainCommit, MainTree: input.Identity.MainTree,
		ToolchainDigest: input.Identity.ToolchainDigest, Platform: input.Identity.Platform,
		RuntimeDependencyDigest: input.RuntimeDependencyDigest, JobKey: prefix + "request.job.json",
	}
	if err := request.Validate(); err != nil {
		return remoteci.OCIBaselineBuilderRequest{}, nil, fmt.Errorf("construct remote OCI baseline builder request: %w", err)
	}
	return request, build.ContextTar, nil
}

func newRemoteOCIBuilderRuntime(config remoteRunConfig) (remoteOCIBuilderRuntime, error) {
	return eci.New(eci.Config{Binary: config.AliyunCLI, RegionID: config.RegionID, VSwitchID: config.VSwitchID, SecurityGroupID: config.SecurityGroupID, WorkerRoleName: config.WorkerRoleName, Profile: config.CredentialProfile, Deadline: remoteBaselineRefreshDeadline, SpotStrategy: eci.SpotStrategyAsPriceGo, SpotDurationHours: 1})
}

func requestRemoteOCIBuild(ctx context.Context, config remoteRunConfig, runtime remoteOCIBuilderRuntime, request remoteci.OCIBaselineBuilderRequest, contextArchive []byte) (remoteci.OCIBaselineBuilderResult, error) {
	return coordinateRemoteOCIBuild(ctx, config, runtime, request, contextArchive)
}
