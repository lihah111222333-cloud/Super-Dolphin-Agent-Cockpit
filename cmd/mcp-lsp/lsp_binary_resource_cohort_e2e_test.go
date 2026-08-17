//go:build e2e && (darwin || linux)

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	gostdruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

const (
	resourceCohortE2EHelperEnv        = "MCP_LSP_RESOURCE_COHORT_E2E_HELPER"
	resourceCohortE2EStateDirEnv      = "MCP_LSP_RESOURCE_COHORT_E2E_STATE_DIR"
	resourceCohortE2EOwnerEnv         = "MCP_LSP_RESOURCE_COHORT_E2E_OWNER"
	resourceCohortE2EMemoryMiBEnv     = "MCP_LSP_RESOURCE_COHORT_E2E_MEMORY_MIB"
	resourceCohortE2EHardLimitMiB     = 192
	resourceCohortE2EChildMemoryMiB   = 96
	resourceCohortE2EFirstBlockLead   = 20 * time.Second
	resourceCohortE2ERecycleBlockLead = 50 * time.Second
	resourceCohortE2ERecoveryCallLead = 80 * time.Second
)

type resourceCohortE2EFixture struct {
	repository string
	worktrees  [2]string
	targets    [2]string
	cacheDir   string
	stateDir   string
	serverBin  string
	sidecarBin string
}

type resourceCohortE2ELease struct {
	SchemaVersion     int    `json:"schema_version"`
	CohortID          string `json:"cohort_id"`
	Role              string `json:"role"`
	OwnerPID          int    `json:"owner_pid"`
	CreatedAtUnixNano int64  `json:"created_at_unix_nano"`
}

type resourceCohortE2EMember struct {
	SchemaVersion        int    `json:"schema_version"`
	OwnerPID             int    `json:"owner_pid"`
	OwnerStartIdentity   string `json:"owner_start_identity"`
	ClientPID            int    `json:"client_pid"`
	ClientStartIdentity  string `json:"client_start_identity"`
	WorkspaceHash        string `json:"workspace_hash"`
	LanguageID           string `json:"language_id"`
	RepositoryCohortID   string `json:"repository_cohort_id"`
	Role                 string `json:"role"`
	RSSBytes             uint64 `json:"rss_bytes"`
	ProcessRSSLimitBytes uint64 `json:"process_rss_limit_bytes"`
	CohortHardLimitBytes uint64 `json:"cohort_hard_limit_bytes"`
	ActiveLeases         int    `json:"active_leases"`
	LastActivityUnixNano int64  `json:"last_activity_unix_nano"`
	UpdatedAtUnixNano    int64  `json:"updated_at_unix_nano"`
	Stale                bool   `json:"stale"`
}

type resourceCohortE2ECallResult struct {
	response mcpLSPBinaryResponse
	err      error
}

type resourceCohortE2EOwner struct {
	launched  time.Time
	client    *mcpLSPBinaryClient
	pid       int
	leasePath string
	lease     resourceCohortE2ELease
}

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryLinkedWorktreesResourceCohortRecycleAndRecover_E2E(t *testing.T) {
	fixture := newResourceCohortE2EFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	primary := startResourceCohortE2EOwner(t, ctx, fixture, 0, "primary")
	secondary := startResourceCohortE2EOwner(t, ctx, fixture, 1, "secondary")
	registerResourceCohortE2ECleanup(t, fixture, primary, secondary)
	assertResourceCohortE2ESharedIdentityAndRSS(t, primary, secondary)
	assertResourceCohortE2EIdleSecondaryRecycle(t, fixture, primary, secondary)
	assertResourceCohortE2ESecondaryRecovery(t, fixture, primary, secondary)
}

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryTypeScriptSecondaryNodeBudget_E2E(t *testing.T) {
	fixture := newResourceCohortE2ETypeScriptFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	primary := startResourceCohortE2ETypeScriptOwner(t, ctx, fixture, 0, "primary")
	secondary := startResourceCohortE2ETypeScriptOwner(t, ctx, fixture, 1, "secondary")
	registerResourceCohortE2ECleanup(t, fixture, primary, secondary)
	assertResourceCohortE2ETypeScriptNodeBudget(t, fixture, primary, secondary)
}

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryResourceCohortMalformedReportQuarantine_E2E(t *testing.T) {
	fixture := newResourceCohortE2EFixture(t)
	badReportPath := writeResourceCohortE2EBadReport(t, fixture.cacheDir)
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	primary := startResourceCohortE2EOwner(t, ctx, fixture, 0, "primary")
	registerResourceCohortE2ECleanup(t, fixture, primary)

	waitUntilResourceCohortE2ETime(t, primary.launched.Add(resourceCohortE2EFirstBlockLead))
	blockResourceCohortE2EOwner(t, fixture.stateDir, "primary", primary.pid)
	call := startResourceCohortE2EToolCall(primary.client, fixture.targets[0])
	waitForResourceCohortE2EBlock(t, fixture.stateDir, "primary", primary.pid)
	waitForResourceCohortE2EPathState(t, badReportPath+".bad", true, 25*time.Second)
	waitForResourceCohortE2EPathState(t, badReportPath, false, time.Second)
	waitForResourceCohortE2ELog(t, primary.client, "unhealthy_members", 3*time.Second)
	releaseResourceCohortE2EBlock(fixture.stateDir, "primary")
	requireResourceCohortE2ECallSuccess(t, primary.client, call, "request held during bad report quarantine")
}

func startResourceCohortE2EOwner(
	t *testing.T,
	ctx context.Context,
	fixture resourceCohortE2EFixture,
	worktreeIndex int,
	role string,
) resourceCohortE2EOwner {
	t.Helper()
	launched := time.Now()
	client := startResourceCohortE2ESidecar(t, ctx, fixture, worktreeIndex, role)
	initializeResourceCohortE2ESidecar(t, client)
	warmResourceCohortE2ESidecar(t, client, fixture.targets[worktreeIndex], role)
	pid := waitForResourceCohortE2ELSPPID(t, fixture.stateDir, role, 0)
	waitForResourceCohortE2EInitialize(t, fixture.stateDir, role, pid)
	leasePath, lease := waitForResourceCohortE2ELease(
		t, fixture.cacheDir, client.cmd.Process.Pid, role, "",
	)
	return resourceCohortE2EOwner{
		launched: launched, client: client, pid: pid, leasePath: leasePath, lease: lease,
	}
}

func registerResourceCohortE2ECleanup(
	t *testing.T,
	fixture resourceCohortE2EFixture,
	owners ...resourceCohortE2EOwner,
) {
	t.Helper()
	t.Cleanup(func() {
		for _, owner := range slices.Backward(owners) {
			owner.client.close(t)
		}
	})
	t.Cleanup(func() {
		releaseResourceCohortE2EBlock(fixture.stateDir, "primary")
		releaseResourceCohortE2EBlock(fixture.stateDir, "secondary")
	})
}

func assertResourceCohortE2ESharedIdentityAndRSS(
	t *testing.T,
	primary, secondary resourceCohortE2EOwner,
) {
	t.Helper()
	if primary.lease.CohortID == "" || primary.lease.CohortID != secondary.lease.CohortID {
		t.Fatalf("linked worktree leases do not share one repo/language/server cohort: primary=%#v secondary=%#v",
			primary.lease, secondary.lease)
	}
	assertResourceCohortE2ERealRSS(t, primary.pid, secondary.pid)
}

func assertResourceCohortE2EIdleSecondaryRecycle(
	t *testing.T,
	fixture resourceCohortE2EFixture,
	primary, secondary resourceCohortE2EOwner,
) {
	t.Helper()
	waitUntilResourceCohortE2ETime(t, primary.launched.Add(resourceCohortE2EFirstBlockLead))
	blockResourceCohortE2EOwner(t, fixture.stateDir, "primary", primary.pid)
	blockResourceCohortE2EOwner(t, fixture.stateDir, "secondary", secondary.pid)
	sampleStarted := time.Now()
	primarySample := startResourceCohortE2EToolCall(primary.client, fixture.targets[0])
	secondarySample := startResourceCohortE2EToolCall(secondary.client, fixture.targets[1])
	waitForResourceCohortE2EBlock(t, fixture.stateDir, "primary", primary.pid)
	waitForResourceCohortE2EBlock(t, fixture.stateDir, "secondary", secondary.pid)
	waitForResourceCohortE2EHealthyProductionSnapshot(
		t, fixture.cacheDir, sampleStarted, primary, secondary,
	)
	releaseResourceCohortE2EBlock(fixture.stateDir, "primary")
	releaseResourceCohortE2EBlock(fixture.stateDir, "secondary")
	requireResourceCohortE2ECallSuccess(t, primary.client, primarySample, "primary production report sample")
	requireResourceCohortE2ECallSuccess(t, secondary.client, secondarySample, "secondary production report sample")

	waitUntilResourceCohortE2ETime(t, primary.launched.Add(resourceCohortE2ERecycleBlockLead))
	blockResourceCohortE2EOwner(t, fixture.stateDir, "primary", primary.pid)
	primaryCall := startResourceCohortE2EToolCall(primary.client, fixture.targets[0])
	waitForResourceCohortE2EBlock(t, fixture.stateDir, "primary", primary.pid)
	waitForResourceCohortE2EProcessState(t, secondary.pid, false, 25*time.Second)
	waitForResourceCohortE2EProcessState(t, primary.pid, true, time.Second)
	waitForResourceCohortE2EProcessState(t, secondary.client.cmd.Process.Pid, true, time.Second)
	member := waitForResourceCohortE2EMember(
		t, fixture.cacheDir, primary.client.cmd.Process.Pid, primary.pid, 1, 15*time.Second,
	)
	if member.RepositoryCohortID != primary.lease.CohortID || member.Role != "primary" {
		t.Fatalf("primary member report = %#v, lease = %#v", member, primary.lease)
	}
	waitForResourceCohortE2EPathState(t, secondary.leasePath, false, 5*time.Second)
	waitForResourceCohortE2EPathState(t, primary.leasePath, true, time.Second)
	waitForResourceCohortE2EMemberAbsence(t, fixture.cacheDir, secondary.client.cmd.Process.Pid, 5*time.Second)
	waitForResourceCohortE2ELog(t, secondary.client, "cohort_rss_limit", 3*time.Second)
	releaseResourceCohortE2EBlock(fixture.stateDir, "primary")
	requireResourceCohortE2ECallSuccess(
		t, primary.client, primaryCall, "active primary request after secondary recycle",
	)
}

func assertResourceCohortE2ESecondaryRecovery(
	t *testing.T,
	fixture resourceCohortE2EFixture,
	primary, secondary resourceCohortE2EOwner,
) {
	t.Helper()
	waitUntilResourceCohortE2ETime(t, secondary.launched.Add(resourceCohortE2ERecoveryCallLead))
	assertResourceCohortE2ENoLazyRecoveryArtifacts(t, fixture, secondary)
	blockResourceCohortE2EOwner(t, fixture.stateDir, "secondary", secondary.pid)
	recoveryRequestedAt := time.Now()
	recoveryCall := startResourceCohortE2EToolCall(secondary.client, fixture.targets[1])
	recoveredPID := waitForResourceCohortE2ELSPPID(t, fixture.stateDir, "secondary", secondary.pid)
	waitForResourceCohortE2EMarkerAfter(
		t, fixture.stateDir, resourceCohortE2EStartedMarker("secondary", recoveredPID), recoveryRequestedAt,
	)
	waitForResourceCohortE2EInitializeAfter(
		t, fixture.stateDir, "secondary", recoveredPID, recoveryRequestedAt,
	)
	waitForResourceCohortE2EBlock(t, fixture.stateDir, "secondary", recoveredPID)
	recoveredPath, recoveredLease := waitForResourceCohortE2ELease(
		t, fixture.cacheDir, secondary.client.cmd.Process.Pid, "secondary", secondary.leasePath,
	)
	if recoveredPID == secondary.pid || recoveredPath == secondary.leasePath ||
		recoveredLease.CohortID != primary.lease.CohortID ||
		recoveredLease.CreatedAtUnixNano < recoveryRequestedAt.UnixNano() {
		t.Fatalf("secondary recovery did not create a fresh server/lease in the same cohort: old_pid=%d new_pid=%d old_lease=%s new_lease=%s lease=%#v",
			secondary.pid, recoveredPID, secondary.leasePath, recoveredPath, recoveredLease)
	}
	member := waitForResourceCohortE2EMemberAfter(
		t, fixture.cacheDir, secondary.client.cmd.Process.Pid, recoveredPID, 1,
		recoveryRequestedAt, 30*time.Second,
	)
	if member.RepositoryCohortID != primary.lease.CohortID || member.Role != "secondary" {
		t.Fatalf("recovered secondary member report = %#v, cohort=%s", member, primary.lease.CohortID)
	}
	waitForResourceCohortE2EProcessState(t, primary.pid, true, time.Second)
	waitForResourceCohortE2EPathState(t, primary.leasePath, true, time.Second)
	releaseResourceCohortE2EBlock(fixture.stateDir, "secondary")
	requireResourceCohortE2ECallSuccess(
		t, secondary.client, recoveryCall, "first real request after lazy secondary recovery",
	)
	waitForResourceCohortE2EProcessState(t, recoveredPID, true, time.Second)
}

// super-dolphin-ci: helper
func TestResourceCohortE2ELanguageServerHelper(t *testing.T) {
	if os.Getenv(resourceCohortE2EHelperEnv) != "1" {
		return
	}
	if err := runResourceCohortE2ELanguageServer(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "resource cohort E2E language server: %v\n", err)
		os.Exit(2)
	}
	os.Exit(0)
}

func newResourceCohortE2EFixture(t *testing.T) resourceCohortE2EFixture {
	t.Helper()
	root := t.TempDir()
	repository := createResourceCohortE2ERepository(t, root)
	worktrees := createResourceCohortE2EWorktrees(t, root, repository)
	cacheDir := createResourceCohortE2EDirectory(t, root, "cache")
	stateDir := createResourceCohortE2EDirectory(t, root, "state")
	return resourceCohortE2EFixture{
		repository: repository,
		worktrees:  worktrees,
		targets: [2]string{
			filepath.Join(worktrees[0], "probe.py"),
			filepath.Join(worktrees[1], "probe.py"),
		},
		cacheDir:   cacheDir,
		stateDir:   stateDir,
		serverBin:  writeResourceCohortE2ELanguageServer(t),
		sidecarBin: buildMcpLSPBinaryForTest(t),
	}
}

func createResourceCohortE2ERepository(t *testing.T, root string) string {
	t.Helper()
	repository := createResourceCohortE2EDirectory(t, root, "repository")
	runResourceCohortE2EGit(t, repository, "init")
	runResourceCohortE2EGit(t, repository, "config", "user.name", "Resource Cohort E2E")
	runResourceCohortE2EGit(t, repository, "config", "user.email", "resource-cohort-e2e@example.invalid")
	if err := os.WriteFile(
		filepath.Join(repository, "pyproject.toml"),
		[]byte("[project]\nname = \"resource-cohort-e2e\"\nversion = \"0.0.0\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("write temporary pyproject: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "probe.py"), []byte("RESOURCE_COHORT = 1\n"), 0o600); err != nil {
		t.Fatalf("write temporary Python source: %v", err)
	}
	runResourceCohortE2EGit(t, repository, "add", "pyproject.toml", "probe.py")
	runResourceCohortE2EGit(t, repository, "commit", "-m", "初始化资源组 E2E 仓库")
	return repository
}

func createResourceCohortE2EWorktrees(t *testing.T, root, repository string) [2]string {
	t.Helper()
	linkedRoot := createResourceCohortE2EDirectory(t, root, "linked")
	worktrees := [2]string{
		filepath.Join(linkedRoot, "primary"),
		filepath.Join(linkedRoot, "secondary"),
	}
	for _, worktree := range worktrees {
		runResourceCohortE2EGit(t, repository, "worktree", "add", "--detach", worktree, "HEAD")
	}
	t.Cleanup(func() { cleanupResourceCohortE2EWorktrees(t, repository, worktrees) })
	return worktrees
}

func cleanupResourceCohortE2EWorktrees(t *testing.T, repository string, worktrees [2]string) {
	t.Helper()
	for index := len(worktrees) - 1; index >= 0; index-- {
		cmd := exec.Command("git", "-C", repository, "worktree", "remove", "--force", worktrees[index])
		if output, err := cmd.CombinedOutput(); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove temporary linked worktree %s: %v; output=%s", worktrees[index], err, output)
		}
	}
	cmd := exec.Command("git", "-C", repository, "worktree", "prune")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("prune temporary linked worktrees: %v; output=%s", err, output)
	}
}

func createResourceCohortE2EDirectory(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("create resource cohort E2E directory %s: %v", path, err)
	}
	return path
}

func runResourceCohortE2EGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	cmdArgs := append([]string{"-C", repository}, args...)
	cmd := exec.Command("git", cmdArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v; output=%s", strings.Join(args, " "), err, output)
	}
}

func writeResourceCohortE2ELanguageServer(t *testing.T) string {
	t.Helper()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve E2E test binary: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "pyright-langserver")
	script := "#!/bin/sh\n" + resourceCohortE2EHelperEnv + "=1 exec " + shellQuote(testBinary) +
		" -test.run=^TestResourceCohortE2ELanguageServerHelper$ -test.count=1 -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write controlled pyright language server: %v", err)
	}
	return dir
}

func writeResourceCohortE2ETypeScriptLanguageServer(t *testing.T) string {
	t.Helper()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve E2E test binary: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "typescript-language-server")
	script := "#!/bin/sh\n" + resourceCohortE2EHelperEnv + "=1 exec " + shellQuote(testBinary) +
		" -test.run=^TestResourceCohortE2ELanguageServerHelper$ -test.count=1 -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write controlled TypeScript language server: %v", err)
	}
	return dir
}

func startResourceCohortE2ESidecar(
	t *testing.T,
	ctx context.Context,
	fixture resourceCohortE2EFixture,
	worktreeIndex int,
	owner string,
) *mcpLSPBinaryClient {
	t.Helper()
	primaryLimit := strconv.Itoa(resourceCohortE2EHardLimitMiB)
	secondaryLimit := strconv.Itoa(resourceCohortE2EHardLimitMiB - 32)
	return startMcpLSPBinaryForTestWithEnv(
		t,
		ctx,
		fixture.sidecarBin,
		fixture.worktrees[worktreeIndex],
		fixture.serverBin,
		[]string{
			"AGENT_LSP_SHARED_CACHE_DIR=" + fixture.cacheDir,
			"AGENT_LSP_COHORT_RSS_LIMIT_MB=" + primaryLimit,
			"AGENT_LSP_PRIMARY_RSS_LIMIT_MB=" + primaryLimit,
			"AGENT_LSP_SECONDARY_RSS_LIMIT_MB=" + secondaryLimit,
			"AGENT_LSP_NODE_PRIMARY_HEAP_LIMIT_MB=128",
			"AGENT_LSP_NODE_SECONDARY_HEAP_LIMIT_MB=128",
			resourceCohortE2EStateDirEnv + "=" + fixture.stateDir,
			resourceCohortE2EOwnerEnv + "=" + owner,
			resourceCohortE2EMemoryMiBEnv + "=" + strconv.Itoa(resourceCohortE2EChildMemoryMiB),
			"MCP_LSP_PROCESS_IDLE_TIMEOUT=3m",
		},
	)
}

func startResourceCohortE2ETypeScriptSidecar(
	t *testing.T,
	ctx context.Context,
	fixture resourceCohortE2EFixture,
	worktreeIndex int,
	owner string,
) *mcpLSPBinaryClient {
	t.Helper()
	return startMcpLSPBinaryForTestWithEnv(
		t,
		ctx,
		fixture.sidecarBin,
		fixture.worktrees[worktreeIndex],
		fixture.serverBin,
		[]string{
			"AGENT_LSP_SHARED_CACHE_DIR=" + fixture.cacheDir,
			"AGENT_LSP_COHORT_RSS_LIMIT_MB=5120",
			"AGENT_LSP_PRIMARY_RSS_LIMIT_MB=2560",
			"AGENT_LSP_SECONDARY_RSS_LIMIT_MB=2560",
			resourceCohortE2EStateDirEnv + "=" + fixture.stateDir,
			resourceCohortE2EOwnerEnv + "=" + owner,
			resourceCohortE2EMemoryMiBEnv + "=8",
			"MCP_LSP_PROCESS_IDLE_TIMEOUT=3m",
		},
	)
}

func initializeResourceCohortE2ESidecar(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
}

func warmResourceCohortE2ESidecar(t *testing.T, client *mcpLSPBinaryClient, target, owner string) {
	t.Helper()
	response := client.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, response, "warm controlled "+owner+" language server")
}

func runResourceCohortE2ELanguageServer() error {
	stateDir, owner, memoryMiB, err := resourceCohortE2ELanguageServerConfig()
	if err != nil {
		return err
	}
	payload := allocateResourceCohortE2EMemory(memoryMiB)
	defer gostdruntime.KeepAlive(payload)
	pid := os.Getpid()
	if err := writeResourceCohortE2EMarker(stateDir, resourceCohortE2EStartedMarker(owner, pid)); err != nil {
		return err
	}
	if err := writeResourceCohortE2ENodeOptions(stateDir, owner, pid); err != nil {
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	var goroutines sync.WaitGroup
	writer := &fakeLSPWriter{w: os.Stdout, goroutines: &goroutines}
	defer goroutines.Wait()
	return serveResourceCohortE2ELanguageServer(reader, writer, stateDir, owner, pid)
}

func resourceCohortE2ELanguageServerConfig() (string, string, int, error) {
	stateDir := strings.TrimSpace(os.Getenv(resourceCohortE2EStateDirEnv))
	owner := strings.TrimSpace(os.Getenv(resourceCohortE2EOwnerEnv))
	if stateDir == "" || owner == "" {
		return "", "", 0, errors.New("resource cohort E2E state directory and owner are required")
	}
	memoryMiB, err := strconv.Atoi(strings.TrimSpace(os.Getenv(resourceCohortE2EMemoryMiBEnv)))
	if err != nil || memoryMiB <= 0 {
		return "", "", 0, fmt.Errorf(
			"invalid resource cohort E2E memory MiB: %q", os.Getenv(resourceCohortE2EMemoryMiBEnv),
		)
	}
	return stateDir, owner, memoryMiB, nil
}

func allocateResourceCohortE2EMemory(memoryMiB int) []byte {
	payload := make([]byte, memoryMiB*1024*1024)
	for offset := 0; offset < len(payload); offset += os.Getpagesize() {
		payload[offset] = byte(offset)
	}
	return payload
}

func serveResourceCohortE2ELanguageServer(
	reader *bufio.Reader,
	writer *fakeLSPWriter,
	stateDir, owner string,
	pid int,
) error {
	for {
		raw, err := readFakeLSPFramedMessage(reader)
		if err != nil {
			return nil
		}
		exit, err := handleResourceCohortE2ELSPRequest(writer, stateDir, owner, pid, raw)
		if err != nil {
			return err
		}
		if exit {
			return nil
		}
	}
}

func handleResourceCohortE2ELSPRequest(
	writer *fakeLSPWriter,
	stateDir, owner string,
	pid int,
	raw []byte,
) (bool, error) {
	var request fakeLSPRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return false, fmt.Errorf("decode controlled LSP request: %w", err)
	}
	if request.Method == "exit" {
		return true, nil
	}
	if len(request.ID) == 0 {
		return false, nil
	}
	result, err := resourceCohortE2ELSPResult(stateDir, owner, pid, request)
	if err != nil {
		return false, err
	}
	if err := writer.writeResponse(request.ID, result); err != nil {
		return false, fmt.Errorf("write controlled LSP response: %w", err)
	}
	return false, nil
}

func resourceCohortE2ELSPResult(stateDir, owner string, pid int, request fakeLSPRequest) (any, error) {
	switch request.Method {
	case "initialize":
		if err := writeResourceCohortE2EMarker(stateDir, resourceCohortE2EInitializedMarker(owner, pid)); err != nil {
			return nil, err
		}
		return map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":       1,
				"documentSymbolProvider": true,
			},
		}, nil
	case "shutdown":
		return nil, nil
	case "textDocument/documentSymbol":
		if err := waitForResourceCohortE2ERelease(stateDir, owner, pid); err != nil {
			return nil, err
		}
		return []map[string]any{{
			"name": owner + "Symbol",
			"kind": 13,
			"range": map[string]any{
				"start": map[string]any{"line": 0, "character": 0},
				"end":   map[string]any{"line": 0, "character": 15},
			},
			"selectionRange": map[string]any{
				"start": map[string]any{"line": 0, "character": 0},
				"end":   map[string]any{"line": 0, "character": 15},
			},
		}}, nil
	default:
		return nil, nil
	}
}

func waitForResourceCohortE2ERelease(stateDir, owner string, pid int) error {
	blockPath := filepath.Join(stateDir, owner+".block")
	if _, err := os.Stat(blockPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect controlled LSP block marker: %w", err)
	}
	if err := writeResourceCohortE2EMarker(stateDir, resourceCohortE2EBlockedMarker(owner, pid)); err != nil {
		return err
	}
	releasePath := filepath.Join(stateDir, owner+".release")
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(releasePath); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect controlled LSP release marker: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for controlled LSP owner %s release", owner)
}

func writeResourceCohortE2EMarker(stateDir, name string) error {
	path := filepath.Join(stateDir, name)
	payload := strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		return fmt.Errorf("write resource cohort E2E marker %s: %w", name, err)
	}
	return nil
}

func resourceCohortE2EStartedMarker(owner string, pid int) string {
	return owner + ".started." + strconv.Itoa(pid)
}

func resourceCohortE2EInitializedMarker(owner string, pid int) string {
	return owner + ".initialized." + strconv.Itoa(pid)
}

func resourceCohortE2EBlockedMarker(owner string, pid int) string {
	return owner + ".blocked." + strconv.Itoa(pid)
}

func blockResourceCohortE2EOwner(t *testing.T, stateDir, owner string, pid int) {
	t.Helper()
	releasePath := filepath.Join(stateDir, owner+".release")
	if err := os.Remove(releasePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove stale resource cohort release marker: %v", err)
	}
	blockedPath := filepath.Join(stateDir, resourceCohortE2EBlockedMarker(owner, pid))
	if err := os.Remove(blockedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove stale resource cohort blocked marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, owner+".block"), []byte("block"), 0o600); err != nil {
		t.Fatalf("write resource cohort block marker: %v", err)
	}
}

func releaseResourceCohortE2EBlock(stateDir, owner string) {
	_ = os.WriteFile(filepath.Join(stateDir, owner+".release"), []byte("release"), 0o600)
}

func waitForResourceCohortE2ELSPPID(t *testing.T, stateDir, owner string, excludedPID int) int {
	t.Helper()
	prefix := owner + ".started."
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(stateDir)
		if err != nil {
			t.Fatalf("read resource cohort E2E state directory: %v", err)
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), prefix) {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), prefix))
			if err == nil && pid > 1 && pid != excludedPID {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s controlled LSP PID distinct from %d", owner, excludedPID)
	return 0
}

func waitForResourceCohortE2EInitialize(t *testing.T, stateDir, owner string, pid int) {
	t.Helper()
	waitForResourceCohortE2EPathState(
		t,
		filepath.Join(stateDir, resourceCohortE2EInitializedMarker(owner, pid)),
		true,
		10*time.Second,
	)
}

func waitForResourceCohortE2EBlock(t *testing.T, stateDir, owner string, pid int) {
	t.Helper()
	waitForResourceCohortE2EPathState(
		t,
		filepath.Join(stateDir, resourceCohortE2EBlockedMarker(owner, pid)),
		true,
		10*time.Second,
	)
}

func startResourceCohortE2EToolCall(client *mcpLSPBinaryClient, target string) <-chan resourceCohortE2ECallResult {
	result := make(chan resourceCohortE2ECallResult, 1)
	runtimesafe.SafeGo(context.Background(), nil, "mcp-lsp.resource-cohort-e2e-call", func(context.Context) {
		response, err := callResourceCohortE2EMCP(client, "tools/call", map[string]any{
			"name": "structure",
			"arguments": map[string]any{
				"action":    "document_symbol",
				"file_path": target,
			},
			"_cwd":            client.cmd.Dir,
			"_workspaceRoots": []string{client.cmd.Dir},
		})
		result <- resourceCohortE2ECallResult{response: response, err: err}
	})
	return result
}

func callResourceCohortE2EMCP(
	client *mcpLSPBinaryClient,
	method string,
	params map[string]any,
) (mcpLSPBinaryResponse, error) {
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("encode %s request: %w", method, err)
	}
	if _, err := client.stdin.Write(append(payload, '\n')); err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("write %s request: %w", method, err)
	}
	line, err := client.stdout.ReadBytes('\n')
	if err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("read %s response: %w", method, err)
	}
	var response mcpLSPBinaryResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("decode %s response %q: %w", method, line, err)
	}
	if response.Error != nil {
		return response, fmt.Errorf("%s returned JSON-RPC error %d: %s", method, response.Error.Code, response.Error.Message)
	}
	return response, nil
}

func requireResourceCohortE2ECallSuccess(
	t *testing.T,
	client *mcpLSPBinaryClient,
	result <-chan resourceCohortE2ECallResult,
	description string,
) {
	t.Helper()
	select {
	case outcome := <-result:
		if outcome.err != nil {
			t.Fatalf("%s failed: %v; stderr=%s", description, outcome.err, client.stderrString())
		}
		requireMCPToolSuccess(t, client, outcome.response, description)
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not complete after release; stderr=%s", description, client.stderrString())
	}
}
