//go:build windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

const runtimeServerWindowsGoplsBrokerHandshakeTimeout = 10 * time.Second

// validateRuntimeServerGoplsRootCohortPlatform 允许 Windows 生成 durable root cohort 配置。
func validateRuntimeServerGoplsRootCohortPlatform() error {
	return nil
}

// runtimeServerAcquireGoplsRootLease 获取 durable lease 并确保显式 TCP daemon 已就绪。
func runtimeServerAcquireGoplsRootLease(command multilsp.ServerCommand, serverBinary, dir string, env []string, controller multilsp.GoplsRootCohortController) (*multilsp.GoplsRootCohortLease, error) {
	if !runtimeServerUsesSharedGoplsDaemon(command) {
		return nil, nil
	}
	config, err := runtimeServerGoplsRootCohortConfig(command, serverBinary, dir, env)
	if err != nil {
		return nil, err
	}
	durable, ok := controller.(*runtimeServerDurableGoplsRootCohortController)
	if !ok || durable == nil {
		return nil, fmt.Errorf("%w for cohort %s", multilsp.ErrGoplsRootCohortDurabilityUnsupported, config.CohortID)
	}
	idle, err := runtimeServerWindowsGoplsDaemonIdleTimeout(command.Args)
	if err != nil {
		return nil, err
	}
	lease, err := durable.AcquireLease(config)
	if err != nil {
		return nil, err
	}
	daemon, err := runtimeServerEnsureWindowsGoplsDaemon(durable, config, serverBinary, dir, env, idle, runtimeServerLaunchWindowsGoplsDaemon)
	if err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	if err := runtimeServerAccountWindowsGoplsRootResource(durable, config, daemon); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	wrapped, err := runtimeServerWrapWindowsGoplsRootLease(durable, lease)
	if err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	return &wrapped, nil
}

// runtimeServerWrapWindowsGoplsRootLease 把所有释放路径绑定到零租约压力复核。
func runtimeServerWrapWindowsGoplsRootLease(controller *runtimeServerDurableGoplsRootCohortController, lease multilsp.GoplsRootCohortLease) (multilsp.GoplsRootCohortLease, error) {
	config, fence := lease.Config(), lease.Fence()
	rawLease := lease
	return multilsp.NewGoplsRootCohortLeaseFromAuthority(config, fence, func() error {
		if err := rawLease.Release(); err != nil {
			return err
		}
		return runtimeServerReclaimWindowsGoplsRootResourceAfterLease(controller, config, fence)
	})
}

// runtimeServerWindowsGoplsDaemonIdleTimeout 从唯一远端参数解析正数 daemon 空闲期限。
func runtimeServerWindowsGoplsDaemonIdleTimeout(args []string) (time.Duration, error) {
	const prefix = "-remote.listen.timeout="
	var value string
	for _, arg := range args {
		candidate, ok := strings.CutPrefix(arg, prefix)
		if !ok {
			continue
		}
		if value != "" {
			return 0, errors.New("Windows gopls daemon idle timeout is duplicated")
		}
		value = candidate
	}
	if value == "" {
		return 0, errors.New("Windows gopls daemon idle timeout is required")
	}
	idle, err := time.ParseDuration(value)
	if err != nil || idle <= 0 {
		return 0, errors.Join(err, errors.New("Windows gopls daemon idle timeout must be positive"))
	}
	return idle, nil
}

// runtimeServerLaunchWindowsGoplsDaemon 通过已验真的 self broker 启动并移交共享 daemon authority。
func runtimeServerLaunchWindowsGoplsDaemon(spec runtimeServerWindowsGoplsDaemonStartSpec) (runtimeServerWindowsGoplsDaemonProcess, error) {
	proof, err := runtimeServerValidateWindowsGoplsBrokerStartSpec(spec)
	if err != nil {
		return runtimeServerWindowsGoplsDaemonProcess{}, err
	}
	broker, err := hiddenexec.StartWindowsGoplsBrokerBootstrap()
	if err != nil {
		return runtimeServerWindowsGoplsDaemonProcess{}, err
	}
	requestWriter := broker.RequestWriter()
	response, err := runtimeServerExchangeWindowsGoplsBrokerStart(requestWriter, broker.ResponseReader(), spec, proof)
	if err != nil {
		return runtimeServerWindowsGoplsDaemonProcess{}, errors.Join(err, runtimeServerKillWindowsGoplsBroker(broker, requestWriter))
	}
	process := runtimeServerWindowsGoplsDaemonProcess{
		OwnerPID:              broker.PID,
		OwnerStartIdentity:    broker.StartIdentity,
		OwnerExecutablePath:   broker.ExecutablePath,
		OwnerSHA256:           broker.ImageSHA256,
		DaemonPID:             response.DaemonPID,
		DaemonStartIdentity:   response.DaemonStartIdentity,
		GoplsExecutablePath:   response.GoplsExecutablePath,
		GoplsSHA256:           response.GoplsSHA256,
		ObservationEndpoint:   response.ObservationEndpoint,
		ObservationCapability: response.ObservationCapability,
		ReclaimCapability:     response.ReclaimCapability,
		KillAndWait: func() error {
			return runtimeServerKillWindowsGoplsBroker(broker, requestWriter)
		},
		ReleaseAuthority: func() error {
			return runtimeServerCommitWindowsGoplsBroker(broker, requestWriter, spec.ConfigDigest)
		},
	}
	if err := runtimeServerValidateWindowsGoplsBrokerAuthority(process, spec, proof, response); err != nil {
		return runtimeServerWindowsGoplsDaemonProcess{}, errors.Join(err, process.KillAndWait())
	}
	return process, nil
}

// runtimeServerValidateWindowsGoplsBrokerStartSpec 校验父侧固定参数并重取 Windows bundle 信任证明。
func runtimeServerValidateWindowsGoplsBrokerStartSpec(spec runtimeServerWindowsGoplsDaemonStartSpec) (runtimeServerWindowsGoplsTrustProof, error) {
	idle := time.Duration(spec.IdleTimeoutNanos)
	if spec.ConfigDigest == "" || idle <= 0 {
		return runtimeServerWindowsGoplsTrustProof{}, errors.New("Windows gopls broker start identity is invalid")
	}
	if err := runtimeServerValidateWindowsGoplsDaemonEndpoint(spec.Endpoint); err != nil {
		return runtimeServerWindowsGoplsTrustProof{}, err
	}
	wantArgs := []string{"serve", "-listen=" + spec.Endpoint, "-listen.timeout=" + idle.String()}
	if !slices.Equal(spec.Args, wantArgs) {
		return runtimeServerWindowsGoplsTrustProof{}, errors.New("Windows gopls broker start arguments are invalid")
	}
	return runtimeServerTrustedWindowsGopls(spec.Binary)
}

// runtimeServerExchangeWindowsGoplsBrokerStart 写入唯一请求并保留父写端等待 durable commit。
func runtimeServerExchangeWindowsGoplsBrokerStart(writer io.Writer, reader io.ReadCloser, spec runtimeServerWindowsGoplsDaemonStartSpec, proof runtimeServerWindowsGoplsTrustProof) (runtimeServerWindowsGoplsBrokerResponse, error) {
	request := runtimeServerWindowsGoplsBrokerRequest{
		Schema:              runtimeServerWindowsGoplsBrokerSchema,
		ConfigDigest:        spec.ConfigDigest,
		Endpoint:            spec.Endpoint,
		WorkingDirectory:    spec.Directory,
		TrustedGoplsPath:    proof.Path,
		TrustedGoplsSHA256:  proof.SHA256,
		TrustedGoplsVersion: proof.Version,
		BundleRoot:          proof.BundleRoot,
		IdleTimeoutNanos:    spec.IdleTimeoutNanos,
		Env:                 append([]string{}, spec.Env...),
	}
	if err := runtimeServerWriteWindowsGoplsBrokerRequest(writer, request); err != nil {
		return runtimeServerWindowsGoplsBrokerResponse{}, err
	}
	return runtimeServerReadWindowsGoplsBrokerResponseWithTimeout(reader)
}

// runtimeServerWriteWindowsGoplsBrokerRequest 编码一个有界单行严格请求且保留父侧写端。
func runtimeServerWriteWindowsGoplsBrokerRequest(writer io.Writer, request runtimeServerWindowsGoplsBrokerRequest) error {
	return runtimeServerWriteWindowsGoplsBrokerFrame(writer, request, "request")
}

// runtimeServerCommitWindowsGoplsBroker 写入 durable commit、关闭父 pipe，最后释放精确权限。
func runtimeServerCommitWindowsGoplsBroker(broker *hiddenexec.WindowsGoplsBrokerBootstrapProcess, writer io.WriteCloser, configDigest string) error {
	commit := runtimeServerWindowsGoplsBrokerCommit{
		Schema:       runtimeServerWindowsGoplsBrokerSchema,
		ConfigDigest: configDigest,
		Commit:       true,
	}
	writeErr := runtimeServerWriteWindowsGoplsBrokerFrame(writer, commit, "commit")
	closeErr := runtimeServerCloseWindowsGoplsBrokerRequestWriter(writer)
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return broker.ReleaseAuthority()
}

// runtimeServerKillWindowsGoplsBroker 先关闭捕获的父 pipe，再通过启动句柄精确终止 broker。
func runtimeServerKillWindowsGoplsBroker(broker *hiddenexec.WindowsGoplsBrokerBootstrapProcess, writer io.WriteCloser) error {
	closeErr := runtimeServerCloseWindowsGoplsBrokerRequestWriter(writer)
	killErr := broker.KillAndWait()
	return errors.Join(closeErr, killErr)
}

// runtimeServerCloseWindowsGoplsBrokerRequestWriter 关闭父侧 commit pipe 并拒绝缺失权限。
func runtimeServerCloseWindowsGoplsBrokerRequestWriter(writer io.WriteCloser) error {
	if writer == nil {
		return errors.New("Windows gopls broker request writer is unavailable")
	}
	return writer.Close()
}

// runtimeServerReadWindowsGoplsBrokerResponseWithTimeout 用可停止 timer 关闭读端并等待回调收敛。
func runtimeServerReadWindowsGoplsBrokerResponseWithTimeout(reader io.ReadCloser) (runtimeServerWindowsGoplsBrokerResponse, error) {
	if reader == nil {
		return runtimeServerWindowsGoplsBrokerResponse{}, errors.New("Windows gopls broker response reader is unavailable")
	}
	timeoutDone := make(chan struct{})
	var timeoutCloseErr error
	timer := time.AfterFunc(runtimeServerWindowsGoplsBrokerHandshakeTimeout, func() {
		timeoutCloseErr = reader.Close()
		close(timeoutDone)
	})
	response, readErr := runtimeServerReadWindowsGoplsBrokerResponse(reader)
	if timer.Stop() {
		return response, errors.Join(readErr, reader.Close())
	}
	<-timeoutDone
	return response, errors.Join(errors.New("Windows gopls broker response timed out"), timeoutCloseErr, readErr)
}

// runtimeServerReadWindowsGoplsBrokerResponse 读取一个换行分帧且大小受限的严格 JSON 响应。
func runtimeServerReadWindowsGoplsBrokerResponse(reader io.Reader) (runtimeServerWindowsGoplsBrokerResponse, error) {
	var response runtimeServerWindowsGoplsBrokerResponse
	framed := bufio.NewReaderSize(reader, runtimeServerWindowsGoplsBrokerMaxPayloadSize+1)
	payload, err := runtimeServerReadWindowsGoplsBrokerFrame(framed, "response")
	if err != nil {
		return response, err
	}
	if err := runtimeServerDecodeWindowsGoplsBrokerFrame(payload, &response, "response"); err != nil {
		return response, err
	}
	return response, nil
}

// runtimeServerValidateWindowsGoplsBrokerAuthority 复核 broker 与 daemon 的分离身份及固定 gopls 证明。
func runtimeServerValidateWindowsGoplsBrokerAuthority(process runtimeServerWindowsGoplsDaemonProcess, spec runtimeServerWindowsGoplsDaemonStartSpec, proof runtimeServerWindowsGoplsTrustProof, response runtimeServerWindowsGoplsBrokerResponse) error {
	if response.SchemaVersion != runtimeServerWindowsGoplsBrokerSchema || response.Endpoint != spec.Endpoint {
		return errors.New("Windows gopls broker response identity is invalid")
	}
	if process.OwnerPID == process.DaemonPID || !runtimeServerWindowsGoplsProcessProofValid(process.OwnerPID, process.OwnerStartIdentity, process.OwnerExecutablePath, process.OwnerSHA256) {
		return errors.New("Windows gopls broker owner identity is invalid")
	}
	if !runtimeServerWindowsGoplsProcessProofValid(process.DaemonPID, process.DaemonStartIdentity, process.GoplsExecutablePath, process.GoplsSHA256) {
		return errors.New("Windows gopls broker daemon identity is invalid")
	}
	if !runtimeServerSameWindowsPath(process.GoplsExecutablePath, proof.Path) || process.GoplsSHA256 != proof.SHA256 {
		return errors.New("Windows gopls broker daemon proof does not match the trusted bundle")
	}
	if !runtimeServerWindowsGoplsBrokerResponseMapped(process, response) {
		return errors.New("Windows gopls broker observation response is not mapped")
	}
	binding := runtimeServerWindowsGoplsObservationBinding{
		ConfigDigest:        spec.ConfigDigest,
		OwnerPID:            process.OwnerPID,
		OwnerStartIdentity:  process.OwnerStartIdentity,
		DaemonPID:           process.DaemonPID,
		DaemonStartIdentity: process.DaemonStartIdentity,
	}
	return runtimeServerCheckWindowsGoplsObservationEndpoint(process.ObservationEndpoint, process.ObservationCapability, binding)
}

// runtimeServerWindowsGoplsBrokerResponseMapped 核对 observer 与独立回收 capability 的完整映射。
func runtimeServerWindowsGoplsBrokerResponseMapped(process runtimeServerWindowsGoplsDaemonProcess, response runtimeServerWindowsGoplsBrokerResponse) bool {
	return process.ObservationEndpoint == response.ObservationEndpoint &&
		process.ObservationCapability == response.ObservationCapability &&
		process.ReclaimCapability == response.ReclaimCapability &&
		runtimeServerWindowsSHA256Valid(response.ObservationCapability) &&
		runtimeServerWindowsSHA256Valid(response.ReclaimCapability) &&
		response.ObservationCapability != response.ReclaimCapability
}

// runtimeServerNewPlatformGoplsRootCohortController 创建共享 daemon 的 durable 生命周期 owner。
func runtimeServerNewPlatformGoplsRootCohortController(command multilsp.ServerCommand) (multilsp.GoplsRootCohortController, error) {
	if !runtimeServerUsesSharedGoplsDaemon(command) {
		return nil, nil
	}
	return runtimeServerNewDurableGoplsRootCohortController()
}
