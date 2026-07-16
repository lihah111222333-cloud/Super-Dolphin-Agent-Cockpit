package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

// productionCoordinatorDependencies 装配签名 accepted image、Git object source 与一次性 Docker runner。
func productionCoordinatorDependencies(ctx context.Context) (coordinatorDependencies, error) {
	if ctx == nil {
		return coordinatorDependencies{}, fmt.Errorf("%w: production context is required", errCoordinatorDependency)
	}
	if err := validateProductionGitEnvironment(); err != nil {
		return coordinatorDependencies{}, err
	}
	config, err := loadProductionCoordinatorConfig()
	if err != nil {
		return coordinatorDependencies{}, err
	}
	if err := validateProductionRuntimeRoot(config.TrustedSourceRoot); err != nil {
		return coordinatorDependencies{}, err
	}
	imageEnsurer, err := newProductionImageEnsurer(ctx, config)
	if err != nil {
		return coordinatorDependencies{}, err
	}
	sourceMaterializer, freshRunner, err := newProductionExecutionAdapters(config)
	if err != nil {
		return coordinatorDependencies{}, err
	}
	dependencies := coordinatorDependencies{
		ImageEnsurer: imageEnsurer, SourceMaterializer: sourceMaterializer,
		FreshRunner: freshRunner, RecoveryRunner: freshRunner,
	}
	return dependencies, dependencies.validate()
}

// newProductionImageEnsurer 组装验签 accepted state 与只会产出候选的 BuildKit builder。
func newProductionImageEnsurer(
	ctx context.Context,
	config productionCoordinatorConfig,
) (*productionImageEnsurer, error) {
	verifier, err := newProductionSignatureVerifier(config.AcceptedImageSigners)
	if err != nil {
		return nil, err
	}
	authority, err := newProductionGitAuthority(ctx, config)
	if err != nil {
		return nil, err
	}
	state, err := localci.NewAcceptedImageState(config.AcceptedImageRoot, verifier, authority)
	if err != nil {
		return nil, fmt.Errorf("open accepted image state: %w", err)
	}
	accepted := &productionAcceptedImageLoader{state: state, authority: authority}
	record, err := accepted.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load production accepted image: %w", err)
	}
	if err := validateAcceptedPlatform(record.Image, config.Platform); err != nil {
		return nil, err
	}
	buildx, err := localci.NewDockerBuildxRunner(config.CandidateBuildRoot)
	if err != nil {
		return nil, err
	}
	builder, err := localci.NewImageBuilder(buildx)
	if err != nil {
		return nil, err
	}
	truth, err := localci.NewTruthImageEnsurer(accepted, builder)
	if err != nil {
		return nil, err
	}
	return &productionImageEnsurer{truth: truth, platform: config.Platform}, nil
}

// newProductionExecutionAdapters 组装 Git bundle 快照与 Docker 一次性容器边界。
func newProductionExecutionAdapters(
	config productionCoordinatorConfig,
) (*productionSourceMaterializer, *productionFreshContainerRunner, error) {
	fresh, err := localci.NewFreshContainerRunner(config.SeccompProfile, config.TrustedSourceRoot)
	if err != nil {
		return nil, nil, err
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, nil, fmt.Errorf("resolve source materializer Git executable: %w", err)
	}
	return &productionSourceMaterializer{gitPath: gitPath}, &productionFreshContainerRunner{runner: fresh}, nil
}

func validateProductionGitEnvironment() error {
	for _, name := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_COUNT",
	} {
		if _, exists := os.LookupEnv(name); exists {
			return fmt.Errorf("production coordinator rejects inherited %s", name)
		}
	}
	return nil
}

func validateProductionRuntimeRoot(trustedSourceRoot string) error {
	runtimeRoot, err := coordinatorRuntimeRoot()
	if err != nil {
		return err
	}
	if !productionPathContains(trustedSourceRoot, runtimeRoot) {
		return errors.New("trusted_source_root does not contain the coordinator runtime root")
	}
	return nil
}

func validateAcceptedPlatform(identity gatecontract.ImageIdentity, platform string) error {
	acceptedPlatform := identity.OS + "/" + identity.Architecture
	if identity.Variant != "" {
		acceptedPlatform += "/" + identity.Variant
	}
	if acceptedPlatform != strings.TrimSpace(platform) {
		return fmt.Errorf("accepted image platform %q does not match configured platform %q", acceptedPlatform, platform)
	}
	return nil
}
