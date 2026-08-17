//go:build windows && e2e

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

type windowsGoplsJobRSSObservation struct {
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

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWindowsGoplsBrokerOwnedJobRSSObservationE2E(t *testing.T) {
	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	fixture := t.TempDir()
	argsLog, cacheRoot := filepath.Join(fixture, "args.log"), filepath.Join(fixture, "cache")
	install := buildWindowsGoplsShortIdlePrecheckTestInstall(t)
	env := windowsGoplsSidecarEnv(t, install, fakeGoplsArgsLogEnv+"="+argsLog,
		"AGENT_LSP_SHARED_CACHE_DIR="+cacheRoot, "MCP_LSP_FAKE_GOPLS_RSS_CHILD=1")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var exact []windowsGoplsProvisionalIdentity
	registerWindowsGoplsExactCleanup(t, &exact, cacheRoot)
	clients := startWindowsGoplsClients(t, ctx, install.Binary, roots, filepath.Dir(install.Gopls), env, targets)

	invocations := waitForFakeGoplsInvocations(t, argsLog, 4)
	daemonPID, childPID, forwarders := requireWindowsGoplsRSSInvocationTopology(t, invocations, install.Gopls)
	record := waitForWindowsGoplsBrokerRecord(t, cacheRoot)
	exact = []windowsGoplsProvisionalIdentity{
		{PID: record.OwnerPID, StartIdentity: record.OwnerStartIdentity},
		{PID: record.DaemonPID, StartIdentity: record.DaemonStartIdentity},
		requireWindowsGoplsStartIdentity(t, childPID),
	}
	requireWindowsGoplsBrokerProcessIdentities(t, record, daemonPID)
	requireWindowsGoplsRSSRecordContract(t, record)

	assertWindowsGoplsJobRSSObservation(t, queryWindowsGoplsJobRSS(t, record, record.ObservationCapability), record, childPID, forwarders)
	requireWindowsGoplsObservationCapabilityRejected(t, record)
	requireWindowsGoplsMethodCapabilityRejected(t, record, runtimeServerWindowsGoplsReclaimMethod, record.ObservationCapability)
	requireWindowsGoplsMethodCapabilityRejected(t, record, runtimeServerWindowsGoplsObservationMethod, record.ReclaimCapability)
	requireWindowsProcessAlive(t, record.DaemonPID, "gopls daemon after cross-capability rejection")
	clients[0].close(t)
	result := clients[1].callTool(t, "completion", map[string]any{"pos": targets[1] + ":3:1"})
	requireMCPToolSuccess(t, clients[1], result, "second Windows forwarder after first closed")
	assertWindowsGoplsJobRSSObservation(t, queryWindowsGoplsJobRSS(t, record, record.ObservationCapability), record, childPID, forwarders)
	clients[1].close(t)
	waitForWindowsGoplsObservationEndpointClose(t, record.ObservationEndpoint)
	requireWindowsGoplsExactIdentitiesGone(t, 2*time.Second, exact...)
}

// registerWindowsGoplsExactCleanup 注册成功路径 identity 与 cache record 兜底清理。
// 兜底只读取指定 cacheRoot 下的 daemon.json，并始终使用 PID+启动身份，拒绝裸 PID。
func registerWindowsGoplsExactCleanup(t *testing.T, identities *[]windowsGoplsProvisionalIdentity, cacheRoots ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, identity := range *identities {
			cleanupWindowsGoplsExactIdentity(t, identity)
		}
		for _, cacheRoot := range cacheRoots {
			paths, err := windowsGoplsBrokerRecordPaths(cacheRoot)
			if err != nil {
				t.Errorf("find Windows gopls broker records during cleanup: %v", err)
				continue
			}
			for _, path := range paths {
				record, err := readWindowsGoplsBrokerRecordForCleanup(path)
				if err != nil {
					t.Errorf("read Windows gopls broker record during cleanup %s: %v", path, err)
					continue
				}
				for _, identity := range []windowsGoplsProvisionalIdentity{
					{PID: record.OwnerPID, StartIdentity: record.OwnerStartIdentity},
					{PID: record.DaemonPID, StartIdentity: record.DaemonStartIdentity},
				} {
					if identity.PID > 1 && identity.StartIdentity != "" {
						cleanupWindowsGoplsExactIdentity(t, identity)
					}
				}
			}
		}
	})
}

func readWindowsGoplsBrokerRecordForCleanup(path string) (windowsGoplsBrokerRecordV2, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return windowsGoplsBrokerRecordV2{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var record windowsGoplsBrokerRecordV2
	if err := decoder.Decode(&record); err != nil {
		return windowsGoplsBrokerRecordV2{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return windowsGoplsBrokerRecordV2{}, errors.New("trailing JSON payload")
	}
	return record, nil
}

func requireWindowsGoplsRSSInvocationTopology(t *testing.T, invocations [][]string, executable string) (int, int, map[int]bool) {
	t.Helper()
	topology, childPID := splitWindowsGoplsRSSChildInvocation(t, invocations)
	daemonArgs, forwarderArgs := splitWindowsGoplsInvocations(topology)
	daemonEndpoint, daemonPID := requireWindowsGoplsDaemonInvocation(t, daemonArgs, executable)
	requireWindowsGoplsForwarders(t, forwarderArgs, daemonEndpoint, executable)
	return daemonPID, childPID, windowsGoplsForwarderPIDSet(t, forwarderArgs)
}

func splitWindowsGoplsRSSChildInvocation(t *testing.T, invocations [][]string) ([][]string, int) {
	t.Helper()
	var childPID int
	topology := make([][]string, 0, 3)
	for _, args := range invocations {
		if len(args) > 0 && args[0] == "rss-child" {
			pid, err := strconv.Atoi(windowsGoplsArg(args, "pid="))
			if err != nil || pid <= 1 {
				t.Fatalf("invalid native fake gopls RSS child invocation: %v", args)
			}
			childPID = pid
			continue
		}
		topology = append(topology, args)
	}
	if childPID <= 1 {
		t.Fatalf("native fake gopls RSS child missing from invocations: %v", invocations)
	}
	return topology, childPID
}

func windowsGoplsForwarderPIDSet(t *testing.T, invocations [][]string) map[int]bool {
	t.Helper()
	result := make(map[int]bool, len(invocations))
	for _, args := range invocations {
		pid, err := strconv.Atoi(windowsGoplsArg(args, "pid="))
		if err != nil || pid <= 1 {
			t.Fatalf("invalid fake gopls forwarder PID in %v", args)
		}
		result[pid] = true
	}
	return result
}

func requireWindowsGoplsRSSRecordContract(t *testing.T, record windowsGoplsBrokerRecordV2) {
	t.Helper()
	observationErr := validateWindowsGoplsTestCapability(record.ObservationCapability)
	reclaimErr := validateWindowsGoplsTestCapability(record.ReclaimCapability)
	if observationErr != nil || reclaimErr != nil || record.ObservationCapability == record.ReclaimCapability ||
		record.SchemaVersion != runtimeServerWindowsGoplsDaemonSchema || record.ObservationEndpoint == "" || record.ObservationEndpoint == record.Endpoint {
		t.Fatalf("Windows broker RSS observation record contract mismatch: %+v observation_error=%v reclaim_error=%v", record, observationErr, reclaimErr)
	}
}

func validateWindowsGoplsTestCapability(value string) error {
	capability, err := hex.DecodeString(value)
	if err != nil {
		return err
	}
	if len(capability) != 32 || value != strings.ToLower(value) {
		return errors.New("capability is not canonical 256-bit hex")
	}
	return nil
}

func queryWindowsGoplsJobRSS(t *testing.T, record windowsGoplsBrokerRecordV2, capability string) windowsGoplsJobRSSObservation {
	t.Helper()
	connection := dialWindowsGoplsObservation(t, record.ObservationEndpoint)
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close Windows gopls observation connection: %v", err)
		}
	}()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set Windows gopls observation deadline: %v", err)
	}
	request := map[string]any{"schema": 1, "method": "observe_process_tree_rss", "capability": capability}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		t.Fatalf("write Windows gopls RSS observation request: %v", err)
	}
	var observation windowsGoplsJobRSSObservation
	decoder := json.NewDecoder(connection)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&observation); err != nil {
		t.Fatalf("read Windows gopls RSS observation response: %v", err)
	}
	return observation
}

func dialWindowsGoplsObservation(t *testing.T, endpoint string) net.Conn {
	t.Helper()
	if err := runtimeServerValidateWindowsGoplsDaemonEndpoint(endpoint); err != nil {
		t.Fatalf("invalid Windows gopls observation endpoint %q: %v", endpoint, err)
	}
	connection, err := net.DialTimeout("tcp4", strings.TrimPrefix(endpoint, "tcp;"), 250*time.Millisecond)
	if err != nil {
		t.Fatalf("dial Windows gopls observation endpoint: %v", err)
	}
	return connection
}

func requireWindowsGoplsObservationCapabilityRejected(t *testing.T, record windowsGoplsBrokerRecordV2) {
	t.Helper()
	wrong := record.ObservationCapability
	if wrong[0] == '0' {
		wrong = "1" + wrong[1:]
	} else {
		wrong = "0" + wrong[1:]
	}
	requireWindowsGoplsMethodCapabilityRejected(t, record, runtimeServerWindowsGoplsObservationMethod, wrong)
}

func requireWindowsGoplsMethodCapabilityRejected(t *testing.T, record windowsGoplsBrokerRecordV2, method, capability string) {
	t.Helper()
	connection := dialWindowsGoplsObservation(t, record.ObservationEndpoint)
	defer func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close rejected Windows gopls observation connection: %v", err)
		}
	}()
	if err := connection.SetDeadline(time.Now().Add(750 * time.Millisecond)); err != nil {
		t.Fatalf("set rejected observation deadline: %v", err)
	}
	if err := json.NewEncoder(connection).Encode(map[string]any{"schema": 1, "method": method, "capability": capability}); err != nil {
		t.Fatalf("write rejected observation request: %v", err)
	}
	var failure struct {
		Error string `json:"error"`
	}
	err := json.NewDecoder(connection).Decode(&failure)
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatalf("wrong observation capability did not fail fast: %v", err)
	}
	if err == nil && strings.TrimSpace(failure.Error) == "" {
		t.Fatalf("wrong observation capability returned a successful response")
	}
}

func assertWindowsGoplsJobRSSObservation(t *testing.T, observation windowsGoplsJobRSSObservation, record windowsGoplsBrokerRecordV2, childPID int, forwarders map[int]bool) {
	t.Helper()
	requireWindowsGoplsJobRSSIdentity(t, observation, record)
	requireWindowsGoplsJobRSSMembers(t, observation, record, childPID, forwarders)
	requireWindowsGoplsJobRSSLowerBound(t, observation, record.DaemonPID, childPID)
}

func requireWindowsGoplsJobRSSIdentity(t *testing.T, observation windowsGoplsJobRSSObservation, record windowsGoplsBrokerRecordV2) {
	t.Helper()
	if observation.SchemaVersion != 1 || observation.Source != "broker_process_tree_job" || observation.ConfigDigest != record.ConfigDigest ||
		observation.OwnerPID != record.OwnerPID || observation.OwnerStartIdentity != record.OwnerStartIdentity ||
		observation.DaemonPID != record.DaemonPID || observation.DaemonStartIdentity != record.DaemonStartIdentity {
		t.Fatalf("Windows gopls Job RSS observation identity mismatch: %+v record=%+v", observation, record)
	}
}

func requireWindowsGoplsJobRSSMembers(t *testing.T, observation windowsGoplsJobRSSObservation, record windowsGoplsBrokerRecordV2, childPID int, forwarders map[int]bool) {
	t.Helper()
	seen := map[int]bool{}
	for _, pid := range observation.MemberPIDs {
		if pid <= 1 || seen[pid] || pid == record.OwnerPID || forwarders[pid] {
			t.Fatalf("Windows gopls Job RSS observation has invalid member PID %d: %+v", pid, observation)
		}
		seen[pid] = true
	}
	if !seen[record.DaemonPID] || !seen[childPID] {
		t.Fatalf("Windows gopls Job RSS members = %v, want daemon %d and child %d", observation.MemberPIDs, record.DaemonPID, childPID)
	}
}

func requireWindowsGoplsJobRSSLowerBound(t *testing.T, observation windowsGoplsJobRSSObservation, daemonPID, childPID int) {
	t.Helper()
	daemonRSS, daemonErr := hiddenexec.ProcessRSSBytes(daemonPID)
	childRSS, childErr := hiddenexec.ProcessRSSBytes(childPID)
	if daemonErr != nil || childErr != nil {
		t.Fatalf("read fake daemon/child single-PID RSS: %v", errors.Join(daemonErr, childErr))
	}
	if observation.RSSBytes <= daemonRSS || observation.RSSBytes < (daemonRSS+childRSS)*3/4 {
		t.Fatalf("Windows gopls Job RSS = %d, daemon-only=%d child=%d", observation.RSSBytes, daemonRSS, childRSS)
	}
}

func waitForWindowsGoplsObservationEndpointClose(t *testing.T, endpoint string) {
	t.Helper()
	address := strings.TrimPrefix(endpoint, "tcp;")
	deadline := time.Now().Add(6 * time.Second)
	for {
		connection, err := net.DialTimeout("tcp4", address, 100*time.Millisecond)
		if err != nil {
			return
		}
		if err := connection.Close(); err != nil {
			t.Fatalf("close Windows gopls observation convergence probe: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("Windows gopls observation endpoint remained reachable after last close")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
