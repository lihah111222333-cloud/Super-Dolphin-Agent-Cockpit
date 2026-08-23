package heterocinode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	// BatchRequestSchemaVersion 是本地非权威 batch request 的唯一 wire schema。
	BatchRequestSchemaVersion = "heteroci-node-batch-request/v5"
	// BatchTargetPlatformLinuxAMD64 是 Ubuntu direct-go-test 的目标平台。
	BatchTargetPlatformLinuxAMD64 = "linux/amd64"
	// BatchTargetPlatformWindowsAMD64 是 Wine 交叉编译的目标平台。
	BatchTargetPlatformWindowsAMD64 = "windows/amd64"
	// BatchRuntimeCapabilityUbuntuContainer 是 Linux 容器 direct-go-test 能力。
	BatchRuntimeCapabilityUbuntuContainer = "ubuntu_container"
	// BatchRuntimeCapabilityUbuntuWineContainer 是 Linux 容器内 Windows/Wine 能力。
	BatchRuntimeCapabilityUbuntuWineContainer = "ubuntu_wine_container"
	// BatchResultSchemaVersion 是本地非权威 batch result 的唯一 wire schema。
	BatchResultSchemaVersion = "heteroci-node-batch-result/v1"
	// BatchAuthorityLocalNonAuthoritative 标记结果不能升级为远程权威证据。
	BatchAuthorityLocalNonAuthoritative = "LOCAL_NON_AUTHORITATIVE"
	// BatchStatusNotVerified 标记结果尚未取得远程权威验证。
	BatchStatusNotVerified = "NOT_VERIFIED"
	// BatchExecutionPolicyNormal 执行普通 go test。
	BatchExecutionPolicyNormal BatchExecutionPolicy = "normal"
	// BatchExecutionPolicyRace 执行带 -race 的 go test。
	BatchExecutionPolicyRace BatchExecutionPolicy = "race"
	// BatchOutcomeSuccess 表示一个 exact workload 以零退出码完成。
	BatchOutcomeSuccess = "success"
	// BatchOutcomeFailed 表示一个 exact workload 未以零退出码完成。
	BatchOutcomeFailed        = "failed"
	batchRequestMaxBytes      = 1 << 20
	batchWineCleanupTimeout   = 5 * time.Second
	batchWineCleanupWaitDelay = 1 * time.Second
)

// BatchExecutionPolicy 是 exact go test 的受控执行策略。
type BatchExecutionPolicy string

// BatchRequest 是 repository-neutral 的 typed exact go test 批次请求。
// 请求不携带 shell、argv 或 cwd；worker 从当前工作目录解析 module_root。
type BatchRequest struct {
	SchemaVersion                string          `json:"schema_version"`
	GoExecutable                 string          `json:"go_executable"`
	WineExecutable               string          `json:"wine_executable,omitempty"`
	WineExecutableIdentity       string          `json:"wine_executable_identity,omitempty"`
	WineServerExecutable         string          `json:"wine_server_executable,omitempty"`
	WineServerExecutableIdentity string          `json:"wine_server_executable_identity,omitempty"`
	WinePrefixRoot               string          `json:"wine_prefix_root"`
	Workloads                    []BatchWorkload `json:"workloads"`
}

// BatchWorkload 是一个按 unit_id 排序的 exact go_test workload。
type BatchWorkload struct {
	UnitID            string `json:"unit_id"`
	Kind              string `json:"kind"`
	ModuleRoot        string `json:"module_root"`
	Package           string `json:"package"`
	Name              string `json:"name"`
	TargetPlatform    string `json:"target_platform"`
	RuntimeCapability string `json:"runtime_capability"`
	// CPUMilli 是 caller 从 ExecutionBatch.Units 绑定到本批次 workload 的 CPU 预算。
	CPUMilli int64 `json:"cpu_milli"`
	// MemoryBytes 是 caller 从 ExecutionBatch.Units 绑定到本批次 workload 的内存预算。
	MemoryBytes     int64                `json:"memory_bytes"`
	ExecutionPolicy BatchExecutionPolicy `json:"execution_policy"`
	// CommandDigest 是 caller 绑定的 exact workload command identity。
	CommandDigest string `json:"command_digest"`
	// ExecutionDigest 是 caller 绑定的 exact workload execution identity。
	ExecutionDigest string `json:"execution_digest"`
}

// BatchOutcome 是一个 workload 的稳定、非权威终态观察。
type BatchOutcome struct {
	UnitID    string `json:"unit_id"`
	Status    string `json:"status"`
	ExitCode  int    `json:"exit_code"`
	ErrorCode string `json:"error_code,omitempty"`
}

// BatchResult 是一次 batch 的完整逐 unit 非权威结果集合。
type BatchResult struct {
	SchemaVersion string         `json:"schema_version"`
	Authority     string         `json:"authority"`
	Status        string         `json:"status"`
	Outcome       string         `json:"outcome"`
	Outcomes      []BatchOutcome `json:"outcomes"`
}

// DecodeBatchResult 严格解码 worker stdout 中的 canonical batch result。
func DecodeBatchResult(data []byte) (BatchResult, error) {
	if len(data) == 0 || len(data) > batchRequestMaxBytes {
		return BatchResult{}, errors.New("batch result size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result BatchResult
	if err := decoder.Decode(&result); err != nil {
		return BatchResult{}, fmt.Errorf("decode batch result: %w", err)
	}
	if err := ensureBatchJSONEOF(decoder); err != nil {
		return BatchResult{}, err
	}
	if err := result.Validate(); err != nil {
		return BatchResult{}, err
	}
	canonical, err := json.Marshal(result)
	if err != nil || !bytes.Equal(canonical, data) {
		return BatchResult{}, errors.New("batch result is not canonical")
	}
	return result, nil
}

// Validate 校验 authority/status、整体 outcome 和逐 unit 终态一致。
func (result BatchResult) Validate() error {
	if result.SchemaVersion != BatchResultSchemaVersion || result.Authority != BatchAuthorityLocalNonAuthoritative || result.Status != BatchStatusNotVerified {
		return errors.New("batch result identity is invalid")
	}
	if len(result.Outcomes) == 0 {
		return errors.New("batch result outcomes are empty")
	}
	failed, err := validateBatchOutcomes(result.Outcomes)
	if err != nil {
		return err
	}
	want := BatchOutcomeSuccess
	if failed {
		want = BatchOutcomeFailed
	}
	if result.Outcome != want {
		return errors.New("batch result aggregate outcome drifted")
	}
	return nil
}

// validateBatchOutcomes 校验排序后的逐 unit 终态并报告是否存在失败。
func validateBatchOutcomes(outcomes []BatchOutcome) (bool, error) {
	failed := false
	for index, outcome := range outcomes {
		if err := validateBatchOutcome(outcome); err != nil {
			return false, err
		}
		if index > 0 && outcomes[index-1].UnitID >= outcome.UnitID {
			return false, errors.New("batch result outcomes must be sorted and unique")
		}
		failed = failed || outcome.Status == BatchOutcomeFailed
	}
	return failed, nil
}

// validateBatchOutcome 校验单个 unit 的 status、exit code 与 error code。
func validateBatchOutcome(outcome BatchOutcome) error {
	if err := validateBatchUnitID(outcome.UnitID); err != nil {
		return err
	}
	if outcome.Status == BatchOutcomeSuccess && outcome.ExitCode == 0 && outcome.ErrorCode == "" {
		return nil
	}
	if outcome.Status == BatchOutcomeFailed && (outcome.ExitCode != 0 || outcome.ErrorCode != "") && !hasBatchControl(outcome.ErrorCode) {
		return nil
	}
	return errors.New("batch outcome fields are inconsistent")
}

// EncodeBatchRequest 只编码已经满足排序和 typed workload 约束的 canonical JSON。
func EncodeBatchRequest(request BatchRequest) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode batch request: %w", err)
	}
	if len(encoded) > batchRequestMaxBytes {
		return nil, errors.New("batch request exceeds 1 MiB")
	}
	return encoded, nil
}

// DecodeBatchRequest 严格解码 canonical JSON，拒绝 unknown、null、重复及 trailing 字段。
func DecodeBatchRequest(data []byte) (BatchRequest, error) {
	if len(data) == 0 || len(data) > batchRequestMaxBytes {
		return BatchRequest{}, errors.New("batch request size is invalid")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return BatchRequest{}, fmt.Errorf("decode batch request fields: %w", err)
	}
	prefixRoot, present := fields["wine_prefix_root"]
	if !present || bytes.Equal(bytes.TrimSpace(prefixRoot), []byte("null")) {
		return BatchRequest{}, errors.New("batch request wine_prefix_root is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request BatchRequest
	if err := decoder.Decode(&request); err != nil {
		return BatchRequest{}, fmt.Errorf("decode batch request: %w", err)
	}
	if err := ensureBatchJSONEOF(decoder); err != nil {
		return BatchRequest{}, err
	}
	if err := request.Validate(); err != nil {
		return BatchRequest{}, fmt.Errorf("validate batch request: %w", err)
	}
	canonical, err := EncodeBatchRequest(request)
	if err != nil {
		return BatchRequest{}, err
	}
	if !bytes.Equal(canonical, data) {
		return BatchRequest{}, errors.New("batch request is not canonical")
	}
	return request, nil
}

// Validate 校验请求身份、排序和每个 exact workload 的受控字段。
func (request BatchRequest) Validate() error {
	if request.SchemaVersion != BatchRequestSchemaVersion {
		return fmt.Errorf("schema_version must equal %q", BatchRequestSchemaVersion)
	}
	if err := validateBatchExecutable(request.GoExecutable); err != nil {
		return err
	}
	if len(request.Workloads) == 0 {
		return errors.New("batch workloads must be non-empty")
	}
	targetPlatform := ""
	runtimeCapability := ""
	for index, workload := range request.Workloads {
		if err := workload.Validate(); err != nil {
			return fmt.Errorf("workload %d: %w", index, err)
		}
		if index > 0 && request.Workloads[index-1].UnitID >= workload.UnitID {
			return errors.New("workloads must be sorted by unique unit_id")
		}
		if index == 0 {
			targetPlatform = workload.TargetPlatform
			runtimeCapability = workload.RuntimeCapability
			continue
		}
		if workload.TargetPlatform != targetPlatform || workload.RuntimeCapability != runtimeCapability {
			return errors.New("all batch workloads must use one target_platform and runtime_capability")
		}
	}
	if runtimeCapability == BatchRuntimeCapabilityUbuntuWineContainer {
		if err := validateBatchWineRequest(request); err != nil {
			return err
		}
	} else if request.hasWineConfiguration() || request.WinePrefixRoot != "" {
		return errors.New("Wine executables are only valid for ubuntu_wine_container")
	}
	return nil
}

// Validate 校验单个 exact go_test workload 的 repository-neutral 形状。
func (workload BatchWorkload) Validate() error {
	if err := validateBatchUnitID(workload.UnitID); err != nil {
		return err
	}
	if err := validateBatchTargetRuntime(workload.TargetPlatform, workload.RuntimeCapability); err != nil {
		return err
	}
	if workload.CPUMilli <= 0 || workload.MemoryBytes <= 0 {
		return errors.New("workload resource budget must be positive")
	}
	if err := validateBatchWorkloadDigests(workload); err != nil {
		return err
	}
	if err := validateBatchModuleRoot(workload.ModuleRoot); err != nil {
		return err
	}
	if err := validateBatchPackage(workload.Package); err != nil {
		return err
	}
	switch workload.Kind {
	case "go_test":
		if err := validateBatchTestName(workload.Name); err != nil {
			return err
		}
	case "go_package":
		if workload.Name != "" {
			return errors.New("go_package workload name must be empty")
		}
	default:
		return errors.New("workload kind is unsupported")
	}
	switch workload.ExecutionPolicy {
	case BatchExecutionPolicyNormal, BatchExecutionPolicyRace:
		if workload.RuntimeCapability == BatchRuntimeCapabilityUbuntuWineContainer && workload.ExecutionPolicy == BatchExecutionPolicyRace {
			return errors.New("ubuntu_wine_container does not support race execution")
		}
		return nil
	default:
		return errors.New("workload execution_policy is unsupported")
	}
}

func validateBatchTargetRuntime(targetPlatform, runtimeCapability string) error {
	switch {
	case targetPlatform == BatchTargetPlatformLinuxAMD64 && runtimeCapability == BatchRuntimeCapabilityUbuntuContainer:
		return nil
	case targetPlatform == BatchTargetPlatformWindowsAMD64 && runtimeCapability == BatchRuntimeCapabilityUbuntuWineContainer:
		return nil
	default:
		return errors.New("target_platform and runtime_capability are not a supported pair")
	}
}

func (request BatchRequest) hasWineConfiguration() bool {
	return request.WineExecutable != "" || request.WineExecutableIdentity != "" || request.WineServerExecutable != "" || request.WineServerExecutableIdentity != ""
}

func validateBatchWineRequest(request BatchRequest) error {
	for _, check := range []struct {
		name  string
		value string
	}{
		{name: "wine_executable", value: request.WineExecutable},
		{name: "wine_server_executable", value: request.WineServerExecutable},
	} {
		if err := validateBatchAbsoluteExecutable(check.value); err != nil {
			return fmt.Errorf("%s: %w", check.name, err)
		}
	}
	if err := validateBatchAbsoluteCleanPath(request.WinePrefixRoot); err != nil {
		return fmt.Errorf("wine_prefix_root: %w", err)
	}
	for _, check := range []struct {
		name  string
		value string
	}{
		{name: "wine_executable_identity", value: request.WineExecutableIdentity},
		{name: "wine_server_executable_identity", value: request.WineServerExecutableIdentity},
	} {
		if !validBatchDigest(check.value) {
			return fmt.Errorf("%s must be a canonical sha256 digest", check.name)
		}
	}
	return nil
}

func validateBatchWorkloadDigests(workload BatchWorkload) error {
	if !validBatchDigest(workload.CommandDigest) || !validBatchDigest(workload.ExecutionDigest) {
		return errors.New("workload command_digest and execution_digest must be canonical sha256 digests")
	}
	return nil
}

func validBatchDigest(value string) bool {
	encoded, found := strings.CutPrefix(value, "sha256:")
	if !found || len(encoded) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil && encoded == strings.ToLower(encoded) && value == "sha256:"+encoded
}

// RunBatch 在当前工作目录内并发执行每个 exact go test，并写出一行 canonical result。
func RunBatch(ctx context.Context, request BatchRequest, stdout, stderr io.Writer) (BatchResult, error) {
	if ctx == nil {
		return BatchResult{}, errors.New("batch context is required")
	}
	if stdout == nil || stderr == nil {
		return BatchResult{}, errors.New("batch output writers are required")
	}
	if err := request.Validate(); err != nil {
		return BatchResult{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return BatchResult{}, fmt.Errorf("resolve batch current directory: %w", err)
	}
	plan, err := prepareBatchPlan(cwd, request)
	if err != nil {
		return BatchResult{}, err
	}
	output := &batchOutput{stdout: stdout, stderr: stderr}
	outcomes, err := executeBatchWorkloads(ctx, plan, output)
	if err != nil {
		return BatchResult{}, err
	}
	result := buildBatchResult(outcomes)
	if err := writeBatchResult(output, result); err != nil {
		return result, err
	}
	return result, nil
}

type batchExecutionPlan struct {
	GoExecutable         string
	WineExecutable       string
	WineServerExecutable string
	WinePrefixRoot       string
	Workloads            []resolvedBatchWorkload
}

// prepareBatchPlan 绑定当前工作目录和 caller 显式提供的 go executable。
func prepareBatchPlan(cwd string, request BatchRequest) (batchExecutionPlan, error) {
	if len(request.Workloads) == 0 {
		return batchExecutionPlan{}, errors.New("batch workloads must be non-empty")
	}
	if request.Workloads[0].RuntimeCapability == BatchRuntimeCapabilityUbuntuWineContainer {
		if strings.TrimSpace(os.Getenv("CGO_ENABLED")) == "1" {
			return batchExecutionPlan{}, errors.New("ubuntu_wine_container requires CGO_ENABLED=0")
		}
		if err := validateBatchWineExecutableIdentities(request); err != nil {
			return batchExecutionPlan{}, err
		}
	}
	resolved, err := resolveBatchWorkloads(cwd, request.Workloads)
	if err != nil {
		return batchExecutionPlan{}, err
	}
	goExecutable := request.GoExecutable
	if resolvedExecutable, resolveErr := exec.LookPath(request.GoExecutable); resolveErr == nil {
		goExecutable = resolvedExecutable
	}
	return batchExecutionPlan{
		GoExecutable:         goExecutable,
		WineExecutable:       request.WineExecutable,
		WineServerExecutable: request.WineServerExecutable,
		WinePrefixRoot:       request.WinePrefixRoot,
		Workloads:            resolved,
	}, nil
}

// executeBatchWorkloads 无人为并发上限地等待全部 exact workload 终态。
func executeBatchWorkloads(ctx context.Context, plan batchExecutionPlan, output *batchOutput) ([]BatchOutcome, error) {
	outcomes := make([]BatchOutcome, len(plan.Workloads))
	var waitGroup errgroup.Group
	for index, workload := range plan.Workloads {
		waitGroup.Go(func() error {
			outcomes[index] = runBatchWorkload(ctx, plan, workload, output)
			return nil
		})
	}
	if err := waitGroup.Wait(); err != nil {
		return nil, fmt.Errorf("wait for batch workloads: %w", err)
	}
	return outcomes, nil
}

// buildBatchResult 构造固定 authority/status 并保留按 unit_id 排序的逐项结果。
func buildBatchResult(outcomes []BatchOutcome) BatchResult {
	result := BatchResult{
		SchemaVersion: BatchResultSchemaVersion,
		Authority:     BatchAuthorityLocalNonAuthoritative,
		Status:        BatchStatusNotVerified,
		Outcome:       BatchOutcomeSuccess,
		Outcomes:      outcomes,
	}
	for _, outcome := range outcomes {
		if outcome.Status != BatchOutcomeSuccess {
			result.Outcome = BatchOutcomeFailed
			break
		}
	}
	return result
}

// writeBatchResult 将 canonical batch result 作为 stdout 的单行终态输出。
func writeBatchResult(output *batchOutput, result BatchResult) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode batch result: %w", err)
	}
	if err := output.writeStdout(encoded); err != nil {
		return fmt.Errorf("write batch result: %w", err)
	}
	return nil
}

type resolvedBatchWorkload struct {
	BatchWorkload
	Directory string
}

// resolveBatchWorkloads 将每个 module_root 解析为当前目录内的真实目录。
func resolveBatchWorkloads(cwd string, workloads []BatchWorkload) ([]resolvedBatchWorkload, error) {
	cleanCWD, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return nil, fmt.Errorf("resolve batch working directory: %w", err)
	}
	realCWD, err := filepath.EvalSymlinks(cleanCWD)
	if err != nil {
		return nil, fmt.Errorf("resolve batch working directory identity: %w", err)
	}
	resolved := make([]resolvedBatchWorkload, len(workloads))
	for index, workload := range workloads {
		directory := filepath.Clean(filepath.Join(cleanCWD, filepath.FromSlash(workload.ModuleRoot)))
		if err := ensureBatchWithinRoot(cleanCWD, directory); err != nil {
			return nil, fmt.Errorf("workload %q module_root: %w", workload.UnitID, err)
		}
		realDirectory, err := filepath.EvalSymlinks(directory)
		if err != nil {
			return nil, fmt.Errorf("workload %q module_root is unavailable: %w", workload.UnitID, err)
		}
		if err := ensureBatchWithinRoot(realCWD, realDirectory); err != nil {
			return nil, fmt.Errorf("workload %q module_root escapes current directory: %w", workload.UnitID, err)
		}
		info, err := os.Stat(realDirectory)
		if err != nil || !info.IsDir() {
			if err == nil {
				err = errors.New("module_root is not a directory")
			}
			return nil, fmt.Errorf("workload %q module_root: %w", workload.UnitID, err)
		}
		resolved[index] = resolvedBatchWorkload{BatchWorkload: workload, Directory: realDirectory}
	}
	return resolved, nil
}

func ensureBatchWithinRoot(root, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("path escapes current directory")
	}
	return nil
}

// runBatchWorkload 启动单个 exact go test，并把退出状态投影为稳定 outcome。
func runBatchWorkload(ctx context.Context, plan batchExecutionPlan, workload resolvedBatchWorkload, output *batchOutput) BatchOutcome {
	if workload.RuntimeCapability == BatchRuntimeCapabilityUbuntuWineContainer {
		return runWineBatchWorkload(ctx, plan, workload, output)
	}
	return runDirectBatchWorkload(ctx, plan.GoExecutable, workload, output)
}

func runDirectBatchWorkload(ctx context.Context, goExecutable string, workload resolvedBatchWorkload, output *batchOutput) BatchOutcome {
	outcome := BatchOutcome{UnitID: workload.UnitID, ExitCode: -1}
	if err := ctx.Err(); err != nil {
		outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, "context_canceled"
		return outcome
	}
	args := batchGoTestArgs(workload.BatchWorkload)
	command := exec.Command(goExecutable, args...)
	command.Dir = workload.Directory
	command.Env = batchWorkloadEnvironment()
	command.Stdout = output.eventWriter(workload.UnitID, "go_test")
	command.Stderr = output.eventWriter(workload.UnitID, "go_test")
	guard, err := startBatchProcess(command)
	if err != nil {
		outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, "process_start_failed"
		return outcome
	}
	waitErr, errorCode := waitBatchProcess(ctx, command, guard)
	outcome.ErrorCode = errorCode
	if err := guard.close(); err != nil && outcome.ErrorCode == "" {
		outcome.ErrorCode = "process_close_failed"
	}
	outcome.ExitCode = batchExitCode(command)
	if waitErr == nil && outcome.ExitCode == 0 && outcome.ErrorCode == "" {
		outcome.Status = BatchOutcomeSuccess
		return outcome
	}
	outcome.Status = BatchOutcomeFailed
	return outcome
}

func runWineBatchWorkload(ctx context.Context, plan batchExecutionPlan, workload resolvedBatchWorkload, output *batchOutput) (outcome BatchOutcome) {
	outcome = BatchOutcome{UnitID: workload.UnitID, ExitCode: -1}
	if err := ctx.Err(); err != nil {
		outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, "context_canceled"
		return outcome
	}
	tempRoot, err := os.MkdirTemp(plan.WinePrefixRoot, "heteroci-wine-batch-")
	if err != nil {
		outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, "wine_temp_create_failed"
		return outcome
	}
	if err := os.Chmod(tempRoot, 0o700); err != nil {
		_ = os.RemoveAll(tempRoot)
		outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, "wine_temp_create_failed"
		return outcome
	}
	prefix := filepath.Join(tempRoot, "prefix")
	if err := os.Mkdir(prefix, 0o700); err != nil {
		_ = os.RemoveAll(tempRoot)
		outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, "wine_prefix_create_failed"
		return outcome
	}
	defer func() {
		if cleanupErr := cleanupWineBatch(plan.WineServerExecutable, prefix, tempRoot, output, workload.UnitID); cleanupErr != nil {
			outcome.Status = BatchOutcomeFailed
			outcome.ErrorCode = "wine_cleanup_failed"
		}
	}()

	compiledExecutable := filepath.Join(tempRoot, "batch.test.exe")
	compile := exec.Command(plan.GoExecutable, batchWineCompileArgs(workload.BatchWorkload, compiledExecutable)...)
	compile.Dir = workload.Directory
	compile.Env = batchWineCompileEnvironment()
	compile.Stdout = output.eventWriter(workload.UnitID, "wine_compile")
	compile.Stderr = output.eventWriter(workload.UnitID, "wine_compile")
	guard, err := startBatchProcess(compile)
	if err != nil {
		outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, "wine_compile_start_failed"
		return outcome
	}
	waitErr, errorCode := waitBatchProcess(ctx, compile, guard)
	if closeErr := guard.close(); closeErr != nil && errorCode == "" {
		errorCode = "wine_compile_close_failed"
	}
	if waitErr != nil || batchExitCode(compile) != 0 || errorCode != "" {
		if errorCode == "" {
			errorCode = "wine_compile_failed"
		}
		outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, errorCode
		outcome.ExitCode = batchExitCode(compile)
		return outcome
	}
	if err := ctx.Err(); err != nil {
		outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, "context_canceled"
		return outcome
	}

	wine := exec.Command(plan.WineExecutable, batchWineRunArgs(workload.BatchWorkload, compiledExecutable)...)
	wine.Dir = workload.Directory
	wine.Env = batchWineRunEnvironment(prefix)
	wine.Stdout = output.eventWriter(workload.UnitID, "wine_run")
	wine.Stderr = output.eventWriter(workload.UnitID, "wine_run")
	wineGuard, err := startBatchProcess(wine)
	if err != nil {
		outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, "wine_run_start_failed"
		return outcome
	}
	wineWaitErr, wineErrorCode := waitBatchProcess(ctx, wine, wineGuard)
	if closeErr := wineGuard.close(); closeErr != nil && wineErrorCode == "" {
		wineErrorCode = "wine_run_close_failed"
	}
	outcome.ExitCode = batchExitCode(wine)
	if wineWaitErr == nil && outcome.ExitCode == 0 && wineErrorCode == "" {
		outcome.Status = BatchOutcomeSuccess
		return outcome
	}
	if wineErrorCode == "" {
		wineErrorCode = "wine_run_failed"
	}
	outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, wineErrorCode
	return outcome
}

func cleanupWineBatch(wineServerExecutable, prefix, tempRoot string, output *batchOutput, unitID string) error {
	cleanupContext, cancel := context.WithTimeout(context.Background(), batchWineCleanupTimeout)
	defer cancel()
	cleanup := exec.CommandContext(cleanupContext, wineServerExecutable, "-k")
	cleanup.WaitDelay = batchWineCleanupWaitDelay
	cleanup.Env = batchWineRunEnvironment(prefix)
	cleanup.Stdout = output.eventWriter(unitID, "wineserver_cleanup")
	cleanup.Stderr = output.eventWriter(unitID, "wineserver_cleanup")
	var cleanupErr error
	if err := cleanup.Run(); err != nil {
		cleanupErr = errors.New("wineserver -k failed")
	}
	if err := os.RemoveAll(tempRoot); err != nil {
		cleanupErr = errors.New("Wine temporary root cleanup failed")
	}
	return cleanupErr
}

func batchWineCompileArgs(workload BatchWorkload, outputPath string) []string {
	return []string{"test", "-c", "-o", outputPath, workload.Package, "-p=1", "-count=1", "-timeout=0"}
}

func batchWineRunArgs(workload BatchWorkload, executable string) []string {
	args := []string{executable, "-test.count=1", "-test.timeout=0"}
	if workload.Kind == "go_test" {
		args = append(args, "-test.run=^"+workload.Name+"$")
	}
	return args
}

func batchWineCompileEnvironment() []string {
	return batchEnvironmentWithValues(map[string]string{
		"GOOS":        "windows",
		"GOARCH":      "amd64",
		"CGO_ENABLED": "0",
		"GOMAXPROCS":  "1",
	}, "WINEPREFIX")
}

func batchWineRunEnvironment(prefix string) []string {
	return batchEnvironmentWithValues(map[string]string{
		"WINEPREFIX": prefix,
		"GOMAXPROCS": "1",
	}, "GOOS", "GOARCH", "CGO_ENABLED")
}

func validateBatchWineExecutableIdentities(request BatchRequest) error {
	checks := []struct {
		name     string
		path     string
		identity string
	}{
		{name: "wine executable", path: request.WineExecutable, identity: request.WineExecutableIdentity},
		{name: "wineserver executable", path: request.WineServerExecutable, identity: request.WineServerExecutableIdentity},
	}
	for _, check := range checks {
		digest, err := hashBatchExecutable(check.path)
		if err != nil {
			return fmt.Errorf("%s identity verification failed", check.name)
		}
		if digest != check.identity {
			return fmt.Errorf("%s identity drifted", check.name)
		}
	}
	return nil
}

func hashBatchExecutable(value string) (string, error) {
	info, err := os.Lstat(value)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("executable is not a regular executable file")
	}
	file, err := os.Open(value)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, copyErr := io.Copy(hash, file); copyErr != nil {
		_ = file.Close()
		return "", copyErr
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// waitBatchProcess 在取消时杀死外层进程组或 Job，并等待 Wait 完成。
func waitBatchProcess(ctx context.Context, command *exec.Cmd, guard batchProcessGuard) (error, string) {
	waitChannel := make(chan error, 1)
	var waitGroup errgroup.Group
	waitGroup.Go(func() error {
		waitChannel <- command.Wait()
		return nil
	})
	var waitErr error
	errorCode := ""
	select {
	case waitErr = <-waitChannel:
	case <-ctx.Done():
		if err := guard.stop(command); err != nil && !isBatchProcessGone(err) {
			errorCode = "process_stop_failed"
		}
		waitErr = <-waitChannel
		if errorCode == "" {
			errorCode = "context_canceled"
		}
	}
	if err := waitGroup.Wait(); err != nil {
		return err, "process_wait_failed"
	}
	return waitErr, errorCode
}

func batchGoTestArgs(workload BatchWorkload) []string {
	args := []string{
		"test",
		workload.Package,
		"-p=1",
		"-count=1",
		"-timeout=0",
	}
	if workload.Kind == "go_test" {
		args = append(args, "-run", "^"+workload.Name+"$")
	}
	if workload.ExecutionPolicy == BatchExecutionPolicyRace {
		args = append(args, "-race")
	}
	return args
}

func batchWorkloadEnvironment() []string {
	return batchEnvironmentWithValues(map[string]string{"GOMAXPROCS": "1"})
}

func batchEnvironmentWithValues(values map[string]string, remove ...string) []string {
	keys := make(map[string]struct{}, len(values)+len(remove))
	for key := range values {
		keys[key] = struct{}{}
	}
	for _, key := range remove {
		keys[key] = struct{}{}
	}
	environment := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, allowed := batchEnvironmentAllowlist[key]; !allowed {
			continue
		}
		if _, replace := keys[key]; replace {
			continue
		}
		environment = append(environment, entry)
	}
	sort.Slice(environment, func(left, right int) bool {
		leftKey, _, _ := strings.Cut(environment[left], "=")
		rightKey, _, _ := strings.Cut(environment[right], "=")
		return leftKey < rightKey
	})
	valueKeys := make([]string, 0, len(values))
	for key := range values {
		valueKeys = append(valueKeys, key)
	}
	sort.Strings(valueKeys)
	for _, key := range valueKeys {
		value := values[key]
		environment = append(environment, key+"="+value)
	}
	return environment
}

var batchEnvironmentAllowlist = map[string]struct{}{
	"CGO_ENABLED":                         {},
	"DISPLAY":                             {},
	"GOCACHE":                             {},
	"GOENV":                               {},
	"GOFLAGS":                             {},
	"GOMODCACHE":                          {},
	"GOPROXY":                             {},
	"GOPATH":                              {},
	"GOROOT":                              {},
	"GOSUMDB":                             {},
	"GOTOOLCHAIN":                         {},
	"HOME":                                {},
	"LOGNAME":                             {},
	"PATH":                                {},
	"SUPER_DOLPHIN_HETEROCI_BATCH_HELPER": {},
	"SYSTEMROOT":                          {},
	"TEMP":                                {},
	"TMP":                                 {},
	"TMPDIR":                              {},
	"USER":                                {},
	"WINEDEBUG":                           {},
	"WINDIR":                              {},
	"XAUTHORITY":                          {},
	"XDG_RUNTIME_DIR":                     {},
}

func batchExitCode(command *exec.Cmd) int {
	if command == nil || command.ProcessState == nil {
		return -1
	}
	return command.ProcessState.ExitCode()
}

func validateBatchExecutable(value string) error {
	if value == "" || strings.TrimSpace(value) != value || hasBatchControl(value) || strings.ContainsAny(value, ";|&<>$`'\"\n\r\t") {
		return errors.New("go_executable must be an explicit executable without shell syntax")
	}
	return nil
}

func validateBatchAbsoluteExecutable(value string) error {
	if err := validateBatchExecutable(value); err != nil {
		return err
	}
	return validateBatchAbsoluteCleanPath(value)
}

func validateBatchAbsoluteCleanPath(value string) error {
	if value == "" || strings.TrimSpace(value) != value || hasBatchControl(value) || strings.ContainsAny(value, ";|&<>$`'"+"\n\r\t") {
		return errors.New("path must be an explicit path without shell syntax")
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return errors.New("path must be an absolute clean path")
	}
	return nil
}

// validateBatchUnitID 与上游 ExecutionUnit 一致地校验非空、trimmed、无控制字符身份。
func validateBatchUnitID(value string) error {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || hasBatchControl(value) {
		return errors.New("unit_id is invalid")
	}
	return nil
}

// validateBatchModuleRoot 拒绝绝对、逃逸、平台分隔符和控制字符。
func validateBatchModuleRoot(value string) error {
	if value == "" || value != path.Clean(value) || path.IsAbs(value) || strings.Contains(value, "\\") || hasBatchControl(value) {
		return errors.New("module_root must be a clean repository-relative path")
	}
	for component := range strings.SplitSeq(value, "/") {
		if component == ".." || component == "" {
			return errors.New("module_root contains an invalid path component")
		}
	}
	return nil
}

// validateBatchPackage 校验 module-relative exact package 的 slash path；`.` 代表 module root。
func validateBatchPackage(value string) error {
	relative, err := validateBatchPackageShape(value)
	if err != nil {
		return err
	}
	return validateBatchPackageComponents(relative)
}

// validateBatchPackageShape 校验 exact package 的顶层 slash path 形状。
func validateBatchPackageShape(value string) (string, error) {
	if value == "." {
		return value, nil
	}
	relative := strings.TrimPrefix(value, "./")
	if !strings.HasPrefix(value, "./") || value == "./" || strings.Contains(value, "...") || relative != path.Clean(relative) || path.IsAbs(relative) || strings.Contains(value, "\\") || hasBatchControl(value) {
		return "", errors.New("package must be a clean exact ./ package path")
	}
	return relative, nil
}

func validateBatchPackageComponents(relative string) error {
	for component := range strings.SplitSeq(relative, "/") {
		if component == ".." || component == "" {
			return errors.New("package contains an invalid path component")
		}
	}
	return nil
}

// validateBatchTestName 校验 exact top-level Test、Fuzz 或 Example 名称。
func validateBatchTestName(value string) error {
	if !token.IsIdentifier(value) {
		return errors.New("name must be a Go identifier")
	}
	for _, prefix := range []string{"Test", "Fuzz"} {
		if suffix, ok := strings.CutPrefix(value, prefix); ok {
			if suffix == "" || !token.IsIdentifier(suffix) {
				return nil
			}
			if suffix[0] >= 'a' && suffix[0] <= 'z' {
				return errors.New("name has a lowercase Go test suffix")
			}
			return nil
		}
	}
	if value == "Example" || strings.HasPrefix(value, "Example") {
		return nil
	}
	return errors.New("name must be a top-level Test, Fuzz, or Example")
}

func hasBatchControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func ensureBatchJSONEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("batch request contains trailing JSON")
		}
		return fmt.Errorf("batch request trailing JSON is invalid: %w", err)
	}
	return nil
}

type batchOutput struct {
	mu     sync.Mutex
	stdout io.Writer
	stderr io.Writer
}

const batchEventSchemaVersion = "heteroci-node-batch-event/v1"

type batchEvent struct {
	SchemaVersion string `json:"schema_version"`
	UnitID        string `json:"unit_id"`
	Stage         string `json:"stage"`
	State         string `json:"state"`
	Bytes         int    `json:"bytes,omitempty"`
}

func (output *batchOutput) eventWriter(unitID, stage string) io.Writer {
	return batchEventWriter{output: output, unitID: unitID, stage: stage}
}

func (output *batchOutput) writeEvent(event batchEvent) error {
	event.SchemaVersion = batchEventSchemaVersion
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return output.write(&output.stderr, encoded)
}

func (output *batchOutput) writeStdout(data []byte) error {
	return output.write(&output.stdout, data)
}

func (output *batchOutput) write(target *io.Writer, data []byte) error {
	output.mu.Lock()
	defer output.mu.Unlock()
	for len(data) != 0 {
		written, err := (*target).Write(data)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

type batchEventWriter struct {
	output *batchOutput
	unitID string
	stage  string
}

func (writer batchEventWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if err := writer.output.writeEvent(batchEvent{UnitID: writer.unitID, Stage: writer.stage, State: "output", Bytes: len(data)}); err != nil {
		return 0, err
	}
	return len(data), nil
}
