package eci

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type imageCacheListResponse struct {
	ImageCaches []ImageCache `json:"ImageCaches"`
}

// DescribeImageCache 查询一个缓存，并拒绝缺失或歧义的 CLI JSON 响应。
func (c *Client) DescribeImageCache(ctx context.Context, imageCacheID string) (ImageCache, error) {
	if strings.TrimSpace(imageCacheID) == "" {
		return ImageCache{}, errors.New("ECI image cache ID is required")
	}
	return c.describeImageCache(ctx, []string{"--ImageCacheId", imageCacheID}, imageCacheID)
}

// ValidateReadyImageCache 校验外部首代回执绑定的 live ECI identity 与 Ready 镜像。
func ValidateReadyImageCache(cache ImageCache, expectedID, expectedName, expectedImage string) error {
	if err := validateReadyImageCacheIdentity(cache, expectedID, expectedName); err != nil {
		return err
	}
	return validateReadyImageCacheImages(cache.Images, expectedImage)
}

// validateReadyImageCacheIdentity 校验 ECI 返回的状态、ID、名称和 snapshot。
func validateReadyImageCacheIdentity(cache ImageCache, expectedID, expectedName string) error {
	if err := validateDescribedImageCache(cache); err != nil {
		return err
	}
	if cache.Status != "Ready" || cache.ID != expectedID || cache.Name != expectedName || strings.TrimSpace(cache.SnapshotID) == "" {
		return errors.New("ECI ImageCache is not the expected immutable Ready cache")
	}
	return nil
}

// validateReadyImageCacheImages 校验 Ready cache 中只有规范 immutable runtime image。
func validateReadyImageCacheImages(images []string, expectedImage string) error {
	if err := validateImmutableImageReferences(images); err != nil {
		return fmt.Errorf("validate ECI Ready ImageCache images: %w", err)
	}
	seen := 0
	for _, image := range images {
		if image == expectedImage {
			seen++
		}
	}
	if seen != 1 {
		return errors.New("ECI Ready ImageCache runtime image does not match the provision receipt exactly once")
	}
	return nil
}

// describeImageCache 按精确查询参数读取唯一缓存，并校验调用方指定的身份。
func (c *Client) describeImageCache(ctx context.Context, args []string, expectedID string) (ImageCache, error) {
	output, err := c.run(ctx, "DescribeImageCaches", args...)
	if err != nil {
		return ImageCache{}, fmt.Errorf("describe ECI image cache: %w", err)
	}
	caches, err := decodeImageCacheList(output)
	if err != nil {
		return ImageCache{}, fmt.Errorf("decode DescribeImageCaches response: %w", err)
	}
	if len(caches) != 1 {
		return ImageCache{}, fmt.Errorf("DescribeImageCaches response contains %d image caches, want exactly one", len(caches))
	}
	cache := caches[0]
	if err := validateDescribedImageCache(cache); err != nil {
		return ImageCache{}, err
	}
	if cache.ID != expectedID {
		return ImageCache{}, fmt.Errorf("DescribeImageCaches response returned image cache %q, want %q", cache.ID, expectedID)
	}
	return cache, nil
}

// decodeImageCacheList 解码 DescribeImageCaches 的严格响应列表。
func decodeImageCacheList(output []byte) ([]ImageCache, error) {
	var response imageCacheListResponse
	if err := decodeJSON(output, &response); err != nil {
		return nil, err
	}
	return response.ImageCaches, nil
}

// validateDescribedImageCache 拒绝缺少 ECI 返回身份字段的缓存。
func validateDescribedImageCache(cache ImageCache) error {
	if strings.TrimSpace(cache.ID) == "" || strings.TrimSpace(cache.Name) == "" || strings.TrimSpace(cache.Status) == "" {
		return errors.New("DescribeImageCaches response contains an incomplete image cache")
	}
	return nil
}

// validateImmutableImageReferences 验证不可变且不重复的 ECI 镜像身份。
func validateImmutableImageReferences(images []string) error {
	if len(images) == 0 {
		return errors.New("ECI Ready ImageCache has no immutable runtime image")
	}
	seen := make(map[string]struct{}, len(images))
	for index, image := range images {
		if !imageDigestPattern.MatchString(image) {
			return fmt.Errorf("ECI image cache image %d must be an immutable OCI digest reference", index+1)
		}
		if _, exists := seen[image]; exists {
			return fmt.Errorf("ECI image cache image %d duplicates an earlier image", index+1)
		}
		seen[image] = struct{}{}
	}
	return nil
}
