package sqlitereleasegate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// nowFunc 允许测试固定 release gate 报告中的时间戳。
var nowFunc = time.Now

// RunOptions 描述一次 SQLite release gate 运行所需的仓库、日志和选择范围。
type RunOptions struct {
	RepoRoot     string        // 被验证的仓库根目录。
	LogDir       string        // 每个 gate 原始日志的输出目录。
	Only         []string      // 可选 gate ID 列表，支持逗号分隔。
	Timeout      time.Duration // 单个 gate 的最大运行时间。
	AllowPartial bool          // 是否允许只运行 Only 指定的子集。
}

// Run 顺序执行 SQLite release gate 并返回可写入发布证据的报告。
// 参数校验、日志目录创建和结果校验都 fail-fast；即使结果校验失败，也会返回已收集的报告。
func Run(ctx context.Context, opts RunOptions) (Report, error) {
	repoRoot := strings.TrimSpace(opts.RepoRoot)
	if repoRoot == "" {
		return Report{}, fmt.Errorf("repo root is required")
	}
	logDir := strings.TrimSpace(opts.LogDir)
	if logDir == "" {
		return Report{}, fmt.Errorf("log dir is required")
	}
	if opts.Timeout <= 0 {
		return Report{}, fmt.Errorf("positive timeout is required")
	}
	commitSHA, err := gitCommitSHA(ctx, repoRoot)
	if err != nil {
		return Report{}, err
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return Report{}, fmt.Errorf("create sqlite release gate log dir: %w", err)
	}

	selected, err := selectGates(Definitions(), opts.Only)
	if err != nil {
		return Report{}, err
	}
	reportStarted := nowFunc().UTC()
	results := make([]Result, 0, len(selected))
	for _, gate := range selected {
		result := runGate(ctx, repoRoot, logDir, gate, opts.Timeout)
		results = append(results, result)
	}
	reportEnded := nowFunc().UTC()
	report := Report{
		CommitSHA: commitSHA,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		StartedAt: reportStarted,
		EndedAt:   reportEnded,
		Results:   results,
	}
	if err := ValidateResults(Definitions(), results, opts.AllowPartial); err != nil {
		return report, err
	}
	return report, nil
}

// WriteReport 将 release gate 报告写成 Markdown 文件。
// 报告路径不能为空，父目录不存在时会创建，写入失败直接返回错误给发布流程。
func WriteReport(path string, report Report) error {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return fmt.Errorf("report path is required")
	}
	if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
		return fmt.Errorf("create sqlite release gate report dir: %w", err)
	}
	if err := os.WriteFile(clean, []byte(RenderMarkdown(report)), 0o644); err != nil {
		return fmt.Errorf("write sqlite release gate report: %w", err)
	}
	return nil
}

// runGate 执行单个 gate，并把 stdout/stderr 与失败原因写入独立原始日志。
// 默认结果是 FAIL/-1，只有命令成功退出才会改为 PASS，超时由 CommandContext 负责中止进程。
func runGate(parent context.Context, repoRoot, logDir string, gate Gate, timeout time.Duration) Result {
	started := nowFunc().UTC()
	rawLogPath := filepath.Join(logDir, gate.ID+".log")
	result := Result{
		Gate:       gate,
		Command:    gate.CommandString(),
		CWD:        gate.CWD,
		StartedAt:  started,
		RawLogPath: filepath.ToSlash(rawLogPath),
		ExitCode:   -1,
		Status:     StatusFail,
	}
	var log bytes.Buffer
	if len(gate.Command) == 0 {
		log.WriteString("gate command is empty\n")
		result.EndedAt = nowFunc().UTC()
		writeGateLog(rawLogPath, log.Bytes())
		return result
	}
	ctx, cancel := platformconfig.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, gate.Command[0], gate.Command[1:]...)
	cmd.Dir = filepath.Join(repoRoot, filepath.FromSlash(gate.CWD))
	cmd.Env = append(os.Environ(), "SQLITE_RELEASE_GATE_ID="+gate.ID)
	output, err := cmd.CombinedOutput()
	log.Write(output)
	if ctx.Err() == context.DeadlineExceeded {
		log.WriteString("\nrelease gate timed out after " + timeout.String() + "\n")
	} else if err != nil {
		log.WriteString("\nrelease gate failed: " + err.Error() + "\n")
	}
	result.EndedAt = nowFunc().UTC()
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
	} else if err == nil {
		result.ExitCode = 0
		result.Status = StatusPass
	}
	writeGateLog(rawLogPath, log.Bytes())
	return result
}

// writeGateLog 写入 gate 原始日志；日志是发布证据，写失败时直接 panic 阻断流程。
func writeGateLog(path string, body []byte) {
	if err := os.WriteFile(path, body, 0o644); err != nil {
		panic(fmt.Sprintf("write sqlite release gate raw log %s: %v", path, err))
	}
}

// selectGates 根据 Only 参数选择 gate，并按 gate 编号稳定排序。
// 未指定 Only 时返回完整定义；指定未知 ID 时立即报错，避免生成看似成功的空报告。
func selectGates(gates []Gate, only []string) ([]Gate, error) {
	wanted := map[string]bool{}
	for _, value := range only {
		for _, id := range strings.Split(value, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				wanted[id] = true
			}
		}
	}
	if len(wanted) == 0 {
		return gates, nil
	}
	byID := make(map[string]Gate, len(gates))
	for _, gate := range gates {
		byID[gate.ID] = gate
	}
	ids := make([]string, 0, len(wanted))
	for id := range wanted {
		if _, ok := byID[id]; !ok {
			return nil, fmt.Errorf("unknown sqlite release gate id %s", id)
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return gateSortKey(ids[i]) < gateSortKey(ids[j])
	})
	selected := make([]Gate, 0, len(ids))
	for _, id := range ids {
		selected = append(selected, byID[id])
	}
	return selected, nil
}

// gitCommitSHA 读取被验证仓库的 HEAD SHA，作为报告和日志的可追溯锚点。
func gitCommitSHA(ctx context.Context, repoRoot string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve git commit sha: %w: %s", err, strings.TrimSpace(string(output)))
	}
	sha := strings.TrimSpace(string(output))
	if sha == "" {
		return "", fmt.Errorf("git rev-parse HEAD returned empty sha")
	}
	return sha, nil
}
