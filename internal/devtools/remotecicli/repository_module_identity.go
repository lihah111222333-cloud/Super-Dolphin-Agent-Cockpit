package remotecicli

import (
	"context"
	"encoding/json"
	"errors"
	"path"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type detectedModuleIdentity struct {
	Root     string `json:"root"`
	GoModSHA string `json:"go_mod_sha256"`
	GoSumSHA string `json:"go_sum_sha256"`
}

// deriveDetectedRepositoryModuleIdentity 只绑定 exact tree 内全部 module 的 go.mod/go.sum。
func deriveDetectedRepositoryModuleIdentity(ctx context.Context, repository DetectedRepository) (string, error) {
	if ctx == nil || repository.Root == "" || repository.TreeSHA == "" || len(repository.Manifest.Modules) == 0 {
		return "", errors.New("repository module identity input is invalid")
	}
	modules := make([]detectedModuleIdentity, 0, len(repository.Manifest.Modules))
	for _, module := range repository.Manifest.Modules {
		identity, err := deriveDetectedModuleIdentity(ctx, repository, module)
		if err != nil {
			return "", err
		}
		modules = append(modules, identity)
	}
	encoded, err := json.Marshal(modules)
	if err != nil {
		return "", errors.New("repository module identity cannot be encoded")
	}
	return digestBytes(append([]byte("repository-module-identity/v1\x00"), encoded...)), nil
}

// deriveDetectedModuleIdentity 绑定一个 manifest module 的依赖文件。
func deriveDetectedModuleIdentity(ctx context.Context, repository DetectedRepository, module remoteci.RepositoryWorkloadModule) (detectedModuleIdentity, error) {
	goMod, found, err := readDetectedModuleBlob(ctx, repository, path.Join(module.Root, "go.mod"))
	if err != nil || !found {
		return detectedModuleIdentity{}, errors.New("repository module go.mod is unavailable")
	}
	goSum, sumFound, err := readDetectedModuleBlob(ctx, repository, path.Join(module.Root, "go.sum"))
	if err != nil {
		return detectedModuleIdentity{}, errors.New("repository module go.sum cannot be inspected")
	}
	sumDigest := "absent"
	if sumFound {
		sumDigest = digestBytes(goSum.Data)
	}
	return detectedModuleIdentity{Root: module.Root, GoModSHA: digestBytes(goMod.Data), GoSumSHA: sumDigest}, nil
}

// readDetectedModuleBlob 从 exact tree 区分 absent 与 regular blob。
func readDetectedModuleBlob(ctx context.Context, repository DetectedRepository, relative string) (remoteci.GitTreeBlob, bool, error) {
	listing, err := runBoundedGitObjectCommand(ctx, repository.Root, 4<<10, "ls-tree", "-z", repository.TreeSHA, "--", relative)
	if err != nil {
		return remoteci.GitTreeBlob{}, false, err
	}
	if len(listing) == 0 {
		return remoteci.GitTreeBlob{}, false, nil
	}
	blob, err := (gitObjectResolver{}).ReadTreeBlob(ctx, repository.Root, repository.TreeSHA, relative)
	if err != nil {
		return remoteci.GitTreeBlob{}, false, err
	}
	return blob, true, nil
}
