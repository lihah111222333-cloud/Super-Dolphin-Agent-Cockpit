package eci

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const imageCachePollInterval = 2 * time.Second

// CreateImageCache 为不可变 OCI 镜像创建显式命名缓存。
// 层复用和极速缓存不可关闭，避免创建路径静默退化。
func (c *Client) CreateImageCache(ctx context.Context, request ImageCacheCreateRequest) (ImageCache, error) {
	if err := validateImageCacheCreateRequest(request); err != nil {
		return ImageCache{}, err
	}
	args := []string{
		"--VSwitchId", c.config.VSwitchID,
		"--SecurityGroupId", c.config.SecurityGroupID,
		"--ImageCacheName", request.ImageCacheName,
		"--ImageCacheSize", fmt.Sprintf("%d", request.ImageCacheSize),
		"--AutoMatchImageCache", "true",
		"--Flash", "true",
	}
	if request.RetentionDays != 0 {
		args = append(args, "--RetentionDays", fmt.Sprintf("%d", request.RetentionDays))
	}
	args = appendIndexedValues(args, "--Image", request.Images)
	args = appendIndexedMap(args, "--Tag", request.Tags)
	output, err := c.run(ctx, "CreateImageCache", args...)
	if err != nil {
		createErr := fmt.Errorf("create ECI image cache: %w", err)
		if !isTransientCLIError(createErr) {
			return ImageCache{}, createErr
		}
		return c.reconcileCreatedImageCache(ctx, request.ImageCacheName, createErr)
	}
	cache, err := decodeCreatedImageCache(output, request.ImageCacheName)
	if err != nil {
		return c.reconcileCreatedImageCache(ctx, request.ImageCacheName, err)
	}
	return cache, nil
}

// DescribeImageCache 查询一个缓存，并拒绝缺失或歧义的 CLI JSON 响应。
func (c *Client) DescribeImageCache(ctx context.Context, imageCacheID string) (ImageCache, error) {
	if strings.TrimSpace(imageCacheID) == "" {
		return ImageCache{}, errors.New("ECI image cache ID is required")
	}
	return c.describeImageCache(ctx, []string{"--ImageCacheId", imageCacheID}, imageCacheID)
}

// WaitImageCacheReady 仅等待显式创建的缓存进入 Ready。
// 失败和未知状态返回最后事件证据，绝不允许改为缓存未命中。
func (c *Client) WaitImageCacheReady(ctx context.Context, imageCacheID string) (ImageCache, error) {
	for {
		cache, err := c.DescribeImageCache(ctx, imageCacheID)
		if err != nil {
			return ImageCache{}, err
		}
		switch cache.Status {
		case "Ready":
			return cache, nil
		case "Preparing", "Creating", "Updating":
			if err := c.wait(ctx, imageCachePollInterval); err != nil {
				return ImageCache{}, fmt.Errorf("wait for ECI image cache %s: %w", imageCacheID, err)
			}
		case "Failed", "UpdateFailed":
			return cache, fmt.Errorf("ECI image cache %s reached %s: %s", imageCacheID, cache.Status, formatImageCacheEvents(cache.Events))
		default:
			return cache, fmt.Errorf("ECI image cache %s returned unsupported status %q: %s", imageCacheID, cache.Status, formatImageCacheEvents(cache.Events))
		}
	}
}

// DeleteImageCache 删除调用方拥有的缓存，并要求 ECI 返回请求回执。
func (c *Client) DeleteImageCache(ctx context.Context, imageCacheID string) error {
	if strings.TrimSpace(imageCacheID) == "" {
		return errors.New("ECI image cache ID is required")
	}
	output, err := c.run(ctx, "DeleteImageCache", "--ImageCacheId", imageCacheID)
	if err != nil {
		return fmt.Errorf("delete ECI image cache: %w", err)
	}
	var response struct {
		RequestID string `json:"RequestId"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return fmt.Errorf("decode DeleteImageCache response: %w", err)
	}
	if strings.TrimSpace(response.RequestID) == "" {
		return errors.New("DeleteImageCache response is missing RequestId")
	}
	return nil
}

func (c *Client) describeImageCache(ctx context.Context, args []string, expectedID string) (ImageCache, error) {
	output, err := c.run(ctx, "DescribeImageCaches", args...)
	if err != nil {
		return ImageCache{}, fmt.Errorf("describe ECI image cache: %w", err)
	}
	var response struct {
		ImageCaches []ImageCache `json:"ImageCaches"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return ImageCache{}, fmt.Errorf("decode DescribeImageCaches response: %w", err)
	}
	if len(response.ImageCaches) != 1 {
		return ImageCache{}, fmt.Errorf("DescribeImageCaches response contains %d image caches, want exactly one", len(response.ImageCaches))
	}
	cache := response.ImageCaches[0]
	if strings.TrimSpace(cache.ID) == "" || strings.TrimSpace(cache.Name) == "" || strings.TrimSpace(cache.Status) == "" {
		return ImageCache{}, errors.New("DescribeImageCaches response contains an incomplete image cache")
	}
	if expectedID != "" && cache.ID != expectedID {
		return ImageCache{}, fmt.Errorf("DescribeImageCaches response returned image cache %q, want %q", cache.ID, expectedID)
	}
	return cache, nil
}

func (c *Client) reconcileCreatedImageCache(ctx context.Context, name string, cause error) (ImageCache, error) {
	cache, err := c.describeImageCache(ctx, []string{"--ImageCacheName", name}, "")
	if err != nil {
		return ImageCache{}, errors.Join(cause, fmt.Errorf("reconcile ECI image cache %s: %w", name, err))
	}
	if cache.Name != name {
		return ImageCache{}, errors.Join(cause, fmt.Errorf("reconcile ECI image cache %s: returned %q", name, cache.Name))
	}
	return cache, nil
}

func validateImageCacheCreateRequest(request ImageCacheCreateRequest) error {
	if !eciNamePattern.MatchString(request.ImageCacheName) {
		return errors.New("ECI image cache name is invalid")
	}
	if request.ImageCacheSize < 20 || request.ImageCacheSize > 32768 {
		return errors.New("ECI image cache size must be between 20 and 32768 GiB")
	}
	if request.RetentionDays < 0 || request.RetentionDays > 65536 {
		return errors.New("ECI image cache retention days must be between 0 and 65536")
	}
	if len(request.Images) == 0 {
		return errors.New("ECI image cache requires at least one image")
	}
	seen := make(map[string]struct{}, len(request.Images))
	for index, image := range request.Images {
		if !imageDigestPattern.MatchString(image) {
			return fmt.Errorf("ECI image cache image %d must be an immutable OCI digest reference", index+1)
		}
		if _, exists := seen[image]; exists {
			return fmt.Errorf("ECI image cache image %d duplicates an earlier image", index+1)
		}
		seen[image] = struct{}{}
	}
	return validateRequestTags(CreateRequest{Tags: request.Tags})
}

func decodeCreatedImageCache(output []byte, name string) (ImageCache, error) {
	var response struct {
		ImageCacheID string `json:"ImageCacheId"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return ImageCache{}, fmt.Errorf("decode CreateImageCache response: %w", err)
	}
	if strings.TrimSpace(response.ImageCacheID) == "" {
		return ImageCache{}, errors.New("CreateImageCache response is missing ImageCacheId")
	}
	return ImageCache{ID: response.ImageCacheID, Name: name}, nil
}

func formatImageCacheEvents(events []ContainerGroupEvent) string {
	if len(events) == 0 {
		return "no ECI events returned"
	}
	parts := make([]string, 0, len(events))
	for _, event := range events {
		parts = append(parts, fmt.Sprintf("%s/%s: %s", event.Type, event.Reason, event.Message))
	}
	return strings.Join(parts, "; ")
}
