package remoteruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// TargetFacts 是 execute/bind 调用方提供的 exact repository target 身份。
// 它不属于 RuntimeConfig，因同一 runtime policy 必须服务 V3 和 WJ。
type TargetFacts struct {
	// RepositoryID 是调用方显式绑定的 target repository identity。
	RepositoryID string
	// ObjectFormat 是 target Git object format。
	ObjectFormat string
	// CommitSHA 是 exact target commit object。
	CommitSHA string
	// TreeSHA 是 exact target tree object。
	TreeSHA string
}

// ExecutionFacts 是每次 node/batch 执行调用显式提供的平台 runtime identity。
type ExecutionFacts struct {
	// GOOS 是本次 workload 的 Go OS。
	GOOS string
	// GOARCH 是本次 workload 的 Go architecture。
	GOARCH string
	// CGOEnabled 是本次 workload 的显式 cgo 开关。
	CGOEnabled string
	// ModuleIdentity 是本次 module cache identity。
	ModuleIdentity string
	// WorkloadIdentity 是本次 workload identity。
	WorkloadIdentity string
}

// BoundPortableRun 是 runtime policy 与一次 target facts 的显式绑定。
type BoundPortableRun struct {
	// RuntimeConfig 是未携带 target facts 的 immutable runtime policy。
	RuntimeConfig RuntimeConfig
	// Target 是本次调用显式绑定的 target facts。
	Target TargetFacts
	// Namespace 是 owner namespace 的 target-derived identity。
	Namespace string
}

// Validate 校验 target facts 的显式身份和 Git object 格式。
func (facts TargetFacts) Validate() error {
	if err := validateStableIdentity(facts.RepositoryID); err != nil {
		return errors.New("target repository_id is invalid")
	}
	if facts.ObjectFormat != "sha1" && facts.ObjectFormat != "sha256" {
		return errors.New("target object_format is invalid")
	}
	if err := validateTargetObjects(facts); err != nil {
		return err
	}
	return nil
}

// ValidateFor 校验 execution facts 与 node platform 的 exact GOOS/GOARCH 绑定。
func (facts ExecutionFacts) ValidateFor(platform Platform) error {
	wantOS := ""
	switch platform {
	case UbuntuPlatform:
		wantOS = "linux"
	case WindowsPlatform:
		wantOS = "windows"
	default:
		return errors.New("execution platform is unsupported")
	}
	if facts.GOOS != wantOS || facts.GOARCH != "amd64" || (facts.CGOEnabled != "0" && facts.CGOEnabled != "1") {
		return errors.New("execution Go facts do not match node platform")
	}
	for name, value := range map[string]string{"module_identity": facts.ModuleIdentity, "workload_identity": facts.WorkloadIdentity} {
		if err := validateStableIdentity(value); err != nil {
			return fmt.Errorf("execution %s is invalid", name)
		}
	}
	return nil
}

// ValidateForNode 校验 execution facts 与完整 node binding 的平台和 cgo exact 绑定。
func (facts ExecutionFacts) ValidateForNode(node NodeBinding) error {
	if err := facts.ValidateFor(node.Platform); err != nil {
		return err
	}
	if facts.CGOEnabled != node.CGOEnabled {
		return errors.New("execution cgo_enabled does not match node binding")
	}
	return nil
}

// ExecutionBaseFacts 从已校验 node binding 构造平台执行基线；module/workload identity 必须由 batch caller 注入。
func (node NodeBinding) ExecutionBaseFacts() (ExecutionFacts, error) {
	if err := node.Validate(); err != nil {
		return ExecutionFacts{}, fmt.Errorf("node execution base: %w", err)
	}
	goos := ""
	switch node.Platform {
	case UbuntuPlatform:
		goos = "linux"
	case WindowsPlatform:
		goos = "windows"
	default:
		return ExecutionFacts{}, errors.New("node execution platform is unsupported")
	}
	return ExecutionFacts{GOOS: goos, GOARCH: "amd64", CGOEnabled: node.CGOEnabled}, nil
}

func validateTargetObjects(facts TargetFacts) error {
	objectLength := 40
	if facts.ObjectFormat == "sha256" {
		objectLength = 64
	}
	for name, value := range map[string]string{"commit_sha": facts.CommitSHA, "tree_sha": facts.TreeSHA} {
		if len(value) != objectLength || !isLowerHex(value) {
			return fmt.Errorf("target %s is invalid", name)
		}
	}
	return nil
}

// BindPortableRun 将 target facts 绑定到 runtime policy，并派生隔离 owner namespace。
func BindPortableRun(config RuntimeConfig, target TargetFacts) (BoundPortableRun, error) {
	if err := config.Validate(); err != nil {
		return BoundPortableRun{}, err
	}
	if err := target.Validate(); err != nil {
		return BoundPortableRun{}, err
	}
	namespace, err := DeriveOwnerNamespace(config.OwnerNamespace, target)
	if err != nil {
		return BoundPortableRun{}, err
	}
	return BoundPortableRun{RuntimeConfig: config, Target: target, Namespace: namespace}, nil
}

// BindPortableRun 将 transport 绑定到一次 target facts，供后续 Execute 使用。
func (transport *Transport) BindPortableRun(target TargetFacts) (BoundPortableRun, error) {
	if transport == nil {
		return BoundPortableRun{}, ErrInvalidTransportDependencies
	}
	return BindPortableRun(transport.config, target)
}

// Execute 将 target facts 与 per-batch execution facts 注入一次 run/worker operation；fresh probe 必须调用 Probe。
func (transport *Transport) Execute(ctx context.Context, target TargetFacts, execution ExecutionFacts, operation Operation, nodeID string, request []byte) (NodeResult, error) {
	if _, err := transport.BindPortableRun(target); err != nil {
		return NodeResult{}, err
	}
	return transport.execute(ctx, operation, target, execution, nodeID, request)
}

// Execute 将已绑定 target 的 run/worker operation 投影到指定 transport；fresh probe 必须调用 Probe。
func (bound BoundPortableRun) Execute(ctx context.Context, transport *Transport, execution ExecutionFacts, operation Operation, nodeID string, request []byte) (NodeResult, error) {
	if transport == nil {
		return NodeResult{}, ErrInvalidTransportDependencies
	}
	boundPolicy, boundErr := EncodeConfig(bound.RuntimeConfig)
	transportPolicy, transportErr := EncodeConfig(transport.config)
	if boundErr != nil || transportErr != nil || !bytes.Equal(boundPolicy, transportPolicy) {
		return NodeResult{}, errors.New("bound runtime config drifted")
	}
	return transport.execute(ctx, operation, bound.Target, execution, nodeID, request)
}

// DeriveOwnerNamespace 使用 repository_id、object format 和 exact target object 派生 namespace。
func DeriveOwnerNamespace(ownerNamespace string, target TargetFacts) (string, error) {
	if err := validateStableIdentity(ownerNamespace); err != nil {
		return "", errors.New("owner namespace is invalid")
	}
	if err := target.Validate(); err != nil {
		return "", err
	}
	material := strings.Join([]string{
		"remote-ci-runtime-target/v1", ownerNamespace, target.RepositoryID,
		target.ObjectFormat, target.CommitSHA, target.TreeSHA,
	}, "\x00")
	digest := sha256.Sum256([]byte(material))
	return ownerNamespace + "/sha256:" + hex.EncodeToString(digest[:]), nil
}

func isLowerHex(value string) bool {
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
