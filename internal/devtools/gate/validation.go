package gate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

var (
	gitOIDPattern = regexp.MustCompile(`^[0-9a-f]+$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
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

// Validate 校验 GrantRequest 的身份闭包和 audience 专属绑定。
func (r GrantRequest) Validate() error {
	for name, value := range map[string]string{
		"receipt_id": r.ReceiptID, "invocation_id": r.InvocationID, "invocation_owner": r.InvocationOwner,
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
	if err := r.Adapter.Validate(); err != nil {
		return err
	}
	if r.RequestedAt.IsZero() {
		return errors.New("requested_at is required")
	}
	return r.validateAudience()
}

// validateAudience 校验外部动作 audience 的专属远端和 Git ref 字段。
func (r GrantRequest) validateAudience() error {
	switch r.Audience {
	case ActionAudienceGitPush:
		if strings.TrimSpace(r.RemoteURL) == "" || !strings.HasPrefix(r.Ref, "refs/") {
			return errors.New("git.push requires remote_url and full ref")
		}
		if err := validateActionOID("old_sha", r.OldSHA); err != nil {
			return err
		}
		return validateActionOID("new_sha", r.NewSHA)
	case ActionAudienceRelease, ActionAudienceImagePromote:
		return nil
	default:
		return fmt.Errorf("unsupported action audience %q", r.Audience)
	}
}

// Validate 校验单个 gate 的进程结果与摘要闭包。
func (r GateResult) Validate() error {
	if strings.TrimSpace(r.GateID) == "" {
		return errors.New("gate_id is required")
	}
	if r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) {
		return errors.New("gate result timestamps are invalid")
	}
	if err := validateGateExit(r.Status, r.ExitCode); err != nil {
		return err
	}
	if err := validateDigest("argv_digest", r.ArgvDigest); err != nil {
		return err
	}
	return validateDigest("log_digest", r.LogDigest)
}

func validateGateExit(status GateStatus, exitCode int) error {
	switch status {
	case GateStatusPassed:
		if exitCode != 0 {
			return errors.New("passed gate must have zero exit_code")
		}
	case GateStatusFailed:
		if exitCode == 0 {
			return errors.New("failed gate must have non-zero exit_code")
		}
	default:
		return fmt.Errorf("unsupported gate status %q", status)
	}
	return nil
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
	if err := r.validateIdentity(); err != nil {
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
	if err := r.Container.Validate(); err != nil {
		return err
	}
	return r.Signer.Validate()
}

func (r ResultReceipt) validateIdentity() error {
	if r.SchemaVersion != 1 {
		return fmt.Errorf("unsupported result receipt schema_version %d", r.SchemaVersion)
	}
	for name, value := range map[string]string{"receipt_id": r.ReceiptID, "repo_id": r.RepoID, "invocation_id": r.InvocationID, "signature": r.Signature} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if err := r.Source.Validate(); err != nil {
		return fmt.Errorf("receipt source: %w", err)
	}
	return nil
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
	if r.Generation == 0 || r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) || !r.Deadline.After(r.StartedAt) || r.CompletedAt.After(r.Deadline) {
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

// Validate 校验签名 ActionGrant 及其唯一终态时间字段。
func (g ActionGrant) Validate() error {
	if g.SchemaVersion != 1 {
		return fmt.Errorf("unsupported action grant schema_version %d", g.SchemaVersion)
	}
	if strings.TrimSpace(g.GrantID) == "" || strings.TrimSpace(g.Signature) == "" {
		return errors.New("grant_id and signature are required")
	}
	if err := g.Request.Validate(); err != nil {
		return fmt.Errorf("grant request: %w", err)
	}
	if g.IssuedAt.IsZero() || !g.ExpiresAt.After(g.IssuedAt) {
		return errors.New("grant issued_at and expires_at are invalid")
	}
	if err := g.validateState(); err != nil {
		return err
	}
	return g.Signer.Validate()
}

// validateState 将 grant 状态分派到唯一合法的终态时间字段组合。
func (g ActionGrant) validateState() error {
	switch g.State {
	case ActionGrantStateIssued:
		return validateIssuedGrant(g)
	case ActionGrantStateConsumed:
		return validateConsumedGrant(g)
	case ActionGrantStateExpired:
		return validateExpiredGrant(g)
	case ActionGrantStateRevoked:
		return validateRevokedGrant(g)
	default:
		return fmt.Errorf("unsupported action grant state %q", g.State)
	}
}

func validateIssuedGrant(grant ActionGrant) error {
	if grant.ConsumedAt != nil || grant.RevokedAt != nil {
		return errors.New("issued grant cannot have terminal timestamps")
	}
	return nil
}

func validateConsumedGrant(grant ActionGrant) error {
	if grant.ConsumedAt == nil || grant.RevokedAt != nil {
		return errors.New("consumed grant requires only consumed_at")
	}
	return nil
}

func validateExpiredGrant(grant ActionGrant) error {
	if grant.ConsumedAt != nil || grant.RevokedAt != nil {
		return errors.New("expired grant cannot have consumed_at or revoked_at")
	}
	return nil
}

func validateRevokedGrant(grant ActionGrant) error {
	if grant.RevokedAt == nil || grant.ConsumedAt != nil {
		return errors.New("revoked grant requires only revoked_at")
	}
	return nil
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

func validateDigest(name, value string) error {
	if !digestPattern.MatchString(value) {
		return fmt.Errorf("%s must be a canonical sha256 digest", name)
	}
	return nil
}
