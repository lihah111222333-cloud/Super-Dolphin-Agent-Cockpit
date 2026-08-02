package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

const remoteOCIBuildReceiptSchemaVersion uint32 = 1

var remoteOCIDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// remoteOCIBuilderRuntime is the existing ECI lifecycle boundary needed by a
// remote OCI builder worker. Keeping it narrow prevents request-side code from
// gaining Docker daemon access.
type remoteOCIBuilderRuntime interface {
	CreateContainerGroup(context.Context, eci.CreateRequest) (eci.ContainerGroup, error)
	DescribeContainerGroups(context.Context, ...string) ([]eci.ContainerGroup, error)
	DescribeContainerLog(context.Context, string, string) (string, error)
	DeleteContainerGroup(context.Context, string) error
}

// remoteOCIBuildRequest is the cross-runtime contract for a single remote
// BuildKit invocation. The embedded request is already closure-validated by
// localci and carries the exact context and input digests the receipt must echo.
type remoteOCIBuildRequest struct {
	SchemaVersion   uint32                       `json:"schema_version"`
	Repository      string                       `json:"repository"`
	MainTree        string                       `json:"main_tree"`
	ToolchainDigest string                       `json:"toolchain_digest"`
	Platform        string                       `json:"platform"`
	Build           localci.BuildKitBuildRequest `json:"build"`
	RegistryAccess  eci.RegistryAccess           `json:"registry_access"`
}

// remoteOCIBuildReceipt is emitted by the remote builder only after its push
// succeeds. Every identity field is echoed so a stale or cross-tree image can
// never become an accepted baseline.
type remoteOCIBuildReceipt struct {
	SchemaVersion   uint32 `json:"schema_version"`
	MainTree        string `json:"main_tree"`
	ToolchainDigest string `json:"toolchain_digest"`
	Platform        string `json:"platform"`
	InputDigest     string `json:"input_digest"`
	ImageDigest     string `json:"image_digest"`
	ConfigDigest    string `json:"config_digest"`
}

type remoteOCIBuildRequester func(context.Context, remoteOCIBuilderRuntime, remoteOCIBuildRequest) (remoteOCIBuildReceipt, error)

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
	request, err := prepareRemoteOCIBuildRequest(config, accepted, input)
	if err != nil {
		return nil, err
	}
	runtime, err := newRemoteOCIBuilderRuntime(config)
	if err != nil {
		return nil, fmt.Errorf("create remote OCI ECI runtime: %w", err)
	}
	receipt, err := requester(ctx, runtime, request)
	if err != nil {
		return nil, fmt.Errorf("request remote OCI BuildKit build: %w", err)
	}
	if err := validateRemoteOCIBuildReceipt(request, receipt); err != nil {
		return nil, err
	}
	image := config.OCICache.RegistryRepository + "@" + receipt.ImageDigest
	return &remoteci.BaselineOCIProjectCache{
		Image: image, ContentManifestSHA256: receipt.InputDigest,
		MainTree: input.Identity.MainTree, ToolchainDigest: input.Identity.ToolchainDigest,
		Platform: input.Identity.Platform, CachePath: remoteci.OCIProjectGoBuildCachePath,
	}, nil
}

func prepareRemoteOCIBuildRequest(config remoteRunConfig, accepted remoteci.BaselineState, input remoteBaselineRefreshInput) (remoteOCIBuildRequest, error) {
	candidate := localci.CandidateRequest{SourceTreeSHA: input.Identity.MainTree, PolicyDigest: input.Identity.PolicyDigest, ImageSchemaVersion: "1", SourceEntries: append([]sourceexport.TreeEntry(nil), input.SourceEntries...), Platform: input.Identity.Platform}
	if accepted.SchemaVersion != 0 {
		candidate.AcceptedImageReference = accepted.RuntimeImage
	}
	_, build, err := localci.PrepareCandidateBuildRequest(candidate)
	if err != nil {
		return remoteOCIBuildRequest{}, fmt.Errorf("prepare remote OCI build request: %w", err)
	}
	access := eci.RegistryAccess{ACR: config.ACRRegistryInfo}
	if err := eci.ValidateRegistryAccessForRepository(access, config.OCICache.RegistryRepository); err != nil {
		return remoteOCIBuildRequest{}, fmt.Errorf("validate remote OCI registry access: %w", err)
	}
	return remoteOCIBuildRequest{SchemaVersion: remoteOCIBuildReceiptSchemaVersion, Repository: config.OCICache.RegistryRepository, MainTree: input.Identity.MainTree, ToolchainDigest: input.Identity.ToolchainDigest, Platform: input.Identity.Platform, Build: build, RegistryAccess: access}, nil
}

func newRemoteOCIBuilderRuntime(config remoteRunConfig) (remoteOCIBuilderRuntime, error) {
	return eci.New(eci.Config{Binary: config.AliyunCLI, RegionID: config.RegionID, VSwitchID: config.VSwitchID, SecurityGroupID: config.SecurityGroupID, WorkerRoleName: config.WorkerRoleName, Profile: config.CredentialProfile, Image: config.OCICache.RemoteBuilderImage, Deadline: remoteBaselineRefreshDeadline, SpotStrategy: eci.SpotStrategyAsPriceGo, SpotDurationHours: 1, FallbackToPayAsYouGo: true})
}

func validateRemoteOCIBuildReceipt(request remoteOCIBuildRequest, receipt remoteOCIBuildReceipt) error {
	if receipt.SchemaVersion != remoteOCIBuildReceiptSchemaVersion || receipt.MainTree != request.MainTree || receipt.ToolchainDigest != request.ToolchainDigest || receipt.Platform != request.Platform || receipt.InputDigest != request.Build.InputDigest {
		return errors.New("remote OCI build receipt does not bind the requested main tree, toolchain, platform, and input digest")
	}
	if !remoteOCIDigestPattern.MatchString(receipt.ImageDigest) || !remoteOCIDigestPattern.MatchString(receipt.ConfigDigest) {
		return errors.New("remote OCI build receipt image or config digest is invalid")
	}
	return nil
}

// requestRemoteOCIBuild deliberately fails until the remote builder worker is
// deployed. ECI currently transports container lifecycle only; it has no
// versioned source/request delivery or signed receipt channel. Falling back to
// a local daemon here would make a non-comparable baseline look authoritative.
func requestRemoteOCIBuild(context.Context, remoteOCIBuilderRuntime, remoteOCIBuildRequest) (remoteOCIBuildReceipt, error) {
	return remoteOCIBuildReceipt{}, errors.New("remote OCI builder worker protocol is not deployed")
}
