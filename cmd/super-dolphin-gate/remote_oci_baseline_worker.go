package main

import (
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
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	remoteOCIBuildRequestPathEnv = "SUPER_DOLPHIN_REMOTE_OCI_BUILD_REQUEST_PATH"
	remoteOCIBuildDeltaPathEnv   = "SUPER_DOLPHIN_REMOTE_OCI_BUILD_DELTA_PATH"
	remoteOCIBuildResultPrefix   = "SUPER_DOLPHIN_OCI_BASELINE_RESULT="
	remoteOCIBuildFileMax        = int64(64 << 20)
	remoteOCIBuildReceiptFile    = "refresh-build-receipt.json"
)

// runRemoteBuildOCIBaseline rebuilds its BuildKit context solely from the
// accepted source snapshot plus the bound delta staged by the init container.
func runRemoteBuildOCIBaseline(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return protocolError("_remote-build-oci-baseline does not accept arguments")
	}
	requestPath, deltaPath, err := remoteOCIWorkerPaths(os.LookupEnv)
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
	delta, err := readRemoteOCIWorkerFile(deltaPath, request.DeltaArchiveSize)
	if err != nil {
		return err
	}
	if int64(len(delta)) != request.DeltaArchiveSize || fmt.Sprintf("sha256:%x", sha256.Sum256(delta)) != request.DeltaArchiveSHA256 {
		return errors.New("remote OCI builder staged source snapshot delta identity drift")
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteBaselineRefreshDeadline)
	defer cancel()
	image, configDigest, observations, err := executeRemoteOCIBuildKit(ctx, request, delta)
	if err != nil {
		return err
	}
	result := remoteci.OCIBaselineBuilderResult{
		SchemaVersion: request.SchemaVersion, JobID: request.JobID, TransferMode: request.TransferMode,
		ParentGeneration: request.ParentGeneration, ParentStateSHA256: request.ParentStateSHA256,
		OutputRepository: request.OutputRepository, ParentImage: request.ParentImage,
		ParentImageCacheID: request.ParentImageCacheID, ParentImageSnapshotID: request.ParentImageSnapshotID,
		ParentSourceManifest: request.ParentSourceManifest, ParentSourceImagePath: request.ParentSourceImagePath,
		ParentSourceClosure: request.ParentSourceClosure, TargetCommit: request.TargetCommit,
		TargetTree: request.TargetTree, TargetSourceManifest: request.TargetSourceManifest,
		TargetSourceClosure: request.TargetSourceClosure, ImageInputDigest: request.ImageInputDigest,
		PolicyDigest: request.PolicyDigest, ToolchainDigest: request.ToolchainDigest, Platform: request.Platform,
		RuntimeDependencyDigest: request.RuntimeDependencyDigest, DeltaArchiveKey: request.DeltaArchiveKey,
		DeltaArchiveSHA256: request.DeltaArchiveSHA256, DeltaArchiveSize: request.DeltaArchiveSize,
		RefreshReceipts: observations, JobKey: request.JobKey, Repository: request.OutputRepository,
		Image: request.OutputRepository + "@" + image, ConfigDigest: configDigest,
	}
	encoded, _, err := remoteci.EncodeOCIBaselineBuilderResult(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, remoteOCIBuildResultPrefix+base64.StdEncoding.EncodeToString(encoded))
	return err
}

func remoteOCIWorkerPaths(getenv func(string) (string, bool)) (string, string, error) {
	request, requestOK := getenv(remoteOCIBuildRequestPathEnv)
	delta, deltaOK := getenv(remoteOCIBuildDeltaPathEnv)
	if !requestOK || !deltaOK || !remoteOCIWorkerPath(request) || !remoteOCIWorkerPath(delta) || request == delta {
		return "", "", errors.New("remote OCI worker staged request and delta paths are required")
	}
	return request, delta, nil
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

func executeRemoteOCIBuildKit(ctx context.Context, request remoteci.OCIBaselineBuilderRequest, delta []byte) (string, string, []cicontract.RefreshCheckObservation, error) {
	if err := cicontract.ValidateSourceSnapshotLayout(cicontract.SourceSnapshotRootPath, cicontract.SourceSnapshotManifestPath); err != nil {
		return "", "", nil, fmt.Errorf("validate source snapshot layout: %w", err)
	}
	if request.ParentSourceImagePath != cicontract.SourceSnapshotManifestPath {
		return "", "", nil, errors.New("accepted source snapshot manifest path does not match the fixed worker layout")
	}
	accepted, err := loadAcceptedSourceSnapshotManifest(request)
	if err != nil {
		return "", "", nil, err
	}
	workspace, err := os.MkdirTemp("", "remote-oci-build-")
	if err != nil {
		return "", "", nil, err
	}
	defer removeRemoteOCIWorkspace(workspace)
	if err := os.Chmod(workspace, 0o700); err != nil {
		return "", "", nil, fmt.Errorf("secure remote OCI BuildKit HOME: %w", err)
	}
	root := filepath.Join(workspace, "rebuild-root")
	if err := os.Mkdir(root, 0o700); err != nil {
		return "", "", nil, fmt.Errorf("create fresh source snapshot rebuild root: %w", err)
	}
	manifest, err := remoteci.ApplySourceSnapshotDelta(cicontract.SourceSnapshotRootPath, root, accepted, delta)
	if err != nil {
		return "", "", nil, fmt.Errorf("apply accepted source snapshot delta: %w", err)
	}
	if manifest.Target.SourceDigest != request.TargetSourceManifest || manifest.Target.TreeOID != request.TargetTree || manifest.Target.ClosureDigest != request.TargetSourceClosure || manifest.Target.InputDigest != request.ImageInputDigest || manifest.Target.PolicyDigest != request.PolicyDigest || manifest.Target.ToolchainDigest != request.ToolchainDigest || manifest.Target.Platform != request.Platform {
		return "", "", nil, errors.New("rebuilt source snapshot target identity does not match request")
	}
	if err := cicontract.ValidateDeltaRebuild(request.TransferMode, request.ParentGeneration, request.ParentImageSnapshotID, request.DeltaArchiveSHA256, manifest.Target.TreeOID, manifest.Target.ClosureDigest); err != nil {
		return "", "", nil, fmt.Errorf("validate delta rebuild: %w", err)
	}
	receiptDirectory := filepath.Join(workspace, "refresh-build-receipt")
	if err := os.Mkdir(receiptDirectory, 0o700); err != nil {
		return "", "", nil, fmt.Errorf("create refresh build receipt export directory: %w", err)
	}
	buildKit, err := startRemoteOCIBuildKit(ctx, workspace)
	if err != nil {
		return "", "", nil, err
	}
	defer buildKit.stop()
	buildArgs := remoteOCIBuildKitArgs(root, request)
	receiptArgs := append(append([]string{}, buildArgs...), "--opt=target=refresh-build-receipt", "--output=type=local,dest="+receiptDirectory)
	output, err := buildKit.run(ctx, receiptArgs)
	if err != nil {
		return "", "", nil, fmt.Errorf("export generator-owned refresh build receipt: %w: %s", err, strings.TrimSpace(string(output)))
	}
	receiptData, err := readRemoteOCIWorkerFile(filepath.Join(receiptDirectory, remoteOCIBuildReceiptFile), remoteOCIBuildFileMax)
	if err != nil {
		return "", "", nil, fmt.Errorf("read generator-owned refresh build receipt: %w", err)
	}
	observations, err := remoteci.DecodeOCIBuilderRefreshReceiptArtifact(receiptData, request)
	if err != nil {
		return "", "", nil, err
	}
	metadata := filepath.Join(workspace, "metadata.json")
	// Both solves use this exact daemon socket. BuildKit therefore reuses the
	// just-executed check layer in memory; no cache export/import can duplicate
	// the complete cache to the worker's temporary disk.
	imageArgs := append(append([]string{}, buildArgs...), "--metadata-file="+metadata, "--output=type=image,name="+request.OutputRepository+":baseline-"+strings.TrimPrefix(request.DeltaArchiveSHA256, "sha256:")+",push=true,oci-mediatypes=true")
	output, err = buildKit.run(ctx, imageArgs)
	if err != nil {
		return "", "", nil, fmt.Errorf("push OCI baseline image from refresh build receipt cache: %w: %s", err, strings.TrimSpace(string(output)))
	}
	data, err := os.ReadFile(metadata)
	if err != nil {
		return "", "", nil, err
	}
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return "", "", nil, err
	}
	image, config := values["containerimage.digest"], values["containerimage.config.digest"]
	if image == "" || config == "" {
		return "", "", nil, errors.New("ECI-local BuildKit metadata is incomplete")
	}
	return image, config, observations, nil
}

func remoteOCIBuildKitArgs(root string, request remoteci.OCIBaselineBuilderRequest) []string {
	return []string{"build", "--frontend=dockerfile.v0", "--local=context=" + root, "--local=dockerfile=" + root, "--opt=filename=build/gate/Dockerfile", "--opt=platform=" + cicontract.TargetPlatform, "--opt=network=none", "--opt=build-arg:RUNTIME_DEPS_IMAGE=" + request.ParentImage, "--opt=build-arg:BASELINE_CACHE_IMAGE=" + request.ParentImage, "--opt=build-arg:BUILD_SOURCE_TREE=" + request.TargetTree, "--opt=build-arg:ACCEPTED_SNAPSHOT_ID=" + request.ParentImageSnapshotID, "--opt=build-arg:IMAGE_INPUT_DIGEST=" + request.ImageInputDigest, "--opt=build-arg:POLICY_DIGEST=" + request.PolicyDigest, "--opt=build-arg:TOOLCHAIN_DIGEST=" + request.ToolchainDigest, "--opt=build-arg:TARGET_PLATFORM=" + cicontract.TargetPlatform}
}

type remoteOCIBuildKitSession struct {
	socket  string
	daemon  *exec.Cmd
	stderr  bytes.Buffer
	workdir string
}

// startRemoteOCIBuildKit keeps both solves in one ECI-local BuildKit daemon.
// The in-daemon cache is the only bridge from receipt export to final push.
func startRemoteOCIBuildKit(ctx context.Context, workspace string) (*remoteOCIBuildKitSession, error) {
	session := &remoteOCIBuildKitSession{socket: filepath.Join(workspace, "buildkitd.sock"), workdir: workspace}
	session.daemon = exec.CommandContext(ctx, "/usr/bin/buildkitd", "--addr", "unix://"+session.socket, "--root", filepath.Join(workspace, "buildkitd-root"))
	session.daemon.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + workspace}
	session.daemon.Stderr = &session.stderr
	if err := session.daemon.Start(); err != nil {
		return nil, fmt.Errorf("start ECI-local BuildKit daemon: %w", err)
	}
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := waitForRemoteOCIBuildKitSocket(readyCtx, session.socket); err != nil {
		session.stop()
		return nil, fmt.Errorf("wait for ECI-local BuildKit daemon: %w: %s", err, strings.TrimSpace(session.stderr.String()))
	}
	return session, nil
}

func waitForRemoteOCIBuildKitSocket(ctx context.Context, socket string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Lstat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (session *remoteOCIBuildKitSession) run(ctx context.Context, args []string) ([]byte, error) {
	command := exec.CommandContext(ctx, "/usr/bin/buildctl", append([]string{"--addr", "unix://" + session.socket}, args...)...)
	command.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + session.workdir}
	return command.CombinedOutput()
}

func (session *remoteOCIBuildKitSession) stop() {
	if session == nil || session.daemon == nil || session.daemon.Process == nil {
		return
	}
	_ = session.daemon.Process.Kill()
	_ = session.daemon.Wait()
}

func loadAcceptedSourceSnapshotManifest(request remoteci.OCIBaselineBuilderRequest) (remoteci.AcceptedSourceSnapshotManifest, error) {
	data, err := readRemoteOCIWorkerFile(cicontract.SourceSnapshotManifestPath, remoteOCIBuildFileMax)
	if err != nil {
		return remoteci.AcceptedSourceSnapshotManifest{}, fmt.Errorf("read accepted source snapshot manifest: %w", err)
	}
	if fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != request.ParentSourceManifest {
		return remoteci.AcceptedSourceSnapshotManifest{}, errors.New("accepted source snapshot manifest digest does not match request")
	}
	content, err := remoteci.DecodeSourceSnapshotContentManifest(data)
	if err != nil {
		return remoteci.AcceptedSourceSnapshotManifest{}, err
	}
	if content.ClosureDigest != request.ParentSourceClosure {
		return remoteci.AcceptedSourceSnapshotManifest{}, errors.New("accepted source snapshot closure does not match request")
	}
	authority := remoteci.SourceSnapshotAuthorityBinding{Generation: request.ParentGeneration, StateDigest: request.ParentStateSHA256, SnapshotID: request.ParentImageSnapshotID, SourceDigest: request.ParentSourceManifest}
	accepted, err := remoteci.NewAcceptedSourceSnapshotManifest(authority, content)
	if err != nil {
		return remoteci.AcceptedSourceSnapshotManifest{}, err
	}
	return accepted, nil
}

func removeRemoteOCIWorkspace(path string) { _ = os.RemoveAll(path) }
