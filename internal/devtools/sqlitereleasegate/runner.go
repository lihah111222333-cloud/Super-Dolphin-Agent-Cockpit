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

var nowFunc = time.Now

// RunOptions configures a SQLite release gate run.
type RunOptions struct {
	RepoRoot     string
	LogDir       string
	Only         []string
	Timeout      time.Duration
	AllowPartial bool
}

// Run executes the selected SQLite release gates and returns a report even when gate validation fails.
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

// WriteReport writes a rendered SQLite release gate report to disk.
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

// runGate executes one SQLite gate and records enough metadata for release reports.
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
		persistGateLog(&result, rawLogPath, log.Bytes())
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
	persistGateLog(&result, rawLogPath, log.Bytes())
	return result
}

func persistGateLog(result *Result, path string, body []byte) {
	if err := writeGateLog(path, body); err != nil {
		result.Status = StatusFail
		if result.ExitCode == 0 {
			result.ExitCode = -1
		}
		result.LogError = err.Error()
	}
}

func writeGateLog(path string, body []byte) error {
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write sqlite release gate raw log %s: %w", path, err)
	}
	return nil
}

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
