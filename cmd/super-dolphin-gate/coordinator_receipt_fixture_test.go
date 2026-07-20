package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

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
	tampered := cloneResultReceipt(receipt)
	tampered.ShardReceipts[0].ExitedAt = tampered.ShardReceipts[0].ExitedAt.Add(time.Second)
	encoded, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE coordinator_jobs SET receipt_json = ? WHERE job_id = ?", encoded, record.JobID); err != nil {
		t.Fatal(err)
	}
	mustCloseCoordinatorStore(t, store)
	store = mustOpenCoordinatorStore(t, ctx, checkpoint)
	if _, err := store.job(ctx, record.JobID); err == nil {
		t.Fatal("restart accepted a signed receipt whose shard exited_at drifted from SQLite")
	}
	encoded, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE coordinator_jobs SET receipt_json = ? WHERE job_id = ?", encoded, record.JobID); err != nil {
		t.Fatal(err)
	}
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
	record := createStartedCoordinatorReceiptJob(t, ctx, store)
	record = persistCoordinatorReceiptShardEvidence(t, ctx, store, record)
	receipt := buildDurableCoordinatorReceipt(t, record, mustTestResultReceiptSigner(t))
	if err := store.finishJob(ctx, record.JobID, jobStatePassed, receipt.GateResults, "", &receipt); err != nil {
		t.Fatal(err)
	}
	return record, receipt
}

// createStartedCoordinatorReceiptJob 创建已启动且记录镜像溯源的测试 job。
func createStartedCoordinatorReceiptJob(
	t *testing.T,
	ctx context.Context,
	store *coordinatorStore,
) coordinatorJobRecord {
	t.Helper()
	plan := mustTestGatePlan(t, "d")
	record, err := store.createJob(ctx, "receipt-store-invocation", "receipt-store-job", mustWorkingDirectory(t), plan, localci.PromotionCandidatePlan{}, manualSubmissionAuthority())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.startJob(ctx, record.JobID); err != nil {
		t.Fatal(err)
	}
	if err := store.recordImageProvenance(ctx, record.JobID, record.JobSourceTreeSHA); err != nil {
		t.Fatal(err)
	}
	return record
}

// persistCoordinatorReceiptShardEvidence 创建 shard 集并写入完整 durable 生命周期证据。
func persistCoordinatorReceiptShardEvidence(
	t *testing.T,
	ctx context.Context,
	store *coordinatorStore,
	record coordinatorJobRecord,
) coordinatorJobRecord {
	t.Helper()
	accepted := testAcceptedImageRecord(record.Plan)
	set, err := gatecontract.BuildContainerShardSet(record.Plan, accepted.Image.PlatformManifestDigest, accepted.Image.ConfigDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.createContainerShardSet(ctx, record.JobID, set); err != nil {
		t.Fatal(err)
	}
	persistCoordinatorShardLifecycles(t, store, record, set)
	record, err = store.job(ctx, record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// buildDurableCoordinatorReceipt 用 SQLite shard 生命周期对齐、聚合并签发 v2 回执。
func buildDurableCoordinatorReceipt(
	t *testing.T,
	record coordinatorJobRecord,
	signer resultReceiptSigner,
) gatecontract.ResultReceipt {
	t.Helper()
	execution := mustTestCanonicalShardReceiptExecution(t, record)
	for index := range execution.ShardReceipts {
		alignTestShardReceiptTimeline(t, &execution.ShardReceipts[index], record.ContainerShards[index])
	}
	aggregate, err := gatecontract.AggregateContainerShards(*execution.ShardSet, execution.ShardReceipts)
	if err != nil {
		t.Fatal(err)
	}
	execution, err = shardReceiptExecution(execution.Accepted, *execution.ShardSet, execution.ShardReceipts, aggregate)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := buildPassedResultReceipt(record, execution, signer)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
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
	second, err := store.createJob(ctx, "receipt-store-invocation-2", "receipt-store-job-2", mustWorkingDirectory(t), secondPlan, localci.PromotionCandidatePlan{}, manualSubmissionAuthority())
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
