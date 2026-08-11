package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/build"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const (
	protocolVersion               = "2024-11-05"
	buildTagTargetRegistryVersion = "lsp-build-tags/v2"
	diagnosticsShardFileThreshold = 512
	diagnosticsAttempts           = 2
	diagnosticsRetryDelay         = 5 * time.Second
)

type retryableDiagnosticsError struct{ err error }

// Error 返回底层可重试诊断错误。
func (e *retryableDiagnosticsError) Error() string { return e.err.Error() }

// Unwrap 保留底层错误链。
func (e *retryableDiagnosticsError) Unwrap() error { return e.err }

type stringFlags []string

// String 返回重复字符串参数的稳定展示形式。
func (f *stringFlags) String() string { return strings.Join(*f, ",") }

// Set 追加一个重复字符串参数值。
func (f *stringFlags) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type options struct {
	root    string
	files   []string
	all     bool
	output  string
	peer    []string
	timeout time.Duration
}

type coverageArtifact struct {
	Files             []string                `json:"files"`
	TrackedCandidates int                     `json:"tracked_candidates"`
	Inspected         int                     `json:"inspected"`
	TargetCompiles    []targetCompileEvidence `json:"target_compiles,omitempty"`
	Skipped           []skippedTarget         `json:"skipped,omitempty"`
	SkippedCount      int                     `json:"skipped_count"`
	Diagnostics       int                     `json:"diagnostics"`
}

type skippedTarget struct {
	File                    string   `json:"file"`
	Reason                  string   `json:"reason"`
	GOOS                    string   `json:"goos,omitempty"`
	GOARCH                  string   `json:"goarch,omitempty"`
	BuildTags               []string `json:"build_tags,omitempty"`
	BuildTagRegistryVersion string   `json:"build_tag_registry_version,omitempty"`
}

type targetSelection struct {
	files          []string
	candidates     int
	targetCompiles []targetCompileTarget
	skipped        []skippedTarget
}

type targetCompileTarget struct {
	File                    string
	GOOS                    string
	GOARCH                  string
	BuildTags               []string
	BuildTagRegistryVersion string
}

type targetCompileEvidence struct {
	File                    string   `json:"file"`
	GOOS                    string   `json:"goos"`
	GOARCH                  string   `json:"goarch"`
	BuildTags               []string `json:"build_tags,omitempty"`
	BuildTagRegistryVersion string   `json:"build_tag_registry_version,omitempty"`
}

type registeredBuildTagTarget struct {
	version string
	tags    []string
}

var registeredBuildTagTargets = []registeredBuildTagTarget{
	{version: buildTagTargetRegistryVersion, tags: []string{"codex_smoketest"}},
	{version: buildTagTargetRegistryVersion, tags: []string{"e2e"}},
	{version: buildTagTargetRegistryVersion, tags: []string{"e2e_claude"}},
	{version: buildTagTargetRegistryVersion, tags: []string{"e2e_vision"}},
	{version: buildTagTargetRegistryVersion, tags: []string{"lsp_integration"}},
	{version: buildTagTargetRegistryVersion, tags: []string{"manual"}},
	{version: buildTagTargetRegistryVersion, tags: []string{"sqlite_stress"}},
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

type diagnosticsPayload struct {
	Success bool `json:"success"`
	Data    []struct {
		File string   `json:"file"`
		Cols []string `json:"cols"`
		Rows [][]any  `json:"rows"`
	} `json:"data"`
	Total int `json:"total"`
	Meta  struct {
		Message string `json:"message"`
	} `json:"meta"`
}

func main() {
	if err := runMain(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "lsp diagnostics gate:", err)
		os.Exit(1)
	}
}

func runMain(args []string) error {
	fs := flag.NewFlagSet("lsp-diagnostics-gate", flag.ContinueOnError)
	root := fs.String("root", ".", "repository root")
	all := fs.Bool("all", false, "diagnose every tracked file supported by the configured adapters")
	output := fs.String("output", "", "optional atomic coverage artifact path")
	timeout := fs.Duration("timeout", 10*time.Minute, "collector deadline")
	peerBinary := fs.String("peer", "", "mcp-lsp peer binary; default is go run ./cmd/mcp-lsp")
	var files stringFlags
	var peerArgs stringFlags
	fs.Var(&files, "file", "changed file to diagnose; repeatable")
	fs.Var(&peerArgs, "peer-arg", "peer argument; repeatable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	peer := []string{"go", "run", "./cmd/mcp-lsp"}
	if strings.TrimSpace(*peerBinary) != "" {
		peer = append([]string{*peerBinary}, peerArgs...)
	}
	return run(context.Background(), options{
		root: *root, files: files, all: *all, output: *output, peer: peer, timeout: *timeout,
	})
}

// run 在一个真实 MCP-LSP peer 会话中完成目标选择、诊断和 coverage 输出。
func run(parent context.Context, opts options) error {
	root, err := resolveRepositoryRoot(opts.root)
	if err != nil {
		return err
	}
	selection, err := diagnosticTargets(root, opts)
	if err != nil {
		return err
	}
	if err := validateTargetSelection(selection); err != nil {
		return err
	}
	if err := compileTargetConstrainedFiles(parent, root, opts.timeout, selection.targetCompiles); err != nil {
		return err
	}
	if len(selection.files) == 0 {
		return emitCoverage(opts.output, selection)
	}
	if len(opts.peer) == 0 || strings.TrimSpace(opts.peer[0]) == "" {
		return errors.New("mcp-lsp peer command is required")
	}
	if err := collectDiagnostics(parent, root, opts, selection.files); err != nil {
		return err
	}
	return emitCoverage(opts.output, selection)
}

func resolveRepositoryRoot(path string) (string, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root symlinks: %w", err)
	}
	return root, nil
}

func validateTargetSelection(selection targetSelection) error {
	if len(selection.files) == 0 {
		if len(selection.targetCompiles) > 0 {
			return nil
		}
		return errors.New("no diagnostic targets are supported by the configured LSP adapters")
	}
	return nil
}

// compileTargetConstrainedFiles 为当前 host 排除的变更 Go 文件保留目标平台编译证据。
func compileTargetConstrainedFiles(parent context.Context, root string, timeout time.Duration, targets []targetCompileTarget) error {
	if len(targets) == 0 {
		return nil
	}
	ctx, cancel := platformconfig.WithTimeout(parent, timeout)
	defer cancel()
	outputRoot, err := os.MkdirTemp("", "super-dolphin-lsp-target-")
	if err != nil {
		return fmt.Errorf("create target compile directory: %w", err)
	}
	defer os.RemoveAll(outputRoot)
	seen := make(map[string]bool, len(targets))
	for index, target := range targets {
		packageDir := filepath.ToSlash(filepath.Dir(target.File))
		buildTags := strings.Join(target.BuildTags, ",")
		key := targetCompileKey(packageDir, target)
		if seen[key] {
			continue
		}
		seen[key] = true
		args := []string{"test", "-c"}
		if buildTags != "" {
			args = append(args, "-tags", buildTags)
		}
		args = append(args, "-o", filepath.Join(outputRoot, fmt.Sprintf("%d.test", index)), ".")
		command := exec.CommandContext(ctx, "go", args...)
		command.Dir = filepath.Join(root, filepath.FromSlash(packageDir))
		// 目标编译只验证 Go 构建约束；目标平台没有可用的 cgo 工具链。
		command.Env = append(
			os.Environ(),
			"GOOS="+target.GOOS,
			"GOARCH="+target.GOARCH,
			"CGO_ENABLED=0",
		)
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			return fmt.Errorf(
				"target compile %s (GOOS=%s GOARCH=%s build-tags=%q registry=%q): %w\n%s",
				target.File, target.GOOS, target.GOARCH, buildTags, target.BuildTagRegistryVersion, runErr, output,
			)
		}
	}
	return nil
}

func targetCompileKey(packageDir string, target targetCompileTarget) string {
	return packageDir + "|" + target.GOOS + "|" + target.GOARCH + "|" + target.BuildTagRegistryVersion + "|" + strings.Join(target.BuildTags, ",")
}

func collectDiagnostics(parent context.Context, root string, opts options, files []string) error {
	ctx, cancel := platformconfig.WithTimeout(parent, opts.timeout)
	defer cancel()
	shards := diagnosticShards(files)
	for _, shard := range shards {
		if err := collectDiagnosticShard(ctx, root, opts.peer, shard); err != nil {
			return err
		}
	}
	return nil
}

// diagnosticShards 将全仓大集合拆成顺序 peer，限制语言服务器峰值内存。
func diagnosticShards(files []string) [][]string {
	groups := map[string][]string{}
	for _, file := range files {
		group := diagnosticLanguageGroup(file)
		groups[group] = append(groups[group], file)
	}
	var shards [][]string
	for _, group := range []string{"go", "javascript", "other"} {
		shards = append(shards, splitDiagnosticGroup(groups[group])...)
	}
	return shards
}

func diagnosticLanguageGroup(file string) string {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".go":
		return "go"
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return "javascript"
	default:
		return "other"
	}
}

func splitDiagnosticGroup(files []string) [][]string {
	if len(files) == 0 {
		return nil
	}
	if len(files) < diagnosticsShardFileThreshold {
		return [][]string{files}
	}
	middle := (len(files) + 1) / 2
	return [][]string{files[:middle], files[middle:]}
}

func collectDiagnosticShard(ctx context.Context, root string, peer, files []string) error {
	client, err := startPeer(ctx, root, peer)
	if err != nil {
		return diagnosticShardError(ctx, err)
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		return diagnosticShardError(ctx, err)
	}
	for _, file := range files {
		if err := diagnosticsWithRetry(ctx, diagnosticsAttempts, diagnosticsRetryDelay, func() error {
			return client.diagnostics(ctx, root, file)
		}); err != nil {
			return diagnosticShardError(ctx, fmt.Errorf("diagnostics %s: %w", file, err))
		}
	}
	return nil
}

// diagnosticsWithRetry 仅重试 peer 明确声明可重试的诊断错误。
func diagnosticsWithRetry(ctx context.Context, attempts int, delay time.Duration, call func() error) error {
	for attempt := 1; attempt <= attempts; attempt++ {
		err := call()
		if err == nil {
			return nil
		}
		var retryable *retryableDiagnosticsError
		if !errors.As(err, &retryable) || attempt == attempts {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return context.Cause(ctx)
		case <-timer.C:
		}
	}
	return errors.New("diagnostics retry attempts must be positive")
}

// diagnosticShardError 将兄弟分片触发的进程退出归一为取消，避免遮蔽首个真实失败。
func diagnosticShardError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func emitCoverage(output string, selection targetSelection) error {
	coverage := coverageArtifact{
		Files: selection.files, TrackedCandidates: selection.candidates,
		Inspected: len(selection.files) + len(selection.targetCompiles),
		Skipped:   selection.skipped, SkippedCount: len(selection.skipped), Diagnostics: 0,
	}
	for _, target := range selection.targetCompiles {
		coverage.TargetCompiles = append(coverage.TargetCompiles, targetCompileEvidence{
			File: target.File, GOOS: target.GOOS, GOARCH: target.GOARCH,
			BuildTags: append([]string(nil), target.BuildTags...), BuildTagRegistryVersion: target.BuildTagRegistryVersion,
		})
	}
	if output != "" {
		if err := writeCoverageAtomically(output, coverage); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(coverage)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

type targetPolicy struct {
	supported    map[string]bool
	noise        map[string]bool
	buildContext build.Context
}

// diagnosticTargets 使用 Git 路径、adapter registry 与主机构建约束形成闭合诊断目标集。
func diagnosticTargets(root string, opts options) (targetSelection, error) {
	requested, err := requestedDiagnosticTargets(root, opts)
	if err != nil {
		return targetSelection{}, err
	}
	policy := newTargetPolicy()
	seen := make(map[string]bool)
	selection := targetSelection{}
	for _, raw := range requested {
		rel, skipped, include, classifyErr := policy.classify(root, raw, seen)
		if classifyErr != nil {
			return targetSelection{}, classifyErr
		}
		if skipped != nil {
			selection.candidates++
			selection.skipped = append(selection.skipped, *skipped)
			selection.targetCompiles = append(selection.targetCompiles, targetCompileTarget{
				File: skipped.File, GOOS: skipped.GOOS, GOARCH: skipped.GOARCH,
				BuildTags: append([]string(nil), skipped.BuildTags...), BuildTagRegistryVersion: skipped.BuildTagRegistryVersion,
			})
		}
		if include {
			selection.candidates++
			selection.files = append(selection.files, rel)
		}
	}
	sort.Strings(selection.files)
	sort.Slice(selection.skipped, func(i, j int) bool { return selection.skipped[i].File < selection.skipped[j].File })
	return selection, nil
}

func requestedDiagnosticTargets(root string, opts options) ([]string, error) {
	requested := append([]string(nil), opts.files...)
	if !opts.all {
		return requested, nil
	}
	if len(requested) > 0 {
		return nil, errors.New("--all and explicit files are mutually exclusive")
	}
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	return strings.Split(string(out), "\x00"), nil
}

type peerClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	decode  *json.Decoder
	stderr  *lockedBuffer
	nextID  int
	waiting chan error
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write 串行写入 peer stderr，避免 collector 与进程等待路径并发读写 bytes.Buffer。
func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(data)
}

// String 返回加锁后的 peer stderr 快照。
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startPeer(ctx context.Context, root string, peer []string) (*peerClient, error) {
	cmd := exec.CommandContext(ctx, peer[0], peer[1:]...)
	configurePeerCommandCancellation(cmd)
	cmd.Dir = root
	cmd.Env = peerEnvironment(root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("peer stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("peer stdout: %w", err)
	}
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start peer: %w", err)
	}
	client := &peerClient{cmd: cmd, stdin: stdin, decode: json.NewDecoder(stdout), stderr: stderr, waiting: make(chan error, 1)}
	safego.Go(ctx, nil, "lsp-diagnostics-gate.peer-wait", func(context.Context) {
		client.waiting <- cmd.Wait()
	})
	return client, nil
}

func peerEnvironment(root string) []string {
	skip := map[string]bool{
		"GO_AGENT_LSP_ROOT": true, "GO_AGENT_LSP_ROOTS": true, "GO_AGENT_CTL_RPC_ADDR": true,
		"GO_AGENT_PEER_MODE": true, "PROJECT_ROOT": true, "SUPER_DOLPHIN_RUNTIME_MODE": true,
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": true, "SUPER_DOLPHIN_LSP_BUNDLE_DIR": true,
		"SUPER_DOLPHIN_LSP_MANIFEST": true, "GOMEMLIMIT": true, "GOMAXPROCS": true, "NODE_OPTIONS": true,
	}
	env := make([]string, 0, len(os.Environ())+9)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !skip[key] {
			env = append(env, entry)
		}
	}
	roots, _ := json.Marshal([]string{root})
	return append(env,
		"GO_AGENT_LSP_ROOT="+root,
		"GO_AGENT_LSP_ROOTS="+string(roots),
		"PROJECT_ROOT="+root,
		"SUPER_DOLPHIN_RUNTIME_MODE=dev",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="+root,
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE=production",
		"GOMEMLIMIT=3GiB",
		"GOMAXPROCS=4",
		"NODE_OPTIONS=--max-old-space-size=1024",
	)
}

func (c *peerClient) initialize(ctx context.Context) error {
	id, err := c.sendRequest(map[string]any{"method": "initialize", "params": map[string]any{"protocolVersion": protocolVersion}})
	if err != nil {
		return err
	}
	if _, err := c.receive(ctx, id); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	return c.sendNotification("notifications/initialized", map[string]any{})
}

// diagnostics 调用真实 file(diagnostics) 并拒绝任一 severity 或不完整元数据。
func (c *peerClient) diagnostics(ctx context.Context, root, file string) error {
	id, err := c.sendRequest(map[string]any{
		"method": "tools/call",
		"params": map[string]any{
			"name": "file", "arguments": map[string]any{"action": "diagnostics", "file_path": file},
			"_cwd": root, "_workspaceRoots": []string{root},
		},
	})
	if err != nil {
		return err
	}
	raw, err := c.receive(ctx, id)
	if err != nil {
		return err
	}
	var result toolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode tool result: %w", err)
	}
	if result.IsError {
		return diagnosticsToolError(result)
	}
	var payload diagnosticsPayload
	if err := json.Unmarshal(result.StructuredContent, &payload); err != nil {
		return fmt.Errorf("decode diagnostics payload: %w", err)
	}
	if !payload.Success {
		return errors.New("diagnostics payload success=false")
	}
	if err := validateDiagnosticsMeta(payload.Meta.Message); err != nil {
		return err
	}
	if payload.Total != 0 || len(payload.Data) != 0 {
		return fmt.Errorf("found %d Error/Warning/Information/Hint diagnostics: %s", payload.Total, toolText(result))
	}
	return nil
}

// diagnosticsToolError 按 peer 的显式元数据区分临时错误和永久错误。
func diagnosticsToolError(result toolResult) error {
	text := toolText(result)
	err := fmt.Errorf("tool error: %s", text)
	if strings.Contains(text, "Retryable: yes") {
		return &retryableDiagnosticsError{err: err}
	}
	return err
}

func validateDiagnosticsMeta(message string) error {
	lower := strings.ToLower(strings.TrimSpace(message))
	for _, blocker := range []string{"no package metadata", "no packages found", "not ready", "partial", "timed out", "timeout"} {
		if strings.Contains(lower, blocker) {
			return fmt.Errorf("incomplete diagnostics metadata: %s", message)
		}
	}
	return nil
}

func toolText(result toolResult) string {
	var parts []string
	for _, item := range result.Content {
		if strings.TrimSpace(item.Text) != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, " ")
}

func (c *peerClient) sendRequest(fields map[string]any) (int, error) {
	c.nextID++
	fields["jsonrpc"] = "2.0"
	fields["id"] = c.nextID
	return c.nextID, json.NewEncoder(c.stdin).Encode(fields)
}

func (c *peerClient) sendNotification(method string, params any) error {
	return json.NewEncoder(c.stdin).Encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// receive 等待匹配的 MCP 响应，同时传播 deadline、peer 退出和协议错误。
func (c *peerClient) receive(ctx context.Context, wantID int) (json.RawMessage, error) {
	type result struct {
		response rpcResponse
		err      error
	}
	ready := make(chan result, 1)
	safego.Go(ctx, nil, "lsp-diagnostics-gate.receive", func(context.Context) {
		var response rpcResponse
		err := c.decode.Decode(&response)
		ready <- result{response: response, err: err}
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case processErr := <-c.waiting:
		return nil, fmt.Errorf("peer exited: %w; stderr=%s", processErr, c.stderr.String())
	case got := <-ready:
		if got.err != nil {
			return nil, fmt.Errorf("decode peer response: %w; stderr=%s", got.err, c.stderr.String())
		}
		if got.response.ID != wantID {
			return nil, fmt.Errorf("response id=%d, want %d", got.response.ID, wantID)
		}
		if got.response.Error != nil {
			return nil, fmt.Errorf("RPC error %d: %s", got.response.Error.Code, got.response.Error.Message)
		}
		return got.response.Result, nil
	}
}

func (c *peerClient) close() {
	_ = c.stdin.Close()
	if c.cmd.Cancel != nil {
		_ = c.cmd.Cancel()
	}
	select {
	case <-c.waiting:
	case <-time.After(time.Second):
	}
}

// writeCoverageAtomically 仅在 coverage 非空且计数闭合时用 rename 替换旧证据。
func writeCoverageAtomically(path string, coverage coverageArtifact) error {
	if err := validateCoverage(coverage); err != nil {
		return err
	}
	data, err := json.MarshalIndent(coverage, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create coverage directory: %w", err)
	}
	tmpName, err := writeCoverageTemp(dir, data)
	if err != nil {
		return err
	}
	defer os.Remove(tmpName)
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace coverage artifact: %w", err)
	}
	return nil
}

// validateCoverage 拒绝空 coverage、漏文件和候选计数不闭合。
func validateCoverage(coverage coverageArtifact) error {
	if coverage.Inspected == 0 ||
		coverage.Inspected != len(coverage.Files)+len(coverage.TargetCompiles) ||
		coverage.SkippedCount != len(coverage.Skipped) ||
		coverage.TrackedCandidates != len(coverage.Files)+coverage.SkippedCount ||
		len(coverage.TargetCompiles) > coverage.SkippedCount {
		return errors.New("refusing to write empty or incomplete diagnostics coverage")
	}
	return nil
}

// writeCoverageTemp 以私有权限持久化并同步临时 coverage 文件。
func writeCoverageTemp(dir string, data []byte) (string, error) {
	tmp, err := os.CreateTemp(dir, ".lsp-diagnostics-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create coverage temp: %w", err)
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	return tmpName, nil
}
