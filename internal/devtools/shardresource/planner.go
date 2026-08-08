// Package shardresource selects bounded ECI resources from comparable shard observations.
package shardresource

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const gibibyte = int64(1 << 30)

// Class is one configured ECI CPU and memory tier.
type Class struct {
	ID        string  `json:"id"`
	VCPU      float64 `json:"vcpu"`
	MemoryGiB float64 `json:"memory_gib"`
}

// BootstrapClasses maps a workload kind to its first-run resource tier.
type BootstrapClasses struct {
	Guard    string `json:"guard"`
	NodeTest string `json:"node_test"`
	GoTest   string `json:"go_test"`
}

// Policy is the complete deterministic resource selection contract.
type Policy struct {
	Classes                     []Class          `json:"normal_classes"`
	Bootstrap                   BootstrapClasses `json:"bootstrap"`
	CalibrationResource         Class            `json:"calibration_resource"`
	FastWorkloadMaxDurationMS   int64            `json:"fast_workload_max_duration_ms"`
	MediumWorkloadMaxDurationMS int64            `json:"medium_workload_max_duration_ms"`
	HeadroomPercent             uint8            `json:"headroom_percent"`
	MinSamplesToDownsize        uint8            `json:"min_samples_to_downsize"`
}

// Workload is the resource-relevant subset of one planned gate workload.
type Workload struct {
	ID                  string
	Kind                string
	EstimatedDurationMS int64
	ResourceCPU         float64
	ResourceMemoryGiB   float64
}

// Shard identifies one stable workload grouping.
type Shard struct {
	Identity  string
	Workloads []Workload
}

// Context prevents observations from different runner or toolchain environments from mixing.
type Context struct {
	Runner    string
	Toolchain string
}

// Observation records one shard-level ECI resource measurement.
type Observation struct {
	ShardIdentity    string    `json:"shard_identity"`
	Runner           string    `json:"runner"`
	Toolchain        string    `json:"toolchain"`
	ClassID          string    `json:"class_id"`
	PeakCPUNanoCores int64     `json:"peak_cpu_nanocores"`
	PeakMemoryBytes  int64     `json:"peak_memory_bytes"`
	Succeeded        bool      `json:"succeeded"`
	OOMKilled        bool      `json:"oom_killed"`
	ObservedAt       time.Time `json:"observed_at"`
}

// Validate 拒绝 ECI 抢占调度无法精确表示的资源档位和启动策略。
func (policy Policy) Validate() error {
	if err := validatePolicySettings(policy); err != nil {
		return err
	}
	registered, err := validatePolicyClasses(policy.Classes)
	if err != nil {
		return err
	}
	if err := validateBootstrapClasses(policy.Bootstrap, registered); err != nil {
		return err
	}
	if err := validateCalibrationResource(policy.CalibrationResource); err != nil {
		return err
	}
	if _, exists := registered[policy.CalibrationResource.ID]; exists {
		return errors.New("calibration resource ID must not collide with a normal class")
	}
	return nil
}

// validatePolicySettings 校验资源策略的固定阈值、余量与降档样本数。
func validatePolicySettings(policy Policy) error {
	if len(policy.Classes) == 0 {
		return errors.New("resource policy requires at least one class")
	}
	if policy.HeadroomPercent == 0 || policy.HeadroomPercent > 100 {
		return errors.New("resource policy headroom_percent must be within 1..100")
	}
	if policy.MinSamplesToDownsize == 0 {
		return errors.New("resource policy min_samples_to_downsize must be positive")
	}
	if policy.FastWorkloadMaxDurationMS != cicontract.FastWorkloadResourceDuration.Milliseconds() ||
		policy.MediumWorkloadMaxDurationMS != cicontract.MediumWorkloadResourceDuration.Milliseconds() {
		return errors.New("resource policy duration thresholds must equal the remote CI 5s/70s contract")
	}
	return nil
}

// validatePolicyClasses 校验 normal 资源档位严格为 2C/4GiB、4C/8GiB、8C/16GiB。
func validatePolicyClasses(classes []Class) (map[string]Class, error) {
	if len(classes) != 3 {
		return nil, errors.New("normal resource classes must contain exactly the 2C/4GiB, 4C/8GiB, and 8C/16GiB tiers")
	}
	registered := make(map[string]Class, len(classes))
	wantTiers := [...]Class{
		{VCPU: 2, MemoryGiB: 4},
		{VCPU: 4, MemoryGiB: 8},
		{VCPU: 8, MemoryGiB: 16},
	}
	for index, class := range classes {
		if err := validateClass(class); err != nil {
			return nil, fmt.Errorf("resource class %d: %w", index, err)
		}
		if _, exists := registered[class.ID]; exists {
			return nil, fmt.Errorf("resource class %d duplicates id %q", index, class.ID)
		}
		if class.VCPU != wantTiers[index].VCPU || class.MemoryGiB != wantTiers[index].MemoryGiB {
			return nil, fmt.Errorf("normal resource class %d must be exactly %g vCPU and %g GiB", index, wantTiers[index].VCPU, wantTiers[index].MemoryGiB)
		}
		registered[class.ID] = class
		if index > 0 && classDecreases(class, classes[index-1]) {
			return nil, errors.New("resource classes must be monotonic")
		}
	}
	return registered, nil
}

func classDecreases(class Class, previous Class) bool {
	return class.VCPU < previous.VCPU || class.MemoryGiB < previous.MemoryGiB
}

func validateBootstrapClasses(classes BootstrapClasses, registered map[string]Class) error {
	for _, bootstrap := range []struct {
		kind    string
		classID string
	}{
		{kind: "guard", classID: classes.Guard},
		{kind: "node_test", classID: classes.NodeTest},
		{kind: "go_test", classID: classes.GoTest},
	} {
		class, exists := registered[bootstrap.classID]
		if !exists {
			return fmt.Errorf("resource bootstrap class for %s is not registered", bootstrap.kind)
		}
		if class.VCPU != 2 || class.MemoryGiB != 4 {
			return fmt.Errorf("resource bootstrap class for %s must be exactly 2 vCPU and 4 GiB", bootstrap.kind)
		}
	}
	return nil
}

// ResolveClass 在校验完整策略后返回一个已登记的资源档位。
func (policy Policy) ResolveClass(id string) (Class, error) {
	if err := policy.Validate(); err != nil {
		return Class{}, err
	}
	index := classIndex(policy.Classes, id)
	if index < 0 {
		return Class{}, fmt.Errorf("resource class %q is not registered", id)
	}
	return policy.Classes[index], nil
}

// ResolveCalibrationClass 返回所有校准分片唯一允许使用的独立固定规格。
func (policy Policy) ResolveCalibrationClass() (Class, error) {
	if err := policy.Validate(); err != nil {
		return Class{}, err
	}
	return policy.CalibrationResource, nil
}

// IdentityDigest 绑定 normal 档位、耗时阈值和独立校准规格，阻断跨策略复用旧 PASS。
func (policy Policy) IdentityDigest() (string, error) {
	if err := policy.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Schema string `json:"schema"`
		Policy Policy `json:"policy"`
	}{Schema: "remote-ci-resource-policy/v1", Policy: policy})
	if err != nil {
		return "", fmt.Errorf("encode resource policy identity: %w", err)
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// Select 使用计划中固化的 CPU 档，只在同 CPU 档内按观测调整内存并要求稳定降档样本。
func Select(policy Policy, shard Shard, context Context, observations []Observation) (Class, error) {
	if err := policy.Validate(); err != nil {
		return Class{}, err
	}
	if err := validateSelectionInput(shard, context); err != nil {
		return Class{}, err
	}
	baselineIndex, err := baselineClassIndex(policy, shard.Workloads)
	if err != nil {
		return Class{}, err
	}
	targetCPU := shard.Workloads[0].ResourceCPU
	comparable, err := comparableObservations(policy, shard.Identity, context, observations)
	if err != nil {
		return Class{}, err
	}
	if len(comparable) == 0 {
		return policy.Classes[baselineIndex], nil
	}
	return selectFromComparableObservations(policy, targetCPU, baselineIndex, comparable)
}

func selectFromComparableObservations(policy Policy, targetCPU float64, baselineIndex int, comparable []Observation) (Class, error) {
	sort.SliceStable(comparable, func(left, right int) bool {
		return comparable[left].ObservedAt.Before(comparable[right].ObservedAt)
	})
	latest := comparable[len(comparable)-1]
	if latest.OOMKilled {
		return selectAfterOOM(policy, targetCPU, baselineIndex, comparable)
	}
	successful := successfulObservations(comparable)
	requiredIndex, err := requiredMemoryClassIndex(policy, targetCPU, successful)
	if err != nil {
		return Class{}, err
	}
	requiredIndex = max(requiredIndex, baselineIndex)
	latestIndex := classIndex(policy.Classes, latest.ClassID)
	latestClass := observedClass(policy, latest.ClassID)
	if latestClass.VCPU == targetCPU && latestIndex >= 0 && requiredIndex < latestIndex && len(successful) < int(policy.MinSamplesToDownsize) {
		requiredIndex = latestIndex
	}
	return policy.Classes[requiredIndex], nil
}

func selectAfterOOM(policy Policy, targetCPU float64, baselineIndex int, observations []Observation) (Class, error) {
	latest := observations[len(observations)-1]
	observed := observedClass(policy, latest.ClassID)
	if observed.VCPU != targetCPU {
		return Class{}, fmt.Errorf("OOM observation class %q crosses the workload CPU tier", latest.ClassID)
	}
	observedMemory := observed.MemoryGiB
	requiredIndex, err := requiredMemoryClassIndex(policy, targetCPU, successfulObservations(observations))
	if err != nil {
		return Class{}, err
	}
	candidateIndex := max(requiredIndex, baselineIndex)
	candidate := policy.Classes[candidateIndex]
	if candidate.MemoryGiB > observedMemory {
		return candidate, nil
	}
	return selectNextMemoryClass(policy, targetCPU, candidate.MemoryGiB)
}

func validateClass(class Class) error {
	if !classIDPattern.MatchString(class.ID) {
		return errors.New("id is invalid")
	}
	allowed := map[float64]map[float64]bool{
		2: {4: true},
		4: {8: true},
		8: {16: true},
	}
	if !allowed[class.VCPU][class.MemoryGiB] {
		return errors.New("normal CPU and memory must be exactly 2C/4GiB, 4C/8GiB, or 8C/16GiB")
	}
	return nil
}

func validateCalibrationResource(class Class) error {
	if err := cicontract.ValidateCalibrationResources(class.ID, class.VCPU, class.MemoryGiB); err != nil {
		return err
	}
	if class.VCPU != cicontract.CalibrationResourceCPU || class.MemoryGiB != cicontract.CalibrationResourceMemoryGiB {
		return errors.New("remote CI calibration resource must be exactly 4 vCPU and 8 GiB")
	}
	return nil
}

// validateSelectionInput 校验分片、运行器和工具链身份足以参与资源选择。
func validateSelectionInput(shard Shard, context Context) error {
	if !digestPattern.MatchString(shard.Identity) || !digestPattern.MatchString(context.Runner) ||
		!digestPattern.MatchString(context.Toolchain) || len(shard.Workloads) == 0 {
		return errors.New("resource selection identity, context, and workloads are required")
	}
	return validateSelectionWorkloads(shard.Workloads)
}

// validateSelectionWorkloads 拒绝无身份、无估时或混合多个 CPU 档的分片。
func validateSelectionWorkloads(workloads []Workload) error {
	var shardCPU, shardMemoryGiB float64
	for _, workload := range workloads {
		if strings.TrimSpace(workload.ID) == "" || workload.EstimatedDurationMS <= 0 || workload.ResourceCPU <= 0 || workload.ResourceMemoryGiB <= 0 {
			return errors.New("resource selection workload ID, duration, CPU, and memory are required")
		}
		if shardCPU != 0 && (workload.ResourceCPU != shardCPU || workload.ResourceMemoryGiB != shardMemoryGiB) {
			return errors.New("resource selection shard must not mix workload CPU tiers")
		}
		shardCPU, shardMemoryGiB = workload.ResourceCPU, workload.ResourceMemoryGiB
	}
	return nil
}

func baselineClassIndex(policy Policy, workloads []Workload) (int, error) {
	if len(workloads) == 0 {
		return 0, errors.New("resource selection workloads are required")
	}
	targetCPU := workloads[0].ResourceCPU
	memoryFloor := workloads[0].ResourceMemoryGiB
	for _, workload := range workloads {
		if workload.ResourceCPU != targetCPU || workload.ResourceMemoryGiB != memoryFloor {
			return 0, errors.New("resource selection shard must not mix workload CPU tiers")
		}
		classID, err := bootstrapClassID(policy.Bootstrap, workload.Kind)
		if err != nil {
			return 0, err
		}
		bootstrap, bootstrapIndex := classByID(policy, classID)
		if bootstrapIndex < 0 || bootstrap.VCPU != 2 {
			return 0, fmt.Errorf("resource bootstrap class %q is not a normal class", classID)
		}
		memoryFloor = maxFloat(memoryFloor, bootstrap.MemoryGiB)
	}
	return selectMemoryClass(policy, targetCPU, memoryFloor)
}

func classByID(policy Policy, id string) (Class, int) {
	index := classIndex(policy.Classes, id)
	if index < 0 {
		return Class{}, -1
	}
	return policy.Classes[index], index
}

func selectMemoryClass(policy Policy, targetCPU, minimumMemoryGiB float64) (int, error) {
	for index, class := range policy.Classes {
		if class.VCPU == targetCPU && class.MemoryGiB >= minimumMemoryGiB {
			return index, nil
		}
	}
	return 0, fmt.Errorf("resource policy has no %g vCPU class with at least %g GiB memory", targetCPU, minimumMemoryGiB)
}

func selectNextMemoryClass(policy Policy, targetCPU, currentMemoryGiB float64) (Class, error) {
	for _, class := range policy.Classes {
		if class.VCPU == targetCPU && class.MemoryGiB > currentMemoryGiB {
			return class, nil
		}
	}
	return Class{}, fmt.Errorf("resource policy has no larger memory class within %g vCPU after %g GiB", targetCPU, currentMemoryGiB)
}

func maxFloat(left, right float64) float64 {
	if right > left {
		return right
	}
	return left
}

func bootstrapClassID(classes BootstrapClasses, kind string) (string, error) {
	switch kind {
	case "guard":
		return classes.Guard, nil
	case "node_test":
		return classes.NodeTest, nil
	case "go_test":
		return classes.GoTest, nil
	default:
		return "", fmt.Errorf("resource bootstrap workload kind %q is unsupported", kind)
	}
}

// comparableObservations 过滤与当前分片、运行器及工具链完全一致的有效观测。
func comparableObservations(policy Policy, identity string, context Context, observations []Observation) ([]Observation, error) {
	comparable := make([]Observation, 0, len(observations))
	for index, observation := range observations {
		if err := validateObservation(policy, observation); err != nil {
			return nil, fmt.Errorf("resource observation %d: %w", index, err)
		}
		if observation.ShardIdentity == identity && observation.Runner == context.Runner &&
			observation.Toolchain == context.Toolchain {
			comparable = append(comparable, observation)
		}
	}
	return comparable, nil
}

func validateObservation(policy Policy, observation Observation) error {
	if !validObservationIdentity(policy, observation) {
		return errors.New("identity, class, and UTC observation time are required")
	}
	return validateObservationOutcome(observation)
}

// validObservationIdentity 判断观测身份、资源档位和 UTC 时间是否完整有效。
func validObservationIdentity(policy Policy, observation Observation) bool {
	return digestPattern.MatchString(observation.ShardIdentity) &&
		digestPattern.MatchString(observation.Runner) &&
		digestPattern.MatchString(observation.Toolchain) &&
		knownObservationClass(policy, observation.ClassID) &&
		!observation.ObservedAt.IsZero() &&
		observation.ObservedAt.Location() == time.UTC
}

// validateObservationOutcome 校验 OOM 与成功资源采样之间的互斥结果合同。
func validateObservationOutcome(observation Observation) error {
	if observation.OOMKilled {
		if observation.Succeeded {
			return errors.New("OOM observation cannot be successful")
		}
		return nil
	}
	if !observation.Succeeded || observation.PeakCPUNanoCores <= 0 || observation.PeakMemoryBytes <= 0 {
		return errors.New("non-OOM observation requires successful positive resource metrics")
	}
	return nil
}

func successfulObservations(observations []Observation) []Observation {
	return slices.DeleteFunc(slices.Clone(observations), func(observation Observation) bool {
		return !observation.Succeeded
	})
}

func requiredMemoryClassIndex(policy Policy, targetCPU float64, observations []Observation) (int, error) {
	var peakMemory int64
	for _, observation := range observations {
		peakMemory = max(peakMemory, observation.PeakMemoryBytes)
	}
	requiredMemory := withHeadroom(peakMemory, policy.HeadroomPercent)
	return selectMemoryClass(policy, targetCPU, float64(requiredMemory)/float64(gibibyte))
}

func knownObservationClass(policy Policy, id string) bool {
	return classIndex(policy.Classes, id) >= 0 || policy.CalibrationResource.ID == id
}

func observedClass(policy Policy, id string) Class {
	if id == policy.CalibrationResource.ID {
		return policy.CalibrationResource
	}
	return policy.Classes[classIndex(policy.Classes, id)]
}

func withHeadroom(value int64, percent uint8) int64 {
	return value + value*int64(percent)/100
}

func classIndex(classes []Class, id string) int {
	for index, class := range classes {
		if class.ID == id {
			return index
		}
	}
	return -1
}

var (
	classIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	digestPattern  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)
