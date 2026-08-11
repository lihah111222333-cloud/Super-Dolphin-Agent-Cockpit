package eci

import (
	"context"
	"errors"
	"fmt"
)

// describeContainerGroupBatchMode 查询一个批次；只有 absence proof 才允许 provider 明确返回空集合。
func (c *Client) describeContainerGroupBatchMode(ctx context.Context, ids []string, allowEmpty bool) ([]ContainerGroup, error) {
	encodedIDs, err := encodeContainerGroupIDs(ids)
	if err != nil {
		return nil, fmt.Errorf("encode ECI container group IDs: %w", err)
	}
	output, err := c.run(ctx, "DescribeContainerGroups", "--ContainerGroupIds", string(encodedIDs), "--WithEvent", "true")
	if err != nil {
		return nil, fmt.Errorf("describe ECI container groups: %w", err)
	}
	var response struct {
		ContainerGroups *[]ContainerGroup `json:"ContainerGroups"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return nil, fmt.Errorf("decode DescribeContainerGroups response: %w", err)
	}
	if response.ContainerGroups == nil {
		return nil, errors.New("DescribeContainerGroups response is missing ContainerGroups")
	}
	groups := *response.ContainerGroups
	if !allowEmpty || len(groups) != 0 {
		if err := validateContainerGroups(groups); err != nil {
			return nil, err
		}
	}
	return groups, nil
}

// ConfirmContainerGroupAbsent 只有 provider 成功明确返回空集合才确认 ECI 分组已消失。
func (c *Client) ConfirmContainerGroupAbsent(ctx context.Context, containerGroupID string) (bool, error) {
	if err := validateContainerGroupIDs([]string{containerGroupID}); err != nil {
		return false, fmt.Errorf("confirm ECI container group absence: %w", err)
	}
	groups, err := c.describeContainerGroupBatchMode(ctx, []string{containerGroupID}, true)
	if err != nil {
		return false, fmt.Errorf("confirm ECI container group absence: %w", err)
	}
	if len(groups) == 0 {
		return true, nil
	}
	for _, group := range groups {
		if group.ID == containerGroupID {
			return false, nil
		}
	}
	return false, fmt.Errorf("DescribeContainerGroups response omitted requested ECI container group %q", containerGroupID)
}
