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
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const protocolVersion = "2024-11-05"

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
	Files             []string        `json:"files"`
	TrackedCandidates int             `json:"tracked_candidates"`
	Inspected         int             `json:"inspected"`
	Skipped           []skippedTarget `json:"skipped,omitempty"`
	SkippedCount      int             `json:"skipped_count"`
	Diagnostics       int             `json:"diagnostics"`
}

type skippedTarget struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

type targetSelection struct {
	files      []string
	candidates int
	skipped    []skippedTarget
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
	if skip, selectionErr := validateTargetSelection(selection); skip || selectionErr != nil {
		return selectionErr
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

func validateTargetSelection(selection targetSelection) (bool, error) {
	if len(selection.files) == 0 && len(selection.skipped) > 0 {
		fmt.Fprintf(os.Stderr, "lsp diagnostics skip: candidates=%d inspected=0 skipped=%d reason=host-build-constraints\n", selection.candidates, len(selection.skipped))
		return true, nil
	}
	if len(selection.files) == 0 {
		return false, errors.New("no diagnostic targets are supported by the configured LSP adapters")
	}
	return false, nil
}

func collectDiagnostics(parent context.Context, root string, opts options, files []string) error {
	ctx, cancel := platformconfig.WithTimeout(parent, opts.timeout)
	defer cancel()
	client, err := startPeer(ctx, root, opts.peer)
	if err != nil {
		return err
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		return err
	}
	for _, file := range files {
		if err := client.diagnostics(ctx, root, file); err != nil {
			return fmt.Errorf("diagnostics %s: %w", file, err)
		}
	}
	return nil
}

func emitCoverage(output string, selection targetSelection) error {
	coverage := coverageArtifact{
		Files: selection.files, TrackedCandidates: selection.candidates, Inspected: len(selection.files),
		Skipped: selection.skipped, SkippedCount: len(selection.skipped), Diagnostics: 0,
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

func newTargetPolicy() targetPolicy {
	config := platformconfig.DefaultLSPConfig()
	adapters := multilsp.NewLanguageAdapterRegistryFromConfig(config)
	supported := make(map[string]bool)
	for _, languageID := range adapters.LanguageIDs() {
		supported[languageID] = true
	}
	noise := make(map[string]bool, len(config.NoiseDirNames))
	for _, name := range config.NoiseDirNames {
		noise[name] = true
	}
	buildContext := build.Default
	buildContext.GOOS = runtime.GOOS
	buildContext.GOARCH = runtime.GOARCH
	return targetPolicy{supported: supported, noise: noise, buildContext: buildContext}
}

// classify 将单个 Git 路径裁决为诊断、主机构建约束跳过或非目标。
func (p targetPolicy) classify(root, raw string, seen map[string]bool) (string, *skippedTarget, bool, error) {
	rel, err := normalizeTargetPath(raw)
	if err != nil {
		return "", nil, false, err
	}
	if rel == "" || seen[rel] || pathContainsNoiseDir(rel, p.noise) {
		return "", nil, false, nil
	}
	abs, languageID, supported, err := p.supportedTarget(root, rel)
	if err != nil {
		return "", nil, false, err
	}
	if !supported {
		return "", nil, false, nil
	}
	seen[rel] = true
	if languageID != "go" {
		return rel, nil, true, nil
	}
	matched, err := p.buildContext.MatchFile(filepath.Dir(abs), filepath.Base(abs))
	if err != nil {
		return "", nil, false, fmt.Errorf("match host build constraints for %s: %w", rel, err)
	}
	if !matched {
		return "", &skippedTarget{File: rel, Reason: "host-build-constraints"}, false, nil
	}
	return rel, nil, true, nil
}

// supportedTarget 拒绝缺失、非普通文件、symlink 和 registry 未支持的语言路径。
func (p targetPolicy) supportedTarget(root, rel string) (string, string, bool, error) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("stat diagnostics target %s: %w", rel, err)
	}
	languageID := lspmanager.DetectLanguageID(rel)
	if !info.Mode().IsRegular() || !p.supported[languageID] {
		return "", "", false, nil
	}
	return abs, languageID, true, nil
}

// normalizeTargetPath 将显式目标限制为仓库内相对路径，拒绝绝对路径和目录逃逸。
func normalizeTargetPath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if filepath.IsAbs(value) {
		return "", fmt.Errorf("diagnostics target must be repository-relative: %q", raw)
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("diagnostics target escapes repository root: %q", raw)
	}
	return filepath.ToSlash(clean), nil
}

func pathContainsNoiseDir(path string, noise map[string]bool) bool {
	for part := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if noise[part] {
			return true
		}
	}
	return false
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
		"SUPER_DOLPHIN_LSP_MANIFEST": true,
	}
	env := make([]string, 0, len(os.Environ())+6)
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
		return fmt.Errorf("tool error: %s", toolText(result))
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
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
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
	if coverage.Inspected == 0 || len(coverage.Files) == 0 ||
		coverage.Inspected != len(coverage.Files) || coverage.SkippedCount != len(coverage.Skipped) ||
		coverage.TrackedCandidates != coverage.Inspected+coverage.SkippedCount {
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
