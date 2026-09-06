package trustcandidate

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ValidateEnvelope 是 TrustEnvelope.Validate 的函数形式。
func ValidateEnvelope(envelope TrustEnvelope) error { return envelope.Validate() }

// ValidateContentTicket 是 ContentTicket.Validate 的函数形式。
func ValidateContentTicket(ticket ContentTicket) error { return ticket.Validate() }

// Validate 校验 envelope 的固定字段、判别联合、digest 与 inert metadata。
func (e TrustEnvelope) Validate() error {
	if err := validateEnvelopeShape(e); err != nil {
		return err
	}
	if !validDigest(e.EnvelopeDigest) {
		return invalidEnvelope("envelope_digest must be a lowercase SHA-256 digest")
	}
	want, err := ComputeEnvelopeDigest(e)
	if err != nil || want != e.EnvelopeDigest {
		return invalidEnvelope("envelope_digest does not cover the envelope fields")
	}
	return nil
}

// ValidateAt 在指定受信时间点校验 expiry，并保留候选不执行语义。
func (e TrustEnvelope) ValidateAt(now time.Time) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if now.IsZero() || e.ExpiresAtUnix <= now.Unix() {
		return fmt.Errorf("%w: envelope expiry is missing or elapsed", ErrQuarantined)
	}
	return nil
}

// ValidateForCanonicalEvent 只允许已验证 transport 的 untrusted quoted text/metadata。
func (e TrustEnvelope) ValidateForCanonicalEvent() error {
	if err := e.Validate(); err != nil {
		return err
	}
	if !e.Provenance.VerifiedTransport {
		return fmt.Errorf("%w: verified_transport is false", ErrUnverified)
	}
	if e.TrustClass != TrustUntrustedExternalData || e.ContentRole != RoleQuotedData {
		return fmt.Errorf("%w: only untrusted quoted data can enter a candidate event", ErrQuarantined)
	}
	if e.ContentKind == ContentLink || e.ContentKind == ContentImage {
		return fmt.Errorf("%w: links and images stay inert metadata", ErrInertContent)
	}
	if e.ContentKind == ContentControl {
		return fmt.Errorf("%w: control content cannot enter a candidate event", ErrControlRejected)
	}
	return nil
}

// ValidateForQuote 校验 envelope 可被唯一 quoted renderer 包装，拒绝主动 sink 内容。
func (e TrustEnvelope) ValidateForQuote() error {
	if err := e.Validate(); err != nil {
		return err
	}
	if !e.Provenance.VerifiedTransport {
		return fmt.Errorf("%w: quoted renderer requires verified transport", ErrUnverified)
	}
	if e.TrustClass != TrustUntrustedExternalData || e.ContentRole != RoleQuotedData {
		return fmt.Errorf("%w: quote role is not an executable role", ErrQuarantined)
	}
	if e.ContentKind == ContentLink || e.ContentKind == ContentImage || e.ContentKind == ContentControl {
		return fmt.Errorf("%w: non-text/control content is not renderable", ErrInertContent)
	}
	return nil
}

// ValidateForControl 明确阻断 candidate control branch；签名 owner 不在本包中。
func (e TrustEnvelope) ValidateForControl() error {
	if err := e.Validate(); err != nil {
		return err
	}
	return fmt.Errorf("%w: candidate package has no controller signature or trust root", ErrControlRejected)
}

// ValidateForCanonicalControl 是 ValidateForControl 的语义别名。
func (e TrustEnvelope) ValidateForCanonicalControl() error { return e.ValidateForControl() }

// validateEnvelopeShape 校验不含 self-digest 比较的 envelope 字段。
func validateEnvelopeShape(e TrustEnvelope) error {
	if err := validateEnvelopeHeader(e); err != nil {
		return err
	}
	if err := e.Provenance.validate(); err != nil {
		return err
	}
	if err := validateEnvelopeDiscriminators(e); err != nil {
		return err
	}
	if err := e.ContentRef.validate(); err != nil {
		return err
	}
	if err := validateEnvelopeContent(e); err != nil {
		return err
	}
	if err := validatePayloadKind(e); err != nil {
		return err
	}
	return nil
}

// validateEnvelopeHeader 校验版本与 envelope/source identity 字段边界。
func validateEnvelopeHeader(e TrustEnvelope) error {
	if e.SchemaVersion != SchemaVersion {
		return invalidEnvelope("schema_version must equal 1")
	}
	for name, value := range envelopeIdentityFields(e) {
		if !validText(value, 512) {
			return invalidEnvelope(name + " is invalid")
		}
	}
	return nil
}

func envelopeIdentityFields(e TrustEnvelope) map[string]string {
	return map[string]string{
		"envelope_id": e.EnvelopeID, "operation_id": e.OperationID, "source_fence": e.SourceFence,
		"source_agent": e.SourceAgent, "source_incarnation": e.SourceIncarnation, "source_turn": e.SourceTurn,
		"event_cursor": e.EventCursor, "content_cursor": e.ContentCursor,
	}
}

// validateEnvelopeDiscriminators 校验 trust/role/kind 三个固定判别字段。
func validateEnvelopeDiscriminators(e TrustEnvelope) error {
	if !knownTrustClass(e.TrustClass) {
		return invalidEnvelope("unknown trust class")
	}
	if !knownContentRole(e.ContentRole) || !knownContentKind(e.ContentKind) {
		return invalidEnvelope("unknown content role or kind")
	}
	return validateRoleTuple(e)
}

// validateEnvelopeContent 校验 media type、bounded size 与 content hash。
func validateEnvelopeContent(e TrustEnvelope) error {
	if !validText(e.MediaType, 256) {
		return invalidEnvelope("media_type is invalid")
	}
	if e.ContentSize < 0 || e.ContentSize > MaxContentBytes {
		return invalidEnvelope("content_size is outside the bound")
	}
	if !validDigest(e.ContentHash) {
		return invalidEnvelope("content_hash must be a lowercase SHA-256 digest")
	}
	return nil
}

// validateRoleTuple 约束 provider/peer/A2A 等外部来源永远保持 quoted taint。
func validateRoleTuple(e TrustEnvelope) error {
	switch e.TrustClass {
	case TrustUntrustedExternalData:
		if e.ContentRole != RoleQuotedData || !untrustedOrigin(e.Provenance.OriginKind) {
			return invalidEnvelope("untrusted data must use a quoted_data role and external origin")
		}
	case TrustAuthenticatedPrincipal:
		if e.ContentRole != RoleUserContent && e.ContentRole != RoleToolResult {
			return invalidEnvelope("authenticated principal content has an invalid role")
		}
	case TrustControllerSignedControl, TrustInternalControl:
		if e.ContentRole != RoleControlInstruction || e.ContentKind != ContentControl {
			return invalidEnvelope("control trust must use control_instruction/content control")
		}
	default:
		return invalidEnvelope("unknown trust class")
	}
	return nil
}

// validatePayloadKind 校验 text/metadata/link/image/control 的专属字段组合。
func validatePayloadKind(e TrustEnvelope) error {
	switch e.ContentKind {
	case ContentText:
		return validateTextPayload(e)
	case ContentMetadata:
		return validateMetadataPayload(e)
	case ContentLink:
		return validateLinkPayload(e)
	case ContentImage:
		return validateImagePayload(e)
	case ContentControl:
		return validateControlPayload(e)
	default:
		return invalidEnvelope("unknown content kind")
	}
}

// validateTextPayload 校验 text 不携带 metadata/link 且不是主动内容。
func validateTextPayload(e TrustEnvelope) error {
	mediaType := lowerASCII(e.MediaType)
	if strings.HasPrefix(mediaType, "image/") || mediaType == "text/uri-list" {
		return invalidEnvelope("text content cannot masquerade as image/link")
	}
	if !isZeroMetadata(e.Metadata) || !isZeroLink(e.Link) || e.Inert {
		return invalidEnvelope("text content cannot carry metadata/link fields or inert flag")
	}
	return nil
}

// validateMetadataPayload 校验 metadata 的 inert/link 分支与必需 ingress 字段。
func validateMetadataPayload(e TrustEnvelope) error {
	if e.Inert || !isZeroLink(e.Link) {
		return invalidEnvelope("metadata content cannot carry link fields or inert flag")
	}
	return e.Metadata.validate(e.ContentRef, e.ContentHash, e.MediaType)
}

// validateLinkPayload 校验 link 必须 inert 且不能混入 metadata。
func validateLinkPayload(e TrustEnvelope) error {
	if !e.Inert || !isZeroMetadata(e.Metadata) {
		return invalidEnvelope("link content must be inert and cannot carry unrelated metadata")
	}
	return e.Link.validate()
}

// validateImagePayload 校验 image 必须 inert、使用 image media type 且无 active link。
func validateImagePayload(e TrustEnvelope) error {
	if !e.Inert || !strings.HasPrefix(lowerASCII(e.MediaType), "image/") || !isZeroMetadata(e.Metadata) {
		return invalidEnvelope("image content must be inert and use an image media type")
	}
	if !isZeroLink(e.Link) {
		return invalidEnvelope("image content cannot carry an active link")
	}
	return nil
}

// validateControlPayload 校验 control 不能携带 metadata/link candidate 字段。
func validateControlPayload(e TrustEnvelope) error {
	if !isZeroMetadata(e.Metadata) || !isZeroLink(e.Link) {
		return invalidEnvelope("control content cannot carry metadata/link fields")
	}
	return nil
}

// Validate 校验 provenance 的来源 enum 与 source subject。
func (p Provenance) validate() error {
	if !knownOrigin(p.OriginKind) || !validText(p.SourceSubject, 512) {
		return invalidEnvelope("provenance origin or source_subject is invalid")
	}
	return nil
}

// Validate 校验 content ref 的 owner reference 与 self-derived digest。
func (r ContentRef) Validate() error { return r.validate() }

func (r ContentRef) validate() error {
	if err := validateContentRefHeader(r); err != nil {
		return err
	}
	want, err := ComputeContentRefDigest(r.Ref, r.Version)
	if err != nil || want != r.Digest {
		return invalidEnvelope("content_ref digest does not cover ref/version")
	}
	return nil
}

// validateContentRefHeader 校验 content ref 的文字、版本与 digest 形状。
func validateContentRefHeader(r ContentRef) error {
	if !validText(r.Ref, 512) || r.Version <= 0 {
		return invalidEnvelope("content_ref is invalid")
	}
	if !validDigest(r.Digest) {
		return invalidEnvelope("content_ref digest is invalid")
	}
	return nil
}

// validate 校验 MCP/A2A metadata 的 origin、schema receipt 与 digest 绑定。
func (m MetadataInfo) validate(ref ContentRef, contentHash, mediaType string) error {
	if err := validateMetadataHeader(m); err != nil {
		return err
	}
	if err := m.ContentRef.validate(); err != nil || !m.ContentRef.equal(ref) {
		return invalidEnvelope("metadata content_ref does not match envelope")
	}
	return validateMetadataBinding(m, contentHash, mediaType)
}

// validateMetadataHeader 校验 metadata kind/origin 和 ingress receipt 字段。
func validateMetadataHeader(m MetadataInfo) error {
	if !knownMetadataKind(m.MetadataKind) || !knownMetadataOrigin(m.OriginKind) {
		return invalidEnvelope("metadata kind or origin is unknown")
	}
	for _, value := range []string{m.SourceSubject, m.SourceFence, m.TransportAuthRef, m.SchemaValidationReceipt, m.TrustDecisionID} {
		if !validText(value, 512) {
			return invalidEnvelope("metadata ingress text is invalid")
		}
	}
	if m.RegistryRevision <= 0 || !validDigest(m.MetadataDigest) || !validText(m.MediaType, 256) {
		return invalidEnvelope("metadata ingress revision/digest/media is invalid")
	}
	return nil
}

// validateMetadataBinding 校验 metadata content ref、media type 与 raw digest 的绑定。
func validateMetadataBinding(m MetadataInfo, contentHash, mediaType string) error {
	if m.MediaType != mediaType || m.MetadataDigest != contentHash {
		return invalidEnvelope("metadata digest/media_type does not match envelope")
	}
	if m.MetadataKind == MetadataMCPToolsList && m.OriginKind != OriginMCPMetadata {
		return invalidEnvelope("MCP metadata kind and origin are mixed")
	}
	if m.MetadataKind == MetadataA2AAgentCard && m.OriginKind != OriginA2AMetadata {
		return invalidEnvelope("A2A metadata kind and origin are mixed")
	}
	return nil
}

func knownMetadataKind(value MetadataKind) bool {
	return value == MetadataMCPToolsList || value == MetadataA2AAgentCard
}

func knownMetadataOrigin(value OriginKind) bool {
	return value == OriginMCPMetadata || value == OriginA2AMetadata
}

// Validate 校验 candidate ticket 的 bounded body 与 envelope binding。
func (t ContentTicket) Validate() error {
	if err := validateTicketHeader(t); err != nil {
		return err
	}
	if err := t.ContentRef.validate(); err != nil {
		return invalidTicket("ticket content_ref is invalid")
	}
	if err := validateTicketContent(t); err != nil {
		return err
	}
	return nil
}

// validateTicketHeader 校验 ticket identity、envelope digest 与 source fence。
func validateTicketHeader(t ContentTicket) error {
	if t.SchemaVersion != SchemaVersion || !validText(t.TicketID, 256) || !validText(t.OperationID, 512) || !validDigest(t.EnvelopeDigest) {
		return invalidTicket("ticket header is invalid")
	}
	if !validText(t.SourceFence, 512) || !validText(t.MediaType, 256) {
		return invalidTicket("ticket fence or media type is invalid")
	}
	return nil
}

// validateTicketContent 校验正文 bounded size、hash 与 content hash 字段。
func validateTicketContent(t ContentTicket) error {
	if t.ContentSize <= 0 || t.ContentSize > MaxContentBytes || !validDigest(t.ContentHash) {
		return invalidTicket("ticket content size or hash is invalid")
	}
	if int64(len(t.body)) != t.ContentSize || hashBytes(t.body) != t.ContentHash {
		return invalidTicket("ticket body hash or size is invalid")
	}
	return nil
}

// ValidateAgainst 校验 ticket 与 envelope 的所有跨边界字段逐字匹配。
func (t ContentTicket) ValidateAgainst(e TrustEnvelope) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if err := t.Validate(); err != nil {
		return err
	}
	return validateTicketBinding(t, e)
}

// validateTicketBinding 校验 ticket 与 envelope 的跨边界字段逐字绑定。
func validateTicketBinding(t ContentTicket, e TrustEnvelope) error {
	if t.EnvelopeDigest != e.EnvelopeDigest || t.OperationID != e.OperationID || t.SourceFence != e.SourceFence {
		return ErrTicketMismatch
	}
	if t.ContentRef != e.ContentRef || t.MediaType != e.MediaType || t.ContentSize != e.ContentSize || t.ContentHash != e.ContentHash {
		return ErrTicketMismatch
	}
	if t.Truncated != e.Truncated || t.ExpiresAtUnix != e.ExpiresAtUnix {
		return ErrTicketMismatch
	}
	return nil
}

// Validate 校验 canonical event 只携 envelope/ref，不携正文。
func (c CanonicalEvent) Validate(ticket ContentTicket) error {
	if err := validateEventHeader(c); err != nil {
		return err
	}
	if err := c.Envelope.ValidateForCanonicalEvent(); err != nil {
		return err
	}
	if c.ContentTicketID != ticket.TicketID {
		return ErrTicketMismatch
	}
	return ticket.ValidateAgainst(c.Envelope)
}

// validateEventHeader 校验 canonical event 的 schema 与 ticket identity。
func validateEventHeader(c CanonicalEvent) error {
	if c.SchemaVersion != SchemaVersion || !validText(c.ContentTicketID, 256) {
		return invalidEnvelope("canonical event header is invalid")
	}
	return nil
}

// Validate 校验零副作用 sink decision 的固定字段与计数。
func (d SinkDecision) Validate() error {
	if err := validateSinkHeader(d); err != nil {
		return err
	}
	if err := validateSinkCounters(d); err != nil {
		return err
	}
	return validateSinkDecisionTuple(d)
}

// validateSinkHeader 校验 sink decision 的 schema、kind、decision 与 reason。
func validateSinkHeader(d SinkDecision) error {
	if d.SchemaVersion != SchemaVersion || !validDigest(d.EnvelopeDigest) || !knownSink(d.SinkKind) || !knownDecision(d.Decision) {
		return invalidEnvelope("sink decision header is invalid")
	}
	if !validText(d.Reason, 512) {
		return invalidEnvelope("sink decision reason is invalid")
	}
	return nil
}

// validateSinkCounters 校验候选 sink 永不报告 network/file/tool side effect。
func validateSinkCounters(d SinkDecision) error {
	if d.NetworkCalls != 0 || d.FileCalls != 0 || d.NavigationCalls != 0 {
		return invalidEnvelope("candidate sink decision cannot report external side effects")
	}
	if d.ToolCalls != 0 || d.A2ACalls != 0 {
		return invalidEnvelope("candidate sink decision cannot report tool/A2A side effects")
	}
	return nil
}

// validateSinkDecisionTuple 校验 allowed decision 只能对应 quoted user/data。
func validateSinkDecisionTuple(d SinkDecision) error {
	if d.Allowed && d.Decision != DecisionAllowedQuoted {
		return invalidEnvelope("allowed decision must be allowed_quoted")
	}
	return nil
}

func (r ContentRef) equal(other ContentRef) bool { return r == other }

// validText 校验 bounded UTF-8 标识文本，拒绝空白边界与控制字符。
func validText(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func invalidEnvelope(detail string) error { return fmt.Errorf("%w: %s", ErrInvalidEnvelope, detail) }
func invalidTicket(detail string) error   { return fmt.Errorf("%w: %s", ErrInvalidTicket, detail) }

func knownOrigin(value OriginKind) bool {
	switch value {
	case OriginProviderOutput, OriginProviderMetadata, OriginPeerOutput, OriginA2AOutput, OriginTranscript,
		OriginArtifact, OriginMemoryRecall, OriginRoomHistory, OriginMCPMetadata, OriginA2AMetadata,
		OriginUserInput, OriginInternalOwner:
		return true
	default:
		return false
	}
}

func untrustedOrigin(value OriginKind) bool {
	switch value {
	case OriginProviderOutput, OriginProviderMetadata, OriginPeerOutput, OriginA2AOutput, OriginTranscript,
		OriginArtifact, OriginMemoryRecall, OriginRoomHistory, OriginMCPMetadata, OriginA2AMetadata:
		return true
	default:
		return false
	}
}

func knownTrustClass(value TrustClass) bool {
	switch value {
	case TrustUntrustedExternalData, TrustAuthenticatedPrincipal, TrustControllerSignedControl, TrustInternalControl:
		return true
	default:
		return false
	}
}

func knownContentRole(value ContentRole) bool {
	switch value {
	case RoleQuotedData, RoleUserContent, RoleControlInstruction, RoleToolResult:
		return true
	default:
		return false
	}
}

func knownContentKind(value ContentKind) bool {
	switch value {
	case ContentText, ContentMetadata, ContentLink, ContentImage, ContentControl:
		return true
	default:
		return false
	}
}

func knownSink(value SinkKind) bool {
	switch value {
	case SinkModelSystemPrompt, SinkModelUserData, SinkToolArgument, SinkToolResult, SinkUIHTMLMarkdown,
		SinkUIANSIOSC, SinkUILinkImageMedia, SinkMCPMetadata, SinkA2AMetadata, SinkNavigation, SinkNetwork, SinkFile:
		return true
	default:
		return false
	}
}

func knownDecision(value DecisionKind) bool {
	switch value {
	case DecisionAllowedQuoted, DecisionDenied, DecisionQuarantined, DecisionInert:
		return true
	default:
		return false
	}
}

func isZeroMetadata(value MetadataInfo) bool { return value == (MetadataInfo{}) }
func isZeroLink(value LinkMetadata) bool     { return value == (LinkMetadata{}) }
