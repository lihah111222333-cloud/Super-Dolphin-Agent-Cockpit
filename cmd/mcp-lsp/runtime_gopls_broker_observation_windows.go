//go:build windows

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

const (
	runtimeServerWindowsGoplsObservationSchema   = 1
	runtimeServerWindowsGoplsObservationSource   = "broker_process_tree_job"
	runtimeServerWindowsGoplsObservationMethod   = "observe_process_tree_rss"
	runtimeServerWindowsGoplsReclaimMethod       = "reclaim_process_tree"
	runtimeServerWindowsGoplsObservationDeadline = time.Second
)

// runtimeServerWindowsGoplsObservationBinding 把 observer 输出绑定到 durable 配置与进程身份。
type runtimeServerWindowsGoplsObservationBinding struct {
	ConfigDigest        string
	OwnerPID            int
	OwnerStartIdentity  string
	DaemonPID           int
	DaemonStartIdentity string
}

type runtimeServerWindowsGoplsObservationRequest struct {
	Schema     int    `json:"schema"`
	Method     string `json:"method"`
	Capability string `json:"capability"`
}

type runtimeServerWindowsGoplsJobRSSObservation struct {
	SchemaVersion       int    `json:"schema_version"`
	Source              string `json:"source"`
	ConfigDigest        string `json:"config_digest"`
	OwnerPID            int    `json:"owner_pid"`
	OwnerStartIdentity  string `json:"owner_start_identity"`
	DaemonPID           int    `json:"daemon_pid"`
	DaemonStartIdentity string `json:"daemon_start_identity"`
	MemberPIDs          []int  `json:"member_pids"`
	RSSBytes            uint64 `json:"rss_bytes"`
}

type runtimeServerWindowsGoplsReclaimAck struct {
	SchemaVersion       int    `json:"schema_version"`
	Method              string `json:"method"`
	Source              string `json:"source"`
	ConfigDigest        string `json:"config_digest"`
	OwnerPID            int    `json:"owner_pid"`
	OwnerStartIdentity  string `json:"owner_start_identity"`
	DaemonPID           int    `json:"daemon_pid"`
	DaemonStartIdentity string `json:"daemon_start_identity"`
	TerminationAccepted bool   `json:"termination_accepted"`
}

type runtimeServerWindowsGoplsObservationServer struct {
	listener          net.Listener
	tree              *hiddenexec.ProcessTree
	binding           runtimeServerWindowsGoplsObservationBinding
	endpoint          string
	capability        string
	reclaimCapability string
	done              chan error
	closeOnce         sync.Once
	closeErr          error
}

// runtimeServerStartWindowsGoplsObservation 启动 broker 托管的串行 Job observer。
func runtimeServerStartWindowsGoplsObservation(tree *hiddenexec.ProcessTree, daemonEndpoint string, binding runtimeServerWindowsGoplsObservationBinding) (*runtimeServerWindowsGoplsObservationServer, error) {
	if tree == nil || !runtimeServerWindowsGoplsObservationBindingValid(binding) {
		return nil, errors.New("Windows gopls observation authority is invalid")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for Windows gopls observation: %w", err)
	}
	endpoint := "tcp;" + listener.Addr().String()
	if err := errors.Join(runtimeServerValidateWindowsGoplsDaemonEndpoint(endpoint), runtimeServerRejectMatchingGoplsEndpoints(endpoint, daemonEndpoint)); err != nil {
		return nil, errors.Join(err, listener.Close())
	}
	capability, err := runtimeServerNewWindowsGoplsCapability()
	if err != nil {
		return nil, errors.Join(err, listener.Close())
	}
	reclaimCapability, err := runtimeServerNewWindowsGoplsCapability()
	if err != nil {
		return nil, errors.Join(err, listener.Close())
	}
	if reclaimCapability == capability {
		return nil, errors.Join(errors.New("Windows gopls capabilities are not independent"), listener.Close())
	}
	server := &runtimeServerWindowsGoplsObservationServer{
		listener: listener, tree: tree, binding: binding, endpoint: endpoint,
		capability: capability, reclaimCapability: reclaimCapability, done: make(chan error, 1),
	}
	runtimesafe.SafeGo(context.Background(), nil, "mcp-lsp.windows-gopls-job-observer", func(context.Context) {
		server.run()
	})
	return server, nil
}

// runtimeServerNewWindowsGoplsCapability 生成一个不经环境或 argv 传递的 256-bit capability。
func runtimeServerNewWindowsGoplsCapability() (string, error) {
	var secret [32]byte
	if _, err := io.ReadFull(rand.Reader, secret[:]); err != nil {
		return "", fmt.Errorf("generate Windows gopls capability: %w", err)
	}
	return hex.EncodeToString(secret[:]), nil
}

// runtimeServerRejectMatchingGoplsEndpoints 拒绝 observer 占用 daemon 的保留地址。
func runtimeServerRejectMatchingGoplsEndpoints(observationEndpoint, daemonEndpoint string) error {
	if observationEndpoint == daemonEndpoint {
		return errors.New("Windows gopls observation endpoint conflicts with daemon endpoint")
	}
	return nil
}

// runtimeServerWindowsGoplsObservationBindingValid 校验 observer 固定身份完整。
func runtimeServerWindowsGoplsObservationBindingValid(binding runtimeServerWindowsGoplsObservationBinding) bool {
	return binding.ConfigDigest != "" && binding.OwnerPID > 1 && binding.OwnerStartIdentity != "" &&
		binding.DaemonPID > 1 && binding.DaemonStartIdentity != "" && binding.OwnerPID != binding.DaemonPID
}

// run 在 listener 或 Job 查询失败时终止 Job，并保存收敛根因。
func (s *runtimeServerWindowsGoplsObservationServer) run() {
	serveErr := s.serve()
	if errors.Is(serveErr, net.ErrClosed) {
		serveErr = nil
	} else if serveErr != nil {
		serveErr = errors.Join(serveErr, s.tree.Terminate())
	}
	s.done <- errors.Join(serveErr, s.closeListener())
	close(s.done)
}

// serve 串行处理一连接一帧；连接级失败只关闭该连接。
func (s *runtimeServerWindowsGoplsObservationServer) serve() error {
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			return err
		}
		fatal, requestErr := s.serveConnection(connection)
		closeErr := connection.Close()
		if fatal {
			return errors.Join(requestErr, closeErr)
		}
		if requestErr != nil || closeErr != nil {
			continue
		}
	}
}

// serveConnection 在一秒总期限内严格处理唯一 NDJSON 请求。
func (s *runtimeServerWindowsGoplsObservationServer) serveConnection(connection net.Conn) (bool, error) {
	if err := connection.SetDeadline(time.Now().Add(runtimeServerWindowsGoplsObservationDeadline)); err != nil {
		return false, err
	}
	reader := bufio.NewReaderSize(connection, runtimeServerWindowsGoplsBrokerMaxPayloadSize+1)
	payload, err := runtimeServerReadWindowsGoplsBrokerFrame(reader, "observation request")
	var request runtimeServerWindowsGoplsObservationRequest
	if err == nil {
		err = runtimeServerDecodeWindowsGoplsBrokerFrame(payload, &request, "observation request")
	}
	if err != nil || request.Schema != runtimeServerWindowsGoplsObservationSchema {
		writeErr := runtimeServerWriteWindowsGoplsBrokerFrame(connection, map[string]string{"error": "invalid observation request"}, "observation failure")
		return false, errors.Join(err, writeErr)
	}
	expected, ok := s.capabilityForMethod(request.Method)
	if !ok {
		return false, runtimeServerWriteWindowsGoplsBrokerFrame(connection, map[string]string{"error": "invalid observation method"}, "observation failure")
	}
	if !runtimeServerWindowsGoplsObservationCapabilityEqual(request.Capability, expected) {
		return false, runtimeServerWriteWindowsGoplsBrokerFrame(connection, map[string]string{"error": "Windows gopls capability rejected"}, "observation failure")
	}
	return s.serveAuthorizedRequest(connection, request.Method)
}

// capabilityForMethod 为读观察和破坏性回收选择彼此独立的 capability。
func (s *runtimeServerWindowsGoplsObservationServer) capabilityForMethod(method string) (string, bool) {
	switch method {
	case runtimeServerWindowsGoplsObservationMethod:
		return s.capability, true
	case runtimeServerWindowsGoplsReclaimMethod:
		return s.reclaimCapability, true
	default:
		return "", false
	}
}

// serveAuthorizedRequest 只分派固定的观察与整棵 Job 回收方法。
func (s *runtimeServerWindowsGoplsObservationServer) serveAuthorizedRequest(connection io.Writer, method string) (bool, error) {
	if method == runtimeServerWindowsGoplsReclaimMethod {
		ack, err := s.reclaim()
		if err != nil {
			writeErr := runtimeServerWriteWindowsGoplsBrokerFrame(connection, map[string]string{"error": "process-tree reclaim failed"}, "reclaim failure")
			return true, errors.Join(err, writeErr)
		}
		return false, runtimeServerWriteWindowsGoplsBrokerFrame(connection, ack, "reclaim response")
	}
	if method != runtimeServerWindowsGoplsObservationMethod {
		return false, runtimeServerWriteWindowsGoplsBrokerFrame(connection, map[string]string{"error": "invalid observation method"}, "observation failure")
	}
	observation, err := s.observe()
	if err != nil {
		writeErr := runtimeServerWriteWindowsGoplsBrokerFrame(connection, map[string]string{"error": "process-tree observation failed"}, "observation failure")
		return true, errors.Join(err, writeErr)
	}
	return false, runtimeServerWriteWindowsGoplsBrokerFrame(connection, observation, "observation response")
}

// reclaim 只向 broker 自身持有的 Job 提交终止，并返回绑定同一 authority 的接受确认。
func (s *runtimeServerWindowsGoplsObservationServer) reclaim() (runtimeServerWindowsGoplsReclaimAck, error) {
	if err := s.tree.Terminate(); err != nil {
		return runtimeServerWindowsGoplsReclaimAck{}, err
	}
	return runtimeServerWindowsGoplsReclaimAck{
		SchemaVersion: runtimeServerWindowsGoplsObservationSchema,
		Method:        runtimeServerWindowsGoplsReclaimMethod, Source: runtimeServerWindowsGoplsObservationSource,
		ConfigDigest: s.binding.ConfigDigest, OwnerPID: s.binding.OwnerPID,
		OwnerStartIdentity: s.binding.OwnerStartIdentity, DaemonPID: s.binding.DaemonPID,
		DaemonStartIdentity: s.binding.DaemonStartIdentity, TerminationAccepted: true,
	}, nil
}

// observe 仅从 Job Snapshot 与 RSSBytes 生成进程树快照，不退化为单 PID。
func (s *runtimeServerWindowsGoplsObservationServer) observe() (runtimeServerWindowsGoplsJobRSSObservation, error) {
	snapshot, err := s.tree.Snapshot()
	if err != nil {
		return runtimeServerWindowsGoplsJobRSSObservation{}, err
	}
	if len(snapshot.Unknown) != 0 || snapshot.Root.PID != s.binding.DaemonPID || snapshot.Root.StartToken != s.binding.DaemonStartIdentity {
		return runtimeServerWindowsGoplsJobRSSObservation{}, errors.New("Windows gopls Job snapshot identity is invalid")
	}
	members := make([]int, len(snapshot.Members))
	for index, member := range snapshot.Members {
		members[index] = member.PID
	}
	rssBytes, err := s.tree.RSSBytes()
	if err != nil {
		return runtimeServerWindowsGoplsJobRSSObservation{}, err
	}
	return runtimeServerWindowsGoplsJobRSSObservation{
		SchemaVersion: runtimeServerWindowsGoplsObservationSchema, Source: runtimeServerWindowsGoplsObservationSource,
		ConfigDigest: s.binding.ConfigDigest, OwnerPID: s.binding.OwnerPID, OwnerStartIdentity: s.binding.OwnerStartIdentity,
		DaemonPID: s.binding.DaemonPID, DaemonStartIdentity: s.binding.DaemonStartIdentity,
		MemberPIDs: members, RSSBytes: rssBytes,
	}, nil
}

// runtimeServerWindowsGoplsObservationCapabilityEqual 固定宽度比较 capability。
func runtimeServerWindowsGoplsObservationCapabilityEqual(candidate, expected string) bool {
	var fixed [64]byte
	copy(fixed[:], candidate)
	lengthEqual := subtle.ConstantTimeEq(int32(len(candidate)), int32(len(expected)))
	return subtle.ConstantTimeCompare(fixed[:], []byte(expected))&lengthEqual == 1
}

// runtimeServerCheckWindowsGoplsObservationEndpoint 查询并核对 live broker Job observer。
func runtimeServerCheckWindowsGoplsObservationEndpoint(endpoint, capability string, binding runtimeServerWindowsGoplsObservationBinding) error {
	_, err := runtimeServerQueryWindowsGoplsObservationEndpoint(endpoint, capability, binding)
	return err
}

// runtimeServerQueryWindowsGoplsObservationEndpoint 查询并返回经身份核验的真实 Job 样本。
func runtimeServerQueryWindowsGoplsObservationEndpoint(endpoint, capability string, binding runtimeServerWindowsGoplsObservationBinding) (observation runtimeServerWindowsGoplsJobRSSObservation, retErr error) {
	payload, err := runtimeServerCallWindowsGoplsObservationEndpoint(endpoint, capability, runtimeServerWindowsGoplsObservationMethod, "observation")
	if err != nil {
		return observation, err
	}
	if err := runtimeServerDecodeWindowsGoplsBrokerFrame(payload, &observation, "observation response"); err != nil {
		return observation, err
	}
	return observation, runtimeServerValidateWindowsGoplsObservation(observation, binding)
}

// runtimeServerReclaimWindowsGoplsObservationEndpoint 请求 capability 所属 broker 回收其唯一 Job。
func runtimeServerReclaimWindowsGoplsObservationEndpoint(endpoint, capability string, binding runtimeServerWindowsGoplsObservationBinding) error {
	payload, err := runtimeServerCallWindowsGoplsObservationEndpoint(endpoint, capability, runtimeServerWindowsGoplsReclaimMethod, "reclaim")
	if err != nil {
		return err
	}
	var ack runtimeServerWindowsGoplsReclaimAck
	if err := runtimeServerDecodeWindowsGoplsBrokerFrame(payload, &ack, "reclaim response"); err != nil {
		return err
	}
	return runtimeServerValidateWindowsGoplsReclaimAck(ack, binding)
}

// runtimeServerCallWindowsGoplsObservationEndpoint 发送一个固定方法并有界读取唯一响应帧。
func runtimeServerCallWindowsGoplsObservationEndpoint(endpoint, capability, method, kind string) (payload []byte, retErr error) {
	if err := runtimeServerValidateWindowsGoplsDaemonEndpoint(endpoint); err != nil {
		return nil, err
	}
	if !runtimeServerWindowsSHA256Valid(capability) {
		return nil, errors.New("Windows gopls observation capability is invalid")
	}
	address, _ := strings.CutPrefix(endpoint, "tcp;")
	connection, err := net.DialTimeout("tcp4", address, 250*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("dial Windows gopls observation endpoint: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, connection.Close()) }()
	if err := connection.SetDeadline(time.Now().Add(runtimeServerWindowsGoplsObservationDeadline)); err != nil {
		return nil, err
	}
	request := runtimeServerWindowsGoplsObservationRequest{Schema: runtimeServerWindowsGoplsObservationSchema, Method: method, Capability: capability}
	if err := runtimeServerWriteWindowsGoplsBrokerFrame(connection, request, kind+" request"); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(connection, runtimeServerWindowsGoplsBrokerMaxPayloadSize+1)
	return runtimeServerReadWindowsGoplsBrokerFrame(reader, kind+" response")
}

// runtimeServerValidateWindowsGoplsReclaimAck 拒绝未绑定同一配置和进程身份的终止接受确认。
func runtimeServerValidateWindowsGoplsReclaimAck(ack runtimeServerWindowsGoplsReclaimAck, binding runtimeServerWindowsGoplsObservationBinding) error {
	observed := runtimeServerWindowsGoplsObservationBinding{
		ConfigDigest: ack.ConfigDigest, OwnerPID: ack.OwnerPID, OwnerStartIdentity: ack.OwnerStartIdentity,
		DaemonPID: ack.DaemonPID, DaemonStartIdentity: ack.DaemonStartIdentity,
	}
	if ack.SchemaVersion != runtimeServerWindowsGoplsObservationSchema || ack.Method != runtimeServerWindowsGoplsReclaimMethod ||
		ack.Source != runtimeServerWindowsGoplsObservationSource || !ack.TerminationAccepted || observed != binding {
		return errors.New("Windows gopls reclaim acknowledgement is invalid")
	}
	return nil
}

// runtimeServerValidateWindowsGoplsObservation 核对来源、身份、daemon 成员与非零 Job RSS。
func runtimeServerValidateWindowsGoplsObservation(observation runtimeServerWindowsGoplsJobRSSObservation, binding runtimeServerWindowsGoplsObservationBinding) error {
	observedBinding := runtimeServerWindowsGoplsObservationBinding{
		ConfigDigest: observation.ConfigDigest, OwnerPID: observation.OwnerPID,
		OwnerStartIdentity: observation.OwnerStartIdentity, DaemonPID: observation.DaemonPID,
		DaemonStartIdentity: observation.DaemonStartIdentity,
	}
	valid := observation.SchemaVersion == runtimeServerWindowsGoplsObservationSchema &&
		observation.Source == runtimeServerWindowsGoplsObservationSource && observedBinding == binding && observation.RSSBytes > 0
	if !valid {
		return errors.New("Windows gopls observation identity or RSS is invalid")
	}
	if slices.Contains(observation.MemberPIDs, binding.DaemonPID) {
		return nil
	}
	return errors.New("Windows gopls observation omits the daemon Job member")
}

// CloseAndWait 关闭 listener 并等待 observer goroutine 完整退出。
func (s *runtimeServerWindowsGoplsObservationServer) CloseAndWait() error {
	if s == nil {
		return nil
	}
	return errors.Join(s.closeListener(), <-s.done)
}

// closeListener 只关闭一次 observer listener，并保留关闭错误。
func (s *runtimeServerWindowsGoplsObservationServer) closeListener() error {
	s.closeOnce.Do(func() { s.closeErr = s.listener.Close() })
	return s.closeErr
}
