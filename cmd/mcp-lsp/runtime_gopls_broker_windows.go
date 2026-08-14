//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

const (
	runtimeServerWindowsGoplsBrokerSchema         = 2
	runtimeServerWindowsGoplsBrokerMaxPayloadSize = 1 << 20
	runtimeServerWindowsGoplsBrokerCleanupTimeout = 5 * time.Second
	runtimeServerWindowsGoplsBrokerCommitTimeout  = 10 * time.Second
)

// runtimeServerWindowsGoplsBrokerRequest 是已验真 self broker 唯一接受的启动契约。
type runtimeServerWindowsGoplsBrokerRequest struct {
	Schema              int      `json:"schema"`
	ConfigDigest        string   `json:"config_digest"`
	Endpoint            string   `json:"endpoint"`
	WorkingDirectory    string   `json:"working_directory"`
	TrustedGoplsPath    string   `json:"trusted_gopls_path"`
	TrustedGoplsSHA256  string   `json:"trusted_gopls_sha256"`
	TrustedGoplsVersion string   `json:"trusted_gopls_version"`
	BundleRoot          string   `json:"bundle_root"`
	IdleTimeoutNanos    int64    `json:"idle_timeout_nanos"`
	Env                 []string `json:"env"`
}

// runtimeServerWindowsGoplsBrokerResponse 返回 broker Job 内 daemon 的可复核身份。
type runtimeServerWindowsGoplsBrokerResponse struct {
	SchemaVersion         int    `json:"schema_version"`
	Endpoint              string `json:"endpoint"`
	DaemonPID             int    `json:"daemon_pid"`
	DaemonStartIdentity   string `json:"daemon_start_identity"`
	GoplsExecutablePath   string `json:"gopls_executable_path"`
	GoplsSHA256           string `json:"gopls_sha256"`
	ObservationEndpoint   string `json:"observation_endpoint"`
	ObservationCapability string `json:"observation_capability"`
	ReclaimCapability     string `json:"reclaim_capability"`
}

// runtimeServerWindowsGoplsBrokerCommit 把父进程的 durable 发布结果绑定回原启动事务。
type runtimeServerWindowsGoplsBrokerCommit struct {
	Schema       int    `json:"schema"`
	ConfigDigest string `json:"config_digest"`
	Commit       bool   `json:"commit"`
}

// runWindowsGoplsBrokerIfRequested 在常规 runtime 初始化前分流已验真的 Windows broker child。
func runWindowsGoplsBrokerIfRequested(args []string) (bool, int) {
	return hiddenexec.RunWindowsGoplsBrokerBootstrapIfRequested(args, runWindowsGoplsBroker)
}

// runWindowsGoplsBroker 执行单个有界请求，并把任一失败转换为非零退出码。
func runWindowsGoplsBroker(input io.Reader, output io.Writer) int {
	if err := runtimeServerExecuteWindowsGoplsBroker(input, output); err != nil {
		if _, writeErr := io.WriteString(os.Stderr, "mcp-lsp Windows gopls broker failed: "+err.Error()+"\n"); writeErr != nil {
			return 1
		}
		return 1
	}
	return 0
}

// runtimeServerExecuteWindowsGoplsBroker 验证请求、启动固定 gopls 命令并持有 Job 至进程树退出。
func runtimeServerExecuteWindowsGoplsBroker(input io.Reader, output io.Writer) error {
	if input == nil {
		return errors.New("Windows gopls broker request reader is required")
	}
	inputCloser, ok := input.(io.Closer)
	if !ok {
		return errors.New("Windows gopls broker request pipe must be closable")
	}
	reader := bufio.NewReaderSize(input, runtimeServerWindowsGoplsBrokerMaxPayloadSize+1)
	request, err := runtimeServerReadWindowsGoplsBrokerRequest(reader)
	if err != nil {
		return err
	}
	proof, idle, err := runtimeServerValidateWindowsGoplsBrokerRequest(request)
	if err != nil {
		return err
	}
	command, tree, identity, err := runtimeServerStartWindowsGoplsBrokerDaemon(request, proof, idle)
	if err != nil {
		return err
	}
	ownerStartIdentity, err := hiddenexec.ProcessStartIdentity(os.Getpid())
	if err != nil {
		return errors.Join(err, runtimeServerAbortWindowsGoplsBrokerDaemon(command, tree))
	}
	observer, err := runtimeServerStartWindowsGoplsObservation(tree, request.Endpoint, runtimeServerWindowsGoplsObservationBinding{
		ConfigDigest:        request.ConfigDigest,
		OwnerPID:            os.Getpid(),
		OwnerStartIdentity:  ownerStartIdentity,
		DaemonPID:           identity.PID,
		DaemonStartIdentity: identity.StartToken,
	})
	if err != nil {
		return errors.Join(err, runtimeServerAbortWindowsGoplsBrokerDaemon(command, tree))
	}
	response := runtimeServerWindowsGoplsBrokerResponse{
		SchemaVersion:         runtimeServerWindowsGoplsBrokerSchema,
		Endpoint:              request.Endpoint,
		DaemonPID:             identity.PID,
		DaemonStartIdentity:   identity.StartToken,
		GoplsExecutablePath:   proof.Path,
		GoplsSHA256:           proof.SHA256,
		ObservationEndpoint:   observer.endpoint,
		ObservationCapability: observer.capability,
		ReclaimCapability:     observer.reclaimCapability,
	}
	if err := runtimeServerWriteWindowsGoplsBrokerResponse(output, response); err != nil {
		return errors.Join(err, runtimeServerAbortWindowsGoplsBrokerObservation(observer, command, tree))
	}
	if err := runtimeServerWaitWindowsGoplsBrokerCommit(reader, inputCloser, request.ConfigDigest); err != nil {
		return errors.Join(err, runtimeServerAbortWindowsGoplsBrokerObservation(observer, command, tree))
	}
	return runtimeServerWaitWindowsGoplsBrokerDaemon(command, tree, observer)
}

// runtimeServerReadWindowsGoplsBrokerRequest 有界读取一个单行 JSON 帧并拒绝未知字段与尾随载荷。
func runtimeServerReadWindowsGoplsBrokerRequest(reader *bufio.Reader) (runtimeServerWindowsGoplsBrokerRequest, error) {
	var request runtimeServerWindowsGoplsBrokerRequest
	payload, err := runtimeServerReadWindowsGoplsBrokerFrame(reader, "request")
	if err != nil {
		return request, err
	}
	if err := runtimeServerDecodeWindowsGoplsBrokerFrame(payload, &request, "request"); err != nil {
		return request, err
	}
	return request, nil
}

// runtimeServerWaitWindowsGoplsBrokerCommit 在短期限内等待同一父 pipe 的 durable commit。
func runtimeServerWaitWindowsGoplsBrokerCommit(reader *bufio.Reader, input io.Closer, configDigest string) error {
	if input == nil {
		return errors.New("Windows gopls broker commit pipe is unavailable")
	}
	timeoutDone := make(chan struct{})
	var timeoutCloseErr error
	timer := time.AfterFunc(runtimeServerWindowsGoplsBrokerCommitTimeout, func() {
		timeoutCloseErr = input.Close()
		close(timeoutDone)
	})
	readErr := runtimeServerReadWindowsGoplsBrokerCommit(reader, configDigest)
	if timer.Stop() {
		return readErr
	}
	<-timeoutDone
	return errors.Join(errors.New("Windows gopls broker commit timed out"), timeoutCloseErr, readErr)
}

// runtimeServerReadWindowsGoplsBrokerCommit 严格解码并核对 durable commit 与原配置摘要。
func runtimeServerReadWindowsGoplsBrokerCommit(reader *bufio.Reader, configDigest string) error {
	payload, err := runtimeServerReadWindowsGoplsBrokerFrame(reader, "commit")
	if err != nil {
		return err
	}
	var commit runtimeServerWindowsGoplsBrokerCommit
	if err := runtimeServerDecodeWindowsGoplsBrokerFrame(payload, &commit, "commit"); err != nil {
		return err
	}
	if commit.Schema != runtimeServerWindowsGoplsBrokerSchema || !commit.Commit || commit.ConfigDigest != configDigest {
		return errors.New("Windows gopls broker commit identity is invalid")
	}
	return nil
}

// runtimeServerReadWindowsGoplsBrokerFrame 读取一个换行终止且大小受限的 JSON 帧。
func runtimeServerReadWindowsGoplsBrokerFrame(reader *bufio.Reader, kind string) ([]byte, error) {
	if reader == nil {
		return nil, fmt.Errorf("Windows gopls broker %s reader is required", kind)
	}
	payload, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, fmt.Errorf("Windows gopls broker %s size is invalid", kind)
	}
	if err != nil {
		return nil, fmt.Errorf("read Windows gopls broker %s: %w", kind, err)
	}
	if len(payload) == 0 || len(payload) > runtimeServerWindowsGoplsBrokerMaxPayloadSize {
		return nil, fmt.Errorf("Windows gopls broker %s size is invalid", kind)
	}
	return payload, nil
}

// runtimeServerDecodeWindowsGoplsBrokerFrame 拒绝未知字段及同一行中的第二个 JSON 值。
func runtimeServerDecodeWindowsGoplsBrokerFrame(payload []byte, target any, kind string) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode Windows gopls broker %s: %w", kind, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("Windows gopls broker %s has trailing JSON payload", kind)
	}
	return nil
}

// runtimeServerValidateWindowsGoplsBrokerRequest 校验固定字段并重新取得包内 gopls 信任证明。
func runtimeServerValidateWindowsGoplsBrokerRequest(request runtimeServerWindowsGoplsBrokerRequest) (runtimeServerWindowsGoplsTrustProof, time.Duration, error) {
	idle, err := runtimeServerValidateWindowsGoplsBrokerRequestFields(request)
	if err != nil {
		return runtimeServerWindowsGoplsTrustProof{}, 0, err
	}
	proof, err := runtimeServerTrustedWindowsGopls(request.TrustedGoplsPath)
	if err != nil {
		return runtimeServerWindowsGoplsTrustProof{}, 0, err
	}
	if !runtimeServerWindowsGoplsBrokerProofMatches(request, proof) {
		return runtimeServerWindowsGoplsTrustProof{}, 0, errors.New("Windows gopls broker trust proof does not match the packaged executable")
	}
	return proof, idle, nil
}

// runtimeServerValidateWindowsGoplsBrokerRequestFields 校验不依赖文件读取的固定请求字段。
func runtimeServerValidateWindowsGoplsBrokerRequestFields(request runtimeServerWindowsGoplsBrokerRequest) (time.Duration, error) {
	if request.Schema != runtimeServerWindowsGoplsBrokerSchema {
		return 0, errors.New("Windows gopls broker request schema is invalid")
	}
	if request.ConfigDigest == "" || request.ConfigDigest != strings.TrimSpace(request.ConfigDigest) {
		return 0, errors.New("Windows gopls broker config digest is invalid")
	}
	if err := runtimeServerValidateWindowsGoplsDaemonEndpoint(request.Endpoint); err != nil {
		return 0, err
	}
	if !filepath.IsAbs(request.WorkingDirectory) || request.WorkingDirectory != strings.TrimSpace(request.WorkingDirectory) {
		return 0, errors.New("Windows gopls broker working directory must be absolute")
	}
	if request.IdleTimeoutNanos <= 0 || request.Env == nil {
		return 0, errors.New("Windows gopls broker idle timeout and environment are required")
	}
	return time.Duration(request.IdleTimeoutNanos), nil
}

// runtimeServerWindowsGoplsBrokerProofMatches 逐项比较请求声明与 broker 重读的信任证明。
func runtimeServerWindowsGoplsBrokerProofMatches(request runtimeServerWindowsGoplsBrokerRequest, proof runtimeServerWindowsGoplsTrustProof) bool {
	return runtimeServerSameWindowsPath(request.TrustedGoplsPath, proof.Path) &&
		request.TrustedGoplsSHA256 == proof.SHA256 &&
		request.TrustedGoplsVersion == proof.Version &&
		runtimeServerSameWindowsPath(request.BundleRoot, proof.BundleRoot)
}

// runtimeServerStartWindowsGoplsBrokerDaemon 用唯一允许的参数启动包内 gopls 并绑定 Job owner。
func runtimeServerStartWindowsGoplsBrokerDaemon(request runtimeServerWindowsGoplsBrokerRequest, proof runtimeServerWindowsGoplsTrustProof, idle time.Duration) (*exec.Cmd, *hiddenexec.ProcessTree, hiddenexec.ProcessIdentity, error) {
	args := []string{"serve", "-listen=" + request.Endpoint, "-listen.timeout=" + idle.String()}
	command := hiddenexec.Command(proof.Path, args...)
	command.Dir = request.WorkingDirectory
	command.Env = append(os.Environ(), request.Env...)
	command.Stderr = os.Stderr
	tree, err := hiddenexec.StartProcessTree(command)
	if err != nil {
		return nil, nil, hiddenexec.ProcessIdentity{}, errors.Join(err, runtimeServerAbortWindowsGoplsBrokerDaemon(command, tree))
	}
	identity, err := tree.Identity()
	if err != nil {
		return nil, nil, hiddenexec.ProcessIdentity{}, errors.Join(err, runtimeServerAbortWindowsGoplsBrokerDaemon(command, tree))
	}
	if err := runtimeServerVerifyWindowsGoplsBrokerDaemon(identity, proof); err != nil {
		return nil, nil, hiddenexec.ProcessIdentity{}, errors.Join(err, runtimeServerAbortWindowsGoplsBrokerDaemon(command, tree))
	}
	return command, tree, identity, nil
}

// runtimeServerVerifyWindowsGoplsBrokerDaemon 在响应前复核活进程的启动身份、镜像路径与摘要。
func runtimeServerVerifyWindowsGoplsBrokerDaemon(identity hiddenexec.ProcessIdentity, proof runtimeServerWindowsGoplsTrustProof) error {
	stale, err := runtimeServerCheckWindowsGoplsProcessIdentity("broker daemon", identity.PID, identity.StartToken, proof.Path, proof.SHA256)
	if err != nil {
		return err
	}
	if stale {
		return errors.New("Windows gopls broker daemon exited before identity verification")
	}
	return nil
}

// runtimeServerWriteWindowsGoplsBrokerResponse 有界编码唯一响应并检查短写。
func runtimeServerWriteWindowsGoplsBrokerResponse(output io.Writer, response runtimeServerWindowsGoplsBrokerResponse) error {
	return runtimeServerWriteWindowsGoplsBrokerFrame(output, response, "response")
}

// runtimeServerWriteWindowsGoplsBrokerFrame 编码一个有界单行 JSON 帧并检查短写。
func runtimeServerWriteWindowsGoplsBrokerFrame(output io.Writer, value any, kind string) error {
	if output == nil {
		return fmt.Errorf("Windows gopls broker %s writer is required", kind)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Windows gopls broker %s: %w", kind, err)
	}
	payload = append(payload, '\n')
	if len(payload) > runtimeServerWindowsGoplsBrokerMaxPayloadSize {
		return fmt.Errorf("Windows gopls broker %s size is invalid", kind)
	}
	written, err := output.Write(payload)
	if err != nil {
		return fmt.Errorf("write Windows gopls broker %s: %w", kind, err)
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}

// runtimeServerWaitWindowsGoplsBrokerDaemon 先回收根进程，再有界收敛 Job 后代并释放 owner。
func runtimeServerWaitWindowsGoplsBrokerDaemon(command *exec.Cmd, tree *hiddenexec.ProcessTree, observer *runtimeServerWindowsGoplsObservationServer) error {
	processErr := command.Wait()
	observerErr := observer.CloseAndWait()
	waitContext, cancelWait := context.WithTimeout(context.Background(), runtimeServerWindowsGoplsBrokerCleanupTimeout)
	waitErr := tree.Wait(waitContext)
	cancelWait()
	if waitErr == nil {
		return errors.Join(processErr, observerErr, tree.Release())
	}
	forceContext, cancelForce := context.WithTimeout(context.Background(), runtimeServerWindowsGoplsBrokerCleanupTimeout)
	forceErr := tree.Force(forceContext)
	cancelForce()
	retryContext, cancelRetry := context.WithTimeout(context.Background(), runtimeServerWindowsGoplsBrokerCleanupTimeout)
	retryErr := tree.Wait(retryContext)
	cancelRetry()
	if retryErr != nil {
		return errors.Join(processErr, observerErr, waitErr, forceErr, retryErr)
	}
	return errors.Join(processErr, observerErr, waitErr, forceErr, tree.Release())
}

// runtimeServerAbortWindowsGoplsBrokerObservation 先停止 observer，再终止并回收 provisional Job。
func runtimeServerAbortWindowsGoplsBrokerObservation(observer *runtimeServerWindowsGoplsObservationServer, command *exec.Cmd, tree *hiddenexec.ProcessTree) error {
	return errors.Join(observer.CloseAndWait(), runtimeServerAbortWindowsGoplsBrokerDaemon(command, tree))
}

// runtimeServerAbortWindowsGoplsBrokerDaemon 在启动事务失败时有界终止并释放仍可证明的 owner。
func runtimeServerAbortWindowsGoplsBrokerDaemon(command *exec.Cmd, tree *hiddenexec.ProcessTree) error {
	if tree == nil {
		return nil
	}
	terminateErr := tree.Terminate()
	waitContext, cancel := context.WithTimeout(context.Background(), runtimeServerWindowsGoplsBrokerCleanupTimeout)
	waitErr := tree.Wait(waitContext)
	cancel()
	if waitErr != nil {
		return errors.Join(terminateErr, waitErr)
	}
	var processErr error
	if command != nil && command.ProcessState == nil {
		processErr = command.Wait()
		if _, ok := errors.AsType[*exec.ExitError](processErr); ok {
			processErr = nil
		}
	}
	return errors.Join(terminateErr, processErr, tree.Release())
}
