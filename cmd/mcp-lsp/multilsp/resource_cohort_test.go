package multilsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func TestResourceCohortQuarantinesOnlyInvalidReports(t *testing.T) {
	if shouldQuarantineResourceCohortReport(errors.New("transient process probe failure")) {
		t.Fatal("transient process probe error was classified as quarantineable")
	}
	if !shouldQuarantineResourceCohortReport(invalidResourceCohortReport(errors.New("malformed schema"))) {
		t.Fatal("invalid report was not classified as quarantineable")
	}
}

// TestResourceCohortSecurityUsesPlatformPrivateACL 锁定平台中立代码不得再用 POSIX mode 判断 Windows 私有性。
func TestResourceCohortSecurityUsesPlatformPrivateACL(t *testing.T) {
	cases := []struct {
		path     string
		required []string
	}{
		{"resource_cohort.go", []string{"securefs.RestrictPrivateOwnerOnly", "securefs.CheckPrivateOwnerOnly"}},
		{"resource_cohort_policy.go", []string{"securefs.RestrictPrivateOwnerOnly", "securefs.CheckPrivateOwnerOnly"}},
		{"../runtime_server_cache.go", []string{"securefs.RestrictPrivateOwnerOnly", "securefs.CheckPrivateOwnerOnly"}},
		{"../runtime_gopls_root_cohort_durable_storage.go", []string{"securefs.RestrictPrivateOwnerOnly", "securefs.CheckPrivateOwnerOnly"}},
	}
	for _, testCase := range cases {
		raw, err := os.ReadFile(testCase.path)
		if err != nil {
			t.Fatalf("read %s: %v", testCase.path, err)
		}
		source := string(raw)
		if strings.Contains(source, "Mode().Perm()&0o077") {
			t.Fatalf("%s uses POSIX mode bits in platform-neutral private-path validation", testCase.path)
		}
		for _, required := range testCase.required {
			if !strings.Contains(source, required) {
				t.Fatalf("%s is missing %s", testCase.path, required)
			}
		}
	}
}

func TestResourceCohortAllowsHeavyPrimaryAndLightSecondaryBelowHardLimit(t *testing.T) {
	now := time.Now()
	hardLimit := uint64(15 * 1024 * 1024 * 1024)
	members := []resourceCohortMember{
		resourceCohortTestMember(101, 201, 2*1024*1024*1024, now.Add(-time.Minute), 0),
		resourceCohortTestMember(102, 202, 200*1024*1024, now, 0),
	}
	if victims := selectResourceCohortVictims(
		members,
		members[0].RSSBytes+members[1].RSSBytes,
		hardLimit,
		hardLimit*resourceCohortSoftPercent/100,
	); len(victims) != 0 {
		t.Fatalf("victims = %v, want none below 15 GiB non-gopls cohort hard limit", victims)
	}
}

func TestResourceCohortAllowsSecondaryRSSAt2560MiB(t *testing.T) {
	env := []string{
		ResourceRepositoryCohortIDEnv + "=repo-secondary-budget",
		ResourceCohortRoleEnv + "=" + ResourceCohortRoleSecondary,
		ResourceProcessRSSLimitMBEnv + "=2560",
		ResourceCohortHardLimitMBEnv + "=5120",
	}
	policy, err := repositoryResourcePolicyFromEnvironment(env)
	if err != nil {
		t.Fatalf("repositoryResourcePolicyFromEnvironment() error = %v", err)
	}
	if policy.rssLimitBytes != 2560*1024*1024 || policy.cohortHardLimitBytes != 5120*1024*1024 {
		t.Fatalf("secondary policy limits = (rss=%d cohort=%d), want (rss=%d cohort=%d)",
			policy.rssLimitBytes, policy.cohortHardLimitBytes, 2560*1024*1024, 5120*1024*1024)
	}
}

func TestValidateResourceCohortMemberAllowsSecondaryRSSAt2560MiB(t *testing.T) {
	member := resourceCohortTestMember(101, 201, 64*1024*1024, time.Now(), 1)
	member.Role = ResourceCohortRoleSecondary
	member.ProcessRSSLimitBytes = 2560 * 1024 * 1024
	member.CohortHardLimitBytes = 5120 * 1024 * 1024
	if err := validateResourceCohortMember(member); err != nil {
		t.Fatalf("validateResourceCohortMember() error = %v", err)
	}
}

func TestResourceCohortEvictsOldestIdleOwnersUntilSoftLimit(t *testing.T) {
	now := time.Now()
	gib := uint64(1024 * 1024 * 1024)
	oldest := resourceCohortTestMember(101, 201, gib, now.Add(-3*time.Minute), 0)
	active := resourceCohortTestMember(102, 202, 2*gib, now.Add(-2*time.Minute), 1)
	newer := resourceCohortTestMember(103, 203, gib, now.Add(-time.Minute), 0)
	victims := selectResourceCohortVictims(
		[]resourceCohortMember{newer, active, oldest},
		5*gib,
		4*gib,
		4*gib*resourceCohortSoftPercent/100,
	)
	want := []string{resourceCohortMemberKey(oldest), resourceCohortMemberKey(newer)}
	if !slices.Equal(victims, want) {
		t.Fatalf("victims = %v, want owner-only idle LRU order %v", victims, want)
	}
}

const (
	resourceCohortCustomHardMiB    = 512
	resourceCohortCustomProcessMiB = 384
)

func TestResourceCohortCustomHardLimitIsFrozenForPolicyReportAndEviction(t *testing.T) {
	current, workspace, resourceDir := newCustomHardLimitResourceCohort(t)
	policy, err := resourceProcessPolicyForClient(current, workspace.languageID)
	if err != nil {
		t.Fatalf("resourceProcessPolicyForClient() error = %v", err)
	}
	decision, err := evaluateResourceCohort(
		current,
		workspace,
		policy,
		uint64(resourceCohortCustomHardMiB+1)*1024*1024,
		os.Getpid(),
		0,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("evaluateResourceCohort() error = %v", err)
	}
	assertCustomHardLimitResourceCohort(t, resourceDir, policy, decision)
}

func newCustomHardLimitResourceCohort(t *testing.T) (*client, workspaceClient, string) {
	t.Helper()
	t.Setenv(ResourceCohortHardLimitMBEnv, "15360")
	const cohortID = "repo-custom-hard-limit"
	resourceDir := resourceCohortPrivateTestDir(t)
	if err := os.Chmod(resourceDir, 0o700); err != nil {
		t.Fatalf("secure resource cohort directory: %v", err)
	}
	leasePath := writeResourceCohortLeaseFixture(
		t,
		filepath.Join(resourceCohortPrivateTestDir(t), cohortID),
		cohortID,
		ResourceCohortRolePrimary,
	)
	env := []string{
		ResourceCohortDirEnv + "=" + resourceDir,
		ResourceRepositoryCohortIDEnv + "=" + cohortID,
		ResourceCohortRoleEnv + "=" + ResourceCohortRolePrimary,
		ResourceCohortLeaseEnv + "=" + leasePath,
		ResourceProcessRSSLimitMBEnv + "=384",
		ResourceCohortHardLimitMBEnv + "=512",
	}
	current := &client{transport: &transport{cmd: &exec.Cmd{
		Env:     env,
		Process: &os.Process{Pid: os.Getpid()},
	}}}
	return current, workspaceClient{
		key:          "custom-hard-limit",
		languageID:   "typescript",
		client:       current,
		lastActivity: time.Now().Add(-time.Minute),
	}, resourceDir
}

func assertCustomHardLimitResourceCohort(
	t *testing.T,
	resourceDir string,
	policy resourceProcessPolicy,
	decision resourceCohortDecision,
) {
	t.Helper()
	wantHardLimit := uint64(resourceCohortCustomHardMiB * 1024 * 1024)
	if policy.cohortHardLimitBytes != wantHardLimit {
		t.Fatalf("policy hard limit = %d, want %d", policy.cohortHardLimitBytes, wantHardLimit)
	}
	if decision.HardLimit != wantHardLimit {
		t.Fatalf("decision hard limit = %d, want %d", decision.HardLimit, wantHardLimit)
	}
	if !decision.EvictSelf {
		t.Fatalf("decision = %#v, want owner-only eviction", decision)
	}
	membersDir := filepath.Join(resourceDir, resourceCohortMembersDir)
	entries, err := os.ReadDir(membersDir)
	if err != nil {
		t.Fatalf("read resource cohort reports: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("resource cohort reports = %d, want one", len(entries))
	}
	member, err := readResourceCohortMember(filepath.Join(membersDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("readResourceCohortMember() error = %v", err)
	}
	if member.ProcessRSSLimitBytes != uint64(resourceCohortCustomProcessMiB*1024*1024) {
		t.Fatalf("report process limit = %d, want %d MiB", member.ProcessRSSLimitBytes, resourceCohortCustomProcessMiB)
	}
	if member.CohortHardLimitBytes != wantHardLimit {
		t.Fatalf("report cohort limit = %d, want %d", member.CohortHardLimitBytes, wantHardLimit)
	}
}

func TestResourceCohortUnwrapsDecoratedClient(t *testing.T) {
	wantDir := filepath.Join(resourceCohortPrivateTestDir(t), "cohort")
	if err := os.Mkdir(wantDir, 0o700); err != nil {
		t.Fatalf("create secure cohort directory: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(wantDir, 0o700); err != nil {
		t.Fatalf("restrict secure cohort directory: %v", err)
	}
	base := &client{transport: &transport{cmd: &exec.Cmd{
		Env: []string{ResourceCohortDirEnv + "=" + wantDir},
	}}}
	wrapped := &resourceCohortTestWrappedClient{Client: base}
	gotDir, enabled, err := resourceCohortDir(wrapped)
	if err != nil {
		t.Fatalf("resourceCohortDir(wrapped) error = %v", err)
	}
	if !enabled || gotDir != wantDir {
		t.Fatalf("resourceCohortDir(wrapped) = (%q, %v), want (%q, true)", gotDir, enabled, wantDir)
	}
	if got, ok := concreteClient(wrapped); !ok || got != base {
		t.Fatalf("concreteClient(wrapped) = (%p, %v), want (%p, true)", got, ok, base)
	}
}

func TestReadResourceCohortMemberRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(resourceCohortPrivateTestDir(t), "member.json")
	payload := `{
		"schema_version":2,
		"owner_pid":10,
		"owner_start_identity":"owner",
		"client_pid":11,
		"client_start_identity":"client",
		"workspace_hash":"workspace",
		"language_id":"typescript",
		"repository_cohort_id":"repo-test",
		"role":"primary",
		"rss_bytes":1,
		"process_rss_limit_bytes":2684354560,
		"cohort_hard_limit_bytes":16106127360,
		"active_leases":0,
		"last_activity_unix_nano":1,
		"updated_at_unix_nano":1,
		"unexpected":true
	}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write cohort fixture: %v", err)
	}
	if _, err := readResourceCohortMember(path); err == nil {
		t.Fatal("readResourceCohortMember() accepted unknown field")
	}
}

func TestReadResourceCohortMemberRejectsMissingZeroValuedRequiredField(t *testing.T) {
	for _, field := range []string{"active_leases", "process_rss_limit_bytes"} {
		t.Run(field, func(t *testing.T) {
			path := filepath.Join(resourceCohortPrivateTestDir(t), "member.json")
			payload := resourceCohortMemberPayloadWithoutField(t, field)
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				t.Fatalf("write cohort fixture: %v", err)
			}
			_, err := readResourceCohortMember(path)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("required field %q is missing", field)) {
				t.Fatalf("readResourceCohortMember() error = %v, want missing required field %q", err, field)
			}
		})
	}
}

func resourceCohortMemberPayloadWithoutField(t *testing.T, field string) []byte {
	t.Helper()
	payload, err := json.Marshal(resourceCohortTestMember(101, 201, 1, time.Now(), 0))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	delete(fields, field)
	payload, err = json.Marshal(fields)
	if err != nil {
		t.Fatalf("json.Marshal(fields) error = %v", err)
	}
	return payload
}

func TestValidateResourceCohortMemberRejectsMissingRequiredField(t *testing.T) {
	member := resourceCohortTestMember(101, 201, 1, time.Now(), 0)
	member.OwnerStartIdentity = ""
	if err := validateResourceCohortMember(member); err == nil {
		t.Fatal("validateResourceCohortMember() accepted missing owner_start_identity")
	}
}

func TestNewResourceCohortMemberRoundTripsCreationPolicyFields(t *testing.T) {
	const cohortID = "repo-field-guard"
	cohortDir := filepath.Join(resourceCohortPrivateTestDir(t), cohortID)
	leasePath := writeResourceCohortLeaseFixture(t, cohortDir, cohortID, ResourceCohortRoleSecondary)
	env := []string{
		ResourceRepositoryCohortIDEnv + "=" + cohortID,
		ResourceCohortRoleEnv + "=" + ResourceCohortRoleSecondary,
		ResourceCohortLeaseEnv + "=" + leasePath,
		ResourceProcessRSSLimitMBEnv + "=384",
		ResourceCohortHardLimitMBEnv + "=15360",
	}
	current := &client{transport: &transport{cmd: &exec.Cmd{Env: env}}}
	now := time.Now()
	workspace := workspaceClient{
		key:          "field-guard-workspace",
		languageID:   "typescript",
		client:       current,
		lastActivity: now.Add(-time.Second),
	}
	policy, err := resourceProcessPolicyForClient(current, workspace.languageID)
	if err != nil {
		t.Fatalf("resourceProcessPolicyForClient() error = %v", err)
	}
	member, err := newResourceCohortMember(
		workspace,
		policy,
		64*1024*1024,
		os.Getpid(),
		0,
		now,
	)
	if err != nil {
		t.Fatalf("newResourceCohortMember() error = %v", err)
	}
	if member.RepositoryCohortID != cohortID || member.Role != ResourceCohortRoleSecondary ||
		member.ProcessRSSLimitBytes != 384*1024*1024 {
		t.Fatalf("creation policy fields = (%q, %q, %d), want (%q, %q, %d)",
			member.RepositoryCohortID, member.Role, member.ProcessRSSLimitBytes,
			cohortID, ResourceCohortRoleSecondary, uint64(384*1024*1024))
	}
	path := filepath.Join(resourceCohortPrivateTestDir(t), "member.json")
	if err := writeResourceCohortMemberAtPath(filepath.Dir(path), path, member); err != nil {
		t.Fatalf("writeResourceCohortMemberAtPath() error = %v", err)
	}
	decoded, err := readResourceCohortMember(path)
	if err != nil {
		t.Fatalf("readResourceCohortMember() error = %v", err)
	}
	if decoded != member {
		t.Fatalf("round-trip member = %#v, want %#v", decoded, member)
	}
}

func TestResourceCohortCleanupRetriesOtherFailureAfterLeaseRelease(t *testing.T) {
	const cohortID = "repo-cleanup-retry"
	repositoryDir := filepath.Join(resourceCohortPrivateTestDir(t), cohortID)
	leasePath := writeResourceCohortLeaseFixture(t, repositoryDir, cohortID, ResourceCohortRolePrimary)
	resourceDir := resourceCohortPrivateTestDir(t)
	if err := os.Chmod(resourceDir, 0o700); err != nil {
		t.Fatalf("secure resource cohort directory: %v", err)
	}
	env := []string{
		ResourceCohortDirEnv + "=" + resourceDir,
		ResourceRepositoryCohortIDEnv + "=" + cohortID,
		ResourceCohortRoleEnv + "=" + ResourceCohortRolePrimary,
		ResourceCohortLeaseEnv + "=" + leasePath,
		ResourceProcessRSSLimitMBEnv + "=512",
	}
	current := &client{transport: &transport{cmd: &exec.Cmd{
		Env:     env,
		Process: &os.Process{Pid: os.Getpid()},
	}}}
	if err := removeOwnedResourceCohortMember(current); err == nil {
		t.Fatal("first resource cleanup unexpectedly succeeded without members directory")
	}
	if _, err := os.Lstat(leasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first cleanup did not release owned lease: %v", err)
	}
	assertResourceCohortCleanupProgress(t, current, false, true)
	if err := os.Mkdir(filepath.Join(resourceDir, resourceCohortMembersDir), 0o700); err != nil {
		t.Fatalf("repair members directory: %v", err)
	}
	if err := removeOwnedResourceCohortMember(current); err != nil {
		t.Fatalf("second resource cleanup error = %v", err)
	}
	assertResourceCohortCleanupProgress(t, current, true, true)
	if err := removeOwnedResourceCohortMember(current); err != nil {
		t.Fatalf("idempotent resource cleanup error = %v", err)
	}
}

func assertResourceCohortCleanupProgress(t *testing.T, current *client, reportsReleased, leaseReleased bool) {
	t.Helper()
	if current.resourceReportsReleased != reportsReleased || current.resourceCohortLeaseReleased != leaseReleased {
		t.Fatalf("cleanup progress = reports:%v lease:%v", current.resourceReportsReleased, current.resourceCohortLeaseReleased)
	}
}

func TestLoadResourceCohortMembersQuarantinesSchemaV1(t *testing.T) {
	dir := resourceCohortPrivateTestDir(t)
	member := resourceCohortTestMember(os.Getpid(), os.Getpid(), 1, time.Now(), 0)
	member.SchemaVersion = 1
	payload, err := json.Marshal(member)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	path := filepath.Join(dir, "schema-v1.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write schema-v1 fixture: %v", err)
	}
	loaded, err := loadResourceCohortMembers(dir, time.Now(), member.CohortHardLimitBytes)
	if err == nil || loaded.UnhealthyMembers != 1 || loaded.ConservativeRSS != member.CohortHardLimitBytes {
		t.Fatalf("schema-v1 load = %#v, err=%v; want one conservative unhealthy member", loaded, err)
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Fatalf("schema-v1 report was not quarantined: %v", err)
	}
	recovered, err := loadResourceCohortMembers(dir, time.Now(), member.CohortHardLimitBytes)
	if err != nil || recovered.UnhealthyMembers != 0 || recovered.ConservativeRSS != 0 {
		t.Fatalf("post-quarantine load = %#v, err=%v", recovered, err)
	}
}

func writeResourceCohortLeaseFixture(
	t *testing.T,
	cohortDir, cohortID, role string,
) string {
	t.Helper()
	if err := os.Mkdir(cohortDir, 0o700); err != nil {
		t.Fatalf("create repository cohort directory: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(cohortDir, 0o700); err != nil {
		t.Fatalf("restrict repository cohort directory: %v", err)
	}
	ownerStart, err := hiddenexec.ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatalf("ProcessStartIdentity() error = %v", err)
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
		t.Fatalf("json.Marshal(lease) error = %v", err)
	}
	path := filepath.Join(cohortDir, role+".json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write repository cohort lease: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(path, 0o600); err != nil {
		t.Fatalf("restrict repository cohort lease: %v", err)
	}
	return path
}

func TestLoadResourceCohortMembersAccountsHardLimitMismatchConservatively(t *testing.T) {
	dir := resourceCohortPrivateTestDir(t)
	pid := os.Getpid()
	startIdentity, err := hiddenexec.ProcessStartIdentity(pid)
	if err != nil {
		t.Fatalf("processStartIdentity() error = %v", err)
	}
	member := resourceCohortTestMember(pid, pid, 1, time.Now(), 0)
	member.OwnerStartIdentity = startIdentity
	member.ClientStartIdentity = startIdentity
	if err := writeResourceCohortMember(dir, member); err != nil {
		t.Fatalf("writeResourceCohortMember() error = %v", err)
	}
	hardLimit := member.CohortHardLimitBytes + 1
	loaded, err := loadResourceCohortMembers(dir, time.Now(), hardLimit)
	if err == nil {
		t.Fatal("loadResourceCohortMembers() did not report hard-limit mismatch")
	}
	if loaded.UnhealthyMembers != 1 || loaded.ConservativeRSS != hardLimit || len(loaded.Members) != 0 {
		t.Fatalf("mismatched hard-limit result = %#v, want one unhealthy full-limit reserve", loaded)
	}
	recovered, recoveryErr := loadResourceCohortMembers(dir, time.Now(), hardLimit)
	if recoveryErr != nil || recovered.UnhealthyMembers != 0 || recovered.ConservativeRSS != 0 {
		t.Fatalf("mismatched report recovery = %#v, err=%v", recovered, recoveryErr)
	}
}

func TestLoadResourceCohortMembersRefreshesStaleLiveOwnerWithoutEvictingIt(t *testing.T) {
	dir := resourceCohortPrivateTestDir(t)
	pid := os.Getpid()
	startIdentity, err := hiddenexec.ProcessStartIdentity(pid)
	if err != nil {
		t.Fatalf("ProcessStartIdentity() error = %v", err)
	}
	member := resourceCohortTestMember(pid, pid, 1, time.Now().Add(-time.Hour), 0)
	member.OwnerStartIdentity = startIdentity
	member.ClientStartIdentity = startIdentity
	member.UpdatedAtUnixNano = time.Now().Add(-resourceCohortReportMaxAge - time.Minute).UnixNano()
	if err := writeResourceCohortMember(dir, member); err != nil {
		t.Fatalf("writeResourceCohortMember() error = %v", err)
	}
	loaded, err := loadResourceCohortMembers(dir, time.Now(), member.CohortHardLimitBytes)
	if err != nil {
		t.Fatalf("loadResourceCohortMembers() error = %v", err)
	}
	assertStaleLiveResourceCohortLoad(t, loaded)
	refreshed, err := loadResourceCohortMembers(dir, time.Now(), member.CohortHardLimitBytes)
	if err != nil {
		t.Fatalf("reprobe stale member: %v", err)
	}
	assertStaleLiveResourceCohortLoad(t, refreshed)
}

func assertStaleLiveResourceCohortLoad(t *testing.T, loaded resourceCohortLoadResult) {
	t.Helper()
	if loaded.StaleMembers != 1 || loaded.UnhealthyMembers != 0 || len(loaded.Members) != 1 {
		t.Fatalf("stale live result = %#v", loaded)
	}
	if got := loaded.Members[0]; !got.Stale || got.ActiveLeases == 0 || got.RSSBytes <= 1 {
		t.Fatalf("stale live member = %#v, want re-probed or conservatively raised RSS and non-evictable marker", got)
	}
}

func TestLoadResourceCohortMembersAccountsMalformedReportConservatively(t *testing.T) {
	dir := resourceCohortPrivateTestDir(t)
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write malformed cohort report: %v", err)
	}
	loaded, err := loadResourceCohortMembers(dir, time.Now(), defaultCohortHardLimitBytes)
	if err == nil {
		t.Fatal("loadResourceCohortMembers() did not report malformed member")
	}
	if loaded.UnhealthyMembers != 1 || loaded.ConservativeRSS != defaultCohortHardLimitBytes {
		t.Fatalf("malformed report result = %#v, want fail-closed full-limit reserve", loaded)
	}
	recovered, recoveryErr := loadResourceCohortMembers(dir, time.Now(), defaultCohortHardLimitBytes)
	if recoveryErr != nil || recovered.UnhealthyMembers != 0 || recovered.ConservativeRSS != 0 {
		t.Fatalf("malformed report recovery = %#v, err=%v", recovered, recoveryErr)
	}
}

func TestLoadResourceCohortMembersBoundsQuarantineGrowth(t *testing.T) {
	dir := resourceCohortPrivateTestDir(t)
	now := time.Now()
	writeResourceCohortQuarantines(t, dir, now)
	loaded, err := loadResourceCohortMembers(dir, now, defaultCohortHardLimitBytes)
	if err != nil {
		t.Fatalf("load with bounded quarantines: %v", err)
	}
	if len(loaded.Members) != 0 || loaded.UnhealthyMembers != 0 {
		t.Fatalf("bounded quarantine load = %#v", loaded)
	}
	assertResourceCohortQuarantinesBounded(t, dir)
}

func writeResourceCohortQuarantines(t *testing.T, dir string, now time.Time) {
	t.Helper()
	for index := range resourceCohortQuarantineMaxCount + 12 {
		path := filepath.Join(dir, fmt.Sprintf("evidence-%02d.bad", index))
		if err := os.WriteFile(path, []byte("evidence"), 0o600); err != nil {
			t.Fatalf("write member quarantine %d: %v", index, err)
		}
		modifiedAt := now.Add(time.Duration(index) * time.Millisecond)
		if index < 2 {
			modifiedAt = now.Add(-resourceCohortQuarantineMaxAge - time.Hour)
		}
		if err := os.Chtimes(path, modifiedAt, modifiedAt); err != nil {
			t.Fatalf("age member quarantine %d: %v", index, err)
		}
	}
}

func assertResourceCohortQuarantinesBounded(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read bounded member quarantines: %v", err)
	}
	quarantineCount := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bad") {
			quarantineCount++
		}
	}
	if quarantineCount > resourceCohortQuarantineMaxCount {
		t.Fatalf("member quarantine count = %d, limit = %d", quarantineCount, resourceCohortQuarantineMaxCount)
	}
	for index := range 2 {
		if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("evidence-%02d.bad", index))); !os.IsNotExist(err) {
			t.Fatalf("expired member quarantine %d was retained: %v", index, err)
		}
	}
}

func TestLoadResourceCohortMembersRejectsFutureTimestampConservatively(t *testing.T) {
	dir := resourceCohortPrivateTestDir(t)
	member := resourceCohortTestMember(101, 201, 1, time.Now(), 0)
	member.UpdatedAtUnixNano = time.Now().Add(resourceCohortClockSkewTolerance + time.Minute).UnixNano()
	if err := writeResourceCohortMember(dir, member); err != nil {
		t.Fatalf("write future cohort report: %v", err)
	}
	loaded, err := loadResourceCohortMembers(dir, time.Now(), member.CohortHardLimitBytes)
	if err == nil {
		t.Fatal("loadResourceCohortMembers() accepted a future timestamp")
	}
	if loaded.UnhealthyMembers != 1 || loaded.ConservativeRSS != member.CohortHardLimitBytes {
		t.Fatalf("future report result = %#v, want one full-limit reserve", loaded)
	}
}

func resourceCohortTestMember(ownerPID, clientPID int, rss uint64, lastActivity time.Time, leases int) resourceCohortMember {
	return resourceCohortMember{
		SchemaVersion:        resourceCohortSchemaVersion,
		OwnerPID:             ownerPID,
		OwnerStartIdentity:   "owner",
		ClientPID:            clientPID,
		ClientStartIdentity:  "client",
		WorkspaceHash:        "workspace",
		LanguageID:           "typescript",
		RepositoryCohortID:   "repo-test",
		Role:                 ResourceCohortRolePrimary,
		RSSBytes:             rss,
		ProcessRSSLimitBytes: 2560 * 1024 * 1024,
		CohortHardLimitBytes: defaultCohortHardLimitBytes,
		ActiveLeases:         leases,
		LastActivityUnixNano: lastActivity.UnixNano(),
		UpdatedAtUnixNano:    time.Now().UnixNano(),
	}
}

func resourceCohortPrivateTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(dir, 0o700); err != nil {
		t.Fatalf("restrict resource cohort test directory: %v", err)
	}
	return dir
}

type resourceCohortTestWrappedClient struct {
	Client
}

func (c *resourceCohortTestWrappedClient) UnderlyingLSPClient() Client {
	return c.Client
}
