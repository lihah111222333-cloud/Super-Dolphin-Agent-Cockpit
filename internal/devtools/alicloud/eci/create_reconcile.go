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
	group, present, err := c.reconcileArtifactBuilderRequest(ctx, name, tags)
	if err != nil {
		return ContainerGroup{}, errors.Join(cause, err)
	}
	if !present {
		return ContainerGroup{}, errors.Join(cause, errors.New("reconcile ECI container group found no exact match"))
	}
	return group, nil
}

// reconcileArtifactBuilderRequest 按 name+exact tags 查询一次并区分 absent、唯一匹配和歧义。
// 同名或同标签但不同时满足两者的资源属于 identity drift，绝不被当作 absent。
func (c *Client) reconcileArtifactBuilderRequest(ctx context.Context, name string, tags map[string]string) (ContainerGroup, bool, error) {
	args := []string{"--ContainerGroupName", name}
	args = appendIndexedMap(args, "--Tag", tags)
	output, err := c.run(ctx, "DescribeContainerGroups", args...)
	if err != nil {
		return ContainerGroup{}, false, errors.New("reconcile ECI container group query failed")
	}
	var response struct {
		ContainerGroups []ContainerGroup `json:"ContainerGroups"`
	}
	if err := decodeJSON(output, &response); err != nil {
		return ContainerGroup{}, false, errors.New("reconcile ECI container group response is malformed")
	}
	return selectArtifactBuilderRequestGroup(response.ContainerGroups, name, tags)
}

// selectArtifactBuilderRequestGroup 从 provider 结果中选择唯一 exact request identity。
func selectArtifactBuilderRequestGroup(groups []ContainerGroup, name string, tags map[string]string) (ContainerGroup, bool, error) {
	matches := make([]ContainerGroup, 0, len(groups))
	identityDrift := false
	for _, group := range groups {
		exact, drift := classifyArtifactBuilderRequestGroup(group, name, tags)
		if exact {
			matches = append(matches, group)
			continue
		}
		if drift {
			identityDrift = true
		}
	}
	if identityDrift {
		return ContainerGroup{}, false, ErrArtifactBuilderIdentityDrift
	}
	switch len(matches) {
	case 0:
		return ContainerGroup{}, false, nil
	case 1:
		return matches[0], true, nil
	default:
		return ContainerGroup{}, false, errors.New("reconcile ECI container group found multiple exact matches")
	}
}

// classifyArtifactBuilderRequestGroup 区分 exact match、identity drift 与无关资源。
func classifyArtifactBuilderRequestGroup(group ContainerGroup, name string, tags map[string]string) (exact, drift bool) {
	sameName := group.Name == name
	exactTags := exactContainerGroupTags(group.Tags, tags)
	return sameName && exactTags && group.ID != "", sameName || exactTags
}

// exactContainerGroupTags 要求控制面返回的标签集合与创建请求逐项相等，
// 包括数量、键唯一性和值，避免仅按名称误认其他容器组。
func exactContainerGroupTags(actual []ContainerGroupTag, expected map[string]string) bool {
	if len(actual) != len(expected) {
		return false
	}
	seen := make(map[string]struct{}, len(actual))
	for _, tag := range actual {
		want, ok := expected[tag.Key]
		if !ok || want != tag.Value {
			return false
		}
		if _, duplicate := seen[tag.Key]; duplicate {
			return false
		}
		seen[tag.Key] = struct{}{}
	}
	return len(seen) == len(expected)
}
