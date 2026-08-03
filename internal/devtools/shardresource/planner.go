// Package shardresource selects bounded ECI resources from comparable shard observations.
package shardresource

import (
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
	Classes              []Class          `json:"classes"`
	Bootstrap            BootstrapClasses `json:"bootstrap"`
	CalibrationClass     string           `json:"calibration_class"`
	HeadroomPercent      uint8            `json:"headroom_percent"`
	MinSamplesToDownsize uint8            `json:"min_samples_to_downsize"`
}

// Workload is the resource-relevant subset of one planned gate workload.
type Workload struct {
	ID   string
	Kind string
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
	return nil
}

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
	return nil
}

// validatePolicyClasses 校验资源档位唯一、合法并按容量单调递增。
func validatePolicyClasses(classes []Class) (map[string]struct{}, error) {
	registered := make(map[string]struct{}, len(classes))
	for index, class := range classes {
		if err := validateClass(class); err != nil {
			return nil, fmt.Errorf("resource class %d: %w", index, err)
		}
		if _, exists := registered[class.ID]; exists {
			return nil, fmt.Errorf("resource class %d duplicates id %q", index, class.ID)
		}
		registered[class.ID] = struct{}{}
		if index > 0 && classDecreases(class, classes[index-1]) {
			return nil, errors.New("resource classes must be monotonic")
		}
	}
	return registered, nil
}

func classDecreases(class Class, previous Class) bool {
	return class.VCPU < previous.VCPU || class.MemoryGiB < previous.MemoryGiB
}

func validateBootstrapClasses(classes BootstrapClasses, registered map[string]struct{}) error {
	for _, bootstrap := range []struct {
		kind    string
		classID string
	}{
		{kind: "guard", classID: classes.Guard},
		{kind: "node_test", classID: classes.NodeTest},
		{kind: "go_test", classID: classes.GoTest},
	} {
		if _, exists := registered[bootstrap.classID]; !exists {
			return fmt.Errorf("resource bootstrap class for %s is not registered", bootstrap.kind)
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

// ResolveCalibrationClass returns the one explicit class used by every calibration shard.
func (policy Policy) ResolveCalibrationClass() (Class, error) {
	if err := policy.Validate(); err != nil {
		return Class{}, err
	}
	if policy.CalibrationClass == "" {
		return Class{}, errors.New("resource calibration_class is required")
	}
	class, err := policy.ResolveClass(policy.CalibrationClass)
	if err != nil {
		return Class{}, err
	}
	if err := cicontract.ValidateCalibrationResources(class.ID, class.VCPU, class.MemoryGiB); err != nil {
		return Class{}, err
	}
	return class, nil
}

// Select 选择满足观测峰值的最小安全档位，并在降档前要求稳定样本。
func Select(policy Policy, shard Shard, context Context, observations []Observation) (Class, error) {
	if err := policy.Validate(); err != nil {
		return Class{}, err
	}
	if err := validateSelectionInput(shard, context); err != nil {
		return Class{}, err
	}
	bootstrapIndex, err := bootstrapClassIndex(policy, shard.Workloads)
	if err != nil {
		return Class{}, err
	}
	comparable, err := comparableObservations(policy, shard.Identity, context, observations)
	if err != nil {
		return Class{}, err
	}
	if len(comparable) == 0 {
		return policy.Classes[bootstrapIndex], nil
	}
	sort.SliceStable(comparable, func(left, right int) bool {
		return comparable[left].ObservedAt.Before(comparable[right].ObservedAt)
	})
	latest := comparable[len(comparable)-1]
	latestIndex := classIndex(policy.Classes, latest.ClassID)
	if latest.OOMKilled {
		return policy.Classes[min(latestIndex+1, len(policy.Classes)-1)], nil
	}
	requiredIndex := requiredClassIndex(policy, successfulObservations(comparable))
	if requiredIndex < latestIndex && len(successfulObservations(comparable)) < int(policy.MinSamplesToDownsize) {
		requiredIndex = latestIndex
	}
	return policy.Classes[requiredIndex], nil
}

func validateClass(class Class) error {
	if !classIDPattern.MatchString(class.ID) {
		return errors.New("id is invalid")
	}
	allowed := map[float64]map[float64]bool{
		2: {2: true, 4: true, 8: true, 16: true},
		4: {4: true, 8: true, 16: true, 32: true},
		8: {8: true, 16: true, 32: true},
	}
	if class.VCPU > 8 || class.MemoryGiB > 32 || !allowed[class.VCPU][class.MemoryGiB] {
		return errors.New("CPU and memory must be an exact ECI spot class within 8 vCPU and 32 GiB")
	}
	return nil
}

// validateSelectionInput 校验分片、运行器和工具链身份足以参与资源选择。
func validateSelectionInput(shard Shard, context Context) error {
	if !digestPattern.MatchString(shard.Identity) || !digestPattern.MatchString(context.Runner) ||
		!digestPattern.MatchString(context.Toolchain) || len(shard.Workloads) == 0 {
		return errors.New("resource selection identity, context, and workloads are required")
	}
	for _, workload := range shard.Workloads {
		if strings.TrimSpace(workload.ID) == "" {
			return errors.New("resource selection workload ID is required")
		}
	}
	return nil
}

func bootstrapClassIndex(policy Policy, workloads []Workload) (int, error) {
	selected := 0
	for _, workload := range workloads {
		classID, err := bootstrapClassID(policy.Bootstrap, workload.Kind)
		if err != nil {
			return 0, err
		}
		selected = max(selected, classIndex(policy.Classes, classID))
	}
	return selected, nil
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
		classIndex(policy.Classes, observation.ClassID) >= 0 &&
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

func requiredClassIndex(policy Policy, observations []Observation) int {
	var peakCPU, peakMemory int64
	for _, observation := range observations {
		peakCPU = max(peakCPU, observation.PeakCPUNanoCores)
		peakMemory = max(peakMemory, observation.PeakMemoryBytes)
	}
	requiredCPU := withHeadroom(peakCPU, policy.HeadroomPercent)
	requiredMemory := withHeadroom(peakMemory, policy.HeadroomPercent)
	for index, class := range policy.Classes {
		if int64(class.VCPU*1_000_000_000) >= requiredCPU && int64(class.MemoryGiB)*gibibyte >= requiredMemory {
			return index
		}
	}
	return len(policy.Classes) - 1
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
