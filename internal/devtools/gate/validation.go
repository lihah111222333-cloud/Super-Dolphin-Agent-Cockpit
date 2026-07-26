package gate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

var (
	gitOIDPattern          = regexp.MustCompile(`^[0-9a-f]+$`)
	digestPattern          = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	actionAttemptIDPattern = regexp.MustCompile(`^attempt:v1:[0-9a-f]{64}$`)
)

// Validatable is implemented by every canonical contract with fail-fast validation.
type Validatable interface {
	Validate() error
}

// DecodeStrictJSON 严格解码协议 JSON，并拒绝未知字段、畸形输入和尾随值。
func DecodeStrictJSON(data []byte, target Validatable) error {
	if target == nil || reflect.ValueOf(target).Kind() != reflect.Pointer || reflect.ValueOf(target).IsNil() {
		return errors.New("strict JSON target must be a non-nil pointer")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode strict JSON: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return target.Validate()
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return errors.New("trailing JSON value is not allowed")
}

// JSONFieldNames 从结构体生产类型的 JSON tag 动态枚举字段真值。
func JSONFieldNames(producer reflect.Type) ([]string, error) {
	if producer == nil {
		return nil, errors.New("producer type is required")
	}
	for producer.Kind() == reflect.Pointer {
		producer = producer.Elem()
	}
	if producer.Kind() != reflect.Struct {
		return nil, fmt.Errorf("producer must be a struct, got %s", producer.Kind())
	}
	fields := make([]string, 0, producer.NumField())
	seen := make(map[string]struct{}, producer.NumField())
	for i := 0; i < producer.NumField(); i++ {
		field := producer.Field(i)
		tag, ok := field.Tag.Lookup("json")
		if !ok {
			return nil, fmt.Errorf("producer field %s has no json tag", field.Name)
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			return nil, fmt.Errorf("producer field %s has invalid json tag %q", field.Name, tag)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("duplicate producer json field %q", name)
		}
		seen[name] = struct{}{}
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields, nil
}

// FieldCoverageDiff 计算消费登记缺失字段和已过期字段，并稳定排序输出。
func FieldCoverageDiff(producer, coverage []string) (missing, stale []string) {
	producerSet := stringSet(producer)
	coverageSet := stringSet(coverage)
	for field := range producerSet {
		if _, ok := coverageSet[field]; !ok {
			missing = append(missing, field)
		}
	}
	for field := range coverageSet {
		if _, ok := producerSet[field]; !ok {
			stale = append(stale, field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

// Validate 校验 SourceSpec tagged union 及其 Git 对象类型不变量。
func (s SourceSpec) Validate() error {
	if sourceVariantCount(s) > 1 {
		return errors.New("exactly one source variant is required")
	}
	if _, err := objectFormatOIDLength(s.ObjectFormat); err != nil {
		return err
	}
	if err := validateOID("source_tree_sha", s.SourceTreeSHA, s.ObjectFormat, false); err != nil {
		return err
	}
	switch s.Kind {
	case SourceKindCommit:
		return validateCommitSource(s.Commit, s.ObjectFormat, s.SourceTreeSHA)
	case SourceKindTree:
		return validateTreeSource(s.Tree, s.ObjectFormat, s.SourceTreeSHA)
	case SourceKindRange:
		return validateRangeSource(s.Range, s.ObjectFormat, s.SourceTreeSHA)
	default:
		return fmt.Errorf("unsupported source kind %q", s.Kind)
	}
}

func sourceVariantCount(spec SourceSpec) int {
	count := 0
	for _, present := range []bool{spec.Commit != nil, spec.Tree != nil, spec.Range != nil} {
		if present {
			count++
		}
	}
	return count
}

func validateCommitSource(source *CommitSource, objectFormat GitObjectFormat, sourceTreeSHA string) error {
	if source == nil {
		return errors.New("commit source is required for kind commit")
	}
	if err := validateOID("commit.sha", source.SHA, objectFormat, false); err != nil {
		return err
	}
	if source.SHA == sourceTreeSHA {
		return errors.New("commit sha and source_tree_sha must identify different Git object types")
	}
	return nil
}

func validateTreeSource(source *TreeSource, objectFormat GitObjectFormat, sourceTreeSHA string) error {
	if source == nil {
		return errors.New("tree source is required for kind tree")
	}
	if err := validateOID("tree.sha", source.SHA, objectFormat, false); err != nil {
		return err
	}
	if source.SHA != sourceTreeSHA {
		return errors.New("tree sha must equal source_tree_sha")
	}
	if source.ParentCommitSHA != "" {
		return validateOID("tree.parent_commit_sha", source.ParentCommitSHA, objectFormat, false)
	}
	return nil
}

func validateRangeSource(source *RangeSource, objectFormat GitObjectFormat, sourceTreeSHA string) error {
	if source == nil {
		return errors.New("range source is required for kind range")
	}
	if source.HeadSHA == sourceTreeSHA {
		return errors.New("range head_sha and source_tree_sha must identify different Git object types")
	}
	return source.validate(objectFormat)
}

// validate 校验 range 的 ref、对象类型与更新类型组合。
func (r RangeSource) validate(objectFormat GitObjectFormat) error {
	if err := validateOID("range.head_sha", r.HeadSHA, objectFormat, false); err != nil {
		return err
	}
	if !strings.HasPrefix(r.LocalRef, "refs/") || !strings.HasPrefix(r.RemoteRef, "refs/") {
		return errors.New("range local_ref and remote_ref must be full refs")
	}
	switch r.UpdateKind {
	case UpdateKindCreate:
		return r.validateCreate(objectFormat)
	case UpdateKindFastForward, UpdateKindForce:
		return r.validateExistingRef(objectFormat)
	default:
		return fmt.Errorf("unsupported update kind %q", r.UpdateKind)
	}
}

func (r RangeSource) validateCreate(objectFormat GitObjectFormat) error {
	if r.BaseKind != BaseKindEmptyTree {
		return errors.New("create update requires empty_tree base")
	}
	if r.BaseSHA != "" {
		return errors.New("empty_tree base must not include base_sha")
	}
	zeroOID, err := ZeroOID(objectFormat)
	if err != nil {
		return err
	}
	if r.ObservedRemoteSHA != zeroOID {
		return fmt.Errorf("create update requires %s zero observed_remote_sha", objectFormat)
	}
	return nil
}

func (r RangeSource) validateExistingRef(objectFormat GitObjectFormat) error {
	if r.BaseKind != BaseKindCommit {
		return fmt.Errorf("%s update requires commit base", r.UpdateKind)
	}
	if err := validateOID("range.base_sha", r.BaseSHA, objectFormat, false); err != nil {
		return err
	}
	return validateOID("range.observed_remote_sha", r.ObservedRemoteSHA, objectFormat, false)
}

// Validate 校验完整 OCI 执行身份，不接受 tag 或缺失的平台闭包。
func (i ImageIdentity) Validate() error {
	if strings.TrimSpace(i.Registry) == "" || strings.Contains(i.Registry, "@") {
		return errors.New("image registry is required and must not include a digest")
	}
	for name, digest := range map[string]string{
		"oci_index_digest": i.OCIIndexDigest, "platform_manifest_digest": i.PlatformManifestDigest, "config_digest": i.ConfigDigest,
	} {
		if err := validateDigest(name, digest); err != nil {
			return err
		}
	}
	if len(i.RootFSDiffIDs) == 0 {
		return errors.New("rootfs_diff_ids is required")
	}
	for index, digest := range i.RootFSDiffIDs {
		if err := validateDigest(fmt.Sprintf("rootfs_diff_ids[%d]", index), digest); err != nil {
			return err
		}
	}
	if strings.TrimSpace(i.OS) == "" || strings.TrimSpace(i.Architecture) == "" {
		return errors.New("image os and architecture are required")
	}
	return nil
}

// ContainsImmutableImageReference 仅接受精确不可变引用，或规范 docker.io/library
// 单段仓库对应的 Docker familiar 名称。
func ContainsImmutableImageReference(references []string, repository string, digest string) bool {
	if strings.TrimSpace(repository) != repository || repository == "" || strings.Contains(repository, "@") {
		return false
	}
	if err := validateDigest("image reference digest", digest); err != nil {
		return false
	}
	if slices.Contains(references, repository+"@"+digest) {
		return true
	}
	const dockerHubLibraryPrefix = "docker.io/library/"
	familiar, found := strings.CutPrefix(repository, dockerHubLibraryPrefix)
	return found && familiar != "" && !strings.Contains(familiar, "/") &&
		slices.Contains(references, familiar+"@"+digest)
}

// Validate 校验签名密钥身份和轮换 epoch。
func (s SignerIdentity) Validate() error {
	if strings.TrimSpace(s.KeyID) == "" || s.KeyEpoch == 0 {
		return errors.New("signer key_id and positive key_epoch are required")
	}
	if s.Algorithm != SignatureAlgorithmEd25519 {
		return fmt.Errorf("unsupported signature algorithm %q", s.Algorithm)
	}
	return nil
}

// Validate 校验受信 runner 的二进制、签名者与策略摘要。
func (r TrustedRunnerIdentity) Validate() error {
	if err := validateDigest("runner.binary_digest", r.BinaryDigest); err != nil {
		return err
	}
	if err := r.Signer.Validate(); err != nil {
		return fmt.Errorf("runner signer: %w", err)
	}
	return validateDigest("runner.policy_digest", r.PolicyDigest)
}

// Validate 校验受信 action adapter 身份。
func (a TrustedAdapterIdentity) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("adapter name is required")
	}
	if err := validateDigest("adapter.binary_digest", a.BinaryDigest); err != nil {
		return err
	}
	if err := a.Signer.Validate(); err != nil {
		return fmt.Errorf("adapter signer: %w", err)
	}
	return nil
}

// Validate 校验 accepted image authority 记录的完整身份和签名字段。
func (r AcceptedImageRecord) Validate() error {
	return r.validate(true)
}

// validate 按固定子边界校验 accepted image authority 记录。
func (r AcceptedImageRecord) validate(requireSignature bool) error {
	if r.SchemaVersion != AcceptedImageRecordSchemaVersion {
		return fmt.Errorf("accepted image schema_version %d does not match required %d", r.SchemaVersion, AcceptedImageRecordSchemaVersion)
	}
	if err := r.validateSourceIdentity(); err != nil {
		return err
	}
	if err := r.validateArtifactIdentity(); err != nil {
		return err
	}
	if err := r.validateStateIdentity(); err != nil {
		return err
	}
	if err := r.Signer.Validate(); err != nil {
		return fmt.Errorf("accepted image signer: %w", err)
	}
	if requireSignature && strings.TrimSpace(r.Signature) == "" {
		return errors.New("accepted image signature is required")
	}
	return nil
}

// validateSourceIdentity 校验仓库、ref、commit 与 source tree 身份。
func (r AcceptedImageRecord) validateSourceIdentity() error {
	if strings.TrimSpace(r.RepoID) == "" || strings.TrimSpace(r.RepoID) != r.RepoID {
		return errors.New("accepted image repo_id is required and canonical")
	}
	if !strings.HasPrefix(r.TrustedRef, "refs/") || strings.TrimSpace(r.TrustedRef) != r.TrustedRef {
		return errors.New("accepted image trusted_ref must be a canonical full ref")
	}
	if err := validateNonZeroActionOID("accepted image trusted_commit", r.TrustedCommit); err != nil {
		return err
	}
	if err := validateNonZeroActionOID("accepted image source_tree", r.SourceTree); err != nil {
		return err
	}
	return nil
}

// validateArtifactIdentity 校验策略、输入、OCI 镜像与 runner 闭包。
func (r AcceptedImageRecord) validateArtifactIdentity() error {
	if err := validateDigest("accepted image policy_digest", r.PolicyDigest); err != nil {
		return err
	}
	if err := validateDigest("accepted image image_input_digest", r.ImageInputDigest); err != nil {
		return err
	}
	if err := r.Image.Validate(); err != nil {
		return fmt.Errorf("accepted image identity: %w", err)
	}
	if err := r.Runner.Validate(); err != nil {
		return fmt.Errorf("accepted image runner: %w", err)
	}
	if r.Runner.PolicyDigest != r.PolicyDigest {
		return errors.New("accepted image runner policy_digest does not match record policy_digest")
	}
	return nil
}

// validateStateIdentity 校验 generation、前驱摘要和接受时间。
func (r AcceptedImageRecord) validateStateIdentity() error {
	if r.Generation == 0 {
		return errors.New("accepted image generation must be positive")
	}
	if r.PreviousRecordDigest != "" {
		if err := validateDigest("accepted image previous_record_digest", r.PreviousRecordDigest); err != nil {
			return err
		}
	}
	if r.AcceptedAt.IsZero() || r.AcceptedAt.Location() != time.UTC {
		return errors.New("accepted image accepted_at must be a non-zero UTC timestamp")
	}
	return nil
}

// Validate 校验 promotion CAS 包装及其下一份已签记录。
func (r PromotionRecord) Validate() error {
	if r.SchemaVersion != PromotionRecordSchemaVersion {
		return fmt.Errorf("promotion schema_version %d does not match required %d", r.SchemaVersion, PromotionRecordSchemaVersion)
	}
	if err := validateDigest("promotion expected_record_digest", r.ExpectedRecordDigest); err != nil {
		return err
	}
	if r.ExpectedGeneration == 0 {
		return errors.New("promotion expected_generation must be positive")
	}
	if err := r.Next.Validate(); err != nil {
		return fmt.Errorf("promotion next record: %w", err)
	}
	if r.Next.PreviousRecordDigest != r.ExpectedRecordDigest {
		return errors.New("promotion next previous_record_digest does not match expected_record_digest")
	}
	return nil
}

// AcceptedImageSigningPayload 返回排除 signature 值的 canonical 签名输入。
func AcceptedImageSigningPayload(record AcceptedImageRecord) ([]byte, error) {
	unsigned := record
	unsigned.Signature = ""
	if err := unsigned.validate(false); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("marshal accepted image signing payload: %w", err)
	}
	return payload, nil
}

// AcceptedImageRecordDigest 返回覆盖完整已签记录的 canonical sha256 digest。
func AcceptedImageRecordDigest(record AcceptedImageRecord) (string, error) {
	if err := record.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("marshal accepted image record: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// Validate 校验 GrantRequest 的身份闭包和 audience 专属绑定。
func (r GrantRequest) Validate() error {
	for name, value := range map[string]string{
		"receipt_id": r.ReceiptID, "repo_id": r.RepoID, "invocation_id": r.InvocationID,
		"invocation_owner": r.InvocationOwner, "source_tree_sha": r.SourceTreeSHA,
		"subscriber_capability": r.SubscriberCapability, "process_challenge": r.ProcessChallenge,
		"action_policy": r.ActionPolicy, "request_nonce": r.RequestNonce,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if err := validateDigest("receipt_digest", r.ReceiptDigest); err != nil {
		return err
	}
	if err := ValidateActionAttemptID(r.ActionAttemptID); err != nil {
		return err
	}
	if err := r.Adapter.Validate(); err != nil {
		return err
	}
	if r.Generation == 0 {
		return errors.New("generation is required")
	}
	if r.RequestedAt.IsZero() || !r.ExpiresAt.After(r.RequestedAt) {
		return errors.New("requested_at and expires_at are invalid")
	}
	return r.validateAudience()
}

// validateAudience 校验外部动作 audience 的专属远端和 Git ref 字段。
func (r GrantRequest) validateAudience() error {
	switch r.Audience {
	case ActionAudienceGitPush:
		return r.validateGitPushAudience()
	case ActionAudienceRelease:
		return r.validateReleaseBinding()
	case ActionAudienceImagePromote:
		return r.validateImagePromoteAudience()
	default:
		return fmt.Errorf("unsupported action audience %q", r.Audience)
	}
}

func (r GrantRequest) validateGitPushAudience() error {
	if r.hasReleaseBinding() {
		return errors.New("git.push cannot carry release binding")
	}
	if strings.TrimSpace(r.RemoteURL) == "" || !strings.HasPrefix(r.Ref, "refs/") {
		return errors.New("git.push requires remote_url and full ref")
	}
	if err := validateActionOID("old_sha", r.OldSHA); err != nil {
		return err
	}
	return validateActionOID("new_sha", r.NewSHA)
}

func (r GrantRequest) validateImagePromoteAudience() error {
	if r.hasReleaseBinding() {
		return errors.New("image.promote cannot carry release binding")
	}
	return nil
}

func (r GrantRequest) hasReleaseBinding() bool {
	return r.ReleaseRepository != "" || r.ReleaseTag != "" || r.ReleaseCommitSHA != "" || len(r.ReleaseAssets) != 0
}

// Validate 校验证据类型与不可变摘要。
func (e Evidence) Validate() error {
	switch e.Kind {
	case EvidenceKindProcess, EvidenceKindDocker, EvidenceKindLog:
	default:
		return fmt.Errorf("unsupported evidence kind %q", e.Kind)
	}
	return validateDigest("evidence.digest", e.Digest)
}

// Validate 校验容器身份、隔离配置摘要与销毁证明。
func (e ContainerEvidence) Validate() error {
	if strings.TrimSpace(e.ContainerID) == "" || strings.TrimSpace(e.NetworkID) == "" {
		return errors.New("container_id and network_id are required")
	}
	if err := validateDigest("host_config_digest", e.HostConfigDigest); err != nil {
		return err
	}
	resourceDigest, err := e.ResourceWitness.Digest()
	if err != nil {
		return fmt.Errorf("resource_witness: %w", err)
	}
	if err := validateDigest("resource_witness_digest", e.ResourceWitnessDigest); err != nil {
		return err
	}
	if resourceDigest != e.ResourceWitnessDigest {
		return errors.New("resource_witness_digest does not match resource_witness")
	}
	if err := validateDigest("network_policy_digest", e.NetworkPolicyDigest); err != nil {
		return err
	}
	if !e.Removed || !e.NetworkRemoved {
		return errors.New("container and network removal proof are required")
	}
	return nil
}

// Validate 校验签名 ResultReceipt 及其完整执行证据闭包。
func (r ResultReceipt) Validate() error {
	return r.validate(true, nil)
}

// ValidateStored 按历史计划校验终态回执，不把旧 registry 当作当前可执行策略。
func (r ResultReceipt) ValidateStored(plan GatePlan) error {
	if err := plan.ValidateStored(); err != nil {
		return err
	}
	if r.PlanDigest != plan.PlanDigest || r.PolicyDigest != plan.PolicyDigest || !reflect.DeepEqual(r.Source, plan.Source) {
		return errors.New("stored result receipt drifted from its historical plan")
	}
	return r.validate(true, &plan)
}

// validate 按签名要求校验 ResultReceipt 的完整字段和执行证据闭包。
func (r ResultReceipt) validate(requireSignature bool, storedPlan *GatePlan) error {
	if err := r.validateIdentity(requireSignature, storedPlan != nil); err != nil {
		return err
	}
	if err := r.validateExecutionIdentity(); err != nil {
		return err
	}
	if err := r.validateTimeline(); err != nil {
		return err
	}
	if err := r.validateResults(); err != nil {
		return err
	}
	var shardErr error
	if storedPlan == nil {
		shardErr = r.validatePassedShardReceipt()
	} else {
		shardErr = r.validateStoredPassedShardReceipt(*storedPlan)
	}
	if shardErr != nil {
		return shardErr
	}
	if err := r.Container.Validate(); err != nil {
		return err
	}
	return r.Signer.Validate()
}

// validateIdentity 校验 receipt schema、主身份、source 与可选签名字段。
func (r ResultReceipt) validateIdentity(requireSignature bool, allowLegacy bool) error {
	if r.SchemaVersion != ResultReceiptSchemaVersion && !(allowLegacy && r.SchemaVersion == legacyResultReceiptSchemaVersion) {
		return fmt.Errorf("unsupported result receipt schema_version %d", r.SchemaVersion)
	}
	if r.Status != ResultStatusPassed {
		return fmt.Errorf("result receipt schema only signs passed status, got %q", r.Status)
	}
	values := map[string]string{
		"receipt_id": r.ReceiptID, "repo_id": r.RepoID, "invocation_id": r.InvocationID,
		"entrypoint": string(r.Entrypoint), "authority_owner": string(r.AuthorityOwner),
	}
	if requireSignature {
		values["signature"] = r.Signature
	}
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if err := r.Source.Validate(); err != nil {
		return fmt.Errorf("receipt source: %w", err)
	}
	return validateResultReceiptAuthority(r)
}

func (r ResultReceipt) validateExecutionIdentity() error {
	for name, digest := range map[string]string{"plan_digest": r.PlanDigest, "policy_digest": r.PolicyDigest} {
		if err := validateDigest(name, digest); err != nil {
			return err
		}
	}
	if err := r.Runner.Validate(); err != nil {
		return err
	}
	if err := r.Image.Validate(); err != nil {
		return err
	}
	return nil
}

// validateTimeline 校验 receipt generation、截止时间与终态枚举。
func (r ResultReceipt) validateTimeline() error {
	if r.Generation == 0 || r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) || !r.Deadline.After(r.StartedAt) {
		return errors.New("receipt generation or timestamps are invalid")
	}
	switch r.Status {
	case ResultStatusPassed, ResultStatusFailed, ResultStatusCancelled, ResultStatusTimeout, ResultStatusInfraFailed, ResultStatusPassedStalePolicy:
	default:
		return fmt.Errorf("unsupported result status %q", r.Status)
	}
	return nil
}

// validateResults 校验逐 gate 结果和 evidence 闭包。
func (r ResultReceipt) validateResults() error {
	if len(r.GateResults) == 0 || len(r.Evidence) == 0 {
		return errors.New("gate_results and evidence are required")
	}
	for index := range r.GateResults {
		if err := r.GateResults[index].Validate(); err != nil {
			return fmt.Errorf("gate_results[%d]: %w", index, err)
		}
	}
	for index := range r.Evidence {
		if err := r.Evidence[index].Validate(); err != nil {
			return fmt.Errorf("evidence[%d]: %w", index, err)
		}
	}
	return nil
}

// ResultReceiptSigningPayload 返回排除 signature 值后的规范 JSON 签名载荷。
func ResultReceiptSigningPayload(receipt ResultReceipt) ([]byte, error) {
	unsigned := receipt
	unsigned.Signature = ""
	if err := unsigned.validate(false, nil); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("marshal result receipt signing payload: %w", err)
	}
	return payload, nil
}

// VerifyResultReceipt 使用真实 Ed25519 公钥校验完整规范回执。
func VerifyResultReceipt(receipt ResultReceipt, publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("result receipt public key must be Ed25519")
	}
	if err := receipt.Validate(); err != nil {
		return err
	}
	payload, err := ResultReceiptSigningPayload(receipt)
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(receipt.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("result receipt signature must be canonical base64 Ed25519")
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return errors.New("result receipt Ed25519 signature verification failed")
	}
	return nil
}

// ResultReceiptDigest 返回完整校验后签名回执的规范摘要。
func ResultReceiptDigest(receipt ResultReceipt) (string, error) {
	if err := receipt.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return "", fmt.Errorf("marshal result receipt: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// ZeroOID 返回指定 Git object format 的 canonical zero OID。
func ZeroOID(objectFormat GitObjectFormat) (string, error) {
	length, err := objectFormatOIDLength(objectFormat)
	if err != nil {
		return "", err
	}
	return strings.Repeat("0", length), nil
}

func objectFormatOIDLength(objectFormat GitObjectFormat) (int, error) {
	switch objectFormat {
	case GitObjectFormatSHA1:
		return 40, nil
	case GitObjectFormatSHA256:
		return 64, nil
	default:
		return 0, fmt.Errorf("unsupported git object format %q", objectFormat)
	}
}

// validateOID 按仓库 object format 校验 OID 字符集、长度和 zero 约束。
func validateOID(name, value string, objectFormat GitObjectFormat, allowZero bool) error {
	length, err := objectFormatOIDLength(objectFormat)
	if err != nil {
		return err
	}
	if len(value) != length || !gitOIDPattern.MatchString(value) {
		return fmt.Errorf("%s must be a lowercase %d-character %s Git OID", name, length, objectFormat)
	}
	zeroOID, err := ZeroOID(objectFormat)
	if err != nil {
		return err
	}
	if !allowZero && value == zeroOID {
		return fmt.Errorf("%s must not be the zero OID", name)
	}
	return nil
}

func validateActionOID(name, value string) error {
	for _, objectFormat := range []GitObjectFormat{GitObjectFormatSHA1, GitObjectFormatSHA256} {
		if validateOID(name, value, objectFormat, true) == nil {
			return nil
		}
	}
	return fmt.Errorf("%s must be a lowercase SHA-1 or SHA-256 Git OID", name)
}

func validateNonZeroActionOID(name, value string) error {
	for _, objectFormat := range []GitObjectFormat{GitObjectFormatSHA1, GitObjectFormatSHA256} {
		if validateOID(name, value, objectFormat, false) == nil {
			return nil
		}
	}
	return fmt.Errorf("%s must be a non-zero lowercase SHA-1 or SHA-256 Git OID", name)
}

func validateDigest(name, value string) error {
	if !digestPattern.MatchString(value) {
		return fmt.Errorf("%s must be a canonical sha256 digest", name)
	}
	return nil
}

// ValidateActionAttemptID 拒绝不符合高熵 pre-push attempt 格式的身份。
func ValidateActionAttemptID(value string) error {
	if !actionAttemptIDPattern.MatchString(value) {
		return errors.New("action_attempt_id must be attempt:v1 followed by 32 lowercase hex bytes")
	}
	return nil
}
