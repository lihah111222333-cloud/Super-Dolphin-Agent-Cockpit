package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	remoteOCIBuildRequestPathEnv = "SUPER_DOLPHIN_REMOTE_OCI_BUILD_REQUEST_PATH"
	remoteOCIBuildContextPathEnv = "SUPER_DOLPHIN_REMOTE_OCI_BUILD_CONTEXT_PATH"
	remoteOCIBuildResultPrefix   = "SUPER_DOLPHIN_OCI_BASELINE_RESULT="
	remoteOCIBuildContextMax     = int64(4 << 30)
	remoteOCIBuildFileMax        = int64(64 << 20)
)

// runRemoteBuildOCIBaseline consumes only init-staged request/context files.
// It has no OSS client, Docker client, DataCache, seed, or source fallback.
func runRemoteBuildOCIBaseline(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return protocolError("_remote-build-oci-baseline does not accept arguments")
	}
	requestPath, contextPath, err := remoteOCIWorkerPaths(os.LookupEnv)
	if err != nil {
		return err
	}
	requestData, err := readRemoteOCIWorkerFile(requestPath, remoteOCIBuildFileMax)
	if err != nil {
		return err
	}
	request, err := remoteci.DecodeOCIBaselineBuilderRequest(requestData)
	if err != nil {
		return err
	}
	contextData, err := readRemoteOCIWorkerFile(contextPath, request.SourceArchiveSize)
	if err != nil {
		return err
	}
	if int64(len(contextData)) != request.SourceArchiveSize || fmt.Sprintf("sha256:%x", sha256.Sum256(contextData)) != request.ContextSHA256 {
		return errors.New("remote OCI builder staged context identity drift")
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteBaselineRefreshDeadline)
	defer cancel()
	image, configDigest, err := executeRemoteOCIBuildKit(ctx, request, contextData)
	if err != nil {
		return err
	}
	result := remoteci.OCIBaselineBuilderResult{SchemaVersion: remoteci.OCIBaselineBuilderResultSchemaVersion, JobID: request.JobID, ContextKey: request.ContextKey, ContextSHA256: request.ContextSHA256, RegistryRepository: request.RegistryRepository, ACRInstanceID: request.ACRInstanceID, ACRRegionID: request.ACRRegionID, ParentImage: request.ParentImage, MainCommit: request.MainCommit, MainTree: request.MainTree, ToolchainDigest: request.ToolchainDigest, Platform: request.Platform, RuntimeDependencyDigest: request.RuntimeDependencyDigest, JobKey: request.JobKey, Repository: request.RegistryRepository, Image: request.RegistryRepository + "@" + image, ConfigDigest: configDigest, InputDigest: request.ContextSHA256}
	encoded, _, err := remoteci.EncodeOCIBaselineBuilderResult(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, remoteOCIBuildResultPrefix+base64.StdEncoding.EncodeToString(encoded))
	return err
}

func remoteOCIWorkerPaths(getenv func(string) (string, bool)) (string, string, error) {
	request, ok1 := getenv(remoteOCIBuildRequestPathEnv)
	contextFile, ok2 := getenv(remoteOCIBuildContextPathEnv)
	if !ok1 || !ok2 || !remoteOCIWorkerPath(request) || !remoteOCIWorkerPath(contextFile) || request == contextFile {
		return "", "", errors.New("remote OCI worker staged request and context paths are required")
	}
	return request, contextFile, nil
}
func remoteOCIWorkerPath(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
func readRemoteOCIWorkerFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.Join(errors.New("remote OCI worker staged input is not a bounded physical regular file"), err)
	}
	return os.ReadFile(path)
}

func executeRemoteOCIBuildKit(ctx context.Context, request remoteci.OCIBaselineBuilderRequest, archive []byte) (string, string, error) {
	workspace, err := os.MkdirTemp("", "remote-oci-build-")
	if err != nil {
		return "", "", err
	}
	defer removeRemoteOCIWorkspace(workspace)
	if err := os.Chmod(workspace, 0o700); err != nil {
		return "", "", fmt.Errorf("secure remote OCI BuildKit HOME: %w", err)
	}
	acrCredentials, err := acquireACRPushCredentials(ctx, request)
	if err != nil {
		return "", "", err
	}
	if _, err := writeACRDockerConfig(workspace, acrCredentials); err != nil {
		return "", "", fmt.Errorf("write temporary ACR Docker auth: %w", err)
	}
	root := filepath.Join(workspace, "context")
	if err := unpackRemoteOCIContext(root, archive); err != nil {
		return "", "", err
	}
	metadata := filepath.Join(workspace, "metadata.json")
	args := []string{"build", "--frontend=dockerfile.v0", "--local=context=" + root, "--local=dockerfile=" + root, "--opt=filename=build/gate/Dockerfile", "--opt=platform=linux/amd64", "--opt=network=none", "--opt=build-arg:RUNTIME_DEPS_IMAGE=" + request.ParentImage, "--opt=build-arg:BASELINE_CACHE_IMAGE=" + request.ParentImage, "--opt=build-arg:BUILD_SOURCE_TREE=" + request.MainTree, "--opt=build-arg:TOOLCHAIN_DIGEST=" + request.ToolchainDigest, "--opt=build-arg:TARGET_PLATFORM=linux/amd64", "--metadata-file=" + metadata, "--output=type=image,name=" + request.RegistryRepository + ":baseline-" + strings.TrimPrefix(request.ContextSHA256, "sha256:") + ",push=true,oci-mediatypes=true"}
	command := exec.CommandContext(ctx, "/usr/bin/buildctl-daemonless.sh", args...)
	command.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + workspace}
	if output, err := command.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("run daemonless BuildKit: %w: %s", err, strings.TrimSpace(string(output)))
	}
	data, err := os.ReadFile(metadata)
	if err != nil {
		return "", "", err
	}
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return "", "", err
	}
	image, config := values["containerimage.digest"], values["containerimage.config.digest"]
	if image == "" || config == "" {
		return "", "", errors.New("daemonless BuildKit metadata is incomplete")
	}
	return image, config, nil
}

func removeRemoteOCIWorkspace(path string) { _ = os.RemoveAll(path) }

func unpackRemoteOCIContext(root string, archive []byte) error {
	if err := os.Mkdir(root, 0700); err != nil {
		return err
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	total := int64(0)
	seen := map[string]struct{}{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.ToSlash(header.Name)
		if name == "" || filepath.IsAbs(name) || filepath.Clean(name) != name || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
			return errors.New("remote OCI context tar contains escaping path")
		}
		if _, ok := seen[name]; ok {
			return errors.New("remote OCI context tar contains duplicate path")
		}
		seen[name] = struct{}{}
		target := filepath.Join(root, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > remoteOCIBuildFileMax || total+header.Size > remoteOCIBuildContextMax {
				return errors.New("remote OCI context tar exceeds limit")
			}
			total += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, io.LimitReader(reader, header.Size))
			closeErr := out.Close()
			if copyErr != nil || closeErr != nil {
				return errors.Join(copyErr, closeErr)
			}
		default:
			return errors.New("remote OCI context tar contains unsupported entry")
		}
	}
}
