//go:build unix

package localci

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestSchedulerTransportTwoClientsShareSlots(t *testing.T) {
	root := schedulerTransportTestRoot(t)
	config := schedulerTransportTestConfig("two-clients")
	running := startSchedulerTransportOwner(t, root, config)
	first := mustDialSchedulerTransport(t, root, config)
	second := mustDialSchedulerTransport(t, root, config)

	clients := []*SchedulerClient{first, second}
	for index := range 4 {
		request := schedulerTransportJob(fmt.Sprintf("job-%d", index+1), uint64(index+1))
		if err := clients[index%len(clients)].Enqueue(context.Background(), request); err != nil {
			t.Fatalf("enqueue %s: %v", request.ID, err)
		}
	}
	reservations, err := first.ReserveRunnable(context.Background())
	if err != nil {
		t.Fatalf("reserve jobs: %v", err)
	}
	if len(reservations) != 3 {
		t.Fatalf("reservations=%d want=3", len(reservations))
	}
	status, err := second.State(context.Background(), "job-4")
	if err != nil {
		t.Fatalf("read fourth job: %v", err)
	}
	if status != WorkloadStatusQueued {
		t.Fatalf("fourth status=%q want=%q", status, WorkloadStatusQueued)
	}
	snapshot, err := second.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Workloads) != 4 || len(snapshot.Leases) != 3 {
		t.Fatalf("snapshot workloads=%d leases=%d want=4/3", len(snapshot.Workloads), len(snapshot.Leases))
	}
	assertSchedulerSocketPrivate(t, running.owner.socketPath, config.OwnerUID)
}

func TestSchedulerTransportRejectsSecondOwnerAndInvalidFacadeInput(t *testing.T) {
	root := schedulerTransportTestRoot(t)
	config := schedulerTransportTestConfig("single-owner")
	startSchedulerTransportOwner(t, root, config)
	second, err := openSchedulerOwnerWithRuntimeRoot(context.Background(), config, root)
	if second != nil || !errors.Is(err, ErrSchedulerOwned) {
		t.Fatalf("second owner=%v error=%v want ErrSchedulerOwned", second, err)
	}
	client := mustDialSchedulerTransport(t, root, config)
	invalid := schedulerTransportJob("invalid-kind", 1)
	invalid.Kind = WorkloadKind("unknown")
	if err := client.Enqueue(context.Background(), invalid); !errors.Is(err, ErrInvalidSchedulerInput) {
		t.Fatalf("unknown kind error=%v want ErrInvalidSchedulerInput", err)
	}
	if err := client.Complete(context.Background(), "missing", WorkloadStatus("unknown")); !errors.Is(err, ErrInvalidSchedulerInput) {
		t.Fatalf("unknown status error=%v want ErrInvalidSchedulerInput", err)
	}
}

func TestSchedulerTransportRestartRecoversState(t *testing.T) {
	root := schedulerTransportTestRoot(t)
	config := schedulerTransportTestConfig("restart")
	enqueueSchedulerTransportRestartFixture(t, root, config)
	advanceSchedulerTransportRestartFixture(t, root, config)
	assertSchedulerTransportRestartFixture(t, root, config)
}

func enqueueSchedulerTransportRestartFixture(t *testing.T, root string, config SchedulerConfig) {
	t.Helper()
	firstOwner := startSchedulerTransportOwner(t, root, config)
	firstClient := mustDialSchedulerTransport(t, root, config)
	if err := firstClient.Enqueue(context.Background(), WorkloadRequest{
		ID: "build", InvocationID: "restart", EnqueueSequence: 1, Kind: WorkloadKindBuild,
	}); err != nil {
		t.Fatalf("enqueue build: %v", err)
	}
	if err := firstClient.Enqueue(context.Background(), WorkloadRequest{
		ID: "job", InvocationID: "restart", EnqueueSequence: 2, Kind: WorkloadKindJob,
		Dependencies: []string{"build"},
	}); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	reservations, err := firstClient.ReserveRunnable(context.Background())
	if err != nil || len(reservations) != 1 || reservations[0].WorkloadID != "build" {
		t.Fatalf("first reservations=%v err=%v", reservations, err)
	}
	mustCloseSchedulerClient(t, firstClient)
	firstOwner.stop(t)
}

func advanceSchedulerTransportRestartFixture(t *testing.T, root string, config SchedulerConfig) {
	t.Helper()
	secondOwner := startSchedulerTransportOwner(t, root, config)
	secondClient := mustDialSchedulerTransport(t, root, config)
	snapshot, err := secondClient.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after restart: %v", err)
	}
	if len(snapshot.Workloads) != 2 || len(snapshot.Leases) != 1 {
		t.Fatalf("recovered workloads=%d leases=%d want=2/1", len(snapshot.Workloads), len(snapshot.Leases))
	}
	if err := secondClient.Complete(context.Background(), "build", WorkloadStatusPassed); err != nil {
		t.Fatalf("complete recovered build: %v", err)
	}
	reservations, err := secondClient.ReserveRunnable(context.Background())
	if err != nil || len(reservations) != 1 || reservations[0].WorkloadID != "job" {
		t.Fatalf("second reservations=%v err=%v", reservations, err)
	}
	mustCloseSchedulerClient(t, secondClient)
	secondOwner.stop(t)
}

func assertSchedulerTransportRestartFixture(t *testing.T, root string, config SchedulerConfig) {
	t.Helper()
	startSchedulerTransportOwner(t, root, config)
	thirdClient := mustDialSchedulerTransport(t, root, config)
	status, err := thirdClient.State(context.Background(), "build")
	if err != nil || status != WorkloadStatusPassed {
		t.Fatalf("build after second restart status=%q err=%v", status, err)
	}
	finalSnapshot, err := thirdClient.Snapshot(context.Background())
	if err != nil || len(finalSnapshot.Leases) != 1 {
		t.Fatalf("final snapshot leases=%d err=%v", len(finalSnapshot.Leases), err)
	}
}

func TestSchedulerTransportConcurrentClients(t *testing.T) {
	root := schedulerTransportTestRoot(t)
	config := schedulerTransportTestConfig("concurrent")
	startSchedulerTransportOwner(t, root, config)
	clients := []*SchedulerClient{
		mustDialSchedulerTransport(t, root, config),
		mustDialSchedulerTransport(t, root, config),
	}
	group, groupContext := errgroup.WithContext(context.Background())
	for index := range 48 {
		group.Go(func() error {
			request := schedulerTransportJob(fmt.Sprintf("concurrent-%02d", index), uint64(index+1))
			return clients[index%len(clients)].Enqueue(groupContext, request)
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent enqueue: %v", err)
	}
	snapshot, err := clients[0].Snapshot(context.Background())
	if err != nil {
		t.Fatalf("concurrent snapshot: %v", err)
	}
	if len(snapshot.Workloads) != 48 {
		t.Fatalf("workloads=%d want=48", len(snapshot.Workloads))
	}
}

func TestSchedulerTransportRejectsMalformedRequests(t *testing.T) {
	root := schedulerTransportTestRoot(t)
	config := schedulerTransportTestConfig("malformed")
	running := startSchedulerTransportOwner(t, root, config)
	identity := mustSchedulerTransportIdentity(t, config)
	valid := schedulerWireRequest{
		Version: schedulerProtocolVersion, RequestID: "request-000000000001",
		DaemonKey: identity.key, Method: schedulerMethodSnapshot, Params: json.RawMessage(`{}`),
	}
	validPayload, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal valid request: %v", err)
	}
	cases := []struct {
		name    string
		payload []byte
	}{
		{name: "unknown field", payload: append(validPayload[:len(validPayload)-1], []byte(`,"extra":true}`)...)},
		{name: "trailing value", payload: append(append([]byte(nil), validPayload...), []byte(` {}`)...)},
		{name: "unknown method", payload: mustSchedulerWirePayload(t, schedulerWireRequest{
			Version: schedulerProtocolVersion, RequestID: "request-000000000002",
			DaemonKey: identity.key, Method: "destroy", Params: json.RawMessage(`{}`),
		})},
		{name: "unknown params field", payload: mustSchedulerWirePayload(t, schedulerWireRequest{
			Version: schedulerProtocolVersion, RequestID: "request-000000000004",
			DaemonKey: identity.key, Method: schedulerMethodSnapshot, Params: json.RawMessage(`{"extra":true}`),
		})},
		{name: "wrong identity", payload: mustSchedulerWirePayload(t, schedulerWireRequest{
			Version: schedulerProtocolVersion, RequestID: "request-000000000003",
			DaemonKey: stringsOfZero(len(identity.key)), Method: schedulerMethodSnapshot, Params: json.RawMessage(`{}`),
		})},
		{name: "wrong version", payload: mustSchedulerWirePayload(t, schedulerWireRequest{
			Version: schedulerProtocolVersion + 1, RequestID: "request-000000000005",
			DaemonKey: identity.key, Method: schedulerMethodSnapshot, Params: json.RawMessage(`{}`),
		})},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := rawSchedulerRequest(t, running.owner.socketPath, config.OwnerUID, testCase.payload)
			if response.Error == nil || response.Error.Code != "protocol" {
				t.Fatalf("response error=%+v want protocol", response.Error)
			}
		})
	}
}

func TestSchedulerTransportRejectsReplayOversizeAndHalfPacket(t *testing.T) {
	root := schedulerTransportTestRoot(t)
	config := schedulerTransportTestConfig("framing")
	running := startSchedulerTransportOwner(t, root, config)
	assertSchedulerTransportReplayRejected(t, running.owner.socketPath, config)
	assertSchedulerTransportOversizeRejected(t, running.owner.socketPath, config.OwnerUID)
	assertSchedulerTransportHalfPacketIsolated(t, running.owner.socketPath, root, config)
}

func assertSchedulerTransportReplayRejected(t *testing.T, socketPath string, config SchedulerConfig) {
	t.Helper()
	identity := mustSchedulerTransportIdentity(t, config)
	request := schedulerWireRequest{
		Version: schedulerProtocolVersion, RequestID: "replay-000000000001",
		DaemonKey: identity.key, Method: schedulerMethodState,
		Params: mustSchedulerWirePayload(t, schedulerStateParams{WorkloadID: "missing"}),
	}
	conn := mustRawSchedulerConn(t, socketPath, config.OwnerUID)
	if err := writeSchedulerFrame(conn, request); err != nil {
		t.Fatalf("write first replay request: %v", err)
	}
	first := readSchedulerWireResponse(t, conn)
	if first.Error == nil || first.Error.Code != "not_found" {
		t.Fatalf("first replay response=%+v", first.Error)
	}
	if err := writeSchedulerFrame(conn, request); err != nil {
		t.Fatalf("write replay request: %v", err)
	}
	second := readSchedulerWireResponse(t, conn)
	if second.Error == nil || second.Error.Code != "replay" {
		t.Fatalf("second replay response=%+v", second.Error)
	}
	mustCloseNetConn(t, conn)
}

func assertSchedulerTransportOversizeRejected(t *testing.T, socketPath string, ownerUID int) {
	t.Helper()
	oversized := mustRawSchedulerConn(t, socketPath, ownerUID)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], schedulerMaxFrameBytes+1)
	if _, err := oversized.Write(header[:]); err != nil {
		t.Fatalf("write oversized header: %v", err)
	}
	oversizedResponse := readSchedulerWireResponse(t, oversized)
	if oversizedResponse.Error == nil || oversizedResponse.Error.Code != "protocol" {
		t.Fatalf("oversized response=%+v", oversizedResponse.Error)
	}
	mustCloseNetConn(t, oversized)
}

func assertSchedulerTransportHalfPacketIsolated(
	t *testing.T,
	socketPath string,
	root string,
	config SchedulerConfig,
) {
	t.Helper()
	half := mustRawSchedulerConn(t, socketPath, config.OwnerUID)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], 100)
	if _, err := half.Write(append(header[:], []byte(`{"v`)...)); err != nil {
		t.Fatalf("write half packet: %v", err)
	}
	mustCloseNetConn(t, half)
	client := mustDialSchedulerTransport(t, root, config)
	if _, err := client.Snapshot(context.Background()); err != nil {
		t.Fatalf("owner unhealthy after half packet: %v", err)
	}
}

func TestSchedulerTransportSocketSecurityAndStaleRecovery(t *testing.T) {
	t.Run("stale socket", func(t *testing.T) {
		root := schedulerTransportTestRoot(t)
		config := schedulerTransportTestConfig("stale")
		path := mustSchedulerSocketPath(t, root, config)
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatalf("create stale listener: %v", err)
		}
		listener.SetUnlinkOnClose(false)
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatalf("chmod stale socket: %v", err)
		}
		if err := listener.Close(); err != nil {
			t.Fatalf("close stale listener: %v", err)
		}
		owner, err := openSchedulerOwnerWithRuntimeRoot(context.Background(), config, root)
		if err != nil {
			t.Fatalf("open owner over stale socket: %v", err)
		}
		if err := owner.Close(); err != nil {
			t.Fatalf("close stale recovery owner: %v", err)
		}
	})

	t.Run("malicious paths", func(t *testing.T) {
		for _, kind := range []string{"symlink", "regular", "active"} {
			t.Run(kind, func(t *testing.T) {
				root := schedulerTransportTestRoot(t)
				config := schedulerTransportTestConfig("malicious-" + kind)
				path := mustSchedulerSocketPath(t, root, config)
				cleanup := prepareMaliciousSchedulerSocket(t, kind, root, path)
				defer cleanup()
				owner, err := openSchedulerOwnerWithRuntimeRoot(context.Background(), config, root)
				if owner != nil {
					_ = owner.Close()
					t.Fatalf("malicious %s socket path was accepted", kind)
				}
				if err == nil {
					t.Fatalf("malicious %s socket path returned nil error", kind)
				}
			})
		}
	})
}

func TestSchedulerTransportPeerUIDAndAliasPath(t *testing.T) {
	root := schedulerTransportTestRoot(t)
	config := schedulerTransportTestConfig("peer-alias")
	running := startSchedulerTransportOwner(t, root, config)
	conn := mustRawSchedulerConn(t, running.owner.socketPath, config.OwnerUID)
	peerUID, err := schedulerTransportPeerUID(conn)
	if err != nil {
		t.Fatalf("read peer UID: %v", err)
	}
	if peerUID != config.OwnerUID {
		t.Fatalf("peer UID=%d want=%d", peerUID, config.OwnerUID)
	}
	mustCloseNetConn(t, conn)
	alias := config
	alias.Endpoint = "unix:///var/run/../run/docker.sock"
	firstPath := mustSchedulerSocketPath(t, root, config)
	aliasPath := mustSchedulerSocketPath(t, root, alias)
	if firstPath != aliasPath {
		t.Fatalf("alias socket path=%q want=%q", aliasPath, firstPath)
	}
}

type runningSchedulerTransportOwner struct {
	owner  *SchedulerOwner
	cancel context.CancelFunc
	group  errgroup.Group
	once   sync.Once
	err    error
}

func startSchedulerTransportOwner(
	t *testing.T,
	root string,
	config SchedulerConfig,
) *runningSchedulerTransportOwner {
	t.Helper()
	owner, err := openSchedulerOwnerWithRuntimeRoot(context.Background(), config, root)
	if err != nil {
		t.Fatalf("open scheduler transport owner: %v", err)
	}
	serveContext, cancel := context.WithCancel(context.Background())
	running := &runningSchedulerTransportOwner{owner: owner, cancel: cancel}
	running.group.Go(func() error {
		err := owner.Serve(serveContext)
		if errors.Is(err, context.Canceled) || errors.Is(err, ErrSchedulerClosed) {
			return nil
		}
		return err
	})
	t.Cleanup(func() { running.stop(t) })
	return running
}

func (r *runningSchedulerTransportOwner) stop(t *testing.T) {
	t.Helper()
	r.once.Do(func() {
		r.cancel()
		r.err = errors.Join(r.owner.Close(), r.group.Wait())
	})
	if r.err != nil {
		t.Errorf("stop scheduler transport owner: %v", r.err)
	}
}

func schedulerTransportTestRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "lci-")
	if err != nil {
		t.Fatalf("create short transport root: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("canonicalize transport root: %v", err)
	}
	if err := os.Chmod(canonical, 0o700); err != nil {
		t.Fatalf("chmod transport root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(canonical); err != nil {
			t.Errorf("remove transport root: %v", err)
		}
	})
	return canonical
}

func schedulerTransportTestConfig(daemonID string) SchedulerConfig {
	return SchedulerConfig{
		Endpoint: "unix:///var/run/docker.sock",
		DaemonID: daemonID,
		OwnerUID: os.Geteuid(),
	}
}

func schedulerTransportJob(id string, sequence uint64) WorkloadRequest {
	return WorkloadRequest{
		ID: id, InvocationID: "transport", EnqueueSequence: sequence, Kind: WorkloadKindJob,
	}
}

func mustDialSchedulerTransport(t *testing.T, root string, config SchedulerConfig) *SchedulerClient {
	t.Helper()
	client, err := dialSchedulerWithRuntimeRoot(context.Background(), config, root)
	if err != nil {
		t.Fatalf("dial scheduler transport: %v", err)
	}
	t.Cleanup(func() { mustCloseSchedulerClient(t, client) })
	return client
}

func mustCloseSchedulerClient(t *testing.T, client *SchedulerClient) {
	t.Helper()
	if err := client.Close(); err != nil {
		t.Fatalf("close scheduler client: %v", err)
	}
}

func mustSchedulerTransportIdentity(t *testing.T, config SchedulerConfig) daemonIdentity {
	t.Helper()
	identity, err := newDaemonIdentity(config.Endpoint, config.TLSFingerprint, config.DaemonID, config.OwnerUID)
	if err != nil {
		t.Fatalf("normalize scheduler transport identity: %v", err)
	}
	return identity
}

func mustSchedulerSocketPath(t *testing.T, root string, config SchedulerConfig) string {
	t.Helper()
	path, err := deriveSchedulerSocketPath(root, mustSchedulerTransportIdentity(t, config))
	if err != nil {
		t.Fatalf("derive scheduler socket path: %v", err)
	}
	return path
}

func mustRawSchedulerConn(t *testing.T, path string, ownerUID int) net.Conn {
	t.Helper()
	conn, err := dialSchedulerTransport(context.Background(), path, ownerUID)
	if err != nil {
		t.Fatalf("dial raw scheduler connection: %v", err)
	}
	return conn
}

func rawSchedulerRequest(t *testing.T, path string, ownerUID int, payload []byte) schedulerWireResponse {
	t.Helper()
	conn := mustRawSchedulerConn(t, path, ownerUID)
	defer mustCloseNetConn(t, conn)
	writeRawSchedulerPayload(t, conn, payload)
	return readSchedulerWireResponse(t, conn)
}

func writeRawSchedulerPayload(t *testing.T, writer io.Writer, payload []byte) {
	t.Helper()
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := writer.Write(append(header[:], payload...)); err != nil {
		t.Fatalf("write raw scheduler payload: %v", err)
	}
}

func readSchedulerWireResponse(t *testing.T, reader io.Reader) schedulerWireResponse {
	t.Helper()
	payload, err := readSchedulerFrame(reader)
	if err != nil {
		t.Fatalf("read scheduler response frame: %v", err)
	}
	var response schedulerWireResponse
	if err := decodeStrictSchedulerJSON(payload, &response); err != nil {
		t.Fatalf("decode scheduler response: %v", err)
	}
	return response
}

func mustSchedulerWirePayload(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal scheduler wire payload: %v", err)
	}
	return payload
}

func mustCloseNetConn(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("close scheduler net connection: %v", err)
	}
}

func assertSchedulerSocketPrivate(t *testing.T, path string, ownerUID int) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat scheduler socket: %v", err)
	}
	if err := validateSchedulerSocketInfo(info, ownerUID); err != nil {
		t.Fatalf("scheduler socket is not private: %v", err)
	}
	parentInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("lstat scheduler socket parent: %v", err)
	}
	if parentInfo.Mode().Perm() != 0o700 {
		t.Fatalf("scheduler socket parent mode=%04o want=0700", parentInfo.Mode().Perm())
	}
}

func prepareMaliciousSchedulerSocket(t *testing.T, kind, root, path string) func() {
	t.Helper()
	switch kind {
	case "symlink":
		return prepareMaliciousSchedulerSymlink(t, root, path)
	case "regular":
		return prepareMaliciousSchedulerRegularFile(t, path)
	case "active":
		return prepareMaliciousSchedulerActiveSocket(t, path)
	default:
		t.Fatalf("unknown malicious socket kind %q", kind)
		return func() {}
	}
}

func prepareMaliciousSchedulerSymlink(t *testing.T, root, path string) func() {
	t.Helper()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("protected"), 0o600); err != nil {
		t.Fatalf("create symlink target: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("create malicious symlink: %v", err)
	}
	return func() {}
}

func prepareMaliciousSchedulerRegularFile(t *testing.T, path string) func() {
	t.Helper()
	if err := os.WriteFile(path, []byte("not-a-socket"), 0o600); err != nil {
		t.Fatalf("create malicious regular file: %v", err)
	}
	return func() {}
}

func prepareMaliciousSchedulerActiveSocket(t *testing.T, path string) func() {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("create active malicious socket: %v", err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod active malicious socket: %v", err)
	}
	return func() {
		_ = listener.Close()
		_ = os.Remove(path)
	}
}

func stringsOfZero(length int) string {
	value := make([]byte, length)
	for index := range value {
		value[index] = '0'
	}
	return string(value)
}
