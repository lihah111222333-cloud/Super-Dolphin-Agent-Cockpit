package remotecicli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type portableGoPlatform struct {
	name     string
	platform remoteci.RepositoryWorkloadPlatform
	runtime  remoteci.RepositoryWorkloadRuntime
}

func portableGoCatalogPlatforms() [2]portableGoPlatform {
	return [2]portableGoPlatform{
		{name: "linux-amd64", platform: remoteci.RepositoryWorkloadPlatformLinuxAMD64, runtime: remoteci.RepositoryWorkloadRuntimeUbuntuContainer},
		{name: "windows-amd64", platform: remoteci.RepositoryWorkloadPlatformWindowsAMD64, runtime: remoteci.RepositoryWorkloadRuntimeWindowsNative},
	}
}

// expandDetectedRepositoryGoCatalog 从 exact tree 自动派生全部平台 Go package workload。
func expandDetectedRepositoryGoCatalog(ctx context.Context, repository DetectedRepository) (DetectedRepository, error) {
	if ctx == nil || repository.Root == "" || repository.TreeSHA == "" {
		return DetectedRepository{}, errors.New("portable Go catalog repository identity is invalid")
	}
	manifest := repository.Manifest
	for _, target := range portableGoCatalogPlatforms() {
		inventory, err := remoteci.BuildPortableGoWorkloadInventory(ctx, repository.Root, repository.TreeSHA, string(target.platform), manifest.Modules)
		if err != nil {
			return DetectedRepository{}, fmt.Errorf("build %s portable Go inventory: %w", target.name, err)
		}
		workloads, err := portableGoPackageWorkloads(manifest.Modules, inventory.GoPackages, target)
		if err != nil {
			return DetectedRepository{}, err
		}
		manifest.Workloads = append(manifest.Workloads, workloads...)
	}
	slices.SortFunc(manifest.Workloads, func(left, right remoteci.RepositoryWorkload) int { return strings.Compare(left.ID, right.ID) })
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		return DetectedRepository{}, fmt.Errorf("encode portable Go catalog: %w", err)
	}
	manifest, err = remoteci.DecodeRepositoryWorkloadManifest(encoded)
	if err != nil {
		return DetectedRepository{}, err
	}
	repository.Manifest, repository.ManifestSHA256 = manifest, digestBytes(encoded)
	return repository, nil
}

func portableGoPackageWorkloads(modules []remoteci.RepositoryWorkloadModule, packages []string, target portableGoPlatform) ([]remoteci.RepositoryWorkload, error) {
	workloads := make([]remoteci.RepositoryWorkload, 0, len(packages))
	for _, packageTarget := range packages {
		moduleRoot, relativePackage, err := portablePackageModule(modules, packageTarget)
		if err != nil {
			return nil, err
		}
		workloads = append(workloads, remoteci.RepositoryWorkload{
			ID: portablePackageWorkloadID(target.name, string(remoteci.RepositoryWorkloadExecutionPolicyNormal), packageTarget), Kind: remoteci.RepositoryWorkloadKindGoPackage,
			ModuleRoot: moduleRoot, Package: relativePackage, Platform: target.platform, Runtime: target.runtime,
			BootstrapEstimateMS: 15_000, CPUMilli: 1_000, MemoryBytes: 1 << 30, ExecutionPolicy: remoteci.RepositoryWorkloadExecutionPolicyNormal,
		})
	}
	return workloads, nil
}

func portablePackageModule(modules []remoteci.RepositoryWorkloadModule, packageTarget string) (string, string, error) {
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
	if slices.ContainsFunc(modules, func(module remoteci.RepositoryWorkloadModule) bool { return module.Root == "." }) {
		return ".", packageTarget, nil
	}
	return "", "", fmt.Errorf("package %q is outside declared modules", packageTarget)
}

func portablePackageWorkloadID(platform, policy, packageTarget string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(path.Clean(packageTarget)))
	return "auto-go-package/" + platform + "/" + policy + "/" + encoded
}
