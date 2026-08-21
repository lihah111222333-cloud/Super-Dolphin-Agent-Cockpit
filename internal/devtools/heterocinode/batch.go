package heterocinode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

const (
	// BatchRequestSchemaVersion 是本地非权威 batch request 的唯一 wire schema。
	BatchRequestSchemaVersion = "heteroci-node-batch-request/v1"
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
	BatchOutcomeFailed   = "failed"
	batchRequestMaxBytes = 1 << 20
)

// BatchExecutionPolicy 是 exact go test 的受控执行策略。
type BatchExecutionPolicy string

// BatchRequest 是 repository-neutral 的 typed exact go test 批次请求。
// 请求不携带 shell、argv 或 cwd；worker 从当前工作目录解析 module_root。
type BatchRequest struct {
	SchemaVersion string          `json:"schema_version"`
	GoExecutable  string          `json:"go_executable"`
	Workloads     []BatchWorkload `json:"workloads"`
}

// BatchWorkload 是一个按 unit_id 排序的 exact go_test workload。
type BatchWorkload struct {
	UnitID          string               `json:"unit_id"`
	ModuleRoot      string               `json:"module_root"`
	Package         string               `json:"package"`
	Name            string               `json:"name"`
	ExecutionPolicy BatchExecutionPolicy `json:"execution_policy"`
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
	for index, workload := range request.Workloads {
		if err := workload.Validate(); err != nil {
			return fmt.Errorf("workload %d: %w", index, err)
		}
		if index > 0 && request.Workloads[index-1].UnitID >= workload.UnitID {
			return errors.New("workloads must be sorted by unique unit_id")
		}
	}
	return nil
}

// Validate 校验单个 exact go_test workload 的 repository-neutral 形状。
func (workload BatchWorkload) Validate() error {
	if err := validateBatchUnitID(workload.UnitID); err != nil {
		return err
	}
	if err := validateBatchModuleRoot(workload.ModuleRoot); err != nil {
		return err
	}
	if err := validateBatchPackage(workload.Package); err != nil {
		return err
	}
	if err := validateBatchTestName(workload.Name); err != nil {
		return err
	}
	switch workload.ExecutionPolicy {
	case BatchExecutionPolicyNormal, BatchExecutionPolicyRace:
		return nil
	default:
		return errors.New("workload execution_policy is unsupported")
	}
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
	GoExecutable string
	Workloads    []resolvedBatchWorkload
}

// prepareBatchPlan 绑定当前工作目录和 caller 显式提供的 go executable。
func prepareBatchPlan(cwd string, request BatchRequest) (batchExecutionPlan, error) {
	resolved, err := resolveBatchWorkloads(cwd, request.Workloads)
	if err != nil {
		return batchExecutionPlan{}, err
	}
	goExecutable := request.GoExecutable
	if resolvedExecutable, resolveErr := exec.LookPath(request.GoExecutable); resolveErr == nil {
		goExecutable = resolvedExecutable
	}
	return batchExecutionPlan{GoExecutable: goExecutable, Workloads: resolved}, nil
}

// executeBatchWorkloads 无人为并发上限地等待全部 exact workload 终态。
func executeBatchWorkloads(ctx context.Context, plan batchExecutionPlan, output *batchOutput) ([]BatchOutcome, error) {
	outcomes := make([]BatchOutcome, len(plan.Workloads))
	var waitGroup errgroup.Group
	for index, workload := range plan.Workloads {
		waitGroup.Go(func() error {
			outcomes[index] = runBatchWorkload(ctx, plan.GoExecutable, workload, output)
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
	if err := output.writeStdout(append(encoded, '\n')); err != nil {
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
func runBatchWorkload(ctx context.Context, goExecutable string, workload resolvedBatchWorkload, output *batchOutput) BatchOutcome {
	outcome := BatchOutcome{UnitID: workload.UnitID, ExitCode: -1}
	if err := ctx.Err(); err != nil {
		outcome.Status, outcome.ErrorCode = BatchOutcomeFailed, "context_canceled"
		return outcome
	}
	args := batchGoTestArgs(workload.BatchWorkload)
	command := exec.Command(goExecutable, args...)
	command.Dir = workload.Directory
	command.Stdout = output.stdoutWriter()
	command.Stderr = output.stderrWriter()
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
		"-run",
		"^" + workload.Name + "$",
		"-count=1",
		"-timeout=0",
	}
	if workload.ExecutionPolicy == BatchExecutionPolicyRace {
		args = append(args, "-race")
	}
	return args
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

// validateBatchUnitID 校验可排序且不可用于路径逃逸的 unit identity。
func validateBatchUnitID(value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 256 || hasBatchControl(value) || strings.ContainsAny(value, "/\\") {
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

// validateBatchPackage 校验 module-relative exact package 的 slash path。
func validateBatchPackage(value string) error {
	relative, err := validateBatchPackageShape(value)
	if err != nil {
		return err
	}
	return validateBatchPackageComponents(relative)
}

// validateBatchPackageShape 校验 exact package 的顶层 slash path 形状。
func validateBatchPackageShape(value string) (string, error) {
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

func (output *batchOutput) stdoutWriter() io.Writer {
	return batchOutputWriter{output: output, target: &output.stdout}
}

func (output *batchOutput) stderrWriter() io.Writer {
	return batchOutputWriter{output: output, target: &output.stderr}
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

type batchOutputWriter struct {
	output *batchOutput
	target *io.Writer
}

// Write 串行转发子进程输出，避免并发 workload 交错写入观察通道。
func (writer batchOutputWriter) Write(data []byte) (int, error) {
	if err := writer.output.write(writer.target, data); err != nil {
		return 0, err
	}
	return len(data), nil
}
