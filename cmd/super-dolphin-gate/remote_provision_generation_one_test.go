package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"golang.org/x/sync/errgroup"
)

// TestInitializeConfiguredRemoteGenerationOneAllowsEmptySQLite 覆盖首个 normal 请求从空库原子接受 ECI 首代。
func TestInitializeConfiguredRemoteGenerationOneAllowsEmptySQLite(t *testing.T) {
	config, receipt, verifier := generationOneConfiguredFixture(t)
	ledgerPath := filepath.Join(t.TempDir(), "remote-ci.baseline-state.sqlite")
	if err := initializeConfiguredRemoteGenerationOneWithVerifier(t.Context(), config, ledgerPath, verifier); err != nil {
		t.Fatalf("initialize configured generation one: %v", err)
	}
	state, err := loadAcceptedRemoteBaseline(ledgerPath)
	if err != nil {
		t.Fatalf("load accepted generation one: %v", err)
	}
	if state.Generation != 1 || state.ImageCacheSnapshotID != receipt.ImageCacheSnapshotID || state.MainTree != receipt.MainTree {
		t.Fatalf("accepted generation one = %#v", state)
	}
}

func TestInitializeConfiguredRemoteGenerationOneChecksReceiptBeforeECIClient(t *testing.T) {
	err := initializeConfiguredRemoteGenerationOne(remoteRunConfig{}, filepath.Join(t.TempDir(), "remote-ci.baseline-state.sqlite"))
	if err == nil || !strings.Contains(err.Error(), "generation_one_provision") {
		t.Fatalf("missing receipt bootstrap error = %v", err)
	}
}

// TestInitializeConfiguredRemoteGenerationOneFailsBeforeWriting 覆盖缺回执和实时 ECI 漂移不会留下 accepted 行。
func TestInitializeConfiguredRemoteGenerationOneFailsBeforeWriting(t *testing.T) {
	config, _, verifier := generationOneConfiguredFixture(t)
	for name, mutate := range map[string]func(*remoteRunConfig, *generationOneTestVerifier){
		"missing receipt": func(config *remoteRunConfig, _ *generationOneTestVerifier) { config.GenerationOneProvision = nil },
		"live verifier": func(_ *remoteRunConfig, verifier *generationOneTestVerifier) {
			verifier.err = errors.New("ECI unavailable")
		},
	} {
		t.Run(name, func(t *testing.T) {
			currentConfig := config
			currentVerifier := verifier.clone()
			mutate(&currentConfig, currentVerifier)
			ledgerPath := filepath.Join(t.TempDir(), "remote-ci.baseline-state.sqlite")
			if err := initializeConfiguredRemoteGenerationOneWithVerifier(t.Context(), currentConfig, ledgerPath, currentVerifier); err == nil {
				t.Fatal("invalid generation-one bootstrap unexpectedly passed")
			}
			store, err := gatecontract.NewDurationLedgerStore(ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.LoadRemoteBaselineState(); !errors.Is(err, gatecontract.ErrRemoteBaselineStateNotFound) {
				t.Fatalf("failed bootstrap accepted state error = %v", err)
			}
		})
	}
}

// TestInitializeConfiguredRemoteGenerationOneConcurrentIdentity 覆盖同态并发幂等和异态首代拒绝。
func TestInitializeConfiguredRemoteGenerationOneConcurrentIdentity(t *testing.T) {
	config, _, verifier := generationOneConfiguredFixture(t)
	ledgerPath := filepath.Join(t.TempDir(), "remote-ci.baseline-state.sqlite")
	errorsSeen := make(chan error, 8)
	var group errgroup.Group
	for range 8 {
		group.Go(func() error {
			errorsSeen <- initializeConfiguredRemoteGenerationOneWithVerifier(t.Context(), config, ledgerPath, verifier.clone())
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("same generation-one identity did not converge: %v", err)
		}
	}
	conflicting := config
	changed := generationOneReceiptWithRenewedAt(t, *config.GenerationOneProvision, time.Second)
	conflicting.GenerationOneProvision = &changed
	if err := initializeConfiguredRemoteGenerationOneWithVerifier(t.Context(), conflicting, ledgerPath, verifier.clone()); err == nil {
		t.Fatal("different generation-one state unexpectedly replaced accepted singleton")
	}
}

// TestValidateGenerationOneLiveImageCacheBindsExactImages 覆盖实时镜像集合与回执的精确绑定。
func TestValidateGenerationOneLiveImageCacheBindsExactImages(t *testing.T) {
	image := "ghcr.io/example/runtime@sha256:" + strings.Repeat("a", 64)
	extra := "ghcr.io/example/helper@sha256:" + strings.Repeat("b", 64)
	receipt := cicontract.GenerationOneProvisionReceipt{
		ExecutionProvider: cicontract.ExecutionProviderID, RegionID: "cn-shenzhen",
		ImageCacheID: "imc-generation-one", ImageCacheSnapshotID: "snapshot-generation-one",
		ImageCacheName: "generation-one", ImageCacheStatus: "Ready", Image: image,
		ImageCacheImages: []string{extra, image},
	}
	live := eci.ImageCache{
		ID: receipt.ImageCacheID, SnapshotID: receipt.ImageCacheSnapshotID, Name: receipt.ImageCacheName,
		RegionID: "cn-shenzhen", Status: "Ready", Progress: "100%", Images: []string{image, extra},
	}
	if err := validateGenerationOneLiveImageCache(live, "cn-shenzhen", receipt); err != nil {
		t.Fatalf("validate exact live ImageCache images: %v", err)
	}
	for name, mutate := range map[string]func(*eci.ImageCache){
		"missing image": func(cache *eci.ImageCache) { cache.Images = []string{image} },
		"extra image": func(cache *eci.ImageCache) {
			cache.Images = append(cache.Images, "ghcr.io/example/other@sha256:"+strings.Repeat("c", 64))
		},
		"status": func(cache *eci.ImageCache) { cache.Status = "Creating" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := live
			changed.Images = append([]string(nil), live.Images...)
			mutate(&changed)
			if err := validateGenerationOneLiveImageCache(changed, "cn-shenzhen", receipt); err == nil {
				t.Fatalf("live ImageCache %s drift unexpectedly passed", name)
			}
		})
	}
}

// TestVerifyRemoteGenerationOneProvisionECIBindsLiveGroups 覆盖控制面区域、镜像、标签、终态和时间绑定。
func TestVerifyRemoteGenerationOneProvisionECIBindsLiveGroups(t *testing.T) {
	config, receipt, verifier := generationOneLiveECIFixture()
	if err := verifyRemoteGenerationOneProvisionECIWithClient(context.Background(), config, receipt, verifier); err != nil {
		t.Fatalf("verify live generation-one ECI evidence: %v", err)
	}
	for name, mutate := range map[string]func(*generationOneTestVerifier){
		"region": func(client *generationOneTestVerifier) { client.groups[0].RegionID = "cn-hangzhou" },
		"CPU": func(client *generationOneTestVerifier) {
			client.groups[0].CPU = 4
			client.groups[0].MemoryGiB = 8
		},
		"memory": func(client *generationOneTestVerifier) {
			client.groups[0].CPU = 8
			client.groups[0].MemoryGiB = 16
		},
		"image": func(client *generationOneTestVerifier) {
			client.groups[0].Containers[0].Image = "registry.example/other@sha256:" + strings.Repeat("b", 64)
		},
		"tag": func(client *generationOneTestVerifier) { client.groups[0].Tags[0].Value = "docker/v1" },
		"exit": func(client *generationOneTestVerifier) {
			failed := int64(1)
			client.groups[0].Containers[0].CurrentState.ExitCode = &failed
		},
		"timing": func(client *generationOneTestVerifier) {
			client.groups[0].Containers[0].CurrentState.FinishTime = time.UnixMilli(1_010).UTC()
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, currentReceipt, current := generationOneLiveECIFixture()
			mutate(current)
			if err := verifyRemoteGenerationOneProvisionECIWithClient(context.Background(), config, currentReceipt, current); err == nil {
				t.Fatalf("live ECI verifier accepted %s drift", name)
			}
		})
	}
}

func TestValidateRemoteGenerationOneProvisionResourcesBindsNormalClasses(t *testing.T) {
	config, receipt, _ := generationOneConfiguredFixture(t)
	for name, mutate := range map[string]func(*cicontract.ProvisionCheckObservation){
		"calibration class": func(observation *cicontract.ProvisionCheckObservation) {
			observation.ResourceClassID = "calibration"
		},
		"class CPU mismatch": func(observation *cicontract.ProvisionCheckObservation) {
			observation.ResourceClassID = "small"
			observation.ResourceCPU = 4
			observation.ResourceMemoryGiB = 8
		},
	} {
		t.Run(name, func(t *testing.T) {
			current := receipt
			current.ProvisionChecks = append([]cicontract.ProvisionCheckObservation(nil), receipt.ProvisionChecks...)
			mutate(&current.ProvisionChecks[0])
			if err := validateRemoteGenerationOneProvisionResources(config, current); err == nil {
				t.Fatalf("invalid generation-one provision resource %s unexpectedly passed", name)
			}
		})
	}
}

// TestValidateGenerationOneLiveContainerTimingUsesECISecondPrecision 锁定控制面终态秒级截断的唯一容差。
func TestValidateGenerationOneLiveContainerTimingUsesECISecondPrecision(t *testing.T) {
	startedAt := time.Unix(1_000, 0).UTC()
	finishedAt := startedAt.Add(time.Second)
	exitCode := int64(0)
	container := eci.ContainerStatus{CurrentState: eci.ContainerState{
		State: "Terminated", StartTime: startedAt, FinishTime: finishedAt, ExitCode: &exitCode,
	}}
	observation := cicontract.ProvisionCheckObservation{
		StartedAtUnixMS:   startedAt.Add(100 * time.Millisecond).UnixMilli(),
		CompletedAtUnixMS: finishedAt.Add(999 * time.Millisecond).UnixMilli(),
	}
	if err := validateGenerationOneLiveContainerTiming("eci-second-precision", container, observation); err != nil {
		t.Fatalf("second-precision observation rejected: %v", err)
	}
	observation.CompletedAtUnixMS = finishedAt.Add(time.Second).UnixMilli()
	if err := validateGenerationOneLiveContainerTiming("eci-outside", container, observation); err == nil {
		t.Fatal("observation outside the ECI second-precision interval was accepted")
	}
}

type generationOneTestVerifier struct {
	cache  eci.ImageCache
	groups []eci.ContainerGroup
	err    error
}

func (verifier *generationOneTestVerifier) DescribeImageCache(context.Context, string) (eci.ImageCache, error) {
	if verifier.err != nil {
		return eci.ImageCache{}, verifier.err
	}
	return verifier.cache, nil
}

func (verifier *generationOneTestVerifier) DescribeContainerGroups(context.Context, ...string) ([]eci.ContainerGroup, error) {
	if verifier.err != nil {
		return nil, verifier.err
	}
	return verifier.groups, nil
}

func (verifier *generationOneTestVerifier) clone() *generationOneTestVerifier {
	clone := *verifier
	clone.cache.Images = append([]string(nil), verifier.cache.Images...)
	clone.groups = append([]eci.ContainerGroup(nil), verifier.groups...)
	return &clone
}

func generationOneLiveECIFixture() (remoteRunConfig, cicontract.GenerationOneProvisionReceipt, *generationOneTestVerifier) {
	image := "registry.example/runtime@sha256:" + strings.Repeat("a", 64)
	receipt := cicontract.GenerationOneProvisionReceipt{
		ExecutionProvider: cicontract.ExecutionProviderID, RegionID: "cn-shenzhen", ImageCacheID: "imc-generation-one",
		ImageCacheSnapshotID: "snapshot-generation-one", ImageCacheName: "generation-one", ImageCacheStatus: "Ready",
		Image: image, RuntimeImage: image, ImageCacheImages: []string{image}, MainTree: strings.Repeat("c", 40),
	}
	groups := make([]eci.ContainerGroup, 0, len(cicontract.RequiredProvisionChecks()))
	for index, check := range cicontract.RequiredProvisionChecks() {
		startedAt := int64(2_000 + index*1_000)
		planDigest := "sha256:" + strings.Repeat(string(rune('a'+index)), 64)
		groupID := "eci-generation-one-" + string(rune('a'+index))
		resourceClassID, resourceCPU, resourceMemoryGiB := generationOneTestResource(index)
		receipt.ProvisionChecks = append(receipt.ProvisionChecks, cicontract.ProvisionCheckObservation{
			Check: check, ExecutionProvider: cicontract.ExecutionProviderID, RegionID: receipt.RegionID,
			ContainerGroupID: groupID, ContainerName: "provision-check", SourceTree: receipt.MainTree,
			ResourceClassID: resourceClassID, ResourceCPU: resourceCPU, ResourceMemoryGiB: resourceMemoryGiB,
			ProvisionSnapshotID: receipt.ImageCacheSnapshotID, PlanDigest: planDigest,
			StartedAtUnixMS: startedAt + 100, CompletedAtUnixMS: startedAt + 800,
		})
		exitCode := int64(0)
		groups = append(groups, eci.ContainerGroup{
			ID: groupID, RegionID: receipt.RegionID, CPU: resourceCPU, MemoryGiB: resourceMemoryGiB, Status: "Succeeded",
			Tags: generationOneLiveECITags(receipt, check, planDigest),
			Containers: []eci.ContainerStatus{{Name: "provision-check", Image: image, CurrentState: eci.ContainerState{
				State: "Terminated", StartTime: time.UnixMilli(startedAt).UTC(), FinishTime: time.UnixMilli(startedAt + 900).UTC(), ExitCode: &exitCode,
			}}},
		})
	}
	config := remoteRunConfig{RegionID: receipt.RegionID}
	return config, receipt, &generationOneTestVerifier{cache: eci.ImageCache{
		ID: receipt.ImageCacheID, RegionID: receipt.RegionID, SnapshotID: receipt.ImageCacheSnapshotID,
		Name: receipt.ImageCacheName, Status: "Ready", Progress: "100%", Images: []string{image},
	}, groups: groups}
}

func generationOneLiveECITags(receipt cicontract.GenerationOneProvisionReceipt, check cicontract.ProvisionCheck, planDigest string) []eci.ContainerGroupTag {
	return []eci.ContainerGroupTag{
		{Key: cicontract.GenerationOneECITagProvider, Value: cicontract.ExecutionProviderID},
		{Key: cicontract.GenerationOneECITagImageCache, Value: receipt.ImageCacheID},
		{Key: cicontract.GenerationOneECITagSnapshot, Value: receipt.ImageCacheSnapshotID},
		{Key: cicontract.GenerationOneECITagSourceTree, Value: receipt.MainTree},
		{Key: cicontract.GenerationOneECITagCheck, Value: string(check)},
		{Key: cicontract.GenerationOneECITagPlanDigest, Value: planDigest},
	}
}

func generationOneTestResource(index int) (string, float64, float64) {
	resources := [...]struct {
		classID string
		cpu     float64
		memory  float64
	}{
		{classID: "small", cpu: 2, memory: 4},
		{classID: "medium", cpu: 4, memory: 8},
		{classID: "maximum", cpu: 8, memory: 16},
	}
	resource := resources[index%len(resources)]
	return resource.classID, resource.cpu, resource.memory
}

func generationOneConfiguredFixture(t *testing.T) (remoteRunConfig, cicontract.GenerationOneProvisionReceipt, *generationOneTestVerifier) {
	t.Helper()
	configPath := writeRemoteRunConfigFixture(t, validRemoteRunConfigJSON())
	config, err := loadRemoteRunConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	state := generationOneConfiguredState(config.RegionID)
	calibration, err := config.Capacity.ResourcePolicy.ResolveCalibrationClass()
	if err != nil {
		t.Fatal(err)
	}
	receipt, groups := generationOneConfiguredReceipt(t, config, state, calibration.ID, calibration.VCPU, calibration.MemoryGiB)
	config.GenerationOneProvision = &receipt
	verifier := &generationOneTestVerifier{
		cache: eci.ImageCache{ID: receipt.ImageCacheID, RegionID: config.RegionID, SnapshotID: receipt.ImageCacheSnapshotID,
			Name: receipt.ImageCacheName, Status: "Ready", Progress: "100%", Images: []string{receipt.RuntimeImage}},
		groups: groups,
	}
	return config, receipt, verifier
}

func generationOneConfiguredState(regionID string) remoteci.BaselineState {
	image := "registry.example/runtime@" + generationOneTestDigest("generation-one runtime image")
	created := time.Date(2026, 8, 4, 1, 0, 0, 0, time.UTC)
	state := remoteci.BaselineState{
		SchemaVersion: remoteci.BaselineStateSchemaVersion, Generation: 1,
		ExecutionProvider: cicontract.ExecutionProviderID, RegionID: regionID,
		MainCommit: strings.Repeat("b", 40), MainTree: strings.Repeat("c", 40), Platform: cicontract.TargetPlatform,
		PolicyDigest: generationOneTestDigest("policy"), ToolchainDigest: generationOneTestDigest("toolchain"), RuntimeImage: image,
		ImageCacheID: "imc-generation-one", ImageCacheSnapshotID: "snapshot-generation-one", ImageCacheReady: true,
		ImageDigest: remoteRuntimeImageDigest(image), GateBinarySHA256: generationOneTestDigest("gate"),
		RuntimeSeedSHA256: generationOneTestDigest("seed"), BaselineManifestDigest: generationOneTestDigest("manifest"),
		CreatedAt: created, AcceptedAt: created.Add(time.Minute), RenewedAt: created.Add(2 * time.Minute),
	}
	state.OCIProjectCache = &remoteci.BaselineOCIProjectCache{
		Image: image, ContentManifestSHA256: generationOneTestDigest("project cache"), MainTree: state.MainTree,
		ToolchainDigest: state.ToolchainDigest, Platform: state.Platform, CachePath: remoteci.OCIProjectGoBuildCachePath,
	}
	return state
}

func generationOneConfiguredReceipt(t *testing.T, config remoteRunConfig, state remoteci.BaselineState, classID string, cpu, memoryGiB float64) (cicontract.GenerationOneProvisionReceipt, []eci.ContainerGroup) {
	t.Helper()
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	stateDigest := sha256.Sum256(stateJSON)
	receipt := cicontract.GenerationOneProvisionReceipt{
		SchemaVersion: cicontract.GenerationOneProvisionReceiptSchemaVersion, Authority: cicontract.GenerationOneProvisionAuthority,
		ExecutionProvider: cicontract.ExecutionProviderID, RegionID: config.RegionID, Generation: 1,
		StateJSON: stateJSON, StateSHA256: fmt.Sprintf("sha256:%x", stateDigest),
		ImageCacheID: state.ImageCacheID, ImageCacheSnapshotID: state.ImageCacheSnapshotID,
		ImageCacheName: "generation-one", ImageCacheStatus: "Ready", Image: state.RuntimeImage, ImageCacheImages: []string{state.RuntimeImage},
		MainCommit: state.MainCommit, MainTree: state.MainTree, Platform: state.Platform,
		PolicyDigest: state.PolicyDigest, ToolchainDigest: state.ToolchainDigest, RuntimeImage: state.RuntimeImage,
		GateBinarySHA256: state.GateBinarySHA256, RuntimeSeedSHA256: state.RuntimeSeedSHA256,
		BaselineManifestDigest: state.BaselineManifestDigest,
		CalibrationClassID:     classID, CalibrationCPU: cpu, CalibrationMemoryGiB: memoryGiB,
	}
	groups := appendGenerationOneConfiguredChecks(t, config.RegionID, &receipt)
	encoded, _, err := cicontract.EncodeGenerationOneProvisionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err = cicontract.DecodeGenerationOneProvisionReceipt(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return receipt, groups
}

func appendGenerationOneConfiguredChecks(t *testing.T, regionID string, receipt *cicontract.GenerationOneProvisionReceipt) []eci.ContainerGroup {
	t.Helper()
	image := receipt.RuntimeImage
	groups := make([]eci.ContainerGroup, 0, len(cicontract.RequiredProvisionChecks()))
	for index, check := range cicontract.RequiredProvisionChecks() {
		providerStart := int64(2_000 + index*1_000)
		resourceClassID, resourceCPU, resourceMemoryGiB := generationOneTestResource(index)
		observation := cicontract.ProvisionCheckObservation{
			Check: check, ExecutionProvider: cicontract.ExecutionProviderID, RegionID: regionID,
			ContainerGroupID: fmt.Sprintf("eci-generation-one-%d", index), ContainerName: "provision-check",
			ResourceClassID: resourceClassID, ResourceCPU: resourceCPU, ResourceMemoryGiB: resourceMemoryGiB,
			Executed: true, Passed: true, SourceTree: receipt.MainTree, ProvisionSnapshotID: receipt.ImageCacheSnapshotID,
			PlanDigest: generationOneTestDigest(fmt.Sprintf("plan-%d", index)), StartedAtUnixMS: providerStart + 100,
			CompletedAtUnixMS: providerStart + 800, DurationMS: 700, CandidateCompileMS: 400,
			TestBodyNotApplicable: true,
		}
		if check == cicontract.ProvisionCheckDependency {
			observation.CandidateCompileMS = 0
			observation.CandidateCompileNotApplicable = true
		}
		digest, err := cicontract.ProvisionCheckObservationReceiptDigest(observation)
		if err != nil {
			t.Fatal(err)
		}
		observation.ReceiptSHA256 = digest
		receipt.ProvisionChecks = append(receipt.ProvisionChecks, observation)
		exitCode := int64(0)
		groups = append(groups, eci.ContainerGroup{
			ID: observation.ContainerGroupID, RegionID: regionID, CPU: resourceCPU, MemoryGiB: resourceMemoryGiB, Status: "Succeeded",
			Tags: generationOneLiveECITags(*receipt, check, observation.PlanDigest),
			Containers: []eci.ContainerStatus{{Name: observation.ContainerName, Image: image, CurrentState: eci.ContainerState{
				State: "Terminated", StartTime: time.UnixMilli(providerStart).UTC(), FinishTime: time.UnixMilli(providerStart + 900).UTC(), ExitCode: &exitCode,
			}}},
		})
	}
	return groups
}

func generationOneTestDigest(value string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(value)))
}

func generationOneReceiptWithRenewedAt(t *testing.T, receipt cicontract.GenerationOneProvisionReceipt, delta time.Duration) cicontract.GenerationOneProvisionReceipt {
	t.Helper()
	var state remoteci.BaselineState
	if err := json.Unmarshal(receipt.StateJSON, &state); err != nil {
		t.Fatal(err)
	}
	state.RenewedAt = state.RenewedAt.Add(delta)
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	receipt.StateJSON = stateJSON
	receipt.StateSHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256(stateJSON))
	receipt.ReceiptSHA256 = ""
	encoded, _, err := cicontract.EncodeGenerationOneProvisionReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := cicontract.DecodeGenerationOneProvisionReceipt(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}
