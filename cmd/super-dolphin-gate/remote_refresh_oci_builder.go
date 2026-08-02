package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
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
func buildRemoteOCIBaseline(ctx context.Context, config remoteRunConfig, accepted remoteci.BaselineState, input remoteBaselineRefreshInput, request remoteci.OCIBaselineBuilderRequest, deltaArchive []byte) (*remoteOCIBaselineBuild, error) {
	return buildRemoteOCIBaselineWithRequester(ctx, config, input, request, deltaArchive, requestRemoteOCIBuild)
}

func buildRemoteOCIBaselineWithRequester(ctx context.Context, config remoteRunConfig, input remoteBaselineRefreshInput, request remoteci.OCIBaselineBuilderRequest, deltaArchive []byte, requester remoteOCIBuildRequester) (*remoteOCIBaselineBuild, error) {
	if ctx == nil {
		return nil, errors.New("remote OCI baseline build context is required")
	}
	if requester == nil {
		return nil, errors.New("remote OCI baseline build requester is required")
	}
	if err := cicontract.ValidateTargetPlatform(input.Identity.Platform); err != nil {
		return nil, fmt.Errorf("remote OCI baseline build platform: %w", err)
	}
	if len(input.SourceEntries) == 0 {
		return nil, errors.New("remote OCI baseline build source entries are required")
	}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("validate prepared remote OCI baseline request: %w", err)
	}
	runtime, err := newRemoteOCIBuilderRuntime(config)
	if err != nil {
		return nil, fmt.Errorf("create remote OCI ECI runtime: %w", err)
	}
	result, err := requester(ctx, config, runtime, request, deltaArchive)
	if err != nil {
		return nil, fmt.Errorf("request remote OCI BuildKit build: %w", err)
	}
	if err := result.ValidateAgainst(request); err != nil {
		return nil, err
	}
	if err := cicontract.ValidateIncrementalRefreshTransfer(result.TransferMode, result.ParentGeneration, result.ParentImageSnapshotID, result.DeltaArchiveSHA256); err != nil {
		return nil, fmt.Errorf("validate remote OCI baseline incremental transfer: %w", err)
	}
	image := result.Image
	return &remoteOCIBaselineBuild{Request: request, Result: result, Cache: &remoteci.BaselineOCIProjectCache{
		Image: image, ContentManifestSHA256: result.ImageInputDigest,
		MainTree: input.Identity.MainTree, ToolchainDigest: input.Identity.ToolchainDigest,
		Platform: input.Identity.Platform, CachePath: remoteci.OCIProjectGoBuildCachePath,
	}}, nil
}

func prepareRemoteOCIBuildRequest(ctx context.Context, config remoteRunConfig, accepted remoteci.BaselineState, input remoteBaselineRefreshInput) (remoteci.OCIBaselineBuilderRequest, []byte, error) {
	if ctx == nil {
		return remoteci.OCIBaselineBuilderRequest{}, nil, errors.New("remote OCI baseline source snapshot context is required")
	}
	if accepted.SchemaVersion == 0 {
		return remoteci.OCIBaselineBuilderRequest{}, nil, errors.New("remote OCI baseline incremental build requires an accepted ImageCache")
	}
	if input.RepositoryRoot == "" {
		return remoteci.OCIBaselineBuilderRequest{}, nil, errors.New("remote OCI baseline accepted Git repository root is required")
	}
	acceptedInput, err := resolveRemoteBaselineIdentity(ctx, input.RepositoryRoot, accepted.MainCommit, accepted.Platform)
	if err != nil {
		return remoteci.OCIBaselineBuilderRequest{}, nil, fmt.Errorf("load accepted exact Git tree: %w", err)
	}
	acceptedCandidate := remoteOCISnapshotCandidate(acceptedInput, accepted.RuntimeImage)
	acceptedContent, acceptedTarget, acceptedBuild, err := remoteci.PrepareOCISourceSnapshot(acceptedCandidate, accepted.ToolchainDigest)
	if err != nil {
		return remoteci.OCIBaselineBuilderRequest{}, nil, fmt.Errorf("derive accepted source snapshot: %w", err)
	}
	if err := validateAcceptedRemoteOCISourceSnapshot(accepted, input.AcceptedStateSHA256, acceptedContent, acceptedTarget, acceptedBuild); err != nil {
		return remoteci.OCIBaselineBuilderRequest{}, nil, err
	}
	targetContent, target, build, err := remoteci.PrepareOCISourceSnapshot(remoteOCISnapshotCandidate(input, accepted.RuntimeImage), input.Identity.ToolchainDigest)
	if err != nil {
		return remoteci.OCIBaselineBuilderRequest{}, nil, fmt.Errorf("derive target source snapshot: %w", err)
	}
	targetContentDigest, err := remoteci.SourceSnapshotContentDigest(targetContent)
	if err != nil {
		return remoteci.OCIBaselineBuilderRequest{}, nil, fmt.Errorf("derive target source snapshot manifest digest: %w", err)
	}
	if targetContent.SourceTree != input.Identity.MainTree || target.SourceDigest != targetContentDigest {
		return remoteci.OCIBaselineBuilderRequest{}, nil, errors.New("target source snapshot identity drifted from exact Git tree")
	}
	acceptedManifest, err := remoteci.NewAcceptedSourceSnapshotManifest(remoteci.SourceSnapshotAuthorityBinding{Generation: accepted.Generation, StateDigest: input.AcceptedStateSHA256, SnapshotID: accepted.ImageCacheSnapshotID, SourceDigest: acceptedTarget.SourceDigest}, acceptedContent)
	if err != nil {
		return remoteci.OCIBaselineBuilderRequest{}, nil, fmt.Errorf("bind accepted source snapshot authority: %w", err)
	}
	delta, err := remoteci.BuildSourceSnapshotDelta(acceptedManifest, target)
	if err != nil {
		return remoteci.OCIBaselineBuilderRequest{}, nil, fmt.Errorf("build accepted-to-target source snapshot delta: %w", err)
	}
	if len(delta.Archive) == 0 {
		return remoteci.OCIBaselineBuilderRequest{}, nil, errors.New("source snapshot delta archive is empty")
	}
	deltaArchiveSHA256 := fmt.Sprintf("sha256:%x", sha256.Sum256(delta.Archive))
	jobID := "oci-" + strings.TrimPrefix(deltaArchiveSHA256, "sha256:")[:24]
	prefix := path.Join(config.OSS.SourcePrefix, "oci-builds", jobID) + "/"
	request := remoteci.OCIBaselineBuilderRequest{
		SchemaVersion: remoteci.OCIBaselineBuilderRequestSchemaVersion,
		JobID:         jobID, TransferMode: cicontract.RefreshTransferAcceptedSnapshotDelta, ParentGeneration: accepted.Generation,
		ParentStateSHA256: input.AcceptedStateSHA256, OutputRepository: config.OCIRefresh.OutputRepository, ParentImage: accepted.RuntimeImage,
		ParentImageCacheID: accepted.ImageCacheID, ParentImageSnapshotID: accepted.ImageCacheSnapshotID,
		ParentSourceManifest: acceptedTarget.SourceDigest, ParentSourceImagePath: cicontract.SourceSnapshotManifestPath, ParentSourceClosure: acceptedTarget.ClosureDigest,
		TargetCommit: input.Identity.MainCommit, TargetTree: input.Identity.MainTree, TargetSourceManifest: target.SourceDigest, TargetSourceClosure: target.ClosureDigest, ImageInputDigest: build.InputDigest, PolicyDigest: input.Identity.PolicyDigest,
		ToolchainDigest: input.Identity.ToolchainDigest, Platform: input.Identity.Platform,
		RuntimeDependencyDigest: input.RuntimeDependencyDigest, DeltaArchiveKey: prefix + "source.snapshot.delta.tar", DeltaArchiveSHA256: deltaArchiveSHA256, DeltaArchiveSize: int64(len(delta.Archive)), JobKey: prefix + "request.job.json",
	}
	if err := request.Validate(); err != nil {
		return remoteci.OCIBaselineBuilderRequest{}, nil, fmt.Errorf("construct remote OCI baseline builder request: %w", err)
	}
	return request, delta.Archive, nil
}

func remoteOCISnapshotCandidate(input remoteBaselineRefreshInput, acceptedImage string) remoteci.CandidateRequest {
	return remoteci.CandidateRequest{SourceTreeSHA: input.Identity.MainTree, PolicyDigest: input.Identity.PolicyDigest, ImageSchemaVersion: "1", SourceEntries: append([]sourceexport.TreeEntry(nil), input.SourceEntries...), Platform: input.Identity.Platform, AcceptedImageReference: acceptedImage}
}

func validateAcceptedRemoteOCISourceSnapshot(accepted remoteci.BaselineState, stateDigest string, content remoteci.SourceSnapshotContentManifest, target remoteci.TargetSourceBuildClosure, build remoteci.CandidateResult) error {
	if accepted.MainTree != content.SourceTree || accepted.MainTree != target.TreeOID || accepted.PolicyDigest != content.PolicyDigest || accepted.ToolchainDigest != content.ToolchainDigest || accepted.SourceSnapshotManifestDigest != target.SourceDigest || accepted.SourceSnapshotClosureDigest != target.ClosureDigest || accepted.BaselineManifestDigest != build.InputDigest || stateDigest == "" {
		return errors.New("accepted source snapshot identity drifted from accepted BaselineState")
	}
	return nil
}

func newRemoteOCIBuilderRuntime(config remoteRunConfig) (remoteOCIBuilderRuntime, error) {
	return eci.New(eci.Config{Binary: config.AliyunCLI, RegionID: config.RegionID, VSwitchID: config.VSwitchID, SecurityGroupID: config.SecurityGroupID, WorkerRoleName: config.WorkerRoleName, Profile: config.CredentialProfile, Deadline: remoteBaselineRefreshDeadline, SpotStrategy: eci.SpotStrategyAsPriceGo, SpotDurationHours: 1})
}

func requestRemoteOCIBuild(ctx context.Context, config remoteRunConfig, runtime remoteOCIBuilderRuntime, request remoteci.OCIBaselineBuilderRequest, deltaArchive []byte) (remoteci.OCIBaselineBuilderResult, error) {
	return coordinateRemoteOCIBuild(ctx, config, runtime, request, deltaArchive)
}
