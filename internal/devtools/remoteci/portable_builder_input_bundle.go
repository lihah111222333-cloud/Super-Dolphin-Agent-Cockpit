package remoteci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const (
	// PortableBuilderInputBundleRef is the only ref advertised by an input bundle.
	PortableBuilderInputBundleRef = "refs/source/input"

	portableBuilderInputBundleTempPrefix      = ".portable-builder-input-bundle-"
	portableBuilderInputBundleCommandMaxBytes = 64 << 10
)

// PortableBuilderInputBundleRequest binds a source repository to one exact
// commit and tree. The repository is read-only input; no source ref is needed.
type PortableBuilderInputBundleRequest struct {
	RepositoryRoot string               `json:"repository_root"`
	CommitSHA      string               `json:"commit_sha"`
	TreeSHA        string               `json:"tree_sha"`
	ObjectFormat   gate.GitObjectFormat `json:"object_format"`
}

// PortableBuilderInputBundleMetadata is the digest and byte size of one bundle.
type PortableBuilderInputBundleMetadata struct {
	SHA256    string
	SizeBytes int64
}

// Validate 校验仓库路径、对象格式及提交树身份。
func (request PortableBuilderInputBundleRequest) Validate() error {
	if !validCanonicalPath(request.RepositoryRoot) {
		return errors.New("portable builder input repository root must be clean and absolute")
	}
	if err := validateCanonicalDirectory(request.RepositoryRoot, false); err != nil {
		return err
	}
	if request.ObjectFormat != gate.GitObjectFormatSHA1 && request.ObjectFormat != gate.GitObjectFormatSHA256 {
		return errors.New("portable builder input object format is invalid")
	}
	if !validOID(request.CommitSHA, request.ObjectFormat) || !validOID(request.TreeSHA, request.ObjectFormat) {
		return errors.New("portable builder input commit/tree identity is invalid")
	}
	return nil
}

// BuildPortableBuilderInputBundle 创建私有自包含 bundle 并返回稳定字节身份。
func BuildPortableBuilderInputBundle(ctx context.Context, request PortableBuilderInputBundleRequest, outputPath string) (metadata PortableBuilderInputBundleMetadata, err error) {
	if err := validatePortableBuilderContext(ctx); err != nil {
		return PortableBuilderInputBundleMetadata{}, err
	}
	if err := validatePortableBuilderOutputPath(outputPath); err != nil {
		return PortableBuilderInputBundleMetadata{}, err
	}
	return buildPortableBuilderInputBundleWithConsumer(ctx, request, func(stagePath string, want PortableBuilderInputBundleMetadata) error {
		return publishPortableBuilderInputBundle(ctx, stagePath, outputPath, want)
	})
}

// WritePortableBuilderInputBundle 将 bundle 写入调用方拥有且不关闭的 writer。
func WritePortableBuilderInputBundle(ctx context.Context, request PortableBuilderInputBundleRequest, destination io.Writer) (PortableBuilderInputBundleMetadata, error) {
	if err := validatePortableBuilderContext(ctx); err != nil {
		return PortableBuilderInputBundleMetadata{}, err
	}
	if interfaceValueIsNil(destination) {
		return PortableBuilderInputBundleMetadata{}, errors.New("portable builder input bundle writer is required")
	}
	return buildPortableBuilderInputBundleWithConsumer(ctx, request, func(stagePath string, want PortableBuilderInputBundleMetadata) error {
		got, err := copyPortableBuilderFile(ctx, stagePath, destination)
		if err != nil {
			return fmt.Errorf("write portable builder input bundle: %w", err)
		}
		if got != want.SizeBytes {
			return errors.New("portable builder input bundle writer accepted an unexpected size")
		}
		return nil
	})
}

// buildPortableBuilderInputBundleWithConsumer 校验请求、构建临时 bundle 并交给消费者。
func buildPortableBuilderInputBundleWithConsumer(ctx context.Context, request PortableBuilderInputBundleRequest, consume func(string, PortableBuilderInputBundleMetadata) error) (metadata PortableBuilderInputBundleMetadata, err error) {
	if err := request.Validate(); err != nil {
		return PortableBuilderInputBundleMetadata{}, err
	}
	if err := validatePortableBuilderContext(ctx); err != nil {
		return PortableBuilderInputBundleMetadata{}, err
	}
	if consume == nil {
		return PortableBuilderInputBundleMetadata{}, errors.New("portable builder input bundle consumer is required")
	}
	temporaryRoot, err := createPortableBuilderInputBundleTemp()
	if err != nil {
		return PortableBuilderInputBundleMetadata{}, err
	}
	defer func() {
		err = errors.Join(err, removePortableBuilderInputBundleTemp(temporaryRoot))
	}()

	stagePath, metadata, err := createPortableBuilderInputBundleStage(ctx, request, temporaryRoot)
	if err != nil {
		return PortableBuilderInputBundleMetadata{}, err
	}
	if err := consume(stagePath, metadata); err != nil {
		return PortableBuilderInputBundleMetadata{}, err
	}
	return metadata, nil
}

// createPortableBuilderInputBundleStage 创建、验证临时 bundle 并计算元数据。
func createPortableBuilderInputBundleStage(ctx context.Context, request PortableBuilderInputBundleRequest, temporaryRoot string) (string, PortableBuilderInputBundleMetadata, error) {
	inputRepository, err := createPortableBuilderInputBundleRepository(ctx, request, temporaryRoot)
	if err != nil {
		return "", PortableBuilderInputBundleMetadata{}, err
	}
	stagePath := filepath.Join(temporaryRoot, "input.bundle")
	if err := runPortableBuilderGitDiscard(ctx, inputRepository, nil, "bundle", "create", stagePath, "--end-of-options", PortableBuilderInputBundleRef); err != nil {
		return "", PortableBuilderInputBundleMetadata{}, fmt.Errorf("create input bundle: %w", err)
	}
	if err := os.Chmod(stagePath, 0o600); err != nil {
		return "", PortableBuilderInputBundleMetadata{}, fmt.Errorf("protect input bundle: %w", err)
	}
	if err := syncPortableBuilderInputBundle(stagePath); err != nil {
		return "", PortableBuilderInputBundleMetadata{}, err
	}
	if err := readSourceBaselineBuilderHeader(stagePath, request.ObjectFormat, PortableBuilderInputBundleRef, request.CommitSHA); err != nil {
		return "", PortableBuilderInputBundleMetadata{}, fmt.Errorf("validate input bundle header: %w", err)
	}
	if err := verifyPortableBuilderInputBundle(ctx, temporaryRoot, stagePath, request); err != nil {
		return "", PortableBuilderInputBundleMetadata{}, err
	}
	metadata, err := digestPortableBuilderInputBundle(ctx, stagePath)
	if err != nil {
		return "", PortableBuilderInputBundleMetadata{}, err
	}
	return stagePath, metadata, nil
}

// createPortableBuilderInputBundleRepository 准备私有仓库并安装唯一输入引用。
func createPortableBuilderInputBundleRepository(ctx context.Context, request PortableBuilderInputBundleRequest, temporaryRoot string) (string, error) {
	if err := validatePortableBuilderRepositoryInput(ctx, request); err != nil {
		return "", err
	}
	inputRepository := filepath.Join(temporaryRoot, "input.git")
	if err := runPortableBuilderGitDiscard(ctx, temporaryRoot, nil, "init", "-q", "--bare", "--object-format="+string(request.ObjectFormat), "--", inputRepository); err != nil {
		return "", fmt.Errorf("initialize private input repository: %w", err)
	}
	fetchArgs := []string{
		"-c", "protocol.file.allow=always", "fetch", "--no-tags", "--no-write-fetch-head", "--no-auto-gc", "--",
		request.RepositoryRoot, request.CommitSHA,
	}
	if err := runPortableBuilderGitDiscard(ctx, inputRepository, nil, fetchArgs...); err != nil {
		return "", fmt.Errorf("copy exact source commit locally: %w", err)
	}
	refInput := fmt.Sprintf("create %s %s\n", PortableBuilderInputBundleRef, request.CommitSHA)
	if err := runPortableBuilderGitDiscard(ctx, inputRepository, strings.NewReader(refInput), "update-ref", "--stdin"); err != nil {
		return "", fmt.Errorf("install input bundle ref: %w", err)
	}
	if err := validatePortableBuilderRef(ctx, inputRepository, request); err != nil {
		return "", err
	}
	return inputRepository, nil
}

func syncPortableBuilderInputBundle(stagePath string) error {
	file, err := os.OpenFile(stagePath, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open input bundle for fsync: %w", err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(syncErr, closeErr)
	}
	return nil
}

// validatePortableBuilderRepositoryInput 检查源仓库身份、历史完整性及提交树。
func validatePortableBuilderRepositoryInput(ctx context.Context, request PortableBuilderInputBundleRequest) error {
	objectFormatOutput, err := runPortableBuilderGitOutput(ctx, request.RepositoryRoot, nil, "rev-parse", "--show-object-format")
	if err != nil {
		return fmt.Errorf("read source repository object format: %w", err)
	}
	objectFormat, err := strictGitLine(objectFormatOutput)
	if err != nil || objectFormat != string(request.ObjectFormat) {
		return errors.New("source repository object format does not match the explicit input")
	}
	bareOutput, err := runPortableBuilderGitOutput(ctx, request.RepositoryRoot, nil, "rev-parse", "--is-bare-repository")
	if err != nil {
		return fmt.Errorf("read source repository kind: %w", err)
	}
	bare, err := strictGitLine(bareOutput)
	if err != nil {
		return fmt.Errorf("parse source repository kind: %w", err)
	}
	if err := validatePortableBuilderRepositoryRoot(ctx, request, bare); err != nil {
		return err
	}
	shallowOutput, err := runPortableBuilderGitOutput(ctx, request.RepositoryRoot, nil, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return fmt.Errorf("read source repository history mode: %w", err)
	}
	shallow, err := strictGitLine(shallowOutput)
	if err != nil || shallow != "false" {
		return errors.New("source repository must expose complete history")
	}
	return validatePortableBuilderCommitTree(ctx, request.RepositoryRoot, request)
}

func validatePortableBuilderRepositoryRoot(ctx context.Context, request PortableBuilderInputBundleRequest, bare string) error {
	if bare == "true" {
		return nil
	}
	topLevelOutput, err := runPortableBuilderGitOutput(ctx, request.RepositoryRoot, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("read source repository root: %w", err)
	}
	topLevel, err := strictGitLine(topLevelOutput)
	if err != nil || topLevel != request.RepositoryRoot {
		return errors.New("source repository root is not the explicit canonical root")
	}
	return nil
}

func validatePortableBuilderRef(ctx context.Context, repository string, request PortableBuilderInputBundleRequest) error {
	refs, err := runPortableBuilderGitOutput(ctx, repository, nil, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return fmt.Errorf("read input bundle refs: %w", err)
	}
	if string(refs) != request.CommitSHA+" "+PortableBuilderInputBundleRef+"\n" {
		return errors.New("input bundle source refs are not exactly one explicit ref")
	}
	return validatePortableBuilderCommitTree(ctx, repository, request)
}

func validatePortableBuilderCommitTree(ctx context.Context, repository string, request PortableBuilderInputBundleRequest) error {
	for _, object := range []struct {
		suffix string
		want   string
		name   string
	}{
		{suffix: "commit", want: request.CommitSHA, name: "commit"},
		{suffix: "tree", want: request.TreeSHA, name: "tree"},
	} {
		output, err := runPortableBuilderGitOutput(ctx, repository, nil, "rev-parse", "--verify", "--end-of-options", request.CommitSHA+"^{"+object.suffix+"}")
		if err != nil {
			return fmt.Errorf("resolve copied source %s: %w", object.name, err)
		}
		got, err := strictGitLine(output)
		if err != nil || got != object.want {
			return fmt.Errorf("copied source %s identity drifted", object.name)
		}
	}
	return nil
}

// verifyPortableBuilderInputBundle 在独立仓库中验证 bundle 的闭包和身份。
func verifyPortableBuilderInputBundle(ctx context.Context, temporaryRoot, bundlePath string, request PortableBuilderInputBundleRequest) error {
	verifyRepository := filepath.Join(temporaryRoot, "verify.git")
	if err := runPortableBuilderGitDiscard(ctx, temporaryRoot, nil, "init", "-q", "--bare", "--object-format="+string(request.ObjectFormat), "--", verifyRepository); err != nil {
		return fmt.Errorf("initialize input bundle verifier: %w", err)
	}
	if err := runPortableBuilderGitDiscard(ctx, verifyRepository, nil, "bundle", "verify", "--", bundlePath); err != nil {
		return fmt.Errorf("verify input bundle prerequisites: %w", err)
	}
	if err := runPortableBuilderGitDiscard(ctx, verifyRepository, nil, "bundle", "unbundle", "--", bundlePath); err != nil {
		return fmt.Errorf("import self-contained input bundle: %w", err)
	}
	refInput := fmt.Sprintf("create %s %s\n", PortableBuilderInputBundleRef, request.CommitSHA)
	if err := runPortableBuilderGitDiscard(ctx, verifyRepository, strings.NewReader(refInput), "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("install verifier input ref: %w", err)
	}
	if err := validatePortableBuilderRef(ctx, verifyRepository, request); err != nil {
		return fmt.Errorf("validate imported input bundle: %w", err)
	}
	fsckOutput, err := runPortableBuilderGitOutput(ctx, verifyRepository, nil, "fsck", "--full", "--strict", "--no-reflogs", "--unreachable", "--no-progress", PortableBuilderInputBundleRef)
	if err != nil {
		return fmt.Errorf("verify input bundle closure: %w", err)
	}
	if len(bytes.TrimSpace(fsckOutput)) != 0 {
		return errors.New("input bundle contains unreachable extra objects")
	}
	return nil
}

// publishPortableBuilderInputBundle 以创建保护方式写出 bundle 并复核身份。
func publishPortableBuilderInputBundle(ctx context.Context, stagePath, outputPath string, want PortableBuilderInputBundleMetadata) (err error) {
	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create input bundle output: %w", err)
	}
	created := true
	closed := false
	defer func() {
		err = errors.Join(err, cleanupPortableBuilderInputBundleOutput(file, outputPath, created, closed, err != nil))
	}()
	if err := os.Chmod(outputPath, 0o600); err != nil {
		return fmt.Errorf("protect input bundle output: %w", err)
	}
	if _, err := copyPortableBuilderFile(ctx, stagePath, file); err != nil {
		return fmt.Errorf("publish input bundle output: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync input bundle output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close input bundle output: %w", err)
	}
	closed = true
	actual, err := digestPortableBuilderInputBundle(ctx, outputPath)
	if err != nil {
		return err
	}
	if actual != want {
		return errors.New("published input bundle identity drifted")
	}
	created = false
	return nil
}

// cleanupPortableBuilderInputBundleOutput 关闭输出并清理失败时的部分文件。
func cleanupPortableBuilderInputBundleOutput(file *os.File, outputPath string, created, closed, failed bool) error {
	var cleanupErr error
	if !closed {
		if closeErr := file.Close(); closeErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close input bundle output: %w", closeErr))
		}
	}
	if failed && created {
		if removeErr := os.Remove(outputPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("cleanup partial input bundle output: %w", removeErr))
		}
	}
	return cleanupErr
}

func validatePortableBuilderOutputPath(outputPath string) error {
	if !validCanonicalPath(outputPath) {
		return errors.New("portable builder input bundle output must be clean and absolute")
	}
	parent := filepath.Dir(outputPath)
	if err := validateCanonicalDirectory(parent, false); err != nil {
		return fmt.Errorf("validate input bundle output directory: %w", err)
	}
	if _, err := os.Lstat(outputPath); err == nil {
		return errors.New("input bundle output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect input bundle output: %w", err)
	}
	return nil
}

// digestPortableBuilderInputBundle 读取 bundle 并计算受限大小内的 SHA-256。
func digestPortableBuilderInputBundle(ctx context.Context, path string) (PortableBuilderInputBundleMetadata, error) {
	if err := validatePortableBuilderContext(ctx); err != nil {
		return PortableBuilderInputBundleMetadata{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return PortableBuilderInputBundleMetadata{}, fmt.Errorf("open input bundle for digest: %w", err)
	}
	defer func() { _ = file.Close() }()
	digest, size, err := readPortableBuilderInputBundleDigest(ctx, file)
	if err != nil {
		return PortableBuilderInputBundleMetadata{}, err
	}
	if size <= 0 {
		return PortableBuilderInputBundleMetadata{}, errors.New("input bundle is empty")
	}
	return PortableBuilderInputBundleMetadata{SHA256: "sha256:" + hex.EncodeToString(digest), SizeBytes: size}, nil
}

// readPortableBuilderInputBundleDigest 在边界内读取输入并返回摘要与大小。
func readPortableBuilderInputBundleDigest(ctx context.Context, source io.Reader) ([]byte, int64, error) {
	hash := sha256.New()
	buffer := make([]byte, 128<<10)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			size += int64(count)
			if size > SourceBaselineBuilderArtifactMaxBytes {
				return nil, 0, errors.New("input bundle exceeds bounded size")
			}
			if _, err := hash.Write(buffer[:count]); err != nil {
				return nil, 0, fmt.Errorf("hash input bundle: %w", err)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, 0, fmt.Errorf("read input bundle: %w", readErr)
		}
	}
	return hash.Sum(nil), size, nil
}

// copyPortableBuilderFile 在上下文有效时复制 bundle，且不关闭调用方 writer。
func copyPortableBuilderFile(ctx context.Context, sourcePath string, destination io.Writer) (int64, error) {
	if err := validatePortableBuilderContext(ctx); err != nil {
		return 0, err
	}
	if interfaceValueIsNil(destination) {
		return 0, errors.New("portable builder input bundle destination is required")
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return 0, fmt.Errorf("open staged input bundle: %w", err)
	}
	defer func() { _ = source.Close() }()
	return copyPortableBuilderInputBundleBytes(ctx, source, destination)
}

// copyPortableBuilderInputBundleBytes 循环复制输入并检查短写与取消。
func copyPortableBuilderInputBundleBytes(ctx context.Context, source io.Reader, destination io.Writer) (int64, error) {
	buffer := make([]byte, 128<<10)
	var copied int64
	for {
		if err := ctx.Err(); err != nil {
			return copied, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			written, writeErr := destination.Write(buffer[:count])
			copied += int64(written)
			if writeErr != nil {
				return copied, writeErr
			}
			if written != count {
				return copied, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return copied, nil
			}
			return copied, readErr
		}
	}
}

func createPortableBuilderInputBundleTemp() (string, error) {
	root, err := os.MkdirTemp("", portableBuilderInputBundleTempPrefix)
	if err != nil {
		return "", fmt.Errorf("create private input bundle temporary root: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return "", fmt.Errorf("protect private input bundle temporary root: %w", err)
	}
	return root, nil
}

func removePortableBuilderInputBundleTemp(root string) error {
	if root == "" {
		return nil
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("cleanup private input bundle temporary root: %w", err)
	}
	return nil
}

func validatePortableBuilderContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("portable builder input bundle context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func runPortableBuilderGitOutput(ctx context.Context, repository string, stdin io.Reader, args ...string) ([]byte, error) {
	var stdout portableBuilderBoundedBuffer
	if err := runPortableBuilderGit(ctx, repository, stdin, &stdout, args...); err != nil {
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func runPortableBuilderGitDiscard(ctx context.Context, repository string, stdin io.Reader, args ...string) error {
	return runPortableBuilderGit(ctx, repository, stdin, io.Discard, args...)
}

// runPortableBuilderGit 执行受限 Git 命令并保留有界错误输出。
func runPortableBuilderGit(ctx context.Context, repository string, stdin io.Reader, stdout io.Writer, args ...string) error {
	if err := validatePortableBuilderGitInputs(ctx, repository, stdin, stdout, args); err != nil {
		return err
	}
	command, err := gateprivate.TrustedGitCommand(ctx, repository, args...)
	if err != nil {
		return err
	}
	if err := gate.ConfigureBoundedCommand(command); err != nil {
		return err
	}
	command.Env = sourceGitEnvironment()
	command.Stdin = stdin
	command.Stdout = stdout
	var stderr portableBuilderBoundedBuffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return portableBuilderGitRunError(ctx, args[0], err, stderr.String())
	}
	return nil
}

// validatePortableBuilderGitInputs 校验 Git 命令的上下文、路径、流和参数。
func validatePortableBuilderGitInputs(ctx context.Context, repository string, stdin io.Reader, stdout io.Writer, args []string) error {
	if err := validatePortableBuilderContext(ctx); err != nil {
		return err
	}
	if repository == "" || !filepath.IsAbs(repository) || len(args) == 0 || interfaceValueIsNil(stdout) {
		return errors.New("portable builder Git command inputs are invalid")
	}
	if stdin != nil && interfaceValueIsNil(stdin) {
		return errors.New("portable builder Git command stdin is typed nil")
	}
	return nil
}

func portableBuilderGitRunError(ctx context.Context, operation string, runErr error, stderr string) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("git %s: %w", operation, errors.Join(ctxErr, runErr))
	}
	message := strings.TrimSpace(stderr)
	if message == "" {
		return fmt.Errorf("git %s: %w", operation, runErr)
	}
	return fmt.Errorf("git %s: %w: %s", operation, runErr, message)
}

type portableBuilderBoundedBuffer struct {
	bytes.Buffer
}

// Write 限制 Git 输出大小并写入内嵌缓冲区。
func (buffer *portableBuilderBoundedBuffer) Write(data []byte) (int, error) {
	if int64(buffer.Len())+int64(len(data)) > portableBuilderInputBundleCommandMaxBytes {
		return 0, errors.New("portable builder Git command output exceeds bound")
	}
	return buffer.Buffer.Write(data)
}
