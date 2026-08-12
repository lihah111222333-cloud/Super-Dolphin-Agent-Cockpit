package main

import (
	"context"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const configuredRemotePassPreflightEnabled = "SUPER_DOLPHIN_CONFIGURED_PASS_PREFLIGHT"

// TestConfiguredRemotePassPreflight 对显式 exact commit 只执行生产 Prepare；
// 它读取 accepted SQLite 并输出复用决策，但不会进入 OSS、ECI 或持久化路径。
func TestConfiguredRemotePassPreflight(t *testing.T) {
	if os.Getenv(configuredRemotePassPreflightEnabled) != "1" {
		t.Skip("configured remote PASS preflight is not requested")
	}
	observer := &configuredRemotePassPreflightObserver{}
	options := configuredRemotePassPreflightOptions(t, observer)
	prepared, input := prepareConfiguredRemotePassPreflight(t, options)
	observer.logPrepareDiagnostics(t)
	hits, misses := observer.completedPrepareCounts(t)
	wantHits := requiredRemotePassPreflightInt(t, "SUPER_DOLPHIN_PASS_PREFLIGHT_EXPECT_HITS")
	wantMisses := requiredRemotePassPreflightInt(t, "SUPER_DOLPHIN_PASS_PREFLIGHT_EXPECT_MISSES")
	if hits != wantHits || misses != wantMisses {
		t.Fatalf("configured PASS preflight hits/misses = %d/%d, want %d/%d", hits, misses, wantHits, wantMisses)
	}
	if prepared.AllReused() != (misses == 0) {
		t.Fatalf("configured PASS preflight AllReused() = %t with %d misses", prepared.AllReused(), misses)
	}
	t.Logf("configured PASS preflight tree=%s hits=%d misses=%d", input.Tree, hits, misses)
	if os.Getenv("SUPER_DOLPHIN_PASS_PREFLIGHT_AUDIT_MISSES") == "1" {
		auditConfiguredRemotePassMisses(t, options.LedgerPath, input.AcceptedGeneration, prepared)
	}
}

func configuredRemotePassPreflightOptions(t *testing.T, observer remoteci.ProgressObserver) remoteRunOptions {
	t.Helper()
	agentToken, err := cicontract.GenerateAgentToken()
	if err != nil {
		t.Fatalf("generate preflight agent token: %v", err)
	}
	agentTokenDigest, err := cicontract.AgentTokenDigest(agentToken)
	if err != nil {
		t.Fatalf("digest preflight agent token: %v", err)
	}
	return remoteRunOptions{
		ConfigPath:       requiredRemotePassPreflightEnv(t, "SUPER_DOLPHIN_PASS_PREFLIGHT_CONFIG"),
		RepositoryRoot:   requiredRemotePassPreflightEnv(t, "SUPER_DOLPHIN_PASS_PREFLIGHT_REPOSITORY"),
		Commit:           requiredRemotePassPreflightEnv(t, "SUPER_DOLPHIN_PASS_PREFLIGHT_COMMIT"),
		Scenario:         "full",
		Entrypoint:       string(gatecontract.CIEntrypointRelease),
		LedgerPath:       requiredRemotePassPreflightEnv(t, "SUPER_DOLPHIN_PASS_PREFLIGHT_LEDGER"),
		AgentTokenDigest: agentTokenDigest,
		ProgressObserver: observer,
	}
}

func prepareConfiguredRemotePassPreflight(t *testing.T, options remoteRunOptions) (*remoteci.PreparedRun, remoteci.RunInput) {
	t.Helper()
	config, state, err := loadRunnableRemoteRunState(options)
	if err != nil {
		t.Fatalf("load configured remote run state: %v", err)
	}
	runnerIdentity, err := resolveRemoteRunnerIdentity(options.RepositoryRoot, state)
	if err != nil {
		t.Fatalf("resolve configured runner identity: %v", err)
	}
	input, err := resolveRemoteRunInput(options, state, runnerIdentity)
	if err != nil {
		t.Fatalf("resolve configured remote input: %v", err)
	}
	wantTree := requiredRemotePassPreflightEnv(t, "SUPER_DOLPHIN_PASS_PREFLIGHT_TREE")
	if input.Tree != wantTree {
		t.Fatalf("configured preflight tree = %q, want %q", input.Tree, wantTree)
	}
	coordinator, _, err := newRemoteRunCoordinator(config, input, options.ProgressObserver)
	if err != nil {
		t.Fatalf("create configured preflight coordinator: %v", err)
	}
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("configured remote Prepare() error = %v", err)
	}
	return prepared, input
}

type configuredRemotePassPreflightObserver struct {
	mu          sync.Mutex
	events      []remoteci.ProgressEvent
	diagnostics []remoteci.ReuseDiagnostic
}

// ObserveRemoteCIProgress 保留只读 Prepare 事件，供 exact hit/miss 断言使用。
func (observer *configuredRemotePassPreflightObserver) ObserveRemoteCIProgress(event remoteci.ProgressEvent) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(observer.events, event)
}

// ObserveRemoteCIReuseDiagnostic 保留聚合复用归因，供只读性能预检输出拒绝原因。
func (observer *configuredRemotePassPreflightObserver) ObserveRemoteCIReuseDiagnostic(diagnostic remoteci.ReuseDiagnostic) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.diagnostics = append(observer.diagnostics, diagnostic)
}

// logPrepareDiagnostics 输出每个 Prepare 子阶段增量耗时及聚合 replay 计数。
func (observer *configuredRemotePassPreflightObserver) logPrepareDiagnostics(t *testing.T) {
	t.Helper()
	observer.mu.Lock()
	events := append([]remoteci.ProgressEvent(nil), observer.events...)
	diagnostics := append([]remoteci.ReuseDiagnostic(nil), observer.diagnostics...)
	observer.mu.Unlock()
	var previousElapsedMS int64
	for _, event := range events {
		t.Logf("configured PASS preflight stage=%s elapsed_ms=%d delta_ms=%d hits=%d misses=%d", event.State, event.ElapsedMS, event.ElapsedMS-previousElapsedMS, event.CacheHits, event.CacheMisses)
		previousElapsedMS = event.ElapsedMS
	}
	for _, diagnostic := range diagnostics {
		replay := diagnostic.Replay
		t.Logf("configured PASS preflight reuse direct=%d source=%d environment=%d exact=%d effective=%d/%d source_candidates=%d trees=%d source_input_unavailable=%d source_input_mismatch=%d environment_hints=%d current_worker_mismatch=%d historical_mismatch=%d input_mismatch=%d calibration_demoted=%d", diagnostic.DirectHits, diagnostic.SourceReplayHits, diagnostic.EnvironmentReplayHits, diagnostic.ExactHits, diagnostic.EffectiveHits, diagnostic.EffectiveMisses, replay.SourceCandidates, replay.SourceCandidateTrees, replay.SourceInputUnavailable, replay.SourceInputMismatch, replay.EnvironmentHints, replay.EnvironmentCurrentWorkerMismatch, replay.EnvironmentHistoricalMismatch, replay.EnvironmentInputMismatch, diagnostic.CalibrationDurationDemoted)
	}
}

func (observer *configuredRemotePassPreflightObserver) completedPrepareCounts(t *testing.T) (int, int) {
	t.Helper()
	observer.mu.Lock()
	defer observer.mu.Unlock()
	for _, event := range observer.events {
		if event.Phase != remoteci.ProgressPhasePrepare {
			t.Fatalf("configured PASS preflight emitted non-Prepare phase %q", event.Phase)
		}
		if event.State == "completed" {
			return event.CacheHits, event.CacheMisses
		}
	}
	t.Fatal("configured PASS preflight did not emit a completed Prepare event")
	return 0, 0
}

func requiredRemotePassPreflightEnv(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		t.Fatalf("configured PASS preflight requires %s", key)
	}
	return value
}

func requiredRemotePassPreflightInt(t *testing.T, key string) int {
	t.Helper()
	value, err := strconv.Atoi(requiredRemotePassPreflightEnv(t, key))
	if err != nil || value < 0 {
		t.Fatalf("configured PASS preflight %s is invalid", key)
	}
	return value
}
