package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

const (
	remoteOCIRegistryUsernameEnvironment = "SUPER_DOLPHIN_REMOTE_OCI_REGISTRY_USERNAME"
	remoteOCIRegistryPasswordEnvironment = "SUPER_DOLPHIN_REMOTE_OCI_REGISTRY_PASSWORD"
)

// buildRemoteOCIBaseline is the production-only bridge from a fixed main tree
// to an immutable OCI image. Its only output is image@digest identity.
func buildRemoteOCIBaseline(ctx context.Context, config remoteRunConfig, accepted remoteci.BaselineState, input remoteBaselineRefreshInput) (*remoteci.BaselineOCIProjectCache, error) {
	if ctx == nil {
		return nil, errors.New("remote OCI baseline build context is required")
	}
	if input.Identity.Platform != "linux/amd64" {
		return nil, errors.New("remote OCI baseline build requires linux/amd64")
	}
	if len(input.SourceEntries) == 0 {
		return nil, errors.New("remote OCI baseline build source entries are required")
	}
	credential, err := resolveRemoteOCIRegistryCredential(config.OCICache.RegistryRepository)
	if err != nil {
		return nil, err
	}
	runner, err := localci.NewDockerBuildxRunner(config.OCICache.BuildxRoot)
	if err != nil {
		return nil, fmt.Errorf("create remote OCI controlled Buildx runner: %w", err)
	}
	if err := runner.RecoverControlledBuilders(ctx); err != nil {
		return nil, fmt.Errorf("recover remote OCI controlled Buildx runners: %w", err)
	}
	builder, err := localci.NewImageBuilder(runner)
	if err != nil {
		return nil, fmt.Errorf("create remote OCI image builder: %w", err)
	}
	request := localci.CandidateRequest{
		SourceTreeSHA: input.Identity.MainTree, PolicyDigest: input.Identity.PolicyDigest,
		ImageSchemaVersion: "1", SourceEntries: append([]sourceexport.TreeEntry(nil), input.SourceEntries...),
		Platform: input.Identity.Platform,
	}
	if accepted.SchemaVersion != 0 {
		request.AcceptedImageReference = accepted.RuntimeImage
	}
	result, err := builder.EnsureCandidatePushed(ctx, request, localci.RegistryPushRequest{
		Repository: config.OCICache.RegistryRepository,
		Credential: credential,
	})
	if err != nil {
		return nil, err
	}
	image := config.OCICache.RegistryRepository + "@" + result.ImageDigest
	return &remoteci.BaselineOCIProjectCache{
		Image: image, ContentManifestSHA256: result.InputDigest,
		MainTree: input.Identity.MainTree, ToolchainDigest: input.Identity.ToolchainDigest,
		Platform: input.Identity.Platform, CachePath: remoteci.OCIProjectGoBuildCachePath,
	}, nil
}

// resolveRemoteOCIRegistryCredential obtains no persistent Docker credentials:
// every call reads the operator-provided request credential and Buildx deletes
// its private Docker config after the single push.
func resolveRemoteOCIRegistryCredential(repository string) (localci.RegistryCredential, error) {
	server, _, ok := strings.Cut(repository, "/")
	if !ok || server == "" {
		return localci.RegistryCredential{}, errors.New("remote OCI registry repository must include a registry host")
	}
	username, usernameOK := os.LookupEnv(remoteOCIRegistryUsernameEnvironment)
	password, passwordOK := os.LookupEnv(remoteOCIRegistryPasswordEnvironment)
	if !usernameOK || !passwordOK {
		return localci.RegistryCredential{}, errors.New("request-scoped remote OCI registry credentials are required")
	}
	return localci.RegistryCredential{Server: server, Username: username, Password: password}, nil
}
