//go:build e2e && (darwin || linux)

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

type resourceCohortE2ENodeBudget struct {
	NodeOptions       string `json:"node_options"`
	ProcessRSSLimitMB string `json:"process_rss_limit_mb"`
	CohortHardLimitMB string `json:"cohort_hard_limit_mb"`
}

type resourceCohortE2ESnapshot struct {
	Members          []resourceCohortE2EMember
	AggregateRSS     uint64
	HardLimit        uint64
	UnhealthyMembers int
}

func assertResourceCohortE2ERealRSS(t *testing.T, primaryPID, secondaryPID int) {
	t.Helper()
	primaryRSS, err := hiddenexec.ProcessTreeRSSBytes(primaryPID)
	if err != nil {
		t.Fatalf("measure real primary LSP process-tree RSS: %v", err)
	}
	secondaryRSS, err := hiddenexec.ProcessTreeRSSBytes(secondaryPID)
	if err != nil {
		t.Fatalf("measure real secondary LSP process-tree RSS: %v", err)
	}
	hardLimit := uint64(resourceCohortE2EHardLimitMiB) * 1024 * 1024
	minimumRSS := uint64(resourceCohortE2EChildMemoryMiB/2) * 1024 * 1024
	if primaryRSS < minimumRSS || secondaryRSS < minimumRSS {
		t.Fatalf("controlled LSP RSS did not include committed child memory: primary=%d secondary=%d minimum=%d",
			primaryRSS, secondaryRSS, minimumRSS)
	}
	if primaryRSS >= hardLimit || secondaryRSS >= hardLimit {
		t.Fatalf("one controlled LSP already exceeds the cohort hard limit: primary=%d secondary=%d hard=%d",
			primaryRSS, secondaryRSS, hardLimit)
	}
	if primaryRSS+secondaryRSS <= hardLimit {
		t.Fatalf("real aggregate LSP RSS does not exceed the low test limit: primary=%d secondary=%d aggregate=%d hard=%d",
			primaryRSS, secondaryRSS, primaryRSS+secondaryRSS, hardLimit)
	}
}

func writeResourceCohortE2EBadReport(t *testing.T, cacheDir string) string {
	t.Helper()
	membersDir := filepath.Join(cacheDir, "resource-cohorts", "non-gopls", "members")
	if err := os.MkdirAll(membersDir, 0o700); err != nil {
		t.Fatalf("create production resource cohort members directory: %v", err)
	}
	path := filepath.Join(membersDir, "malformed-production-e2e.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"unknown":true}`), 0o600); err != nil {
		t.Fatalf("write malformed resource cohort report: %v", err)
	}
	return path
}

func waitForResourceCohortE2ELease(
	t *testing.T,
	cacheDir string,
	ownerPID int,
	role, excludedPath string,
) (string, resourceCohortE2ELease) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		path, lease, ok, err := findResourceCohortE2ELease(cacheDir, ownerPID, role, excludedPath)
		if err != nil {
			t.Fatalf("find resource cohort lease: %v", err)
		}
		if ok {
			return path, lease
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s lease owned by sidecar PID %d", role, ownerPID)
	return "", resourceCohortE2ELease{}
}

func findResourceCohortE2ELease(
	cacheDir string,
	ownerPID int,
	role, excludedPath string,
) (string, resourceCohortE2ELease, bool, error) {
	repositoriesDir := filepath.Join(cacheDir, "resource-cohorts", "repositories")
	cohorts, err := os.ReadDir(repositoriesDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", resourceCohortE2ELease{}, false, nil
	}
	if err != nil {
		return "", resourceCohortE2ELease{}, false, err
	}
	for _, cohort := range cohorts {
		if !cohort.IsDir() {
			continue
		}
		path, lease, ok, err := findResourceCohortE2ELeaseInDir(
			filepath.Join(repositoriesDir, cohort.Name()), ownerPID, role, excludedPath,
		)
		if err != nil {
			return "", resourceCohortE2ELease{}, false, err
		}
		if ok {
			return path, lease, true, nil
		}
	}
	return "", resourceCohortE2ELease{}, false, nil
}

func findResourceCohortE2ELeaseInDir(
	dir string,
	ownerPID int,
	role, excludedPath string,
) (string, resourceCohortE2ELease, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", resourceCohortE2ELease{}, false, err
	}
	for _, entry := range entries {
		path, lease, ok, err := readResourceCohortE2ELeaseCandidate(
			dir, entry, ownerPID, role, excludedPath,
		)
		if err != nil {
			return "", resourceCohortE2ELease{}, false, err
		}
		if ok {
			return path, lease, true, nil
		}
	}
	return "", resourceCohortE2ELease{}, false, nil
}

func readResourceCohortE2ELeaseCandidate(
	dir string,
	entry os.DirEntry,
	ownerPID int,
	role, excludedPath string,
) (string, resourceCohortE2ELease, bool, error) {
	if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
		return "", resourceCohortE2ELease{}, false, nil
	}
	path := filepath.Join(dir, entry.Name())
	if path == excludedPath {
		return "", resourceCohortE2ELease{}, false, nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", resourceCohortE2ELease{}, false, err
	}
	var lease resourceCohortE2ELease
	if err := json.Unmarshal(payload, &lease); err != nil {
		return "", resourceCohortE2ELease{}, false, err
	}
	return path, lease, lease.OwnerPID == ownerPID && lease.Role == role, nil
}

func waitForResourceCohortE2EMember(
	t *testing.T,
	cacheDir string,
	ownerPID, clientPID, minimumActiveLeases int,
	timeout time.Duration,
) resourceCohortE2EMember {
	t.Helper()
	return waitForResourceCohortE2EMemberAfter(
		t, cacheDir, ownerPID, clientPID, minimumActiveLeases, time.Time{}, timeout,
	)
}

func waitForResourceCohortE2EMemberAfter(
	t *testing.T,
	cacheDir string,
	ownerPID, clientPID, minimumActiveLeases int,
	notBefore time.Time,
	timeout time.Duration,
) resourceCohortE2EMember {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot, err := readResourceCohortE2ESnapshot(cacheDir)
		if err != nil {
			t.Fatalf("read resource cohort E2E members: %v", err)
		}
		if snapshot.UnhealthyMembers != 0 {
			t.Fatalf("resource cohort production snapshot has %d unhealthy members", snapshot.UnhealthyMembers)
		}
		for _, member := range snapshot.Members {
			if member.OwnerPID == ownerPID && member.ClientPID == clientPID &&
				member.ActiveLeases >= minimumActiveLeases &&
				member.UpdatedAtUnixNano >= notBefore.UnixNano() {
				return member
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for member owner=%d client=%d active>=%d", ownerPID, clientPID, minimumActiveLeases)
	return resourceCohortE2EMember{}
}

func readResourceCohortE2ESnapshot(cacheDir string) (resourceCohortE2ESnapshot, error) {
	dir := filepath.Join(cacheDir, "resource-cohorts", "non-gopls", "members")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return resourceCohortE2ESnapshot{}, nil
	}
	if err != nil {
		return resourceCohortE2ESnapshot{}, err
	}
	snapshot := resourceCohortE2ESnapshot{
		Members: make([]resourceCohortE2EMember, 0, len(entries)),
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		member, err := readResourceCohortE2EMemberFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			snapshot.UnhealthyMembers++
			continue
		}
		if err := appendResourceCohortE2ESnapshotMember(&snapshot, member); err != nil {
			snapshot.UnhealthyMembers++
			continue
		}
	}
	return snapshot, nil
}

func readResourceCohortE2EMemberFile(path string) (resourceCohortE2EMember, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return resourceCohortE2EMember{}, err
	}
	var member resourceCohortE2EMember
	if err := decodeResourceCohortE2EMember(payload, &member); err != nil {
		return resourceCohortE2EMember{}, err
	}
	return member, nil
}

func appendResourceCohortE2ESnapshotMember(
	snapshot *resourceCohortE2ESnapshot,
	member resourceCohortE2EMember,
) error {
	if snapshot.HardLimit == 0 {
		snapshot.HardLimit = member.CohortHardLimitBytes
	}
	if snapshot.HardLimit != member.CohortHardLimitBytes {
		return errors.New("resource cohort hard limits do not match")
	}
	if ^uint64(0)-snapshot.AggregateRSS < member.RSSBytes {
		return errors.New("resource cohort aggregate RSS overflows uint64")
	}
	snapshot.AggregateRSS += member.RSSBytes
	snapshot.Members = append(snapshot.Members, member)
	return nil
}

func decodeResourceCohortE2EMember(payload []byte, member *resourceCohortE2EMember) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(member); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("resource cohort member has trailing JSON")
	}
	return validateResourceCohortE2EMember(*member)
}

func validateResourceCohortE2EMember(member resourceCohortE2EMember) error {
	if member.SchemaVersion != 2 {
		return fmt.Errorf("schema version = %d, want 2", member.SchemaVersion)
	}
	for _, validate := range []func(resourceCohortE2EMember) error{
		validateResourceCohortE2EProcessIdentity,
		validateResourceCohortE2ECohortIdentity,
		validateResourceCohortE2EResourceAccounting,
		validateResourceCohortE2ETimestamps,
	} {
		if err := validate(member); err != nil {
			return err
		}
	}
	return nil
}

func validateResourceCohortE2EProcessIdentity(member resourceCohortE2EMember) error {
	if member.OwnerPID <= 1 || member.ClientPID <= 1 ||
		member.OwnerStartIdentity == "" || member.ClientStartIdentity == "" {
		return errors.New("resource cohort member process identity is incomplete")
	}
	return nil
}

func validateResourceCohortE2ECohortIdentity(member resourceCohortE2EMember) error {
	if member.WorkspaceHash == "" || member.LanguageID == "" ||
		member.RepositoryCohortID == "" || member.Role == "" {
		return errors.New("resource cohort member cohort identity is incomplete")
	}
	return nil
}

func validateResourceCohortE2EResourceAccounting(member resourceCohortE2EMember) error {
	if member.RSSBytes == 0 || member.ProcessRSSLimitBytes == 0 ||
		member.CohortHardLimitBytes == 0 || member.ActiveLeases < 0 {
		return errors.New("resource cohort member resource accounting is invalid")
	}
	return nil
}

func validateResourceCohortE2ETimestamps(member resourceCohortE2EMember) error {
	if member.LastActivityUnixNano <= 0 || member.UpdatedAtUnixNano <= 0 {
		return errors.New("resource cohort member timestamps are invalid")
	}
	return nil
}

func waitForResourceCohortE2EHealthyProductionSnapshot(
	t *testing.T,
	cacheDir string,
	notBefore time.Time,
	owners ...resourceCohortE2EOwner,
) resourceCohortE2ESnapshot {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := readResourceCohortE2ESnapshot(cacheDir)
		if err != nil {
			t.Fatalf("read production resource cohort snapshot: %v", err)
		}
		if snapshot.UnhealthyMembers != 0 {
			t.Fatalf("production resource cohort UnhealthyMembers=%d, want 0", snapshot.UnhealthyMembers)
		}
		if resourceCohortE2ESnapshotMatches(snapshot, notBefore, owners) {
			assertResourceCohortE2EProductionTotals(t, snapshot, owners)
			return snapshot
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d healthy production resource cohort reports", len(owners))
	return resourceCohortE2ESnapshot{}
}

func resourceCohortE2ESnapshotMatches(
	snapshot resourceCohortE2ESnapshot,
	notBefore time.Time,
	owners []resourceCohortE2EOwner,
) bool {
	if len(snapshot.Members) != len(owners) {
		return false
	}
	for _, owner := range owners {
		if !resourceCohortE2ESnapshotHasOwner(snapshot, notBefore, owner) {
			return false
		}
	}
	return true
}

func resourceCohortE2ESnapshotHasOwner(
	snapshot resourceCohortE2ESnapshot,
	notBefore time.Time,
	owner resourceCohortE2EOwner,
) bool {
	for _, member := range snapshot.Members {
		if resourceCohortE2EMemberMatchesOwner(member, notBefore, owner) {
			return true
		}
	}
	return false
}

func resourceCohortE2EMemberMatchesOwner(
	member resourceCohortE2EMember,
	notBefore time.Time,
	owner resourceCohortE2EOwner,
) bool {
	if member.OwnerPID != owner.client.cmd.Process.Pid || member.ClientPID != owner.pid {
		return false
	}
	if member.ActiveLeases < 1 || member.UpdatedAtUnixNano < notBefore.UnixNano() {
		return false
	}
	if member.RepositoryCohortID != owner.lease.CohortID || member.Role != owner.lease.Role {
		return false
	}
	return !member.Stale
}

func assertResourceCohortE2EProductionTotals(
	t *testing.T,
	snapshot resourceCohortE2ESnapshot,
	owners []resourceCohortE2EOwner,
) {
	t.Helper()
	hardLimit := uint64(resourceCohortE2EHardLimitMiB) * 1024 * 1024
	if len(snapshot.Members) != len(owners) || snapshot.UnhealthyMembers != 0 {
		t.Fatalf("production reports=%d unhealthy=%d, want reports=%d unhealthy=0",
			len(snapshot.Members), snapshot.UnhealthyMembers, len(owners))
	}
	if snapshot.HardLimit != hardLimit || snapshot.AggregateRSS <= snapshot.HardLimit {
		t.Fatalf("production aggregate RSS did not exceed the cohort hard limit: aggregate=%d hard=%d want_hard=%d",
			snapshot.AggregateRSS, snapshot.HardLimit, hardLimit)
	}
	for _, owner := range owners {
		if strings.Contains(owner.client.stderrString(), "LSP resource cohort degraded") {
			t.Fatalf("healthy production snapshot logged degraded cohort state: %s", owner.client.stderrString())
		}
	}
}

func waitForResourceCohortE2EMemberAbsence(
	t *testing.T,
	cacheDir string,
	ownerPID int,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot, err := readResourceCohortE2ESnapshot(cacheDir)
		if err != nil {
			t.Fatalf("read resource cohort snapshot while waiting for member removal: %v", err)
		}
		if snapshot.UnhealthyMembers != 0 {
			t.Fatalf("resource cohort snapshot has %d unhealthy members", snapshot.UnhealthyMembers)
		}
		found := false
		for _, member := range snapshot.Members {
			found = found || member.OwnerPID == ownerPID
		}
		if !found {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("resource cohort member owned by sidecar PID %d was not removed", ownerPID)
}

func assertResourceCohortE2ENoLazyRecoveryArtifacts(
	t *testing.T,
	fixture resourceCohortE2EFixture,
	secondary resourceCohortE2EOwner,
) {
	t.Helper()
	waitForResourceCohortE2EProcessState(t, secondary.pid, false, time.Second)
	waitForResourceCohortE2EPathState(t, secondary.leasePath, false, time.Second)
	assertResourceCohortE2ENoNewProcessMarkers(t, fixture.stateDir, "secondary", secondary.pid)
	path, lease, ok, err := findResourceCohortE2ELease(
		fixture.cacheDir, secondary.client.cmd.Process.Pid, "secondary", secondary.leasePath,
	)
	if err != nil {
		t.Fatalf("inspect pre-request secondary lease: %v", err)
	}
	if ok {
		t.Fatalf("lazy recovery created a lease before the first request: path=%s lease=%#v", path, lease)
	}
	snapshot, err := readResourceCohortE2ESnapshot(fixture.cacheDir)
	if err != nil {
		t.Fatalf("read pre-request resource cohort snapshot: %v", err)
	}
	if snapshot.UnhealthyMembers != 0 {
		t.Fatalf("pre-request resource cohort UnhealthyMembers=%d, want 0", snapshot.UnhealthyMembers)
	}
	for _, member := range snapshot.Members {
		if member.OwnerPID == secondary.client.cmd.Process.Pid {
			t.Fatalf("lazy recovery created a member report before the first request: %#v", member)
		}
	}
}

func assertResourceCohortE2ENoNewProcessMarkers(
	t *testing.T,
	stateDir, owner string,
	oldPID int,
) {
	t.Helper()
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatalf("read resource cohort state directory before recovery: %v", err)
	}
	oldSuffix := "." + strconv.Itoa(oldPID)
	for _, entry := range entries {
		isLifecycleMarker := strings.HasPrefix(entry.Name(), owner+".started.") ||
			strings.HasPrefix(entry.Name(), owner+".initialized.")
		if isLifecycleMarker && !strings.HasSuffix(entry.Name(), oldSuffix) {
			t.Fatalf("lazy recovery created lifecycle marker before the first request: %s", entry.Name())
		}
	}
}

func waitForResourceCohortE2EInitializeAfter(
	t *testing.T,
	stateDir, owner string,
	pid int,
	notBefore time.Time,
) {
	t.Helper()
	waitForResourceCohortE2EMarkerAfter(
		t, stateDir, resourceCohortE2EInitializedMarker(owner, pid), notBefore,
	)
}

func waitForResourceCohortE2EMarkerAfter(
	t *testing.T,
	stateDir, name string,
	notBefore time.Time,
) {
	t.Helper()
	path := filepath.Join(stateDir, name)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if err != nil {
			t.Fatalf("read resource cohort lifecycle marker %s: %v", path, err)
		}
		writtenAt, err := strconv.ParseInt(strings.TrimSpace(string(payload)), 10, 64)
		if err != nil {
			t.Fatalf("parse resource cohort lifecycle marker %s: %v", path, err)
		}
		if writtenAt < notBefore.UnixNano() {
			t.Fatalf("resource cohort lifecycle marker %s predates first recovery request: marker=%d request=%d",
				path, writtenAt, notBefore.UnixNano())
		}
		return
	}
	t.Fatalf("timed out waiting for resource cohort lifecycle marker %s", path)
}

func waitForResourceCohortE2EProcessState(t *testing.T, pid int, wantAlive bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
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

func waitForResourceCohortE2EPathState(t *testing.T, path string, wantExists bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := os.Stat(path)
		exists := err == nil
		if exists == wantExists {
			return
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect resource cohort E2E path %s: %v", path, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("path %s existence did not become %t", path, wantExists)
}

func waitForResourceCohortE2ELog(
	t *testing.T,
	client *mcpLSPBinaryClient,
	want string,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(client.stderrString(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("mcp-lsp stderr did not contain %q: %s", want, client.stderrString())
}

func waitUntilResourceCohortE2ETime(t *testing.T, target time.Time) {
	t.Helper()
	remaining := time.Until(target)
	if remaining <= 0 {
		t.Fatalf("resource cohort E2E missed recycler coordination window by %s", -remaining)
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	<-timer.C
}

func newResourceCohortE2ETypeScriptFixture(t *testing.T) resourceCohortE2EFixture {
	t.Helper()
	fixture := newResourceCohortE2EFixture(t)
	for index, worktree := range fixture.worktrees {
		path := filepath.Join(worktree, "probe.ts")
		if err := os.WriteFile(path, []byte("export const RESOURCE_COHORT = 1;\n"), 0o600); err != nil {
			t.Fatalf("write temporary TypeScript source %d: %v", index, err)
		}
		fixture.targets[index] = path
	}
	fixture.serverBin = writeResourceCohortE2ETypeScriptLanguageServer(t)
	return fixture
}

func startResourceCohortE2ETypeScriptOwner(
	t *testing.T,
	ctx context.Context,
	fixture resourceCohortE2EFixture,
	worktreeIndex int,
	role string,
) resourceCohortE2EOwner {
	t.Helper()
	launched := time.Now()
	client := startResourceCohortE2ETypeScriptSidecar(t, ctx, fixture, worktreeIndex, role)
	initializeResourceCohortE2ESidecar(t, client)
	warmResourceCohortE2ESidecar(t, client, fixture.targets[worktreeIndex], role)
	pid := waitForResourceCohortE2ELSPPID(t, fixture.stateDir, role, 0)
	waitForResourceCohortE2EInitialize(t, fixture.stateDir, role, pid)
	leasePath, lease := waitForResourceCohortE2ELease(t, fixture.cacheDir, client.cmd.Process.Pid, role, "")
	return resourceCohortE2EOwner{
		launched: launched, client: client, pid: pid, leasePath: leasePath, lease: lease,
	}
}

func assertResourceCohortE2ETypeScriptNodeBudget(
	t *testing.T,
	fixture resourceCohortE2EFixture,
	primary, secondary resourceCohortE2EOwner,
) {
	t.Helper()
	if primary.lease.CohortID == "" || primary.lease.CohortID != secondary.lease.CohortID {
		t.Fatalf("TypeScript leases do not share one repo/language/server cohort: primary=%#v secondary=%#v", primary.lease, secondary.lease)
	}
	for _, owner := range []resourceCohortE2EOwner{primary, secondary} {
		budget := waitForResourceCohortE2ENodeBudget(t, fixture.stateDir, owner.lease.Role, owner.pid)
		t.Logf("TypeScript %s child budget marker: NODE_OPTIONS=%q %s=%q %s=%q", owner.lease.Role, budget.NodeOptions, multilsp.ResourceProcessRSSLimitMBEnv, budget.ProcessRSSLimitMB, multilsp.ResourceCohortHardLimitMBEnv, budget.CohortHardLimitMB)
		if budget.NodeOptions != "--max-old-space-size=2048" ||
			budget.ProcessRSSLimitMB != "2560" || budget.CohortHardLimitMB != "5120" {
			t.Fatalf("TypeScript %s child budget = %#v, want heap=2048 rss=2560 cohort=5120", owner.lease.Role, budget)
		}
		heapMB := parseResourceCohortE2EMiB(t, owner.lease.Role, "heap", strings.TrimPrefix(budget.NodeOptions, "--max-old-space-size="))
		rssMB := parseResourceCohortE2EMiB(t, owner.lease.Role, "RSS", budget.ProcessRSSLimitMB)
		cohortMB := parseResourceCohortE2EMiB(t, owner.lease.Role, "cohort", budget.CohortHardLimitMB)
		if heapMB >= rssMB || rssMB >= cohortMB {
			t.Fatalf("TypeScript %s budget ordering = heap:%d rss:%d cohort:%d, want heap<RSS<cohort", owner.lease.Role, heapMB, rssMB, cohortMB)
		}
	}
}

func parseResourceCohortE2EMiB(t *testing.T, role, name, raw string) int {
	t.Helper()
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		t.Fatalf("TypeScript %s %s budget = %q, want positive MiB", role, name, raw)
	}
	return value
}

func writeResourceCohortE2ENodeOptions(stateDir, owner string, pid int) error {
	path := filepath.Join(stateDir, resourceCohortE2ENodeOptionsMarker(owner, pid))
	payload, err := json.Marshal(resourceCohortE2ENodeBudget{
		NodeOptions:       strings.TrimSpace(os.Getenv("NODE_OPTIONS")),
		ProcessRSSLimitMB: strings.TrimSpace(os.Getenv(multilsp.ResourceProcessRSSLimitMBEnv)),
		CohortHardLimitMB: strings.TrimSpace(os.Getenv(multilsp.ResourceCohortHardLimitMBEnv)),
	})
	if err != nil {
		return fmt.Errorf("encode TypeScript Node budget marker %s: %w", path, err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("write TypeScript Node budget marker %s: %w", path, err)
	}
	return nil
}

func resourceCohortE2ENodeOptionsMarker(owner string, pid int) string {
	return owner + ".node-options." + strconv.Itoa(pid)
}

func waitForResourceCohortE2ENodeBudget(t *testing.T, stateDir, owner string, pid int) resourceCohortE2ENodeBudget {
	t.Helper()
	path := filepath.Join(stateDir, resourceCohortE2ENodeOptionsMarker(owner, pid))
	waitForResourceCohortE2EPathState(t, path, true, 10*time.Second)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read TypeScript Node budget marker %s: %v", path, err)
	}
	var budget resourceCohortE2ENodeBudget
	if err := json.Unmarshal(payload, &budget); err != nil {
		t.Fatalf("decode TypeScript Node budget marker %s: %v", path, err)
	}
	return budget
}
