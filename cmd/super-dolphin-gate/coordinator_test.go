package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"golang.org/x/sync/errgroup"
)

type fakeImageEnsurer struct{}

func (fakeImageEnsurer) EnsureImage(_ context.Context, request imageEnsureRequest) (ensuredImage, error) {
	accepted := testAcceptedImageRecord(request.Plan)
	return ensuredImage{
		Identity: accepted.Image, AcceptedRecord: accepted,
		ImageProvenanceSourceTreeSHA: accepted.SourceTree,
		Truth: localci.FreshContainerImageTruth{
			PolicyDigest: request.Plan.PolicyDigest, InputDigest: "sha256:" + strings.Repeat("3", 64),
			ToolchainDigest: "sha256:" + strings.Repeat("4", 64), SchemaVersion: "1",
			BuildSourceTreeSHA: accepted.SourceTree,
		},
	}, nil
}

type fakeCandidateBuildService struct{}

func (fakeCandidateBuildService) ExecuteBuild(context.Context, string) error { return nil }

type fakePromotionWatcher struct{}

func (fakePromotionWatcher) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

type oneShotCandidatePlanner struct {
	mu   sync.Mutex
	plan localci.PromotionCandidatePlan
}

func (planner *oneShotCandidatePlanner) PlanCandidate(
	context.Context,
	imageEnsureRequest,
) (localci.PromotionCandidatePlan, error) {
	planner.mu.Lock()
	defer planner.mu.Unlock()
	plan := planner.plan
	planner.plan = localci.PromotionCandidatePlan{}
	return plan, nil
}

type delayedCandidatePlanner struct {
	delay time.Duration
	err   error
}

func (planner delayedCandidatePlanner) PlanCandidate(
	ctx context.Context,
	_ imageEnsureRequest,
) (localci.PromotionCandidatePlan, error) {
	timer := time.NewTimer(planner.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return localci.PromotionCandidatePlan{}, ctx.Err()
	case <-timer.C:
		return localci.PromotionCandidatePlan{}, planner.err
	}
}

type countingCandidatePlanner struct {
	mu    sync.Mutex
	calls int
}

func (planner *countingCandidatePlanner) PlanCandidate(
	context.Context,
	imageEnsureRequest,
) (localci.PromotionCandidatePlan, error) {
	planner.mu.Lock()
	defer planner.mu.Unlock()
	planner.calls++
	return localci.PromotionCandidatePlan{}, nil
}

func (planner *countingCandidatePlanner) callCount() int {
	planner.mu.Lock()
	defer planner.mu.Unlock()
	return planner.calls
}

type blockingCandidateBuildService struct {
	started chan string
	release chan struct{}
}

type coordinatorSlotTestFixture struct {
	buildService *blockingCandidateBuildService
	runner       *blockingFreshRunner
	client       *coordinatorTransportClient
}

func (service *blockingCandidateBuildService) ExecuteBuild(ctx context.Context, workloadID string) error {
	service.started <- workloadID
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-service.release:
		return nil
	}
}

type fakeSourceMaterializer struct{}

func (fakeSourceMaterializer) Materialize(
	_ context.Context,
	request sourceMaterializeRequest,
) (materializedJobSource, error) {
	if err := os.MkdirAll(request.OutputRoot, 0o700); err != nil {
		return materializedJobSource{}, err
	}
	return materializedJobSource{
		SnapshotDir: request.OutputRoot, SourceTreeSHA: request.Source.SourceTreeSHA,
		Cleanup: func() error { return os.RemoveAll(request.OutputRoot) },
	}, nil
}

type immediateFreshRunner struct{}

type failingResultReceiptSigner struct{}

func (failingResultReceiptSigner) SignResultReceipt(
	gatecontract.ResultReceipt,
) (gatecontract.ResultReceipt, error) {
	return gatecontract.ResultReceipt{}, errors.New("injected receipt signing failure")
}

func (immediateFreshRunner) RunFreshContainer(
	ctx context.Context,
	request freshContainerRequest,
) (localci.FreshContainerResult, error) {
	return successfulFreshContainerResult(ctx, request)
}

type competingOwnerStarter struct {
	checkpoint   localci.DockerDaemonIdentityCheckpoint
	dependencies coordinatorDependencies
	mu           sync.Mutex
	cancel       context.CancelFunc
	group        *errgroup.Group
}

type blockingOwnerStarter struct{}

func (blockingOwnerStarter) StartCoordinatorOwner(
	ctx context.Context,
	_ localci.DockerDaemonIdentityCheckpoint,
) error {
	<-ctx.Done()
	return ctx.Err()
}

func (starter *competingOwnerStarter) StartCoordinatorOwner(
	_ context.Context,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
) error {
	owner, err := openCoordinatorOwner(context.Background(), checkpoint, starter.dependencies)
	if err != nil {
		return err
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	group := &errgroup.Group{}
	group.Go(func() error { return owner.Serve(serveCtx) })
	starter.mu.Lock()
	starter.cancel, starter.group = cancel, group
	starter.mu.Unlock()
	return nil
}

func (starter *competingOwnerStarter) stop(t *testing.T) {
	t.Helper()
	starter.mu.Lock()
	cancel, group := starter.cancel, starter.group
	starter.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if group != nil {
		if err := group.Wait(); err != nil {
			t.Errorf("competing owner Serve() error = %v", err)
		}
	}
}

func TestConnectCoordinatorCompetingStartersConverge(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	dependencies := coordinatorDependencies{
		ImageEnsurer: fakeImageEnsurer{}, CandidateBuilder: fakeCandidateBuildService{},
		PromotionWatcher: fakePromotionWatcher{}, SourceMaterializer: fakeSourceMaterializer{},
		FreshRunner: immediateFreshRunner{}, RecoveryRunner: &capturingFreshContainerRunner{},
		ReceiptSigner: mustTestResultReceiptSigner(t),
	}
	starters := []*competingOwnerStarter{
		{checkpoint: checkpoint, dependencies: dependencies},
		{checkpoint: checkpoint, dependencies: dependencies},
	}
	clients := make([]*coordinatorTransportClient, 2)
	group := errgroup.Group{}
	for index := range clients {
		group.Go(func() error {
			client, err := connectCoordinator(context.Background(), checkpoint, starters[index])
			clients[index] = client
			return err
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatalf("connectCoordinator() race error = %v", err)
	}
	for _, client := range clients {
		if client == nil {
			t.Fatal("competing connector returned nil client")
		}
		if err := client.Close(); err != nil {
			t.Errorf("client.Close() error = %v", err)
		}
	}
	for _, starter := range starters {
		starter.stop(t)
	}
}

func TestCoordinatorDuplicateJobEnqueueIsIdempotent(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	runner := &blockingFreshRunner{
		seen: make(map[string]bool), started: make(chan freshContainerRequest, 1), release: make(chan struct{}),
	}
	startTestCoordinatorOwner(t, checkpoint, runner)
	client := dialTestCoordinator(t, checkpoint)
	ctx := context.Background()
	invocationID := "hook-" + strings.Repeat("8", 64)
	jobID := "job-" + strings.Repeat("8", 32)
	plan := mustTestGatePlan(t, "e")
	record, err := client.store.createJob(
		ctx, invocationID, jobID, mustWorkingDirectory(t), plan, localci.PromotionCandidatePlan{}, manualSubmissionAuthority(),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := localci.WorkloadRequest{
		ID: record.JobID, InvocationID: record.InvocationID, EnqueueSequence: record.EnqueueSequence,
		Subsequence: record.SchedulerSubsequence, Kind: localci.WorkloadKindJob,
		Dependencies: append([]string(nil), record.SchedulerDependencies...),
	}
	if err := client.scheduler.Enqueue(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := client.enqueuePersistedJob(ctx, record); err != nil {
		t.Fatalf("duplicate enqueue error = %v", err)
	}
	reloaded, err := client.store.job(ctx, record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State == jobStateInfraFailed {
		t.Fatalf("duplicate enqueue marked durable job infra_failed: %#v", reloaded)
	}
	close(runner.release)
	terminal, err := client.Wait(context.Background(), record.JobID)
	if err != nil || terminal.State != jobStatePassed {
		t.Fatalf("duplicate enqueue terminal=%#v err=%v", terminal, err)
	}
}

func TestCoordinatorCandidateBuildBlocksThreeShardGangUntilAllSlotsFree(t *testing.T) {
	fixture := startCoordinatorSlotTestFixture(t)
	client := fixture.client
	client.candidatePlanner = &oneShotCandidatePlanner{plan: localci.PromotionCandidatePlan{
		BuildRequired: true, WorkloadID: "build-candidate-slot-proof",
	}}
	status := submitTestPlan(t, client, "1")
	waitCoordinatorSharedSlots(t, fixture)
	snapshot, err := client.scheduler.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertCoordinatorSharedSlotSnapshot(t, snapshot, status)
	fixture.runner.release <- struct{}{}
	fixture.runner.waitForLifecycle(t, 1)
	terminal, err := client.Wait(context.Background(), status.JobID)
	if err != nil || terminal.State != jobStatePassed {
		t.Fatalf("gang job terminal=%#v err=%v", terminal, err)
	}
}

func startCoordinatorSlotTestFixture(t *testing.T) coordinatorSlotTestFixture {
	t.Helper()
	checkpoint := coordinatorTestCheckpoint(t)
	buildService := &blockingCandidateBuildService{started: make(chan string, 1), release: make(chan struct{})}
	runner := &blockingFreshRunner{
		seen: make(map[string]bool), started: make(chan freshContainerRequest, 3), release: make(chan struct{}),
		lifecycleComplete: make(chan struct{}, 1),
	}
	owner, err := openCoordinatorOwner(context.Background(), checkpoint, coordinatorDependencies{
		ImageEnsurer: fakeImageEnsurer{}, CandidateBuilder: buildService,
		PromotionWatcher: fakePromotionWatcher{}, SourceMaterializer: fakeSourceMaterializer{}, FreshRunner: runner,
		RecoveryRunner: &capturingFreshContainerRunner{},
		ReceiptSigner:  mustTestResultReceiptSigner(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	group := errgroup.Group{}
	group.Go(func() error { return owner.Serve(serveCtx) })
	t.Cleanup(func() {
		cancel()
		close(buildService.release)
		close(runner.release)
		if err := group.Wait(); !isExpectedCoordinatorServeShutdown(err) {
			t.Errorf("owner Serve() error = %v", err)
		}
	})
	client := dialTestCoordinator(t, checkpoint)
	return coordinatorSlotTestFixture{buildService: buildService, runner: runner, client: client}
}

func assertCoordinatorSharedSlotSnapshot(t *testing.T, snapshot localci.SchedulerSnapshot, status jobStatus) {
	t.Helper()
	if len(snapshot.Leases) != gatecontract.MaxContainerShards {
		t.Fatalf("active gang leases = %d, want %d", len(snapshot.Leases), gatecontract.MaxContainerShards)
	}
	groupIdentity := snapshot.Leases[0].GroupIdentity
	if groupIdentity == "" {
		t.Fatal("active shard gang lacks group identity")
	}
	for _, lease := range snapshot.Leases {
		if lease.Kind == localci.WorkloadKindBuild || lease.GroupIdentity != groupIdentity {
			t.Fatalf("non-atomic shard gang lease=%+v all=%+v", lease, snapshot.Leases)
		}
	}
	assertCoordinatorWorkload(t, snapshot, status.JobID, localci.WorkloadStatusPassed, "build-candidate-slot-proof")
	assertCoordinatorWorkload(t, snapshot, status.JobID+"/shards", localci.WorkloadStatusStarted)
}

func assertCoordinatorWorkload(
	t *testing.T,
	snapshot localci.SchedulerSnapshot,
	workloadID string,
	status localci.WorkloadStatus,
	dependencies ...string,
) {
	t.Helper()
	for _, workload := range snapshot.Workloads {
		if workload.Request.ID != workloadID {
			continue
		}
		if workload.Status != status || !slices.Equal(workload.Request.Dependencies, dependencies) {
			t.Fatalf("workload %q = %+v", workloadID, workload)
		}
		return
	}
	t.Fatalf("workload %q is missing", workloadID)
}

func TestConnectCoordinatorDeadlineCoversOwnerStarter(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	_, err := connectCoordinator(ctx, checkpoint, blockingOwnerStarter{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("connectCoordinator() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("connectCoordinator() elapsed = %v, starter escaped deadline", elapsed)
	}
}

func TestOwnerHandshakeDeadlineKillsAndReapsChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCoordinatorProcessHelper$", "-test.count=1")
	command.Env = append(os.Environ(), "SD_COORDINATOR_HELPER=hang-handshake")
	startedAt := time.Now()
	err := startCoordinatorOwnerCommand(ctx, command)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("startCoordinatorOwnerCommand() error = %v, want deadline exceeded", err)
	}
	if command.ProcessState == nil {
		t.Fatal("timed-out owner child was not waited and reaped")
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("owner handshake cancellation elapsed = %v", elapsed)
	}
}

func TestOwnerSurvivesHandshakeContextCancellation(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "owner-survived")
	ctx, cancel := context.WithCancel(context.Background())
	command := newCoordinatorOwnerCommand(
		os.Args[0], "-test.run=^TestCoordinatorProcessHelper$", "-test.count=1",
	)
	command.Env = append(os.Environ(),
		"SD_COORDINATOR_HELPER=ready-after-handshake",
		"SD_COORDINATOR_SENTINEL="+sentinel,
	)
	if err := startCoordinatorOwnerCommand(ctx, command); err != nil {
		cancel()
		t.Fatalf("startCoordinatorOwnerCommand() error = %v", err)
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(sentinel); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("owner child stopped when handshake context was cancelled")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFreshContainerRequestFieldRegistryIsComplete(t *testing.T) {
	producer := reflect.TypeFor[freshContainerRequest]()
	registry := map[string]string{
		"Image": "runner image identity", "ImageTruth": "runner accepted image truth",
		"ImageProvenanceSourceTreeSHA": "accepted image provenance", "JobSourceTreeSHA": "submitted job source",
		"SourceSnapshotDir": "materialized source", "Profile": "timeout contract",
		"Plan": "canonical execution plan", "GateID": "single fresh-container gate",
		"PlanExecution":   "single-container canonical plan execution",
		"ShardGateIDs":    "exact canonical shard command",
		"ShardIdentity":   "durable shard identity",
		"ContainerLabels": "durable container identity", "Deadline": "first-start deadline",
		"ClaimDeadline": "durable shared shard execution clock",
		"LifecycleHook": "durable lifecycle transition",
	}
	for index := range producer.NumField() {
		field := producer.Field(index).Name
		if registry[field] == "" {
			t.Fatalf("freshContainerRequest field %q is not registered", field)
		}
		delete(registry, field)
	}
	if len(registry) != 0 {
		t.Fatalf("freshContainerRequest field registry has stale entries: %v", registry)
	}
}

func TestCoordinatorTwoCLIProcessesShareOwnerAndShardGroupsRunFIFO(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	runner := &blockingFreshRunner{
		seen: make(map[string]bool), started: make(chan freshContainerRequest, 4), release: make(chan struct{}, 4),
		lifecycleComplete: make(chan struct{}, 4),
	}
	startTestCoordinatorOwner(t, checkpoint, runner)

	processStatuses := runTwoCLIProcessSubmits(t, checkpoint)
	client := dialTestCoordinator(t, checkpoint)
	statuses := append(processStatuses,
		submitTestPlan(t, client, "3"),
		submitTestPlan(t, client, "4"),
	)
	sortCoordinatorJobStatusesByEnqueueSequence(statuses)
	assertCoordinatorShardGroupsRunFIFO(t, client, runner, statuses)
	assertCoordinatorFIFOJobsTerminal(t, client, statuses)
}

func TestCoordinatorStatusAndWaitCLIEmitStructuredState(t *testing.T) {
	client := &scriptedCoordinatorClient{status: jobStatus{JobID: "job-1", State: jobStatePassed, Terminal: true}}
	connector := func(context.Context) (coordinatorClient, error) { return client, nil }
	statusOutput := &bytes.Buffer{}
	if err := runStatusWithConnector([]string{"--job", "job-1"}, statusOutput, connector); err != nil {
		t.Fatalf("runStatusWithConnector() error = %v", err)
	}
	waitOutput := &bytes.Buffer{}
	if err := runWaitWithConnector([]string{"--job", "job-1"}, waitOutput, connector); err != nil {
		t.Fatalf("runWaitWithConnector() error = %v", err)
	}
	for _, output := range []*bytes.Buffer{statusOutput, waitOutput} {
		var status jobStatus
		if err := json.Unmarshal(output.Bytes(), &status); err != nil || status.JobID != "job-1" || !status.Terminal {
			t.Fatalf("structured status=%#v error=%v output=%s", status, err, output.String())
		}
	}
}

func TestCoordinatorHookInvocationIsIdempotentAndSourceBound(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	runner := &blockingFreshRunner{
		seen: make(map[string]bool), started: make(chan freshContainerRequest, 1), release: make(chan struct{}),
	}
	startTestCoordinatorOwner(t, checkpoint, runner)
	client := dialTestCoordinator(t, checkpoint)
	request := submitRequest{
		RepositoryRoot:       mustWorkingDirectory(t),
		InvocationID:         "hook-" + strings.Repeat("1", 64),
		Plan:                 mustTestGatePlan(t, "7"),
		Entrypoint:           gatecontract.CIEntrypointGitPreCommit,
		AuthorityOwner:       gatecontract.CIEntrypointOwnerManagedGitPreCommit,
		AuthorityAttestation: "sha256:" + strings.Repeat("1", 64),
	}

	first, err := client.Submit(context.Background(), request)
	if err != nil {
		t.Fatalf("first Submit() error = %v", err)
	}
	waitForStartedJobs(t, runner.started, 1)
	second, err := client.Submit(context.Background(), request)
	if err != nil {
		t.Fatalf("idempotent Submit() error = %v", err)
	}
	if first.JobID != second.JobID || second.InvocationID != request.InvocationID {
		t.Fatalf("idempotent submit first=%#v second=%#v", first, second)
	}

	changed := request
	changed.Plan = mustTestGatePlan(t, "8")
	if _, err := client.Submit(context.Background(), changed); !errors.Is(err, errCoordinatorState) {
		t.Fatalf("changed-source Submit() error = %v, want coordinator state rejection", err)
	}
	close(runner.release)
	terminal, err := client.Wait(context.Background(), first.JobID)
	if err != nil || terminal.State != jobStatePassed {
		t.Fatalf("Wait() status=%#v error=%v", terminal, err)
	}
}

func TestProductionCoordinatorDependenciesFailFast(t *testing.T) {
	t.Setenv(productionCoordinatorConfigEnv, "")
	if _, err := productionCoordinatorDependencies(context.Background()); err == nil ||
		!strings.Contains(err.Error(), productionCoordinatorConfigEnv) {
		t.Fatalf("productionCoordinatorDependencies() error = %v", err)
	}
}

type scriptedCoordinatorClient struct {
	status jobStatus
}

func (client *scriptedCoordinatorClient) Submit(context.Context, submitRequest) (jobStatus, error) {
	return client.status, nil
}

func (client *scriptedCoordinatorClient) Status(context.Context, string) (jobStatus, error) {
	return client.status, nil
}

func (client *scriptedCoordinatorClient) Wait(context.Context, string) (jobStatus, error) {
	return client.status, nil
}

func (*scriptedCoordinatorClient) Close() error { return nil }

func TestCoordinatorProcessHelper(t *testing.T) {
	switch os.Getenv("SD_COORDINATOR_HELPER") {
	case "":
		t.Skip("process helper")
	case "hang-handshake":
		_, _ = fmt.Fprint(os.Stdout, `{"ready":true}`)
		time.Sleep(time.Minute)
		return
	case "ready-after-handshake":
		_, _ = fmt.Fprintln(os.Stdout, `{"ready":true}`)
		time.Sleep(100 * time.Millisecond)
		if err := os.WriteFile(os.Getenv("SD_COORDINATOR_SENTINEL"), []byte("ready\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	case "cache-root":
		cacheRoot, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprint(os.Stdout, cacheRoot)
		return
	case "submit":
	default:
		t.Fatal("unknown coordinator process helper mode")
	}
	checkpoint := checkpointFromHelperEnvironment(t)
	character := os.Getenv("SD_COORDINATOR_TREE_CHARACTER")
	connector := func(ctx context.Context) (coordinatorClient, error) { return dialCoordinator(ctx, checkpoint) }
	args := testPlanArgs(character)
	if err := runSubmitWithConnector(args, os.Stdout, connector); err != nil {
		t.Fatal(err)
	}
}

func runTwoCLIProcessSubmits(
	t *testing.T,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
) []jobStatus {
	t.Helper()
	statuses := make([]jobStatus, 2)
	group := errgroup.Group{}
	for index, character := range []string{"1", "2"} {
		group.Go(func() error {
			status, err := runCLIProcessSubmit(t, checkpoint, character)
			statuses[index] = status
			return err
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatalf("CLI process submit error = %v", err)
	}
	return statuses
}

func runCLIProcessSubmit(
	t *testing.T,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	character string,
) (jobStatus, error) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestCoordinatorProcessHelper$", "-test.count=1")
	command.Env = append(os.Environ(),
		"SD_COORDINATOR_HELPER=submit",
		"SD_COORDINATOR_ENDPOINT="+checkpoint.SchedulerConfig.Endpoint,
		"SD_COORDINATOR_DAEMON_ID="+checkpoint.SchedulerConfig.DaemonID,
		"SD_COORDINATOR_IDENTITY_KEY="+checkpoint.IdentityKey,
		"SD_COORDINATOR_TREE_CHARACTER="+character,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		return jobStatus{}, fmt.Errorf("run CLI helper: %w: %s", err, output)
	}
	var status jobStatus
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&status); err != nil {
		return jobStatus{}, fmt.Errorf("decode CLI helper status: %w", err)
	}
	return status, nil
}

func checkpointFromHelperEnvironment(t *testing.T) localci.DockerDaemonIdentityCheckpoint {
	t.Helper()
	return localci.DockerDaemonIdentityCheckpoint{
		SchedulerConfig: localci.SchedulerConfig{
			Endpoint: os.Getenv("SD_COORDINATOR_ENDPOINT"),
			DaemonID: os.Getenv("SD_COORDINATOR_DAEMON_ID"), OwnerUID: os.Getuid(),
		},
		IdentityKey: os.Getenv("SD_COORDINATOR_IDENTITY_KEY"),
	}
}

func coordinatorTestCheckpoint(t *testing.T) localci.DockerDaemonIdentityCheckpoint {
	t.Helper()
	daemonID := fmt.Sprintf("test-daemon-%d", time.Now().UnixNano())
	endpoint := "unix:///tmp/" + daemonID + ".sock"
	digest := sha256.Sum256([]byte(endpoint + "\x00\x00" + daemonID))
	checkpoint := localci.DockerDaemonIdentityCheckpoint{
		SchedulerConfig: localci.SchedulerConfig{Endpoint: endpoint, DaemonID: daemonID, OwnerUID: os.Getuid()},
		IdentityKey:     hex.EncodeToString(digest[:]),
	}
	t.Cleanup(func() { removeCoordinatorTestState(t, checkpoint) })
	return checkpoint
}

func startTestCoordinatorOwner(
	t *testing.T,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	runner FreshContainerRunner,
) *coordinatorOwner {
	return startTestCoordinatorOwnerWithSigner(t, checkpoint, runner, mustTestResultReceiptSigner(t))
}

func startTestCoordinatorOwnerWithSigner(
	t *testing.T,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	runner FreshContainerRunner,
	signer resultReceiptSigner,
) *coordinatorOwner {
	t.Helper()
	owner, err := openCoordinatorOwner(context.Background(), checkpoint, coordinatorDependencies{
		ImageEnsurer: fakeImageEnsurer{}, CandidateBuilder: fakeCandidateBuildService{},
		PromotionWatcher: fakePromotionWatcher{}, SourceMaterializer: fakeSourceMaterializer{}, FreshRunner: runner,
		RecoveryRunner: &capturingFreshContainerRunner{},
		ReceiptSigner:  signer,
	})
	if err != nil {
		t.Fatalf("openCoordinatorOwner() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	group := errgroup.Group{}
	group.Go(func() error { return owner.Serve(ctx) })
	t.Cleanup(func() {
		cancel()
		if err := group.Wait(); !isExpectedCoordinatorServeShutdown(err) {
			t.Errorf("coordinator owner Serve() error = %v", err)
		}
	})
	return owner
}

func dialTestCoordinator(t *testing.T, checkpoint localci.DockerDaemonIdentityCheckpoint) *coordinatorTransportClient {
	t.Helper()
	client, err := dialCoordinator(context.Background(), checkpoint)
	if err != nil {
		t.Fatalf("dialCoordinator() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("client.Close() error = %v", err)
		}
	})
	return client
}

func submitTestPlan(t *testing.T, client *coordinatorTransportClient, character string) jobStatus {
	t.Helper()
	plan := mustTestGatePlan(t, character)
	status, err := client.Submit(context.Background(), submitRequest{RepositoryRoot: mustWorkingDirectory(t), Plan: plan, Entrypoint: manualSubmissionAuthority().Entrypoint, AuthorityOwner: manualSubmissionAuthority().Owner})
	if err != nil {
		t.Fatalf("Submit(%s) error = %v", character, err)
	}
	return status
}

func mustTestGatePlan(t *testing.T, character string) gatecontract.GatePlan {
	t.Helper()
	plan, err := gatecontract.BuildGatePlan(gatecontract.ProfileLocalFast, gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Commit:        &gatecontract.CommitSource{SHA: strings.Repeat("a", 40)},
		SourceTreeSHA: strings.Repeat(character, 40),
	})
	if err != nil {
		t.Fatalf("BuildGatePlan() error = %v", err)
	}
	return plan
}

func testPlanArgs(character string) []string {
	return []string{
		"--profile", "local-fast", "--object-format", "sha1",
		"--commit", strings.Repeat("a", 40), "--source-tree", strings.Repeat(character, 40),
	}
}

func waitForStartedJobs(t *testing.T, started <-chan freshContainerRequest, count int) {
	t.Helper()
	for range count {
		select {
		case request := <-started:
			if request.ImageProvenanceSourceTreeSHA == request.JobSourceTreeSHA {
				t.Fatal("runner request collapsed provenance and job source trees")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for scheduled job")
		}
	}
}

func mustWorkingDirectory(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func removeCoordinatorTestState(t *testing.T, checkpoint localci.DockerDaemonIdentityCheckpoint) {
	t.Helper()
	runtimeRoot, err := coordinatorRuntimeRoot()
	if err != nil {
		t.Errorf("coordinatorRuntimeRoot() error = %v", err)
		return
	}
	prefixes := []string{
		"localci-coordinator-" + checkpoint.IdentityKey + ".db",
		"localci-scheduler-" + checkpoint.IdentityKey + ".db",
		"localci-scheduler-" + checkpoint.IdentityKey + ".lock",
		"s-" + checkpoint.IdentityKey[:32] + ".sock",
	}
	for _, prefix := range prefixes {
		matches, _ := filepath.Glob(filepath.Join(runtimeRoot, prefix+"*"))
		for _, match := range matches {
			if err := os.Remove(match); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove test state %s: %v", match, err)
			}
		}
	}
}
