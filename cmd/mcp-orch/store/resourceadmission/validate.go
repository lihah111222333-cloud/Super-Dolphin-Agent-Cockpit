package resourceadmission

import (
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"
)

// Validate 拒绝任一缺失或非正数的RESOURCE v1维度。
func (v ResourceVector) Validate() error {
	for _, value := range resourceVectorValues(v) {
		if value <= 0 {
			return ErrPolicyIncomplete
		}
	}
	return nil
}

// FitsWithin 判断完整requested vector是否逐维不超过effective limits。
func (v ResourceVector) FitsWithin(limit ResourceVector) bool {
	if v.Validate() != nil || limit.Validate() != nil {
		return false
	}
	requested, effective := resourceVectorValues(v), resourceVectorValues(limit)
	for index := range requested {
		if requested[index] > effective[index] {
			return false
		}
	}
	return true
}

// resourceVectorValues 以固定顺序返回关闭的十维集合。
func resourceVectorValues(vector ResourceVector) [10]int64 {
	return [10]int64{
		vector.AgentCount, vector.InFlightCount, vector.FanoutCount, vector.ConcurrencyCount, vector.WallTimeMS,
		vector.InputTokens, vector.OutputTokens, vector.CostMicrounits, vector.ContentBytes, vector.IngressBytes,
	}
}

// Validate 校验policy applicability摘要输入。
func (i policyApplicabilityInput) Validate() error {
	return validateApplicabilityInput(i.SchemaVersion, i.RequestedKind, i.CallerPrincipalRef, i.ScopeRef)
}

// Validate 校验完整policy发布请求及其派生摘要。
func (r PublishEffectivePolicyRequest) Validate() error {
	if err := r.validateShape(true); err != nil {
		return err
	}
	wantRequest, err := ComputePublishRequestDigest(r)
	if err != nil || wantRequest != r.PublishRequestDigest {
		return ErrPolicyIncomplete
	}
	wantOperation, err := ComputePublishOperationDigest(r)
	if err != nil || wantOperation != r.PublishOperationDigest {
		return ErrPolicyIncomplete
	}
	return nil
}

// validateShape 校验policy请求的持久字段，按需忽略重试candidate身份。
func (r PublishEffectivePolicyRequest) validateShape(requireCandidates bool) error {
	if err := validatePublishHeader(r, requireCandidates); err != nil {
		return err
	}
	if err := validatePublishTextFields(r); err != nil {
		return err
	}
	if err := validatePublishDigestFields(r); err != nil {
		return err
	}
	if r.EffectiveLimits.Validate() != nil || r.PolicyOwner.Validate() != nil {
		return ErrPolicyIncomplete
	}
	if !validTime(r.EffectiveAt) || !validTime(r.ValidUntil) || !r.ValidUntil.After(r.EffectiveAt) {
		return ErrPolicyIncomplete
	}
	return nil
}

// validatePublishHeader 校验policy版本、revision、applicability和candidate身份。
func validatePublishHeader(request PublishEffectivePolicyRequest, requireCandidates bool) error {
	if request.SchemaVersion != SchemaVersion || request.Revision <= 0 {
		return ErrPolicyIncomplete
	}
	if err := validateApplicabilityInput(request.SchemaVersion, request.RequestedKind, request.CallerPrincipalRef, request.ScopeRef); err != nil {
		return err
	}
	if requireCandidates && (!validText(request.SnapshotID, 128) || !validText(request.PublishReceiptRef, 512)) {
		return ErrPolicyIncomplete
	}
	return nil
}

// validatePublishTextFields 校验policy发布的所有规范文本引用。
func validatePublishTextFields(request PublishEffectivePolicyRequest) error {
	fields := []boundedText{
		{request.LineageID, 128}, {request.SourcePolicySetRef, 512}, {request.PrecedenceTraceRef, 512},
		{request.IdempotencyNamespace, 256}, {request.PublishOperationID, 128},
	}
	return validateTexts(ErrPolicyIncomplete, fields)
}

// validatePublishDigestFields 校验policy摘要并重算applicability与limits摘要。
func validatePublishDigestFields(request PublishEffectivePolicyRequest) error {
	if err := validateDigests(ErrPolicyIncomplete, []string{
		request.IdempotencyKeyDigest, request.SourcePolicySetDigest,
		request.PrecedenceTraceDigest, request.ReleaseConditionDigest,
	}); err != nil {
		return err
	}
	applicability, err := ComputePolicyApplicabilityDigest(request)
	if err != nil || applicability != request.PolicyApplicabilityDigest {
		return ErrPolicyIncomplete
	}
	limits, err := ComputeResourceVectorDigest(request.EffectiveLimits)
	if err != nil || limits != request.EffectiveLimitsDigest {
		return ErrPolicyIncomplete
	}
	canonical, err := ComputeCanonicalPolicyHash(request)
	if err != nil || canonical != request.CanonicalPolicyHash {
		return ErrPolicyIncomplete
	}
	return nil
}

// validateCanonicalPolicyInput 校验canonical hash覆盖的全部typed policy字段。
func validateCanonicalPolicyInput(request PublishEffectivePolicyRequest) error {
	if request.SchemaVersion != SchemaVersion || request.Revision <= 0 {
		return ErrPolicyIncomplete
	}
	if err := validateApplicabilityInput(request.SchemaVersion, request.RequestedKind, request.CallerPrincipalRef, request.ScopeRef); err != nil {
		return err
	}
	if err := validatePublishTextFields(request); err != nil {
		return err
	}
	if err := validateDigests(ErrPolicyIncomplete, []string{
		request.PolicyApplicabilityDigest, request.SourcePolicySetDigest, request.PrecedenceTraceDigest,
		request.EffectiveLimitsDigest, request.ReleaseConditionDigest,
	}); err != nil {
		return err
	}
	if request.EffectiveLimits.Validate() != nil || !validTime(request.EffectiveAt) ||
		!validTime(request.ValidUntil) || !request.ValidUntil.After(request.EffectiveAt) {
		return ErrPolicyIncomplete
	}
	return nil
}

// Validate 校验schema v133持久化的policy owner fence。
func (f PolicyOwnerFence) Validate() error {
	if f.OwnerGeneration <= 0 || f.SecurityTimeGeneration <= 0 {
		return ErrWriterFence
	}
	return validateTexts(ErrWriterFence, []boundedText{
		{f.OwnerID, 256}, {f.WriterFenceRef, 512}, {f.OwnerProofRef, 512}, {f.DeploymentFenceRef, 512},
	})
}

// Validate 校验schema v133持久化的admission owner fence。
func (f AdmissionOwnerFence) Validate() error {
	if f.OwnerGeneration <= 0 || f.SecurityTimeGeneration <= 0 {
		return ErrWriterFence
	}
	return validateTexts(ErrWriterFence, []boundedText{
		{f.OwnerID, 256}, {f.WriterFenceRef, 512}, {f.DeploymentFenceRef, 512}, {f.OwnerProofRef, 512},
	})
}

// Validate 校验完整不可变effective-policy projection及两级摘要。
func (s PolicySnapshot) Validate() error {
	if err := s.validateShape(true); err != nil {
		return err
	}
	wantSnapshot, err := ComputePolicySnapshotDigest(s)
	if err != nil || wantSnapshot != s.PolicySnapshotDigest {
		return ErrSnapshotCorrupt
	}
	wantReceipt, err := ComputePolicyPublishReceiptDigest(s)
	if err != nil || wantReceipt != s.PublishReceiptDigest {
		return ErrSnapshotCorrupt
	}
	return nil
}

// validateShape 校验policy projection并按需忽略两级self-digest。
func (s PolicySnapshot) validateShape(requireDigests bool) error {
	if err := s.publishRequest().Validate(); err != nil {
		return ErrSnapshotCorrupt
	}
	if !validPolicySnapshotDiscriminators(s) || !validPolicySnapshotTimes(s) {
		return ErrSnapshotCorrupt
	}
	if requireDigests && !validPolicySnapshotDigests(s) {
		return ErrSnapshotCorrupt
	}
	return nil
}

// validPolicySnapshotDiscriminators 校验policy projection固定判别字段。
func validPolicySnapshotDiscriminators(snapshot PolicySnapshot) bool {
	return snapshot.DimensionSet == ResourceDimensionSet && snapshot.CanonicalizationProfile == PolicyCanonicalizationProfile
}

// validPolicySnapshotTimes 校验policy projection持久时间关系。
func validPolicySnapshotTimes(snapshot PolicySnapshot) bool {
	return validTime(snapshot.CreatedAt) && !snapshot.EffectiveAt.After(snapshot.CreatedAt) &&
		snapshot.ValidUntil.After(snapshot.CreatedAt)
}

// validPolicySnapshotDigests 校验policy projection两级self-digest形状。
func validPolicySnapshotDigests(snapshot PolicySnapshot) bool {
	return validDigest(snapshot.PolicySnapshotDigest) && validDigest(snapshot.PublishReceiptDigest)
}

// publishRequest 从持久policy projection无损重建发布请求。
func (s PolicySnapshot) publishRequest() PublishEffectivePolicyRequest {
	return PublishEffectivePolicyRequest{
		SchemaVersion: s.SchemaVersion, SnapshotID: s.SnapshotID, LineageID: s.LineageID, Revision: s.Revision,
		RequestedKind: s.RequestedKind, CallerPrincipalRef: s.CallerPrincipalRef, ScopeRef: s.ScopeRef,
		PolicyApplicabilityDigest: s.PolicyApplicabilityDigest, SourcePolicySetRef: s.SourcePolicySetRef,
		SourcePolicySetDigest: s.SourcePolicySetDigest, PrecedenceTraceRef: s.PrecedenceTraceRef,
		PrecedenceTraceDigest: s.PrecedenceTraceDigest, EffectiveLimits: s.EffectiveLimits,
		EffectiveLimitsDigest: s.EffectiveLimitsDigest, CanonicalPolicyHash: s.CanonicalPolicyHash,
		ReleaseConditionDigest: s.ReleaseConditionDigest,
		IdempotencyNamespace:   s.IdempotencyNamespace, IdempotencyKeyDigest: s.IdempotencyKeyDigest,
		PublishRequestDigest: s.PublishRequestDigest, PublishOperationID: s.PublishOperationID,
		PublishOperationDigest: s.PublishOperationDigest, PublishReceiptRef: s.PublishReceiptRef,
		PolicyOwner: s.PolicyOwner, EffectiveAt: s.EffectiveAt, ValidUntil: s.ValidUntil,
	}
}

// validateReceiptInput 校验policy receipt摘要所需的完整输入。
func (s PolicySnapshot) validateReceiptInput() error {
	if !validText(s.PublishReceiptRef, 512) || !validTime(s.CreatedAt) || s.PolicyOwner.Validate() != nil {
		return ErrSnapshotCorrupt
	}
	return validateDigests(ErrSnapshotCorrupt, []string{
		s.PublishOperationDigest, s.PublishRequestDigest, s.PolicySnapshotDigest,
	})
}

// Validate 校验stable reservation请求及request/operation两级摘要。
func (r ReserveRequest) Validate() error {
	if err := r.validateShape(true); err != nil {
		return err
	}
	wantRequest, err := ComputeReservationRequestDigest(r)
	if err != nil || wantRequest != r.ReservationRequestDigest {
		return ErrInvalidRequest
	}
	wantOperation, err := ComputeReservationOperationDigest(r)
	if err != nil || wantOperation != r.ReservationOperationDigest {
		return ErrInvalidRequest
	}
	return nil
}

// validateShape 校验reservation请求并按需忽略重试candidate身份。
func (r ReserveRequest) validateShape(requireCandidates bool) error {
	if r.SchemaVersion != SchemaVersion || r.CommandVersion != 1 {
		return ErrInvalidRequest
	}
	if requireCandidates && !validReserveCandidateIDs(r) {
		return ErrInvalidRequest
	}
	if err := validateTexts(ErrInvalidRequest, []boundedText{
		{r.CommandID, 128}, {r.IdempotencyNamespace, 256}, {r.ReservationOperationID, 128},
	}); err != nil {
		return err
	}
	if err := validateDigests(ErrInvalidRequest, []string{
		r.CommandProfileDigest, r.IdentityFenceDigest, r.ContentAdmissionSnapshotDigest, r.IdempotencyKeyDigest,
	}); err != nil {
		return err
	}
	if r.Requested.Validate() != nil || r.AdmissionOwner.Validate() != nil {
		return ErrInvalidRequest
	}
	return nil
}

// Validate 校验proof-chain lookup且不接受owner-selected ID。
func (r ResolveReservedSnapshotRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion || r.CommandVersion != 1 || !validText(r.CommandID, 128) {
		return ErrInvalidRequest
	}
	return validateDigests(ErrInvalidRequest, []string{
		r.CommandProfileDigest, r.IdentityFenceDigest, r.ContentAdmissionSnapshotDigest,
	})
}

// Validate 校验完整reserved projection及snapshot/receipt两级摘要。
func (s ReservationSnapshot) Validate() error {
	if err := s.validateShape(true); err != nil {
		return err
	}
	wantSnapshot, err := ComputeResourceSnapshotDigest(s)
	if err != nil || wantSnapshot != s.ResourceSnapshotDigest {
		return ErrSnapshotCorrupt
	}
	wantReceipt, err := ComputeReservationReceiptDigest(s)
	if err != nil || wantReceipt != s.ReservationReceiptDigest {
		return ErrSnapshotCorrupt
	}
	return nil
}

// validateShape 校验reserved projection并按需忽略两级self-digest。
func (s ReservationSnapshot) validateShape(requireDigests bool) error {
	if err := validateReservationHeader(s); err != nil {
		return err
	}
	if err := validateReservationRelations(s); err != nil {
		return err
	}
	if err := validateReservationLimitsAndTime(s); err != nil {
		return err
	}
	if requireDigests && (!validDigest(s.ResourceSnapshotDigest) || !validDigest(s.ReservationReceiptDigest)) {
		return ErrSnapshotCorrupt
	}
	return nil
}

// validateReservationHeader 校验reservation固定版本、状态与empty-ledger身份。
func validateReservationHeader(snapshot ReservationSnapshot) error {
	if snapshot.SchemaVersion != SchemaVersion || snapshot.ReservationVersion != ReservationVersion ||
		snapshot.CommandVersion != 1 || snapshot.ResourceStatus != ResourceStatusReserved ||
		snapshot.RequestedKind != RequestedKindSendMessage || snapshot.PolicyRevision <= 0 {
		return ErrSnapshotCorrupt
	}
	if snapshot.UsageDigestDomain != EmptyUsageLedgerDomain || snapshot.UsageWatermark != EmptyUsageWatermark ||
		snapshot.UsageDigest != EmptyUsageLedgerDigest {
		return ErrSnapshotCorrupt
	}
	return nil
}

// validateReservationRelations 校验reservation文本、摘要与三组authority anchor。
func validateReservationRelations(snapshot ReservationSnapshot) error {
	if err := validateReservationTextFields(snapshot); err != nil {
		return err
	}
	if err := validateReservationDigestFields(snapshot); err != nil {
		return err
	}
	if snapshot.Source.Validate() != nil || snapshot.Target.Validate() != nil || snapshot.Content.Validate() != nil {
		return ErrSnapshotCorrupt
	}
	return nil
}

// validateReservationLimitsAndTime 校验closed vectors、policy digest与有效期。
func validateReservationLimitsAndTime(snapshot ReservationSnapshot) error {
	if snapshot.Requested.Validate() != nil || snapshot.EffectiveLimits.Validate() != nil ||
		!snapshot.Requested.FitsWithin(snapshot.EffectiveLimits) {
		return ErrSnapshotCorrupt
	}
	limits, err := ComputeResourceVectorDigest(snapshot.EffectiveLimits)
	if err != nil || limits != snapshot.EffectiveLimitsDigest || snapshot.AdmissionOwner.Validate() != nil {
		return ErrSnapshotCorrupt
	}
	if !validTime(snapshot.ValidUntil) || !validTime(snapshot.CreatedAt) || !snapshot.ValidUntil.After(snapshot.CreatedAt) {
		return ErrSnapshotCorrupt
	}
	return nil
}

// validateReservationTextFields 校验reservation持久文本字段。
func validateReservationTextFields(snapshot ReservationSnapshot) error {
	return validateTexts(ErrSnapshotCorrupt, []boundedText{
		{snapshot.ReservationID, 128}, {snapshot.ResourceSnapshotID, 128}, {snapshot.CommandID, 128},
		{snapshot.CallerPrincipalRef, 512}, {snapshot.ScopeRef, 512}, {snapshot.PolicySnapshotID, 128},
		{snapshot.IdempotencyNamespace, 256}, {snapshot.ReservationOperationID, 128},
		{snapshot.ReservationReceiptRef, 512},
	})
}

// validateReservationDigestFields 校验reservation持久摘要字段。
func validateReservationDigestFields(snapshot ReservationSnapshot) error {
	return validateDigests(ErrSnapshotCorrupt, []string{
		snapshot.CommandProfileDigest, snapshot.IdentityFenceDigest, snapshot.Content.AdmissionSnapshotDigest,
		snapshot.PolicySnapshotDigest, snapshot.PolicyCanonicalHash, snapshot.EffectiveLimitsDigest,
		snapshot.ReleaseConditionDigest,
		snapshot.IdempotencyKeyDigest, snapshot.ReservationRequestDigest, snapshot.ReservationOperationDigest,
		snapshot.UsageDigest,
	})
}

// Validate 校验持久current-binding anchor。
func (a BindingAnchor) Validate() error {
	if a.BindingVersion <= 0 || !validText(a.AgentID, 128) || !validText(a.FenceRef, 512) {
		return ErrIdentityFence
	}
	return nil
}

// Validate 校验完整持久CONTENT anchor。
func (a ContentAnchor) Validate() error {
	if a.ObjectVersion != 1 || a.RefVersion != 1 {
		return ErrContentMismatch
	}
	if err := validateTexts(ErrContentMismatch, []boundedText{
		{a.ReferenceID, 128}, {a.ContentRef, 512}, {a.ObjectID, 128},
	}); err != nil {
		return err
	}
	return validateDigests(ErrContentMismatch, []string{a.OwnerSnapshotDigest, a.AdmissionSnapshotDigest})
}

// validateReceiptInput 校验reservation receipt摘要所需的完整输入。
func (s ReservationSnapshot) validateReceiptInput() error {
	if !validText(s.ReservationReceiptRef, 512) || !validTime(s.CreatedAt) || s.AdmissionOwner.Validate() != nil {
		return ErrSnapshotCorrupt
	}
	return validateDigests(ErrSnapshotCorrupt, []string{
		s.ReservationOperationDigest, s.ReservationRequestDigest, s.ResourceSnapshotDigest,
	})
}

// validateApplicabilityInput 校验固定send_message policy selector。
func validateApplicabilityInput(version int64, kind, principal, scope string) error {
	if version != SchemaVersion || kind != RequestedKindSendMessage ||
		!validText(principal, 512) || !validText(scope, 512) {
		return ErrPolicyIncomplete
	}
	return nil
}

type boundedText struct {
	value string
	limit int
}

// validateTexts 校验一组有界规范UTF-8文本。
func validateTexts(base error, fields []boundedText) error {
	for _, field := range fields {
		if !validText(field.value, field.limit) {
			return base
		}
	}
	return nil
}

// validateDigests 校验一组小写SHA-256十六进制摘要。
func validateDigests(base error, digests []string) error {
	for _, digest := range digests {
		if !validDigest(digest) {
			return base
		}
	}
	return nil
}

// validReserveCandidateIDs 校验只在fresh请求中使用的候选身份。
func validReserveCandidateIDs(request ReserveRequest) bool {
	return validText(request.ReservationID, 128) && validText(request.ResourceSnapshotID, 128) &&
		validText(request.ReservationReceiptRef, 512)
}

// validText 校验单个有界规范UTF-8文本。
func validText(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.TrimSpace(value) == value &&
		utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

// validDigest 校验单个小写SHA-256十六进制摘要。
func validDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// validTime 校验非零UTC时间。
func validTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}
