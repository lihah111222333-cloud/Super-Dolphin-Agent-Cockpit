package remotecicli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
)

type portableGoPlatform struct {
	name     string
	platform remoteportable.RepositoryWorkloadPlatform
	runtime  remoteportable.RepositoryWorkloadRuntime
}

func portableGoCatalogPlatforms() [1]portableGoPlatform {
	return [1]portableGoPlatform{
		{name: "linux-amd64", platform: remoteportable.RepositoryWorkloadPlatformLinuxAMD64, runtime: remoteportable.RepositoryWorkloadRuntimeUbuntuContainer},
	}
}

// expandDetectedRepositoryGoCatalog 从 exact tree 派生 execution workloads，不改写原始 tree blob。
func expandDetectedRepositoryGoCatalog(ctx context.Context, repository DetectedRepository) (DetectedRepository, error) {
	if ctx == nil || repository.Root == "" || repository.TreeSHA == "" {
		return DetectedRepository{}, errors.New("portable Go catalog repository identity is invalid")
	}
	reader := gitObjectResolver{repositoryRoot: repository.Root}
	for _, target := range portableGoCatalogPlatforms() {
		inventory, err := remoteportable.BuildPortableGoWorkloadInventory(ctx, reader, repository.TreeSHA, string(target.platform), repository.Manifest.Modules)
		if err != nil {
			return DetectedRepository{}, fmt.Errorf("build %s portable Go inventory: %w", target.name, err)
		}
		workloads, err := portableGoPackageWorkloads(repository.Manifest.Modules, inventory.GoPackages, target)
		if err != nil {
			return DetectedRepository{}, err
		}
		repository.Workloads = append(repository.Workloads, workloads...)
	}
	slices.SortFunc(repository.Workloads, func(left, right remoteportable.PortableManifestWorkload) int {
		return strings.Compare(left.UnitID, right.UnitID)
	})
	return repository, nil
}

func portableGoPackageWorkloads(modules []remoteportable.RepositoryWorkloadModule, packages []string, target portableGoPlatform) ([]remoteportable.PortableManifestWorkload, error) {
	workloads := make([]remoteportable.PortableManifestWorkload, 0, len(packages))
	for _, packageTarget := range packages {
		moduleRoot, relativePackage, err := portablePackageModule(modules, packageTarget)
		if err != nil {
			return nil, err
		}
		unitID := portablePackageWorkloadID(target.name, string(remoteportable.RepositoryWorkloadExecutionPolicyNormal), packageTarget)
		workloads = append(workloads, remoteportable.PortableManifestWorkload{
			UnitID: unitID, CommandDigest: digestBytes([]byte(unitID)), TargetPlatform: string(target.platform), RuntimeCapability: string(target.runtime),
			CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 15_000, ExecutionDigest: digestBytes([]byte("execution/v1\x00" + unitID)),
			Kind: remoteportable.RepositoryWorkloadKindGoPackage, ModuleRoot: moduleRoot, Package: relativePackage, ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal),
		})
	}
	return workloads, nil
}

func portablePackageModule(modules []remoteportable.RepositoryWorkloadModule, packageTarget string) (string, string, error) {
	if moduleRoot, relative, ok := strings.Cut(packageTarget, "\x00"); ok {
		return moduleRoot, relative, nil
	}
	for _, module := range slices.Backward(modules) {
		root := module.Root
		if root == "." {
			continue
		}
		prefix := "./" + root
		if packageTarget == prefix {
			return root, ".", nil
		}
		if relative, ok := strings.CutPrefix(packageTarget, prefix+"/"); ok {
			return root, "./" + relative, nil
		}
	}
	if slices.ContainsFunc(modules, func(module remoteportable.RepositoryWorkloadModule) bool { return module.Root == "." }) {
		return ".", packageTarget, nil
	}
	return "", "", fmt.Errorf("package %q is outside declared modules", packageTarget)
}

func portablePackageWorkloadID(platform, policy, packageTarget string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(path.Clean(packageTarget)))
	return "auto-go-package/" + platform + "/" + policy + "/" + encoded
}
