//go:build darwin || linux

package multilsp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

const (
	resourceCohortIntegrationTimeout  = 10 * time.Second
	resourceCohortIntegrationHardMiB  = 96
	resourceCohortIntegrationChildMiB = 48

	resourceCohortOwnerHelperEnv  = "MCP_LSP_COHORT_OWNER_HELPER"
	resourceCohortChildHelperEnv  = "MCP_LSP_COHORT_CHILD_HELPER"
	resourceCohortFixtureRootEnv  = "MCP_LSP_COHORT_FIXTURE_ROOT"
	resourceCohortLeaseDirEnv     = "MCP_LSP_COHORT_LEASE_DIR"
	resourceCohortOwnerNameEnv    = "MCP_LSP_COHORT_OWNER_NAME"
	resourceCohortOwnerRoleEnv    = "MCP_LSP_COHORT_OWNER_ROLE"
	resourceCohortActiveLeasesEnv = "MCP_LSP_COHORT_ACTIVE_LEASES"
	resourceCohortChildMiBEnv     = "MCP_LSP_COHORT_CHILD_MIB"
	resourceCohortActivityAgeEnv  = "MCP_LSP_COHORT_ACTIVITY_AGE_SECONDS"
	resourceCohortResultPathEnv   = "MCP_LSP_COHORT_RESULT_PATH"
	resourceCohortReadyPathEnv    = "MCP_LSP_COHORT_READY_PATH"
	resourceCohortStopPathEnv     = "MCP_LSP_COHORT_STOP_PATH"
)

type resourceCohortIntegrationFixture struct {
	binary      string
	root        string
	resourceDir string
	leaseDir    string
	cohortID    string
}

type resourceCohortOwnerConfig struct {
	name            string
	role            string
	activeLeases    int
	childMiB        int
	activityAgeSecs int
}

type resourceCohortOwnerResult struct {
	OwnerPID    int    `json:"owner_pid"`
	ClientPID   int    `json:"client_pid"`
	LeasePath   string `json:"lease_path"`
	RSSBytes    uint64 `json:"rss_bytes"`
	Aggregate   uint64 `json:"aggregate_rss_bytes"`
	HardLimit   uint64 `json:"hard_limit_bytes"`
	Active      int    `json:"active_leases"`
	EvictSelf   bool   `json:"evict_self"`
	ReportCount int    `json:"report_count"`
}

type resourceCohortOwnerProcess struct {
	cmd        *exec.Cmd
	tree       *hiddenexec.ProcessTree
	output     *bytes.Buffer
	resultPath string
	readyPath  string
	stopPath   string
	waited     bool
}

func TestResourceCohortIntegratesTwoRealOwnersAndOwnerOnlyRecycle(t *testing.T) {
	fixture := newResourceCohortIntegrationFixture(t)
	primary := startResourceCohortOwner(t, fixture, resourceCohortOwnerConfig{
		name:         "primary",
		role:         ResourceCohortRolePrimary,
		activeLeases: 1,
		childMiB:     resourceCohortIntegrationChildMiB,
	})
	primaryResult := waitForResourceCohortOwnerResult(t, primary)
	secondary := startResourceCohortOwner(t, fixture, resourceCohortOwnerConfig{
		name:            "secondary",
		role:            ResourceCohortRoleSecondary,
		childMiB:        resourceCohortIntegrationChildMiB,
		activityAgeSecs: 60,
	})
	secondaryResult := waitForResourceCohortOwnerResult(t, secondary)

	assertTwoOwnerResourceCohortDecision(t, primaryResult, secondaryResult)
	assertSecondaryOwnerOnlyCleanup(t, fixture, primaryResult, secondaryResult)
	stopResourceCohortOwner(t, secondary)
	assertResourceCohortProcessState(t, primaryResult.OwnerPID, true)
	assertResourceCohortProcessState(t, primaryResult.ClientPID, true)

	stopResourceCohortOwner(t, primary)
	assertResourceCohortFullyCleaned(t, fixture, primaryResult)
	assertResourceCohortLazyReportRebuild(t, fixture)
}

func TestEvaluateResourceCohortJoinsBadReportAndMissingGoplsDaemon(t *testing.T) {
	t.Setenv(goplsCohortHardLimitEnv, "128")
	t.Setenv(lspGoRSSLimitEnv, "64")
	resourceDir := filepath.Join(t.TempDir(), "cohort")
	membersDir := filepath.Join(resourceDir, resourceCohortMembersDir)
	if err := os.MkdirAll(membersDir, 0o700); err != nil {
		t.Fatalf("create resource cohort members directory: %v", err)
	}
	badReportPath := filepath.Join(membersDir, "bad-report.json")
	if err := os.WriteFile(badReportPath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write malformed cohort report: %v", err)
	}
	cohortID := fmt.Sprintf("missing-daemon-%d-%d", os.Getpid(), time.Now().UnixNano())
	current := &client{transport: &transport{cmd: &exec.Cmd{
		Args:    []string{"gopls", "-remote=auto;" + cohortID},
		Env:     []string{ResourceCohortDirEnv + "=" + resourceDir},
		Process: &os.Process{Pid: os.Getpid()},
	}}}
	now := time.Now()
	workspace := workspaceClient{
		key:          "combined-resource-cohort-probe-failure",
		languageID:   "go",
		client:       current,
		lastActivity: now,
	}
	policy := goplsResourceProcessPolicy(current, workspace.languageID)
	decision, err := evaluateResourceCohort(current, workspace, policy, 32*1024*1024, os.Getpid(), 1, now)
	if err == nil {
		t.Fatal("evaluateResourceCohort() error = nil, want joined report and daemon failures")
	}
	for _, want := range []string{"load LSP resource cohort member bad-report.json", "shared gopls cohort is active but daemon RSS was not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("evaluateResourceCohort() error = %v, want component %q", err, want)
		}
	}
	if decision.UnhealthyMembers != 2 || decision.EvictSelf || decision.AggregateRSS < decision.HardLimit {
		t.Fatalf("combined conservative decision = %#v, want two unhealthy inputs, active owner retained, over hard limit", decision)
	}
	if _, err := os.Stat(badReportPath + ".bad"); err != nil {
		t.Fatalf("malformed report was not quarantined: %v", err)
	}
}

func TestShutdownResourceCohortWorkspaceRestoresCloseFailureForRetry(t *testing.T) {
	closeErr := errors.New("injected first cohort close failure")
	client := &failingCloseP2Client{
		p2LifecycleClient: &p2LifecycleClient{healthy: true},
		err:               closeErr,
	}
	workspace := &workspaceClient{key: "cohort-close-retry", languageID: "go", client: client}
	workspace.generation = 1
	workspace.state = workspaceStateIdleCountdown
	workspace.idleSince = time.Now().Add(-2 * idleTimeoutForTest())
	workspace.lastActivity = workspace.idleSince
	mgr := &manager{idleTimeout: idleTimeoutForTest(), workspaces: map[string]*workspaceClient{workspace.key: workspace}}

	recycled, err := shutdownResourceCohortWorkspace(mgr, *workspace)
	if recycled || !errors.Is(err, closeErr) {
		t.Fatalf("first cohort shutdown = (%v, %v), want (false, %v)", recycled, err, closeErr)
	}
	restored := snapshotWorkspaceClients(mgr)
	if len(restored) != 1 || restored[0].client != client {
		t.Fatalf("workspace after failed close = %#v, want original cleanup owner restored", restored)
	}
	recycled, err = shutdownResourceCohortWorkspace(mgr, restored[0])
	if err != nil || !recycled {
		t.Fatalf("retry cohort shutdown = (%v, %v), want (true, nil)", recycled, err)
	}
	if remaining := snapshotWorkspaceClients(mgr); len(remaining) != 0 {
		t.Fatalf("workspace after successful retry = %#v, want removed", remaining)
	}
}

func TestResourceCohortOwnerProcessHelper(t *testing.T) {
	if os.Getenv(resourceCohortOwnerHelperEnv) != "1" {
		t.Skip("resource cohort owner helper")
	}
	fixtureRoot := requiredResourceCohortHelperEnv(t, resourceCohortFixtureRootEnv)
	cohortID := requiredResourceCohortHelperEnv(t, ResourceRepositoryCohortIDEnv)
	role := requiredResourceCohortHelperEnv(t, resourceCohortOwnerRoleEnv)
	ownerName := requiredResourceCohortHelperEnv(t, resourceCohortOwnerNameEnv)
	activeLeases := requiredResourceCohortHelperInt(t, resourceCohortActiveLeasesEnv)
	childMiB := requiredResourceCohortHelperInt(t, resourceCohortChildMiBEnv)
	activityAge := requiredResourceCohortHelperInt(t, resourceCohortActivityAgeEnv)
	leasePath := writeResourceCohortOwnerLease(t, role, ownerName, cohortID)
	current, closed := startResourceCohortMemoryClient(t, leasePath, cohortID, role, childMiB)
	defer func() {
		if !*closed {
			_ = closeResourceCohortClientForTest(t, current)
		}
	}()

	result := evaluateResourceCohortOwnerHelper(
		t,
		current,
		leasePath,
		activeLeases,
		time.Duration(activityAge)*time.Second,
	)
	if result.EvictSelf {
		if err := closeResourceCohortClientForTest(t, current); err != nil {
			t.Fatalf("close evicted resource cohort client: %v", err)
		}
		*closed = true
	}
	result.ReportCount = countResourceCohortReports(t, filepath.Join(fixtureRoot, resourceCohortMembersDir))
	writeResourceCohortOwnerResult(t, result)
	waitForResourceCohortMarker(t, requiredResourceCohortHelperEnv(t, resourceCohortStopPathEnv))
	if !*closed {
		if err := closeResourceCohortClientForTest(t, current); err != nil {
			t.Fatalf("close resource cohort client: %v", err)
		}
		*closed = true
	}
}

func TestResourceCohortMemoryChildHelper(t *testing.T) {
	if os.Getenv(resourceCohortChildHelperEnv) != "1" {
		t.Skip("resource cohort memory child helper")
	}
	childMiB := requiredResourceCohortHelperInt(t, resourceCohortChildMiBEnv)
	payload := make([]byte, childMiB*1024*1024)
	for offset := 0; offset < len(payload); offset += os.Getpagesize() {
		payload[offset] = byte(offset)
	}
	readyPath := requiredResourceCohortHelperEnv(t, resourceCohortReadyPathEnv)
	if err := os.WriteFile(readyPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("write resource cohort child ready marker: %v", err)
	}
	time.Sleep(30 * time.Second)
	runtime.KeepAlive(payload)
}

func newResourceCohortIntegrationFixture(t *testing.T) resourceCohortIntegrationFixture {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	root := t.TempDir()
	resourceDir := filepath.Join(root, "resource")
	leaseRoot := filepath.Join(root, "leases")
	cohortID := "repo-real-owner-integration"
	for _, path := range []string{resourceDir, leaseRoot, filepath.Join(leaseRoot, cohortID)} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("create resource cohort integration directory %s: %v", path, err)
		}
	}
	return resourceCohortIntegrationFixture{
		binary:      binary,
		root:        root,
		resourceDir: resourceDir,
		leaseDir:    filepath.Join(leaseRoot, cohortID),
		cohortID:    cohortID,
	}
}

func startResourceCohortOwner(
	t *testing.T,
	fixture resourceCohortIntegrationFixture,
	config resourceCohortOwnerConfig,
) *resourceCohortOwnerProcess {
	t.Helper()
	resultPath := filepath.Join(fixture.root, config.name+".result.json")
	readyPath := filepath.Join(fixture.root, config.name+".child.ready")
	stopPath := filepath.Join(fixture.root, config.name+".stop")
	cmd := hiddenexec.Command(
		fixture.binary,
		"-test.run=^TestResourceCohortOwnerProcessHelper$",
		"-test.count=1",
	)
	cmd.Env = append(os.Environ(),
		resourceCohortOwnerHelperEnv+"=1",
		resourceCohortFixtureRootEnv+"="+fixture.resourceDir,
		resourceCohortLeaseDirEnv+"="+fixture.leaseDir,
		ResourceRepositoryCohortIDEnv+"="+fixture.cohortID,
		resourceCohortOwnerNameEnv+"="+config.name,
		resourceCohortOwnerRoleEnv+"="+config.role,
		resourceCohortActiveLeasesEnv+"="+strconv.Itoa(config.activeLeases),
		resourceCohortChildMiBEnv+"="+strconv.Itoa(config.childMiB),
		resourceCohortActivityAgeEnv+"="+strconv.Itoa(config.activityAgeSecs),
		resourceCohortResultPathEnv+"="+resultPath,
		resourceCohortReadyPathEnv+"="+readyPath,
		resourceCohortStopPathEnv+"="+stopPath,
		ResourceCohortHardLimitMBEnv+"="+strconv.Itoa(resourceCohortIntegrationHardMiB),
	)
	output := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = output, output
	tree, err := hiddenexec.StartProcessTree(cmd)
	if err != nil {
		t.Fatalf("start resource cohort owner %s: %v", config.name, err)
	}
	owner := &resourceCohortOwnerProcess{
		cmd:        cmd,
		tree:       tree,
		output:     output,
		resultPath: resultPath,
		readyPath:  readyPath,
		stopPath:   stopPath,
	}
	t.Cleanup(owner.forceCleanup)
	return owner
}

func waitForResourceCohortOwnerResult(
	t *testing.T,
	owner *resourceCohortOwnerProcess,
) resourceCohortOwnerResult {
	t.Helper()
	deadline := time.Now().Add(resourceCohortIntegrationTimeout)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(owner.resultPath)
		if err == nil {
			var result resourceCohortOwnerResult
			if err := json.Unmarshal(payload, &result); err != nil {
				t.Fatalf("decode resource cohort owner result: %v", err)
			}
			return result
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read resource cohort owner result: %v", err)
		}
		assertResourceCohortOwnerStillRunning(t, owner)
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for resource cohort owner result; output=%s", owner.output.String())
	return resourceCohortOwnerResult{}
}

func assertResourceCohortOwnerStillRunning(t *testing.T, owner *resourceCohortOwnerProcess) {
	t.Helper()
	alive, err := hiddenexec.ProcessAlive(owner.cmd.Process.Pid)
	if err != nil {
		t.Fatalf("probe resource cohort owner process: %v", err)
	}
	if !alive {
		waitErr := owner.wait()
		t.Fatalf("resource cohort owner exited early: %v; output=%s", waitErr, owner.output.String())
	}
}

func stopResourceCohortOwner(t *testing.T, owner *resourceCohortOwnerProcess) {
	t.Helper()
	if owner.waited {
		return
	}
	if err := os.WriteFile(owner.stopPath, []byte("stop"), 0o600); err != nil {
		t.Fatalf("signal resource cohort owner stop: %v", err)
	}
	if err := owner.wait(); err != nil {
		t.Fatalf("wait resource cohort owner: %v; output=%s", err, owner.output.String())
	}
}

func (owner *resourceCohortOwnerProcess) wait() error {
	if owner.waited {
		return nil
	}
	owner.waited = true
	return errors.Join(owner.cmd.Wait(), owner.tree.Release())
}

func (owner *resourceCohortOwnerProcess) forceCleanup() {
	if owner == nil || owner.waited {
		return
	}
	_ = os.WriteFile(owner.stopPath, []byte("stop"), 0o600)
	time.Sleep(50 * time.Millisecond)
	_ = owner.tree.Terminate()
	if payload, err := os.ReadFile(owner.readyPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(payload))); err == nil && pid > 1 {
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Kill()
			}
		}
	}
	_ = owner.cmd.Wait()
	_ = owner.tree.Release()
	owner.waited = true
}

func assertTwoOwnerResourceCohortDecision(
	t *testing.T,
	primary, secondary resourceCohortOwnerResult,
) {
	t.Helper()
	if primary.OwnerPID == secondary.OwnerPID || primary.ClientPID == secondary.ClientPID {
		t.Fatalf("resource cohort owners/clients are not distinct: primary=%#v secondary=%#v", primary, secondary)
	}
	if primary.EvictSelf || primary.Active != 1 {
		t.Fatalf("active primary decision = %#v, want protected active owner", primary)
	}
	if !secondary.EvictSelf || secondary.Active != 0 {
		t.Fatalf("idle secondary decision = %#v, want owner-only self eviction", secondary)
	}
	assertRealChildResourceCohortRSS(t, primary, secondary)
	assertResourceCohortProcessState(t, primary.OwnerPID, true)
	assertResourceCohortProcessState(t, secondary.OwnerPID, true)
}

func assertRealChildResourceCohortRSS(
	t *testing.T,
	primary, secondary resourceCohortOwnerResult,
) {
	t.Helper()
	if secondary.Aggregate < primary.RSSBytes+secondary.RSSBytes || secondary.Aggregate <= secondary.HardLimit {
		t.Fatalf("secondary aggregate = %d, own+primary=%d hard=%d",
			secondary.Aggregate, primary.RSSBytes+secondary.RSSBytes, secondary.HardLimit)
	}
	minimumRealRSS := uint64(resourceCohortIntegrationChildMiB/2) * 1024 * 1024
	if primary.RSSBytes < minimumRealRSS || secondary.RSSBytes < minimumRealRSS {
		t.Fatalf("real child RSS samples are too small: primary=%d secondary=%d", primary.RSSBytes, secondary.RSSBytes)
	}
}

func assertSecondaryOwnerOnlyCleanup(
	t *testing.T,
	fixture resourceCohortIntegrationFixture,
	primary, secondary resourceCohortOwnerResult,
) {
	t.Helper()
	assertResourceCohortProcessState(t, primary.ClientPID, true)
	assertResourceCohortProcessState(t, secondary.ClientPID, false)
	if _, err := os.Stat(primary.LeasePath); err != nil {
		t.Fatalf("primary lease disappeared during secondary recycle: %v", err)
	}
	if _, err := os.Stat(secondary.LeasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secondary lease still exists after recycle: %v", err)
	}
	reports := resourceCohortReportPaths(t, filepath.Join(fixture.resourceDir, resourceCohortMembersDir))
	if len(reports) != 1 {
		t.Fatalf("resource cohort reports after secondary recycle = %v, want primary only", reports)
	}
	member, err := readResourceCohortMember(reports[0])
	if err != nil {
		t.Fatalf("read remaining primary report: %v", err)
	}
	if member.OwnerPID != primary.OwnerPID || member.ClientPID != primary.ClientPID ||
		member.Role != ResourceCohortRolePrimary {
		t.Fatalf("remaining resource cohort report = %#v, want primary owner/client", member)
	}
}

func assertResourceCohortFullyCleaned(
	t *testing.T,
	fixture resourceCohortIntegrationFixture,
	primary resourceCohortOwnerResult,
) {
	t.Helper()
	assertResourceCohortProcessState(t, primary.ClientPID, false)
	if reports := resourceCohortReportPaths(t, filepath.Join(fixture.resourceDir, resourceCohortMembersDir)); len(reports) != 0 {
		t.Fatalf("resource cohort reports survived final owner close: %v", reports)
	}
	if _, err := os.Stat(primary.LeasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("primary lease survived final owner close: %v", err)
	}
}

func assertResourceCohortLazyReportRebuild(
	t *testing.T,
	fixture resourceCohortIntegrationFixture,
) {
	t.Helper()
	membersDir := filepath.Join(fixture.resourceDir, resourceCohortMembersDir)
	if err := os.Remove(membersDir); err != nil {
		t.Fatalf("remove empty resource cohort members directory: %v", err)
	}
	replacement := startResourceCohortOwner(t, fixture, resourceCohortOwnerConfig{
		name:     "replacement",
		role:     ResourceCohortRolePrimary,
		childMiB: 16,
	})
	result := waitForResourceCohortOwnerResult(t, replacement)
	if result.EvictSelf || result.ReportCount != 1 {
		t.Fatalf("replacement owner result = %#v, want one rebuilt non-evicted report", result)
	}
	if info, err := os.Stat(membersDir); err != nil || !info.IsDir() {
		t.Fatalf("resource cohort members directory was not rebuilt: info=%v err=%v", info, err)
	}
	stopResourceCohortOwner(t, replacement)
	assertResourceCohortProcessState(t, result.ClientPID, false)
}

func assertResourceCohortProcessState(t *testing.T, pid int, wantAlive bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alive, err := hiddenexec.ProcessAlive(pid)
		if err != nil {
			t.Fatalf("probe process %d: %v", pid, err)
		}
		if alive == wantAlive {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d alive state did not become %t", pid, wantAlive)
}

func writeResourceCohortOwnerLease(t *testing.T, role, ownerName, cohortID string) string {
	t.Helper()
	leaseDir := requiredResourceCohortHelperEnv(t, resourceCohortLeaseDirEnv)
	ownerStart, err := hiddenexec.ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatalf("read resource cohort owner start identity: %v", err)
	}
	lease := resourceCohortLease{
		SchemaVersion:      1,
		CohortID:           cohortID,
		Role:               role,
		OwnerPID:           os.Getpid(),
		OwnerStartIdentity: ownerStart,
		CreatedAtUnixNano:  time.Now().UnixNano(),
	}
	payload, err := json.Marshal(lease)
	if err != nil {
		t.Fatalf("encode resource cohort owner lease: %v", err)
	}
	path := filepath.Join(leaseDir, role+"-"+ownerName+".json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write resource cohort owner lease: %v", err)
	}
	return path
}

func startResourceCohortMemoryClient(
	t *testing.T,
	leasePath, cohortID, role string,
	childMiB int,
) (*client, *bool) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	processLimitMiB := resourceCohortIntegrationHardMiB
	if role == ResourceCohortRoleSecondary {
		processLimitMiB = 64
	}
	tr, err := newTransport(transportOptions{
		Binary: binary,
		Args:   []string{"-test.run=^TestResourceCohortMemoryChildHelper$", "-test.count=1"},
		Env: []string{
			resourceCohortChildHelperEnv + "=1",
			resourceCohortChildMiBEnv + "=" + strconv.Itoa(childMiB),
			resourceCohortReadyPathEnv + "=" + requiredResourceCohortHelperEnv(t, resourceCohortReadyPathEnv),
			ResourceCohortDirEnv + "=" + requiredResourceCohortHelperEnv(t, resourceCohortFixtureRootEnv),
			ResourceRepositoryCohortIDEnv + "=" + cohortID,
			ResourceCohortRoleEnv + "=" + role,
			ResourceCohortLeaseEnv + "=" + leasePath,
			ResourceProcessRSSLimitMBEnv + "=" + strconv.Itoa(processLimitMiB),
			ResourceCohortHardLimitMBEnv + "=" + requiredResourceCohortHelperEnv(t, ResourceCohortHardLimitMBEnv),
		},
	})
	if err != nil {
		t.Fatalf("start resource cohort memory child: %v", err)
	}
	waitForResourceCohortMarker(t, requiredResourceCohortHelperEnv(t, resourceCohortReadyPathEnv))
	closed := false
	return &client{transport: tr}, &closed
}

func evaluateResourceCohortOwnerHelper(
	t *testing.T,
	current *client,
	leasePath string,
	activeLeases int,
	activityAge time.Duration,
) resourceCohortOwnerResult {
	t.Helper()
	rssBytes, clientPID, err := clientRSSBytes(current)
	if err != nil {
		t.Fatalf("read resource cohort client process-tree RSS: %v", err)
	}
	now := time.Now()
	workspace := workspaceClient{
		key:          "real-owner-" + strconv.Itoa(os.Getpid()),
		languageID:   "typescript",
		client:       current,
		generation:   1,
		state:        workspaceStateIdleCountdown,
		idleSince:    now.Add(-activityAge),
		lastActivity: now.Add(-activityAge),
	}
	policy, err := resourceProcessPolicyForClient(current, workspace.languageID)
	if err != nil {
		t.Fatalf("resourceProcessPolicyForClient() error = %v", err)
	}
	decision, err := evaluateResourceCohort(
		current,
		workspace,
		policy,
		rssBytes,
		clientPID,
		activeLeases,
		now,
	)
	if err != nil {
		t.Fatalf("evaluateResourceCohort() error = %v", err)
	}
	return resourceCohortOwnerResult{
		OwnerPID:  os.Getpid(),
		ClientPID: clientPID,
		LeasePath: leasePath,
		RSSBytes:  rssBytes,
		Aggregate: decision.AggregateRSS,
		HardLimit: decision.HardLimit,
		Active:    activeLeases,
		EvictSelf: decision.EvictSelf,
	}
}

func writeResourceCohortOwnerResult(t *testing.T, result resourceCohortOwnerResult) {
	t.Helper()
	path := requiredResourceCohortHelperEnv(t, resourceCohortResultPathEnv)
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("encode resource cohort owner result: %v", err)
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, payload, 0o600); err != nil {
		t.Fatalf("write resource cohort owner result: %v", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		t.Fatalf("publish resource cohort owner result: %v", err)
	}
}

func waitForResourceCohortMarker(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(resourceCohortIntegrationTimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect resource cohort marker: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for resource cohort marker %s", path)
}

func countResourceCohortReports(t *testing.T, dir string) int {
	t.Helper()
	return len(resourceCohortReportPaths(t, dir))
}

func resourceCohortReportPaths(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read resource cohort reports: %v", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	return paths
}

func requiredResourceCohortHelperEnv(t *testing.T, key string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		t.Fatalf("required resource cohort helper environment %s is empty", key)
	}
	return value
}

func requiredResourceCohortHelperInt(t *testing.T, key string) int {
	t.Helper()
	raw := requiredResourceCohortHelperEnv(t, key)
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		t.Fatalf("resource cohort helper environment %s = %q is not a non-negative integer", key, raw)
	}
	return value
}

func (result resourceCohortOwnerResult) String() string {
	return fmt.Sprintf(
		"owner=%d client=%d rss=%d aggregate=%d hard=%d active=%d evict=%t reports=%d",
		result.OwnerPID,
		result.ClientPID,
		result.RSSBytes,
		result.Aggregate,
		result.HardLimit,
		result.Active,
		result.EvictSelf,
		result.ReportCount,
	)
}
