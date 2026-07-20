package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	workflowCheckoutRoot            = "/workspace/super-dolphin-checkout"
	workflowAuthorityBundleEnv      = "SUPER_DOLPHIN_GATE_AUTHORITY_BUNDLE_B64"
	workflowOIDCAudienceEnv         = "SUPER_DOLPHIN_GATE_WORKFLOW_OIDC_AUDIENCE"
	workflowOIDCRequestURLEnv       = "ACTIONS_ID_TOKEN_REQUEST_URL"
	workflowOIDCRequestTokenEnv     = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
	workflowAuthorityBundleMaxBytes = 1 << 20
)

type workflowHostOptions struct {
	repositoryRoot  string
	eventRepository string
	eventRef        string
	eventSHA        string
}

type workflowVerifiedSource struct {
	objectFormat   string
	sourceTree     string
	repositoryRoot string
	cleanup        func() error
}

// runWorkflowHost is the bootstrap-image-only authority boundary. The caller can
// mount candidate source here, but it never executes a candidate-controlled file.
func runWorkflowHost(args []string, stdout io.Writer) (retErr error) {
	options, err := parseWorkflowHostOptions(args)
	if err != nil {
		return err
	}
	authorityRoot, config, root, err := loadWorkflowHostAuthority()
	if err != nil {
		return infrastructureError("prepare workflow authority: %v", err)
	}
	defer os.RemoveAll(authorityRoot)
	source, err := verifyWorkflowHostSource(options, config, root)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, source.cleanup())
	}()
	attestation, err := workflowOIDCAttestationDigest(context.Background(), options)
	if err != nil {
		return infrastructureError("obtain workflow OIDC attestation: %v", err)
	}
	return runWorkflowWithConnectorAt([]string{
		"--profile", string(gatecontract.ProfileRemoteRequired),
		"--object-format", source.objectFormat,
		"--commit", options.eventSHA,
		"--source-tree", source.sourceTree,
	}, stdout, connectProductionCoordinator, source.repositoryRoot, attestation)
}

// loadWorkflowHostAuthority 物化一次性 authority bundle，生成 runtime 配置并加载经签名的 bootstrap root。
func loadWorkflowHostAuthority() (string, productionCoordinatorConfig, productionBootstrapRoot, error) {
	runtimeRoot, err := requiredWorkflowEnvironment(workflowRuntimeEnvironment)
	if err != nil {
		return "", productionCoordinatorConfig{}, productionBootstrapRoot{}, fmt.Errorf("load workflow runtime root: %w", err)
	}
	if _, err := canonicalProductionDirectory(runtimeRoot); err != nil {
		return "", productionCoordinatorConfig{}, productionBootstrapRoot{}, fmt.Errorf("validate workflow runtime root: %w", err)
	}
	authorityRoot, err := materializeWorkflowAuthorityBundle(os.Getenv(workflowAuthorityBundleEnv))
	if err != nil {
		return "", productionCoordinatorConfig{}, productionBootstrapRoot{}, fmt.Errorf("materialize workflow authority bundle: %w", err)
	}
	configPath := filepath.Join(runtimeRoot, workflowGeneratedConfigFile)
	if err := prepareWorkflowProductionConfig(
		filepath.Join(authorityRoot, workflowConfigTemplateFile), authorityRoot, runtimeRoot, configPath,
	); err != nil {
		_ = os.RemoveAll(authorityRoot)
		return "", productionCoordinatorConfig{}, productionBootstrapRoot{}, fmt.Errorf("prepare workflow production config: %w", err)
	}
	config, err := loadProductionCoordinatorConfigFile(configPath)
	if err != nil {
		_ = os.RemoveAll(authorityRoot)
		return "", productionCoordinatorConfig{}, productionBootstrapRoot{}, fmt.Errorf("load generated workflow production config: %w", err)
	}
	root, err := loadProductionBootstrapRoot(config.BootstrapRootFile, config.AcceptedImageSigners)
	if err != nil {
		_ = os.RemoveAll(authorityRoot)
		return "", productionCoordinatorConfig{}, productionBootstrapRoot{}, fmt.Errorf("load workflow bootstrap root: %w", err)
	}
	return authorityRoot, config, root, nil
}

// verifyWorkflowHostSource 将事件身份、只读 checkout、可信镜像与 bootstrap 签名链串联校验后返回唯一可物化的可信源。
func verifyWorkflowHostSource(options workflowHostOptions, config productionCoordinatorConfig, root productionBootstrapRoot) (workflowVerifiedSource, error) {
	if err := validateWorkflowEvent(options, config, root); err != nil {
		return workflowVerifiedSource{}, protocolError("validate workflow event identity: %v", err)
	}
	objectFormat, sourceTree, err := verifyWorkflowCheckout(options)
	if err != nil {
		return workflowVerifiedSource{}, infrastructureError("verify read-only workflow checkout: %v", err)
	}
	if gatecontract.GitObjectFormat(objectFormat) != root.ObjectFormat {
		return workflowVerifiedSource{}, protocolError("workflow checkout object format does not match signed bootstrap root")
	}
	if err := importWorkflowTrustedMirror(context.Background(), root, config.TrustedRepository, options.eventRef, options.eventSHA, sourceTree); err != nil {
		return workflowVerifiedSource{}, infrastructureError("import signed workflow event into trusted mirror: %v", err)
	}
	authority, err := newProductionGitAuthority(context.Background(), config)
	if err != nil {
		return workflowVerifiedSource{}, infrastructureError("open imported workflow trusted mirror: %v", err)
	}
	if err := verifyProductionBootstrapRepository(context.Background(), authority, root); err != nil {
		return workflowVerifiedSource{}, infrastructureError("verify imported workflow trusted mirror: %v", err)
	}
	repositoryRoot, cleanup, err := checkoutWorkflowTrustedSource(
		context.Background(), config.TrustedRepository, root.ObjectFormat, options.eventSHA, sourceTree,
	)
	if err != nil {
		return workflowVerifiedSource{}, infrastructureError("checkout verified workflow trusted mirror: %v", err)
	}
	return workflowVerifiedSource{
		objectFormat: objectFormat, sourceTree: sourceTree, repositoryRoot: repositoryRoot, cleanup: cleanup,
	}, nil
}

// parseWorkflowHostOptions 严格解析固定只读挂载与 GitHub 事件字段，拒绝空白、控制字符和额外参数。
func parseWorkflowHostOptions(args []string) (workflowHostOptions, error) {
	options := workflowHostOptions{}
	flags := flag.NewFlagSet("workflow-host", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.repositoryRoot, "repository-root", "", "read-only candidate checkout")
	flags.StringVar(&options.eventRepository, "event-repository", "", "GitHub event repository")
	flags.StringVar(&options.eventRef, "event-ref", "", "GitHub event full ref")
	flags.StringVar(&options.eventSHA, "event-sha", "", "GitHub event commit")
	if err := flags.Parse(args); err != nil {
		return workflowHostOptions{}, protocolError("parse workflow-host flags: %v", err)
	}
	if flags.NArg() != 0 {
		return workflowHostOptions{}, protocolError("workflow-host has unexpected positional arguments: %v", flags.Args())
	}
	if options.repositoryRoot != workflowCheckoutRoot {
		return workflowHostOptions{}, protocolError("workflow-host repository-root must be the fixed read-only checkout mount")
	}
	for name, value := range map[string]string{
		"event-repository": options.eventRepository, "event-ref": options.eventRef, "event-sha": options.eventSHA,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return workflowHostOptions{}, protocolError("workflow-host --%s is required and canonical", name)
		}
	}
	return options, nil
}

func validateWorkflowEvent(options workflowHostOptions, config productionCoordinatorConfig, root productionBootstrapRoot) error {
	if options.eventRepository != config.RepoID || options.eventRepository != root.RepoID {
		return errors.New("event repository does not match signed workflow repository")
	}
	if !validWorkflowEventRef(options.eventRef) {
		return errors.New("event ref must be a canonical branch ref or strict pull request ref from the signed repository")
	}
	return validateWorkflowEventSHA(options.eventSHA, root.ObjectFormat)
}

func validWorkflowEventRef(ref string) bool {
	return validWorkflowBranchRef(ref) || validWorkflowPullRequestRef(ref)
}

func validWorkflowPullRequestRef(ref string) bool {
	const prefix = "refs/pull/"
	if !strings.HasPrefix(ref, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(ref, prefix), "/")
	return len(parts) == 2 && parts[1] == "head" && validWorkflowPullRequestNumber(parts[0])
}

// validWorkflowPullRequestNumber 校验 PR 编号为无前导零的正十进制整数。
func validWorkflowPullRequestNumber(value string) bool {
	if value == "" || value == "0" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// validWorkflowBranchRef 校验工作流引用为 Git 接受的规范分支引用。
func validWorkflowBranchRef(ref string) bool {
	const prefix = "refs/heads/"
	if !strings.HasPrefix(ref, prefix) {
		return false
	}
	name := strings.TrimPrefix(ref, prefix)
	return validWorkflowBranchName(name) && validWorkflowBranchCharacters(name) && validWorkflowBranchComponents(name)
}

// validWorkflowBranchName 拒绝 Git 规范禁止的空名称、保留名称和危险序列。
func validWorkflowBranchName(name string) bool {
	if name == "" || name == "@" || strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.HasSuffix(name, ".") {
		return false
	}
	return true
}

func validWorkflowBranchCharacters(name string) bool {
	for _, character := range name {
		if character <= 0x20 || character == 0x7f || strings.ContainsRune("\\~^:?*[", character) {
			return false
		}
	}
	return true
}

func validWorkflowBranchComponents(name string) bool {
	for component := range strings.SplitSeq(name, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

// validateWorkflowEventSHA 按 bootstrap 声明的 Git 对象格式校验事件提交 ID 的长度和小写十六进制形式。
func validateWorkflowEventSHA(eventSHA string, objectFormat gatecontract.GitObjectFormat) error {
	length := workflowObjectIDLength(objectFormat)
	if len(eventSHA) != length {
		return errors.New("event SHA does not match signed object format")
	}
	for _, character := range eventSHA {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return errors.New("event SHA must be lowercase hexadecimal")
		}
	}
	return nil
}

func workflowObjectIDLength(objectFormat gatecontract.GitObjectFormat) int {
	if objectFormat == gatecontract.GitObjectFormatSHA256 {
		return 64
	}
	return 40
}

func verifyWorkflowCheckout(options workflowHostOptions) (string, string, error) {
	info, err := os.Lstat(options.repositoryRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.Join(errors.New("workflow checkout mount must be a real directory"), err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "", "", fmt.Errorf("resolve Git executable: %w", err)
	}
	return verifyWorkflowCheckoutGit(gitPath, options)
}

// verifyWorkflowCheckoutGit 读取只读 checkout 的 HEAD、tree 与 object format，并要求它们与事件提交严格一致。
func verifyWorkflowCheckoutGit(gitPath string, options workflowHostOptions) (string, string, error) {
	head, err := workflowGitLine(context.Background(), gitPath, options.repositoryRoot, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}")
	if err != nil || head != options.eventSHA {
		return "", "", errors.Join(errors.New("read-only checkout HEAD does not match event SHA"), err)
	}
	tree, err := workflowGitLine(context.Background(), gitPath, options.repositoryRoot, "rev-parse", "--verify", "--end-of-options", options.eventSHA+"^{tree}")
	if err != nil {
		return "", "", err
	}
	objectFormat, err := workflowGitLine(context.Background(), gitPath, options.repositoryRoot, "rev-parse", "--show-object-format")
	if err != nil || (objectFormat != "sha1" && objectFormat != "sha256") {
		return "", "", errors.Join(errors.New("workflow checkout has unsupported object format"), err)
	}
	return objectFormat, tree, nil
}

func workflowGitLine(ctx context.Context, gitPath, directory string, args ...string) (string, error) {
	output, err := workflowGitCommand(ctx, gitPath, directory, args...)
	value := strings.TrimSuffix(string(output), "\n")
	if err != nil || value == "" || strings.ContainsAny(value, "\r\n\x00") || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("workflow Git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return value, nil
}

func workflowGitCommand(ctx context.Context, gitPath, directory string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, gitPath, append([]string{"-C", directory}, args...)...)
	command.Env = workflowGitEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("workflow Git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func workflowGitEnvironment() []string {
	return []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "PATH=" + os.Getenv("PATH")}
}

// importWorkflowTrustedMirror 从签名 remote 重新建立空 bare mirror，并同时验证 trusted baseline 与 workflow 事件 tree。
func importWorkflowTrustedMirror(
	ctx context.Context,
	root productionBootstrapRoot,
	mirror string,
	eventRef string,
	eventSHA string,
	eventTree string,
) error {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("resolve Git executable: %w", err)
	}
	if entries, err := os.ReadDir(mirror); err != nil || len(entries) != 0 {
		return errors.Join(errors.New("workflow trusted mirror root must be empty"), err)
	}
	if err := initializeWorkflowTrustedMirror(ctx, gitPath, mirror, root); err != nil {
		return err
	}
	if err := fetchWorkflowTrustedRefs(ctx, gitPath, mirror, root.TrustedRef, eventRef); err != nil {
		return err
	}
	return verifyWorkflowTrustedMirror(ctx, gitPath, mirror, root, eventRef, eventSHA, eventTree)
}

func initializeWorkflowTrustedMirror(ctx context.Context, gitPath, mirror string, root productionBootstrapRoot) error {
	if output, err := exec.CommandContext(ctx, gitPath, "init", "--bare", "--object-format="+string(root.ObjectFormat), mirror).CombinedOutput(); err != nil {
		return fmt.Errorf("initialize workflow trusted mirror: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if _, err := workflowGitCommand(ctx, gitPath, mirror, "remote", "add", "origin", root.RemoteURL); err != nil {
		return fmt.Errorf("configure signed workflow remote: %w", err)
	}
	return nil
}

func fetchWorkflowTrustedRefs(ctx context.Context, gitPath, mirror, trustedRef, eventRef string) error {
	for _, ref := range []string{trustedRef, eventRef} {
		if _, err := workflowGitCommand(ctx, gitPath, mirror, "fetch", "--no-tags", "--no-write-fetch-head", "origin", "+"+ref+":"+ref); err != nil {
			return fmt.Errorf("fetch signed workflow ref %q: %w", ref, err)
		}
	}
	return nil
}

// verifyWorkflowTrustedMirror 确认 fetch 后的 baseline、事件 ref 与事件 tree 均未偏离 bootstrap 和 checkout 观察值。
func verifyWorkflowTrustedMirror(ctx context.Context, gitPath, mirror string, root productionBootstrapRoot, eventRef, eventSHA, eventTree string) error {
	trusted, err := workflowGitLine(ctx, gitPath, mirror, "rev-parse", "--verify", "--end-of-options", root.TrustedRef+"^{commit}")
	if err != nil || trusted != root.BaselineCommit {
		return errors.Join(errors.New("signed trusted ref does not match bootstrap baseline"), err)
	}
	observed, err := workflowGitLine(ctx, gitPath, mirror, "rev-parse", "--verify", "--end-of-options", eventRef+"^{commit}")
	if err != nil || observed != eventSHA {
		return errors.Join(errors.New("signed remote event ref does not match workflow event SHA"), err)
	}
	observedTree, err := workflowGitLine(ctx, gitPath, mirror, "rev-parse", "--verify", "--end-of-options", eventSHA+"^{tree}")
	if err != nil || observedTree != eventTree {
		return errors.Join(errors.New("signed remote event tree does not match read-only checkout"), err)
	}
	return nil
}

// checkoutWorkflowTrustedSource 从已验证镜像创建私有 detached checkout，供只接受非 bare 仓库的源码物化器使用。
func checkoutWorkflowTrustedSource(
	ctx context.Context,
	mirror string,
	objectFormat gatecontract.GitObjectFormat,
	eventSHA string,
	eventTree string,
) (string, func() error, error) {
	gitPath, err := prepareWorkflowTrustedCheckout(eventSHA, objectFormat)
	if err != nil {
		return "", nil, err
	}
	root, err := os.MkdirTemp("", "super-dolphin-gate-workflow-source.")
	if err != nil {
		return "", nil, fmt.Errorf("create workflow trusted source root: %w", err)
	}
	cleanup := func() error { return cleanupWorkflowTrustedSource(root) }
	fail := func(err error) (string, func() error, error) {
		return "", nil, errors.Join(err, cleanup())
	}
	repositoryRoot := filepath.Join(root, "source")
	command := exec.CommandContext(ctx, gitPath, "clone", "--no-checkout", "--no-local", "--quiet", "--", mirror, repositoryRoot)
	command.Env = workflowGitEnvironment()
	if output, err := command.CombinedOutput(); err != nil {
		return fail(fmt.Errorf("clone verified workflow trusted mirror: %w: %s", err, strings.TrimSpace(string(output))))
	}
	if _, err := workflowGitCommand(ctx, gitPath, repositoryRoot, "checkout", "--detach", "--force", "--quiet", eventSHA); err != nil {
		return fail(fmt.Errorf("checkout verified workflow event: %w", err))
	}
	head, err := workflowGitLine(ctx, gitPath, repositoryRoot, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}")
	if err != nil || head != eventSHA {
		return fail(errors.Join(errors.New("trusted workflow checkout HEAD does not match event SHA"), err))
	}
	tree, err := workflowGitLine(ctx, gitPath, repositoryRoot, "rev-parse", "--verify", "--end-of-options", eventSHA+"^{tree}")
	if err != nil || tree != eventTree {
		return fail(errors.Join(errors.New("trusted workflow checkout tree does not match verified event tree"), err))
	}
	if err := makeWorkflowTrustedSourceReadOnly(repositoryRoot); err != nil {
		return fail(fmt.Errorf("protect trusted workflow checkout: %w", err))
	}
	return repositoryRoot, cleanup, nil
}

// prepareWorkflowTrustedCheckout 校验事件 SHA 并解析 Git，确保创建临时 checkout 前完成全部输入准备。
func prepareWorkflowTrustedCheckout(eventSHA string, objectFormat gatecontract.GitObjectFormat) (string, error) {
	if err := validateWorkflowEventSHA(eventSHA, objectFormat); err != nil {
		return "", fmt.Errorf("validate workflow checkout event SHA: %w", err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("resolve Git executable: %w", err)
	}
	return gitPath, nil
}

func makeWorkflowTrustedSourceReadOnly(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		mode := os.FileMode(0o400)
		if info.IsDir() {
			mode = 0o500
		}
		return os.Chmod(path, mode)
	})
}

// cleanupWorkflowTrustedSource restores traversal and unlink permission on real directories before deletion.
func cleanupWorkflowTrustedSource(root string) error {
	if err := restoreWorkflowTrustedSourceDirectories(root); err != nil {
		return fmt.Errorf("restore trusted workflow checkout for cleanup: %w", err)
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove trusted workflow checkout: %w", err)
	}
	return nil
}

func restoreWorkflowTrustedSourceDirectories(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil
		}
		return os.Chmod(path, 0o700)
	})
}

func materializeWorkflowAuthorityBundle(encoded string) (string, error) {
	payload, err := decodeWorkflowAuthorityBundle(encoded)
	if err != nil {
		return "", err
	}
	return extractWorkflowAuthorityBundle(payload)
}

func decodeWorkflowAuthorityBundle(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, errors.New("workflow authority bundle is required")
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(payload) == 0 || len(payload) > workflowAuthorityBundleMaxBytes {
		return nil, errors.New("workflow authority bundle must be bounded canonical base64")
	}
	return payload, nil
}

// extractWorkflowAuthorityBundle 将有界 gzip/tar authority 载荷解包到私有目录，并在任何失败时删除临时材料。
func extractWorkflowAuthorityBundle(payload []byte) (root string, err error) {
	root, err = os.MkdirTemp("", "super-dolphin-gate-authority.")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(root)
		}
	}()
	gzipReader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("open workflow authority bundle: %w", err)
	}
	defer gzipReader.Close()
	archive := tar.NewReader(io.LimitReader(gzipReader, workflowAuthorityBundleMaxBytes+1))
	expected := map[string]os.FileMode{
		workflowConfigTemplateFile: 0o600, "bootstrap-root.json": 0o600, "bootstrap-controller": 0o700,
		"bootstrap-controller-key.json": 0o600, "seccomp.json": 0o600, "promotion-private.key": 0o600,
		"receipt-private.json": 0o600, "action-grant-private.json": 0o600,
	}
	seen := make(map[string]bool, len(expected))
	if err := extractWorkflowAuthorityEntries(root, archive, expected, seen); err != nil {
		return "", err
	}
	if len(seen) != len(expected) {
		return "", errors.New("workflow authority bundle is missing required authority material")
	}
	return root, nil
}

func extractWorkflowAuthorityEntries(root string, archive *tar.Reader, expected map[string]os.FileMode, seen map[string]bool) error {
	for {
		header, done, err := nextWorkflowAuthorityHeader(archive)
		if err != nil {
			return err
		}
		if done {
			break
		}
		if err := extractWorkflowAuthorityEntry(root, archive, header, expected, seen); err != nil {
			return err
		}
	}
	return nil
}

func nextWorkflowAuthorityHeader(archive *tar.Reader) (*tar.Header, bool, error) {
	header, err := archive.Next()
	if errors.Is(err, io.EOF) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read workflow authority bundle: %w", err)
	}
	return header, false, nil
}

// extractWorkflowAuthorityEntry 只写入白名单中的单个 regular file，并校验去重、权限、长度和 O_EXCL 原子性。
func extractWorkflowAuthorityEntry(root string, archive *tar.Reader, header *tar.Header, expected map[string]os.FileMode, seen map[string]bool) error {
	mode, ok := expected[header.Name]
	if !ok || header.Typeflag != tar.TypeReg || seen[header.Name] || header.Size < 0 || header.Size > workflowAuthorityBundleMaxBytes {
		return errors.New("workflow authority bundle has an unsafe or unexpected entry")
	}
	seen[header.Name] = true
	file, err := os.OpenFile(filepath.Join(root, header.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(archive, workflowAuthorityBundleMaxBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != header.Size {
		return errors.Join(copyErr, closeErr, errors.New("workflow authority bundle entry has invalid length"))
	}
	return nil
}

func workflowOIDCAttestationDigest(ctx context.Context, options workflowHostOptions) (string, error) {
	return workflowOIDCAttestationDigestWithClient(ctx, options, &http.Client{Timeout: 20 * time.Second})
}

// workflowOIDCAttestationDigestWithClient 请求 GitHub OIDC token，并把 token、事件身份和 audience 绑定为稳定摘要。
func workflowOIDCAttestationDigestWithClient(
	ctx context.Context,
	options workflowHostOptions,
	client *http.Client,
) (string, error) {
	if client == nil {
		return "", errors.New("workflow OIDC HTTP client is required")
	}
	endpoint, audience, token, err := workflowOIDCEndpoint()
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request workflow OIDC token: %w", err)
	}
	defer response.Body.Close()
	jwt, err := workflowOIDCResponseToken(response)
	if err != nil {
		return "", err
	}
	payload := strings.Join([]string{options.eventRepository, options.eventRef, options.eventSHA, audience, jwt}, "\n")
	sum := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// workflowOIDCEndpoint 读取部署注入的 OIDC 参数，只接受 HTTPS endpoint 并附加受信 audience 查询参数。
func workflowOIDCEndpoint() (string, string, string, error) {
	audience, err := requiredWorkflowEnvironment(workflowOIDCAudienceEnv)
	if err != nil {
		return "", "", "", err
	}
	requestURL, err := requiredWorkflowEnvironment(workflowOIDCRequestURLEnv)
	if err != nil {
		return "", "", "", err
	}
	requestToken, err := requiredWorkflowEnvironment(workflowOIDCRequestTokenEnv)
	if err != nil {
		return "", "", "", err
	}
	endpoint, err := url.Parse(requestURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return "", "", "", errors.New("workflow OIDC request URL must be HTTPS")
	}
	query := endpoint.Query()
	query.Set("audience", audience)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), audience, requestToken, nil
}

func workflowOIDCResponseToken(response *http.Response) (string, error) {
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("workflow OIDC endpoint returned HTTP %d", response.StatusCode)
	}
	var token struct {
		Value string `json:"value"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&token); err != nil || strings.Count(token.Value, ".") != 2 || strings.TrimSpace(token.Value) != token.Value {
		return "", errors.New("workflow OIDC endpoint returned an invalid token")
	}
	return token.Value, nil
}

const (
	workflowAuthorityRoot        = "/super-dolphin-gate-authority"
	workflowConfigTemplateFile   = "workflow-config.template.json"
	workflowGeneratedConfigFile  = "workflow-config.json"
	workflowAuthorityPlaceholder = "@authority/"
	workflowRuntimePlaceholder   = "@runtime/"
	workflowRuntimeEnvironment   = "SUPER_DOLPHIN_GATE_WORKFLOW_RUNTIME_ROOT"
)

func requiredWorkflowEnvironment(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func prepareWorkflowProductionConfig(templatePath, authorityRoot, runtimeRoot, outputPath string) error {
	if err := validateWorkflowProductionConfigPaths(authorityRoot, runtimeRoot, outputPath); err != nil {
		return err
	}
	config, err := loadRelocatedWorkflowProductionConfig(templatePath, authorityRoot, runtimeRoot)
	if err != nil {
		return err
	}
	return writeWorkflowProductionConfig(outputPath, config)
}

func validateWorkflowProductionConfigPaths(authorityRoot, runtimeRoot, outputPath string) error {
	if _, err := canonicalProductionDirectory(authorityRoot); err != nil {
		return fmt.Errorf("validate workflow authority root: %w", err)
	}
	if _, err := canonicalProductionDirectory(runtimeRoot); err != nil {
		return fmt.Errorf("validate workflow runtime root: %w", err)
	}
	if outputPath != filepath.Join(runtimeRoot, workflowGeneratedConfigFile) {
		return errors.New("workflow production config must be generated inside the shared runtime root")
	}
	return nil
}

func loadRelocatedWorkflowProductionConfig(templatePath, authorityRoot, runtimeRoot string) (productionCoordinatorConfig, error) {
	data, err := readProductionCoordinatorConfig(templatePath)
	if err != nil {
		return productionCoordinatorConfig{}, fmt.Errorf("read workflow config template: %w", err)
	}
	var config productionCoordinatorConfig
	if err := decodeWorkflowConfigTemplate(data, &config); err != nil {
		return productionCoordinatorConfig{}, fmt.Errorf("decode workflow config template: %w", err)
	}
	if err := relocateWorkflowProductionConfig(&config, authorityRoot, runtimeRoot); err != nil {
		return productionCoordinatorConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return productionCoordinatorConfig{}, fmt.Errorf("validate relocated workflow config: %w", err)
	}
	return config, nil
}

func writeWorkflowProductionConfig(outputPath string, config productionCoordinatorConfig) error {
	if _, err := os.Lstat(outputPath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("workflow production config output already exists")
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode relocated workflow config: %w", err)
	}
	return createWorkflowProductionConfig(outputPath, append(encoded, '\n'))
}

func createWorkflowProductionConfig(outputPath string, payload []byte) error {
	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create workflow production config: %w", err)
	}
	written, writeErr := file.Write(payload)
	if writeErr != nil || written != len(payload) {
		return errors.Join(fmt.Errorf("write workflow production config: %w", errors.Join(writeErr, io.ErrShortWrite)), file.Close())
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close workflow production config: %w", err)
	}
	return nil
}

func decodeWorkflowConfigTemplate(data []byte, config *productionCoordinatorConfig) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(config); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errors.New("workflow config template has trailing JSON")
	}
	return nil
}

func relocateWorkflowProductionConfig(config *productionCoordinatorConfig, authorityRoot, runtimeRoot string) error {
	if config == nil {
		return errors.New("workflow production config is required")
	}
	if err := relocateWorkflowRuntimePaths(config, runtimeRoot); err != nil {
		return err
	}
	if err := prepareWorkflowCoordinatorRuntimeRoots(config.TrustedSourceRoot); err != nil {
		return err
	}
	if err := relocateWorkflowTrustedRepository(config, runtimeRoot); err != nil {
		return err
	}
	return relocateWorkflowAuthorityPaths(config, authorityRoot)
}

func relocateWorkflowRuntimePaths(config *productionCoordinatorConfig, runtimeRoot string) error {
	runtimePaths := []struct {
		name      string
		value     *string
		directory string
	}{
		{name: "accepted image root", value: &config.AcceptedImageRoot, directory: "accepted"},
		{name: "candidate state root", value: &config.CandidateStateRoot, directory: "candidate-state"},
		{name: "candidate build root", value: &config.CandidateBuildRoot, directory: "candidate-build"},
		{name: "trusted source root", value: &config.TrustedSourceRoot, directory: "trusted-source"},
	}
	for _, path := range runtimePaths {
		if err := relocateWorkflowRuntimePath(path.name, path.value, path.directory, runtimeRoot); err != nil {
			return err
		}
	}
	return nil
}

func relocateWorkflowRuntimePath(name string, value *string, directory, runtimeRoot string) error {
	if *value != workflowRuntimePlaceholder+directory {
		return fmt.Errorf("workflow template %s must be %q", name, workflowRuntimePlaceholder+directory)
	}
	target := filepath.Join(runtimeRoot, directory)
	if err := os.Mkdir(target, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create workflow %s: %w", name, err)
	}
	if _, err := canonicalProductionDirectory(target); err != nil {
		return fmt.Errorf("validate workflow %s: %w", name, err)
	}
	*value = target
	return nil
}

func relocateWorkflowTrustedRepository(config *productionCoordinatorConfig, runtimeRoot string) error {
	if config.TrustedRepository != workflowRuntimePlaceholder+"trusted.git" {
		return fmt.Errorf("workflow template trusted repository must be %q", workflowRuntimePlaceholder+"trusted.git")
	}
	config.TrustedRepository = filepath.Join(runtimeRoot, "trusted.git")
	if err := os.Mkdir(config.TrustedRepository, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create workflow trusted repository root: %w", err)
	}
	if _, err := canonicalProductionDirectory(config.TrustedRepository); err != nil {
		return fmt.Errorf("validate workflow trusted repository root: %w", err)
	}
	return nil
}

func relocateWorkflowAuthorityPaths(config *productionCoordinatorConfig, authorityRoot string) error {
	authorityPaths := []struct {
		name  string
		value *string
		file  string
	}{
		{name: "bootstrap root", value: &config.BootstrapRootFile, file: "bootstrap-root.json"},
		{name: "bootstrap controller", value: &config.BootstrapControllerFile, file: "bootstrap-controller"},
		{name: "bootstrap controller key", value: &config.BootstrapControllerKeyFile, file: "bootstrap-controller-key.json"},
		{name: "seccomp profile", value: &config.SeccompProfile, file: "seccomp.json"},
		{name: "promotion private key", value: &config.PromotionSigner.PrivateKeyFile, file: "promotion-private.key"},
		{name: "receipt private key", value: &config.ResultReceiptAuthority.PrivateKeyFile, file: "receipt-private.json"},
		{name: "action grant private key", value: &config.ActionGrantAuthority.PrivateKeyFile, file: "action-grant-private.json"},
	}
	for _, path := range authorityPaths {
		if *path.value != workflowAuthorityPlaceholder+path.file {
			return fmt.Errorf("workflow template %s must be %q", path.name, workflowAuthorityPlaceholder+path.file)
		}
		*path.value = filepath.Join(authorityRoot, path.file)
	}
	return nil
}

func prepareWorkflowCoordinatorRuntimeRoots(trustedSourceRoot string) error {
	for _, cacheRoot := range []string{
		filepath.Join(trustedSourceRoot, "cache"),
		filepath.Join(trustedSourceRoot, "home", "Library", "Caches"),
	} {
		runtimeRoot := filepath.Join(cacheRoot, "super-dolphin", "localci")
		if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
			return fmt.Errorf("create workflow coordinator runtime root: %w", err)
		}
		if _, err := canonicalProductionDirectory(runtimeRoot); err != nil {
			return fmt.Errorf("validate workflow coordinator runtime root: %w", err)
		}
	}
	return nil
}
