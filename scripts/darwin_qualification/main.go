// Command darwin_qualification proves the pinned Darling Darwin compatibility
// runtime with the repository's real cross-compiled Mach-O smoke tests.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const (
	qualificationSuite      = "darwin-mach-o-smoke"
	qualificationPackage    = "./cmd/mcp-lsp/internal/hiddenexec"
	maxTimeoutSeconds       = 600
	commandOutputLimit      = 1 << 20
	localQualificationState = "LOCAL_NON_AUTHORITATIVE"
	notVerifiedState        = "NOT_VERIFIED"
)

const qualificationTestMaterial = "TestDarwinIdentityUsesHighResolutionNativeStartToken\nTestDarwinProcessTreeRSSUsesPsClosure\nTestDarwinStartupPIDReuseDoesNotSignal\nTestDarwinStartupCaptureActionRaceRemainsZeroSignal\nTestProcessStartIdentityPredatesCurrentBoot\nTestProcessStartIdentityPredatesCurrentBootRejectsMalformedIdentity"

// qualificationTestNames 返回每次调用独立的固定 Darwin 资格测试名称集合。
func qualificationTestNames() []string {
	return strings.Split(qualificationTestMaterial, "\n")
}

type explicitString struct {
	value  string
	seen   bool
	option string
}
type explicitInt struct {
	target *int
	seen   bool
}

// String 返回整数参数当前值，供 flag 包展示选项状态。
func (value *explicitInt) String() string {
	if value == nil || value.target == nil {
		return ""
	}
	return strconv.Itoa(*value.target)
}

// Set 解析一次性整数参数，并拒绝重复提供。
func (value *explicitInt) Set(raw string) error {
	if value == nil || value.target == nil {
		return errors.New("timeout-seconds target is unavailable")
	}
	if value.seen {
		return errors.New("--timeout-seconds must be provided once")
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("--timeout-seconds must be an integer: %w", err)
	}
	value.seen = true
	*value.target = parsed
	return nil
}

type aliasValue struct {
	target *explicitString
	name   string
}

type cliOptions struct {
	repository     explicitString
	nodeID         explicitString
	probeIdentity  explicitString
	probeBootID    explicitString
	imageDigest    explicitString
	sourceRevision explicitString
	qualification  explicitString
	receiptPath    explicitString
	goPath         explicitString
	darlingPath    explicitString
	filePath       explicitString
	timeoutSeconds int
}

type commandOutput struct {
	stdout string
	stderr string
}

type limitedOutput struct {
	bytes.Buffer
	limit     int
	truncated bool
}

// String 返回参数存储的原始字符串值。
func (value *explicitString) String() string {
	if value == nil {
		return ""
	}
	return value.value
}

// String 返回显式参数当前值，供 flag 包展示选项状态。
func (value *aliasValue) String() string {
	return value.target.String()
}

// Set 记录一次显式参数，并拒绝同一语义字段的重复别名。
func (value *aliasValue) Set(raw string) error {
	if value.target.seen {
		return fmt.Errorf("%s and --%s identify the same option", value.target.option, value.name)
	}
	value.target.value = raw
	value.target.seen = true
	value.target.option = "--" + value.name
	return nil
}

func main() {
	if err := runQualification(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "darwin qualification failed: %v %s=1 %s=1\n", err, localQualificationState, notVerifiedState)
		os.Exit(1)
	}
	fmt.Printf("darwin qualification passed %s=1 %s=1\n", localQualificationState, notVerifiedState)
}

// runQualification 严格执行交叉编译、Mach-O 检查、Darling 测试和回执写入。
func runQualification(args []string) error {
	options, err := parseOptions(args)
	if err != nil {
		return err
	}
	workRoot, err := os.MkdirTemp("", "darwin-qualification-")
	if err != nil {
		return fmt.Errorf("create bounded qualification workspace: %w", err)
	}
	defer os.RemoveAll(workRoot)
	artifact := filepath.Join(workRoot, "hiddenexec.test")
	goTemp := filepath.Join(workRoot, "go-tmp")
	goCache := filepath.Join(workRoot, "go-cache")
	for _, directory := range []string{goTemp, goCache} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return fmt.Errorf("create qualification build directory: %w", err)
		}
	}
	if err := buildDarwinTest(options, artifact, goTemp, goCache); err != nil {
		return err
	}
	if err := verifyMachO(options, artifact); err != nil {
		return err
	}
	if err := runDarlingSmoke(options, artifact); err != nil {
		return err
	}
	receipt := makeReceipt(options)
	return writeReceipt(options.receiptPath.value, receipt)
}

// parseOptions 要求所有身份、工具路径、回执路径和超时均由调用方显式提供。
func parseOptions(args []string) (cliOptions, error) {
	var options cliOptions
	set := flag.NewFlagSet("darwin_qualification", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	registerAliases(set, &options.repository, "repo-path", "repo", "repository")
	registerAliases(set, &options.nodeID, "node-id")
	registerAliases(set, &options.probeIdentity, "probe-identity")
	registerAliases(set, &options.probeBootID, "probe-boot-id")
	registerAliases(set, &options.imageDigest, "image-digest")
	registerAliases(set, &options.sourceRevision, "source-revision")
	registerAliases(set, &options.qualification, "qualification-suite", "suite")
	registerAliases(set, &options.receiptPath, "receipt-path", "receipt")
	registerAliases(set, &options.goPath, "go-path", "go")
	registerAliases(set, &options.darlingPath, "darling-path", "darling")
	registerAliases(set, &options.filePath, "file-path", "file")
	set.Var(&explicitInt{target: &options.timeoutSeconds}, "timeout-seconds", "bounded timeout for each external command")
	if err := set.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if set.NArg() != 0 {
		return cliOptions{}, errors.New("positional arguments are not allowed")
	}
	if err := validateOptions(options); err != nil {
		return cliOptions{}, err
	}
	return options, nil
}

// registerAliases 为同一必填字段注册明确的长选项别名，并拒绝重复提供。
func registerAliases(set *flag.FlagSet, target *explicitString, names ...string) {
	for _, name := range names {
		set.Var(&aliasValue{target: target, name: name}, name, "explicit required value")
	}
}

// validateOptions 校验身份字段、工具可执行文件和回执目标的路径安全边界。
func validateOptions(options cliOptions) error {
	if err := validateRequiredOptions(options); err != nil {
		return err
	}
	if options.timeoutSeconds < 1 || options.timeoutSeconds > maxTimeoutSeconds {
		return fmt.Errorf("timeout-seconds must be in 1..%d", maxTimeoutSeconds)
	}
	if err := validateMetadata(options); err != nil {
		return err
	}
	return validateToolAndReceiptPaths(options)
}

// validateRequiredOptions 确认所有身份与路径字段均由命令行显式传入。
func validateRequiredOptions(options cliOptions) error {
	for _, field := range []struct {
		value *explicitString
		name  string
	}{
		{&options.repository, "repository"}, {&options.nodeID, "node id"},
		{&options.probeIdentity, "probe identity"}, {&options.probeBootID, "probe boot id"},
		{&options.imageDigest, "image digest"}, {&options.sourceRevision, "source revision"},
		{&options.qualification, "qualification suite"}, {&options.receiptPath, "receipt path"},
		{&options.goPath, "go path"}, {&options.darlingPath, "Darling path"}, {&options.filePath, "file path"},
	} {
		if err := requireExplicit(field.value, field.name); err != nil {
			return err
		}
	}
	return nil
}

// validateMetadata 校验固定 Darling 套件、来源修订和镜像摘要。
func validateMetadata(options cliOptions) error {
	if err := validateIdentity(options.imageDigest.value, "image digest"); err != nil {
		return err
	}
	if err := validateSourceRevision(options.sourceRevision.value); err != nil {
		return err
	}
	if options.qualification.value != qualificationSuite {
		return fmt.Errorf("qualification suite must be %q", qualificationSuite)
	}
	return nil
}

// validateToolAndReceiptPaths 校验仓库、工具和回执的非链接路径边界。
func validateToolAndReceiptPaths(options cliOptions) error {
	if err := validateDirectoryPath(options.repository.value, "repository"); err != nil {
		return err
	}
	for _, tool := range []struct {
		path  string
		label string
	}{
		{options.goPath.value, "go"}, {options.darlingPath.value, "Darling"}, {options.filePath.value, "file"},
	} {
		if err := validateExecutablePath(tool.path, tool.label); err != nil {
			return err
		}
	}
	return validateReceiptPath(options.receiptPath.value)
}

// requireExplicit 拒绝空值、空白和带控制字符的必填字段。
func requireExplicit(value *explicitString, label string) error {
	if value == nil || !value.seen || value.value == "" {
		return fmt.Errorf("%s is explicitly required", label)
	}
	if value.value != strings.TrimSpace(value.value) || strings.IndexFunc(value.value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%s contains invalid whitespace or control characters", label)
	}
	return nil
}

// validateIdentity 校验不可变 SHA-256 镜像摘要。
func validateIdentity(value, label string) error {
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(value) {
		return fmt.Errorf("%s must be sha256:<64 lowercase hex>", label)
	}
	return nil
}

// validateSourceRevision 校验 Darling 官方仓库的固定 tag 或完整提交。
func validateSourceRevision(value string) error {
	const prefix = "https://github.com/darlinghq/darling@"
	if !strings.HasPrefix(value, prefix) {
		return errors.New("source revision must use the pinned official Darling repository")
	}
	revision := strings.TrimPrefix(value, prefix)
	if strings.IndexFunc(revision, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("source revision contains whitespace or control characters")
	}
	if tag, ok := strings.CutPrefix(revision, "refs/tags/"); ok {
		return validatePinnedTag(tag)
	}
	if len(revision) != 40 || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(revision) {
		return errors.New("source revision must be a full lowercase commit or refs/tags value")
	}
	return nil
}

// validatePinnedTag 校验 refs/tags 后的 canonical 分段和不可变别名边界。
func validatePinnedTag(tag string) error {
	if tag == "" {
		return errors.New("source revision tag must not be empty")
	}
	segmentPattern := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	for segment := range strings.SplitSeq(tag, "/") {
		if segment == "" || segment == "." || segment == ".." || !segmentPattern.MatchString(segment) {
			return errors.New("source revision tag has a non-canonical path segment")
		}
		switch strings.ToLower(segment) {
		case "latest", "main", "master", "head":
			return errors.New("source revision tag must be immutable")
		}
	}
	return nil
}

// validateDirectoryPath 要求存在的仓库目录为绝对、规范且无符号链接路径。
func validateDirectoryPath(path, label string) error {
	if err := validateAbsoluteCleanPath(path, label); err != nil {
		return err
	}
	if err := validateAncestors(filepath.Dir(path), label+" parent"); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s is unavailable: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s must be a regular non-symlink directory", label)
	}
	return nil
}

// validateExecutablePath 要求工具为绝对、规范、非链接且可执行文件。
func validateExecutablePath(path, label string) error {
	if err := validateAbsoluteCleanPath(path, label); err != nil {
		return err
	}
	if err := validateAncestors(filepath.Dir(path), label+" parent"); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s is unavailable: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s must be a regular non-symlink executable", label)
	}
	return nil
}

// validateReceiptPath 校验回执父目录和目标不存在、绝对、规范且无符号链接。
func validateReceiptPath(path string) error {
	if err := validateAbsoluteCleanPath(path, "receipt path"); err != nil {
		return err
	}
	if err := validateAncestors(filepath.Dir(path), "receipt parent"); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("receipt path must not already exist")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect receipt path: %w", err)
	}
	return nil
}

// validateAbsoluteCleanPath 检查非空路径的绝对性、规范性和字符边界。
func validateAbsoluteCleanPath(path, label string) error {
	if path == "" || path != strings.TrimSpace(path) || filepath.Clean(path) != path || !filepath.IsAbs(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return fmt.Errorf("%s must be absolute and clean", label)
	}
	return nil
}

// validateAncestors 逐级拒绝父目录中的链接、非目录和缺失项。
func validateAncestors(path, label string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("%s is unavailable: %w", label, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%s contains a symlink or non-directory", label)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

// buildDarwinTest 在固定无网络工具链环境中构建目标 Darwin 测试二进制。
func buildDarwinTest(options cliOptions, artifact, goTemp, goCache string) error {
	env := buildEnvironment(goTemp, goCache)
	result, err := runBounded(options, options.goPath.value, []string{"test", "-c", "-o", artifact, qualificationPackage}, env, options.repository.value)
	if err != nil {
		return fmt.Errorf("cross-compile Darwin hiddenexec tests: %w", err)
	}
	if err := rejectRuntimeDiagnostics(result); err != nil {
		return fmt.Errorf("cross-compile Darwin hiddenexec tests: %w", err)
	}
	return nil
}

// buildEnvironment 锁定 Darwin 交叉编译和离线模块解析策略。
func buildEnvironment(goTemp, goCache string) []string {
	blocked := map[string]bool{"GOOS": true, "GOARCH": true, "CGO_ENABLED": true, "GOTOOLCHAIN": true, "GOPROXY": true, "GOTMPDIR": true, "GOCACHE": true, "GOFLAGS": true, "GOWORK": true}
	env := make([]string, 0, len(os.Environ())+8)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || blocked[key] {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "GOOS=darwin", "GOARCH=amd64", "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOPROXY=off", "GOTMPDIR="+goTemp, "GOCACHE="+goCache, "GOWORK=off")
	return env
}

// verifyMachO 用显式 file 工具确认产物是精确的 x86_64 Mach-O 可执行文件。
func verifyMachO(options cliOptions, artifact string) error {
	result, err := runBounded(options, options.filePath.value, []string{"-b", artifact}, runtimeEnvironment(), options.repository.value)
	if err != nil {
		return fmt.Errorf("inspect Darwin test binary: %w", err)
	}
	if err := rejectRuntimeDiagnostics(result); err != nil {
		return fmt.Errorf("inspect Darwin test binary: %w", err)
	}
	description := strings.TrimSpace(result.stdout)
	canonical := strings.HasPrefix(description, "Mach-O 64-bit x86_64 executable") || strings.HasPrefix(description, "Mach-O 64-bit executable x86_64")
	if strings.ContainsAny(description, "\r\n") || !canonical || strings.Contains(strings.ToLower(description), "universal") {
		return fmt.Errorf("Darwin test binary has non-canonical file identity: %q", description)
	}
	return nil
}

// runDarlingSmoke 先验证四个 test.list 名称，再执行同一个精确 test.run 正则。
func runDarlingSmoke(options cliOptions, artifact string) error {
	regex := exactTestRegex()
	listed, err := runDarling(options, artifact, "-test.list", regex)
	if err != nil {
		return fmt.Errorf("Darling test-list qualification: %w", err)
	}
	if err := verifyTestList(listed.stdout); err != nil {
		return err
	}
	if err := rejectRuntimeDiagnostics(listed); err != nil {
		return err
	}
	run, err := runDarling(options, artifact, "-test.run", regex)
	if err != nil {
		return fmt.Errorf("Darling smoke qualification: %w", err)
	}
	return rejectRuntimeDiagnostics(run)
}

// runDarling 通过显式 Darling 工具运行一个有界的 test.list 或 test.run 操作。
func runDarling(options cliOptions, artifact string, flagName, regex string) (commandOutput, error) {
	return runBounded(options, options.darlingPath.value, []string{artifact, flagName, regex}, runtimeEnvironment(), options.repository.value)
}

// runtimeEnvironment 标记所有本地资格执行均不具备远程权威性。
func runtimeEnvironment() []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "LOCAL_NON_AUTHORITATIVE" || key == "NOT_VERIFIED" || key == "SUPER_DOLPHIN_RUNTIME" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "LOCAL_NON_AUTHORITATIVE=1", "NOT_VERIFIED=1", "SUPER_DOLPHIN_RUNTIME=darling_compat_runtime")
}

// exactTestRegex 构造只允许固定四个 Darwin 资格测试的正则。
func exactTestRegex() string {
	return "^(" + strings.Join(qualificationTestNames(), "|") + ")$"
}

// verifyTestList 要求 test.list 的输出与固定四测试集合逐项、恰好一次一致。
func verifyTestList(output string) error {
	trimmed := strings.TrimSuffix(output, "\n")
	if trimmed == "" || strings.Contains(trimmed, "\r") {
		return errors.New("Darling test.list output is empty or malformed")
	}
	expectedNames := qualificationTestNames()
	expected := make(map[string]struct{}, len(expectedNames))
	for _, name := range expectedNames {
		expected[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(expected))
	for line := range strings.SplitSeq(trimmed, "\n") {
		if _, ok := expected[line]; !ok {
			return fmt.Errorf("Darling test.list returned unexpected test %q", line)
		}
		if _, duplicate := seen[line]; duplicate {
			return fmt.Errorf("Darling test.list duplicated test %q", line)
		}
		seen[line] = struct{}{}
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("Darling test.list returned %d tests, want %d", len(seen), len(expected))
	}
	return nil
}

// rejectRuntimeDiagnostics 将 ENOSYS、framework 缺失和 not-found 信号统一视为失败。
func rejectRuntimeDiagnostics(result commandOutput) error {
	combined := strings.ToLower(result.stdout + "\n" + result.stderr)
	if strings.Contains(combined, "enosys") || strings.Contains(combined, "not found") || strings.Contains(combined, "framework missing") || strings.Contains(combined, "frameworks missing") {
		return errors.New("Darling runtime reported ENOSYS, framework missing, or not-found support")
	}
	if strings.TrimSpace(result.stderr) != "" {
		return errors.New("Darling qualification emitted stderr")
	}
	return nil
}

// runBounded 执行一个显式工具并同时限制上下文、后代管道等待和输出内存。
func runBounded(options cliOptions, name string, args, env []string, directory string) (commandOutput, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(options.timeoutSeconds)*time.Second)
	defer cancel()
	command := exec.Command(name, args...)
	command.Dir = directory
	command.Env = env
	stdout := &limitedOutput{limit: commandOutputLimit}
	stderr := &limitedOutput{limit: commandOutputLimit}
	command.Stdout = stdout
	command.Stderr = stderr
	// 仅依据 owned-command helper 返回值分类超时/取消，不读取事后 ctx.Err。
	helperErr := runOwnedCommand(ctx, command)
	result := commandOutput{stdout: stdout.String(), stderr: stderr.String()}
	commandErr := classifyBoundedCommandError(helperErr)
	if stdout.truncated || stderr.truncated {
		outputErr := errors.New("external command output exceeded bounded limit")
		if commandErr != nil {
			return result, errors.Join(outputErr, commandErr)
		}
		return result, outputErr
	}
	if commandErr != nil {
		return result, commandErr
	}
	return result, nil
}

// classifyBoundedCommandError 保留 helper 根因，并给 timeout/cancel 提供明确标签。
func classifyBoundedCommandError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		root := fmt.Errorf("external command timeout: %w", context.DeadlineExceeded)
		if err != nil {
			return errors.Join(root, err)
		}
		return root
	case errors.Is(err, context.Canceled):
		root := fmt.Errorf("external command canceled: %w", context.Canceled)
		if err != nil {
			return errors.Join(root, err)
		}
		return root
	case err != nil:
		return fmt.Errorf("external command failed: %w", err)
	default:
		return nil
	}
}

// Write 将外部输出限制在固定内存上限内，同时保留溢出失败标记。
func (output *limitedOutput) Write(data []byte) (int, error) {
	remaining := output.limit - output.Len()
	if remaining <= 0 {
		output.truncated = true
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = output.Buffer.Write(data[:remaining])
		output.truncated = true
		return len(data), nil
	}
	return output.Buffer.Write(data)
}

// makeReceipt 生成带有 heteroci canonical digest 的严格 Darling 资格回执。
func makeReceipt(options cliOptions) heteroci.CapabilityQualificationReceipt {
	receipt := heteroci.CapabilityQualificationReceipt{
		SchemaVersion:        heteroci.CapabilityQualificationSchemaVersion,
		NodeID:               options.nodeID.value,
		Runtime:              heteroci.RuntimeDarlingCompatRuntime,
		ProbeIdentity:        options.probeIdentity.value,
		ProbeBootID:          options.probeBootID.value,
		ImageDigest:          options.imageDigest.value,
		SourceRevision:       options.sourceRevision.value,
		QualificationSuite:   options.qualification.value,
		ImmutableIdentity:    "",
		CompletedAtUnixMilli: time.Now().UnixMilli(),
	}
	receipt.Digest = receipt.CanonicalDigest()
	return receipt
}

// writeReceipt 以临时文件、fsync、create-only link 和目录同步持久化 0600 回执。
func writeReceipt(path string, receipt heteroci.CapabilityQualificationReceipt) error {
	if receipt.Digest == "" || receipt.Digest != receipt.CanonicalDigest() {
		return errors.New("qualification receipt canonical digest is invalid")
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode qualification receipt: %w", err)
	}
	data = append(data, '\n')
	parent := filepath.Dir(path)
	temporary, err := os.CreateTemp(parent, ".darwin-qualification-receipt-*")
	if err != nil {
		return fmt.Errorf("create qualification receipt temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := writeTemporaryReceipt(temporary, data); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return fmt.Errorf("create qualification receipt: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("remove qualification receipt temporary file: %w", err)
	}
	keep = true
	if err := syncDirectory(parent); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// writeTemporaryReceipt 将 JSON 写入 0600 临时文件并完成文件级 fsync。
func writeTemporaryReceipt(file *os.File, data []byte) error {
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set qualification receipt mode: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write qualification receipt: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync qualification receipt: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close qualification receipt: %w", err)
	}
	return nil
}

// syncDirectory 将原子 rename 的父目录变更同步到持久存储。
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open qualification receipt directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(syncErr, closeErr)
	}
	return nil
}
