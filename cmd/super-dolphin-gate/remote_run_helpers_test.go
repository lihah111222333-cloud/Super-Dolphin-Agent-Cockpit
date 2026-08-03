package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// seedRemoteRunTestAcceptedGeneration writes an accepted baseline whose schema,
// generation, snapshot and content digest agree with the SQLite authority.
func seedRemoteRunTestAcceptedGeneration(t *testing.T, store *gatecontract.DurationLedgerStore, generation uint64) string {
	t.Helper()
	state := remoteRunTestAcceptedBaselineState(generation)
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal accepted baseline fixture: %v", err)
	}
	digest := sha256.Sum256(payload)
	stateSHA256 := fmt.Sprintf("sha256:%x", digest)
	database, err := sql.Open("sqlite", store.AuthorityPath())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO ci_remote_baseline_state (
		singleton, schema_version, generation, state_json, state_sha256, updated_at_unix_ms
	) VALUES (1, 3, ?, ?, ?, 1)
	ON CONFLICT(singleton) DO UPDATE SET
		schema_version = excluded.schema_version,
		generation = excluded.generation,
		state_json = excluded.state_json,
		state_sha256 = excluded.state_sha256,
		updated_at_unix_ms = excluded.updated_at_unix_ms`, strconv.FormatUint(generation, 10), payload, stateSHA256); err != nil {
		t.Fatal(err)
	}
	return stateSHA256
}

func remoteRunTestAcceptedBaselineState(generation uint64) remoteci.BaselineState {
	created := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	state := remoteRunRunnerIdentityState()
	state.SchemaVersion = remoteci.BaselineStateSchemaVersion
	state.Generation = generation
	state.MainCommit = strings.Repeat("a", 40)
	state.MainTree = strings.Repeat("b", 40)
	state.ImageCacheID = "imc-test-" + strconv.FormatUint(generation, 10)
	state.ImageCacheSnapshotID = "snapshot-test-" + strconv.FormatUint(generation, 10)
	state.ImageCacheReady = true
	state.ImageDigest = strings.TrimPrefix(state.RuntimeImage, strings.Split(state.RuntimeImage, "@")[0]+"@")
	state.OCIProjectCache = &remoteci.BaselineOCIProjectCache{
		Image: state.RuntimeImage, ContentManifestSHA256: testRemoteBaselineDigest("test OCI project cache content manifest"),
		MainTree: state.MainTree, ToolchainDigest: state.ToolchainDigest, Platform: state.Platform,
		CachePath: remoteci.OCIProjectGoBuildCachePath,
	}
	state.CreatedAt = created
	state.AcceptedAt = created.Add(time.Minute)
	state.RenewedAt = state.AcceptedAt
	return state
}

func validRemoteRunConfigJSON() string {
	return `{
	  "schema_version": 8,
  "aliyun_cli": "aliyun",
  "credential_profile": "super-dolphin-ci",
  "region_id": "cn-shenzhen",
  "vswitch_id": "vsw-test",
  "security_group_id": "sg-test",
  "worker_role_name": "super-dolphin-ci-worker",
  "oss": {"bucket": "super-dolphin-ci-test", "endpoint": "https://oss-cn-shenzhen.aliyuncs.com", "internal_endpoint": "https://oss-cn-shenzhen-internal.aliyuncs.com", "source_prefix": "source-bundles/"},
  "capacity": {"resource_policy": {"classes": [{"id": "small", "vcpu": 2, "memory_gib": 4}, {"id": "standard", "vcpu": 4, "memory_gib": 8}, {"id": "memory", "vcpu": 4, "memory_gib": 16}, {"id": "maximum", "vcpu": 8, "memory_gib": 32}], "bootstrap": {"guard": "small", "node_test": "standard", "go_test": "memory"}, "calibration_class": "maximum", "headroom_percent": 25, "min_samples_to_downsize": 5}}
}`
}

func remoteRunBaselineState(t *testing.T, repository string) remoteci.BaselineState {
	t.Helper()
	commit := remoteRunGitOutput(t, repository, "rev-parse", "HEAD^")
	tree := remoteRunGitOutput(t, repository, "rev-parse", commit+"^{tree}")
	created := time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)
	state := remoteRunRunnerIdentityState()
	state.SchemaVersion, state.Generation = remoteci.BaselineStateSchemaVersion, 1
	state.MainCommit, state.MainTree = commit, tree
	state.ImageCacheID, state.ImageCacheSnapshotID, state.ImageCacheReady = "imc-baseline-1", "snap-baseline-1", true
	state.ImageDigest = strings.TrimPrefix(state.RuntimeImage, strings.Split(state.RuntimeImage, "@")[0]+"@")
	state.CreatedAt, state.AcceptedAt = created, created.Add(time.Minute)
	state.RenewedAt = state.AcceptedAt
	state.OCIProjectCache = &remoteci.BaselineOCIProjectCache{Image: state.RuntimeImage, ContentManifestSHA256: testRemoteBaselineDigest("OCI project cache content manifest"), MainTree: state.MainTree, ToolchainDigest: state.ToolchainDigest, Platform: state.Platform, CachePath: remoteci.OCIProjectGoBuildCachePath}
	return state
}

func remoteRunRunnerIdentityState() remoteci.BaselineState {
	return remoteci.BaselineState{Platform: "linux/amd64", PolicyDigest: testRemoteBaselineDigest("remote baseline policy"), ToolchainDigest: testRemoteBaselineDigest("remote baseline toolchain"), RuntimeImage: "registry.example/runtime@" + testRemoteBaselineDigest("remote baseline runtime image"), GateBinarySHA256: testRemoteBaselineDigest("remote baseline gate binary"), RuntimeSeedSHA256: testRemoteBaselineDigest("remote baseline runtime seed"), BaselineManifestDigest: testRemoteBaselineDigest("remote baseline manifest")}
}

// testRemoteBaselineDigest keeps baseline fixtures bound to deterministic SHA-256 values.
func testRemoteBaselineDigest(value string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(value)))
}

func remoteRunRunnerIdentity(state remoteci.BaselineState) string {
	return remoteRunnerIdentityDigest(state, "sha256:"+strings.Repeat("f", 64))
}

func writeRemoteRunConfigFixture(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remote-ci.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("write remote CI config fixture: %v", err)
	}
	return path
}

func initRemoteRunGitFixture(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runRemoteRunGit(t, repository, "init", "--quiet", "-b", "main")
	runRemoteRunGit(t, repository, "config", "user.name", "Remote CI Test")
	runRemoteRunGit(t, repository, "config", "user.email", "remote-ci@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module example.com/remote-run-fixture\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write Git fixture go.mod: %v", err)
	}
	writeRemoteRunGateCompileFixture(t, repository)
	writeRemoteRunVitestPolicyFixture(t, repository)
	for index, contents := range []string{"base\n", "head\n"} {
		path := filepath.Join(repository, "fixture.txt")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write Git fixture %d: %v", index, err)
		}
		runRemoteRunGit(t, repository, "add", ".")
		runRemoteRunGit(t, repository, "commit", "--quiet", "-m", "测试提交")
	}
	return repository
}

func writeRemoteRunGateCompileFixture(t *testing.T, repository string) {
	t.Helper()
	files := map[string]string{
		"build/gate/Dockerfile": "FROM scratch\n",
		"build/gate/inputs.json": `{
  "schema_version": "2",
  "dockerfile": "build/gate/Dockerfile",
  "inputs": ["build/gate/Dockerfile", "build/gate/inputs.json", "build/gate/toolchain.lock", "cmd/super-dolphin-gate/main.go", "go.mod", "go.sum"],
  "gate_compile_inputs": ["cmd/super-dolphin-gate/main.go", "go.mod", "go.sum"]
}
`,
		"build/gate/toolchain.lock":      "{}\n",
		"cmd/super-dolphin-gate/main.go": "package main\n\nfunc main() {}\n",
		"go.sum":                         "",
	}
	for relativePath, contents := range files {
		filePath := filepath.Join(repository, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
			t.Fatalf("create gate compile fixture directory: %v", err)
		}
		if err := os.WriteFile(filePath, []byte(contents), 0o600); err != nil {
			t.Fatalf("write gate compile fixture %s: %v", relativePath, err)
		}
	}
}

func writeRemoteRunVitestPolicyFixture(t *testing.T, repository string) {
	t.Helper()
	path := filepath.Join(repository, "frontend-app", "config", "vitest-suite-policy.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create Vitest policy fixture directory: %v", err)
	}
	contents := `{"schemaVersion":1,"defaultExcludes":["**/node_modules/**"]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write Vitest policy fixture: %v", err)
	}
}

func runRemoteRunGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}

func remoteRunGitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}
