package eci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// FindContainerGroupsByNameAndJobTag 以确定名称与精确 job tag 定位待回收实例。
// 零结果表示实例已不存在；唯一性和字段漂移由 cleanup-only 调用方继续校验。
func (c *Client) FindContainerGroupsByNameAndJobTag(ctx context.Context, name, jobID string) ([]ContainerGroup, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(jobID) == "" {
		return nil, errors.New("ECI cleanup name and job tag are required")
	}
	tags, err := json.Marshal([]ContainerGroupTag{{Key: "super-dolphin-job", Value: jobID}})
	if err != nil {
		return nil, fmt.Errorf("encode ECI cleanup job tag: %w", err)
	}
	output, err := c.run(ctx, "DescribeContainerGroups", "--ContainerGroupName", name, "--Tag", string(tags), "--WithEvent", "true")
	if err != nil {
		return nil, fmt.Errorf("find ECI container group by name and job tag: %w", err)
	}
	var response struct {
		ContainerGroups []ContainerGroup `json:"ContainerGroups"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return nil, fmt.Errorf("decode ECI cleanup discovery response: %w", err)
	}
	return response.ContainerGroups, nil
}
