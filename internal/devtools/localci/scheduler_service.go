package localci

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrSchedulerOwned 表示同一 daemon 与 lock path 已由另一个实例持有。
	ErrSchedulerOwned = errSchedulerOwned
	// ErrSchedulerClosed 表示 scheduler 已关闭或从未成功打开。
	ErrSchedulerClosed = errors.New("scheduler is closed or not open")
	// ErrInvalidSchedulerInput 表示 facade 输入不满足公开契约。
	ErrInvalidSchedulerInput = errors.New("invalid scheduler input")
	// ErrSchedulerState 表示当前 kernel 状态不允许请求的操作。
	ErrSchedulerState = errors.New("invalid scheduler state")
	// ErrSchedulerPersistence 表示 mutation 未能持久化且未提交到内存。
	ErrSchedulerPersistence = errors.New("scheduler persistence failed")
	// ErrWorkloadNotFound 表示公开查询的 workload 不存在。
	ErrWorkloadNotFound = errors.New("scheduler workload not found")
)

// SchedulerConfig 只描述 daemon identity；运行时根目录由进程内固定解析。
type SchedulerConfig struct {
	Endpoint       string
	TLSFingerprint string
	DaemonID       string
	OwnerUID       int
}

// WorkloadKind 是 scheduler 自有的 workload 分类，不复制 gate DTO。
type WorkloadKind string

const (
	WorkloadKindBuild   WorkloadKind = "build"
	WorkloadKindService WorkloadKind = "service"
	WorkloadKindJob     WorkloadKind = "job"
)

// WorkloadStatus 是 scheduler 对外可见的严格状态集合。
type WorkloadStatus string

const (
	WorkloadStatusQueued      WorkloadStatus = "queued"
	WorkloadStatusStarted     WorkloadStatus = "started"
	WorkloadStatusPassed      WorkloadStatus = "passed"
	WorkloadStatusFailed      WorkloadStatus = "failed"
	WorkloadStatusInfraFailed WorkloadStatus = "infra_failed"
)

// WorkloadRequest 是加入 owner-local queue 所需的最小调度输入。
type WorkloadRequest struct {
	ID              string       `json:"id"`
	InvocationID    string       `json:"invocation_id"`
	EnqueueSequence uint64       `json:"enqueue_sequence"`
	Subsequence     uint32       `json:"subsequence"`
	Kind            WorkloadKind `json:"kind"`
	ServiceCount    int          `json:"service_count"`
	Dependencies    []string     `json:"dependencies"`
}

// Lease 描述一次进程内 slot 占用。
type Lease struct {
	ID         string       `json:"id"`
	WorkloadID string       `json:"workload_id"`
	Kind       WorkloadKind `json:"kind"`
}

// WorkloadReservation 描述一个 workload 原子取得的全部 slot。
type WorkloadReservation struct {
	WorkloadID string  `json:"workload_id"`
	Leases     []Lease `json:"leases"`
}

// WorkloadSnapshot 是 workload 输入与当前状态的深拷贝视图。
type WorkloadSnapshot struct {
	Request WorkloadRequest `json:"request"`
	Status  WorkloadStatus  `json:"status"`
}

// SchedulerSnapshot 是 queue、DAG 与 lease 的稳定深拷贝视图。
type SchedulerSnapshot struct {
	Workloads []WorkloadSnapshot `json:"workloads"`
	Leases    []Lease            `json:"leases"`
}

// Scheduler 串行化一个 daemon identity 下的全部 kernel 与持久化操作。
type Scheduler struct {
	mu         sync.Mutex
	kernel     *schedulerKernel
	state      *schedulerState
	lock       *schedulerLock
	saveKernel func(context.Context, *schedulerKernel) error
	closed     bool
	closeErr   error
}

// OpenScheduler 依次规范化 identity、取得单例锁、验证 SQLite 并恢复 kernel。
func OpenScheduler(ctx context.Context, config SchedulerConfig) (*Scheduler, error) {
	identity, err := newDaemonIdentity(config.Endpoint, config.TLSFingerprint, config.DaemonID, config.OwnerUID)
	if err != nil {
		return nil, fmt.Errorf("%w: normalize daemon identity: %w", ErrInvalidSchedulerInput, err)
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidSchedulerInput)
	}
	runtimeRoot, err := defaultSchedulerRuntimeRoot(identity.ownerUID)
	if err != nil {
		return nil, fmt.Errorf("resolve scheduler runtime root: %w", err)
	}
	return openSchedulerAtRuntimeRoot(ctx, identity, runtimeRoot)
}

// openSchedulerWithRuntimeRoot 仅为包内测试提供隔离根目录，生产调用方不可指定路径。
func openSchedulerWithRuntimeRoot(
	ctx context.Context,
	config SchedulerConfig,
	runtimeRoot string,
) (*Scheduler, error) {
	identity, err := newDaemonIdentity(config.Endpoint, config.TLSFingerprint, config.DaemonID, config.OwnerUID)
	if err != nil {
		return nil, fmt.Errorf("%w: normalize daemon identity: %w", ErrInvalidSchedulerInput, err)
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidSchedulerInput)
	}
	return openSchedulerAtRuntimeRoot(ctx, identity, runtimeRoot)
}

func openSchedulerAtRuntimeRoot(
	ctx context.Context,
	identity daemonIdentity,
	runtimeRoot string,
) (*Scheduler, error) {
	lockPath, statePath, err := deriveSchedulerRuntimePaths(runtimeRoot, identity)
	if err != nil {
		return nil, fmt.Errorf("%w: validate scheduler runtime root: %w", ErrInvalidSchedulerInput, err)
	}
	lock, err := acquireSchedulerLock(lockPath, identity)
	if err != nil {
		return nil, fmt.Errorf("acquire scheduler singleton: %w", err)
	}
	state, err := openSchedulerState(ctx, statePath, identity)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open scheduler SQLite: %w", err), closeSchedulerLock(lock))
	}
	kernel, err := state.loadKernel(ctx, identity)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("recover scheduler kernel: %w", err), closeSchedulerResources(state, lock))
	}
	return &Scheduler{
		kernel:     kernel,
		state:      state,
		lock:       lock,
		saveKernel: state.saveKernel,
	}, nil
}

// defaultSchedulerRuntimeRoot 创建 owner-global 固定 cache 根，调用方不能覆盖。
func defaultSchedulerRuntimeRoot(ownerUID int) (string, error) {
	currentUID, err := currentSchedulerOwnerUID()
	if err != nil {
		return "", err
	}
	if ownerUID != currentUID {
		return "", fmt.Errorf("scheduler owner UID %d does not match current UID %d", ownerUID, currentUID)
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	cacheRoot = filepath.Clean(cacheRoot)
	if err := validateExistingPrivateSchedulerDirectory(cacheRoot, ownerUID); err != nil {
		return "", fmt.Errorf("validate user cache directory: %w", err)
	}
	productRoot := filepath.Join(cacheRoot, "super-dolphin")
	if err := ensurePrivateSchedulerDirectory(productRoot, ownerUID); err != nil {
		return "", fmt.Errorf("prepare product cache directory: %w", err)
	}
	runtimeRoot := filepath.Join(productRoot, "localci")
	if err := ensurePrivateSchedulerDirectory(runtimeRoot, ownerUID); err != nil {
		return "", fmt.Errorf("prepare localci runtime directory: %w", err)
	}
	return runtimeRoot, nil
}

// ensurePrivateSchedulerDirectory 只创建并接受 current-UID 私有的真实目录。
func ensurePrivateSchedulerDirectory(directory string, ownerUID int) error {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return errors.New("scheduler directory must be canonical and absolute")
	}
	if err := validateExistingPrivateSchedulerDirectory(filepath.Dir(directory), ownerUID); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create scheduler directory: %w", err)
	}
	return validateExistingPrivateSchedulerDirectory(directory, ownerUID)
}

// validateExistingPrivateSchedulerDirectory 拒绝链接、共享权限和 owner 漂移。
func validateExistingPrivateSchedulerDirectory(directory string, ownerUID int) error {
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("canonicalize scheduler directory: %w", err)
	}
	if canonical != directory {
		return errors.New("scheduler directory path must not contain symlinks")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("lstat scheduler directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("scheduler runtime root must be a real directory")
	}
	return validatePrivateOwnerAndMode(info, ownerUID, true)
}

// deriveSchedulerRuntimePaths 从受信任根目录和 identity key 唯一派生运行时文件。
func deriveSchedulerRuntimePaths(runtimeRoot string, identity daemonIdentity) (string, string, error) {
	if strings.TrimSpace(identity.key) == "" {
		return "", "", errors.New("validated daemon identity is required")
	}
	if runtimeRoot == "" || !filepath.IsAbs(runtimeRoot) || filepath.Clean(runtimeRoot) != runtimeRoot {
		return "", "", errors.New("scheduler runtime root must be canonical and absolute")
	}
	prefix := "localci-scheduler-" + identity.key
	lockPath := filepath.Join(runtimeRoot, prefix+".lock")
	statePath := filepath.Join(runtimeRoot, prefix+".db")
	if err := validatePrivateSchedulerParent(lockPath, identity.ownerUID); err != nil {
		return "", "", err
	}
	return lockPath, statePath, nil
}

// Close 幂等关闭 SQLite 后释放 lock，并在重复调用时返回首次完整结果。
func (s *Scheduler) Close() error {
	if s == nil {
		return ErrSchedulerClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.closeErr
	}
	if s.state == nil || s.lock == nil {
		return ErrSchedulerClosed
	}
	s.closed = true
	s.closeErr = closeSchedulerResources(s.state, s.lock)
	s.kernel = nil
	return s.closeErr
}

// Enqueue 校验输入，在候选 kernel 上修改并 durable save 后才提交内存状态。
func (s *Scheduler) Enqueue(ctx context.Context, request WorkloadRequest) error {
	spec, err := request.toSpec()
	if err != nil {
		return err
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidSchedulerInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return err
	}
	candidate := cloneSchedulerKernel(s.kernel)
	if err := candidate.enqueue(spec); err != nil {
		return fmt.Errorf("%w: enqueue workload: %w", ErrInvalidSchedulerInput, err)
	}
	if err := candidate.validateDAG(); err != nil {
		return fmt.Errorf("%w: validate workload DAG: %w", ErrInvalidSchedulerInput, err)
	}
	return s.commitCandidate(ctx, candidate)
}

// ReserveRunnable 原子持久化所有本轮 runnable reservation。
func (s *Scheduler) ReserveRunnable(ctx context.Context) ([]WorkloadReservation, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidSchedulerInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	candidate := cloneSchedulerKernel(s.kernel)
	reservations, err := candidate.reserveRunnable()
	if err != nil {
		return nil, fmt.Errorf("%w: reserve runnable: %w", ErrSchedulerState, err)
	}
	if len(reservations) == 0 {
		return []WorkloadReservation{}, nil
	}
	exported, err := exportReservations(reservations)
	if err != nil {
		return nil, err
	}
	if err := s.commitCandidate(ctx, candidate); err != nil {
		return nil, err
	}
	return exported, nil
}

// Complete 持久化已启动 workload 的终态并原子释放其全部 lease。
func (s *Scheduler) Complete(ctx context.Context, id string, status WorkloadStatus) error {
	workloadIDValue, err := validatePublicID("workload ID", id)
	if err != nil {
		return err
	}
	terminalState, err := importTerminalStatus(status)
	if err != nil {
		return err
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidSchedulerInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return err
	}
	candidate := cloneSchedulerKernel(s.kernel)
	if err := candidate.complete(workloadID(workloadIDValue), terminalState); err != nil {
		return fmt.Errorf("%w: complete workload: %w", ErrSchedulerState, err)
	}
	return s.commitCandidate(ctx, candidate)
}

// State 返回一个 workload 的当前状态，不暴露 kernel 内部引用。
func (s *Scheduler) State(id string) (WorkloadStatus, error) {
	workloadIDValue, err := validatePublicID("workload ID", id)
	if err != nil {
		return "", err
	}
	if s == nil {
		return "", ErrSchedulerClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return "", err
	}
	state := s.kernel.state(workloadID(workloadIDValue))
	if state == "" {
		return "", fmt.Errorf("%w: %q", ErrWorkloadNotFound, workloadIDValue)
	}
	return exportStatus(state)
}

// Snapshot 返回按调度序稳定排序的 workload 和 lease 深拷贝。
func (s *Scheduler) Snapshot() (SchedulerSnapshot, error) {
	if s == nil {
		return SchedulerSnapshot{}, ErrSchedulerClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return SchedulerSnapshot{}, err
	}
	return exportSnapshot(s.kernel)
}

// ensureOpen 在持锁状态下验证 facade 的全部运行时资源仍可用。
func (s *Scheduler) ensureOpen() error {
	if s == nil || s.closed || s.kernel == nil || s.state == nil || s.lock == nil || s.saveKernel == nil {
		return ErrSchedulerClosed
	}
	return nil
}

func (s *Scheduler) commitCandidate(ctx context.Context, candidate *schedulerKernel) error {
	if err := s.saveKernel(ctx, candidate); err != nil {
		return fmt.Errorf("%w: %w", ErrSchedulerPersistence, err)
	}
	s.kernel = candidate
	return nil
}

func (request WorkloadRequest) toSpec() (workloadSpec, error) {
	id, err := validatePublicID("workload ID", request.ID)
	if err != nil {
		return workloadSpec{}, err
	}
	invocation, err := validatePublicID("invocation ID", request.InvocationID)
	if err != nil {
		return workloadSpec{}, err
	}
	kind, err := importKind(request.Kind)
	if err != nil {
		return workloadSpec{}, err
	}
	dependencies, err := importDependencies(id, request.Dependencies)
	if err != nil {
		return workloadSpec{}, err
	}
	return workloadSpec{
		id:           workloadID(id),
		invocationID: invocationID(invocation),
		enqueueSeq:   request.EnqueueSequence,
		subSeq:       request.Subsequence,
		kind:         kind,
		serviceCount: request.ServiceCount,
		dependencies: dependencies,
	}, nil
}

func validatePublicID(field, value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("%w: %s must be non-empty without surrounding whitespace", ErrInvalidSchedulerInput, field)
	}
	return value, nil
}

func importDependencies(owner string, values []string) ([]workloadID, error) {
	dependencies := make([]workloadID, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		dependency, err := validatePublicID("dependency ID", value)
		if err != nil {
			return nil, err
		}
		if dependency == owner {
			return nil, fmt.Errorf("%w: workload %q depends on itself", ErrInvalidSchedulerInput, owner)
		}
		if _, exists := seen[dependency]; exists {
			return nil, fmt.Errorf("%w: duplicate dependency %q", ErrInvalidSchedulerInput, dependency)
		}
		seen[dependency] = struct{}{}
		dependencies[index] = workloadID(dependency)
	}
	return dependencies, nil
}

func importKind(kind WorkloadKind) (workloadKind, error) {
	switch kind {
	case WorkloadKindBuild:
		return workloadBuild, nil
	case WorkloadKindService:
		return workloadService, nil
	case WorkloadKindJob:
		return workloadJob, nil
	default:
		return 0, fmt.Errorf("%w: unknown workload kind %q", ErrInvalidSchedulerInput, kind)
	}
}

func exportKind(kind workloadKind) (WorkloadKind, error) {
	switch kind {
	case workloadBuild:
		return WorkloadKindBuild, nil
	case workloadService:
		return WorkloadKindService, nil
	case workloadJob:
		return WorkloadKindJob, nil
	default:
		return "", fmt.Errorf("%w: unknown kernel workload kind %d", ErrSchedulerState, kind)
	}
}

func importTerminalStatus(status WorkloadStatus) (workloadState, error) {
	switch status {
	case WorkloadStatusPassed:
		return statePassed, nil
	case WorkloadStatusFailed:
		return stateFailed, nil
	case WorkloadStatusInfraFailed:
		return stateInfraFailed, nil
	default:
		return "", fmt.Errorf("%w: completion status %q is not terminal", ErrInvalidSchedulerInput, status)
	}
}

// exportStatus 严格映射 kernel 状态，未知值视为内存不变量损坏。
func exportStatus(state workloadState) (WorkloadStatus, error) {
	switch state {
	case stateQueued:
		return WorkloadStatusQueued, nil
	case stateStarted:
		return WorkloadStatusStarted, nil
	case statePassed:
		return WorkloadStatusPassed, nil
	case stateFailed:
		return WorkloadStatusFailed, nil
	case stateInfraFailed:
		return WorkloadStatusInfraFailed, nil
	default:
		return "", fmt.Errorf("%w: unknown kernel workload state %q", ErrSchedulerState, state)
	}
}

func cloneSchedulerKernel(source *schedulerKernel) *schedulerKernel {
	clone := &schedulerKernel{
		identity: source.identity,
		nodes:    make(map[workloadID]*workloadNode, len(source.nodes)),
		leases:   make(map[string]slotLease, len(source.leases)),
	}
	for id, node := range source.nodes {
		spec := node.spec
		spec.dependencies = append([]workloadID(nil), node.spec.dependencies...)
		clone.nodes[id] = &workloadNode{spec: spec, state: node.state, gangBypasses: node.gangBypasses}
	}
	maps.Copy(clone.leases, source.leases)
	return clone
}

func exportReservations(values []reservation) ([]WorkloadReservation, error) {
	result := make([]WorkloadReservation, len(values))
	for index, value := range values {
		leases, err := exportLeases(value.leases)
		if err != nil {
			return nil, err
		}
		result[index] = WorkloadReservation{WorkloadID: string(value.workloadID), Leases: leases}
	}
	return result, nil
}

func exportLeases(values []slotLease) ([]Lease, error) {
	result := make([]Lease, len(values))
	for index, value := range values {
		kind, err := exportKind(value.kind)
		if err != nil {
			return nil, err
		}
		result[index] = Lease{ID: value.id, WorkloadID: string(value.workloadID), Kind: kind}
	}
	return result, nil
}

// exportSnapshot 构造不共享底层 slice 或 map 的稳定公开视图。
func exportSnapshot(kernel *schedulerKernel) (SchedulerSnapshot, error) {
	workloads := make([]WorkloadSnapshot, 0, len(kernel.nodes))
	for _, node := range kernel.nodes {
		item, err := exportWorkload(node)
		if err != nil {
			return SchedulerSnapshot{}, err
		}
		workloads = append(workloads, item)
	}
	sort.Slice(workloads, func(i, j int) bool { return workloadSnapshotLess(workloads[i], workloads[j]) })
	internalLeases := make([]slotLease, 0, len(kernel.leases))
	for _, lease := range kernel.leases {
		internalLeases = append(internalLeases, lease)
	}
	sort.Slice(internalLeases, func(i, j int) bool { return internalLeases[i].id < internalLeases[j].id })
	leases := make([]Lease, len(internalLeases))
	for index, lease := range internalLeases {
		kind, err := exportKind(lease.kind)
		if err != nil {
			return SchedulerSnapshot{}, err
		}
		leases[index] = Lease{ID: lease.id, WorkloadID: string(lease.workloadID), Kind: kind}
	}
	return SchedulerSnapshot{Workloads: workloads, Leases: leases}, nil
}

func exportWorkload(node *workloadNode) (WorkloadSnapshot, error) {
	kind, err := exportKind(node.spec.kind)
	if err != nil {
		return WorkloadSnapshot{}, err
	}
	status, err := exportStatus(node.state)
	if err != nil {
		return WorkloadSnapshot{}, err
	}
	dependencies := make([]string, len(node.spec.dependencies))
	for index, dependency := range node.spec.dependencies {
		dependencies[index] = string(dependency)
	}
	return WorkloadSnapshot{Request: WorkloadRequest{
		ID:              string(node.spec.id),
		InvocationID:    string(node.spec.invocationID),
		EnqueueSequence: node.spec.enqueueSeq,
		Subsequence:     node.spec.subSeq,
		Kind:            kind,
		ServiceCount:    node.spec.serviceCount,
		Dependencies:    dependencies,
	}, Status: status}, nil
}

func workloadSnapshotLess(left, right WorkloadSnapshot) bool {
	if left.Request.EnqueueSequence != right.Request.EnqueueSequence {
		return left.Request.EnqueueSequence < right.Request.EnqueueSequence
	}
	if left.Request.Subsequence != right.Request.Subsequence {
		return left.Request.Subsequence < right.Request.Subsequence
	}
	return left.Request.ID < right.Request.ID
}

func closeSchedulerResources(state *schedulerState, lock *schedulerLock) error {
	var stateErr error
	if state != nil {
		stateErr = state.close()
	}
	return errors.Join(wrapCloseError("close scheduler state", stateErr), closeSchedulerLock(lock))
}

func closeSchedulerLock(lock *schedulerLock) error {
	if lock == nil {
		return nil
	}
	return wrapCloseError("close scheduler lock", lock.close())
}

func wrapCloseError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}
