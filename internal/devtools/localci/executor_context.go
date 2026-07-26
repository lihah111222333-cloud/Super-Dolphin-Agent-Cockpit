package localci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

const (
	maxReadOnlyGitTreeEntries = 100_000
	maxReadOnlyGitTreeBytes   = 512 << 20
	terminalLifecycleAttempts = 3
	terminalLifecycleRetry    = 10 * time.Millisecond
)

// BoundedCleanupContext 从已取消的执行上下文派生仍受限时长的 CI 清理上下文。
func BoundedCleanupContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return platformconfig.WithTimeout(context.WithoutCancel(parent), timeout)
}

// BoundedOperationContext 为调用方保留取消链并附加固定操作上限。
func BoundedOperationContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return platformconfig.WithTimeout(parent, timeout)
}

// statusForContext 将执行上下文终态映射为公开容器结果状态。
func statusForContext(err error) gate.ResultStatus {
	if errors.Is(err, context.Canceled) {
		return gate.ResultStatusCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return gate.ResultStatusTimeout
	}
	return gate.ResultStatusInfraFailed
}

// runCleanup 以独立有界上下文执行 Docker 证据收尾动作。
func (runner *FreshContainerRunner) runCleanup(parentContext context.Context, args ...string) (string, error) {
	cleanupContext, cancel := BoundedCleanupContext(parentContext, 30*time.Second)
	defer cancel()
	return runner.docker.runner.Run(cleanupContext, args...)
}

// emitCleanupLifecycle 为每个终态持久化动作在执行完成后创建独立且不可继承取消的时限。
func (runner *FreshContainerRunner) emitCleanupLifecycle(
	parent context.Context,
	request FreshContainerRequest,
	result FreshContainerResult,
	phase FreshContainerLifecyclePhase,
) error {
	ctx, cancel := BoundedCleanupContext(parent, runner.lifecycleCleanupTimeout)
	defer cancel()
	var lastErr error
	for attempt := range terminalLifecycleAttempts {
		if err := runner.emitLifecycle(ctx, request, result, phase); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt+1 == terminalLifecycleAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return errors.Join(lastErr, ctx.Err())
		case <-time.After(terminalLifecycleRetry):
		}
	}
	return fmt.Errorf("persist terminal container lifecycle after %d attempts: %w", terminalLifecycleAttempts, lastErr)
}

// validateFreshContainerLifecycleEvent 约束非终态、可信退出与无观测清理的时钟边界。
func validateFreshContainerLifecycleEvent(event FreshContainerLifecycleEvent, status gate.ResultStatus) error {
	switch event.Phase {
	case FreshContainerPhasePrepared, FreshContainerPhaseCreated,
		FreshContainerPhaseStarting, FreshContainerPhaseStarted:
		if !event.ExitedAt.IsZero() {
			return errors.New("non-terminal container lifecycle exited_at must be zero")
		}
	case FreshContainerPhaseCreating:
		if !IsFreshContainerOperationIdentity(event.ContainerID) {
			return errors.New("creating lifecycle requires deterministic operation identity")
		}
		if !event.ExitedAt.IsZero() {
			return errors.New("non-terminal container lifecycle exited_at must be zero")
		}
	case FreshContainerPhaseExited:
		return validateObservedLifecycleExit(event, status)
	case FreshContainerPhaseRemovalPending:
		return validateRemovalPendingLifecycle(event, status)
	case FreshContainerPhaseRemoved:
		return validateRemovedLifecycleExit(event, status)
	default:
		return fmt.Errorf("unsupported container lifecycle phase %q", event.Phase)
	}
	return nil
}

// validateRemovalPendingLifecycle records only an intent; it never accepts a removal proof before Docker confirms absence.
func validateRemovalPendingLifecycle(event FreshContainerLifecycleEvent, status gate.ResultStatus) error {
	if !event.ExitedAt.IsZero() {
		return validateObservedLifecycleExit(event, status)
	}
	if status != gate.ResultStatusInfraFailed {
		return errors.New("pending removal lifecycle is missing trusted exited_at")
	}
	if strings.TrimSpace(event.ContainerID) == "" {
		return errors.New("pending removal lifecycle requires container identity")
	}
	if event.RemovalProofDigest != "" {
		return errors.New("pending removal lifecycle must not carry a removal proof")
	}
	return nil
}

// validateObservedLifecycleExit 校验 Docker 终态 inspect 提供的退出时钟。
func validateObservedLifecycleExit(event FreshContainerLifecycleEvent, status gate.ResultStatus) error {
	if event.ExitedAt.IsZero() || event.CompletedAt.Before(event.ExitedAt) {
		return errors.New("terminal container lifecycle timing is invalid")
	}
	if status == gate.ResultStatusTimeout && (event.Deadline.IsZero() || event.ExitedAt.Before(event.Deadline)) {
		return errors.New("timeout container lifecycle exited before deadline")
	}
	return nil
}

// validateRemovedLifecycleExit 仅允许未观察到进程终态的 pre-start 或 unproved 清理省略退出时刻。
func validateRemovedLifecycleExit(event FreshContainerLifecycleEvent, status gate.ResultStatus) error {
	if !event.ExitedAt.IsZero() {
		return validateObservedLifecycleExit(event, status)
	}
	if status != gate.ResultStatusInfraFailed || event.ExitCode != -1 {
		return errors.New("removed container lifecycle is missing trusted exited_at")
	}
	if err := validateDigest("removal proof digest", event.RemovalProofDigest); err != nil {
		return fmt.Errorf("removed container lifecycle without exited_at: %w", err)
	}
	return nil
}

// ResolveGateImageInputs 校验传入的 Git tree，并推导不依赖活动工作区的规范构建闭包。
func ResolveGateImageInputs(tree ReadOnlyGitTree, policyDigest string, platform string) (GateImageInputs, error) {
	if err := verifyReadOnlyGitTree(tree); err != nil {
		return GateImageInputs{}, err
	}
	request := CandidateRequest{
		SourceTreeSHA: tree.Source.SourceTreeSHA, PolicyDigest: policyDigest,
		ImageSchemaVersion: imageInputSchemaVersion, SourceEntries: cloneTreeEntries(tree.Entries), Platform: platform,
	}
	prepared, err := prepareCandidate(request)
	if err != nil {
		return GateImageInputs{}, fmt.Errorf("resolve gate image input closure: %w", err)
	}
	result := prepared.result
	return GateImageInputs{
		SubmittedSourceTree: result.SourceTreeSHA, PolicyDigest: policyDigest,
		ImageSchemaVersion: imageInputSchemaVersion, Platform: platform, SourceEntries: cloneTreeEntries(prepared.sourceEntries),
		ImageInputDigest: result.InputDigest, ContextDigest: result.ContextDigest,
		InputManifestDigest: result.InputManifestDigest, ToolchainDigest: result.ToolchainDigest,
		DockerfileDigest: result.DockerfileDigest,
	}, nil
}

// LoadReadOnlyGitTree 从已验证 SourceSpec 的 Git object tree 读取镜像输入，不读取工作区文件。
func LoadReadOnlyGitTree(ctx context.Context, repoRoot string, spec gate.SourceSpec) (ReadOnlyGitTree, error) {
	if err := errors.Join(validateContext(ctx), spec.Validate(), validateCanonicalDirectory(repoRoot, false)); err != nil {
		return ReadOnlyGitTree{}, fmt.Errorf("validate read-only Git tree input: %w", err)
	}
	if err := verifyRepositoryIdentity(ctx, repoRoot, spec.ObjectFormat); err != nil {
		return ReadOnlyGitTree{}, err
	}
	plan, err := inspectSourcePlan(ctx, repoRoot, spec)
	if err != nil {
		return ReadOnlyGitTree{}, err
	}
	entries, err := loadReadOnlyTreeEntries(ctx, repoRoot, plan.tree)
	if err != nil {
		return ReadOnlyGitTree{}, err
	}
	tree := ReadOnlyGitTree{Source: cloneSourceSpec(spec), Entries: entries}
	if err := verifyReadOnlyGitTree(tree); err != nil {
		return ReadOnlyGitTree{}, fmt.Errorf("verify read-only Git tree: %w", err)
	}
	return tree, nil
}

// LoadReadOnlyBootstrapTree 从已验证的 bare authority 读取首次自举镜像输入。
func LoadReadOnlyBootstrapTree(
	ctx context.Context,
	repository string,
	spec gate.SourceSpec,
) (ReadOnlyGitTree, error) {
	if err := errors.Join(validateContext(ctx), spec.Validate(), validateCanonicalDirectory(repository, false)); err != nil {
		return ReadOnlyGitTree{}, fmt.Errorf("validate bootstrap Git tree input: %w", err)
	}
	if err := verifyBootstrapBareRepository(ctx, repository, spec.ObjectFormat); err != nil {
		return ReadOnlyGitTree{}, err
	}
	plan, err := inspectSourcePlan(ctx, repository, spec)
	if err != nil {
		return ReadOnlyGitTree{}, err
	}
	entries, err := loadReadOnlyTreeEntries(ctx, repository, plan.tree)
	if err != nil {
		return ReadOnlyGitTree{}, err
	}
	tree := ReadOnlyGitTree{Source: cloneSourceSpec(spec), Entries: entries}
	if err := verifyReadOnlyGitTree(tree); err != nil {
		return ReadOnlyGitTree{}, fmt.Errorf("verify bootstrap Git tree: %w", err)
	}
	return tree, nil
}

// verifyBootstrapBareRepository 固定 bare Git 目录与 object format，拒绝工作树输入。
func verifyBootstrapBareRepository(
	ctx context.Context,
	repository string,
	objectFormat gate.GitObjectFormat,
) error {
	bareOutput, err := runGitOutput(ctx, repository, nil, "rev-parse", "--is-bare-repository")
	if err != nil {
		return fmt.Errorf("inspect bootstrap bare repository: %w", err)
	}
	bare, err := strictGitLine(bareOutput)
	if err != nil || bare != "true" {
		return errors.Join(errors.New("bootstrap repository must be bare"), err)
	}
	gitDirOutput, err := runGitOutput(ctx, repository, nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return fmt.Errorf("resolve bootstrap Git directory: %w", err)
	}
	gitDir, err := strictGitLine(gitDirOutput)
	if err != nil || gitDir != repository {
		return errors.Join(errors.New("bootstrap repository Git directory drifted"), err)
	}
	return verifyBootstrapObjectFormat(ctx, repository, objectFormat)
}

func verifyBootstrapObjectFormat(
	ctx context.Context,
	repository string,
	objectFormat gate.GitObjectFormat,
) error {
	output, err := runGitOutput(ctx, repository, nil, "rev-parse", "--show-object-format")
	if err != nil {
		return fmt.Errorf("read bootstrap repository object format: %w", err)
	}
	actual, err := strictGitLine(output)
	if err != nil {
		return fmt.Errorf("parse bootstrap repository object format: %w", err)
	}
	if actual != string(objectFormat) {
		return fmt.Errorf("bootstrap repository object format is %q, want %q", actual, objectFormat)
	}
	return nil
}

// loadReadOnlyTreeEntries 读取稳定排序的 blob 记录并从 Git object database 取得内容。
func loadReadOnlyTreeEntries(ctx context.Context, repoRoot string, treeOID string) ([]sourceexport.TreeEntry, error) {
	output, err := runGitOutput(ctx, repoRoot, nil, "ls-tree", "-rz", "--full-tree", treeOID)
	if err != nil {
		return nil, fmt.Errorf("list read-only Git tree: %w", err)
	}
	records := bytes.Split(output, []byte{0})
	entries := make([]sourceexport.TreeEntry, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		entry, parseErr := parseReadOnlyTreeEntry(record)
		if parseErr != nil {
			return nil, parseErr
		}
		entries = append(entries, entry)
	}
	return loadReadOnlyTreeBlobs(ctx, repoRoot, entries)
}

// loadReadOnlyTreeBlobs 以单个 cat-file batch 进程读取去重 blob，并严格验证顺序和总字节数。
func loadReadOnlyTreeBlobs(ctx context.Context, repoRoot string, entries []sourceexport.TreeEntry) ([]sourceexport.TreeEntry, error) {
	if len(entries) > maxReadOnlyGitTreeEntries {
		return nil, fmt.Errorf("read-only Git tree entry count %d exceeds limit %d", len(entries), maxReadOnlyGitTreeEntries)
	}
	if len(entries) == 0 {
		return entries, nil
	}
	unique := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if _, exists := seen[entry.Hash]; exists {
			continue
		}
		seen[entry.Hash] = struct{}{}
		unique = append(unique, entry.Hash)
	}
	output, err := runGitOutput(ctx, repoRoot, strings.NewReader(strings.Join(unique, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, fmt.Errorf("read 0/%d read-only Git tree blobs: %w", len(unique), err)
	}
	blobs, err := parseReadOnlyTreeBlobBatch(output, unique)
	if err != nil {
		return nil, err
	}
	for index := range entries {
		entries[index].Data = blobs[entries[index].Hash]
	}
	return entries, nil
}

// parseReadOnlyTreeBlobBatch 严格消费所有预期对象，拒绝类型、大小、顺序或尾随输出漂移。
func parseReadOnlyTreeBlobBatch(output []byte, expected []string) (map[string][]byte, error) {
	blobs := make(map[string][]byte, len(expected))
	offset := 0
	total := 0
	for index, oid := range expected {
		object, consumed, err := parseSourceObjectPrefix(output[offset:], oid)
		if err != nil {
			return nil, fmt.Errorf("read %d/%d read-only Git tree blobs: %w", index, len(expected), err)
		}
		if object.kind != "blob" {
			return nil, fmt.Errorf("read-only Git tree object %s is %q, want blob", oid, object.kind)
		}
		total += len(object.data)
		if total > maxReadOnlyGitTreeBytes {
			return nil, fmt.Errorf("read-only Git tree bytes %d exceed limit %d", total, maxReadOnlyGitTreeBytes)
		}
		blobs[oid] = object.data
		offset += consumed
	}
	if offset != len(output) {
		return nil, errors.New("read-only Git tree cat-file returned trailing output")
	}
	return blobs, nil
}

func parseReadOnlyTreeEntry(record []byte) (sourceexport.TreeEntry, error) {
	metadata, path, found := bytes.Cut(record, []byte{'\t'})
	if !found || len(path) == 0 {
		return sourceexport.TreeEntry{}, errors.New("read-only Git tree record is missing its path")
	}
	fields := bytes.Fields(metadata)
	if len(fields) != 3 || string(fields[1]) != "blob" {
		return sourceexport.TreeEntry{}, fmt.Errorf("read-only Git tree entry %q is not a blob", path)
	}
	return sourceexport.TreeEntry{
		Path: string(path), Mode: string(fields[0]), Hash: string(fields[2]),
	}, nil
}

// CleanupUnprovedFreshContainer 对无法接管的旧容器执行 kill、wait、remove。
func (runner *FreshContainerRunner) CleanupUnprovedFreshContainer(
	ctx context.Context,
	request FreshContainerCleanupRequest,
) (FreshContainerResult, error) {
	recovery := FreshContainerRecoveryRequest{
		ContainerID: request.ContainerID, ContainerLabels: request.ContainerLabels,
		ImageReference: request.ImageReference, ConfigDigest: request.ConfigDigest,
		SourceSnapshotDir: request.SourceSnapshotDir, Command: request.Command,
		Profile: request.Profile, GateID: request.GateID, LifecycleHook: request.LifecycleHook,
	}
	result := FreshContainerResult{Status: gate.ResultStatusInfraFailed, ImageReference: request.ImageReference, ExitCode: -1}
	if err := runner.validateCleanupRequest(ctx, recovery); err != nil {
		return result, err
	}
	if request.RemovalPending {
		return runner.replayPendingRemoval(ctx, recovery, result)
	}
	containerID, absent, err := runner.resolveCleanupContainerForRecovery(ctx, recovery)
	if err != nil {
		return result, err
	}
	if absent {
		return runner.proveCleanupAbsence(ctx, recovery, result)
	}
	result.setContainerID(containerID)
	document, absent, err := runner.inspectCleanupContainer(ctx, recovery, containerID)
	if absent {
		return runner.proveCleanupAbsence(ctx, recovery, result)
	}
	if err == nil {
		err = runner.validateRecoveryContainerIdentity(document, recovery)
	}
	if err == nil {
		err = runner.attachCleanupResourceWitness(&result, document, recovery)
	}
	return runner.terminateUnprovedRecovery(ctx, recovery, result, err)
}

// validateCleanupRequest 校验清理运行器和待清理容器请求均可安全使用。
func (runner *FreshContainerRunner) validateCleanupRequest(ctx context.Context, request FreshContainerRecoveryRequest) error {
	if ctx == nil {
		return errors.New("cleanup runner and context are required")
	}
	if runner == nil {
		return errors.New("cleanup runner and context are required")
	}
	if runner.docker == nil {
		return errors.New("cleanup runner and context are required")
	}
	container := newContainerRequest(request.ImageReference, request.SourceSnapshotDir, request.Command, request.Profile == gate.ProfileRelease, request.ContainerLabels)
	return runner.docker.validateContainerRequest(container)
}

// inspectCleanupContainer 在精确持久身份已消失时返回 absence proof，否则保留原始检查错误。
func (runner *FreshContainerRunner) inspectCleanupContainer(
	ctx context.Context,
	request FreshContainerRecoveryRequest,
	containerID string,
) (containerInspectDocument, bool, error) {
	document, err := runner.inspectContainer(ctx, containerID)
	if err == nil || request.ContainerID == "" {
		return document, false, err
	}
	absent, absenceErr := runner.cleanupIdentityAbsent(ctx, request.ContainerID)
	if absenceErr != nil {
		return document, false, errors.Join(err, absenceErr)
	}
	return document, absent, err
}

// attachCleanupResourceWitness 将已验证的容器资源契约保留到清理生命周期中。
func (runner *FreshContainerRunner) attachCleanupResourceWitness(
	result *FreshContainerResult,
	document containerInspectDocument,
	request FreshContainerRecoveryRequest,
) error {
	canonical, err := runner.validateContainerContract(
		document, result.Container.ContainerID, request.ImageReference, request.ConfigDigest,
		request.SourceSnapshotDir, request.Command,
	)
	if err != nil {
		return err
	}
	if err := validateExpectedContainerLabels(document, request.ContainerLabels); err != nil {
		return err
	}
	hostConfigDigest, err := digestJSON(canonical)
	if err != nil {
		return fmt.Errorf("digest cleanup container host config: %w", err)
	}
	witness := gate.ContainerResourceWitness{
		SchemaVersion: gate.ContainerResourceWitnessSchemaVersion,
		NanoCPUs:      canonical.NanoCPUs, MemoryBytes: canonical.Memory, PidsLimit: canonical.PidsLimit,
	}
	witnessDigest, err := witness.Digest()
	if err != nil {
		return fmt.Errorf("digest cleanup container resource witness: %w", err)
	}
	result.Container.HostConfigDigest = hostConfigDigest
	result.Container.ResourceWitness = witness
	result.Container.ResourceWitnessDigest = witnessDigest
	return nil
}

// resolveCleanupContainerForRecovery 对 Creating 操作身份执行有界稳定性确认。
func (runner *FreshContainerRunner) resolveCleanupContainerForRecovery(
	ctx context.Context,
	request FreshContainerRecoveryRequest,
) (string, bool, error) {
	if err := validateCreatingOperationIdentity(request); err != nil {
		return "", false, err
	}
	containerID, absent, err := runner.resolveCleanupContainer(ctx, request)
	if err != nil || !absent || !IsFreshContainerOperationIdentity(request.ContainerID) {
		return containerID, absent, err
	}
	return runner.confirmCreatingContainerAbsence(ctx, request)
}

func validateCreatingOperationIdentity(request FreshContainerRecoveryRequest) error {
	if !IsFreshContainerOperationIdentity(request.ContainerID) {
		return nil
	}
	expected, err := FreshContainerOperationIdentity(request.ContainerLabels)
	if err != nil {
		return err
	}
	if request.ContainerID != expected {
		return errors.New("creating operation identity drifted from container labels")
	}
	return nil
}

// resolveCleanupContainer 对运行时 ID 直接定位；操作身份必须用完整 labels 闭包发现。
func (runner *FreshContainerRunner) resolveCleanupContainer(
	ctx context.Context,
	request FreshContainerRecoveryRequest,
) (string, bool, error) {
	if request.ContainerID != "" && !IsFreshContainerOperationIdentity(request.ContainerID) {
		return request.ContainerID, false, nil
	}
	return runner.resolveCleanupContainerByLabels(ctx, request.ContainerLabels)
}

// resolveCleanupContainerByLabels 以完整 labels 集合确认至多一个待清理容器。
func (runner *FreshContainerRunner) resolveCleanupContainerByLabels(
	ctx context.Context,
	labels map[string]string,
) (string, bool, error) {
	args := []string{"ps", "--all", "--no-trunc"}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--filter=label="+key+"="+labels[key])
	}
	args = append(args, "--format={{.ID}}")
	output, err := runner.runCleanup(ctx, args...)
	if err != nil {
		return "", false, fmt.Errorf("discover cleanup container: %w", err)
	}
	lines := strings.Fields(output)
	if len(lines) == 0 {
		return "", true, nil
	}
	if len(lines) != 1 || !isContainerID(lines[0]) {
		return "", false, fmt.Errorf("cleanup identity resolved %d canonical containers, want at most 1", len(lines))
	}
	return lines[0], false, nil
}

// confirmCreatingContainerAbsence 对迟到 create 执行连续且有界的零结果确认。
func (runner *FreshContainerRunner) confirmCreatingContainerAbsence(
	parent context.Context,
	request FreshContainerRecoveryRequest,
) (string, bool, error) {
	ctx, cancel := BoundedCleanupContext(parent, freshContainerLifecycleCleanupTimeout)
	defer cancel()
	for attempt := 1; attempt < creatingAbsenceProofs; attempt++ {
		containerID, absent, err := runner.resolveCleanupContainerByLabels(ctx, request.ContainerLabels)
		if err != nil {
			return "", false, err
		}
		if !absent {
			return containerID, false, nil
		}
		if attempt+1 == creatingAbsenceProofs {
			break
		}
		select {
		case <-ctx.Done():
			return "", false, fmt.Errorf("confirm creating container absence: %w", ctx.Err())
		case <-time.After(creatingAbsenceRetry):
		}
	}
	return "", true, nil
}

func (runner *FreshContainerRunner) cleanupIdentityAbsent(ctx context.Context, containerID string) (bool, error) {
	output, err := runner.runCleanup(ctx, "ps", "--all", "--no-trunc", "--filter=id="+containerID, "--format={{.ID}}")
	if err != nil {
		return false, fmt.Errorf("prove cleanup container absence: %w", err)
	}
	return strings.TrimSpace(output) == "", nil
}

// proveCleanupAbsence records Docker's zero-match result without inventing an execution clock.
func (runner *FreshContainerRunner) proveCleanupAbsence(
	ctx context.Context,
	recovery FreshContainerRecoveryRequest,
	result FreshContainerResult,
) (FreshContainerResult, error) {
	result.Container.Removed = true
	result.RemovalProofDigest = cleanupAbsenceProofDigest(recovery)
	result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindDocker, Digest: result.RemovalProofDigest})
	lifecycleResult := result
	if IsFreshContainerOperationIdentity(recovery.ContainerID) {
		lifecycleResult.Container.ContainerID = recovery.ContainerID
	}
	if err := runner.emitCleanupLifecycle(ctx, freshContainerRequestForRecovery(recovery), lifecycleResult, FreshContainerPhaseRemoved); err != nil {
		return result, err
	}
	return result, nil
}

func cleanupAbsenceProofDigest(request FreshContainerRecoveryRequest) string {
	keys := make([]string, 0, len(request.ContainerLabels))
	for key := range request.ContainerLabels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var proof strings.Builder
	proof.WriteString("absent\n")
	proof.WriteString(request.ContainerID)
	proof.WriteByte('\n')
	for _, key := range keys {
		proof.WriteString(key)
		proof.WriteByte('=')
		proof.WriteString(request.ContainerLabels[key])
		proof.WriteByte('\n')
	}
	return digestBytes([]byte(proof.String()))
}

// replayPendingRemoval 重放删除意图，并在 Docker 证明容器消失后提交最终证明。
func (runner *FreshContainerRunner) replayPendingRemoval(
	ctx context.Context,
	recovery FreshContainerRecoveryRequest,
	result FreshContainerResult,
) (FreshContainerResult, error) {
	if recovery.ContainerID == "" {
		return result, errors.New("pending removal requires a durable container ID")
	}
	output, err := runner.runCleanup(ctx, "ps", "--all", "--no-trunc", "--filter=id="+recovery.ContainerID, "--format={{.ID}}")
	if err != nil {
		return result, fmt.Errorf("replay pending removal proof: %w", err)
	}
	if strings.TrimSpace(output) != "" {
		return runner.removePendingContainer(ctx, recovery, result)
	}
	result.setContainerID(recovery.ContainerID)
	result.Container.Removed = true
	result.RemovalProofDigest = digestBytes([]byte("removed\n" + result.Container.ContainerID + "\n"))
	result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindDocker, Digest: result.RemovalProofDigest})
	request := freshContainerRequestForRecovery(recovery)
	if err := runner.emitCleanupLifecycle(ctx, request, result, FreshContainerPhaseRemoved); err != nil {
		return result, err
	}
	return result, nil
}

// removePendingContainer 仅在持久身份与现存容器完全一致时继续删除。
func (runner *FreshContainerRunner) removePendingContainer(
	ctx context.Context,
	recovery FreshContainerRecoveryRequest,
	result FreshContainerResult,
) (FreshContainerResult, error) {
	result.setContainerID(recovery.ContainerID)
	document, err := runner.inspectContainer(ctx, recovery.ContainerID)
	if err != nil {
		return result, fmt.Errorf("inspect pending removal container: %w", err)
	}
	if err := runner.validateRecoveryContainerIdentity(document, recovery); err != nil {
		return result, fmt.Errorf("validate pending removal container identity: %w", err)
	}
	return runner.terminateUnprovedRecovery(ctx, recovery, result, nil)
}

// validateRecoveryRequest 校验恢复时钟与不可变 Docker 请求，不接受重算 deadline。
func (runner *FreshContainerRunner) validateRecoveryRequest(request FreshContainerRecoveryRequest) error {
	if err := request.Profile.Validate(); err != nil {
		return err
	}
	if !hasCanonicalRecoveryClock(request) {
		return errors.New("recovery started_at and deadline must be non-zero UTC timestamps")
	}
	if !request.Deadline.Equal(request.StartedAt.Add(executionTimeout(request.Profile == gate.ProfileRelease))) {
		return errors.New("recovery deadline does not match the original profile timeout")
	}
	if request.ContainerID != "" && !isContainerID(request.ContainerID) {
		return errors.New("recovery container ID is invalid")
	}
	container := newContainerRequest(request.ImageReference, request.SourceSnapshotDir, request.Command, request.Profile == gate.ProfileRelease, request.ContainerLabels)
	if err := runner.docker.validateContainerRequest(container); err != nil {
		return err
	}
	if err := validateDigest("recovery config digest", request.ConfigDigest); err != nil {
		return err
	}
	if len(request.ContainerLabels) == 0 {
		return errors.New("recovery container labels are required")
	}
	return nil
}

// hasCanonicalRecoveryClock 确认恢复请求沿用原始 UTC 开始和截止时钟。
func hasCanonicalRecoveryClock(request FreshContainerRecoveryRequest) bool {
	return request.StartedAt.Equal(request.StartedAt.UTC()) && request.Deadline.Equal(request.Deadline.UTC()) &&
		!request.StartedAt.IsZero() && !request.Deadline.IsZero()
}

// freshContainerRequestForRecovery 复原终态清理所需的不可变容器请求。
func freshContainerRequestForRecovery(request FreshContainerRecoveryRequest) FreshContainerRequest {
	return FreshContainerRequest{
		Image: gate.ImageIdentity{ConfigDigest: request.ConfigDigest}, SourceSnapshotDir: request.SourceSnapshotDir,
		Profile: request.Profile, GateID: request.GateID, ContainerLabels: request.ContainerLabels,
		Deadline: request.Deadline, LifecycleHook: request.LifecycleHook,
	}
}

// resolveRecoveryContainer 使用持久 ID 或全部 labels 唯一定位原容器。
func (runner *FreshContainerRunner) resolveRecoveryContainer(
	ctx context.Context,
	request FreshContainerRecoveryRequest,
) (string, error) {
	if request.ContainerID != "" {
		return request.ContainerID, nil
	}
	keys := make([]string, 0, len(request.ContainerLabels))
	for key := range request.ContainerLabels {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	args := []string{"ps", "--all", "--no-trunc"}
	for _, key := range keys {
		args = append(args, "--filter=label="+key+"="+request.ContainerLabels[key])
	}
	args = append(args, "--format={{.ID}}")
	output, err := runner.docker.runner.Run(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("discover recovery container: %w", err)
	}
	lines := strings.Fields(output)
	if len(lines) != 1 || !isContainerID(lines[0]) {
		return "", fmt.Errorf("recovery labels resolved %d canonical containers, want 1", len(lines))
	}
	return lines[0], nil
}

// validateRecoveryContainer 验证候选容器身份及其可接管的运行状态。
func (runner *FreshContainerRunner) validateRecoveryContainer(
	document containerInspectDocument,
	request FreshContainerRecoveryRequest,
) error {
	if err := runner.validateRecoveryContainerIdentity(document, request); err != nil {
		return err
	}
	if document.State == nil || (!document.State.Running && document.State.Status != "exited") {
		return errors.New("recovery container is not alive or exited")
	}
	return nil
}

// validateRecoveryContainerIdentity 验证镜像、命令、挂载、隔离与 labels 的闭包。
func (runner *FreshContainerRunner) validateRecoveryContainerIdentity(
	document containerInspectDocument,
	request FreshContainerRecoveryRequest,
) error {
	expectedContainerID := document.ID
	if request.ContainerID != "" && !IsFreshContainerOperationIdentity(request.ContainerID) {
		expectedContainerID = request.ContainerID
	}
	if err := validateContainerIdentity(
		document, expectedContainerID, request.ImageReference, request.ConfigDigest, request.Command,
	); err != nil {
		return err
	}
	if err := validateContainerHostIsolation(document.HostConfig); err != nil {
		return err
	}
	if err := validateContainerMount(document, request.SourceSnapshotDir); err != nil {
		return err
	}
	if document.Config == nil {
		return errors.New("recovery container config is missing")
	}
	for key, expected := range request.ContainerLabels {
		if document.Config.Labels[key] != expected {
			return fmt.Errorf("recovery container label %q drifted", key)
		}
	}
	return nil
}

// terminateUnprovedRecovery 杀死并移除未能证明身份的恢复容器。
func (runner *FreshContainerRunner) terminateUnprovedRecovery(
	ctx context.Context,
	request FreshContainerRecoveryRequest,
	result FreshContainerResult,
	cause error,
) (FreshContainerResult, error) {
	cleanupContext, cancel := platformconfig.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_, killErr := runner.docker.runner.Run(cleanupContext, "kill", result.Container.ContainerID)
	if killErr == nil {
		result.Killed = true
		result.KillProofDigest = digestBytes([]byte("killed\n" + result.Container.ContainerID + "\n"))
	}
	_, waitErr := runner.docker.runner.Run(cleanupContext, "wait", result.Container.ContainerID)
	freshRequest := freshContainerRequestForRecovery(request)
	result, removeErr := runner.removeContainer(cleanupContext, result, freshRequest, nil)
	return result, errors.Join(cause, killErr, waitErr, removeErr)
}
