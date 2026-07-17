package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

const (
	productionBootstrapProtocolVersion    uint32 = 1
	productionBootstrapControllerMaxBytes        = 128 << 20
	productionBootstrapOutputMaxBytes            = 4 << 20
	productionBootstrapStderrMaxBytes            = 64 << 10
	productionBootstrapContainerCPU              = int64(4_000_000_000)
	productionBootstrapContainerMemory           = int64(8 << 30)
	productionBootstrapLockPoll                  = 25 * time.Millisecond
)

const (
	productionBootstrapRootLabel    = "com.super-dolphin.bootstrap.root"
	productionBootstrapRepoLabel    = "com.super-dolphin.bootstrap.repo"
	productionBootstrapRequestLabel = "com.super-dolphin.bootstrap.request"
)

type productionBootstrapRequest struct {
	SchemaVersion      uint32                                `json:"schema_version"`
	Challenge          string                                `json:"challenge"`
	RootDigest         string                                `json:"root_digest"`
	RepoID             string                                `json:"repo_id"`
	RemoteURL          string                                `json:"remote_url"`
	TrustedRef         string                                `json:"trusted_ref"`
	ObjectFormat       gatecontract.GitObjectFormat          `json:"object_format"`
	BaselineCommit     string                                `json:"baseline_commit"`
	BaselineTree       string                                `json:"baseline_tree"`
	PolicyDigest       string                                `json:"policy_digest"`
	ImageInputDigest   string                                `json:"image_input_digest"`
	ToolchainDigest    string                                `json:"toolchain_digest"`
	ImageSchemaVersion string                                `json:"image_schema_version"`
	CandidateRegistry  string                                `json:"candidate_registry"`
	Platform           string                                `json:"platform"`
	Runner             gatecontract.ImageIdentity            `json:"runner"`
	Controller         productionBootstrapControllerIdentity `json:"controller"`
	BootstrapSigner    gatecontract.SignerIdentity           `json:"bootstrap_signer"`
	BootstrapPublicKey string                                `json:"bootstrap_ed25519_public_key"`
}

// Validate 为 controller 与 runner strict decoder 固定完整 request 形态。
func (request productionBootstrapRequest) Validate() error {
	if request.SchemaVersion != productionBootstrapProtocolVersion {
		return errors.New("production bootstrap request schema version is invalid")
	}
	challenge, err := base64.RawURLEncoding.DecodeString(request.Challenge)
	if err != nil || len(challenge) != 32 {
		return errors.New("production bootstrap request challenge is invalid")
	}
	for name, digest := range map[string]string{
		"root": request.RootDigest, "policy": request.PolicyDigest, "image input": request.ImageInputDigest,
		"toolchain": request.ToolchainDigest,
	} {
		if err := validateProductionBootstrapDigest(name+" request digest", digest); err != nil {
			return err
		}
	}
	return validateProductionBootstrapRequestIdentity(request)
}

// validateProductionBootstrapRequestIdentity 固定 request 的仓库、baseline 与 registry 身份。
func validateProductionBootstrapRequestIdentity(request productionBootstrapRequest) error {
	if strings.TrimSpace(request.RepoID) == "" || strings.TrimSpace(request.RepoID) != request.RepoID {
		return errors.New("production bootstrap request repo_id is invalid")
	}
	if err := validateProductionBootstrapRemoteURL(request.RemoteURL); err != nil {
		return err
	}
	if err := validateProductionBootstrapTrustedRef(request.TrustedRef); err != nil {
		return err
	}
	return validateProductionBootstrapRequestSource(request)
}

// validateProductionBootstrapRequestSource 固定请求中的 Git baseline 与候选镜像闭包。
func validateProductionBootstrapRequestSource(request productionBootstrapRequest) error {
	if request.ObjectFormat != gatecontract.GitObjectFormatSHA1 &&
		request.ObjectFormat != gatecontract.GitObjectFormatSHA256 {
		return errors.New("production bootstrap request object format is invalid")
	}
	if err := validateProductionBootstrapOID("request baseline_commit", request.BaselineCommit); err != nil {
		return err
	}
	if err := validateProductionBootstrapOID("request baseline_tree", request.BaselineTree); err != nil {
		return err
	}
	if err := validateProductionBootstrapCandidateRegistry(request.CandidateRegistry); err != nil {
		return err
	}
	if request.ImageSchemaVersion != "1" {
		return errors.New("production bootstrap request image schema version is invalid")
	}
	return validateProductionBootstrapRequestAuthority(request)
}

// validateProductionBootstrapRequestAuthority 固定 runner、controller 与执行签名身份。
func validateProductionBootstrapRequestAuthority(request productionBootstrapRequest) error {
	if err := request.Runner.Validate(); err != nil {
		return err
	}
	if err := validateAcceptedPlatform(request.Runner, request.Platform); err != nil {
		return err
	}
	if err := validateProductionBootstrapControllerIdentity(request.Controller); err != nil {
		return err
	}
	if request.Controller.Signer != request.BootstrapSigner {
		return errors.New("production bootstrap request controller signer drifted")
	}
	if _, err := decodeProductionBootstrapPublicKey(request.BootstrapPublicKey); err != nil {
		return err
	}
	return nil
}

type productionBootstrapAttestation struct {
	SchemaVersion          uint32                           `json:"schema_version"`
	Challenge              string                           `json:"challenge"`
	RootDigest             string                           `json:"root_digest"`
	RequestDigest          string                           `json:"request_digest"`
	ControllerDigest       string                           `json:"controller_digest"`
	Record                 gatecontract.AcceptedImageRecord `json:"record"`
	ContainerID            string                           `json:"container_id"`
	ContainerArgvDigest    string                           `json:"container_argv_digest"`
	ContainerLogDigest     string                           `json:"container_log_digest"`
	ContainerInspectDigest string                           `json:"container_inspect_digest"`
	StartedAt              time.Time                        `json:"started_at"`
	CompletedAt            time.Time                        `json:"completed_at"`
	Signature              string                           `json:"signature"`
}

// Validate 对 controller 返回的签名文档执行严格结构校验。
func (attestation productionBootstrapAttestation) Validate() error {
	return validateProductionBootstrapAttestationShape(attestation)
}

type productionBootstrapHostRuntime struct{}

type productionBootstrapFileLock struct {
	file *os.File
}

// bootstrapProductionAcceptedImage 在仓库外锁下串行化首次构建。
func bootstrapProductionAcceptedImage(
	ctx context.Context,
	config productionCoordinatorConfig,
	promotion *productionPromotionAuthority,
	runtime productionBootstrapRuntime,
) (record gatecontract.AcceptedImageRecord, retErr error) {
	if ctx == nil || promotion == nil || promotion.state == nil || promotion.authority == nil || runtime == nil {
		return record, errors.New("production bootstrap dependencies are required")
	}
	lock, err := acquireProductionBootstrapLock(ctx, config.AcceptedImageRoot)
	if err != nil {
		return record, err
	}
	defer func() { retErr = errors.Join(retErr, lock.close()) }()
	return bootstrapProductionAcceptedImageLocked(ctx, config, promotion, runtime)
}

// bootstrapProductionAcceptedImageLocked 在唯一赢家路径验证 root 并调用 controller。
func bootstrapProductionAcceptedImageLocked(
	ctx context.Context,
	config productionCoordinatorConfig,
	promotion *productionPromotionAuthority,
	runtime productionBootstrapRuntime,
) (record gatecontract.AcceptedImageRecord, retErr error) {
	var err error
	record, err = promotion.state.Load(ctx)
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, localci.ErrAcceptedImageStateNotFound) {
		return record, err
	}
	root, rootDigest, err := verifyProductionBootstrapPrerequisites(ctx, config, promotion.authority, runtime)
	if err != nil {
		return record, err
	}
	if err := runtime.CleanupStaleContainers(ctx, root, rootDigest); err != nil {
		return record, err
	}
	request, requestDigest, err := newProductionBootstrapRequest(config, root, rootDigest)
	if err != nil {
		return record, err
	}
	attestation, err := runtime.ExecuteController(ctx, config, root, request, requestDigest)
	if err != nil {
		return record, err
	}
	defer func() {
		retErr = errors.Join(retErr, runtime.CleanupStaleContainers(context.WithoutCancel(ctx), root, rootDigest))
	}()
	return commitProductionBootstrapAttestation(ctx, config, promotion, runtime, root, request, requestDigest, attestation)
}

// commitProductionBootstrapAttestation 完成镜像、容器证据验证后原子写入记录。
func commitProductionBootstrapAttestation(
	ctx context.Context,
	config productionCoordinatorConfig,
	promotion *productionPromotionAuthority,
	runtime productionBootstrapRuntime,
	root productionBootstrapRoot,
	request productionBootstrapRequest,
	requestDigest string,
	attestation productionBootstrapAttestation,
) (gatecontract.AcceptedImageRecord, error) {
	if err := verifyProductionBootstrapAttestation(config, root, request, requestDigest, attestation); err != nil {
		return gatecontract.AcceptedImageRecord{}, err
	}
	if err := runtime.VerifyRunner(ctx, attestation.Record.Image); err != nil {
		return gatecontract.AcceptedImageRecord{}, fmt.Errorf("verify production bootstrap candidate image: %w", err)
	}
	if err := runtime.VerifyAndRemoveContainer(ctx, config, root, request, attestation); err != nil {
		return gatecontract.AcceptedImageRecord{}, err
	}
	if err := promotion.state.Bootstrap(ctx, attestation.Record); err != nil {
		if !errors.Is(err, localci.ErrAcceptedImageStateExists) {
			return gatecontract.AcceptedImageRecord{}, err
		}
		return promotion.state.Load(ctx)
	}
	return promotion.state.Load(ctx)
}

func newProductionBootstrapRequest(
	config productionCoordinatorConfig,
	root productionBootstrapRoot,
	rootDigest string,
) (productionBootstrapRequest, string, error) {
	challengeBytes := make([]byte, 32)
	if _, err := rand.Read(challengeBytes); err != nil {
		return productionBootstrapRequest{}, "", fmt.Errorf("generate production bootstrap challenge: %w", err)
	}
	request := productionBootstrapRequest{
		SchemaVersion: productionBootstrapProtocolVersion,
		Challenge:     base64.RawURLEncoding.EncodeToString(challengeBytes), RootDigest: rootDigest,
		RepoID: root.RepoID, RemoteURL: root.RemoteURL, TrustedRef: root.TrustedRef,
		ObjectFormat:   root.ObjectFormat,
		BaselineCommit: root.BaselineCommit, BaselineTree: root.BaselineTree,
		PolicyDigest: root.PolicyDigest, ImageInputDigest: root.ImageInputDigest,
		ToolchainDigest: root.ToolchainDigest, ImageSchemaVersion: root.ImageSchemaVersion,
		CandidateRegistry: root.CandidateRegistry,
		Platform:          config.Platform, Runner: root.Runner, Controller: root.Controller,
		BootstrapSigner: root.BootstrapSigner, BootstrapPublicKey: root.BootstrapPublicKey,
	}
	digest, err := productionBootstrapJSONDigest(request)
	return request, digest, err
}

func productionBootstrapContainerArgv(requestDigest string) []string {
	return []string{
		"/usr/local/bin/super-dolphin-bootstrap",
		"--protocol-version=1",
		"--request-digest=" + requestDigest,
	}
}

func productionBootstrapContainerLabels(request productionBootstrapRequest, requestDigest string) map[string]string {
	return map[string]string{
		productionBootstrapRootLabel:    request.RootDigest,
		productionBootstrapRepoLabel:    request.RepoID,
		productionBootstrapRequestLabel: requestDigest,
	}
}

// verifyProductionBootstrapAttestation 绑定 challenge、argv、签名和 generation-one 记录。
func verifyProductionBootstrapAttestation(
	config productionCoordinatorConfig,
	root productionBootstrapRoot,
	request productionBootstrapRequest,
	requestDigest string,
	attestation productionBootstrapAttestation,
) error {
	if err := validateProductionBootstrapAttestationShape(attestation); err != nil {
		return err
	}
	if attestation.Challenge != request.Challenge || attestation.RootDigest != request.RootDigest ||
		attestation.RequestDigest != requestDigest || attestation.ControllerDigest != root.Controller.BinaryDigest {
		return errors.New("production bootstrap attestation identity drifted from request")
	}
	expectedArgvDigest, err := productionBootstrapJSONDigest(productionBootstrapContainerArgv(requestDigest))
	if err != nil {
		return err
	}
	if attestation.ContainerArgvDigest != expectedArgvDigest {
		return errors.New("production bootstrap container argv digest drifted")
	}
	if err := verifyProductionBootstrapAttestationSignature(root, attestation); err != nil {
		return err
	}
	return validateProductionBootstrapAcceptedRecord(config, root, attestation)
}

// validateProductionBootstrapAttestationShape 拒绝缺字段、非规范摘要和无效时间区间。
func validateProductionBootstrapAttestationShape(attestation productionBootstrapAttestation) error {
	if attestation.SchemaVersion != productionBootstrapProtocolVersion {
		return errors.New("production bootstrap attestation schema version is unsupported")
	}
	if strings.TrimSpace(attestation.ContainerID) == "" || strings.ContainsAny(attestation.ContainerID, "\x00\r\n") {
		return errors.New("production bootstrap attestation container ID is required")
	}
	if err := validateProductionBootstrapAttestationDigests(attestation); err != nil {
		return err
	}
	if err := validateProductionBootstrapAttestationTimes(attestation); err != nil {
		return err
	}
	_, err := decodeProductionBootstrapSignature(attestation.Signature)
	return err
}

func validateProductionBootstrapAttestationDigests(attestation productionBootstrapAttestation) error {
	for name, digest := range map[string]string{
		"root": attestation.RootDigest, "request": attestation.RequestDigest,
		"controller": attestation.ControllerDigest, "argv": attestation.ContainerArgvDigest,
		"log": attestation.ContainerLogDigest, "inspect": attestation.ContainerInspectDigest,
	} {
		if err := validateProductionBootstrapDigest(name+" attestation digest", digest); err != nil {
			return err
		}
	}
	return nil
}

// validateProductionBootstrapAttestationTimes 要求 UTC 且完成时间不早于开始时间。
func validateProductionBootstrapAttestationTimes(attestation productionBootstrapAttestation) error {
	if attestation.StartedAt.IsZero() || attestation.CompletedAt.IsZero() ||
		!attestation.StartedAt.Equal(attestation.StartedAt.UTC()) ||
		!attestation.CompletedAt.Equal(attestation.CompletedAt.UTC()) ||
		attestation.CompletedAt.Before(attestation.StartedAt) {
		return errors.New("production bootstrap attestation timestamps are invalid")
	}
	return nil
}

func verifyProductionBootstrapAttestationSignature(
	root productionBootstrapRoot,
	attestation productionBootstrapAttestation,
) error {
	publicKey, err := productionBootstrapExecutionPublicKey(root)
	if err != nil {
		return err
	}
	unsigned := attestation
	unsigned.Signature = ""
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("marshal production bootstrap attestation payload: %w", err)
	}
	signature, err := decodeProductionBootstrapSignature(attestation.Signature)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return errors.New("production bootstrap attestation Ed25519 signature verification failed")
	}
	return nil
}

// validateProductionBootstrapAcceptedRecord 验证 root 约束、runner 身份和真实记录签名。
func validateProductionBootstrapAcceptedRecord(
	config productionCoordinatorConfig,
	root productionBootstrapRoot,
	attestation productionBootstrapAttestation,
) error {
	record := attestation.Record
	if err := record.Validate(); err != nil {
		return fmt.Errorf("validate production bootstrap accepted record: %w", err)
	}
	if err := validateProductionBootstrapAcceptedBaseline(root, record); err != nil {
		return err
	}
	if err := validateProductionBootstrapAcceptedAuthority(root, attestation, record); err != nil {
		return err
	}
	if record.Image.Registry != root.CandidateRegistry {
		return errors.New("production bootstrap candidate registry drifted from signed root")
	}
	if err := validateAcceptedPlatform(record.Image, config.Platform); err != nil {
		return err
	}
	return verifyProductionBootstrapAcceptedSignature(config, record)
}

// validateProductionBootstrapAcceptedBaseline 逐字段对齐 root 固定的 Git 和输入闭包。
func validateProductionBootstrapAcceptedBaseline(
	root productionBootstrapRoot,
	record gatecontract.AcceptedImageRecord,
) error {
	if record.RepoID != root.RepoID || record.TrustedRef != root.TrustedRef ||
		record.TrustedCommit != root.BaselineCommit || record.SourceTree != root.BaselineTree ||
		record.PolicyDigest != root.PolicyDigest || record.ImageInputDigest != root.ImageInputDigest {
		return errors.New("production bootstrap accepted record drifted from signed baseline")
	}
	return nil
}

// validateProductionBootstrapAcceptedAuthority 固定 generation、runner、signer 和时间区间。
func validateProductionBootstrapAcceptedAuthority(
	root productionBootstrapRoot,
	attestation productionBootstrapAttestation,
	record gatecontract.AcceptedImageRecord,
) error {
	expectedRunner := gatecontract.TrustedRunnerIdentity{
		BinaryDigest: root.Controller.BinaryDigest, Signer: root.Controller.Signer, PolicyDigest: root.PolicyDigest,
	}
	if record.Runner != expectedRunner || record.Signer != root.BootstrapSigner || record.Generation != 1 || record.PreviousRecordDigest != "" {
		return errors.New("production bootstrap accepted authority identity is invalid")
	}
	if record.AcceptedAt.Before(attestation.StartedAt) || record.AcceptedAt.After(attestation.CompletedAt) {
		return errors.New("production bootstrap accepted timestamp is outside attested execution")
	}
	return nil
}

func verifyProductionBootstrapAcceptedSignature(
	config productionCoordinatorConfig,
	record gatecontract.AcceptedImageRecord,
) error {
	verifier, err := newProductionSignatureVerifier(config.AcceptedImageSigners)
	if err != nil {
		return err
	}
	payload, err := gatecontract.AcceptedImageSigningPayload(record)
	if err != nil {
		return err
	}
	return verifier.VerifyAcceptedImage(context.Background(), record.Signer, payload, record.Signature)
}

func productionBootstrapJSONDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal production bootstrap digest payload: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// acquireProductionBootstrapLock 以可取消轮询取得跨进程首次构建锁。
func acquireProductionBootstrapLock(ctx context.Context, root string) (*productionBootstrapFileLock, error) {
	if ctx == nil {
		return nil, errors.New("production bootstrap lock context is required")
	}
	path := filepath.Join(root, "bootstrap.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open production bootstrap lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &productionBootstrapFileLock{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("lock production bootstrap: %w", err), file.Close())
		}
		timer := time.NewTimer(productionBootstrapLockPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, errors.Join(ctx.Err(), file.Close())
		case <-timer.C:
		}
	}
}

func (lock *productionBootstrapFileLock) close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

// VerifyRunner 复用严格 OCI identity verifier，不启动容器。
func (productionBootstrapHostRuntime) VerifyRunner(ctx context.Context, identity gatecontract.ImageIdentity) error {
	return (productionDockerBootstrapRunnerVerifier{}).VerifyRunner(ctx, identity)
}

// ExecuteController 只运行已做摘要和 codesign 快照的仓库外 controller。
func (productionBootstrapHostRuntime) ExecuteController(
	ctx context.Context,
	config productionCoordinatorConfig,
	root productionBootstrapRoot,
	request productionBootstrapRequest,
	requestDigest string,
) (productionBootstrapAttestation, error) {
	rootDigest, err := productionBootstrapRootDigest(root, config.AcceptedImageSigners)
	if err != nil {
		return productionBootstrapAttestation{}, err
	}
	observedDigest, err := productionBootstrapJSONDigest(request)
	if err != nil {
		return productionBootstrapAttestation{}, err
	}
	if request.RootDigest != rootDigest || observedDigest != requestDigest {
		return productionBootstrapAttestation{}, errors.New("production bootstrap request digest changed before controller execution")
	}
	executable, cleanup, err := snapshotProductionBootstrapController(config, root.Controller)
	if err != nil {
		return productionBootstrapAttestation{}, err
	}
	defer cleanup()
	requestData, err := json.Marshal(request)
	if err != nil {
		return productionBootstrapAttestation{}, err
	}
	return runProductionBootstrapControllerCommand(ctx, executable, config.BootstrapControllerKeyFile, requestData)
}

// runProductionBootstrapControllerCommand 限长收集固定协议的 stdout/stderr。
func runProductionBootstrapControllerCommand(
	ctx context.Context,
	executable string,
	privateKeyFile string,
	requestData []byte,
) (productionBootstrapAttestation, error) {
	command := exec.CommandContext(ctx, executable, "bootstrap", "--protocol-version=1")
	command.Stdin = bytes.NewReader(append(requestData, '\n'))
	command.Env = []string{
		"HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH"), "LC_ALL=C",
		productionBootstrapControllerKeyEnv + "=" + privateKeyFile,
	}
	stdout := &productionBootstrapLimitedBuffer{limit: productionBootstrapOutputMaxBytes}
	stderr := &productionBootstrapLimitedBuffer{limit: productionBootstrapStderrMaxBytes}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		return productionBootstrapAttestation{}, fmt.Errorf("execute production bootstrap controller: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.exceeded || stderr.exceeded {
		return productionBootstrapAttestation{}, errors.New("production bootstrap controller output exceeded limit")
	}
	if strings.TrimSpace(stderr.String()) != "" {
		return productionBootstrapAttestation{}, errors.New("production bootstrap controller wrote stderr on success")
	}
	var attestation productionBootstrapAttestation
	if err := gatecontract.DecodeStrictJSON(stdout.Bytes(), &attestation); err != nil {
		return productionBootstrapAttestation{}, fmt.Errorf("decode production bootstrap controller attestation: %w", err)
	}
	return attestation, nil
}

type productionBootstrapLimitedBuffer struct {
	data     bytes.Buffer
	limit    int
	exceeded bool
}

// Write 限制受信进程失控时的宿主内存占用。
func (buffer *productionBootstrapLimitedBuffer) Write(data []byte) (int, error) {
	if buffer.data.Len()+len(data) > buffer.limit {
		remaining := buffer.limit - buffer.data.Len()
		if remaining > 0 {
			_, _ = buffer.data.Write(data[:remaining])
		}
		buffer.exceeded = true
		return len(data), nil
	}
	return buffer.data.Write(data)
}

// Bytes 返回已截获的 controller stdout。
func (buffer *productionBootstrapLimitedBuffer) Bytes() []byte { return buffer.data.Bytes() }

// String 返回已截获的 controller stderr。
func (buffer *productionBootstrapLimitedBuffer) String() string { return buffer.data.String() }

// snapshotProductionBootstrapController 固化验证后的只读可执行快照，关闭路径竞态窗口。
func snapshotProductionBootstrapController(
	config productionCoordinatorConfig,
	identity productionBootstrapControllerIdentity,
) (string, func(), error) {
	path, err := canonicalProductionExecutable("bootstrap controller", config.BootstrapControllerFile)
	if err != nil {
		return "", func() {}, err
	}
	data, err := readProductionBootstrapController(path)
	if err != nil {
		return "", func() {}, err
	}
	digest := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(digest[:]) != identity.BinaryDigest {
		return "", func() {}, errors.New("production bootstrap controller binary digest drifted")
	}
	if err := verifyProductionBootstrapCodeRequirement(path, identity.DesignatedRequirement); err != nil {
		return "", func() {}, err
	}
	snapshot, err := os.CreateTemp(config.AcceptedImageRoot, ".bootstrap-controller-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create production bootstrap controller snapshot: %w", err)
	}
	snapshotPath := snapshot.Name()
	cleanup := func() { _ = os.Remove(snapshotPath) }
	if err := writeProductionBootstrapControllerSnapshot(snapshot, data); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := verifyProductionBootstrapCodeRequirement(snapshotPath, identity.DesignatedRequirement); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return snapshotPath, cleanup, nil
}

// readProductionBootstrapController 复核 pathname、fd、owner 和文件大小后读取。
func readProductionBootstrapController(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	pathname, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || !os.SameFile(opened, pathname) || opened.Size() <= 0 ||
		opened.Size() > productionBootstrapControllerMaxBytes || !productionBootstrapOwnedByCurrentUID(opened) {
		return nil, errors.Join(errors.New("production bootstrap controller file identity is invalid"), statErr, lstatErr, file.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(file, productionBootstrapControllerMaxBytes+1))
	return data, errors.Join(readErr, file.Close())
}

func productionBootstrapOwnedByCurrentUID(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func writeProductionBootstrapControllerSnapshot(file *os.File, data []byte) error {
	if err := file.Chmod(0o500); err != nil {
		return errors.Join(err, file.Close())
	}
	written, err := file.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}

func verifyProductionBootstrapCodeRequirement(path string, requirement string) error {
	command := exec.Command("/usr/bin/codesign", "--verify", "--strict", "-R="+requirement, "--", path)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("verify production bootstrap controller code requirement: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// validateProductionBootstrapContainer 首先固定 runner manifest/config identity。
func validateProductionBootstrapContainer(
	config productionCoordinatorConfig,
	root productionBootstrapRoot,
	request productionBootstrapRequest,
	attestation productionBootstrapAttestation,
	document productionBootstrapContainerInspect,
) error {
	if document.ID != attestation.ContainerID || document.Config == nil || document.HostConfig == nil || document.State == nil {
		return errors.New("production bootstrap container identity is incomplete")
	}
	expectedReference := root.Runner.Registry + "@" + root.Runner.PlatformManifestDigest
	imageMatchesSignedDigest := document.Image == root.Runner.ConfigDigest ||
		document.Image == root.Runner.PlatformManifestDigest
	if document.Config.Image != expectedReference || !imageMatchesSignedDigest {
		return errors.New("production bootstrap container image identity drifted")
	}
	return validateProductionBootstrapContainerPolicy(config, request, attestation, document)
}

// validateProductionBootstrapContainerPolicy 校验 argv、labels、资源、网络和退出状态。
func validateProductionBootstrapContainerPolicy(
	config productionCoordinatorConfig,
	request productionBootstrapRequest,
	attestation productionBootstrapAttestation,
	document productionBootstrapContainerInspect,
) error {
	if document.ID != attestation.ContainerID || document.Config == nil || document.HostConfig == nil || document.State == nil {
		return errors.New("production bootstrap container policy evidence is incomplete")
	}
	if err := validateProductionBootstrapContainerArgv(attestation, document); err != nil {
		return err
	}
	if err := validateProductionBootstrapContainerLabels(request, attestation, document); err != nil {
		return err
	}
	if err := validateProductionBootstrapContainerEnvironment(request, document.Config.Env); err != nil {
		return err
	}
	if err := validateProductionBootstrapContainerHost(document.HostConfig); err != nil {
		return err
	}
	if err := validateProductionBootstrapContainerBinds(config, document.HostConfig.Binds); err != nil {
		return err
	}
	return validateProductionBootstrapContainerState(document)
}

func validateProductionBootstrapContainerEnvironment(
	request productionBootstrapRequest,
	environment []string,
) error {
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	expected := productionBootstrapRunnerRequestEnv + "=" + base64.RawStdEncoding.EncodeToString(data)
	for _, value := range environment {
		if value == expected {
			return nil
		}
		if strings.HasPrefix(value, "SUPER_DOLPHIN_BOOTSTRAP_") {
			return errors.New("production bootstrap container environment drifted")
		}
	}
	return errors.New("production bootstrap container request environment is missing")
}

func validateProductionBootstrapContainerArgv(
	attestation productionBootstrapAttestation,
	document productionBootstrapContainerInspect,
) error {
	expectedArgv := productionBootstrapContainerArgv(attestation.RequestDigest)
	observedArgv := append([]string{document.Path}, document.Args...)
	if !slices.Equal(observedArgv, expectedArgv) {
		return errors.New("production bootstrap container argv drifted")
	}
	return nil
}

func validateProductionBootstrapContainerLabels(
	request productionBootstrapRequest,
	attestation productionBootstrapAttestation,
	document productionBootstrapContainerInspect,
) error {
	for key, value := range productionBootstrapContainerLabels(request, attestation.RequestDigest) {
		if document.Config.Labels[key] != value {
			return errors.New("production bootstrap container labels drifted")
		}
	}
	return nil
}

// CleanupStaleContainers 在持有 bootstrap 锁时按 root/repo labels 清理 crash residue。
func (productionBootstrapHostRuntime) CleanupStaleContainers(
	ctx context.Context,
	root productionBootstrapRoot,
	rootDigest string,
) error {
	output, err := productionBootstrapDocker(
		ctx, "ps", "-aq", "--filter", "label="+productionBootstrapRootLabel+"="+rootDigest,
		"--filter", "label="+productionBootstrapRepoLabel+"="+root.RepoID,
	)
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		containerID := strings.TrimSpace(line)
		if containerID == "" {
			continue
		}
		if err := removeProductionBootstrapContainer(ctx, containerID); err != nil {
			return err
		}
	}
	return nil
}

func removeProductionBootstrapContainer(ctx context.Context, containerID string) error {
	if strings.TrimSpace(containerID) == "" {
		return nil
	}
	_, err := productionBootstrapDocker(ctx, "rm", "-f", containerID)
	return err
}

func productionBootstrapDocker(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = []string{"HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH"), "LC_ALL=C"}
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("production bootstrap docker %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
