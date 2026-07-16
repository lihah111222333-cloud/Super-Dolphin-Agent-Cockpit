package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"golang.org/x/sync/errgroup"
)

type blockingFreshRunner struct {
	mu      sync.Mutex
	seen    map[string]bool
	started chan freshContainerRequest
	release chan struct{}
}

func (runner *blockingFreshRunner) RunFreshContainer(
	ctx context.Context,
	request freshContainerRequest,
) (localci.FreshContainerResult, error) {
	runner.mu.Lock()
	first := !runner.seen[request.JobSourceTreeSHA]
	runner.seen[request.JobSourceTreeSHA] = true
	runner.mu.Unlock()
	if first {
		runner.started <- request
		select {
		case <-ctx.Done():
			return localci.FreshContainerResult{}, ctx.Err()
		case <-runner.release:
		}
	}
	return successfulFreshContainerResult(request), nil
}

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
	_ context.Context,
	request freshContainerRequest,
) (localci.FreshContainerResult, error) {
	return successfulFreshContainerResult(request), nil
}

func successfulFreshContainerResult(request freshContainerRequest) localci.FreshContainerResult {
	startedAt := time.Now().UTC()
	completedAt := startedAt.Add(time.Millisecond)
	logDigest := coordinatorDigest("2")
	container := gatecontract.ContainerEvidence{
		ContainerID: "container-" + string(request.GateID), NetworkID: "network-" + string(request.GateID),
		HostConfigDigest: coordinatorDigest("5"), NetworkPolicyDigest: coordinatorDigest("6"),
		Removed: true, NetworkRemoved: true,
	}
	removalProof := containerRemovalProofDigest(container.ContainerID)
	gateResult := gatecontract.GateResult{
		GateID: string(request.GateID), Status: gatecontract.GateStatusPassed, ExitCode: 0,
		StartedAt: startedAt, CompletedAt: completedAt,
		ArgvDigest: coordinatorDigest("1"), LogDigest: logDigest,
	}
	return localci.FreshContainerResult{
		Status: gatecontract.ResultStatusPassed, ExitCode: 0, GateResult: &gateResult,
		Container: container, StartedAt: startedAt, CompletedAt: completedAt,
		Deadline: startedAt.Add(time.Minute), LogDigest: logDigest, RemovalProofDigest: removalProof,
		Evidence: []gatecontract.Evidence{
			{Kind: gatecontract.EvidenceKindLog, Digest: logDigest},
			{Kind: gatecontract.EvidenceKindDocker, Digest: removalProof},
		},
	}
}

func testAcceptedImageRecord(plan gatecontract.GatePlan) gatecontract.AcceptedImageRecord {
	signer := gatecontract.SignerIdentity{
		KeyID: "coordinator-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}
	return gatecontract.AcceptedImageRecord{
		SchemaVersion: gatecontract.AcceptedImageRecordSchemaVersion,
		RepoID:        "example/repository", TrustedRef: "refs/heads/main",
		TrustedCommit: strings.Repeat("e", 40), SourceTree: strings.Repeat("f", len(plan.Source.SourceTreeSHA)),
		PolicyDigest: plan.PolicyDigest, ImageInputDigest: coordinatorDigest("3"),
		Image: gatecontract.ImageIdentity{
			Registry: "registry.invalid/super-dolphin/gate", OCIIndexDigest: coordinatorDigest("7"),
			PlatformManifestDigest: coordinatorDigest("8"), ConfigDigest: coordinatorDigest("9"),
			RootFSDiffIDs: []string{coordinatorDigest("a")}, OS: "linux", Architecture: "arm64",
		},
		Runner: gatecontract.TrustedRunnerIdentity{
			BinaryDigest: coordinatorDigest("b"), Signer: signer, PolicyDigest: plan.PolicyDigest,
		},
		Generation: 1, AcceptedAt: time.Date(2026, time.July, 17, 0, 0, 0, 0, time.UTC),
		Signer: signer, Signature: "signed-accepted-image-test-record",
	}
}

func mustTestResultReceiptSigner(t *testing.T) resultReceiptSigner {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := newEd25519ResultReceiptSigner(gatecontract.SignerIdentity{
		KeyID: "result-receipt-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func mustTestPassedReceipt(
	t *testing.T,
	record coordinatorJobRecord,
	signer resultReceiptSigner,
) gatecontract.ResultReceipt {
	t.Helper()
	execution := receiptExecution{
		Accepted: testAcceptedImageRecord(record.Plan),
		Deadline: time.Now().UTC().Add(2 * time.Minute),
	}
	for _, gateSpec := range record.Plan.Gates {
		if err := execution.appendResult(successfulFreshContainerResult(freshContainerRequest{
			GateID: gateSpec.ID,
		})); err != nil {
			t.Fatal(err)
		}
	}
	receipt, err := buildPassedResultReceipt(record, execution, signer)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func coordinatorDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
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
		ImageEnsurer: fakeImageEnsurer{}, SourceMaterializer: fakeSourceMaterializer{}, FreshRunner: immediateFreshRunner{},
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

func TestFreshContainerRequestFieldRegistryIsComplete(t *testing.T) {
	producer := reflect.TypeFor[freshContainerRequest]()
	registry := map[string]string{
		"Image": "runner image identity", "ImageTruth": "runner accepted image truth",
		"ImageProvenanceSourceTreeSHA": "accepted image provenance", "JobSourceTreeSHA": "submitted job source",
		"SourceSnapshotDir": "materialized source", "Profile": "timeout contract",
		"Plan": "canonical execution plan", "GateID": "single fresh-container gate",
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

func TestCoordinatorStorePersistsReceiptCrashSafely(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	ctx := context.Background()
	store := mustOpenCoordinatorStore(t, ctx, checkpoint)
	t.Cleanup(func() {
		_ = store.close()
	})

	record, receipt := persistCoordinatorReceipt(t, ctx, store)
	assertCoordinatorReceiptIdempotency(t, ctx, store, record, receipt)
	assertCoordinatorReceiptlessPassRejected(t, ctx, store)

	mustCloseCoordinatorStore(t, store)
	store = mustOpenCoordinatorStore(t, ctx, checkpoint)
	assertCoordinatorReceiptReloaded(t, ctx, store, record, receipt)
	deleteCoordinatorReceiptColumns(t, ctx, store, record.JobID)

	mustCloseCoordinatorStore(t, store)
	store = mustOpenCoordinatorStore(t, ctx, checkpoint)
	if _, err := store.job(ctx, record.JobID); err == nil {
		t.Fatal("restart restored a passed row whose receipt was lost")
	}
}

// mustOpenCoordinatorStore 打开测试 store，失败时立即终止测试。
func mustOpenCoordinatorStore(
	t *testing.T,
	ctx context.Context,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
) *coordinatorStore {
	t.Helper()
	store, err := openCoordinatorStore(ctx, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// mustCloseCoordinatorStore 关闭测试 store，失败时立即终止测试。
func mustCloseCoordinatorStore(t *testing.T, store *coordinatorStore) {
	t.Helper()
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
}

// persistCoordinatorReceipt 创建、启动并持久化一个带权威回执的 passed job。
func persistCoordinatorReceipt(
	t *testing.T,
	ctx context.Context,
	store *coordinatorStore,
) (coordinatorJobRecord, gatecontract.ResultReceipt) {
	t.Helper()
	plan := mustTestGatePlan(t, "d")
	record, err := store.createJob(ctx, "receipt-store-invocation", "receipt-store-job", mustWorkingDirectory(t), plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.startJob(ctx, record.JobID); err != nil {
		t.Fatal(err)
	}
	signer := mustTestResultReceiptSigner(t)
	receipt := mustTestPassedReceipt(t, record, signer)
	if err := store.finishJob(ctx, record.JobID, jobStatePassed, receipt.GateResults, "", &receipt); err != nil {
		t.Fatal(err)
	}
	return record, receipt
}

// assertCoordinatorReceiptIdempotency 校验同 job 仅接受唯一且内容一致的 receipt。
func assertCoordinatorReceiptIdempotency(
	t *testing.T,
	ctx context.Context,
	store *coordinatorStore,
	record coordinatorJobRecord,
	receipt gatecontract.ResultReceipt,
) {
	t.Helper()
	if err := store.finishJob(ctx, record.JobID, jobStatePassed, receipt.GateResults, "", &receipt); err != nil {
		t.Fatalf("idempotent finishJob() error = %v", err)
	}
	different := receipt
	different.Signature = "different-signature"
	if err := store.finishJob(ctx, record.JobID, jobStatePassed, receipt.GateResults, "", &different); err == nil {
		t.Fatal("finishJob() accepted a second receipt for the same job")
	}
}

// assertCoordinatorReceiptlessPassRejected 校验 passed 与 receipt 缺失不会写入 durable store。
func assertCoordinatorReceiptlessPassRejected(
	t *testing.T,
	ctx context.Context,
	store *coordinatorStore,
) {
	t.Helper()
	secondPlan := mustTestGatePlan(t, "b")
	second, err := store.createJob(ctx, "receipt-store-invocation-2", "receipt-store-job-2", mustWorkingDirectory(t), secondPlan)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.startJob(ctx, second.JobID); err != nil {
		t.Fatal(err)
	}
	if err := store.finishJob(ctx, second.JobID, jobStatePassed, nil, "", nil); err == nil {
		t.Fatal("finishJob() persisted passed without a receipt")
	}
	second, err = store.job(ctx, second.JobID)
	if err != nil || second.State != jobStateStarted {
		t.Fatalf("receipt-less pass changed durable state: state=%q error=%v", second.State, err)
	}
}

// assertCoordinatorReceiptReloaded 校验重启后 receipt 与 passed 终态保持一致。
func assertCoordinatorReceiptReloaded(
	t *testing.T,
	ctx context.Context,
	store *coordinatorStore,
	record coordinatorJobRecord,
	receipt gatecontract.ResultReceipt,
) {
	t.Helper()
	reloaded, err := store.job(ctx, record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != jobStatePassed || reloaded.Receipt == nil ||
		reloaded.ReceiptID != receipt.ReceiptID || !receiptsEqual(reloaded.Receipt, &receipt) {
		t.Fatalf("reloaded receipt drifted: %#v", reloaded)
	}
}

// deleteCoordinatorReceiptColumns 模拟 passed 行持久化后 receipt 列丢失。
func deleteCoordinatorReceiptColumns(
	t *testing.T,
	ctx context.Context,
	store *coordinatorStore,
	jobID string,
) {
	t.Helper()
	if _, err := store.db.ExecContext(ctx, `
		UPDATE coordinator_jobs SET receipt_id = NULL, receipt_json = NULL WHERE job_id = ?
	`, jobID); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorReceiptSigningFailurePersistsInfraFailed(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	owner := startTestCoordinatorOwnerWithSigner(
		t, checkpoint, immediateFreshRunner{}, failingResultReceiptSigner{},
	)
	client := dialTestCoordinator(t, checkpoint)
	submitted := submitTestPlan(t, client, "c")
	status, err := client.Wait(context.Background(), submitted.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != jobStateInfraFailed || !status.Terminal || status.ReceiptID != "" {
		t.Fatalf("signing failure status = %#v", status)
	}
	record, err := owner.store.job(context.Background(), submitted.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != jobStateInfraFailed || record.Receipt != nil ||
		!strings.Contains(record.Error, "sign canonical result receipt") {
		t.Fatalf("signing failure durable record = %#v", record)
	}
}

func TestCoordinatorTwoCLIProcessesShareOwnerAndFourthJobQueues(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	runner := &blockingFreshRunner{
		seen: make(map[string]bool), started: make(chan freshContainerRequest, 4), release: make(chan struct{}, 4),
	}
	owner := startTestCoordinatorOwner(t, checkpoint, runner)
	_ = owner

	processStatuses := runTwoCLIProcessSubmits(t, checkpoint)
	client := dialTestCoordinator(t, checkpoint)
	localStatuses := []jobStatus{
		submitTestPlan(t, client, "3"),
		submitTestPlan(t, client, "4"),
	}
	statuses := append(processStatuses, localStatuses...)
	waitForStartedJobs(t, runner.started, 3)

	fourth, err := client.Status(context.Background(), localStatuses[1].JobID)
	if err != nil {
		t.Fatalf("Status(fourth) error = %v", err)
	}
	if fourth.State != jobStateQueued || fourth.Terminal {
		t.Fatalf("fourth status = %#v, want observable queued", fourth)
	}
	assertQueuePosition(t, fourth, 1)

	for range 3 {
		runner.release <- struct{}{}
	}
	waitForStartedJobs(t, runner.started, 1)
	runner.release <- struct{}{}
	for _, submitted := range statuses {
		terminal, waitErr := client.Wait(context.Background(), submitted.JobID)
		if waitErr != nil || terminal.State != jobStatePassed || !terminal.Terminal {
			t.Fatalf("Wait(%s) status=%#v error=%v", submitted.JobID, terminal, waitErr)
		}
		if terminal.ImageProvenanceSourceTreeSHA == terminal.JobSourceTreeSHA {
			t.Fatalf("job %s collapsed image provenance tree into job source tree", submitted.JobID)
		}
	}
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
		RepositoryRoot: mustWorkingDirectory(t),
		InvocationID:   "hook-" + strings.Repeat("1", 64),
		Plan:           mustTestGatePlan(t, "7"),
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
		ImageEnsurer: fakeImageEnsurer{}, SourceMaterializer: fakeSourceMaterializer{}, FreshRunner: runner,
		ReceiptSigner: signer,
	})
	if err != nil {
		t.Fatalf("openCoordinatorOwner() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	group := errgroup.Group{}
	group.Go(func() error { return owner.Serve(ctx) })
	t.Cleanup(func() {
		cancel()
		if err := group.Wait(); err != nil {
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
	status, err := client.Submit(context.Background(), submitRequest{RepositoryRoot: mustWorkingDirectory(t), Plan: plan})
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

func assertQueuePosition(t *testing.T, status jobStatus, want int) {
	t.Helper()
	if status.QueuePosition != want {
		t.Fatalf("queue position = %d, want authoritative FIFO position %d", status.QueuePosition, want)
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
