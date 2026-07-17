package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	productionBootstrapControllerKeyEnv           = "SUPER_DOLPHIN_BOOTSTRAP_CONTROLLER_KEY_FILE"
	productionBootstrapRunnerRequestEnv           = "SUPER_DOLPHIN_BOOTSTRAP_REQUEST_B64"
	productionBootstrapRunnerResultVersion uint32 = 1
)

type productionBootstrapControllerPrivateKey struct {
	Signer     gatecontract.SignerIdentity `json:"signer"`
	PrivateKey string                      `json:"private_key"`
}

// Validate 校验 controller key 文件的 signer 与私钥编码。
func (key productionBootstrapControllerPrivateKey) Validate() error {
	if err := key.Signer.Validate(); err != nil {
		return fmt.Errorf("production bootstrap controller key signer: %w", err)
	}
	privateKey, err := base64.StdEncoding.DecodeString(key.PrivateKey)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("production bootstrap controller private key must be canonical base64 Ed25519")
	}
	return nil
}

type productionBootstrapRunnerResult struct {
	SchemaVersion uint32                     `json:"schema_version"`
	Image         gatecontract.ImageIdentity `json:"image"`
}

// Validate 校验 runner 只返回完整 immutable candidate identity。
func (result productionBootstrapRunnerResult) Validate() error {
	if result.SchemaVersion != productionBootstrapRunnerResultVersion {
		return errors.New("production bootstrap runner result schema version is invalid")
	}
	return result.Image.Validate()
}

// runProductionBootstrapControllerCLI 执行固定 controller 协议并只向 stdout 写 attestation。
func runProductionBootstrapControllerCLI(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) != 1 || args[0] != "--protocol-version=1" {
		return protocolError("bootstrap requires --protocol-version=1")
	}
	request, requestData, requestDigest, err := decodeProductionBootstrapControllerRequest(stdin)
	if err != nil {
		return err
	}
	privateKey, err := loadProductionBootstrapControllerKey(request)
	if err != nil {
		return err
	}
	attestation, err := executeProductionBootstrapController(
		context.Background(), request, requestData, requestDigest, privateKey,
	)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(attestation)
}

// decodeProductionBootstrapControllerRequest 严格读取 host 生成的单个 request。
func decodeProductionBootstrapControllerRequest(
	reader io.Reader,
) (productionBootstrapRequest, []byte, string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, productionBootstrapControllerMaxBytes+1))
	if err != nil || len(data) > productionBootstrapControllerMaxBytes {
		return productionBootstrapRequest{}, nil, "", errors.Join(errors.New("read production bootstrap request"), err)
	}
	var request productionBootstrapRequest
	if err := gatecontract.DecodeStrictJSON(data, &request); err != nil {
		return productionBootstrapRequest{}, nil, "", fmt.Errorf("decode production bootstrap request: %w", err)
	}
	digest, err := productionBootstrapJSONDigest(request)
	return request, bytes.TrimSpace(data), digest, err
}

// loadProductionBootstrapControllerKey 从 host 指定的 0600 文件读取本机 bootstrap key。
func loadProductionBootstrapControllerKey(request productionBootstrapRequest) (ed25519.PrivateKey, error) {
	path := os.Getenv(productionBootstrapControllerKeyEnv)
	canonical, err := canonicalProductionFile("bootstrap controller private key", path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		return nil, err
	}
	var key productionBootstrapControllerPrivateKey
	if err := gatecontract.DecodeStrictJSON(data, &key); err != nil {
		return nil, fmt.Errorf("decode production bootstrap controller key: %w", err)
	}
	if key.Signer != request.BootstrapSigner || key.Signer != request.Controller.Signer {
		return nil, errors.New("production bootstrap controller key signer drifted from signed request")
	}
	privateKeyData, _ := base64.StdEncoding.DecodeString(key.PrivateKey)
	privateKey := ed25519.PrivateKey(privateKeyData)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	if base64.StdEncoding.EncodeToString(publicKey) != request.BootstrapPublicKey {
		return nil, errors.New("production bootstrap controller private key does not match signed public key")
	}
	return ed25519.PrivateKey(append([]byte(nil), privateKey...)), nil
}

// executeProductionBootstrapController 构建 candidate 并生成 host 可复核的签名证据。
func executeProductionBootstrapController(
	ctx context.Context,
	request productionBootstrapRequest,
	requestData []byte,
	requestDigest string,
	privateKey ed25519.PrivateKey,
) (attestation productionBootstrapAttestation, retErr error) {
	started := time.Now().UTC().Truncate(time.Millisecond)
	containerID, err := createProductionBootstrapRunnerContainer(ctx, request, requestData, requestDigest)
	if err != nil {
		return attestation, err
	}
	keepForVerifier := false
	defer func() {
		if !keepForVerifier {
			retErr = errors.Join(retErr, removeProductionBootstrapContainer(context.WithoutCancel(ctx), containerID))
		}
	}()
	result, err := startProductionBootstrapRunnerContainer(ctx, containerID)
	if err != nil {
		return attestation, err
	}
	attestation, err = buildProductionBootstrapAttestation(
		ctx, request, requestDigest, containerID, result.Image, started, privateKey,
	)
	if err != nil {
		return productionBootstrapAttestation{}, err
	}
	keepForVerifier = true
	return attestation, nil
}

// createProductionBootstrapRunnerContainer 固定 runner identity、资源与唯一非源码挂载。
func createProductionBootstrapRunnerContainer(
	ctx context.Context,
	request productionBootstrapRequest,
	requestData []byte,
	requestDigest string,
) (string, error) {
	socket, err := productionBootstrapDockerSocket(ctx)
	if err != nil {
		return "", err
	}
	args := []string{
		"create", "--platform=" + request.Platform, "--network=bridge", "--read-only",
		"--cpus=4", "--memory=8g", "--cap-drop=ALL", "--security-opt=no-new-privileges:true",
		"--volume=" + socket + ":/var/run/docker.sock",
		"--env=" + productionBootstrapRunnerRequestEnv + "=" + base64.RawStdEncoding.EncodeToString(requestData),
	}
	for key, value := range productionBootstrapContainerLabels(request, requestDigest) {
		args = append(args, "--label="+key+"="+value)
	}
	args = append(args, request.Runner.Registry+"@"+request.Runner.PlatformManifestDigest)
	args = append(args, productionBootstrapContainerArgv(requestDigest)[1:]...)
	output, err := productionBootstrapDocker(ctx, args...)
	if err != nil {
		return "", err
	}
	containerID := strings.TrimSpace(string(output))
	if len(containerID) != 64 {
		return "", errors.New("production bootstrap Docker create returned invalid container ID")
	}
	return containerID, nil
}

// productionBootstrapDockerSocket 读取当前 Docker context 的 canonical Unix socket。
func productionBootstrapDockerSocket(ctx context.Context) (string, error) {
	output, err := productionBootstrapDocker(ctx, "context", "inspect", "--format={{.Endpoints.docker.Host}}")
	if err != nil {
		return "", err
	}
	host := strings.TrimSpace(string(output))
	path, ok := strings.CutPrefix(host, "unix://")
	if !ok || !filepath.IsAbs(path) || filepath.Base(path) != "docker.sock" {
		return "", errors.New("production bootstrap requires a canonical Docker Unix socket")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve production bootstrap Docker socket: %w", err)
	}
	return canonical, nil
}

// startProductionBootstrapRunnerContainer 启动 runner 并严格解析唯一 JSON result。
func startProductionBootstrapRunnerContainer(
	ctx context.Context,
	containerID string,
) (productionBootstrapRunnerResult, error) {
	output, err := productionBootstrapDocker(ctx, "start", "-a", containerID)
	if err != nil {
		return productionBootstrapRunnerResult{}, err
	}
	var result productionBootstrapRunnerResult
	if err := gatecontract.DecodeStrictJSON(output, &result); err != nil {
		return result, fmt.Errorf("decode production bootstrap runner result: %w", err)
	}
	if err := result.Validate(); err != nil {
		return result, err
	}
	return result, nil
}

// buildProductionBootstrapAttestation 绑定 candidate、container inspect/log 与真实 Ed25519 签名。
func buildProductionBootstrapAttestation(
	ctx context.Context,
	request productionBootstrapRequest,
	requestDigest string,
	containerID string,
	image gatecontract.ImageIdentity,
	started time.Time,
	privateKey ed25519.PrivateKey,
) (productionBootstrapAttestation, error) {
	record, err := signProductionBootstrapAcceptedRecord(request, image, started, privateKey)
	if err != nil {
		return productionBootstrapAttestation{}, err
	}
	logDigest, inspectDigest, err := productionBootstrapContainerEvidence(ctx, containerID)
	if err != nil {
		return productionBootstrapAttestation{}, err
	}
	argvDigest, err := productionBootstrapJSONDigest(productionBootstrapContainerArgv(requestDigest))
	if err != nil {
		return productionBootstrapAttestation{}, err
	}
	attestation := productionBootstrapAttestation{
		SchemaVersion: productionBootstrapProtocolVersion, Challenge: request.Challenge,
		RootDigest: request.RootDigest, RequestDigest: requestDigest,
		ControllerDigest: request.Controller.BinaryDigest, Record: record, ContainerID: containerID,
		ContainerArgvDigest: argvDigest, ContainerLogDigest: logDigest, ContainerInspectDigest: inspectDigest,
		StartedAt: started, CompletedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	data, err := json.Marshal(attestation)
	if err != nil {
		return attestation, err
	}
	attestation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, data))
	return attestation, nil
}

// signProductionBootstrapAcceptedRecord 签署 generation one candidate identity。
func signProductionBootstrapAcceptedRecord(
	request productionBootstrapRequest,
	image gatecontract.ImageIdentity,
	acceptedAt time.Time,
	privateKey ed25519.PrivateKey,
) (gatecontract.AcceptedImageRecord, error) {
	record := gatecontract.AcceptedImageRecord{
		SchemaVersion: gatecontract.AcceptedImageRecordSchemaVersion,
		RepoID:        request.RepoID, TrustedRef: request.TrustedRef, TrustedCommit: request.BaselineCommit,
		SourceTree: request.BaselineTree, PolicyDigest: request.PolicyDigest, ImageInputDigest: request.ImageInputDigest,
		Image: image, Runner: gatecontract.TrustedRunnerIdentity{
			BinaryDigest: request.Controller.BinaryDigest, Signer: request.BootstrapSigner, PolicyDigest: request.PolicyDigest,
		},
		Generation: 1, AcceptedAt: acceptedAt, Signer: request.BootstrapSigner,
	}
	payload, err := gatecontract.AcceptedImageSigningPayload(record)
	if err != nil {
		return record, err
	}
	record.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return record, nil
}

// productionBootstrapContainerEvidence 计算与 host verifier 相同的 canonical evidence digest。
func productionBootstrapContainerEvidence(ctx context.Context, containerID string) (string, string, error) {
	inspectData, err := productionBootstrapDocker(ctx, "inspect", containerID)
	if err != nil {
		return "", "", err
	}
	document, err := decodeProductionBootstrapContainerInspect(inspectData)
	if err != nil {
		return "", "", err
	}
	canonicalInspect, err := json.Marshal(document)
	if err != nil {
		return "", "", err
	}
	logs, err := productionBootstrapDocker(ctx, "logs", containerID)
	if err != nil {
		return "", "", err
	}
	logSum, inspectSum := sha256.Sum256(logs), sha256.Sum256(canonicalInspect)
	return "sha256:" + hex.EncodeToString(logSum[:]), "sha256:" + hex.EncodeToString(inspectSum[:]), nil
}
