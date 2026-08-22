package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
)

// ErrHeterociRootInput 表示异构 workflow composition root 缺少显式输入。
var ErrHeterociRootInput = errors.New("heteroci workflow root input is invalid")

// HeterociRootInput 是待生产装配的 composition seam 所需全部显式依赖。
type HeterociRootInput struct {
	// ManifestPath 是唯一允许的本地异构清单绝对路径。
	ManifestPath string
	// OwnerDBPath 是独立 durable owner SQLite authority 的绝对路径。
	OwnerDBPath string
	// Scope 是本次 owner 生命周期绑定的 routing scope。
	Scope heteroci.RoutingOwnerScope
	// RepositoryID 是本次 run 显式审计绑定的目标仓库身份。
	RepositoryID string
	// SourceTreeSHA 是本次 run 显式审计绑定的 canonical Git tree OID。
	SourceTreeSHA string
	// ExternalOperationID 是外部 request 的不可变 operation identity。
	ExternalOperationID heteroci.OperationID
	// ManifestDigest 是外部 binding 提供的 portable manifest raw blob 摘要。
	ManifestDigest string
	// RunID 是本次 workflow 的稳定运行 identity。
	RunID string
	// Units 是待规划的异构 execution units。
	Units []heteroci.ExecutionUnit
	// HeartbeatTTL 是节点快照和 lease 使用的正 heartbeat TTL。
	HeartbeatTTL time.Duration
	// ProbeTransport 是显式注入的节点探针传输端口。
	ProbeTransport heteroci.ProbeTransport
	// ExecutorBinder 是显式注入的 local/ECI executor 绑定端口。
	ExecutorBinder heterociworkflow.ExecutorBinder
	// LifecycleObserver 是生产 lifecycle 事件持久化的显式注入端口。
	LifecycleObserver heterociworkflow.LifecycleObserver
	// CapacityBlockedObserver 是生产容量阻塞回执的显式注入端口。
	CapacityBlockedObserver heterociworkflow.CapacityBlockedObserver
	// CleanupTimeout 是 workflow bounded cleanup 的正超时。
	CleanupTimeout time.Duration
	// Now 是 workflow 使用的显式时钟。
	Now func() time.Time
}

// RunHeterociWorkflow 在尚未接入 CLI 的 composition seam 中运行一次本地异构 workflow。
// owner 数据库、manifest、probe、executor 和时钟均不得从环境或默认路径推断；
// typed execution payload 与生产 requester/runner 接线闭合前，本函数不得暴露为 CLI 成功路径。
func RunHeterociWorkflow(ctx context.Context, input HeterociRootInput) (result heterociworkflow.RunResult, returnErr error) {
	if err := validateHeterociRootInput(ctx, input); err != nil {
		return result, err
	}
	manifest, err := heteroci.LoadManifestFile(input.ManifestPath)
	if err != nil {
		return result, fmt.Errorf("%w: load manifest: %w", ErrHeterociRootInput, err)
	}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("%w: context canceled before owner open: %w", ErrHeterociRootInput, err)
	}
	database, err := sql.Open("sqlite", input.OwnerDBPath)
	if err != nil {
		return result, fmt.Errorf("%w: open owner database: %w", ErrHeterociRootInput, err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, database.Close())
	}()
	owner, err := heterociowner.NewOwnerWithClock(ctx, database, input.Scope, input.Now)
	if err != nil {
		return result, fmt.Errorf("%w: construct durable owner: %w", ErrHeterociRootInput, err)
	}
	manifestCopy := cloneHeterociRootManifest(manifest)
	runIdentity, err := heteroci.DeriveTopologyRunIdentity(input.RepositoryID, input.SourceTreeSHA, input.RunID)
	if err != nil {
		return result, fmt.Errorf("%w: derive durable run identity: %w", ErrHeterociRootInput, err)
	}
	topologyManifestDigest, err := heterociworkflow.DeriveManifestDigest(manifest)
	if err != nil {
		return result, fmt.Errorf("%w: derive topology manifest digest: %w", ErrHeterociRootInput, err)
	}
	dependencies := heterociworkflow.Dependencies{
		Owner:                   owner,
		ProbeTransport:          input.ProbeTransport,
		Executors:               input.ExecutorBinder,
		LifecycleObserver:       input.LifecycleObserver,
		CapacityBlockedObserver: input.CapacityBlockedObserver,
		LoadManifest:            fixedHeterociRootManifestLoader(input.ManifestPath, manifestCopy),
		AcquireSnapshot:         heteroci.AcquireSnapshot,
		Now:                     input.Now,
		CleanupTimeout:          input.CleanupTimeout,
	}
	request := heterociworkflow.RunRequest{
		SchemaVersion:          heterociworkflow.RunRequestSchemaVersion,
		ExternalOperationID:    input.ExternalOperationID,
		DurableRunID:           runIdentity.DurableRunID,
		ManifestDigest:         input.ManifestDigest,
		TopologyManifestDigest: topologyManifestDigest,
		ManifestPath:           input.ManifestPath,
		RepositoryID:           input.RepositoryID,
		SourceTreeSHA:          input.SourceTreeSHA,
		RunID:                  input.RunID,
		Units:                  cloneHeterociRootUnits(input.Units),
		HeartbeatTTL:           input.HeartbeatTTL,
	}
	request.RequestDigest, err = heterociworkflow.DeriveRunRequestDigest(request)
	if err != nil {
		return result, fmt.Errorf("%w: derive request digest: %w", ErrHeterociRootInput, err)
	}
	return heterociworkflow.Execute(ctx, dependencies, request)
}

// validateHeterociRootInput 在任何文件、数据库或 owner mutation 前拒绝缺失依赖。
func validateHeterociRootInput(ctx context.Context, input HeterociRootInput) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrHeterociRootInput)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: context is already done: %w", ErrHeterociRootInput, err)
	}
	if err := validateHeterociRootPaths(input.ManifestPath, input.OwnerDBPath); err != nil {
		return err
	}
	if err := validateHeterociRootRouting(input); err != nil {
		return err
	}
	if err := validateHeterociRootTiming(input); err != nil {
		return err
	}
	if err := validateHeterociRootDependencies(input); err != nil {
		return err
	}
	return validateHeterociRootClock(input.Now)
}

// validateHeterociRootRouting 校验 scope、run audit 与执行单元的显式边界。
func validateHeterociRootRouting(input HeterociRootInput) error {
	if err := input.Scope.Validate(); err != nil {
		return fmt.Errorf("%w: scope is invalid", ErrHeterociRootInput)
	}
	if err := validateHeterociRootRunID(input.RunID); err != nil {
		return err
	}
	if err := heteroci.ValidateTopologyAuditIdentity(input.RepositoryID, input.SourceTreeSHA, input.RunID); err != nil {
		return fmt.Errorf("%w: run audit identity is invalid: %w", ErrHeterociRootInput, err)
	}
	if err := input.ExternalOperationID.Validate(); err != nil || input.ManifestDigest == "" {
		return fmt.Errorf("%w: external operation and manifest digest are required", ErrHeterociRootInput)
	}
	if err := heteroci.ValidateExecutionUnits(input.Units); err != nil {
		return fmt.Errorf("%w: execution units are invalid: %w", ErrHeterociRootInput, err)
	}
	return nil
}

// validateHeterociRootTiming 校验 heartbeat 与 cleanup 的正时长。
func validateHeterociRootTiming(input HeterociRootInput) error {
	if input.HeartbeatTTL <= 0 || input.HeartbeatTTL.Milliseconds() <= 0 {
		return fmt.Errorf("%w: heartbeat TTL is invalid", ErrHeterociRootInput)
	}
	if input.CleanupTimeout <= 0 {
		return fmt.Errorf("%w: cleanup timeout is invalid", ErrHeterociRootInput)
	}
	return nil
}

// validateHeterociRootDependencies 校验 probe 与 executor 的显式注入对象。
func validateHeterociRootDependencies(input HeterociRootInput) error {
	if isNilHeterociRootDependency(input.ProbeTransport) || isNilHeterociRootDependency(input.ExecutorBinder) || isNilHeterociRootDependency(input.LifecycleObserver) || isNilHeterociRootDependency(input.CapacityBlockedObserver) {
		return fmt.Errorf("%w: probe transport, executor binder, lifecycle observer, and capacity-blocked observer are required", ErrHeterociRootInput)
	}
	return nil
}

// validateHeterociRootClock 校验显式时钟且不接受 Unix epoch 之前的值。
func validateHeterociRootClock(now func() time.Time) error {
	if now == nil || now().UnixMilli() <= 0 {
		return fmt.Errorf("%w: clock is invalid", ErrHeterociRootInput)
	}
	return nil
}

// validateHeterociRootPaths 只接受调用方直接提供的绝对 clean 路径。
func validateHeterociRootPaths(manifestPath, ownerDBPath string) error {
	if err := validateHeterociRootPath(manifestPath, "manifest path"); err != nil {
		return err
	}
	if err := validateHeterociRootPath(ownerDBPath, "owner database path"); err != nil {
		return err
	}
	if err := validateHeterociRootFilesystemPath(manifestPath, "manifest path"); err != nil {
		return err
	}
	return validateHeterociRootFilesystemPath(ownerDBPath, "owner database path")
}

// validateHeterociRootPath 拒绝空白、控制字符、相对路径和 clean 之外的路径。
func validateHeterociRootPath(path, field string) error {
	if path == "" || path != strings.TrimSpace(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %s is invalid", ErrHeterociRootInput, field)
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%w: %s must be an absolute clean path", ErrHeterociRootInput, field)
	}
	return nil
}

// validateHeterociRootFilesystemPath 逐个检查现存路径组件，并仅允许缺失最终文件。
func validateHeterociRootFilesystemPath(path, field string) error {
	prefixes := heterociRootPathPrefixes(path)
	for index, prefix := range prefixes {
		final := index == len(prefixes)-1
		done, err := validateHeterociRootPathComponent(prefix, field, final)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	return nil
}

// heterociRootPathPrefixes 返回从文件系统根到目标的有序 clean 路径前缀。
func heterociRootPathPrefixes(path string) []string {
	separator := string(filepath.Separator)
	root := filepath.VolumeName(path) + separator
	prefixes := []string{root}
	relative := strings.TrimPrefix(path, root)
	if relative == "" {
		return prefixes
	}
	current := root
	for component := range strings.SplitSeq(relative, separator) {
		current = filepath.Join(current, component)
		prefixes = append(prefixes, current)
	}
	return prefixes
}

// validateHeterociRootPathComponent 拒绝 symlink、非目录父组件与非 regular 最终文件。
func validateHeterociRootPathComponent(path, field string, final bool) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return validateHeterociRootPathComponentError(err, field, final)
	}
	return false, validateHeterociRootPathComponentMode(info, field, final)
}

// validateHeterociRootPathComponentError 只允许最终文件尚不存在且不泄漏具体路径。
func validateHeterociRootPathComponentError(err error, field string, final bool) (bool, error) {
	if errors.Is(err, os.ErrNotExist) {
		if final {
			return true, nil
		}
		return false, fmt.Errorf("%w: %s parent component does not exist", ErrHeterociRootInput, field)
	}
	return false, fmt.Errorf("%w: %s component cannot be inspected", ErrHeterociRootInput, field)
}

// validateHeterociRootPathComponentMode 校验父组件为真实目录、最终组件为真实 regular file。
func validateHeterociRootPathComponentMode(info os.FileInfo, field string, final bool) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s contains a symbolic link", ErrHeterociRootInput, field)
	}
	if final {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: %s is not a regular file", ErrHeterociRootInput, field)
		}
		return nil
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s parent component is not a directory", ErrHeterociRootInput, field)
	}
	return nil
}

// validateHeterociRootRunID 校验 root 不在 owner 初始化后才发现非法 identity。
func validateHeterociRootRunID(runID string) error {
	if runID == "" || strings.TrimSpace(runID) != runID || len(runID) > 256 {
		return fmt.Errorf("%w: run ID is invalid", ErrHeterociRootInput)
	}
	for _, character := range runID {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%w: run ID is invalid", ErrHeterociRootInput)
		}
	}
	return nil
}

// isNilHeterociRootDependency 识别接口携带的 typed nil，避免进入 owner mutation。
func isNilHeterociRootDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// fixedHeterociRootManifestLoader 固定唯一预检过的显式 manifest path，拒绝路径漂移。
func fixedHeterociRootManifestLoader(path string, manifest heteroci.Manifest) heterociworkflow.ManifestLoader {
	return func(requested string) (heteroci.Manifest, error) {
		if requested != path {
			return heteroci.Manifest{}, fmt.Errorf("%w: manifest path drifted", ErrHeterociRootInput)
		}
		return cloneHeterociRootManifest(manifest), nil
	}
}

// cloneHeterociRootManifest 隔离 workflow 对 manifest 节点和能力 slice 的读取。
func cloneHeterociRootManifest(manifest heteroci.Manifest) heteroci.Manifest {
	manifest.Nodes = append([]heteroci.NodeConfig(nil), manifest.Nodes...)
	for index := range manifest.Nodes {
		manifest.Nodes[index].Capabilities = append([]heteroci.CapabilityConfig(nil), manifest.Nodes[index].Capabilities...)
	}
	return manifest
}

// cloneHeterociRootUnits 隔离 caller 对 execution unit slice backing array 的后续修改。
func cloneHeterociRootUnits(units []heteroci.ExecutionUnit) []heteroci.ExecutionUnit {
	return append([]heteroci.ExecutionUnit(nil), units...)
}
