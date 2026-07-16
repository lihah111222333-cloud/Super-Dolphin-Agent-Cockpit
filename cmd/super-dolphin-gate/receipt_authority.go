package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

type resultReceiptSigner interface {
	SignResultReceipt(gatecontract.ResultReceipt) (gatecontract.ResultReceipt, error)
}

type resultReceiptVerifier interface {
	VerifyResultReceipt(gatecontract.ResultReceipt) error
}

type hookResultReceiptAuthority interface {
	VerifyCurrentResultReceipt(context.Context, gatecontract.ResultReceipt) error
}

type productionHookResultReceiptAuthority struct {
	verifier resultReceiptVerifier
	accepted *productionAcceptedImageLoader
}

type ed25519ResultReceiptSigner struct {
	identity   gatecontract.SignerIdentity
	privateKey ed25519.PrivateKey
}

type ed25519ResultReceiptVerifier struct {
	identity  gatecontract.SignerIdentity
	publicKey ed25519.PublicKey
}

func newEd25519ResultReceiptSigner(
	identity gatecontract.SignerIdentity,
	privateKey ed25519.PrivateKey,
	publicKey ed25519.PublicKey,
) (*ed25519ResultReceiptSigner, error) {
	if err := identity.Validate(); err != nil {
		return nil, fmt.Errorf("result receipt signer identity: %w", err)
	}
	if len(privateKey) != ed25519.PrivateKeySize || len(publicKey) != ed25519.PublicKeySize ||
		!privateKey.Public().(ed25519.PublicKey).Equal(publicKey) {
		return nil, errors.New("result receipt Ed25519 private and public keys do not match")
	}
	return &ed25519ResultReceiptSigner{
		identity: identity, privateKey: append(ed25519.PrivateKey(nil), privateKey...),
	}, nil
}

func newEd25519ResultReceiptVerifier(
	identity gatecontract.SignerIdentity,
	publicKey ed25519.PublicKey,
) (*ed25519ResultReceiptVerifier, error) {
	if err := identity.Validate(); err != nil {
		return nil, fmt.Errorf("result receipt verifier identity: %w", err)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("result receipt public key must be Ed25519")
	}
	return &ed25519ResultReceiptVerifier{
		identity: identity, publicKey: append(ed25519.PublicKey(nil), publicKey...),
	}, nil
}

func newProductionResultReceiptVerifier(
	config productionCoordinatorConfig,
) (*ed25519ResultReceiptVerifier, error) {
	publicKey, err := decodeResultReceiptPublicKey(config.ResultReceiptAuthority.PublicKey)
	if err != nil {
		return nil, err
	}
	return newEd25519ResultReceiptVerifier(config.ResultReceiptAuthority.Signer, publicKey)
}

// newProductionResultReceiptSigner 从 host owner 的仓库外 0600 配置加载 Ed25519 私钥。
func newProductionResultReceiptSigner(
	config productionCoordinatorConfig,
) (*ed25519ResultReceiptSigner, error) {
	publicKey, err := decodeResultReceiptPublicKey(config.ResultReceiptAuthority.PublicKey)
	if err != nil {
		return nil, err
	}
	path, err := canonicalProductionFile(
		"result receipt private key",
		config.ResultReceiptAuthority.PrivateKeyFile,
	)
	if err != nil {
		return nil, err
	}
	data, err := readProductionCoordinatorConfig(path)
	if err != nil {
		return nil, fmt.Errorf("read result receipt private key: %w", err)
	}
	var encoded productionResultReceiptPrivateKey
	if err := gatecontract.DecodeStrictJSON(data, &encoded); err != nil {
		return nil, fmt.Errorf("decode result receipt private key: %w", err)
	}
	privateKey, err := base64.StdEncoding.Strict().DecodeString(encoded.PrivateKey)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("result receipt private key must be canonical base64 Ed25519")
	}
	return newEd25519ResultReceiptSigner(
		config.ResultReceiptAuthority.Signer,
		ed25519.PrivateKey(privateKey),
		publicKey,
	)
}

func decodeResultReceiptPublicKey(encoded string) (ed25519.PublicKey, error) {
	publicKey, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("result receipt public key must be canonical base64 Ed25519")
	}
	return ed25519.PublicKey(publicKey), nil
}

func newProductionHookResultReceiptAuthority(
	ctx context.Context,
	config productionCoordinatorConfig,
) (*productionHookResultReceiptAuthority, error) {
	verifier, err := newProductionResultReceiptVerifier(config)
	if err != nil {
		return nil, err
	}
	accepted, _, err := newProductionAcceptedImageLoader(ctx, config)
	if err != nil {
		return nil, err
	}
	return &productionHookResultReceiptAuthority{verifier: verifier, accepted: accepted}, nil
}

// VerifyCurrentResultReceipt 验签并绑定当前 accepted image generation。
func (authority *productionHookResultReceiptAuthority) VerifyCurrentResultReceipt(
	ctx context.Context,
	receipt gatecontract.ResultReceipt,
) error {
	if authority == nil || authority.verifier == nil || authority.accepted == nil {
		return errors.New("hook result receipt authority is not configured")
	}
	if err := authority.verifier.VerifyResultReceipt(receipt); err != nil {
		return fmt.Errorf("verify result receipt signature: %w", err)
	}
	accepted, err := authority.accepted.Load(ctx)
	if err != nil {
		return fmt.Errorf("load current accepted image record: %w", err)
	}
	return validateCurrentAcceptedReceipt(receipt, accepted)
}

// validateCurrentAcceptedReceipt 比较 receipt 与当前 accepted authority 全部执行身份。
func validateCurrentAcceptedReceipt(
	receipt gatecontract.ResultReceipt,
	accepted gatecontract.AcceptedImageRecord,
) error {
	if receipt.RepoID != accepted.RepoID || receipt.PolicyDigest != accepted.PolicyDigest ||
		receipt.Generation != accepted.Generation ||
		!reflect.DeepEqual(receipt.Image, accepted.Image) ||
		!reflect.DeepEqual(receipt.Runner, accepted.Runner) {
		return errors.New("result receipt does not match current accepted image generation")
	}
	return nil
}

// SignResultReceipt 对规范签名载荷生成真实 Ed25519 签名并回验结构。
func (signer *ed25519ResultReceiptSigner) SignResultReceipt(
	receipt gatecontract.ResultReceipt,
) (gatecontract.ResultReceipt, error) {
	if signer == nil || len(signer.privateKey) != ed25519.PrivateKeySize {
		return gatecontract.ResultReceipt{}, errors.New("result receipt signer is not configured")
	}
	receipt.Signer = signer.identity
	receipt.Signature = ""
	payload, err := gatecontract.ResultReceiptSigningPayload(receipt)
	if err != nil {
		return gatecontract.ResultReceipt{}, fmt.Errorf("build result receipt signing payload: %w", err)
	}
	receipt.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(signer.privateKey, payload))
	if err := receipt.Validate(); err != nil {
		return gatecontract.ResultReceipt{}, fmt.Errorf("validate signed result receipt: %w", err)
	}
	return receipt, nil
}

// VerifyResultReceipt 校验受信 signer identity 与 Ed25519 签名。
func (verifier *ed25519ResultReceiptVerifier) VerifyResultReceipt(receipt gatecontract.ResultReceipt) error {
	if verifier == nil || !reflect.DeepEqual(receipt.Signer, verifier.identity) {
		return errors.New("result receipt signer identity is not trusted")
	}
	return gatecontract.VerifyResultReceipt(receipt, verifier.publicKey)
}

type receiptExecution struct {
	Accepted    gatecontract.AcceptedImageRecord
	Results     []gatecontract.GateResult
	Evidence    []gatecontract.Evidence
	Containers  []gatecontract.ContainerEvidence
	StartedAt   time.Time
	CompletedAt time.Time
	Deadline    time.Time
}

// appendResult 校验并累计一个真实 fresh-container 执行结果。
func (execution *receiptExecution) appendResult(result localci.FreshContainerResult) error {
	if err := validateFreshReceiptResult(result); err != nil {
		return err
	}
	processDigest, containerDigest, err := receiptExecutionDigests(result)
	if err != nil {
		return err
	}
	if err := execution.recordReceiptTimeline(result); err != nil {
		return err
	}
	execution.Results = append(execution.Results, *result.GateResult)
	execution.Evidence = append(execution.Evidence, result.Evidence...)
	execution.Evidence = append(execution.Evidence,
		gatecontract.Evidence{Kind: gatecontract.EvidenceKindProcess, Digest: processDigest},
		gatecontract.Evidence{Kind: gatecontract.EvidenceKindDocker, Digest: containerDigest},
	)
	execution.Containers = append(execution.Containers, result.Container)
	return nil
}

// validateFreshReceiptResult 校验 gate、容器、时间线和直接证据闭包。
func validateFreshReceiptResult(result localci.FreshContainerResult) error {
	if result.GateResult == nil {
		return errors.New("fresh container result is missing gate result")
	}
	if err := result.Container.Validate(); err != nil {
		return fmt.Errorf("fresh container evidence: %w", err)
	}
	if err := validateFreshReceiptTimeline(result); err != nil {
		return err
	}
	return validateFreshReceiptEvidence(result)
}

// validateFreshReceiptTimeline 校验容器和 GateResult 的时间线完全一致。
func validateFreshReceiptTimeline(result localci.FreshContainerResult) error {
	if result.StartedAt.IsZero() || result.CompletedAt.Before(result.StartedAt) ||
		!result.Deadline.After(result.StartedAt) || result.CompletedAt.After(result.Deadline) {
		return errors.New("fresh container result timeline is incomplete")
	}
	if result.GateResult.LogDigest != result.LogDigest ||
		!result.GateResult.StartedAt.Equal(result.StartedAt) ||
		!result.GateResult.CompletedAt.Equal(result.CompletedAt) {
		return errors.New("fresh container gate result drifted from execution evidence")
	}
	return nil
}

// validateFreshReceiptEvidence 校验日志和 removal proof 均直接可追溯。
func validateFreshReceiptEvidence(result localci.FreshContainerResult) error {
	if !slices.Contains(result.Evidence, gatecontract.Evidence{
		Kind: gatecontract.EvidenceKindLog, Digest: result.LogDigest,
	}) || !slices.Contains(result.Evidence, gatecontract.Evidence{
		Kind: gatecontract.EvidenceKindDocker, Digest: result.RemovalProofDigest,
	}) {
		return errors.New("fresh container result is missing log or removal evidence")
	}
	if result.RemovalProofDigest != containerRemovalProofDigest(result.Container.ContainerID) {
		return errors.New("fresh container removal proof does not bind the container identity")
	}
	return nil
}

// receiptExecutionDigests 生成 process 与 container 的规范证据摘要。
func receiptExecutionDigests(result localci.FreshContainerResult) (string, string, error) {
	processDigest, err := canonicalReceiptDigest(*result.GateResult)
	if err != nil {
		return "", "", err
	}
	containerDigest, err := canonicalReceiptDigest(result.Container)
	if err != nil {
		return "", "", err
	}
	return processDigest, containerDigest, nil
}

// recordReceiptTimeline 累计 job 时间线并阻断越过 coordinator deadline。
func (execution *receiptExecution) recordReceiptTimeline(result localci.FreshContainerResult) error {
	if len(execution.Results) == 0 {
		execution.StartedAt = result.StartedAt
		if execution.Deadline.IsZero() {
			execution.Deadline = result.Deadline
		}
	}
	if result.Deadline.After(execution.Deadline) {
		return errors.New("fresh container deadline exceeds coordinator job deadline")
	}
	execution.CompletedAt = result.CompletedAt
	return nil
}

func buildPassedResultReceipt(
	record coordinatorJobRecord,
	execution receiptExecution,
	signer resultReceiptSigner,
) (gatecontract.ResultReceipt, error) {
	if interfaceIsNil(signer) {
		return gatecontract.ResultReceipt{}, errors.New("result receipt signer is required")
	}
	if err := validatePassedReceiptExecution(record, execution); err != nil {
		return gatecontract.ResultReceipt{}, err
	}
	container, err := aggregateContainerEvidence(execution.Containers)
	if err != nil {
		return gatecontract.ResultReceipt{}, err
	}
	receipt := gatecontract.ResultReceipt{
		SchemaVersion: 1, ReceiptID: resultReceiptID(record.JobID),
		RepoID: execution.Accepted.RepoID, InvocationID: record.InvocationID,
		Source: record.Plan.Source, PlanDigest: record.Plan.PlanDigest, PolicyDigest: record.Plan.PolicyDigest,
		Runner: execution.Accepted.Runner, Image: execution.Accepted.Image,
		Generation: execution.Accepted.Generation,
		StartedAt:  execution.StartedAt, CompletedAt: execution.CompletedAt, Deadline: execution.Deadline,
		Status:      gatecontract.ResultStatusPassed,
		GateResults: append([]gatecontract.GateResult(nil), execution.Results...),
		Evidence:    append([]gatecontract.Evidence(nil), execution.Evidence...), Container: container,
	}
	return signer.SignResultReceipt(receipt)
}

// validatePassedReceiptExecution 要求 passed job 的全部 gate 与容器证据完整一致。
func validatePassedReceiptExecution(record coordinatorJobRecord, execution receiptExecution) error {
	if err := execution.Accepted.Validate(); err != nil {
		return fmt.Errorf("accepted image record: %w", err)
	}
	if execution.Accepted.PolicyDigest != record.Plan.PolicyDigest {
		return errors.New("accepted image policy digest does not match job plan")
	}
	if len(execution.Results) != len(record.Plan.Gates) || len(execution.Containers) != len(record.Plan.Gates) {
		return errors.New("passed execution does not cover every planned gate and container")
	}
	for index, gateSpec := range record.Plan.Gates {
		if execution.Results[index].GateID != string(gateSpec.ID) ||
			execution.Results[index].Status != gatecontract.GateStatusPassed {
			return fmt.Errorf("passed execution gate %d does not match plan", index)
		}
	}
	return nil
}

// aggregateContainerEvidence 汇总各 gate 容器身份并要求 removal proof 完整一致。
func aggregateContainerEvidence(containers []gatecontract.ContainerEvidence) (gatecontract.ContainerEvidence, error) {
	if len(containers) == 0 {
		return gatecontract.ContainerEvidence{}, errors.New("container evidence is required")
	}
	for index := range containers {
		if err := containers[index].Validate(); err != nil {
			return gatecontract.ContainerEvidence{}, fmt.Errorf("container evidence %d: %w", index, err)
		}
	}
	allDigest, err := canonicalReceiptDigest(containers)
	if err != nil {
		return gatecontract.ContainerEvidence{}, err
	}
	hostDigests := make([]string, len(containers))
	networkDigests := make([]string, len(containers))
	for index := range containers {
		hostDigests[index] = containers[index].HostConfigDigest
		networkDigests[index] = containers[index].NetworkPolicyDigest
	}
	hostDigest, err := canonicalReceiptDigest(hostDigests)
	if err != nil {
		return gatecontract.ContainerEvidence{}, err
	}
	networkDigest, err := canonicalReceiptDigest(networkDigests)
	if err != nil {
		return gatecontract.ContainerEvidence{}, err
	}
	return gatecontract.ContainerEvidence{
		ContainerID: "aggregate:" + allDigest, NetworkID: "aggregate:" + allDigest,
		HostConfigDigest: hostDigest, NetworkPolicyDigest: networkDigest,
		Removed: true, NetworkRemoved: true,
	}, nil
}

func canonicalReceiptDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal receipt evidence: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

func containerRemovalProofDigest(containerID string) string {
	digest := sha256.Sum256([]byte("removed\n" + containerID + "\n"))
	return fmt.Sprintf("sha256:%x", digest)
}

func resultReceiptID(jobID string) string {
	digest := sha256.Sum256([]byte("super-dolphin-result-receipt\x00" + jobID))
	return fmt.Sprintf("receipt-%x", digest)
}

func cloneResultReceipt(receipt gatecontract.ResultReceipt) gatecontract.ResultReceipt {
	if receipt.Source.Commit != nil {
		commit := *receipt.Source.Commit
		receipt.Source.Commit = &commit
	}
	if receipt.Source.Range != nil {
		receiptRange := *receipt.Source.Range
		receipt.Source.Range = &receiptRange
	}
	if receipt.Source.Tree != nil {
		tree := *receipt.Source.Tree
		receipt.Source.Tree = &tree
	}
	receipt.Image.RootFSDiffIDs = append([]string(nil), receipt.Image.RootFSDiffIDs...)
	receipt.GateResults = append([]gatecontract.GateResult(nil), receipt.GateResults...)
	receipt.Evidence = append([]gatecontract.Evidence(nil), receipt.Evidence...)
	return receipt
}
