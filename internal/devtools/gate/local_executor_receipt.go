package gate

// This file owns the local executor's verified-session boundary.  The receipt
// is intentionally a sealed interface: callers can consume the producer's
// environment material, but cannot manufacture a value which the scheduler or
// the executor would accept as proof.

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

type localExecutorReceiptSeal uint8

const localExecutorReceiptSealed localExecutorReceiptSeal = 1

// LocalWorkloadPassHostContext is static host/toolchain context.  Dynamic CPU
// admission samples are deliberately not included: those are measured by the
// scheduler only after a local MISS is known.
type LocalWorkloadPassHostContext struct {
	Platform               string `json:"platform"`
	GOOS                   string `json:"goos"`
	GOARCH                 string `json:"goarch"`
	GOAMD64                string `json:"goamd64"`
	GOARM64                string `json:"goarm64"`
	CGOEnabled             string `json:"cgo_enabled"`
	GOEXPERIMENT           string `json:"goexperiment"`
	CC                     string `json:"cc"`
	CXX                    string `json:"cxx"`
	SDK                    string `json:"sdk"`
	OSBuild                string `json:"os_build"`
	GoVersion              string `json:"go_version"`
	ToolchainClosureDigest string `json:"toolchain_closure_digest"`
	RunnerSemanticPolicy   string `json:"runner_semantic_policy"`
	RunnerSemanticDigest   string `json:"runner_semantic_digest"`
}

// LocalExecutorSessionReceipt is a verified, producer-owned execution
// material.  The unexported marker is part of the type contract: a package
// outside gate cannot implement this interface or pass a struct literal in its
// place.  Use NewLocalExecutorSessionReceipt to obtain one.
type LocalExecutorSessionReceipt interface {
	localExecutorSessionReceiptMarker()
	IncludesWorkload(GateID) bool
	Environment(GateID) (LocalWorkloadPassEnvironment, error)
	EnvironmentFor(GateID) (LocalWorkloadPassEnvironment, error)
	HostContext() (LocalWorkloadPassHostContext, error)
	HostContextDigest() (string, error)
	Digest() (string, error)
	TrustedGitBinary() (TrustedGitBinary, error)
	TrustedGoBinary() (TrustedGoBinary, error)
	TrustedSelfBinary() (TrustedSelfBinary, error)
	localExecutorReceiptSeal() localExecutorReceiptSeal
	// Reverify rechecks the exact materialized tree and all bound tools/dependency
	// proofs.  It must be called after Restore and tree.Verify, before execution.
	Reverify(materializedRoot string) error
}

// ReverifyLocalExecutorSessionReceiptForLookup revalidates the sealed proof
// needed to derive a local PASS lookup without materializing a source tree.
// It intentionally reads source and lock material only through the receipt's
// captured CandidateObjectAuthority and exact Git tree; filesystem reverify is
// reserved for the materialized MISS execution boundary.
func ReverifyLocalExecutorSessionReceiptForLookup(ctx context.Context, receipt LocalExecutorSessionReceipt, repositoryRoot, exactTreeSHA string) error {
	if ctx == nil {
		return errors.New("local executor receipt pre-lookup reverify context is required")
	}
	if err := validateLocalExecutorSessionReceipt(receipt); err != nil {
		return err
	}
	verified, ok := receipt.(*localExecutorSessionReceipt)
	if !ok {
		return errors.New("local executor receipt pre-lookup reverify requires producer receipt")
	}
	return verified.reverifyForLookup(ctx, repositoryRoot, exactTreeSHA)
}

// LocalExecutorSessionReceiptCandidateObjectAuthority returns the receipt-bound
// private ODB authority for a sibling exact-tree reader. Callers must use it
// only with a gateprivate typed Git command and never re-read ambient routing.
func LocalExecutorSessionReceiptCandidateObjectAuthority(receipt LocalExecutorSessionReceipt) (CandidateObjectAuthority, error) {
	if err := validateLocalExecutorSessionReceipt(receipt); err != nil {
		return CandidateObjectAuthority{}, err
	}
	verified, ok := receipt.(*localExecutorSessionReceipt)
	if !ok {
		return CandidateObjectAuthority{}, errors.New("local executor receipt candidate object authority requires producer receipt")
	}
	if _, err := verified.candidateObjectAuthority.Digest(); err != nil {
		return CandidateObjectAuthority{}, fmt.Errorf("verify local executor receipt candidate object authority: %w", err)
	}
	return verified.candidateObjectAuthority, nil
}

type localExecutorSessionReceipt struct {
	repositoryRoot           string
	exactTreeSHA             string
	workloadIDs              []GateID
	environments             map[GateID]LocalWorkloadPassEnvironment
	host                     LocalWorkloadPassHostContext
	toolPath                 string
	tools                    []localExecutorToolProof
	self                     TrustedSelfBinary
	sources                  []localExecutorSourceProof
	dependencies             []localExecutorDependencyProof
	programs                 map[GateID]localExecutorProgramProof
	candidateObjectAuthority CandidateObjectAuthority
	digest                   string
}

type localExecutorSessionReceiptPayload struct {
	SchemaVersion                  string                            `json:"schema_version"`
	Domain                         string                            `json:"domain"`
	Host                           LocalWorkloadPassHostContext      `json:"host"`
	Tools                          []localExecutorToolPayload        `json:"tools"`
	Sources                        []localExecutorSourcePayload      `json:"sources"`
	Dependencies                   []localExecutorDependencyPayload  `json:"dependencies"`
	Programs                       []localExecutorProgramPayload     `json:"programs"`
	Self                           localExecutorSelfPayload          `json:"self"`
	Environments                   []localExecutorEnvironmentPayload `json:"environments"`
	CandidateObjectAuthorityDigest string                            `json:"candidate_object_authority_digest"`
}

type localExecutorEnvironmentPayload struct {
	WorkloadID  GateID                       `json:"workload_id"`
	Environment LocalWorkloadPassEnvironment `json:"environment"`
}

type localExecutorToolProof struct {
	name    string
	path    string
	digest  string
	version string
	goRoot  string
}

type localExecutorToolPayload struct {
	Name    string `json:"name"`
	Digest  string `json:"digest"`
	Version string `json:"version"`
}

type localExecutorSelfPayload struct {
	Name    string `json:"name"`
	Digest  string `json:"digest"`
	Version string `json:"version"`
}

type localExecutorSourceProof struct {
	path   string
	digest string
}

type localExecutorSourcePayload struct {
	Digest string `json:"digest"`
}

type localExecutorDependencyProof struct {
	name          string
	root          string
	secondaryRoot string
	tertiaryRoot  string
	lockDigest    string
	contentDigest string
	verification  string
	lockFiles     []localExecutorLockedFile
	contentFiles  []localExecutorDependencyContentFile
}

type localExecutorDependencyPayload struct {
	Name          string `json:"name"`
	LockDigest    string `json:"lock_digest"`
	ContentDigest string `json:"content_digest"`
	Verification  string `json:"verification"`
}

type localExecutorLockedFile struct {
	path   string
	digest string
}

// localExecutorDependencyContentFile binds one regular file in the exact
// dependency closure without exposing an absolute host cache path.
type localExecutorDependencyContentFile struct {
	path   string
	digest string
	mode   uint32
}

type localExecutorProgramProof struct {
	id        GateID
	steps     []localExecutorStepProof
	toolNames []string
	path      string
}

type localExecutorProgramPayload struct {
	WorkloadID GateID                     `json:"workload_id"`
	Steps      []localExecutorStepPayload `json:"steps"`
	Tools      []string                   `json:"tools"`
}

type localExecutorStepProof struct {
	directory string
	argv      []string
	binary    string
}

type localExecutorStepPayload struct {
	Directory string   `json:"directory"`
	Argv      []string `json:"argv"`
	Binary    string   `json:"binary"`
}

// NewLocalExecutorSessionReceipt 在 PASS 查询前绑定精确 Git 树、工具和依赖证明，生成不可伪造的本地执行回执。
func NewLocalExecutorSessionReceipt(ctx context.Context, repositoryRoot, exactTreeSHA string, workloadIDs []GateID, dependencies LocalExecutorDependencyInputs) (LocalExecutorSessionReceipt, error) {
	return NewLocalExecutorSessionReceiptWithCandidateObjectAuthority(ctx, repositoryRoot, exactTreeSHA, CandidateObjectAuthority{}, workloadIDs, dependencies)
}

// NewLocalExecutorSessionReceiptWithCandidateObjectAuthority seals a receipt
// using a one-time validated private candidate ODB proof when the exact tree is
// absent from the repository's canonical object database.
func NewLocalExecutorSessionReceiptWithCandidateObjectAuthority(ctx context.Context, repositoryRoot, exactTreeSHA string, authority CandidateObjectAuthority, workloadIDs []GateID, dependencies LocalExecutorDependencyInputs) (LocalExecutorSessionReceipt, error) {
	if ctx == nil {
		return nil, errors.New("local executor receipt context is required")
	}
	canonicalRoot, err := canonicalReceiptRepositoryRoot(repositoryRoot)
	if err != nil {
		return nil, err
	}
	if !validLocalTreeObjectID(exactTreeSHA) {
		return nil, errors.New("local executor receipt exact tree SHA is invalid")
	}
	if len(workloadIDs) == 0 {
		return nil, errors.New("local executor receipt requires workload IDs")
	}
	programs, err := resolveLocalExecutorReceiptPrograms(workloadIDs)
	if err != nil {
		return nil, err
	}
	receipt, err := buildLocalExecutorReceipt(ctx, canonicalRoot, exactTreeSHA, authority, workloadIDs, programs, dependencies)
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

func (receipt *localExecutorSessionReceipt) localExecutorSessionReceiptMarker() {
	// The method is intentionally non-empty so static guards cannot mistake the
	// seal for an accidental no-op capability.
	if receipt == nil {
		return
	}
}

func (receipt *localExecutorSessionReceipt) localExecutorReceiptSeal() localExecutorReceiptSeal {
	if receipt == nil {
		return 0
	}
	return localExecutorReceiptSealed
}

func validateLocalExecutorSessionReceipt(receipt LocalExecutorSessionReceipt) error {
	if receipt == nil || receipt.localExecutorReceiptSeal() != localExecutorReceiptSealed {
		return errors.New("local executor receipt is not producer-sealed")
	}
	return nil
}

// ValidateLocalExecutorSessionReceipt 在其他包依赖 workload 绑定前确认回执来自 gate 生产者。
func ValidateLocalExecutorSessionReceipt(receipt LocalExecutorSessionReceipt) error {
	return validateLocalExecutorSessionReceipt(receipt)
}

// LocalExecutorSessionReceiptIncludesWorkload 显式确认 producer-sealed 回执是否绑定 workload。
// 它与 Environment 查询分离，调用方不得把环境读取失败转化为路由决策。
func LocalExecutorSessionReceiptIncludesWorkload(receipt LocalExecutorSessionReceipt, id GateID) bool {
	return receipt != nil && receipt.localExecutorReceiptSeal() == localExecutorReceiptSealed && receipt.IncludesWorkload(id)
}

// LocalExecutorReceiptBoundWorkloadIDs 选择拥有 canonical executor mapping 的 workload。
// 它保留 mapped-ineligible workload，以便 producer 为 local PASS lookup 封存真实环境；
// 没有 mapping 的 known workload 不得获得 local receipt 或 identity。
func LocalExecutorReceiptBoundWorkloadIDs(workloadIDs []GateID) ([]GateID, error) {
	selected := make([]GateID, 0, len(workloadIDs))
	seen := make(map[GateID]struct{}, len(workloadIDs))
	for index, workloadID := range workloadIDs {
		if workloadID == "" {
			return nil, fmt.Errorf("local executor receipt-bound workload[%d] is empty", index)
		}
		if _, duplicate := seen[workloadID]; duplicate {
			return nil, fmt.Errorf("local executor receipt-bound workload %q is duplicated", workloadID)
		}
		seen[workloadID] = struct{}{}
		eligibility, err := EvaluateLocalWorkloadExecutionEligibility(workloadID)
		if err != nil {
			return nil, fmt.Errorf("local executor receipt-bound workload %q eligibility: %w", workloadID, err)
		}
		if eligibility.Strategy != "" {
			selected = append(selected, workloadID)
		}
	}
	return selected, nil
}

// LocalExecutorExecutionWorkloadIDs 只选择本地会话可以执行的 receipt-bound workload。
// mapped-ineligible ID 必须保持缺席，确保复用的本地 PASS 不会授予执行能力。
func LocalExecutorExecutionWorkloadIDs(workloadIDs []GateID) ([]GateID, error) {
	selected := make([]GateID, 0, len(workloadIDs))
	seen := make(map[GateID]struct{}, len(workloadIDs))
	for index, workloadID := range workloadIDs {
		if workloadID == "" {
			return nil, fmt.Errorf("local executor execution workload[%d] is empty", index)
		}
		if _, duplicate := seen[workloadID]; duplicate {
			return nil, fmt.Errorf("local executor execution workload %q is duplicated", workloadID)
		}
		seen[workloadID] = struct{}{}
		eligibility, err := EvaluateLocalWorkloadExecutionEligibility(workloadID)
		if err != nil {
			return nil, fmt.Errorf("local executor execution workload %q eligibility: %w", workloadID, err)
		}
		if eligibility.Eligible {
			selected = append(selected, workloadID)
		}
	}
	return selected, nil
}

// IncludesWorkload 返回 sealed 回执是否在构造时绑定了 id。
func (receipt *localExecutorSessionReceipt) IncludesWorkload(id GateID) bool {
	if receipt == nil {
		return false
	}
	return slices.Contains(receipt.workloadIDs, id)
}

// Environment 返回指定工作负载绑定的本地 PASS 环境，缺失时立即拒绝。
func (receipt *localExecutorSessionReceipt) Environment(id GateID) (LocalWorkloadPassEnvironment, error) {
	if receipt == nil {
		return LocalWorkloadPassEnvironment{}, errors.New("local executor receipt is nil")
	}
	environment, ok := receipt.environments[id]
	if !ok {
		return LocalWorkloadPassEnvironment{}, fmt.Errorf("local executor receipt workload %q is not present", id)
	}
	if proof, bound := receipt.programs[id]; bound && localReceiptProgramProofRequiresTrustedSelf(proof) {
		if _, err := receipt.TrustedSelfBinary(); err != nil {
			return LocalWorkloadPassEnvironment{}, fmt.Errorf("local executor receipt workload %q trusted self proof: %w", id, err)
		}
	}
	return environment, nil
}

func localReceiptProgramProofRequiresTrustedSelf(proof localExecutorProgramProof) bool {
	for _, step := range proof.steps {
		if len(step.argv) != 0 && step.argv[0] == ExecutorSelfCommandName {
			return true
		}
	}
	return false
}

// EnvironmentFor 保持调用方的环境查询入口，并委托给 Environment。
func (receipt *localExecutorSessionReceipt) EnvironmentFor(id GateID) (LocalWorkloadPassEnvironment, error) {
	return receipt.Environment(id)
}

// HostContext 返回回执冻结的静态主机与工具链上下文。
func (receipt *localExecutorSessionReceipt) HostContext() (LocalWorkloadPassHostContext, error) {
	if receipt == nil {
		return LocalWorkloadPassHostContext{}, errors.New("local executor receipt is nil")
	}
	return receipt.host, nil
}

// HostContextDigest 计算冻结主机上下文的 canonical 摘要。
func (receipt *localExecutorSessionReceipt) HostContextDigest() (string, error) {
	if receipt == nil {
		return "", errors.New("local executor receipt is nil")
	}
	return LocalWorkloadPassHostContextDigest(localReceiptEnvironment(receipt.host, CanonicalGoFlags(false)))
}

// Digest 返回已验证的 receipt 摘要，缺失或格式漂移时拒绝。
func (receipt *localExecutorSessionReceipt) Digest() (string, error) {
	if receipt == nil || !isPrefixedSHA256Digest(receipt.digest) {
		return "", errors.New("local executor receipt digest is unavailable")
	}
	return receipt.digest, nil
}

// TrustedGitBinary 返回回执绑定的 Git 证明，供精确树工作使用。
func (receipt *localExecutorSessionReceipt) TrustedGitBinary() (TrustedGitBinary, error) {
	if receipt == nil {
		return TrustedGitBinary{}, errors.New("local executor receipt is nil")
	}
	trustedGit, err := trustedGitBinaryFromProofs(receipt.tools)
	if err != nil {
		return TrustedGitBinary{}, err
	}
	return trustedGit.withCandidateObjectAuthority(receipt.candidateObjectAuthority)
}

// TrustedGoBinary 返回回执绑定的 Go 证明，供离线验证和每次会话执行使用。
func (receipt *localExecutorSessionReceipt) TrustedGoBinary() (TrustedGoBinary, error) {
	if receipt == nil {
		return TrustedGoBinary{}, errors.New("local executor receipt is nil")
	}
	return trustedGoBinaryFromProofs(receipt.tools)
}

// TrustedSelfBinary 返回回执绑定的 gate 可执行文件证明，仅供 ProjectMap/self 程序使用。
func (receipt *localExecutorSessionReceipt) TrustedSelfBinary() (TrustedSelfBinary, error) {
	if receipt == nil {
		return TrustedSelfBinary{}, errors.New("local executor receipt is nil")
	}
	if _, err := receipt.self.VerifiedPath(); err != nil {
		return TrustedSelfBinary{}, err
	}
	return receipt.self, nil
}

// Reverify 复核物化树及所有已绑定工具、源码和依赖证明，执行前禁止漂移。
func (receipt *localExecutorSessionReceipt) Reverify(materializedRoot string) error {
	if receipt == nil {
		return errors.New("local executor receipt is nil")
	}
	canonicalRoot, err := canonicalReceiptRepositoryRoot(materializedRoot)
	if err != nil {
		return err
	}
	if err := receipt.reverifyMaterializedTree(canonicalRoot); err != nil {
		return err
	}
	if err := reverifyLocalReceiptSources(canonicalRoot, receipt.sources); err != nil {
		return err
	}
	if err := receipt.reverifyTools(); err != nil {
		return err
	}
	if err := reverifyLocalReceiptDependencies(canonicalRoot, receipt.dependencies); err != nil {
		return err
	}
	return nil
}

// reverifyForLookup checks receipt-bound material without ever reading the
// ambient worktree. A local PASS hit must not materialize source; MISS keeps
// Reverify at its existing restore-and-execute boundary.
func (receipt *localExecutorSessionReceipt) reverifyForLookup(ctx context.Context, repositoryRoot, exactTreeSHA string) error {
	if receipt == nil {
		return errors.New("local executor receipt is nil")
	}
	canonicalRoot, err := canonicalReceiptRepositoryRoot(repositoryRoot)
	if err != nil {
		return err
	}
	if !validLocalTreeObjectID(exactTreeSHA) || exactTreeSHA != receipt.exactTreeSHA {
		return errors.New("local executor receipt pre-lookup exact tree drifted")
	}
	trustedGit, err := receipt.prelookupTrustedGit(ctx, canonicalRoot)
	if err != nil {
		return err
	}
	return receipt.reverifyLookupBoundMaterial(ctx, trustedGit, canonicalRoot)
}

func (receipt *localExecutorSessionReceipt) prelookupTrustedGit(ctx context.Context, repositoryRoot string) (TrustedGitBinary, error) {
	if _, err := receipt.candidateObjectAuthority.Digest(); err != nil {
		return TrustedGitBinary{}, fmt.Errorf("reverify local executor receipt candidate object authority: %w", err)
	}
	trustedGit, err := receipt.TrustedGitBinary()
	if err != nil {
		return TrustedGitBinary{}, err
	}
	if _, err := verifyGitTreeObject(ctx, trustedGit, repositoryRoot, receipt.exactTreeSHA); err != nil {
		return TrustedGitBinary{}, fmt.Errorf("reverify local executor receipt exact tree: %w", err)
	}
	return trustedGit, nil
}

func (receipt *localExecutorSessionReceipt) reverifyLookupBoundMaterial(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot string) error {
	if err := reverifyLocalReceiptSourcesForLookup(ctx, trustedGit, repositoryRoot, receipt.exactTreeSHA, receipt.sources); err != nil {
		return err
	}
	if err := receipt.reverifyTools(); err != nil {
		return err
	}
	if err := reverifyLocalReceiptDependenciesForLookup(ctx, trustedGit, repositoryRoot, receipt.exactTreeSHA, receipt.dependencies); err != nil {
		return err
	}
	return nil
}

// reverifyMaterializedTree 复核已物化根仍对应回执冻结的精确 Git 树。
func (receipt *localExecutorSessionReceipt) reverifyMaterializedTree(canonicalRoot string) error {
	trustedGit, err := receipt.TrustedGitBinary()
	if err != nil {
		return err
	}
	tree, err := verifyGitTreeObject(context.Background(), trustedGit, canonicalRoot, receipt.exactTreeSHA)
	if err != nil {
		return fmt.Errorf("reverify local executor materialized tree: %w", err)
	}
	if tree != receipt.exactTreeSHA {
		return errors.New("reverify local executor materialized tree SHA drifted")
	}
	return nil
}

func (receipt *localExecutorSessionReceipt) reverifyTools() error {
	if _, err := receipt.TrustedGoBinary(); err != nil {
		return err
	}
	if _, err := receipt.TrustedSelfBinary(); err != nil {
		return err
	}
	return reverifyLocalReceiptTools(receipt.tools)
}

// receiptTrustedExecutionBinaries 同时取得 session 冻结步骤必须消费的 Go 与 self receipt proof。
func receiptTrustedExecutionBinaries(receipt LocalExecutorSessionReceipt) (TrustedGoBinary, TrustedSelfBinary, error) {
	trustedGo, err := receipt.TrustedGoBinary()
	if err != nil {
		return TrustedGoBinary{}, TrustedSelfBinary{}, err
	}
	trustedSelf, err := receipt.TrustedSelfBinary()
	if err != nil {
		return TrustedGoBinary{}, TrustedSelfBinary{}, err
	}
	return trustedGo, trustedSelf, nil
}
