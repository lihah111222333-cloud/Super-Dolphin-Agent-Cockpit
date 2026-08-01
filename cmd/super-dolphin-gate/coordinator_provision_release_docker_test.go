package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"golang.org/x/sync/errgroup"
)

type productionProvisionDockerExecution struct {
	fixture      productionProvisionDockerFixture
	result       productionProvisionResult
	config       productionCoordinatorConfig
	dependencies coordinatorDependencies
	accepted     gatecontract.AcceptedImageRecord
	checkpoint   localci.DockerDaemonIdentityCheckpoint
}

func prepareProductionProvisionDockerExecution(t *testing.T) productionProvisionDockerExecution {
	t.Helper()
	fixture := newProductionProvisionDockerFixture(t)
	initialCheckpoint := probeProductionProvisionSchedulerAuthority(t, localci.ProbeDockerSchedulerAuthority)
	checkpoint := isolateProductionProvisionSchedulerAuthority(
		t,
		initialCheckpoint,
		localci.ProbeDockerSchedulerAuthority,
	)
	manifest, root := productionProvisionDockerManifest(
		t, fixture.base, fixture.controller, fixture.repository, fixture.runner, fixture.candidate.Registry, fixture.policy, fixture.inputs,
	)
	if root.BootstrapPublicKey == root.Ed25519PublicKey || root.BootstrapSigner == root.Signer {
		t.Fatal("Docker E2E root reused release trust as bootstrap authority")
	}
	result, err := provisionProductionWithRuntime(context.Background(), manifest, fixture.runtime)
	if err != nil {
		t.Fatalf("provisionProductionWithRuntime() error = %v", err)
	}
	config, err := loadProductionCoordinatorConfigFile(result.ProductionConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	assertProductionProvisionStartsEmpty(t, config)
	assertProductionProvisionTrustedSourceRoot(t, manifest, config)
	t.Setenv(productionCoordinatorConfigEnv, result.ProductionConfigFile)
	dependencies, err := productionCoordinatorDependencies(context.Background())
	if err != nil {
		t.Fatalf("productionCoordinatorDependencies() bootstrap error = %v", err)
	}
	loader, accepted, err := newProductionAcceptedImageLoader(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if loader == nil || accepted.Generation != 1 || !reflect.DeepEqual(accepted.Image, fixture.candidate) {
		t.Fatalf("generation-one accepted record = %#v", accepted)
	}
	return productionProvisionDockerExecution{
		fixture: fixture, result: result, config: config, dependencies: dependencies, accepted: accepted, checkpoint: checkpoint,
	}
}

func assertProductionProvisionTrustedSourceRoot(
	t *testing.T,
	manifest productionProvisionManifest,
	config productionCoordinatorConfig,
) {
	t.Helper()
	runtimeRoot, err := coordinatorRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	if config.TrustedSourceRoot != runtimeRoot || config.TrustedSourceRoot != manifest.TrustedSourceRoot {
		t.Fatalf(
			"trusted source roots manifest=%q config=%q runtime=%q",
			manifest.TrustedSourceRoot,
			config.TrustedSourceRoot,
			runtimeRoot,
		)
	}
}

// super-dolphin-ci: platform=darwin
func TestProductionProvisionBootstrapOwnerReleaseCLIDockerE2E(t *testing.T) {
	requireProductionProvisionDockerE2E(t)
	execution := prepareProductionProvisionDockerExecution(t)
	_ = startProductionProvisionOwner(t, execution.checkpoint, execution.dependencies)
	sampler := startProductionProvisionContainerSampler(t, execution.fixture.repository.tree)
	submitted := submitProductionProvisionReleaseCLI(t, execution)
	record := assertProductionProvisionOwnerTerminal(
		t, execution.config, execution.checkpoint, submitted.JobID, execution.accepted, gatecontract.ProfileRelease,
	)
	peak := sampler.stop(t)
	assertProductionProvisionReleaseRecord(t, execution, submitted, record, peak)
}

type productionProvisionSchedulerAuthorityProbe func(
	context.Context,
) (localci.DockerDaemonIdentityCheckpoint, error)

// probeProductionProvisionSchedulerAuthority 在受限时限内读取 Docker scheduler authority。
func probeProductionProvisionSchedulerAuthority(
	t *testing.T,
	probe productionProvisionSchedulerAuthorityProbe,
) localci.DockerDaemonIdentityCheckpoint {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	checkpoint, err := probe(ctx)
	if err != nil {
		t.Fatalf("probe production Docker scheduler authority: %v", err)
	}
	return checkpoint
}

// isolateProductionProvisionSchedulerAuthority 保留 Docker context 配置，隔离 cache HOME 后复核身份。
func isolateProductionProvisionSchedulerAuthority(
	t *testing.T,
	initial localci.DockerDaemonIdentityCheckpoint,
	probe productionProvisionSchedulerAuthorityProbe,
) localci.DockerDaemonIdentityCheckpoint {
	t.Helper()
	dockerConfigDir := resolveProductionProvisionDockerConfigDir(t)
	t.Setenv("DOCKER_CONFIG", dockerConfigDir)
	requireProductionProvisionDockerIdentityEnvironment(t)
	configureProductionProvisionSchedulerCacheHome(t)
	isolated := probeProductionProvisionSchedulerAuthority(t, probe)
	if err := validateProductionProvisionSchedulerAuthority(initial, isolated); err != nil {
		t.Fatal(err)
	}
	return isolated
}

func resolveProductionProvisionDockerConfigDir(t *testing.T) string {
	t.Helper()
	configDir, configured := os.LookupEnv("DOCKER_CONFIG")
	if !configured || configDir == "" {
		home, ok := os.LookupEnv("HOME")
		if !ok || strings.TrimSpace(home) == "" {
			t.Fatal("original HOME is required to locate Docker config")
		}
		configDir = filepath.Join(home, ".docker")
	}
	if strings.TrimSpace(configDir) != configDir || !filepath.IsAbs(configDir) || filepath.Clean(configDir) != configDir {
		t.Fatalf("Docker config directory must be canonical and absolute")
	}
	info, err := os.Stat(configDir)
	if err != nil {
		t.Fatalf("inspect Docker config directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("Docker config path must be a directory")
	}
	return configDir
}

func requireProductionProvisionDockerIdentityEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"DOCKER_HOST", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"} {
		if _, set := os.LookupEnv(name); set {
			t.Fatalf("%s override is set; active context identity probe requires it to be unset", name)
		}
	}
}

// validateProductionProvisionSchedulerAuthority 阻断 cache 隔离前后的 daemon 身份漂移。
func validateProductionProvisionSchedulerAuthority(
	initial localci.DockerDaemonIdentityCheckpoint,
	isolated localci.DockerDaemonIdentityCheckpoint,
) error {
	if initial.SchedulerConfig != isolated.SchedulerConfig {
		return fmt.Errorf(
			"Docker scheduler identity changed after cache isolation: initial=%#v isolated=%#v",
			initial.SchedulerConfig,
			isolated.SchedulerConfig,
		)
	}
	if initial.IdentityKey != isolated.IdentityKey {
		return fmt.Errorf(
			"Docker scheduler identity key changed after cache isolation: initial=%q isolated=%q",
			initial.IdentityKey,
			isolated.IdentityKey,
		)
	}
	return nil
}

func configureProductionProvisionSchedulerCacheHome(t *testing.T) string {
	t.Helper()
	root := canonicalPrivateTestTempRoot(t)
	createPrivateSchedulerCacheEnvironment(t, root)
	return ensurePrivateCacheRoot(t)
}

func canonicalPrivateTestTempRoot(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if runtime.GOOS != "windows" {
		base = "/tmp"
	}
	return canonicalPrivateTestTempRootAtBase(t, base)
}

func canonicalPrivateTestTempRootAtBase(t *testing.T, base string) string {
	t.Helper()
	canonicalBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatalf("resolve isolated scheduler cache temporary base %q: %v", base, err)
	}
	root, err := os.MkdirTemp(canonicalBase, "")
	if err != nil {
		t.Fatalf("create isolated scheduler cache temporary root in %q: %v", canonicalBase, err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove isolated scheduler cache temporary root %q: %v", root, err)
		}
	})
	if canonicalRoot, err := filepath.EvalSymlinks(root); err != nil {
		t.Fatalf("resolve isolated scheduler cache temporary root %q: %v", root, err)
	} else if canonicalRoot != root {
		t.Fatalf("isolated scheduler cache temporary root %q is not canonical: %q", root, canonicalRoot)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("isolated scheduler cache temporary root %q is not absolute", root)
	}
	if filepath.Clean(root) != root {
		t.Fatalf("isolated scheduler cache temporary root %q is not clean", root)
	}
	ensurePrivateTestDirectory(t, root)
	return root
}

func TestProductionProvisionSchedulerSocketPathFitsUnixBudget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket path budget does not apply on Windows")
	}
	cacheRoot := configureProductionProvisionSchedulerCacheHome(t)
	socketName := "s-" + strings.Repeat("a", 32) + ".sock"
	socketPath := filepath.Join(cacheRoot, "super-dolphin", "localci", socketName)
	if len(socketPath) > 100 {
		t.Fatalf("scheduler socket path is %d bytes, maximum is 100: %q", len(socketPath), socketPath)
	}
}

func ensurePrivateTestDirectory(t *testing.T, root string) {
	t.Helper()
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatalf("stat isolated scheduler cache temporary root %q: %v", root, err)
	}
	if !info.IsDir() {
		t.Fatalf("isolated scheduler cache temporary root %q is not an existing directory", root)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("isolated scheduler cache temporary root %q is a symlink", root)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("make isolated scheduler cache temporary root %q private: %v", root, err)
	}
	info, err = os.Lstat(root)
	if err != nil {
		t.Fatalf("restat isolated scheduler cache temporary root %q: %v", root, err)
	}
	if !info.IsDir() {
		t.Fatalf("isolated scheduler cache temporary root %q is no longer a directory", root)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("isolated scheduler cache temporary root %q became a symlink", root)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("isolated scheduler cache temporary root %q is not private", root)
	}
}

func createPrivateSchedulerCacheEnvironment(t *testing.T, root string) {
	t.Helper()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("LOCALAPPDATA", root)
}

func ensurePrivateCacheRoot(t *testing.T) string {
	t.Helper()
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("resolve isolated scheduler cache root: %v", err)
	}
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		t.Fatalf("create isolated scheduler cache root %q: %v", cacheRoot, err)
	}
	info, err := os.Stat(cacheRoot)
	if err != nil {
		t.Fatalf("stat isolated scheduler cache root %q: %v", cacheRoot, err)
	}
	if !info.IsDir() {
		t.Fatalf("isolated scheduler cache root %q is not a directory", cacheRoot)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("isolated scheduler cache root %q is not private", cacheRoot)
	}
	return cacheRoot
}

func TestCoordinatorProcessHelperInheritsIsolatedCacheRoot(t *testing.T) {
	want := configureProductionProvisionSchedulerCacheHome(t)
	command := exec.Command(os.Args[0], "-test.run=^TestCoordinatorProcessHelper$", "-test.count=1")
	command.Env = append(os.Environ(), "SD_COORDINATOR_HELPER=cache-root")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run cache-root helper: %v: %s", err, output)
	}
	if got := strings.TrimSuffix(string(output), "PASS\n"); got != want {
		t.Fatalf("child cache root = %q, want %q", got, want)
	}
}

func TestCoordinatorProcessHelperInheritsCanonicalCacheRootFromSymlinkedTempAlias(t *testing.T) {
	realBase := t.TempDir()
	canonicalRealBase, err := filepath.EvalSymlinks(realBase)
	if err != nil {
		t.Fatalf("resolve real scheduler cache temporary base: %v", err)
	}
	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "alias")
	if err := os.Symlink(realBase, alias); err != nil {
		t.Fatalf("create scheduler cache temporary alias: %v", err)
	}

	t.Run("canonicalizes-alias", func(t *testing.T) {
		root := canonicalPrivateTestTempRootAtBase(t, alias)
		assertCoordinatorProcessHelperInheritsCanonicalCacheRoot(t, root, canonicalRealBase)
	})
}

func assertCoordinatorProcessHelperInheritsCanonicalCacheRoot(t *testing.T, root, canonicalRealBase string) {
	t.Helper()
	createPrivateSchedulerCacheEnvironment(t, root)
	want := ensurePrivateCacheRoot(t)
	relative, err := filepath.Rel(canonicalRealBase, want)
	if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("canonical cache root %q is not below real temporary base %q: relative=%q err=%v", want, canonicalRealBase, relative, err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestCoordinatorProcessHelper$", "-test.count=1")
	command.Env = append(os.Environ(), "SD_COORDINATOR_HELPER=cache-root")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run canonical cache-root helper: %v: %s", err, output)
	}
	if got := strings.TrimSuffix(string(output), "PASS\n"); got != want {
		t.Fatalf("child canonical cache root = %q, want %q", got, want)
	}
}

func TestProductionProvisionSchedulerAuthorityPreservesDockerConfigAcrossCacheIsolation(t *testing.T) {
	dockerConfigDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dockerConfigDir)
	initial := productionProvisionSchedulerCheckpointFixture()
	checkpoint := isolateProductionProvisionSchedulerAuthority(
		t,
		initial,
		func(context.Context) (localci.DockerDaemonIdentityCheckpoint, error) {
			if got := os.Getenv("DOCKER_CONFIG"); got != dockerConfigDir {
				return localci.DockerDaemonIdentityCheckpoint{}, fmt.Errorf(
					"DOCKER_CONFIG = %q, want %q",
					got,
					dockerConfigDir,
				)
			}
			return initial, nil
		},
	)
	if checkpoint != initial {
		t.Fatalf("isolated checkpoint = %#v, want %#v", checkpoint, initial)
	}
}

const productionProvisionSchedulerAuthorityFailureModeEnv = "SD_PRODUCTION_PROVISION_SCHEDULER_AUTHORITY_FAILURE_MODE"

func TestProductionProvisionSchedulerAuthorityFailsFast(t *testing.T) {
	if mode := os.Getenv(productionProvisionSchedulerAuthorityFailureModeEnv); mode != "" {
		runProductionProvisionSchedulerAuthorityFailureMode(t, mode)
		return
	}
	for _, test := range []struct {
		name string
		mode string
		want string
	}{
		{
			name: "forbidden Docker override",
			mode: "forbidden-docker-override",
			want: "DOCKER_HOST override is set; active context identity probe requires it to be unset",
		},
		{
			name: "identity mismatch",
			mode: "identity-mismatch",
			want: "Docker scheduler identity key changed after cache isolation",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := runProductionProvisionSchedulerAuthorityFailureSubprocess(t, test.mode)
			if err == nil {
				t.Fatal("scheduler authority failure subprocess succeeded")
			}
			if !strings.Contains(string(output), test.want) {
				t.Fatalf("scheduler authority failure output did not contain %q", test.want)
			}
		})
	}
}

func runProductionProvisionSchedulerAuthorityFailureMode(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	initial := productionProvisionSchedulerCheckpointFixture()
	switch mode {
	case "forbidden-docker-override":
		t.Setenv("DOCKER_HOST", "unix:///private/forbidden-docker.sock")
		isolateProductionProvisionSchedulerAuthority(t, initial, func(context.Context) (localci.DockerDaemonIdentityCheckpoint, error) {
			return initial, nil
		})
	case "identity-mismatch":
		mismatched := initial
		mismatched.IdentityKey = "different-identity-key"
		isolateProductionProvisionSchedulerAuthority(t, initial, func(context.Context) (localci.DockerDaemonIdentityCheckpoint, error) {
			return mismatched, nil
		})
	default:
		t.Fatalf("unknown scheduler authority failure mode %q", mode)
	}
	t.Fatal("scheduler authority failure mode did not fail fast")
}

func runProductionProvisionSchedulerAuthorityFailureSubprocess(t *testing.T, mode string) ([]byte, error) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestProductionProvisionSchedulerAuthorityFailsFast$", "-test.count=1")
	command.Env = productionProvisionSchedulerAuthoritySubprocessEnvironment(os.Environ())
	command.Env = append(
		command.Env,
		productionProvisionSchedulerAuthorityFailureModeEnv+"="+mode,
	)
	return command.CombinedOutput()
}

func productionProvisionSchedulerAuthoritySubprocessEnvironment(environment []string) []string {
	forbidden := map[string]struct{}{
		"DOCKER_CONFIG":     {},
		"DOCKER_HOST":       {},
		"DOCKER_TLS":        {},
		"DOCKER_TLS_VERIFY": {},
		"DOCKER_CERT_PATH":  {},
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, excluded := forbidden[name]; !excluded {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func productionProvisionSchedulerCheckpointFixture() localci.DockerDaemonIdentityCheckpoint {
	return localci.DockerDaemonIdentityCheckpoint{
		ContextName: "desktop-linux",
		SchedulerConfig: localci.SchedulerConfig{
			Endpoint:       "unix:///private/docker-desktop.sock",
			TLSFingerprint: "tls-fingerprint",
			DaemonID:       "daemon-id",
			OwnerUID:       501,
		},
		IdentityKey: "identity-key",
	}
}

func submitProductionProvisionReleaseCLI(
	t *testing.T,
	execution productionProvisionDockerExecution,
) jobStatus {
	t.Helper()
	source := execution.fixture.repository
	command := exec.Command(
		execution.result.LauncherPath,
		"submit", "--profile", string(gatecontract.ProfileRelease),
		"--object-format", string(gatecontract.GitObjectFormatSHA1),
		"--source-tree", source.tree, "--commit", source.commit,
	)
	command.Dir = source.sourceRepo
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("production release CLI submit error = %v: %s", err, strings.TrimSpace(string(output)))
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	var status jobStatus
	if err := decoder.Decode(&status); err != nil {
		t.Fatalf("decode production release CLI submit status: %v: %s", err, strings.TrimSpace(string(output)))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("production release CLI submit emitted trailing data: %v", err)
	}
	if status.Profile != gatecontract.ProfileRelease || status.JobSourceTreeSHA != source.tree || status.JobID == "" {
		t.Fatalf("production release CLI submit status = %#v", status)
	}
	return status
}

func assertProductionProvisionReleaseRecord(
	t *testing.T,
	execution productionProvisionDockerExecution,
	submitted jobStatus,
	record coordinatorJobRecord,
	peak int64,
) {
	t.Helper()
	source := gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Commit:        &gatecontract.CommitSource{SHA: execution.fixture.repository.commit},
		SourceTreeSHA: execution.fixture.repository.tree,
	}
	expected, err := gatecontract.BuildGatePlan(gatecontract.ProfileRelease, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProductionProvisionReleaseGateSet(record, expected); err != nil {
		t.Fatal(err)
	}
	if err := validateProductionProvisionReleaseProvenance(execution, submitted, record, source, expected); err != nil {
		t.Fatal(err)
	}
	if err := validateProductionProvisionReleaseExecution(execution.accepted, record); err != nil {
		t.Fatal(err)
	}
	if peak < 1 || peak > 3 {
		t.Fatalf("production release active container peak = %d, want 1..3", peak)
	}
}

func validateProductionProvisionReleaseProvenance(
	execution productionProvisionDockerExecution,
	submitted jobStatus,
	record coordinatorJobRecord,
	source gatecontract.SourceSpec,
	expected gatecontract.GatePlan,
) error {
	if record.Profile != gatecontract.ProfileRelease || submitted.Profile != gatecontract.ProfileRelease {
		return fmt.Errorf("production release profile drifted: submitted=%q record=%q", submitted.Profile, record.Profile)
	}
	if !reflect.DeepEqual(record.Plan.Source, source) || record.JobSourceTreeSHA != source.SourceTreeSHA ||
		record.ImageProvenanceSourceTreeSHA != source.SourceTreeSHA {
		return fmt.Errorf("production release job source provenance drifted: %#v", record)
	}
	if record.Receipt == nil || !reflect.DeepEqual(record.Receipt.Source, source) {
		return fmt.Errorf("production release receipt source provenance drifted: %#v", record.Receipt)
	}
	return validateProductionProvisionReleaseReceiptIdentity(execution.accepted, record.Receipt, expected)
}

func validateProductionProvisionReleaseReceiptIdentity(
	accepted gatecontract.AcceptedImageRecord,
	receipt *gatecontract.ResultReceipt,
	expected gatecontract.GatePlan,
) error {
	if receipt.PlanDigest != expected.PlanDigest || receipt.PolicyDigest != expected.PolicyDigest {
		return fmt.Errorf("production release receipt plan identity drifted: %#v", receipt)
	}
	if !reflect.DeepEqual(receipt.Image, accepted.Image) {
		return fmt.Errorf("production release receipt image provenance drifted: %#v", receipt.Image)
	}
	return nil
}

func validateProductionProvisionReleaseExecution(
	accepted gatecontract.AcceptedImageRecord,
	record coordinatorJobRecord,
) error {
	if record.StartedAt == nil || record.Deadline == nil {
		return errors.New("production release started_at/deadline is incomplete")
	}
	if !record.Deadline.Equal(record.StartedAt.Add(coordinatorReleaseTimeout)) {
		return fmt.Errorf("production release deadline is not the exact 30m budget: %#v", record)
	}
	if record.Receipt == nil || !reflect.DeepEqual(record.Receipt.Image, accepted.Image) {
		return errors.New("production release receipt image drifted from accepted image")
	}
	if !record.Receipt.Container.Removed || !record.Receipt.Container.NetworkRemoved ||
		record.Receipt.Container.NetworkPolicyDigest == "" {
		return fmt.Errorf("production release container evidence drifted: %#v", record.Receipt.Container)
	}
	return nil
}

func validateProductionProvisionReleaseGateSet(
	record coordinatorJobRecord,
	expected gatecontract.GatePlan,
) error {
	if err := validateProductionProvisionReleaseGateCounts(record, expected); err != nil {
		return err
	}
	requiredRelease := map[gatecontract.GateID]bool{
		gatecontract.GateIDBackendTestGuardWithRace: false,
		gatecontract.GateIDBackendNilness:           false,
		gatecontract.GateIDReleaseLayeredCheck:      false,
	}
	for index, gate := range expected.Gates {
		if err := validateProductionProvisionReleaseGate(record, gate.ID, index); err != nil {
			return err
		}
		if _, ok := requiredRelease[gate.ID]; ok {
			requiredRelease[gate.ID] = true
		}
	}
	for gateID, present := range requiredRelease {
		if !present {
			return fmt.Errorf("production release required gate %q is absent", gateID)
		}
	}
	return nil
}

func validateProductionProvisionReleaseGateCounts(
	record coordinatorJobRecord,
	expected gatecontract.GatePlan,
) error {
	if record.Receipt == nil || len(expected.Gates) != 14 {
		return fmt.Errorf("production release gate receipt/count drifted: expected=%d record=%#v", len(expected.Gates), record)
	}
	if len(record.Plan.Gates) != len(expected.Gates) ||
		len(record.GateResults) != len(expected.Gates) ||
		len(record.Receipt.GateResults) != len(expected.Gates) {
		return fmt.Errorf("production release gate count drifted: expected=%d record=%#v", len(expected.Gates), record)
	}
	return nil
}

func validateProductionProvisionReleaseGate(
	record coordinatorJobRecord,
	gateID gatecontract.GateID,
	index int,
) error {
	if record.Plan.Gates[index].ID != gateID {
		return fmt.Errorf("production release plan gate[%d] = %q, want %q", index, record.Plan.Gates[index].ID, gateID)
	}
	if record.GateResults[index].GateID != string(gateID) ||
		record.Receipt.GateResults[index].GateID != string(gateID) {
		return fmt.Errorf("production release gate result[%d] identity drifted", index)
	}
	if record.GateResults[index].Status != gatecontract.GateStatusPassed ||
		record.Receipt.GateResults[index].Status != gatecontract.GateStatusPassed {
		return fmt.Errorf("production release gate result[%d] did not pass", index)
	}
	return nil
}

func TestProductionReleaseGateSetRequiresCanonicalFourteen(t *testing.T) {
	source := gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Commit:        &gatecontract.CommitSource{SHA: strings.Repeat("a", 40)},
		SourceTreeSHA: strings.Repeat("b", 40),
	}
	plan, err := gatecontract.BuildGatePlan(gatecontract.ProfileRelease, source)
	if err != nil {
		t.Fatal(err)
	}
	fixture := coordinatorJobRecord{
		JobID: "release-canonical-receipt", Plan: plan, Profile: gatecontract.ProfileRelease,
		JobSourceTreeSHA: source.SourceTreeSHA,
	}
	execution := mustTestCanonicalShardReceiptExecution(t, fixture)
	record := fixture
	record.GateResults = execution.Results
	record.Receipt = &gatecontract.ResultReceipt{
		SchemaVersion: gatecontract.ResultReceiptSchemaVersion,
		GateResults:   append([]gatecontract.GateResult(nil), execution.Results...),
		ShardReceipts: cloneContainerShardReceipts(execution.ShardReceipts),
	}
	if err := validateProductionProvisionReleaseGateSet(record, plan); err != nil {
		t.Fatalf("canonical release gate set rejected: %v", err)
	}
	record.Receipt.GateResults[8].GateID = string(gatecontract.GateIDFrontendLint)
	if err := validateProductionProvisionReleaseGateSet(record, plan); err == nil {
		t.Fatal("tampered release receipt gate set passed validation")
	}
}

type productionProvisionContainerSampler struct {
	cancel context.CancelFunc
	group  errgroup.Group
	peak   atomic.Int64
}

func startProductionProvisionContainerSampler(
	t *testing.T,
	sourceTree string,
) *productionProvisionContainerSampler {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sampler := &productionProvisionContainerSampler{cancel: cancel}
	t.Cleanup(cancel)
	sampler.group.Go(func() error {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			if err := sampler.sample(ctx, sourceTree); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	})
	return sampler
}

func (sampler *productionProvisionContainerSampler) sample(ctx context.Context, sourceTree string) error {
	command := exec.CommandContext(
		ctx, "docker", "ps", "-q", "--filter",
		"label="+coordinatorLabelJobSource+"="+sourceTree,
	)
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("sample production release containers: %w", err)
	}
	active := int64(len(bytes.Fields(output)))
	for current := sampler.peak.Load(); active > current && !sampler.peak.CompareAndSwap(current, active); {
		current = sampler.peak.Load()
	}
	if active > 3 {
		return fmt.Errorf("production release active containers = %d, exceeds global maximum 3", active)
	}
	return nil
}

func (sampler *productionProvisionContainerSampler) stop(t *testing.T) int64 {
	t.Helper()
	sampler.cancel()
	if err := sampler.group.Wait(); err != nil {
		t.Fatal(err)
	}
	return sampler.peak.Load()
}
