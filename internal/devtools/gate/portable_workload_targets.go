package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
)

const (
	workloadTargetPortableGoPackage = "portable-go-package"
	workloadTargetPortableGoTest    = "portable-go-test"
)

// PortableGoExecutionPolicy 是 portable Go workload 唯一允许的执行策略。
type PortableGoExecutionPolicy string

const (
	PortableGoExecutionPolicyNormal PortableGoExecutionPolicy = "normal"
	PortableGoExecutionPolicyRace   PortableGoExecutionPolicy = "race"
)

// PortableGoTarget 只描述 target tree 中的 typed Go 执行目标，不接受 argv、shell 或 gate ID。
type PortableGoTarget struct {
	ModuleRoot      string                    `json:"module_root"`
	Package         string                    `json:"package"`
	Name            string                    `json:"name"`
	ExecutionPolicy PortableGoExecutionPolicy `json:"execution_policy"`
}

// NewPortableGoPackageWorkload 从 typed package target 构造 sealed canonical workload。
func NewPortableGoPackageWorkload(target PortableGoTarget, estimateMS int64) (Workload, error) {
	encoded, err := encodePortableGoPackageTarget(target)
	if err != nil {
		return Workload{}, err
	}
	return newPortableGoWorkload(target.ExecutionPolicy, workloadTargetPortableGoPackage, encoded, estimateMS)
}

// NewPortableGoTestWorkload 从 typed exact test target 构造 sealed canonical workload。
func NewPortableGoTestWorkload(target PortableGoTarget, estimateMS int64) (Workload, error) {
	encoded, err := encodePortableGoTestTarget(target)
	if err != nil {
		return Workload{}, err
	}
	return newPortableGoWorkload(target.ExecutionPolicy, workloadTargetPortableGoTest, encoded, estimateMS)
}

func newPortableGoWorkload(policy PortableGoExecutionPolicy, targetKind, target string, estimateMS int64) (Workload, error) {
	parent, err := portableGoParentGate(policy)
	if err != nil {
		return Workload{}, err
	}
	return selectedTargetWorkload(parent, targetKind, target, WorkloadKindGoTest, estimateMS)
}

// ParsePortableGoPackageTarget 严格解析 canonical portable package payload。
func ParsePortableGoPackageTarget(value string) (PortableGoTarget, error) {
	return parsePortableGoTarget(value, false)
}

// ParsePortableGoTestTarget 严格解析 canonical portable exact-test payload。
func ParsePortableGoTestTarget(value string) (PortableGoTarget, error) {
	return parsePortableGoTarget(value, true)
}

func parsePortableGoTarget(value string, exactTest bool) (PortableGoTarget, error) {
	var target PortableGoTarget
	if err := decodeStrictJSON(strings.NewReader(value), &target); err != nil {
		return PortableGoTarget{}, fmt.Errorf("decode portable Go target: %w", err)
	}
	encoded, err := encodePortableGoTarget(target, exactTest)
	if err != nil {
		return PortableGoTarget{}, err
	}
	if encoded != value {
		return PortableGoTarget{}, errors.New("portable Go target is not canonical")
	}
	return target, nil
}

func encodePortableGoPackageTarget(target PortableGoTarget) (string, error) {
	return encodePortableGoTarget(target, false)
}

func encodePortableGoTestTarget(target PortableGoTarget) (string, error) {
	return encodePortableGoTarget(target, true)
}

func encodePortableGoTarget(target PortableGoTarget, exactTest bool) (string, error) {
	if err := validatePortableGoTarget(target, exactTest); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return "", fmt.Errorf("encode portable Go target: %w", err)
	}
	return string(encoded), nil
}

func validatePortableGoTarget(target PortableGoTarget, exactTest bool) error {
	if err := validatePortableModuleRoot(target.ModuleRoot); err != nil {
		return err
	}
	if err := validatePortableGoPackage(target.Package); err != nil {
		return err
	}
	if _, err := portableGoParentGate(target.ExecutionPolicy); err != nil {
		return err
	}
	if exactTest {
		return validateGoTestName(target.Name)
	}
	if target.Name != "" {
		return errors.New("portable Go package target name must be empty")
	}
	return nil
}

func validatePortableModuleRoot(root string) error {
	if root == "." {
		return nil
	}
	if root == "" || path.IsAbs(root) || path.Clean(root) != root || strings.ContainsAny(root, "\\\x00\r\n,") ||
		strings.IndexFunc(root, unicode.IsSpace) >= 0 || strings.HasPrefix(root, "../") {
		return errors.New("portable Go module_root is invalid")
	}
	return nil
}

func validatePortableGoPackage(packageTarget string) error {
	if packageTarget == "." {
		return nil
	}
	return validateGoPackageTarget(packageTarget)
}

func portableGoParentGate(policy PortableGoExecutionPolicy) (GateID, error) {
	switch policy {
	case PortableGoExecutionPolicyNormal:
		return GateIDBackendTestWithGuard, nil
	case PortableGoExecutionPolicyRace:
		return GateIDBackendTestGuardWithRace, nil
	default:
		return "", fmt.Errorf("portable Go execution policy %q is unsupported", policy)
	}
}

func validatePortableGoWorkloadTarget(parent GateID, targetKind, payload string) error {
	exactTest := targetKind == workloadTargetPortableGoTest
	target, err := parsePortableGoTarget(payload, exactTest)
	if err != nil {
		return err
	}
	wantParent, err := portableGoParentGate(target.ExecutionPolicy)
	if err != nil {
		return err
	}
	if parent != wantParent {
		return fmt.Errorf("portable Go target policy does not allow parent gate %q", parent)
	}
	return nil
}

func portableGoExecutorProgram(targetKind, payload string) (ExecutorProgram, error) {
	exactTest := targetKind == workloadTargetPortableGoTest
	target, err := parsePortableGoTarget(payload, exactTest)
	if err != nil {
		return ExecutorProgram{}, err
	}
	argv := []string{"go", "test", target.Package}
	if exactTest {
		argv = append(argv, "-run", "^"+regexp.QuoteMeta(target.Name)+"$")
	}
	argv = append(argv, "-count=1")
	step := normalGoExecutorStep(argv)
	if target.ExecutionPolicy == PortableGoExecutionPolicyRace {
		step = raceGoExecutorStep(argv)
	}
	step.Directory = target.ModuleRoot
	requiredGoMod := "go.mod"
	if target.ModuleRoot != "." {
		requiredGoMod = path.Join(target.ModuleRoot, "go.mod")
	}
	return withGoSeed(requirePaths(ExecutorProgram{
		Strategy: ExecutorStrategyCommands,
		Steps:    []ExecutorStep{step},
	}, requiredGoMod)), nil
}
