package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

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
	imageEnsurer, candidateBuilder, watcher, err := newProductionImageServices(ctx, config)
	if err != nil {
		return coordinatorDependencies{}, err
	}
	sourceMaterializer, freshRunner, err := newProductionExecutionAdapters(config)
	if err != nil {
		return coordinatorDependencies{}, err
	}
	dependencies := coordinatorDependencies{
		ImageEnsurer: imageEnsurer, CandidateBuilder: candidateBuilder, PromotionWatcher: watcher,
		SourceMaterializer: sourceMaterializer, FreshRunner: freshRunner, RecoveryRunner: freshRunner,
	}
	return dependencies, dependencies.validate()
}

// newProductionImageServices 组装 accepted 读取、调度构建与宿主晋升控制器。
func newProductionImageServices(
	ctx context.Context,
	config productionCoordinatorConfig,
) (*productionImageEnsurer, *productionCandidateBuildService, *localci.PromotionController, error) {
	promotion, err := newProductionPromotionAuthority(ctx, config)
	if err != nil {
		return nil, nil, nil, err
	}
	record, err := loadOrBootstrapProductionAcceptedImage(
		ctx, config, promotion, productionBootstrapHostRuntime{},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load production accepted image: %w", err)
	}
	if err := validateAcceptedPlatform(record.Image, config.Platform); err != nil {
		return nil, nil, nil, err
	}
	buildx, err := localci.NewDockerBuildxRunner(config.CandidateBuildRoot)
	if err != nil {
		return nil, nil, nil, err
	}
	builder, err := localci.NewImageBuilder(buildx)
	if err != nil {
		return nil, nil, nil, err
	}
	truth, err := localci.NewTruthImageEnsurer(promotion.accepted, promotion.candidates)
	if err != nil {
		return nil, nil, nil, err
	}
	buildService := &productionCandidateBuildService{
		store: promotion.candidates, builder: builder, resolver: localci.NewDockerCandidateIdentityResolver(),
	}
	watcher, err := localci.NewPromotionController(
		promotion.candidates, promotion.state, promotion.authority, promotion.signer,
		time.Duration(config.PromotionPollMillis)*time.Millisecond,
	)
	if err != nil {
		return nil, nil, nil, err
	}
	return &productionImageEnsurer{truth: truth, platform: config.Platform}, buildService, watcher, nil
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
