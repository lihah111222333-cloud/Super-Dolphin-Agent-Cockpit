package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// newProductionLocalTestCLIAdapter 连接生产 local plan 和远程 subset owner。
func newProductionLocalTestCLIAdapter() localTestCLIAdapter {
	return localTestCLIAdapter{Prepare: prepareProductionLocalTestCLIPlan, RequireRemoteToken: requireTestRemoteSubsetToken, ExecuteRemoteSubset: executeProductionRemoteWorkloadSubset}
}

type productionLocalPlanInputs struct {
	repository                              string
	tree                                    string
	scheduleTarget                          gatecontract.LocalWorkloadScheduleTarget
	store                                   *gatecontract.DurationLedgerStore
	selected, receiptBoundIDs, executionIDs []gatecontract.GateID
	dependencies                            gatecontract.LocalExecutorDependencyInputs
	receipt                                 gatecontract.LocalExecutorSessionReceipt
}

// prepareProductionLocalTestCLIPlan 仅冻结 identity；采样、物化和 session 均留给 local MISS 回调。
func prepareProductionLocalTestCLIPlan(ctx context.Context, options remoteRunOptions) (localTestCLIPlan, error) {
	if ctx == nil {
		return localTestCLIPlan{}, errors.New("production local test plan context is required")
	}
	inputs, err := loadProductionLocalPlanInputs(ctx, options)
	if err != nil {
		return localTestCLIPlan{}, err
	}
	return buildProductionLocalTestCLIPlan(ctx, inputs)
}

// loadProductionLocalPlanInputs 只读取 local authority 所需的 exact source、
// clean-initialized SQLite、sealed receipt 与 local generation inputs。
func loadProductionLocalPlanInputs(ctx context.Context, options remoteRunOptions) (productionLocalPlanInputs, error) {
	selected, err := localGateWorkloadIDs(options.GateWorkloadIDs)
	if err != nil {
		return productionLocalPlanInputs{}, err
	}
	if err := validateWorkloadAuthorityTarget(options.Target); err != nil {
		return productionLocalPlanInputs{}, fmt.Errorf("validate local workload target: %w", err)
	}
	repository, _, _, target, _, err := resolveRemoteRunSource(options)
	if err != nil {
		return productionLocalPlanInputs{}, fmt.Errorf("resolve local test source: %w", err)
	}
	store, err := gatecontract.NewDurationLedgerStore(options.LedgerPath)
	if err != nil {
		return productionLocalPlanInputs{}, fmt.Errorf("open local workload authority: %w", err)
	}
	if _, err := store.InitializeLocalAuthority(); err != nil {
		return productionLocalPlanInputs{}, fmt.Errorf("initialize local workload authority: %w", err)
	}
	deps, receipt, receiptBoundIDs, executionIDs, err := prepareProductionLocalExecutorReceipt(ctx, repository, target.tree, target.candidateObjectAuthority, selected)
	if err != nil {
		return productionLocalPlanInputs{}, err
	}
	return productionLocalPlanInputs{repository: repository, tree: target.tree, scheduleTarget: gatecontract.LocalWorkloadScheduleTarget(options.Target), store: store, selected: selected, receiptBoundIDs: receiptBoundIDs, executionIDs: executionIDs, dependencies: deps, receipt: receipt}, nil
}

// prepareProductionLocalExecutorReceipt 为所有 canonical executor-mapped ID 封存真实
// PASS 环境，同时只为 local-executable IDs 发现依赖并创建 session。
func prepareProductionLocalExecutorReceipt(ctx context.Context, repository, tree string, authority gatecontract.CandidateObjectAuthority, selected []gatecontract.GateID) (gatecontract.LocalExecutorDependencyInputs, gatecontract.LocalExecutorSessionReceipt, []gatecontract.GateID, []gatecontract.GateID, error) {
	receiptBoundIDs, err := gatecontract.LocalExecutorReceiptBoundWorkloadIDs(selected)
	if err != nil {
		return gatecontract.LocalExecutorDependencyInputs{}, nil, nil, nil, fmt.Errorf("select local executor receipt-bound workloads: %w", err)
	}
	if len(receiptBoundIDs) == 0 {
		return gatecontract.LocalExecutorDependencyInputs{}, nil, receiptBoundIDs, nil, nil
	}
	executionIDs, err := gatecontract.LocalExecutorExecutionWorkloadIDs(receiptBoundIDs)
	if err != nil {
		return gatecontract.LocalExecutorDependencyInputs{}, nil, nil, nil, fmt.Errorf("select local executor execution workloads: %w", err)
	}
	deps := gatecontract.LocalExecutorDependencyInputs{}
	if len(executionIDs) != 0 {
		deps, err = gatecontract.DiscoverLocalExecutorDependencyInputs(ctx, executionIDs)
		if err != nil {
			return gatecontract.LocalExecutorDependencyInputs{}, nil, nil, nil, fmt.Errorf("discover local executor execution dependencies: %w", err)
		}
	}
	receipt, err := gatecontract.NewLocalExecutorSessionReceiptWithCandidateObjectAuthority(ctx, repository, tree, authority, receiptBoundIDs, deps)
	if err != nil {
		return gatecontract.LocalExecutorDependencyInputs{}, nil, nil, nil, fmt.Errorf("seal local executor receipt: %w", err)
	}
	if err := gatecontract.ValidateLocalExecutorSessionReceipt(receipt); err != nil {
		return gatecontract.LocalExecutorDependencyInputs{}, nil, nil, nil, fmt.Errorf("validate sealed local executor receipt: %w", err)
	}
	return deps, receipt, receiptBoundIDs, executionIDs, nil
}

// buildProductionLocalTestCLIPlan 组装 scheduler 输入并读取 local authority generation。
func buildProductionLocalTestCLIPlan(ctx context.Context, inputs productionLocalPlanInputs) (localTestCLIPlan, error) {
	plan, err := remoteci.BuildLocalWorkloadPlan(ctx, remoteci.LocalWorkloadPlanInput{RepositoryRoot: inputs.repository, ExactTreeSHA: inputs.tree, SelectedGateIDs: inputs.selected, Receipt: inputs.receipt})
	if err != nil {
		return localTestCLIPlan{}, fmt.Errorf("build canonical local workload plan: %w", err)
	}
	generation, err := inputs.store.LoadLocalAuthorityGeneration()
	if err != nil {
		return localTestCLIPlan{}, fmt.Errorf("load local authority generation: %w", err)
	}
	origin, err := productionLocalWorkloadOrigin(plan, inputs.receipt)
	if err != nil {
		return localTestCLIPlan{}, err
	}
	input := gatecontract.LocalWorkloadSchedulerInput{Target: inputs.scheduleTarget, Items: plan.Items, SourceTreeSHA: inputs.tree, LocalGeneration: generation, Origin: origin, RunID: fmt.Sprintf("local-workload-%d", time.Now().UTC().UnixNano()), Now: time.Now, SampleHost: newProductionLocalHostAdmissionSampler(), Receipt: inputs.receipt}
	bindProductionLocalExecutor(&input, inputs.repository, inputs.executionIDs, inputs.dependencies, inputs.receipt)
	return localTestCLIPlan{Store: inputs.store, Input: input, Catalog: plan.Catalog}, nil
}

// productionLocalWorkloadOrigin 从 sealed receipt host context 和 catalog digest 构造本地 provenance。
func productionLocalWorkloadOrigin(plan remoteci.LocalWorkloadPlan, receipt gatecontract.LocalExecutorSessionReceipt) (gatecontract.LocalWorkloadPassOrigin, error) {
	if receipt == nil {
		return gatecontract.LocalWorkloadPassOrigin{CatalogDigest: plan.CatalogDigest}, nil
	}
	host, err := receipt.HostContext()
	if err != nil {
		return gatecontract.LocalWorkloadPassOrigin{}, fmt.Errorf("read local receipt host context: %w", err)
	}
	digest, err := receipt.HostContextDigest()
	if err != nil {
		return gatecontract.LocalWorkloadPassOrigin{}, fmt.Errorf("digest local receipt host context: %w", err)
	}
	return gatecontract.LocalWorkloadPassOrigin{CatalogDigest: plan.CatalogDigest, HostContextDigest: digest, ToolchainClosureDigest: host.ToolchainClosureDigest, RunnerSemanticPolicy: host.RunnerSemanticPolicy, RunnerSemanticDigest: host.RunnerSemanticDigest}, nil
}

// bindProductionLocalExecutor 建立仅在 materialize callback 中创建的一次 batch session。
func bindProductionLocalExecutor(input *gatecontract.LocalWorkloadSchedulerInput, repository string, receiptIDs []gatecontract.GateID, dependencies gatecontract.LocalExecutorDependencyInputs, receipt gatecontract.LocalExecutorSessionReceipt) {
	if input == nil || len(receiptIDs) == 0 {
		return
	}
	var session *gatecontract.LocalExecutorSession
	input.Materialize = func(ctx context.Context, tree string) (gatecontract.LocalMaterializedTree, error) {
		trustedGit, err := receipt.TrustedGitBinary()
		if err != nil {
			return gatecontract.LocalMaterializedTree{}, err
		}
		exact, err := materializeLocalExactTree(repository, tree, trustedGit)
		if err != nil {
			return gatecontract.LocalMaterializedTree{}, err
		}
		session, err = gatecontract.NewLocalExecutorSessionWithReceipt(exact.SourceRoot, productionLocalExecutorNow, receiptIDs, dependencies, receipt)
		if err != nil {
			return gatecontract.LocalMaterializedTree{}, errors.Join(err, exact.Cleanup())
		}
		return gatecontract.LocalMaterializedTree{Root: exact.SourceRoot, SourceTreeSHA: exact.SourceTreeSHA, Restore: exact.Restore, Verify: exact.Verify, ExecutorCleanup: session.Close, Cleanup: exact.Cleanup}, nil
	}
	input.Execute = func(ctx context.Context, _ string, id gatecontract.GateID) (gatecontract.PlanGateExecution, error) {
		if session == nil {
			return gatecontract.PlanGateExecution{}, errors.New("local executor session was not materialized")
		}
		return session.Execute(ctx, id)
	}
}

// productionLocalExecutorNow is the command boundary's sole wall-clock owner.
// The gate executor receives it explicitly and tests replace it with a fake.
func productionLocalExecutorNow() time.Time {
	return time.Now()
}
