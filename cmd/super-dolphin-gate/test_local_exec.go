package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	localLightTestTimeout      = 3 * time.Second
	localLightTestLogTailBytes = 16 << 10
)

var (
	errLocalLightTestFailed      = errors.New("local light test failed")
	errLocalLightTestNeedsRemote = errors.New("local light test requires remote execution")
	errLocalLightTestTimeout     = errors.New("local light test exceeded its resource deadline")
)

type localLightTestExecution struct {
	WorkloadID              gatecontract.GateID `json:"workload_id"`
	Package                 string              `json:"package"`
	Test                    string              `json:"test"`
	CloudObservedDurationMS int64               `json:"cloud_observed_duration_ms"`
	ExitCode                int                 `json:"exit_code"`
	StartedAt               time.Time           `json:"started_at"`
	CompletedAt             time.Time           `json:"completed_at"`
	LogTail                 string              `json:"log_tail,omitempty"`
	LogTruncated            bool                `json:"log_truncated"`
}

// executeLocalLightTests 只顺序执行一个已经过云端耗时过滤的精确测试。
func executeLocalLightTests(
	input remoteci.RunInput,
	selection remoteci.WorkloadCacheProbeResult,
	decisions map[string]remoteci.LocalLightTestDecision,
) (autoTestRunResult, error) {
	startedAt := time.Now().UTC()
	result := autoTestRunResult{
		SchemaVersion: autoTestRunResultSchemaVersion,
		Backend:       autoTestBackendLocalLight,
		SourceTreeSHA: input.Tree,
		Status:        gatecontract.ResultStatusPassed,
		ReusedWorkloads: append(
			[]gatecontract.GateID(nil),
			selection.ReusedWorkloads...,
		),
		StartedAt: startedAt,
	}
	if len(selection.CacheMissWorkloads) != 1 {
		result.Status = gatecontract.ResultStatusFailed
		result.CompletedAt = time.Now().UTC()
		return result, errors.New("local light execution requires exactly one cache-miss workload")
	}
	goBinary, err := resolveLocalLightGoBinary(input.RepositoryRoot)
	if err != nil {
		result.Status = gatecontract.ResultStatusFailed
		result.CompletedAt = time.Now().UTC()
		return result, err
	}
	for _, workload := range selection.CacheMissWorkloads {
		decision, found := decisions[workload.ID]
		if !found || !decision.Eligible {
			result.Status = gatecontract.ResultStatusFailed
			result.CompletedAt = time.Now().UTC()
			return result, errors.New("local light execution requires a filtered eligible workload")
		}
		execution, runErr := executeOneLocalLightTest(
			input.RepositoryRoot,
			goBinary,
			workload,
			decision,
		)
		result.Executions = append(result.Executions, execution)
		if runErr != nil {
			result.Status = gatecontract.ResultStatusFailed
			result.CompletedAt = time.Now().UTC()
			return result, runErr
		}
	}
	result.CompletedAt = time.Now().UTC()
	return result, nil
}

// executeOneLocalLightTest 以固定低资源预算运行一个精确 Go Test。
func executeOneLocalLightTest(
	repositoryRoot string,
	goBinary string,
	workload gatecontract.Workload,
	decision remoteci.LocalLightTestDecision,
) (localLightTestExecution, error) {
	_, kind, target, targeted, err := gatecontract.ParseWorkloadID(workload.ID)
	if err != nil || !targeted || kind != gatecontract.WorkloadTargetGoTest || !decision.Eligible {
		return localLightTestExecution{}, errors.New("local light test workload is not eligible")
	}
	testTarget, err := gatecontract.ParseGoTestTarget(target)
	if err != nil {
		return localLightTestExecution{}, err
	}
	ctx, cancel := gateprivate.WithTimeout(context.Background(), localLightTestTimeout)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		goBinary,
		"test",
		testTarget.Package,
		"-json",
		"-run",
		"^"+testTarget.Name+"$",
		"-count=1",
		"-timeout="+localLightTestTimeout.String(),
	)
	command.Dir = repositoryRoot
	command.Env = localLightTestEnvironment()
	logTail := newLocalTestTail(localLightTestLogTailBytes)
	timingWriter := testtiming.NewEventWriter(logTail)
	command.Stdout, command.Stderr = timingWriter, logTail
	startedAt := time.Now().UTC()
	runErr := command.Run()
	timingErr := timingWriter.Close()
	completedAt := time.Now().UTC()
	execution := localLightTestExecution{
		WorkloadID:              gatecontract.GateID(workload.ID),
		Package:                 testTarget.Package,
		Test:                    testTarget.Name,
		CloudObservedDurationMS: decision.ObservedDurationMS,
		ExitCode:                localCommandExitCode(runErr),
		StartedAt:               startedAt,
		CompletedAt:             completedAt,
		LogTail:                 logTail.String(),
		LogTruncated:            logTail.Truncated(),
	}
	if ctx.Err() != nil {
		return execution, errLocalLightTestTimeout
	}
	return execution, classifyLocalLightTestResult(
		runErr,
		timingErr,
		timingWriter.Timings(),
		testTarget,
	)
}

// classifyLocalLightTestResult 只把明确到达目标的 pass/fail 当成本机测试结论。
func classifyLocalLightTestResult(
	runErr error,
	timingErr error,
	timings []testtiming.Timing,
	target gatecontract.GoTestTarget,
) error {
	status, reachedTarget := localExactTestStatus(timings, target.Name)
	if timingErr != nil || !reachedTarget || status == testtiming.StatusSkip {
		return errors.Join(errLocalLightTestNeedsRemote, timingErr)
	}
	if runErr == nil && status == testtiming.StatusPass {
		return nil
	}
	if status == testtiming.StatusFail {
		return fmt.Errorf("%w: %s#%s", errLocalLightTestFailed, target.Package, target.Name)
	}
	return errors.Join(errLocalLightTestNeedsRemote, runErr)
}

func localExactTestStatus(timings []testtiming.Timing, targetName string) (testtiming.Status, bool) {
	for _, timing := range timings {
		if timing.Name == targetName {
			return timing.Status, true
		}
	}
	return "", false
}

func localSourceMatchesTree(repositoryRoot string, tree string) (bool, error) {
	diff := exec.Command("git", "-C", repositoryRoot, "diff", "--quiet", tree, "--")
	diff.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	if err := diff.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	untracked := exec.Command("git", "-C", repositoryRoot, "ls-files", "--others", "--exclude-standard")
	untracked.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := untracked.Output()
	if err != nil {
		return false, err
	}
	return len(output) == 0, nil
}

// resolveLocalLightGoBinary 解析真实 Go 工具并拒绝再次进入仓库包装器。
func resolveLocalLightGoBinary(repositoryRoot string) (string, error) {
	candidate := os.Getenv("REAL_GO_BIN")
	if candidate == "" {
		var err error
		candidate, err = exec.LookPath("go")
		if err != nil {
			return "", errors.New("local light test requires a real Go binary")
		}
	}
	if !filepath.IsAbs(candidate) {
		return "", errors.New("local light test Go binary must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve local light test Go binary: %w", err)
	}
	wrapper, wrapperErr := filepath.EvalSymlinks(filepath.Join(repositoryRoot, "scripts", "go"))
	if wrapperErr == nil && resolved == wrapper {
		return "", errors.New("local light test refuses the repository Go wrapper")
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return "", errors.New("local light test Go binary is not executable")
	}
	return resolved, nil
}

// localLightTestEnvironment 固定离线、只读且低资源的宿主测试环境。
func localLightTestEnvironment() []string {
	const (
		goEnvKey       = "GOENV="
		goFlagsKey     = "GOFLAGS="
		goMaxProcsKey  = "GOMAXPROCS="
		goMemLimitKey  = "GOMEMLIMIT="
		goProxyKey     = "GOPROXY="
		goSumDBKey     = "GOSUMDB="
		goToolchainKey = "GOTOOLCHAIN="
	)
	environment := make([]string, 0, len(os.Environ())+9)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, goEnvKey) ||
			strings.HasPrefix(value, goFlagsKey) ||
			strings.HasPrefix(value, goMaxProcsKey) ||
			strings.HasPrefix(value, goMemLimitKey) ||
			strings.HasPrefix(value, goProxyKey) ||
			strings.HasPrefix(value, goSumDBKey) ||
			strings.HasPrefix(value, goToolchainKey) {
			continue
		}
		environment = append(environment, value)
	}
	return append(
		environment,
		"GOENV=off",
		"GOFLAGS=-p=1 -mod=readonly",
		"GOMAXPROCS=1",
		"GOMEMLIMIT=768MiB",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"SUPER_DOLPHIN_TEST_BACKEND=local-light",
	)
}

func localCommandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

type localTestTail struct {
	mu        sync.Mutex
	limit     int
	total     int
	data      []byte
	truncated bool
}

func newLocalTestTail(limit int) *localTestTail {
	return &localTestTail{limit: limit}
}

// Write 仅保留本机轻量测试输出的定长尾部。
func (tail *localTestTail) Write(data []byte) (int, error) {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	written := len(data)
	tail.total += written
	if written >= tail.limit {
		tail.data = append(tail.data[:0], data[written-tail.limit:]...)
		tail.truncated = tail.total > tail.limit
		return written, nil
	}
	if len(tail.data)+written > tail.limit {
		drop := len(tail.data) + written - tail.limit
		copy(tail.data, tail.data[drop:])
		tail.data = tail.data[:len(tail.data)-drop]
		tail.truncated = true
	}
	tail.data = append(tail.data, data...)
	return written, nil
}

// String 返回当前保留的纯文本日志尾部。
func (tail *localTestTail) String() string {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	return string(tail.data)
}

// Truncated 报告日志前部是否因长度上限被丢弃。
func (tail *localTestTail) Truncated() bool {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	return tail.truncated
}
