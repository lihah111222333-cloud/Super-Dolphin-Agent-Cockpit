package gateclosure

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type ociIndex struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Manifests     []ociDescriptor   `json:"manifests"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

type ociDescriptor struct {
	MediaType string      `json:"mediaType"`
	Digest    string      `json:"digest"`
	Size      int64       `json:"size"`
	Platform  ociPlatform `json:"platform"`
}

type ociPlatform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

type ociManifest struct {
	SchemaVersion int `json:"schemaVersion"`
	Config        struct {
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"config"`
	Layers []struct {
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"layers"`
}

type ociImageConfig struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant,omitempty"`
	RootFS       struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids"`
	} `json:"rootfs"`
}

// publishRuntimeDepsIndex 使用宿主 Docker 凭据将已推送 refresh 索引标记为 locked，并以匿名读取严格核验摘要和平台描述符。
func publishRuntimeDepsIndex(repository, reference, sourceReference string, platforms []string) error {
	registry, err := runtimeDepsRegistryFactory(repository)
	if err != nil {
		return err
	}
	sourceDocument, err := registry.readManifest(sourceReference)
	if err != nil {
		return err
	}
	descriptors, err := runtimeDepsIndexDescriptors(sourceDocument, platforms)
	if err != nil {
		return err
	}
	source, err := runtimeDepsImageReference(repository, sourceReference)
	if err != nil {
		return err
	}
	locked, err := runtimeDepsImageReference(repository, reference)
	if err != nil {
		return err
	}
	if err := runtimeDepsRunCommand(runtimeDepsBuildTimeout, "docker", "buildx", "imagetools", "create", "--tag", locked, source); err != nil {
		return fmt.Errorf("publish locked runtime dependency OCI index through Docker credential store: %w", err)
	}
	return verifyPublishedRuntimeDepsIndex(registry, reference, sourceReference, platforms, descriptors)
}

// verifyPublishedRuntimeDepsIndex 以匿名读取复核 locked 标签与 refresh 索引完全一致。
func verifyPublishedRuntimeDepsIndex(registry runtimeDepsRegistry, reference, sourceReference string, platforms []string, descriptors []ociDescriptor) error {
	published, err := registry.readManifest(reference)
	if err != nil {
		return fmt.Errorf("verify published runtime dependency index: %w", err)
	}
	sourceDocument, err := registry.readManifest(sourceReference)
	if err != nil {
		return fmt.Errorf("re-read refresh runtime dependency index: %w", err)
	}
	if published.Digest != sourceDocument.Digest || published.MediaType != sourceDocument.MediaType {
		return errors.New("published runtime dependency index digest or media type differs from refresh index")
	}
	publishedDescriptors, err := runtimeDepsIndexDescriptors(published, platforms)
	if err != nil {
		return fmt.Errorf("verify published runtime dependency platform descriptors: %w", err)
	}
	if !slices.Equal(publishedDescriptors, descriptors) {
		return errors.New("published runtime dependency platform descriptors differ from refresh index")
	}
	return nil
}

// runtimeDepsImageReference 将受校验的 tag 或不可变摘要转换为 Docker/buildx 可发布的镜像引用。
func runtimeDepsImageReference(repository, reference string) (string, error) {
	if validSHA256(reference) {
		return repository + "@" + reference, nil
	}
	registry, err := runtimeDepsRegistryFactory(repository)
	if err != nil {
		return "", err
	}
	if _, err := registry.manifestURL(reference); err != nil {
		return "", err
	}
	return repository + ":" + reference, nil
}

func isImageManifestMediaType(mediaType string) bool {
	return mediaType == ociManifestMediaType || mediaType == dockerManifestMediaType
}

func isImageIndexMediaType(mediaType string) bool {
	return mediaType == ociIndexMediaType || mediaType == dockerIndexMediaType
}

// runtimeDepsIndexDescriptors 从一次已验证的 index 响应解析锁定平台描述符。
func runtimeDepsIndexDescriptors(document registryManifest, platforms []string) ([]ociDescriptor, error) {
	if !slices.Equal(platforms, runtimeDepsPlatforms) {
		return nil, errors.New("runtime dependency platforms must be exactly linux/amd64 and linux/arm64")
	}
	var index ociIndex
	if err := json.Unmarshal(document.Data, &index); err != nil {
		return nil, fmt.Errorf("decode runtime dependency OCI index: %w", err)
	}
	if err := validateRuntimeDepsIndexDocument(document, index); err != nil {
		return nil, err
	}
	byPlatform, err := runtimeDepsDescriptorsByPlatform(index.Manifests, platforms)
	if err != nil {
		return nil, err
	}
	return orderedRuntimeDepsDescriptors(byPlatform, platforms)
}

func validateRuntimeDepsIndexDocument(document registryManifest, index ociIndex) error {
	if index.SchemaVersion != 2 || !isImageIndexMediaType(index.MediaType) || !isImageIndexMediaType(document.MediaType) {
		return errors.New("runtime dependency publication did not produce an OCI index")
	}
	return nil
}

func runtimeDepsDescriptorsByPlatform(manifests []ociDescriptor, platforms []string) (map[string]ociDescriptor, error) {
	byPlatform := make(map[string]ociDescriptor, len(manifests))
	for _, descriptor := range manifests {
		platform, err := validateRuntimeDepsDescriptor(descriptor, platforms)
		if err != nil {
			return nil, err
		}
		if _, exists := byPlatform[platform]; exists {
			return nil, fmt.Errorf("runtime dependency OCI index has duplicate %s manifests", platform)
		}
		byPlatform[platform] = descriptor
	}
	if len(byPlatform) != len(platforms) {
		return nil, errors.New("runtime dependency OCI index does not contain exactly the locked platforms")
	}
	return byPlatform, nil
}

// validateRuntimeDepsDescriptor 校验索引描述符并返回其规范平台名称。
func validateRuntimeDepsDescriptor(descriptor ociDescriptor, platforms []string) (string, error) {
	platform := descriptor.Platform.OS + "/" + descriptor.Platform.Architecture
	if descriptor.Platform.Variant != "" || !slices.Contains(platforms, platform) || descriptor.Size <= 0 ||
		!validSHA256(descriptor.Digest) || !isImageManifestMediaType(descriptor.MediaType) {
		return "", errors.New("runtime dependency OCI index has an invalid platform manifest")
	}
	return platform, nil
}

func orderedRuntimeDepsDescriptors(byPlatform map[string]ociDescriptor, platforms []string) ([]ociDescriptor, error) {
	descriptors := make([]ociDescriptor, 0, len(platforms))
	for _, platform := range platforms {
		descriptor, exists := byPlatform[platform]
		if !exists {
			return nil, fmt.Errorf("runtime dependency OCI index has no %s manifest", platform)
		}
		descriptors = append(descriptors, descriptor)
	}
	return descriptors, nil
}

// inspectRuntimeDepsImages 交叉验证索引、平台清单、配置根文件系统和每个平台的聚合大小。
func inspectRuntimeDepsImages(repository, reference string, platforms []string) ([]runtimeDepsImage, error) {
	registry, err := runtimeDepsRegistryFactory(repository)
	if err != nil {
		return nil, err
	}
	indexDocument, err := registry.readManifest(reference)
	if err != nil {
		return nil, fmt.Errorf("inspect runtime dependency OCI index: %w", err)
	}
	descriptors, err := runtimeDepsIndexDescriptors(indexDocument, platforms)
	if err != nil {
		return nil, err
	}
	images := make([]runtimeDepsImage, 0, len(descriptors))
	for _, descriptor := range descriptors {
		image, err := inspectRuntimeDepsImage(registry, repository, indexDocument.Digest, descriptor)
		if err != nil {
			return nil, err
		}
		images = append(images, image)
	}
	if err := validateRuntimeDepsImageSet(images); err != nil {
		return nil, fmt.Errorf("validate generated runtime dependency image set: %w", err)
	}
	return images, nil
}

// inspectRuntimeDepsImage 读取并验证单个平台 manifest、config 与 rootfs 身份。
func inspectRuntimeDepsImage(registry runtimeDepsRegistry, repository, indexDigest string, descriptor ociDescriptor) (runtimeDepsImage, error) {
	document, err := registry.readManifest(descriptor.Digest)
	if err != nil {
		return runtimeDepsImage{}, fmt.Errorf("inspect runtime dependency platform manifest: %w", err)
	}
	var manifest ociManifest
	if err := json.Unmarshal(document.Data, &manifest); err != nil {
		return runtimeDepsImage{}, fmt.Errorf("decode runtime dependency platform manifest: %w", err)
	}
	if err := validateRuntimeDepsPlatformManifest(document, descriptor, manifest); err != nil {
		return runtimeDepsImage{}, err
	}
	config, err := readRuntimeDepsConfig(registry, manifest)
	if err != nil {
		return runtimeDepsImage{}, err
	}
	platform := descriptor.Platform.OS + "/" + descriptor.Platform.Architecture
	if err := validateRuntimeDepsConfig(config, manifest, platform); err != nil {
		return runtimeDepsImage{}, err
	}
	size, err := runtimeDepsImageSize(manifest)
	if err != nil {
		return runtimeDepsImage{}, err
	}
	identity := gatecontract.ImageIdentity{
		Registry: repository, OCIIndexDigest: indexDigest, PlatformManifestDigest: descriptor.Digest,
		ConfigDigest: manifest.Config.Digest, RootFSDiffIDs: config.RootFS.DiffIDs,
		OS: config.OS, Architecture: config.Architecture, Variant: config.Variant,
	}
	if err := identity.Validate(); err != nil {
		return runtimeDepsImage{}, err
	}
	return runtimeDepsImage{Platform: platform, Image: identity, ImageSize: size}, nil
}

// validateRuntimeDepsPlatformManifest 交叉校验描述符与平台 manifest 的摘要和结构。
func validateRuntimeDepsPlatformManifest(document registryManifest, descriptor ociDescriptor, manifest ociManifest) error {
	if document.Digest != descriptor.Digest || !isImageManifestMediaType(document.MediaType) || manifest.SchemaVersion != 2 {
		return errors.New("runtime dependency platform manifest is incomplete")
	}
	if !validSHA256(manifest.Config.Digest) || manifest.Config.Size <= 0 || len(manifest.Layers) == 0 {
		return errors.New("runtime dependency platform manifest is incomplete")
	}
	return nil
}

func readRuntimeDepsConfig(registry runtimeDepsRegistry, manifest ociManifest) (ociImageConfig, error) {
	data, err := registry.readConfigBlob(manifest.Config.Digest, manifest.Config.Size)
	if err != nil {
		return ociImageConfig{}, err
	}
	var config ociImageConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return ociImageConfig{}, fmt.Errorf("decode runtime dependency image config: %w", err)
	}
	return config, nil
}

func validateRuntimeDepsConfig(config ociImageConfig, manifest ociManifest, platform string) error {
	if config.RootFS.Type != "layers" || len(config.RootFS.DiffIDs) != len(manifest.Layers) {
		return errors.New("runtime dependency image config rootfs or platform is incomplete")
	}
	if config.OS+"/"+config.Architecture != platform || config.Variant != "" {
		return errors.New("runtime dependency image config rootfs or platform is incomplete")
	}
	return nil
}

func runtimeDepsImageSize(manifest ociManifest) (int64, error) {
	size := manifest.Config.Size
	for _, layer := range manifest.Layers {
		if !validSHA256(layer.Digest) || layer.Size <= 0 || size > int64(^uint64(0)>>1)-layer.Size {
			return 0, errors.New("runtime dependency layer identity is invalid")
		}
		size += layer.Size
	}
	return size, nil
}
