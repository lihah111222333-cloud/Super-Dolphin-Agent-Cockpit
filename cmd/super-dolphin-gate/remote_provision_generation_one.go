package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type remoteGenerationOneECIVerifier interface {
	DescribeImageCache(context.Context, string) (eci.ImageCache, error)
	DescribeContainerGroups(context.Context, ...string) ([]eci.ContainerGroup, error)
}

// initializeConfiguredRemoteGenerationOne 仅在 accepted singleton 为空时消费配置携带的严格 ECI 首代回执。
// 并发调用只允许同一 state digest 幂等收敛；不同首代候选仍立即拒绝。
func initializeConfiguredRemoteGenerationOne(config remoteRunConfig, ledgerPath string) error {
	if _, _, err := configuredRemoteGenerationOneProvision(config); err != nil {
		return err
	}
	client, err := newRemoteGenerationOneVerifier(config)
	if err != nil {
		return infrastructureError("create ECI generation-one verifier: %v", err)
	}
	verifyContext, cancel := gateprivate.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	return initializeConfiguredRemoteGenerationOneWithVerifier(verifyContext, config, ledgerPath, client)
}

// initializeConfiguredRemoteGenerationOneWithVerifier 承载首代验证和原子首写，供真实 ECI 客户端与聚焦测试共用。
func initializeConfiguredRemoteGenerationOneWithVerifier(ctx context.Context, config remoteRunConfig, ledgerPath string, verifier remoteGenerationOneECIVerifier) error {
	if verifier == nil {
		return errors.New("generation-one ECI verifier is required")
	}
	receipt, receiptJSON, err := configuredRemoteGenerationOneProvision(config)
	if err != nil {
		return err
	}
	if err := verifyRemoteGenerationOneProvisionECIWithClient(ctx, config, receipt, verifier); err != nil {
		return err
	}
	store, err := baselineLedger(ledgerPath)
	if err != nil {
		return infrastructureError("open generation-one SQLite authority: %v", err)
	}
	return insertConfiguredRemoteGenerationOne(store, receipt, receiptJSON)
}

// configuredRemoteGenerationOneProvision 严格验证配置中的协议输入，SQLite 仍是 accepted 唯一真相源。
func configuredRemoteGenerationOneProvision(config remoteRunConfig) (cicontract.GenerationOneProvisionReceipt, []byte, error) {
	if config.GenerationOneProvision == nil {
		return cicontract.GenerationOneProvisionReceipt{}, nil, errors.New("empty remote CI SQLite requires generation_one_provision for generation one")
	}
	receipt := *config.GenerationOneProvision
	if err := cicontract.ValidateGenerationOneProvisionChecks(receipt); err != nil {
		return cicontract.GenerationOneProvisionReceipt{}, nil, fmt.Errorf("validate configured generation-one content checks: %w", err)
	}
	receiptJSON, _, err := cicontract.EncodeGenerationOneProvisionReceipt(receipt)
	if err != nil {
		return cicontract.GenerationOneProvisionReceipt{}, nil, fmt.Errorf("validate configured generation-one provision receipt: %w", err)
	}
	if err := validateRemoteGenerationOneProvisionInput(config, receipt); err != nil {
		return cicontract.GenerationOneProvisionReceipt{}, nil, fmt.Errorf("validate configured generation-one provision input: %w", err)
	}
	return receipt, receiptJSON, nil
}

// insertConfiguredRemoteGenerationOne 执行唯一首代 INSERT，并只把相同摘要的并发赢家视为幂等成功。
func insertConfiguredRemoteGenerationOne(store *gatecontract.DurationLedgerStore, receipt cicontract.GenerationOneProvisionReceipt, receiptJSON []byte) error {
	if _, err := store.InitializeRemoteBaselineGenerationOne(receiptJSON); err == nil {
		return nil
	} else if !errors.Is(err, gatecontract.ErrRemoteBaselineGenerationOneAlreadyInitialized) {
		return err
	}
	record, err := store.LoadRemoteBaselineState()
	if err != nil {
		return fmt.Errorf("reload concurrently initialized generation one: %w", err)
	}
	if record.Generation != 1 || record.StateSHA256 != receipt.StateSHA256 {
		return errors.New("concurrent generation-one initialization selected a different accepted state")
	}
	return nil
}

// configuredRemoteGenerationOneAlreadyAccepted 允许同态并发 loser 在 ECI 证据已清理后严格重读赢家状态。
func configuredRemoteGenerationOneAlreadyAccepted(config remoteRunConfig, ledgerPath string) bool {
	if config.GenerationOneProvision == nil {
		return false
	}
	store, err := baselineLedger(ledgerPath)
	if err != nil {
		return false
	}
	record, err := store.LoadRemoteBaselineState()
	if err != nil {
		return false
	}
	return record.Generation == 1 && record.StateSHA256 == config.GenerationOneProvision.StateSHA256
}

// validateRemoteGenerationOneProvisionInput 校验状态绑定和固定校准资源，拒绝配置漂移。
func validateRemoteGenerationOneProvisionInput(config remoteRunConfig, receipt cicontract.GenerationOneProvisionReceipt) error {
	if receipt.ExecutionProvider != cicontract.ExecutionProviderID || receipt.RegionID != config.RegionID {
		return errors.New("generation-one receipt provider or region does not match the Alibaba Cloud ECI config")
	}
	state, err := decodeGenerationOneProvisionState(receipt)
	if err != nil {
		return fmt.Errorf("validate generation-one baseline state: %w", err)
	}
	if err := validateGenerationOneProvisionStateBinding(receipt, state); err != nil {
		return fmt.Errorf("validate generation-one receipt/state binding: %w", err)
	}
	if err := validateRemoteGenerationOneProvisionResources(config, receipt); err != nil {
		return fmt.Errorf("validate generation-one provision resources: %w", err)
	}
	calibration, err := config.Capacity.ResourcePolicy.ResolveCalibrationClass()
	if err != nil {
		return fmt.Errorf("resolve generation-one calibration resources: %w", err)
	}
	return validateRemoteGenerationOneCalibration(receipt, calibration.ID, calibration.VCPU, calibration.MemoryGiB)
}

// validateRemoteGenerationOneProvisionResources 确认每项内容检查声明的 normal 资源档位与当前策略一致。
func validateRemoteGenerationOneProvisionResources(config remoteRunConfig, receipt cicontract.GenerationOneProvisionReceipt) error {
	for _, observation := range receipt.ProvisionChecks {
		resourceClass, err := config.Capacity.ResourcePolicy.ResolveClass(observation.ResourceClassID)
		if err != nil {
			return fmt.Errorf("provision check %q resource class %q is not a normal class: %w", observation.Check, observation.ResourceClassID, err)
		}
		if resourceClass.VCPU != observation.ResourceCPU || resourceClass.MemoryGiB != observation.ResourceMemoryGiB {
			return fmt.Errorf("provision check %q resource class %q does not match %.0f vCPU/%.0f GiB", observation.Check, observation.ResourceClassID, observation.ResourceCPU, observation.ResourceMemoryGiB)
		}
	}
	return nil
}

// validateRemoteGenerationOneCalibration 确认回执使用远程配置声明的唯一固定规格。
func validateRemoteGenerationOneCalibration(receipt cicontract.GenerationOneProvisionReceipt, classID string, cpu, memoryGiB float64) error {
	if receipt.CalibrationClassID != classID || receipt.CalibrationCPU != cpu || receipt.CalibrationMemoryGiB != memoryGiB {
		return errors.New("generation-one provision receipt calibration resources do not match remote config")
	}
	return nil
}

// verifyRemoteGenerationOneProvisionECIWithClient 将导入边界绑定到阿里云控制面实时事实。
func verifyRemoteGenerationOneProvisionECIWithClient(ctx context.Context, config remoteRunConfig, receipt cicontract.GenerationOneProvisionReceipt, client remoteGenerationOneECIVerifier) error {
	live, err := client.DescribeImageCache(ctx, receipt.ImageCacheID)
	if err != nil {
		return infrastructureError("describe generation-one ECI ImageCache: %v", err)
	}
	if err := validateGenerationOneLiveImageCache(live, config.RegionID, receipt); err != nil {
		return infrastructureError("validate generation-one ECI ImageCache Ready state: %v", err)
	}
	groupIDs := generationOneProvisionContainerGroupIDs(receipt)
	groups, err := client.DescribeContainerGroups(ctx, groupIDs...)
	if err != nil {
		return infrastructureError("describe generation-one ECI container groups: %v", err)
	}
	if err := validateGenerationOneLiveContainerGroups(groups, config.RegionID, receipt); err != nil {
		return infrastructureError("validate generation-one ECI container groups: %v", err)
	}
	return nil
}

// validateGenerationOneLiveImageCache 将回执完整镜像集合与 ECI 实时事实规范化后精确绑定。
func validateGenerationOneLiveImageCache(live eci.ImageCache, regionID string, receipt cicontract.GenerationOneProvisionReceipt) error {
	if live.RegionID != regionID || live.RegionID != receipt.RegionID || live.SnapshotID != receipt.ImageCacheSnapshotID || live.Status != receipt.ImageCacheStatus {
		return errors.New("generation-one ECI ImageCache lifecycle does not match receipt")
	}
	if err := eci.ValidateReadyImageCache(live, receipt.ImageCacheID, receipt.ImageCacheName, receipt.Image); err != nil {
		return err
	}
	liveImages := slices.Clone(live.Images)
	receiptImages := slices.Clone(receipt.ImageCacheImages)
	slices.Sort(liveImages)
	slices.Sort(receiptImages)
	if !slices.Equal(liveImages, receiptImages) {
		return errors.New("generation-one ECI ImageCache images do not match receipt")
	}
	return nil
}

// generationOneProvisionContainerGroupIDs 按 receipt 顺序返回每项内容检查的真实 ECI 身份。
func generationOneProvisionContainerGroupIDs(receipt cicontract.GenerationOneProvisionReceipt) []string {
	groupIDs := make([]string, 0, len(receipt.ProvisionChecks))
	for _, observation := range receipt.ProvisionChecks {
		groupIDs = append(groupIDs, observation.ContainerGroupID)
	}
	return groupIDs
}

// validateGenerationOneLiveContainerGroups 要求 ECI 返回集合与六项检查一一对应且全部成功。
func validateGenerationOneLiveContainerGroups(groups []eci.ContainerGroup, regionID string, receipt cicontract.GenerationOneProvisionReceipt) error {
	if len(groups) != len(receipt.ProvisionChecks) {
		return errors.New("generation-one ECI container group response is incomplete")
	}
	expected := generationOneExpectedContainerGroups(receipt)
	for _, group := range groups {
		observation, found := expected[group.ID]
		if !found {
			return fmt.Errorf("generation-one ECI container group %q is unexpected", group.ID)
		}
		if err := validateGenerationOneLiveContainerGroup(group, regionID, receipt, observation); err != nil {
			return err
		}
		delete(expected, group.ID)
	}
	if len(expected) != 0 {
		return errors.New("generation-one ECI container group response is missing a requested group")
	}
	return nil
}

// generationOneExpectedContainerGroups 以真实 ECI group ID 索引每项声明的内容检查。
func generationOneExpectedContainerGroups(receipt cicontract.GenerationOneProvisionReceipt) map[string]cicontract.ProvisionCheckObservation {
	expected := make(map[string]cicontract.ProvisionCheckObservation, len(receipt.ProvisionChecks))
	for _, observation := range receipt.ProvisionChecks {
		expected[observation.ContainerGroupID] = observation
	}
	return expected
}

// validateGenerationOneLiveContainerGroup 绑定单项检查的区域、镜像、标签、终态与实际运行区间。
func validateGenerationOneLiveContainerGroup(group eci.ContainerGroup, regionID string, receipt cicontract.GenerationOneProvisionReceipt, observation cicontract.ProvisionCheckObservation) error {
	if err := validateGenerationOneLiveContainerGroupStatus(group, regionID, receipt.RegionID); err != nil {
		return err
	}
	if err := validateGenerationOneLiveContainerGroupResources(group, observation); err != nil {
		return err
	}
	if err := validateGenerationOneLiveContainerGroupTags(group, receipt, observation); err != nil {
		return err
	}
	container, err := generationOneLiveContainer(group, observation.ContainerName)
	if err != nil {
		return err
	}
	if err := validateGenerationOneLiveContainerExecution(group.ID, receipt.RuntimeImage, container); err != nil {
		return err
	}
	return validateGenerationOneLiveContainerTiming(group.ID, container, observation)
}

// validateGenerationOneLiveContainerGroupResources 将 ECI 控制面真实规格绑定到内容检查观测。
func validateGenerationOneLiveContainerGroupResources(group eci.ContainerGroup, observation cicontract.ProvisionCheckObservation) error {
	if err := cicontract.ValidateNormalResources(group.CPU, group.MemoryGiB); err != nil {
		return fmt.Errorf("generation-one ECI container group %q resources are invalid: %w", group.ID, err)
	}
	if group.CPU != observation.ResourceCPU || group.MemoryGiB != observation.ResourceMemoryGiB {
		return fmt.Errorf("generation-one ECI container group %q resources do not match provision check %q", group.ID, observation.Check)
	}
	return nil
}

// validateGenerationOneLiveContainerGroupStatus 校验控制面 region 和成功终态。
func validateGenerationOneLiveContainerGroupStatus(group eci.ContainerGroup, configRegionID, receiptRegionID string) error {
	if group.RegionID != configRegionID || group.RegionID != receiptRegionID || group.Status != "Succeeded" {
		return fmt.Errorf("generation-one ECI container group %q region or terminal status is invalid", group.ID)
	}
	return nil
}

// validateGenerationOneLiveContainerExecution 校验 immutable image 和零退出终态。
func validateGenerationOneLiveContainerExecution(groupID, runtimeImage string, container eci.ContainerStatus) error {
	if container.Image != runtimeImage || container.CurrentState.State != "Terminated" || container.CurrentState.ExitCode == nil || *container.CurrentState.ExitCode != 0 {
		return fmt.Errorf("generation-one ECI container group %q did not run the immutable image successfully", groupID)
	}
	return nil
}

// validateGenerationOneLiveContainerTiming 按 ECI 秒级终态精度校验回执观测位于实际运行区间内。
func validateGenerationOneLiveContainerTiming(groupID string, container eci.ContainerStatus, observation cicontract.ProvisionCheckObservation) error {
	startedAt := container.CurrentState.StartTime.UTC().UnixMilli()
	completedAtExclusive := container.CurrentState.FinishTime.UTC().Add(time.Second).UnixMilli()
	if container.CurrentState.StartTime.IsZero() || container.CurrentState.FinishTime.IsZero() || completedAtExclusive <= startedAt || observation.StartedAtUnixMS < startedAt || observation.CompletedAtUnixMS >= completedAtExclusive {
		return fmt.Errorf("generation-one ECI container group %q timing does not contain the check observation", groupID)
	}
	return nil
}

// validateGenerationOneLiveContainerGroupTags 读取 ECI 控制面标签并绑定 snapshot、源码、检查和计划。
func validateGenerationOneLiveContainerGroupTags(group eci.ContainerGroup, receipt cicontract.GenerationOneProvisionReceipt, observation cicontract.ProvisionCheckObservation) error {
	tags, err := generationOneLiveContainerGroupTags(group)
	if err != nil {
		return err
	}
	want := map[string]string{
		cicontract.GenerationOneECITagProvider:   cicontract.ExecutionProviderID,
		cicontract.GenerationOneECITagImageCache: receipt.ImageCacheID,
		cicontract.GenerationOneECITagSnapshot:   receipt.ImageCacheSnapshotID,
		cicontract.GenerationOneECITagSourceTree: receipt.MainTree,
		cicontract.GenerationOneECITagCheck:      string(observation.Check),
		cicontract.GenerationOneECITagPlanDigest: observation.PlanDigest,
	}
	for key, value := range want {
		if tags[key] != value {
			return fmt.Errorf("generation-one ECI container group %q tag %q is not bound to receipt", group.ID, key)
		}
	}
	return nil
}

// generationOneLiveContainerGroupTags 将控制面标签转为拒绝重复键的严格映射。
func generationOneLiveContainerGroupTags(group eci.ContainerGroup) (map[string]string, error) {
	tags := make(map[string]string, len(group.Tags))
	for _, tag := range group.Tags {
		if _, duplicate := tags[tag.Key]; duplicate {
			return nil, fmt.Errorf("generation-one ECI container group %q has duplicate tag %q", group.ID, tag.Key)
		}
		tags[tag.Key] = tag.Value
	}
	return tags, nil
}

// generationOneLiveContainer 返回检查声明的唯一 ECI 主容器。
func generationOneLiveContainer(group eci.ContainerGroup, name string) (eci.ContainerStatus, error) {
	var found *eci.ContainerStatus
	for index := range group.Containers {
		if group.Containers[index].Name != name {
			continue
		}
		if found != nil {
			return eci.ContainerStatus{}, fmt.Errorf("generation-one ECI container group %q has duplicate container %q", group.ID, name)
		}
		found = &group.Containers[index]
	}
	if found == nil {
		return eci.ContainerStatus{}, fmt.Errorf("generation-one ECI container group %q is missing container %q", group.ID, name)
	}
	return *found, nil
}

// newRemoteGenerationOneVerifier 构造不具备 Create 权限语义的固定 ECI verifier client。
func newRemoteGenerationOneVerifier(config remoteRunConfig) (*eci.Client, error) {
	return eci.New(eci.Config{
		Binary: config.AliyunCLI, RegionID: config.RegionID, VSwitches: slices.Clone(config.VSwitches),
		SecurityGroupID: config.SecurityGroupID, WorkerRoleName: config.WorkerRoleName,
		Profile: config.CredentialProfile, Deadline: 30 * time.Minute,
		SpotStrategy: eci.SpotStrategyNoSpot,
	})
}

// decodeGenerationOneProvisionState 严格解码 canonical BaselineState 并执行其完整校验。
func decodeGenerationOneProvisionState(receipt cicontract.GenerationOneProvisionReceipt) (remoteci.BaselineState, error) {
	var state remoteci.BaselineState
	if err := gatecontract.DecodeStrictJSON(receipt.StateJSON, &state); err != nil {
		return remoteci.BaselineState{}, err
	}
	canonical, err := json.Marshal(state)
	if err != nil {
		return remoteci.BaselineState{}, err
	}
	if !bytes.Equal(canonical, receipt.StateJSON) {
		return remoteci.BaselineState{}, errors.New("generation-one state_json is not canonical JSON")
	}
	if err := state.Validate(); err != nil {
		return remoteci.BaselineState{}, err
	}
	return state, nil
}

// validateGenerationOneProvisionStateBinding 证明回执字段与 canonical BaselineState 一一绑定。
func validateGenerationOneProvisionStateBinding(receipt cicontract.GenerationOneProvisionReceipt, state remoteci.BaselineState) error {
	if err := validateGenerationOneStateCoreBinding(receipt, state); err != nil {
		return err
	}
	if err := validateGenerationOneStateArtifactBinding(receipt, state); err != nil {
		return err
	}
	return nil
}

// validateGenerationOneStateCoreBinding 校验代次、缓存、平台与策略身份。
func validateGenerationOneStateCoreBinding(receipt cicontract.GenerationOneProvisionReceipt, state remoteci.BaselineState) error {
	if !generationOneStateCloudIdentityMatches(receipt, state) || !generationOneStateSourceIdentityMatches(receipt, state) {
		return errors.New("generation-one receipt core fields do not match BaselineState")
	}
	return nil
}

// generationOneStateCloudIdentityMatches 比较首代 provider、region、cache 与 runtime image。
func generationOneStateCloudIdentityMatches(receipt cicontract.GenerationOneProvisionReceipt, state remoteci.BaselineState) bool {
	return state.Generation == 1 && state.ExecutionProvider == receipt.ExecutionProvider && state.RegionID == receipt.RegionID &&
		state.ImageCacheID == receipt.ImageCacheID && state.ImageCacheSnapshotID == receipt.ImageCacheSnapshotID && state.RuntimeImage == receipt.RuntimeImage
}

// generationOneStateSourceIdentityMatches 比较首代源码、平台、策略和工具链身份。
func generationOneStateSourceIdentityMatches(receipt cicontract.GenerationOneProvisionReceipt, state remoteci.BaselineState) bool {
	return state.MainCommit == receipt.MainCommit && state.MainTree == receipt.MainTree && state.Platform == receipt.Platform &&
		state.PolicyDigest == receipt.PolicyDigest && state.ToolchainDigest == receipt.ToolchainDigest
}

// validateGenerationOneStateArtifactBinding 校验 Gate、seed 与 baseline manifest 摘要。
func validateGenerationOneStateArtifactBinding(receipt cicontract.GenerationOneProvisionReceipt, state remoteci.BaselineState) error {
	if state.GateBinarySHA256 != receipt.GateBinarySHA256 || state.RuntimeSeedSHA256 != receipt.RuntimeSeedSHA256 || state.BaselineManifestDigest != receipt.BaselineManifestDigest {
		return errors.New("generation-one receipt artifact fields do not match BaselineState")
	}
	return nil
}
