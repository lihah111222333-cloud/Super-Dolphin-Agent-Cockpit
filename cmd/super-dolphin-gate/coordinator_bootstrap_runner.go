package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const productionBootstrapRunnerMetadataFile = "/dev/shm/super-dolphin-bootstrap-metadata.json"

// runProductionBootstrapRunnerCLI 在受限容器中从 signed baseline 构建并发布 candidate。
func runProductionBootstrapRunnerCLI(args []string, stdout io.Writer) error {
	requestDigest, err := parseProductionBootstrapRunnerArgs(args)
	if err != nil {
		return err
	}
	request, err := loadProductionBootstrapRunnerRequest(requestDigest)
	if err != nil {
		return err
	}
	image, err := buildProductionBootstrapCandidate(context.Background(), request, requestDigest)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(productionBootstrapRunnerResult{
		SchemaVersion: productionBootstrapRunnerResultVersion, Image: image,
	})
}

// parseProductionBootstrapRunnerArgs 只接受 host verifier 固定的两项参数。
func parseProductionBootstrapRunnerArgs(args []string) (string, error) {
	if len(args) != 2 || args[0] != "--protocol-version=1" || !strings.HasPrefix(args[1], "--request-digest=") {
		return "", protocolError("bootstrap runner requires fixed protocol and request digest")
	}
	digest := strings.TrimPrefix(args[1], "--request-digest=")
	if err := validateProductionBootstrapDigest("runner request digest", digest); err != nil {
		return "", err
	}
	return digest, nil
}

// loadProductionBootstrapRunnerRequest 解码非秘密 request 并复核 argv digest。
func loadProductionBootstrapRunnerRequest(requestDigest string) (productionBootstrapRequest, error) {
	encoded := os.Getenv(productionBootstrapRunnerRequestEnv)
	data, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > productionBootstrapControllerMaxBytes {
		return productionBootstrapRequest{}, errors.New("production bootstrap runner request environment is invalid")
	}
	var request productionBootstrapRequest
	if err := gatecontract.DecodeStrictJSON(data, &request); err != nil {
		return request, fmt.Errorf("decode production bootstrap runner request: %w", err)
	}
	observed, err := productionBootstrapJSONDigest(request)
	if err != nil || observed != requestDigest {
		return request, errors.Join(errors.New("production bootstrap runner request digest drifted"), err)
	}
	return request, nil
}

// buildProductionBootstrapCandidate 通过 Docker Buildx 从固定 commit 构建并推送 immutable candidate。
func buildProductionBootstrapCandidate(
	ctx context.Context,
	request productionBootstrapRequest,
	requestDigest string,
) (gatecontract.ImageIdentity, error) {
	tag := request.CandidateRegistry + ":bootstrap-" + strings.TrimPrefix(requestDigest, "sha256:")[:16]
	contextURL := request.RemoteURL + "#" + request.BaselineCommit
	args := []string{
		"buildx", "build", "--push", "--provenance=false", "--network=none",
		"--platform=" + request.Platform, "--file=build/gate/Dockerfile", "--tag=" + tag,
		"--label=org.super-dolphin.policy-sha=" + request.PolicyDigest,
		"--label=org.super-dolphin.source-tree-sha=" + request.BaselineTree,
		"--label=org.super-dolphin.image-input-digest=" + request.ImageInputDigest,
		"--label=org.super-dolphin.toolchain-digest=" + request.ToolchainDigest,
		"--label=org.super-dolphin.schema-version=" + request.ImageSchemaVersion,
		"--metadata-file=" + productionBootstrapRunnerMetadataFile, contextURL,
	}
	if _, err := productionBootstrapRunnerDocker(ctx, args...); err != nil {
		return gatecontract.ImageIdentity{}, err
	}
	digest, err := readProductionBootstrapCandidateDigest()
	if err != nil {
		return gatecontract.ImageIdentity{}, err
	}
	reference := request.CandidateRegistry + "@" + digest
	if _, err := productionBootstrapRunnerDocker(ctx, "pull", "--platform="+request.Platform, reference); err != nil {
		return gatecontract.ImageIdentity{}, err
	}
	output, err := productionBootstrapRunnerDocker(ctx, "image", "inspect", reference)
	if err != nil {
		return gatecontract.ImageIdentity{}, err
	}
	document, err := decodeProductionBootstrapInspect(output)
	if err != nil {
		return gatecontract.ImageIdentity{}, err
	}
	return productionBootstrapCandidateIdentity(request.CandidateRegistry, digest, document)
}

// readProductionBootstrapCandidateDigest 读取 Buildx 固定 metadata 字段。
func readProductionBootstrapCandidateDigest() (string, error) {
	data, err := os.ReadFile(productionBootstrapRunnerMetadataFile)
	if err != nil {
		return "", err
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", err
	}
	var digest string
	if err := json.Unmarshal(metadata["containerimage.digest"], &digest); err != nil {
		return "", errors.New("production bootstrap candidate metadata digest is missing")
	}
	if err := validateProductionBootstrapDigest("candidate image digest", digest); err != nil {
		return "", err
	}
	return digest, nil
}

// productionBootstrapCandidateIdentity 从 Docker content store 提取完整 candidate identity。
func productionBootstrapCandidateIdentity(
	registry string,
	indexDigest string,
	document productionBootstrapImageInspect,
) (gatecontract.ImageIdentity, error) {
	if document.Descriptor == nil || document.RootFS == nil {
		return gatecontract.ImageIdentity{}, errors.New("production bootstrap candidate inspect is incomplete")
	}
	identity := gatecontract.ImageIdentity{
		Registry: registry, OCIIndexDigest: indexDigest, PlatformManifestDigest: document.Descriptor.Digest,
		ConfigDigest:  document.Descriptor.Annotations["config.digest"],
		RootFSDiffIDs: append([]string(nil), document.RootFS.Layers...),
		OS:            document.OS, Architecture: document.Architecture, Variant: document.Variant,
	}
	if err := identity.Validate(); err != nil {
		return identity, err
	}
	return identity, nil
}

// productionBootstrapRunnerDocker 调用容器内 Docker CLI，并限制输出规模。
func productionBootstrapRunnerDocker(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = []string{"HOME=/tmp", "PATH=" + os.Getenv("PATH"), "LC_ALL=C", "DOCKER_HOST=unix:///var/run/docker.sock"}
	output := &productionBootstrapLimitedBuffer{limit: productionBootstrapOutputMaxBytes}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("bootstrap runner docker %s: %w: %s", args[0], err, strings.TrimSpace(output.String()))
	}
	if output.exceeded {
		return nil, errors.New("production bootstrap runner Docker output exceeded limit")
	}
	return append([]byte(nil), output.Bytes()...), nil
}

// productionBootstrapRunnerProgram 判断当前 artifact 是否以 runner entrypoint 名运行。
func productionBootstrapRunnerProgram(path string) bool {
	return filepath.Base(path) == "super-dolphin-bootstrap"
}
