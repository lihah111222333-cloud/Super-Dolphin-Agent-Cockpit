//go:build windows

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"golang.org/x/sys/windows"
)

const runtimeServerWindowsGoplsDaemonSchema = 4

type runtimeServerWindowsGoplsDaemonRecord struct {
	SchemaVersion         int    `json:"schema_version"`
	ConfigDigest          string `json:"config_digest"`
	Endpoint              string `json:"endpoint"`
	OwnerPID              int    `json:"owner_pid"`
	OwnerStartIdentity    string `json:"owner_start_identity"`
	OwnerExecutablePath   string `json:"owner_executable_path"`
	OwnerSHA256           string `json:"owner_sha256"`
	DaemonPID             int    `json:"daemon_pid"`
	DaemonStartIdentity   string `json:"daemon_start_identity"`
	GoplsExecutablePath   string `json:"gopls_executable_path"`
	GoplsSHA256           string `json:"gopls_sha256"`
	IdleTimeoutNanos      int64  `json:"idle_timeout_nanos"`
	ObservationEndpoint   string `json:"observation_endpoint"`
	ObservationCapability string `json:"observation_capability"`
	ReclaimCapability     string `json:"reclaim_capability"`
}
type runtimeServerWindowsGoplsDaemonEndpoint = runtimeServerWindowsGoplsDaemonRecord
type runtimeServerWindowsGoplsDaemonStartSpec struct {
	Directory, Binary, Endpoint string
	ConfigDigest                string
	IdleTimeoutNanos            int64
	Args, Env                   []string
}
type runtimeServerWindowsGoplsDaemonProcess struct {
	OwnerPID                                   int
	OwnerStartIdentity, OwnerExecutablePath    string
	OwnerSHA256                                string
	DaemonPID                                  int
	DaemonStartIdentity, GoplsExecutablePath   string
	GoplsSHA256                                string
	ObservationEndpoint, ObservationCapability string
	ReclaimCapability                          string
	KillAndWait, ReleaseAuthority              func() error
}
type runtimeServerWindowsGoplsDaemonStarter func(runtimeServerWindowsGoplsDaemonStartSpec) (runtimeServerWindowsGoplsDaemonProcess, error)

// 在同一 root durable 锁内复用或启动显式 TCP daemon。
func runtimeServerEnsureWindowsGoplsDaemon(controller *runtimeServerDurableGoplsRootCohortController, config multilsp.GoplsRootCohortConfig, binary, workingDir string, env []string, idle time.Duration, starter runtimeServerWindowsGoplsDaemonStarter) (runtimeServerWindowsGoplsDaemonEndpoint, error) {
	if err := runtimeServerValidateWindowsGoplsDaemonInput(controller, starter, binary, workingDir, idle); err != nil {
		return runtimeServerWindowsGoplsDaemonEndpoint{}, err
	}
	return runtimeServerDurableGoplsRootCohortWithStateLock(controller, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (runtimeServerWindowsGoplsDaemonEndpoint, error) {
		return runtimeServerEnsureWindowsGoplsDaemonLocked(dir, state, config, binary, workingDir, env, idle, starter)
	})
}

// 校验 daemon 启动所需的 durable owner、绝对工作目录和正数 idle 契约。
func runtimeServerValidateWindowsGoplsDaemonInput(controller *runtimeServerDurableGoplsRootCohortController, starter runtimeServerWindowsGoplsDaemonStarter, binary, workingDir string, idle time.Duration) error {
	if controller == nil || starter == nil || strings.TrimSpace(binary) == "" || !filepath.IsAbs(workingDir) || idle <= 0 {
		return errors.New("Windows gopls daemon startup input is invalid")
	}
	return nil
}

// 在已持有 durable 锁时复核旧记录，并只清理可证明 stale 的记录。
func runtimeServerEnsureWindowsGoplsDaemonLocked(dir string, state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig, binary, workingDir string, env []string, idle time.Duration, starter runtimeServerWindowsGoplsDaemonStarter) (runtimeServerWindowsGoplsDaemonEndpoint, error) {
	digest, err := runtimeServerWindowsGoplsDaemonStateDigest(state, config)
	if err != nil {
		return runtimeServerWindowsGoplsDaemonEndpoint{}, err
	}
	path := filepath.Join(dir, "daemon.json")
	record, err := runtimeServerReadWindowsGoplsDaemonRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return runtimeServerStartWindowsGoplsDaemon(path, digest, binary, workingDir, env, idle, starter)
	}
	if err != nil {
		return runtimeServerWindowsGoplsDaemonEndpoint{}, err
	}
	endpoint, stale, err := runtimeServerCheckWindowsGoplsDaemon(record, digest, idle)
	if err != nil || !stale {
		return endpoint, err
	}
	if err := runtimeServerRemoveWindowsGoplsDaemonRecord(dir, path); err != nil {
		return endpoint, err
	}
	return runtimeServerStartWindowsGoplsDaemon(path, digest, binary, workingDir, env, idle, starter)
}

// 从共享 cache root 的 durable record 复核已发布 endpoint。
func runtimeServerReadWindowsGoplsDaemonEndpoint(config multilsp.GoplsRootCohortConfig) (runtimeServerWindowsGoplsDaemonEndpoint, error) {
	root, err := runtimeServerCacheRoot()
	if err != nil {
		return runtimeServerWindowsGoplsDaemonEndpoint{}, err
	}
	return runtimeServerDurableGoplsRootCohortWithStateLock(&runtimeServerDurableGoplsRootCohortController{root: root}, config, func(dir string, state *runtimeServerDurableGoplsRootCohortState) (runtimeServerWindowsGoplsDaemonEndpoint, error) {
		digest, err := runtimeServerWindowsGoplsDaemonStateDigest(state, config)
		if err != nil {
			return runtimeServerWindowsGoplsDaemonEndpoint{}, err
		}
		path := filepath.Join(dir, "daemon.json")
		record, err := runtimeServerReadWindowsGoplsDaemonRecord(path)
		if err != nil {
			return runtimeServerWindowsGoplsDaemonEndpoint{}, err
		}
		endpoint, stale, err := runtimeServerCheckWindowsGoplsDaemon(record, digest, time.Duration(record.IdleTimeoutNanos))
		if err != nil || !stale {
			return endpoint, err
		}
		return endpoint, errors.Join(runtimeServerRemoveWindowsGoplsDaemonRecord(dir, path), errors.New("Windows gopls daemon record is stale"))
	})
}
func runtimeServerWindowsGoplsDaemonStateDigest(state *runtimeServerDurableGoplsRootCohortState, config multilsp.GoplsRootCohortConfig) (string, error) {
	if state == nil {
		return "", errors.New("Windows gopls daemon requires an acquired root lease")
	}
	stored, err := state.configValue()
	if err != nil || !storedEqualGoplsRootCohortConfig(stored, config) {
		return "", errors.Join(err, errors.New("Windows gopls daemon root cohort config mismatch"))
	}
	return state.ConfigDigest, nil
}

// 复用严格 JSON reader 并校验 daemon record 的必需身份字段。
func runtimeServerReadWindowsGoplsDaemonRecord(path string) (runtimeServerWindowsGoplsDaemonRecord, error) {
	var record runtimeServerWindowsGoplsDaemonRecord
	if err := runtimeServerReadGoplsRootCohortJSON(path, &record, 16*1024); err != nil {
		return record, err
	}
	if err := runtimeServerValidateWindowsGoplsDaemonRecord(record); err != nil {
		return record, errors.New("Windows gopls daemon record schema is invalid")
	}
	return record, nil
}

// 校验 schema v4 的 owner、daemon、服务 endpoint 与两类 capability 证据完整且可复核。
func runtimeServerValidateWindowsGoplsDaemonRecord(record runtimeServerWindowsGoplsDaemonRecord) error {
	baseValid := runtimeServerWindowsGoplsDaemonRecordBaseValid(record)
	ownerValid := runtimeServerWindowsGoplsProcessProofValid(record.OwnerPID, record.OwnerStartIdentity, record.OwnerExecutablePath, record.OwnerSHA256)
	daemonValid := runtimeServerWindowsGoplsProcessProofValid(record.DaemonPID, record.DaemonStartIdentity, record.GoplsExecutablePath, record.GoplsSHA256)
	if !baseValid || !ownerValid || !daemonValid || record.OwnerPID == record.DaemonPID {
		return errors.New("Windows gopls daemon record fields are incomplete")
	}
	if !runtimeServerWindowsSHA256Valid(record.ObservationCapability) ||
		!runtimeServerWindowsSHA256Valid(record.ReclaimCapability) || record.ObservationCapability == record.ReclaimCapability {
		return errors.New("Windows gopls daemon capabilities are invalid or not independent")
	}
	return errors.Join(
		runtimeServerValidateWindowsGoplsDaemonEndpoint(record.Endpoint),
		runtimeServerValidateWindowsGoplsDaemonEndpoint(record.ObservationEndpoint),
	)
}

// runtimeServerWindowsGoplsDaemonRecordBaseValid 校验 record 的非进程基础字段。
func runtimeServerWindowsGoplsDaemonRecordBaseValid(record runtimeServerWindowsGoplsDaemonRecord) bool {
	return record.SchemaVersion == runtimeServerWindowsGoplsDaemonSchema && record.ConfigDigest != "" && record.Endpoint != "" && record.IdleTimeoutNanos > 0
}

// runtimeServerWindowsGoplsProcessProofValid 校验一组持久化进程证明字段。
func runtimeServerWindowsGoplsProcessProofValid(pid int, startIdentity, executablePath, sha256Value string) bool {
	return pid > 1 && startIdentity != "" && filepath.IsAbs(executablePath) && runtimeServerWindowsSHA256Valid(sha256Value)
}

// runtimeServerWindowsSHA256Valid 只接受规范小写的完整 SHA-256 十六进制值。
func runtimeServerWindowsSHA256Valid(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

// 只把已证明死亡或 PID 复用的 record 判为可清理 stale。
func runtimeServerCheckWindowsGoplsDaemon(record runtimeServerWindowsGoplsDaemonRecord, digest string, idle time.Duration) (runtimeServerWindowsGoplsDaemonEndpoint, bool, error) {
	endpoint := record
	stale, err := runtimeServerCheckWindowsGoplsDaemonProcess(record)
	if err != nil || stale {
		return endpoint, stale, err
	}
	if record.ConfigDigest != digest || time.Duration(record.IdleTimeoutNanos) != idle {
		return endpoint, false, errors.New("live Windows gopls daemon config identity is uncertain")
	}
	binding := runtimeServerWindowsGoplsObservationBinding{
		ConfigDigest:        record.ConfigDigest,
		OwnerPID:            record.OwnerPID,
		OwnerStartIdentity:  record.OwnerStartIdentity,
		DaemonPID:           record.DaemonPID,
		DaemonStartIdentity: record.DaemonStartIdentity,
	}
	if err := runtimeServerCheckWindowsGoplsObservationEndpoint(record.ObservationEndpoint, record.ObservationCapability, binding); err != nil {
		return endpoint, false, fmt.Errorf("query live Windows gopls daemon observation: %w", err)
	}
	return endpoint, false, runtimeServerDialWindowsGoplsDaemon(record.Endpoint)
}

// 分别复核 broker owner 与 Job 内 daemon，任何不确定身份都拒绝复用。
func runtimeServerCheckWindowsGoplsDaemonProcess(record runtimeServerWindowsGoplsDaemonRecord) (bool, error) {
	stale, err := runtimeServerCheckWindowsGoplsProcessIdentity("broker owner", record.OwnerPID, record.OwnerStartIdentity, record.OwnerExecutablePath, record.OwnerSHA256)
	if err != nil || stale {
		return stale, err
	}
	return runtimeServerCheckWindowsGoplsProcessIdentity("daemon", record.DaemonPID, record.DaemonStartIdentity, record.GoplsExecutablePath, record.GoplsSHA256)
}

// 逐项复核一个进程的存活、启动 token、镜像路径和内容摘要。
func runtimeServerCheckWindowsGoplsProcessIdentity(label string, pid int, wantStart, wantPath, wantSHA256 string) (bool, error) {
	alive, err := hiddenexec.ProcessAlive(pid)
	if err != nil {
		return false, fmt.Errorf("inspect Windows gopls %s liveness: %w", label, err)
	}
	if !alive {
		return true, nil
	}
	start, err := hiddenexec.ProcessStartIdentity(pid)
	if err != nil {
		return false, fmt.Errorf("inspect Windows gopls %s start identity: %w", label, err)
	}
	if start != wantStart {
		return true, nil
	}
	image, err := runtimeServerWindowsProcessExecutablePath(pid)
	if err != nil || !strings.EqualFold(filepath.Clean(image), filepath.Clean(wantPath)) {
		return false, errors.Join(err, fmt.Errorf("live Windows gopls %s executable path is uncertain", label))
	}
	realPath, digest, err := runtimeServerBinaryIdentity(image, nil)
	if err != nil || !strings.EqualFold(filepath.Clean(realPath), filepath.Clean(wantPath)) || digest != wantSHA256 {
		return false, errors.Join(err, fmt.Errorf("live Windows gopls %s executable digest is uncertain", label))
	}
	return false, nil
}

// 通过受限查询句柄读取实际镜像路径，并把句柄关闭失败一并上报。
func runtimeServerWindowsProcessExecutablePath(pid int) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", fmt.Errorf("open live Windows gopls daemon: %w", err)
	}
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	imageErr := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size)
	image := windows.UTF16ToString(buffer[:size])
	closeErr := windows.CloseHandle(handle)
	if imageErr != nil || closeErr != nil {
		return "", errors.Join(imageErr, closeErr)
	}
	return image, nil
}

// 持有 provisional authority 至 ready、身份复核和原子发布全部成功。
func runtimeServerStartWindowsGoplsDaemon(path, digest, binary, workingDir string, env []string, idle time.Duration, starter runtimeServerWindowsGoplsDaemonStarter) (runtimeServerWindowsGoplsDaemonEndpoint, error) {
	var result runtimeServerWindowsGoplsDaemonEndpoint
	endpoint, err := runtimeServerReserveWindowsGoplsDaemonEndpoint()
	if err != nil {
		return result, err
	}
	process, err := starter(runtimeServerWindowsGoplsDaemonStartSpec{
		Directory:        workingDir,
		Binary:           binary,
		Endpoint:         endpoint,
		ConfigDigest:     digest,
		IdleTimeoutNanos: idle.Nanoseconds(),
		Args:             []string{"serve", "-listen=" + endpoint, "-listen.timeout=" + idle.String()},
		Env:              append([]string(nil), env...),
	})
	if err != nil {
		return result, err
	}
	if err := runtimeServerValidateWindowsGoplsDaemonAuthority(process); err != nil {
		return result, err
	}
	record := runtimeServerWindowsGoplsDaemonRecord{
		SchemaVersion:         runtimeServerWindowsGoplsDaemonSchema,
		ConfigDigest:          digest,
		Endpoint:              endpoint,
		OwnerPID:              process.OwnerPID,
		OwnerStartIdentity:    process.OwnerStartIdentity,
		OwnerExecutablePath:   filepath.Clean(process.OwnerExecutablePath),
		OwnerSHA256:           process.OwnerSHA256,
		DaemonPID:             process.DaemonPID,
		DaemonStartIdentity:   process.DaemonStartIdentity,
		GoplsExecutablePath:   filepath.Clean(process.GoplsExecutablePath),
		GoplsSHA256:           process.GoplsSHA256,
		IdleTimeoutNanos:      idle.Nanoseconds(),
		ObservationEndpoint:   process.ObservationEndpoint,
		ObservationCapability: process.ObservationCapability,
		ReclaimCapability:     process.ReclaimCapability,
	}
	if err := runtimeServerPublishWindowsGoplsDaemon(path, record, process); err != nil {
		return result, errors.Join(err, process.KillAndWait())
	}
	return record, nil
}

func runtimeServerReserveWindowsGoplsDaemonEndpoint() (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("reserve Windows gopls daemon endpoint: %w", err)
	}
	endpoint := "tcp;" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("release Windows gopls endpoint reservation: %w", err)
	}
	return endpoint, nil
}

// 校验 provisional process 提供完整身份和精确清理、移交能力。
func runtimeServerValidateWindowsGoplsDaemonAuthority(process runtimeServerWindowsGoplsDaemonProcess) error {
	ownerComplete := runtimeServerWindowsGoplsProcessProofValid(process.OwnerPID, process.OwnerStartIdentity, process.OwnerExecutablePath, process.OwnerSHA256)
	daemonComplete := runtimeServerWindowsGoplsProcessProofValid(process.DaemonPID, process.DaemonStartIdentity, process.GoplsExecutablePath, process.GoplsSHA256)
	observationComplete := runtimeServerValidateWindowsGoplsDaemonEndpoint(process.ObservationEndpoint) == nil &&
		runtimeServerWindowsSHA256Valid(process.ObservationCapability) &&
		runtimeServerWindowsSHA256Valid(process.ReclaimCapability) && process.ObservationCapability != process.ReclaimCapability
	complete := ownerComplete && daemonComplete && observationComplete && process.KillAndWait != nil && process.ReleaseAuthority != nil
	if complete {
		return nil
	}
	err := errors.New("Windows gopls daemon launcher returned incomplete authority")
	if process.KillAndWait != nil {
		return errors.Join(err, process.KillAndWait())
	}
	return err
}

// ready 与身份复核成功后才原子发布记录，最后移交 provisional authority。
func runtimeServerPublishWindowsGoplsDaemon(path string, record runtimeServerWindowsGoplsDaemonRecord, process runtimeServerWindowsGoplsDaemonProcess) error {
	if err := runtimeServerWaitWindowsGoplsDaemonReady(record.Endpoint); err != nil {
		return err
	}
	if _, stale, err := runtimeServerCheckWindowsGoplsDaemon(record, record.ConfigDigest, time.Duration(record.IdleTimeoutNanos)); err != nil || stale {
		return errors.Join(err, errors.New("started Windows gopls daemon identity is invalid"))
	}
	if err := runtimeServerWriteGoplsRootCohortJSON(path, record); err != nil {
		return err
	}
	if err := process.ReleaseAuthority(); err != nil {
		cleanupErr := runtimeServerRemoveWindowsGoplsDaemonRecord(filepath.Dir(path), path)
		return errors.Join(fmt.Errorf("release published Windows gopls daemon authority: %w", err), cleanupErr)
	}
	return nil
}

// 在有界期限内等待显式 loopback endpoint 可连接。
func runtimeServerWaitWindowsGoplsDaemonReady(endpoint string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := runtimeServerDialWindowsGoplsDaemon(endpoint); err == nil {
			return nil
		} else if time.Now().After(deadline) {
			return fmt.Errorf("Windows gopls daemon endpoint did not become ready: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 只允许规范的 IPv4 loopback TCP endpoint，并在复用前拨号探测。
func runtimeServerDialWindowsGoplsDaemon(endpoint string) error {
	if err := runtimeServerValidateWindowsGoplsDaemonEndpoint(endpoint); err != nil {
		return err
	}
	address, _ := strings.CutPrefix(endpoint, "tcp;")
	connection, err := net.DialTimeout("tcp4", address, 250*time.Millisecond)
	if err != nil {
		return fmt.Errorf("dial Windows gopls daemon endpoint: %w", err)
	}
	return connection.Close()
}

// runtimeServerValidateWindowsGoplsDaemonEndpoint 拒绝非规范或非 loopback 的 daemon 地址。
func runtimeServerValidateWindowsGoplsDaemonEndpoint(endpoint string) error {
	address, ok := strings.CutPrefix(endpoint, "tcp;")
	host, port, err := net.SplitHostPort(address)
	portNumber, portErr := strconv.Atoi(port)
	if !ok || err != nil || portErr != nil || host != "127.0.0.1" || portNumber < 1 || portNumber > 65535 || endpoint != "tcp;"+net.JoinHostPort(host, port) {
		return errors.New("Windows gopls daemon endpoint must be canonical IPv4 loopback TCP")
	}
	return nil
}
func runtimeServerRemoveWindowsGoplsDaemonRecord(dir, path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Windows gopls daemon record: %w", err)
	}
	return runtimeServerSyncGoplsRootCohortDirectory(dir)
}
