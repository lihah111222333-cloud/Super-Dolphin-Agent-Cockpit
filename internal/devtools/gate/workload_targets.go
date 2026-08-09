package gate

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	workloadTargetDelimiter     = "::"
	workloadTargetGoGuard       = "go-guard"
	workloadTargetGoPackage     = "go-package"
	workloadTargetGoTest        = "go-test"
	workloadTargetGoBenchmark   = "go-benchmark"
	workloadTargetVitest        = "vitest-file"
	workloadTargetPlaywright    = "playwright-spec"
	workloadTargetFrontendGuard = "frontend-guard"
)

const (
	GoGuardTargetCanonical         = "canonical"
	GoGuardTargetSource            = "source"
	GoGuardTargetSourceRawGoTest   = "source-raw-go-test"
	GoGuardTargetCopylocksProvider = "copylocks-provider"
	GoGuardTargetCopylocksPlatform = "copylocks-platform"
	GoGuardTargetCopylocksThread   = "copylocks-thread"
	GoGuardTargetAIMaintenanceUnit = "ai-maintenance-unit"
	GoGuardTargetAIMaintenanceGate = "ai-maintenance-gate"

	goGuardTargetNestedModulePrefix = "nested-module:"
)

// WorkloadTargetKind 标识 workload 的生产代码输入边界。
type WorkloadTargetKind string

const (
	WorkloadTargetGoGuard       WorkloadTargetKind = workloadTargetGoGuard
	WorkloadTargetGoPackage     WorkloadTargetKind = workloadTargetGoPackage
	WorkloadTargetGoTest        WorkloadTargetKind = workloadTargetGoTest
	WorkloadTargetGoBenchmark   WorkloadTargetKind = workloadTargetGoBenchmark
	WorkloadTargetVitest        WorkloadTargetKind = workloadTargetVitest
	WorkloadTargetPlaywright    WorkloadTargetKind = workloadTargetPlaywright
	WorkloadTargetFrontendGuard WorkloadTargetKind = workloadTargetFrontendGuard
)

const (
	FrontendChangedSuiteCarrierTarget = "scripts/remote-suite-carriers/changed.test.mjs"
	FrontendFullSuiteCarrierTarget    = "scripts/remote-suite-carriers/full.test.mjs"
	frontendPreflightCarrierPrefix    = "scripts/remote-preflight-carriers/"
	frontendPreflightCarrierSuffix    = ".test.mjs"
)

const (
	FrontendPreflightTargetCriticalGuards         = "critical-guards"
	FrontendPreflightTargetTurnContractVerify     = "turncontract-verify"
	FrontendPreflightTargetTurnContractFieldGuard = "turncontract-field-guard"
	FrontendPreflightTargetCriticalTypecheck      = "critical-typecheck"
	FrontendPreflightTargetContractsVitest        = "contracts-vitest"
	FrontendPreflightTargetRPCAudit               = "rpc-audit"
	FrontendPreflightTargetDependencyContract     = "dependency-contract"
)

// FrontendPreflightTargets 返回 preflight 的固定 allowlist，调用方不得修改规范顺序。
func FrontendPreflightTargets() []string {
	return []string{
		FrontendPreflightTargetCriticalGuards,
		FrontendPreflightTargetTurnContractVerify,
		FrontendPreflightTargetTurnContractFieldGuard,
		FrontendPreflightTargetCriticalTypecheck,
		FrontendPreflightTargetContractsVitest,
		FrontendPreflightTargetRPCAudit,
		FrontendPreflightTargetDependencyContract,
	}
}

// FrontendPreflightCarrierTarget 将新 preflight 原子目标投影到旧 materializer 已支持的 Vitest 文件协议。
func FrontendPreflightCarrierTarget(target string) (string, error) {
	if !slices.Contains(FrontendPreflightTargets(), target) {
		return "", fmt.Errorf("frontend preflight target %q is not in the canonical allowlist", target)
	}
	return frontendPreflightCarrierPrefix + target + frontendPreflightCarrierSuffix, nil
}

// ParseFrontendPreflightCarrierTarget 只识别由规范 allowlist 派生的兼容载体路径。
func ParseFrontendPreflightCarrierTarget(target string) (string, bool) {
	name, ok := strings.CutPrefix(target, frontendPreflightCarrierPrefix)
	if !ok {
		return "", false
	}
	name, ok = strings.CutSuffix(name, frontendPreflightCarrierSuffix)
	if !ok || !slices.Contains(FrontendPreflightTargets(), name) {
		return "", false
	}
	canonical, err := FrontendPreflightCarrierTarget(name)
	return name, err == nil && canonical == target
}

// GoTestTarget 将一个顶层 Go 测试绑定到其精确包。
type GoTestTarget struct {
	Package string `json:"package"`
	Name    string `json:"name"`
}

// WorkloadInventory 是从精确 Git tree 枚举出的可执行测试单元。
type WorkloadInventory struct {
	GoPackages           []string
	NestedGoModules      []string
	GoTests              []GoTestTarget
	GoRaceTests          []GoTestTarget
	GoBenchmarks         []GoTestTarget
	FrontendChangedTests []string
	FrontendFullTests    []string
}

func targetWorkloadID(gateID GateID, targetKind string, target string) (string, error) {
	if err := validateWorkloadTarget(gateID, targetKind, target); err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(target))
	return string(gateID) + workloadTargetDelimiter + targetKind + workloadTargetDelimiter + encoded, nil
}

// parseTargetWorkloadID 严格解析 canonical 或带原子目标的 workload 标识。
func parseTargetWorkloadID(id string) (GateID, string, string, bool, error) {
	parts := strings.Split(id, workloadTargetDelimiter)
	if len(parts) == 1 {
		return GateID(id), "", "", false, nil
	}
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", true, errors.New("target workload id is malformed")
	}
	target, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || base64.RawURLEncoding.EncodeToString(target) != parts[2] {
		return "", "", "", true, errors.New("target workload id payload is invalid")
	}
	gateID := GateID(parts[0])
	if err := validateWorkloadTarget(gateID, parts[1], string(target)); err != nil {
		return "", "", "", true, err
	}
	return gateID, parts[1], string(target), true, nil
}

// ParseWorkloadID 返回 canonical gate 或原子测试目标的稳定结构。
func ParseWorkloadID(id string) (GateID, WorkloadTargetKind, string, bool, error) {
	parent, targetKind, target, targeted, err := parseTargetWorkloadID(id)
	return parent, WorkloadTargetKind(targetKind), target, targeted, err
}

// validateWorkloadTarget 校验 gate、目标类型和目标值的允许组合。
func validateWorkloadTarget(gateID GateID, targetKind string, target string) error {
	switch targetKind {
	case workloadTargetGoGuard:
		return validateGoGuardWorkloadTarget(gateID, target)
	case workloadTargetGoPackage:
		return validateGoPackageWorkloadTarget(gateID, target)
	case workloadTargetGoTest:
		return validateGoTestWorkloadTarget(gateID, target)
	case workloadTargetGoBenchmark:
		return validateGoBenchmarkWorkloadTarget(gateID, target)
	case workloadTargetVitest:
		return validateVitestWorkloadTarget(gateID, target)
	case workloadTargetPlaywright:
		return validatePlaywrightWorkloadTarget(gateID, target)
	case workloadTargetFrontendGuard:
		return validateFrontendPreflightWorkloadTarget(gateID, target)
	default:
		return fmt.Errorf("workload target kind %q is unsupported", targetKind)
	}
}

// validateFrontendPreflightWorkloadTarget 只允许登记过的 preflight 原子 workload。
func validateFrontendPreflightWorkloadTarget(gateID GateID, target string) error {
	if gateID != GateIDFrontendPreflight || !slices.Contains(FrontendPreflightTargets(), target) {
		return fmt.Errorf("gate %q has an invalid frontend preflight workload", gateID)
	}
	return nil
}

// validateGoGuardWorkloadTarget 保留旧 canonical 身份并约束原子守卫的父 gate。
func validateGoGuardWorkloadTarget(gateID GateID, target string) error {
	if target == GoGuardTargetCanonical && isBackendTestGate(gateID) {
		return nil
	}
	if gateID == GateIDAIMaintenanceSelfTest {
		switch target {
		case GoGuardTargetAIMaintenanceUnit, GoGuardTargetAIMaintenanceGate:
			return nil
		default:
			return fmt.Errorf("gate %q has an invalid AI maintenance workload", gateID)
		}
	}
	if gateID != GateIDBackendTestWithGuard || !isSplitGoGuardWorkloadTarget(target) {
		return fmt.Errorf("gate %q has an invalid Go guard workload", gateID)
	}
	return nil
}

// isSplitGoGuardWorkloadTarget 识别可独立计时和缓存的固定守卫。
func isSplitGoGuardWorkloadTarget(target string) bool {
	switch target {
	case GoGuardTargetSource,
		GoGuardTargetSourceRawGoTest,
		GoGuardTargetCopylocksProvider,
		GoGuardTargetCopylocksPlatform,
		GoGuardTargetCopylocksThread:
		return true
	default:
		_, err := ParseNestedGoModuleGuardTarget(target)
		return err == nil
	}
}

// NestedGoModuleGuardTarget 为一个仓库内嵌套 Go 模块生成稳定的守卫目标。
func NestedGoModuleGuardTarget(module string) (string, error) {
	if err := validateNestedGoModulePath(module); err != nil {
		return "", err
	}
	return goGuardTargetNestedModulePrefix + module, nil
}

// ParseNestedGoModuleGuardTarget 严格解析动态嵌套 Go 模块守卫目标。
func ParseNestedGoModuleGuardTarget(target string) (string, error) {
	module, ok := strings.CutPrefix(target, goGuardTargetNestedModulePrefix)
	if !ok {
		return "", errors.New("Go guard target is not a nested module")
	}
	if err := validateNestedGoModulePath(module); err != nil {
		return "", err
	}
	if canonical, err := NestedGoModuleGuardTarget(module); err != nil || canonical != target {
		return "", errors.New("nested Go module guard target is not canonical")
	}
	return module, nil
}

// validateNestedGoModulePath 拒绝根模块、路径穿越和无法作为仓库相对目录的模块路径。
func validateNestedGoModulePath(module string) error {
	if module == "" || module == "." || path.IsAbs(module) || path.Clean(module) != module ||
		strings.ContainsAny(module, "\\\x00\r\n,") || strings.IndexFunc(module, unicode.IsSpace) >= 0 {
		return errors.New("nested Go module path is invalid")
	}
	return nil
}

// validateGoPackageWorkloadTarget 校验整包测试所属 gate 与包路径。
func validateGoPackageWorkloadTarget(gateID GateID, target string) error {
	if !isGoPackageWorkloadGate(gateID) {
		return fmt.Errorf("gate %q does not accept Go package workloads", gateID)
	}
	return validateGoPackageTarget(target)
}

// validateGoTestWorkloadTarget 校验顶层 Go 测试所属 gate 与 canonical payload。
func validateGoTestWorkloadTarget(gateID GateID, target string) error {
	if !isBackendTestGate(gateID) {
		return fmt.Errorf("gate %q does not accept Go test workloads", gateID)
	}
	_, err := ParseGoTestTarget(target)
	return err
}

// validateGoBenchmarkWorkloadTarget 校验 benchmark 只能进入非 race 后端 gate。
func validateGoBenchmarkWorkloadTarget(gateID GateID, target string) error {
	if gateID != GateIDBackendTestWithGuard {
		return fmt.Errorf("gate %q does not accept Go benchmark workloads", gateID)
	}
	_, err := ParseGoBenchmarkTarget(target)
	return err
}

// validateVitestWorkloadTarget 校验前端 gate 与 Vitest 文件目标。
func validateVitestWorkloadTarget(gateID GateID, target string) error {
	if gateID != GateIDFrontendTest && gateID != GateIDFrontendFullTest {
		return fmt.Errorf("gate %q does not accept Vitest workloads", gateID)
	}
	if _, carrier := ParseFrontendPreflightCarrierTarget(target); carrier && gateID != GateIDFrontendTest {
		return fmt.Errorf("gate %q does not accept frontend preflight carriers", gateID)
	}
	if target == FrontendChangedSuiteCarrierTarget && gateID != GateIDFrontendTest ||
		target == FrontendFullSuiteCarrierTarget && gateID != GateIDFrontendFullTest {
		return fmt.Errorf("gate %q does not accept frontend suite carrier %q", gateID, target)
	}
	return validateVitestTarget(target)
}

// validatePlaywrightWorkloadTarget 将远程 E2E 限定为已登记的独立 spec。
func validatePlaywrightWorkloadTarget(gateID GateID, target string) error {
	if gateID != GateIDFrontendE2E || !isPlaywrightE2ESpec(target) {
		return fmt.Errorf("gate %q has an invalid Playwright workload", gateID)
	}
	return nil
}

// ParsePlaywrightE2ETarget 将稳定的 spec#describe 身份拆成文件和 grep 选择器。
func ParsePlaywrightE2ETarget(target string) (string, string, error) {
	spec, grep, ok := strings.Cut(target, "#")
	if !ok || spec == "" || grep == "" {
		return "", "", fmt.Errorf("Playwright E2E target %q must be spec#describe", target)
	}
	switch target {
	case playwrightBusinessReadSurfacesTarget,
		playwrightBusinessChatBridgeTarget,
		playwrightDesktopShellTarget,
		playwrightDesktopBusinessPagesTarget,
		playwrightDesktopReadSettingsTarget:
		return spec, grep, nil
	default:
		return "", "", fmt.Errorf("unsupported Playwright E2E target %q", target)
	}
}

// NewGoTestWorkload 构造绑定精确包、顶层测试和执行语义的原子 workload。
func NewGoTestWorkload(gateID GateID, packageTarget string, testName string, estimateMS int64) (Workload, error) {
	target, err := encodeGoTestTarget(GoTestTarget{Package: packageTarget, Name: testName})
	if err != nil {
		return Workload{}, err
	}
	return selectedTargetWorkload(gateID, workloadTargetGoTest, target, WorkloadKindGoTest, estimateMS)
}

// NewGoBenchmarkWorkload 构造只能交给远程 runner 的精确 benchmark workload。
func NewGoBenchmarkWorkload(gateID GateID, packageTarget string, benchmarkName string, estimateMS int64) (Workload, error) {
	target, err := encodeGoBenchmarkTarget(GoTestTarget{Package: packageTarget, Name: benchmarkName})
	if err != nil {
		return Workload{}, err
	}
	return selectedTargetWorkload(gateID, workloadTargetGoBenchmark, target, WorkloadKindGoTest, estimateMS)
}

// NewGoPackageWorkload 构造一个精确 Go 包 workload。
func NewGoPackageWorkload(gateID GateID, packageTarget string, estimateMS int64) (Workload, error) {
	return selectedTargetWorkload(gateID, workloadTargetGoPackage, packageTarget, WorkloadKindGoTest, estimateMS)
}

func encodeGoTestTarget(target GoTestTarget) (string, error) {
	if err := validateGoPackageTarget(target.Package); err != nil {
		return "", err
	}
	if err := validateGoTestName(target.Name); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return "", fmt.Errorf("encode Go test target: %w", err)
	}
	return string(encoded), nil
}

func encodeGoBenchmarkTarget(target GoTestTarget) (string, error) {
	if err := validateGoPackageTarget(target.Package); err != nil {
		return "", err
	}
	if err := validateGoBenchmarkName(target.Name); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return "", fmt.Errorf("encode Go benchmark target: %w", err)
	}
	return string(encoded), nil
}

// ParseGoTestTarget 严格解析 canonical Go 测试目标。
func ParseGoTestTarget(value string) (GoTestTarget, error) {
	var target GoTestTarget
	if err := decodeStrictJSON(strings.NewReader(value), &target); err != nil {
		return GoTestTarget{}, fmt.Errorf("decode Go test target: %w", err)
	}
	canonical, err := encodeGoTestTarget(target)
	if err != nil {
		return GoTestTarget{}, err
	}
	if canonical != value {
		return GoTestTarget{}, errors.New("Go test target is not canonical")
	}
	return target, nil
}

// ParseGoBenchmarkTarget 严格解析 canonical 的 Go 包与 benchmark 组合。
func ParseGoBenchmarkTarget(value string) (GoTestTarget, error) {
	var target GoTestTarget
	if err := decodeStrictJSON(strings.NewReader(value), &target); err != nil {
		return GoTestTarget{}, fmt.Errorf("decode Go benchmark target: %w", err)
	}
	canonical, err := encodeGoBenchmarkTarget(target)
	if err != nil {
		return GoTestTarget{}, err
	}
	if canonical != value {
		return GoTestTarget{}, errors.New("Go benchmark target is not canonical")
	}
	return target, nil
}

// validateGoTestName 校验 Go 顶层 Test、Fuzz 或 Example 的命名规则。
func validateGoTestName(name string) error {
	if !token.IsIdentifier(name) {
		return errors.New("Go test name is not an identifier")
	}
	for _, prefix := range []string{"Test", "Fuzz"} {
		if suffix, ok := strings.CutPrefix(name, prefix); ok {
			if suffix == "" {
				return nil
			}
			first, _ := utf8.DecodeRuneInString(suffix)
			if unicode.IsLower(first) {
				return errors.New("Go test name has an invalid suffix")
			}
			return nil
		}
	}
	if name == "Example" || strings.HasPrefix(name, "Example") {
		return nil
	}
	return errors.New("Go test name has an unsupported prefix")
}

func validateGoBenchmarkName(name string) error {
	if !token.IsIdentifier(name) || !strings.HasPrefix(name, "Benchmark") {
		return errors.New("Go benchmark name is invalid")
	}
	suffix := strings.TrimPrefix(name, "Benchmark")
	if suffix == "" {
		return nil
	}
	first, _ := utf8.DecodeRuneInString(suffix)
	if unicode.IsLower(first) {
		return errors.New("Go benchmark name has an invalid suffix")
	}
	return nil
}

// isBackendTestGate 报告 gate 是否允许 Go worker 目标。
func isBackendTestGate(gateID GateID) bool {
	return gateID == GateIDBackendTestWithGuard || gateID == GateIDBackendTestGuardWithRace
}

func isExpansionOnlyGate(gateID GateID) bool {
	return gateID == GateIDBackendNilness || gateID == GateIDFrontendPreflight
}

func expansionOnlyWorkloadDigest(gateID GateID) string {
	sum := sha256.Sum256([]byte("super-dolphin-expansion-only:" + string(gateID)))
	return hex.EncodeToString(sum[:])
}

func isGoPackageWorkloadGate(gateID GateID) bool {
	// Expansion-only gates are not all Go package gates: frontend preflight
	// expands into its own frontend-guard allowlist and must reject Go targets.
	return isBackendTestGate(gateID) || gateID == GateIDBackendNilness
}

// validateGoPackageTarget 只校验无命令注入能力的仓库相对 Go 包目标。
func validateGoPackageTarget(target string) error {
	if !isCanonicalGoPackageTarget(target) {
		return errors.New("Go package workload target is invalid")
	}
	return nil
}

// GoPackageTargetForSource 将根模块候选源码映射为精确包目标；模块归属由 tree inventory 判定。
func GoPackageTargetForSource(file string) (string, bool) {
	if file == "" || path.Ext(file) != ".go" || path.IsAbs(file) || path.Clean(file) != file ||
		strings.ContainsAny(file, "\\\x00\r\n,") || strings.IndexFunc(file, unicode.IsSpace) >= 0 ||
		hasIgnoredGoDirectory(file) {
		return "", false
	}
	target := "./" + path.Dir(file)
	if err := validateGoPackageTarget(target); err != nil {
		return "", false
	}
	return target, true
}

// isCanonicalGoPackageTarget 仅检查 Go 包字符串自身的语法与路径规范性。
func isCanonicalGoPackageTarget(target string) bool {
	relative := strings.TrimPrefix(target, "./")
	for component := range strings.SplitSeq(relative, "/") {
		if component == ".." {
			return false
		}
	}
	return target != "" && strings.TrimSpace(target) == target && !strings.ContainsAny(target, "\\\x00\r\n,") &&
		strings.IndexFunc(target, unicode.IsSpace) < 0 && strings.HasPrefix(target, "./") &&
		!strings.Contains(target, "...") && path.Clean(relative) == relative
}

// hasIgnoredGoDirectory 对齐 go list ./... 不遍历的目录边界。
func hasIgnoredGoDirectory(file string) bool {
	directory := path.Dir(file)
	if directory == "." {
		return false
	}
	for component := range strings.SplitSeq(directory, "/") {
		if component == "testdata" || component == "vendor" ||
			strings.HasPrefix(component, ".") || strings.HasPrefix(component, "_") {
			return true
		}
	}
	return false
}

// validateVitestTarget 只允许前端规范根目录下的测试文件目标。
func validateVitestTarget(target string) error {
	if !isCanonicalVitestTarget(target) {
		return errors.New("Vitest workload target is invalid")
	}
	if !strings.HasPrefix(target, "src/") && !strings.HasPrefix(target, "scripts/") {
		return errors.New("Vitest workload target is outside frontend test roots")
	}
	if !strings.Contains(target, ".test.") && !strings.Contains(target, ".spec.") {
		return errors.New("Vitest workload target is not a test file")
	}
	if !slices.Contains([]string{".js", ".jsx", ".mjs", ".ts", ".tsx"}, path.Ext(target)) {
		return errors.New("Vitest workload target extension is unsupported")
	}
	return nil
}

// isCanonicalVitestTarget 仅检查 Vitest 文件字符串的路径规范性。
func isCanonicalVitestTarget(target string) bool {
	return target != "" && strings.TrimSpace(target) == target && !strings.ContainsAny(target, "\\\x00\r\n,") &&
		!strings.HasPrefix(target, "-") && !path.IsAbs(target) && path.Clean(target) == target
}

func isPlaywrightE2ESpec(target string) bool {
	_, _, err := ParsePlaywrightE2ETarget(target)
	return err == nil
}

func workloadParentGateID(id string) (GateID, error) {
	gateID, _, _, _, err := parseTargetWorkloadID(id)
	return gateID, err
}

// WorkloadParentGateID 返回 canonical workload 或原子测试 workload 所属的 GateID。
func WorkloadParentGateID(id string) (GateID, error) {
	return workloadParentGateID(id)
}

// WorkloadExecutionDigest 只绑定实际执行程序；相同测试在不同 CI 场景下得到同一摘要。
func WorkloadExecutionDigest(id string) (string, error) {
	_, program, err := executorProgramForWorkload(GateID(id))
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(program)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
