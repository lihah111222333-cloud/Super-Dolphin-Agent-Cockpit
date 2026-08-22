package remotecicli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteruntime"
)

// currentPortableEngineFacts 从正在运行的独立 binary 和运行时计算 actual engine identity。
func currentPortableEngineFacts() (remoteportable.PortableEngineFacts, error) {
	executable, err := os.Executable()
	if err != nil {
		return remoteportable.PortableEngineFacts{}, errors.New("resolve current engine binary failed")
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil || validateAbsolutePathComponents(executable, false) != nil {
		return remoteportable.PortableEngineFacts{}, errors.New("current engine binary path is invalid")
	}
	binaryDigest, err := hashStableEngineBinary(executable)
	if err != nil {
		return remoteportable.PortableEngineFacts{}, err
	}
	protocolDigest, err := remoteportable.PortableBindingProtocolABIDigest()
	if err != nil {
		return remoteportable.PortableEngineFacts{}, err
	}
	toolchainDigest := digestBytes([]byte("remote-ci-toolchain/v1\x00" + runtime.Version()))
	return remoteportable.PortableEngineFacts{BinarySHA256: binaryDigest, ProtocolABIDigest: protocolDigest, ToolchainDigest: toolchainDigest, Capabilities: []string{"go"}}, nil
}

// hashStableEngineBinary 对独立 binary 做 bounded handle read，并核对前后 identity。
func hashStableEngineBinary(path string) (string, error) {
	const maxEngineBinaryBytes = 512 << 20
	file, openedInfo, err := openEngineBinary(path, maxEngineBinaryBytes)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	count, copyErr := io.Copy(hash, io.LimitReader(file, maxEngineBinaryBytes+1))
	finalInfo, finalStatErr := file.Stat()
	closeErr := file.Close()
	if err := validateEngineBinaryRead(count, openedInfo.Size(), copyErr, finalStatErr, closeErr, openedInfo, finalInfo); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// openEngineBinary 打开并冻结独立 binary 的 regular-file handle 与初始身份。
func openEngineBinary(path string, maxBytes int64) (*os.File, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.New("current engine binary is not regular")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, errors.New("current engine binary cannot be opened")
	}
	openedInfo, err := file.Stat()
	if err != nil || !sameFileIdentity(info, openedInfo) || openedInfo.Size() > maxBytes {
		_ = file.Close()
		return nil, nil, errors.New("current engine binary identity changed before hashing")
	}
	return file, openedInfo, nil
}

// validateEngineBinaryRead 校验独立 binary 的 bounded read、size 和最终 handle identity。
func validateEngineBinaryRead(count, size int64, copyErr, statErr, closeErr error, opened, final os.FileInfo) error {
	if copyErr != nil || statErr != nil || closeErr != nil {
		return errors.New("current engine binary changed during hashing")
	}
	if count != size || !sameFileIdentity(opened, final) {
		return errors.New("current engine binary changed during hashing")
	}
	return nil
}

// executePreparedInputs 是 execute 在任何网络或 authority I/O 前完成的不可变输入绑定。
type executePreparedInputs struct {
	RawRequest             []byte
	Portable               remoteportable.BoundPortableRun
	Repository             DetectedRepository
	Runtime                remoteruntime.RuntimeConfig
	RuntimePolicyDigest    string
	RuntimeBinding         remoteruntime.BoundPortableRun
	Topology               heteroci.Manifest
	TopologyManifestDigest string
	OwnerScope             heteroci.RoutingOwnerScope
	RunRequest             heterociworkflow.RunRequest
}

type executeLocatorBinding struct {
	raw               []byte
	portable          remoteportable.BoundPortableRun
	repositoryRoot    string
	runtimeConfigPath string
}

type executeRuntimePolicy struct {
	config       remoteruntime.RuntimeConfig
	policyDigest string
	topology     heteroci.Manifest
}

type executeCatalogBinding struct {
	repository     DetectedRepository
	runtimeBinding remoteruntime.BoundPortableRun
	units          []heteroci.ExecutionUnit
}

// prepareExecuteInputs 完成 exact tree、runtime、四节点 topology 和请求摘要绑定；不连接节点或 PostgreSQL。
func prepareExecuteInputs(ctx context.Context, args []string) (executePreparedInputs, error) {
	locator, err := loadExecuteLocatorBinding(args)
	if err != nil {
		return executePreparedInputs{}, err
	}
	runtimePolicy, err := loadExecuteRuntimePolicy(locator.runtimeConfigPath)
	if err != nil {
		return executePreparedInputs{}, err
	}
	catalog, err := bindExecuteCatalog(ctx, locator, runtimePolicy.config)
	if err != nil {
		return executePreparedInputs{}, err
	}
	return assembleExecutePreparedInputs(locator, runtimePolicy, catalog)
}

func loadExecuteLocatorBinding(args []string) (executeLocatorBinding, error) {
	requestPath, repositoryRoot, runtimeConfigPath, err := parseExecuteArgs(args)
	if err != nil {
		return executeLocatorBinding{}, err
	}
	raw, err := readDescriptorFile(requestPath)
	if err != nil {
		return executeLocatorBinding{}, newCLIFailure("read execute request", err)
	}
	descriptor, err := remoteportable.DecodePortableRunDescriptor(raw)
	if err != nil {
		return executeLocatorBinding{}, newCLIFailure("decode execute request", err)
	}
	portable, err := bindPortableDescriptor(descriptor, repositoryRoot)
	if err != nil {
		return executeLocatorBinding{}, newCLIFailure("bind execute target", err)
	}
	return executeLocatorBinding{raw: append([]byte(nil), raw...), portable: portable, repositoryRoot: repositoryRoot, runtimeConfigPath: runtimeConfigPath}, nil
}

func loadExecuteRuntimePolicy(path string) (executeRuntimePolicy, error) {
	config, err := remoteruntime.LoadConfig(path)
	if err != nil {
		return executeRuntimePolicy{}, newCLIFailure("load execute runtime config", err)
	}
	topology, err := heteroci.LoadManifestFile(config.TopologyManifestPath)
	if err != nil {
		return executeRuntimePolicy{}, newCLIFailure("load execute topology", err)
	}
	if err := validateExecuteRuntimeTopology(config, topology); err != nil {
		return executeRuntimePolicy{}, newCLIFailure("bind execute topology", err)
	}
	policyDigest, err := remoteruntime.PolicyDigest(config)
	if err != nil {
		return executeRuntimePolicy{}, newCLIFailure("derive execute runtime policy digest", err)
	}
	return executeRuntimePolicy{config: config, policyDigest: policyDigest, topology: topology}, nil
}

func bindExecuteCatalog(ctx context.Context, locator executeLocatorBinding, config remoteruntime.RuntimeConfig) (executeCatalogBinding, error) {
	repository, err := expandBoundPortableRepositoryCatalog(ctx, locator.repositoryRoot, locator.portable)
	if err != nil {
		return executeCatalogBinding{}, newCLIFailure("expand execute workload catalog", err)
	}
	units, err := remoteportable.ProjectPortableWorkloadBootstrapExecutionUnits(repository.Workloads)
	if err != nil {
		return executeCatalogBinding{}, newCLIFailure("project execute workloads", err)
	}
	target := remoteruntime.TargetFacts{RepositoryID: locator.portable.RepositoryID, ObjectFormat: locator.portable.ObjectFormat, CommitSHA: locator.portable.CommitSHA, TreeSHA: locator.portable.TreeSHA}
	runtimeBinding, err := remoteruntime.BindPortableRun(config, target)
	if err != nil {
		return executeCatalogBinding{}, newCLIFailure("bind execute runtime target", err)
	}
	return executeCatalogBinding{repository: repository, runtimeBinding: runtimeBinding, units: units}, nil
}

func assembleExecutePreparedInputs(locator executeLocatorBinding, runtimePolicy executeRuntimePolicy, catalog executeCatalogBinding) (executePreparedInputs, error) {
	ownerScope, err := heteroci.DeriveTopologyOwnerScope(runtimePolicy.config.OwnerNamespace, runtimePolicy.topology)
	if err != nil {
		return executePreparedInputs{}, newCLIFailure("derive execute owner scope", err)
	}
	topologyDigest, err := heteroci.DeriveTopologyManifestDigest(runtimePolicy.topology)
	if err != nil {
		return executePreparedInputs{}, newCLIFailure("derive execute topology digest", err)
	}
	externalOperationID, err := heteroci.ParseOperationID(locator.portable.OperationID)
	if err != nil {
		return executePreparedInputs{}, newCLIFailure("parse execute operation", err)
	}
	runIdentity, err := heteroci.DeriveTopologyRunIdentity(locator.portable.RepositoryID, locator.portable.TreeSHA, locator.portable.OperationID)
	if err != nil {
		return executePreparedInputs{}, newCLIFailure("derive execute run identity", err)
	}
	runRequest := newExecuteRunRequest(locator.portable, runtimePolicy, catalog, topologyDigest, externalOperationID, runIdentity.DurableRunID)
	runRequest.RequestDigest, err = heterociworkflow.DeriveRunRequestDigest(runRequest)
	if err != nil {
		return executePreparedInputs{}, newCLIFailure("derive execute request digest", err)
	}
	return executePreparedInputs{
		RawRequest: locator.raw, Portable: locator.portable, Repository: catalog.repository,
		Runtime: runtimePolicy.config, RuntimePolicyDigest: runtimePolicy.policyDigest, RuntimeBinding: catalog.runtimeBinding,
		Topology: runtimePolicy.topology, TopologyManifestDigest: topologyDigest, OwnerScope: ownerScope, RunRequest: runRequest,
	}, nil
}

func newExecuteRunRequest(portable remoteportable.BoundPortableRun, runtimePolicy executeRuntimePolicy, catalog executeCatalogBinding, topologyDigest string, operationID heteroci.OperationID, durableRunID heteroci.TopologyRunID) heterociworkflow.RunRequest {
	return heterociworkflow.RunRequest{
		SchemaVersion: heterociworkflow.RunRequestSchemaVersion, ExternalOperationID: operationID, DurableRunID: durableRunID,
		ManifestDigest: portable.ManifestDigest, ExternalBindingDigest: portable.BindingSHA256, ManifestBlobSHA256: portable.ManifestBlobSHA256, RuntimePolicyDigest: runtimePolicy.policyDigest, TopologyManifestDigest: topologyDigest,
		ManifestPath: runtimePolicy.config.TopologyManifestPath, RepositoryID: portable.RepositoryID,
		SourceTreeSHA: portable.TreeSHA, RunID: portable.OperationID, Units: catalog.units,
		HeartbeatTTL: time.Duration(runtimePolicy.config.HeartbeatTTLMS) * time.Millisecond,
	}
}

// parseExecuteArgs 只接受调用方显式 request、repository 与 runtime config。
func parseExecuteArgs(args []string) (string, string, string, error) {
	if len(args) != 6 || args[0] != "--request" || args[2] != "--repository" || args[4] != "--runtime-config" {
		return "", "", "", newCLIFailure("execute usage: execute --request <absolute-file> --repository <absolute-root> --runtime-config <absolute-file>", nil)
	}
	if err := validateDescriptorArgumentPath(args[1]); err != nil {
		return "", "", "", newCLIFailure("execute request path is invalid", err)
	}
	if err := validateAbsolutePathComponents(args[3], true); err != nil {
		return "", "", "", newCLIFailure("execute repository path is invalid", err)
	}
	if err := validateDescriptorArgumentPath(args[5]); err != nil {
		return "", "", "", newCLIFailure("execute runtime config path is invalid", err)
	}
	return args[1], args[3], args[5], nil
}

// validateExecuteRuntimeTopology 要求 runtime binding 与四节点 manifest 在 node、platform、endpoint 和 probe identity 上双向 exact 相等。
func validateExecuteRuntimeTopology(runtimeConfig remoteruntime.RuntimeConfig, topology heteroci.Manifest) error {
	if err := topology.ValidateFourNodeExecutionPool(); err != nil {
		return err
	}
	if len(runtimeConfig.Nodes) != len(topology.Nodes) {
		return errors.New("execute runtime and topology node sets differ")
	}
	bindings := make(map[string]remoteruntime.NodeBinding, len(runtimeConfig.Nodes))
	for _, binding := range runtimeConfig.Nodes {
		bindings[binding.NodeID] = binding
	}
	for _, node := range topology.Nodes {
		binding, ok := bindings[node.NodeID]
		if !ok || binding.Endpoint != node.Endpoint || binding.ProbeIdentity != node.ProbeIdentity || string(binding.Platform) != string(node.Platform) {
			return errors.New("execute runtime and topology node binding drifted")
		}
		delete(bindings, node.NodeID)
	}
	if len(bindings) != 0 {
		return errors.New("execute runtime contains stale topology node bindings")
	}
	return nil
}

// expandBoundPortableRepositoryCatalog 从已绑定 exact tree 构造 catalog 输入，不读取 cwd、HEAD 或 branch。
func expandBoundPortableRepositoryCatalog(ctx context.Context, repositoryRoot string, portable remoteportable.BoundPortableRun) (DetectedRepository, error) {
	repository := DetectedRepository{
		Root: repositoryRoot, RepositoryID: portable.RepositoryID, ObjectFormat: portable.ObjectFormat,
		CommitSHA: portable.CommitSHA, TreeSHA: portable.TreeSHA, ManifestPath: portable.ManifestPath,
		ManifestBlobSHA256: portable.ManifestBlobSHA256, Manifest: portable.Manifest,
		Workloads: append([]remoteportable.PortableManifestWorkload(nil), portable.Manifest.Workloads...),
	}
	return expandDetectedRepositoryGoCatalog(ctx, repository)
}
