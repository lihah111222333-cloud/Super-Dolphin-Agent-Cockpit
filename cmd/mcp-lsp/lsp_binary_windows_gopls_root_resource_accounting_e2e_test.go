//go:build windows && e2e

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const windowsGoplsRootResourceLimitBytes = uint64(1 << 20)

type windowsGoplsRootResourceReceipt struct {
	SchemaVersion       int    `json:"schema_version"`
	ConfigDigest        string `json:"config_digest"`
	CohortID            string `json:"cohort_id"`
	Generation          uint64 `json:"generation"`
	Source              string `json:"source"`
	DaemonPID           int    `json:"daemon_pid"`
	DaemonStartIdentity string `json:"daemon_start_identity"`
	MemberPIDs          []int  `json:"member_pids"`
	RSSBytes            uint64 `json:"rss_bytes"`
	RSSLimitBytes       uint64 `json:"rss_limit_bytes"`
	ActiveLeases        int    `json:"active_leases"`
	Decision            string `json:"decision"`
}

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWindowsGoplsRootCohortJobRSSAccountingDefersActiveLeasesE2E(t *testing.T) {
	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	fixture := t.TempDir()
	argsLog, cacheRoot := filepath.Join(fixture, "args.log"), filepath.Join(fixture, "cache")
	install := buildWindowsGoplsShortIdlePrecheckTestInstall(t)
	env := windowsGoplsSidecarEnv(t, install, fakeGoplsArgsLogEnv+"="+argsLog,
		"AGENT_LSP_SHARED_CACHE_DIR="+cacheRoot, "AGENT_LSP_GO_RSS_LIMIT_MB=1", "MCP_LSP_FAKE_GOPLS_RSS_CHILD=1")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var exact []windowsGoplsProvisionalIdentity
	registerWindowsGoplsExactCleanup(t, &exact)
	clients := startWindowsGoplsClients(t, ctx, install.Binary, roots, filepath.Dir(install.Gopls), env, targets)

	daemonPID, childPID, forwarders := requireWindowsGoplsRSSInvocationTopology(
		t, waitForFakeGoplsInvocations(t, argsLog, 4), install.Gopls,
	)
	record := waitForWindowsGoplsBrokerRecord(t, cacheRoot)
	exact = []windowsGoplsProvisionalIdentity{
		{PID: record.OwnerPID, StartIdentity: record.OwnerStartIdentity},
		{PID: record.DaemonPID, StartIdentity: record.DaemonStartIdentity},
		requireWindowsGoplsStartIdentity(t, childPID),
	}
	requireWindowsGoplsBrokerProcessIdentities(t, record, daemonPID)
	path, receipt := waitForWindowsGoplsRootResourceReceipt(t, cacheRoot)
	requireWindowsGoplsRootResourceContract(t, path, receipt, record, childPID, forwarders)
	requireWindowsGoplsRootResourceStable(t, cacheRoot, path, receipt)

	for index, client := range clients {
		result := client.callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": targets[index]})
		requireMCPToolSuccess(t, client, result, "Windows gopls forwarder after active-lease RSS defer")
	}
	requireWindowsGoplsRootResourceProcessesAlive(t, record, childPID, forwarders)
}

func waitForWindowsGoplsRootResourceReceipt(t *testing.T, cacheRoot string) (string, windowsGoplsRootResourceReceipt) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		paths, err := windowsGoplsRootResourcePaths(cacheRoot)
		if err != nil {
			t.Fatalf("find Windows gopls root resource receipt: %v", err)
		}
		if len(paths) == 1 {
			return paths[0], readWindowsGoplsRootResourceReceipt(t, paths[0])
		}
		if len(paths) > 1 || time.Now().After(deadline) {
			t.Fatalf("Windows gopls root resource receipts = %v, want exactly one within 5s", paths)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func windowsGoplsRootResourcePaths(cacheRoot string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(cacheRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == "resource.json" {
			paths = append(paths, path)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return paths, err
}

func readWindowsGoplsRootResourceReceipt(t *testing.T, path string) windowsGoplsRootResourceReceipt {
	t.Helper()
	var receipt windowsGoplsRootResourceReceipt
	if err := runtimeServerReadGoplsRootCohortJSON(path, &receipt, 16*1024); err != nil {
		t.Fatalf("read strict Windows gopls root resource receipt: %v", err)
	}
	return receipt
}

func requireWindowsGoplsRootResourceContract(t *testing.T, path string, receipt windowsGoplsRootResourceReceipt, record windowsGoplsBrokerRecordV2, childPID int, forwarders map[int]bool) {
	t.Helper()
	state, err := runtimeServerReadGoplsRootCohortState(filepath.Join(filepath.Dir(path), "state.json"))
	if err != nil {
		t.Fatalf("read Windows gopls root cohort state for resource receipt: %v", err)
	}
	requireWindowsGoplsRootResourceIdentity(t, receipt, record.ConfigDigest, state.ConfigDigest, state.Config.CohortID)
	if receipt.Source != "broker_process_tree_job" || receipt.DaemonPID != record.DaemonPID ||
		receipt.DaemonStartIdentity != record.DaemonStartIdentity {
		t.Fatalf("Windows gopls root resource Job source mismatch: receipt=%+v record=%+v", receipt, record)
	}
	requireWindowsGoplsJobRSSMembers(t, windowsGoplsJobRSSObservation{MemberPIDs: receipt.MemberPIDs}, record, childPID, forwarders)
	if receipt.RSSLimitBytes != windowsGoplsRootResourceLimitBytes || receipt.RSSBytes <= receipt.RSSLimitBytes ||
		receipt.ActiveLeases != 2 || receipt.Decision != "defer_active_leases" {
		t.Fatalf("Windows gopls root resource decision mismatch: %+v", receipt)
	}
}

func requireWindowsGoplsRootResourceIdentity(t *testing.T, receipt windowsGoplsRootResourceReceipt, recordDigest, stateDigest, cohortID string) {
	t.Helper()
	if receipt.SchemaVersion != 1 || receipt.Generation != 1 || receipt.ConfigDigest == "" ||
		receipt.ConfigDigest != recordDigest || receipt.ConfigDigest != stateDigest ||
		receipt.CohortID == "" || receipt.CohortID != cohortID {
		t.Fatalf("Windows gopls root resource identity mismatch: receipt=%+v record_digest=%q state_digest=%q cohort_id=%q", receipt, recordDigest, stateDigest, cohortID)
	}
}

func requireWindowsGoplsRootResourceStable(t *testing.T, cacheRoot, path string, receipt windowsGoplsRootResourceReceipt) {
	t.Helper()
	time.Sleep(1250 * time.Millisecond)
	paths, err := windowsGoplsRootResourcePaths(cacheRoot)
	if err != nil || len(paths) != 1 || paths[0] != path {
		t.Fatalf("Windows gopls root resource receipt lost uniqueness: paths=%v err=%v", paths, err)
	}
	current := readWindowsGoplsRootResourceReceipt(t, path)
	if current.Generation != receipt.Generation || current.ConfigDigest != receipt.ConfigDigest || current.CohortID != receipt.CohortID {
		t.Fatalf("Windows gopls root resource sample was double-counted: first=%+v current=%+v", receipt, current)
	}
}

func requireWindowsGoplsRootResourceProcessesAlive(t *testing.T, record windowsGoplsBrokerRecordV2, childPID int, forwarders map[int]bool) {
	t.Helper()
	requireWindowsProcessAlive(t, record.OwnerPID, "gopls broker")
	requireWindowsProcessAlive(t, record.DaemonPID, "gopls daemon")
	requireWindowsProcessAlive(t, childPID, "gopls RSS child")
	for pid := range forwarders {
		requireWindowsProcessAlive(t, pid, "gopls forwarder")
	}
}
