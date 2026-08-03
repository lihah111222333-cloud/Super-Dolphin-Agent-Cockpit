package remoteci

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// PreparedRun 在 job、临时目录、OSS 或 ECI 产生副作用前冻结单个远程 CI 候选的目录与复用决策。
type PreparedRun struct {
	allReused     bool
	mu            sync.Mutex
	consumed      bool
	owner         *Coordinator
	frozenDigest  string
	input         RunInput
	plan          gate.GatePlan
	catalog       gate.WorkloadCatalog
	catalogDigest string
	entrypoint    gate.CIEntrypoint
	reuse         remoteWorkloadReusePreparation
}

// Prepare 一次性构造精确候选的计划、目录、摘要与复用决策。
func (coordinator *Coordinator) Prepare(ctx context.Context, input RunInput) (*PreparedRun, error) {
	if err := validateCoordinatorRunInput(ctx, coordinator.config, input); err != nil {
		return nil, err
	}
	plan, catalog, entrypoint, err := buildRemotePlan(input)
	if err != nil {
		return nil, err
	}
	catalogDigest, err := gate.WorkloadCatalogDigest(catalog)
	if err != nil {
		return nil, err
	}
	reuse, err := prepareRemoteWorkloadReuse(ctx, input, catalog, coordinator.config.WorkerTimeout)
	if err != nil {
		return nil, err
	}
	prepared := &PreparedRun{
		allReused:     reuse.allReused(),
		owner:         coordinator,
		input:         input,
		plan:          plan,
		catalog:       catalog,
		catalogDigest: catalogDigest,
		entrypoint:    entrypoint,
		reuse:         reuse,
	}
	frozenDigest, err := prepared.frozenIdentityDigest()
	if err != nil {
		return nil, err
	}
	prepared.frozenDigest = frozenDigest
	return prepared, nil
}

// AllReused 返回 Prepare 已冻结的不可变复用决策。
func (prepared *PreparedRun) AllReused() bool {
	if prepared == nil {
		return false
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	return prepared.allReused
}

// RefreshPlanningSnapshot 仅在同步校准后，按冻结的 SQLite 权威和计划上下文重新加载耗时计划快照。
func (prepared *PreparedRun) RefreshPlanningSnapshot(store *gate.DurationLedgerStore) error {
	if prepared == nil {
		return errors.New("prepared remote CI run is required")
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.consumed {
		return errors.New("prepared remote CI run is already consumed")
	}
	if err := prepared.validateFrozenLocked(); err != nil {
		return err
	}
	if err := prepared.validateLocked(); err != nil {
		return err
	}
	return prepared.reloadPlanningSnapshotLocked(store)
}

// reloadPlanningSnapshotLocked 在已冻结且未消费的互斥边界内重载并验证 SQLite 计划快照。
func (prepared *PreparedRun) reloadPlanningSnapshotLocked(store *gate.DurationLedgerStore) error {
	if store == nil {
		return errors.New("remote CI planning duration ledger SQLite authority is required")
	}
	if prepared.input.LedgerStore == nil || store.AuthorityPath() != prepared.input.LedgerStore.AuthorityPath() {
		return errors.New("remote CI planning duration ledger authority drifted")
	}
	context := remotePlanningContext(prepared.input)
	current, err := store.LoadPlanning(context)
	if err != nil {
		return fmt.Errorf("load remote CI planning snapshot from authority: %w", err)
	}
	if current.Generation == 0 {
		return errors.New("remote CI planning duration ledger generation is required")
	}
	if err := gate.ValidateDurationLedger(current.Ledger); err != nil {
		return fmt.Errorf("validate remote CI planning duration ledger: %w", err)
	}
	if _, err := gate.DurationSampleIndexFromSnapshot(current, context); err != nil {
		return fmt.Errorf("validate remote CI planning snapshot context: %w", err)
	}
	prepared.input.LedgerSnapshot = current
	prepared.input.LedgerStore = store
	return nil
}

// RunPrepared 创建独立 job，并消费一个先前冻结的计划。
func (coordinator *Coordinator) RunPrepared(ctx context.Context, prepared *PreparedRun) (result RunResult, returnErr error) {
	if err := prepared.consume(coordinator); err != nil {
		return result, err
	}
	if err := validateCoordinatorRunInput(ctx, coordinator.config, prepared.input); err != nil {
		return result, err
	}
	jobID, err := coordinator.newID()
	if err != nil {
		return result, fmt.Errorf("create remote CI job identity: %w", err)
	}
	result = coordinator.newRunResult(prepared.plan, prepared.catalogDigest, prepared.entrypoint, prepared.input, jobID)
	defer func() {
		result.CompletedAt = coordinator.now().UTC()
		persistErr := recordRemoteCIRun(prepared.input.LedgerStore, result, returnErr)
		returnErr = errors.Join(returnErr, persistErr)
	}()
	if err := prepared.input.LedgerStore.RecordWorkloadCatalog(prepared.catalog, gate.WorkloadCatalogObservation{
		AcceptedGeneration: prepared.input.AcceptedGeneration,
		SourceTreeSHA:      prepared.input.Tree,
		Entrypoint:         prepared.entrypoint.ID,
		Profile:            prepared.plan.Profile,
		ObservedAt:         result.StartedAt,
	}); err != nil {
		return result, err
	}
	prepared.reuse.apply(&result)
	if prepared.allReused {
		return completeRemoteReuse(prepared.catalog, prepared.reuse.reused, result)
	}
	tempRoot, err := createRemoteTempRoot()
	if err != nil {
		return result, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, os.RemoveAll(tempRoot))
	}()
	objectKeys := make([]string, 0)
	createdGroups := make([]string, 0)
	defer func() {
		cleanupErr := coordinator.cleanup(jobID, createdGroups, objectKeys)
		result.CleanupComplete = cleanupErr == nil
		returnErr = errors.Join(returnErr, cleanupErr)
	}()
	return coordinator.runRemoteWorkloadMisses(
		ctx, prepared.input, prepared.plan, prepared.catalog, prepared.reuse.cacheMisses,
		prepared.reuse.reused, jobID, tempRoot, &objectKeys, &createdGroups, result,
	)
}

// consume 在同一互斥边界校验所有权与冻结身份，并保证每个准备结果只能被执行一次。
func (prepared *PreparedRun) consume(coordinator *Coordinator) error {
	if prepared == nil {
		return errors.New("prepared remote CI run is required")
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.consumed {
		return errors.New("prepared remote CI run is already consumed")
	}
	if prepared.owner != coordinator {
		return errors.New("prepared remote CI run belongs to a different coordinator")
	}
	if err := prepared.validateFrozenLocked(); err != nil {
		return err
	}
	if err := prepared.validateLocked(); err != nil {
		return err
	}
	prepared.consumed = true
	return nil
}

func (prepared *PreparedRun) validateFrozenLocked() error {
	if prepared.owner == nil || prepared.frozenDigest == "" {
		return errors.New("prepared remote CI run frozen identity is required")
	}
	digest, err := prepared.frozenIdentityDigest()
	if err != nil {
		return err
	}
	if digest != prepared.frozenDigest {
		return errors.New("prepared remote CI run identity drifted")
	}
	return nil
}

func (prepared *PreparedRun) frozenIdentityDigest() (string, error) {
	input := prepared.input
	input.LedgerSnapshot = gate.DurationLedgerSnapshot{}
	input.LedgerStore = nil
	payload, err := json.Marshal(struct {
		Input              RunInput                    `json:"input"`
		Plan               gate.GatePlan               `json:"plan"`
		Catalog            gate.WorkloadCatalog        `json:"catalog"`
		CatalogDigest      string                      `json:"catalog_digest"`
		Entrypoint         gate.CIEntrypoint           `json:"entrypoint"`
		ReuseIdentities    []gate.WorkloadPassIdentity `json:"reuse_identities"`
		ReusedWorkloads    []gate.WorkloadPassEvidence `json:"reused_workloads"`
		CacheMissWorkloads []gate.GateID               `json:"cache_miss_workloads"`
		ReuseEnabled       bool                        `json:"reuse_enabled"`
	}{input, prepared.plan, prepared.catalog, prepared.catalogDigest, prepared.entrypoint, prepared.reuse.identities, prepared.reuse.reusedWorkloads, prepared.reuse.cacheMisses, prepared.reuse.enabled})
	if err != nil {
		return "", fmt.Errorf("encode prepared remote CI identity: %w", err)
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// validateLocked 校验冻结计划、目录摘要和复用决策仍与准备时的候选上下文一致。
func (prepared *PreparedRun) validateLocked() error {
	if err := prepared.plan.Validate(); err != nil {
		return fmt.Errorf("validate prepared remote CI plan: %w", err)
	}
	catalogDigest, err := gate.WorkloadCatalogDigest(prepared.catalog)
	if err != nil {
		return err
	}
	if prepared.catalogDigest != catalogDigest {
		return errors.New("prepared remote CI catalog digest drifted")
	}
	if prepared.entrypoint.ID == "" || prepared.plan.Profile != prepared.input.Profile {
		return errors.New("prepared remote CI plan context drifted")
	}
	if prepared.allReused != prepared.reuse.allReused() {
		return errors.New("prepared remote CI reuse decision drifted")
	}
	return nil
}
