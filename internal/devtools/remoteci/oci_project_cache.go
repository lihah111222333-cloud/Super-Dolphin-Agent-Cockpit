package remoteci

import (
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// OCIProjectGoBuildCachePath 是验收后 OCI 基线镜像内唯一允许的只读 Go 构建缓存路径。
// 候选任务不得把它用作可写 GOCACHE 根目录。
const OCIProjectGoBuildCachePath = "/opt/super-dolphin/cache/go-build"

// BaselineOCIProjectCache 将不可变基线镜像内的项目构建缓存绑定到精确源码和工具链。
type BaselineOCIProjectCache struct {
	Image                 string `json:"image"`
	ContentManifestSHA256 string `json:"content_manifest_sha256"`
	MainTree              string `json:"main_tree"`
	ToolchainDigest       string `json:"toolchain_digest"`
	Platform              string `json:"platform"`
	CachePath             string `json:"cache_path"`
}

func cloneBaselineOCIProjectCache(cache *BaselineOCIProjectCache) *BaselineOCIProjectCache {
	if cache == nil {
		return nil
	}
	copy := *cache
	return &copy
}

func (cache BaselineOCIProjectCache) validate() error {
	if !validRemoteImageReference(cache.Image) ||
		!remoteDigestPattern.MatchString(cache.ContentManifestSHA256) ||
		!baselineOIDPattern.MatchString(cache.MainTree) ||
		!remoteDigestPattern.MatchString(cache.ToolchainDigest) ||
		cache.Platform != cicontract.TargetPlatform ||
		cache.CachePath != OCIProjectGoBuildCachePath {
		return errors.New("remote OCI project cache identity is invalid")
	}
	return nil
}

// ValidateForBaseline rejects an OCI cache identity that is not bound to the
// exact accepted runtime identity used by the consumer.
func (cache BaselineOCIProjectCache) ValidateForBaseline(mainTree, toolchainDigest, platform, runtimeImage string) error {
	if err := cache.validate(); err != nil {
		return err
	}
	if cache.MainTree != mainTree || cache.ToolchainDigest != toolchainDigest ||
		cache.Platform != platform || cache.Image != runtimeImage {
		return errors.New("remote OCI project cache does not match baseline identity")
	}
	return nil
}
