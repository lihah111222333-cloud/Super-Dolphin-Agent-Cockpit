package eci

import (
	"context"
	"errors"
	"fmt"
)

// decodeCreatedContainerGroup 校验 CreateContainerGroup 的最小成功响应。
func decodeCreatedContainerGroup(output []byte, name string) (ContainerGroup, error) {
	var response struct {
		ContainerGroupID string `json:"ContainerGroupId"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return ContainerGroup{}, fmt.Errorf("decode CreateContainerGroup response: %w", err)
	}
	if response.ContainerGroupID == "" {
		return ContainerGroup{}, errors.New("CreateContainerGroup response is missing ContainerGroupId")
	}
	return ContainerGroup{ID: response.ContainerGroupID, Name: name}, nil
}

// reconcileCreatedContainerGroup 在 Create 结果不确定时按唯一名称和标签回查真实资源。
func (c *Client) reconcileCreatedContainerGroup(
	ctx context.Context,
	name string,
	tags map[string]string,
	cause error,
) (ContainerGroup, error) {
	args := []string{"--ContainerGroupName", name}
	args = appendIndexedMap(args, "--Tag", tags)
	output, err := c.run(ctx, "DescribeContainerGroups", args...)
	if err != nil {
		return ContainerGroup{}, errors.Join(cause, fmt.Errorf("reconcile ECI container group %s: %w", name, err))
	}
	var response struct {
		ContainerGroups []ContainerGroup `json:"ContainerGroups"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return ContainerGroup{}, errors.Join(cause, fmt.Errorf("decode reconciled ECI container groups: %w", err))
	}
	matches := make([]ContainerGroup, 0, len(response.ContainerGroups))
	for _, group := range response.ContainerGroups {
		if group.Name == name && group.ID != "" {
			matches = append(matches, group)
		}
	}
	if len(matches) != 1 {
		return ContainerGroup{}, errors.Join(cause, fmt.Errorf("reconcile ECI container group %s: found %d matches", name, len(matches)))
	}
	return matches[0], nil
}
