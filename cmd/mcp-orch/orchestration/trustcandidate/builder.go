package trustcandidate

import (
	"fmt"
	"unicode/utf8"
)

// EnvelopeInput 是通用候选 envelope 构造输入；不会生成任何 owner、grant 或 sink。
type EnvelopeInput struct {
	SchemaVersion     int64        `json:"schema_version"`
	EnvelopeID        string       `json:"envelope_id"`
	TicketID          string       `json:"ticket_id"`
	OperationID       string       `json:"operation_id"`
	Provenance        Provenance   `json:"provenance"`
	TrustClass        TrustClass   `json:"trust_class"`
	ContentRole       ContentRole  `json:"content_role"`
	ContentKind       ContentKind  `json:"content_kind"`
	SourceFence       string       `json:"source_fence"`
	ContentRef        ContentRef   `json:"content_ref"`
	MediaType         string       `json:"media_type"`
	SourceAgent       string       `json:"source_agent"`
	SourceIncarnation string       `json:"source_incarnation"`
	SourceTurn        string       `json:"source_turn"`
	EventCursor       string       `json:"event_cursor"`
	ContentCursor     string       `json:"content_cursor"`
	Truncated         bool         `json:"truncated"`
	Inert             bool         `json:"inert"`
	ExpiresAtUnix     int64        `json:"expires_at_unix"`
	Metadata          MetadataInfo `json:"metadata"`
	Link              LinkMetadata `json:"link"`
	Content           []byte       `json:"-"`
}

// NewEnvelope 严格构造 candidate envelope 与短期内存 content ticket。
func NewEnvelope(input EnvelopeInput) (TrustEnvelope, ContentTicket, error) {
	if err := validateEnvelopeInput(input); err != nil {
		return TrustEnvelope{}, ContentTicket{}, err
	}
	envelope := buildEnvelope(input)
	finalized, err := FinalizeEnvelope(envelope)
	if err != nil {
		return TrustEnvelope{}, ContentTicket{}, err
	}
	ticket, err := newTicketForEnvelope(finalized, input.TicketID, input.Content)
	if err != nil {
		return TrustEnvelope{}, ContentTicket{}, err
	}
	return finalized, ticket, nil
}

func validateEnvelopeInput(input EnvelopeInput) error {
	if input.SchemaVersion == 0 {
		return invalidEnvelope("schema_version is required")
	}
	if err := validateInputContent(input.ContentKind, input.Content); err != nil {
		return err
	}
	if input.ContentRef.Digest == "" {
		return invalidTicket("content_ref digest is required")
	}
	return nil
}

// validateInputContent 校验正文大小、UTF-8 与 metadata JSON 形状。
func validateInputContent(kind ContentKind, content []byte) error {
	if len(content) == 0 || len(content) > MaxContentBytes {
		return invalidTicket("content must be non-empty and bounded")
	}
	if kind != ContentImage && !utf8.Valid(content) {
		return invalidTicket("textual candidate content is not valid UTF-8")
	}
	if kind == ContentMetadata {
		return validateJSONShape(content)
	}
	return nil
}

func buildEnvelope(input EnvelopeInput) TrustEnvelope {
	hash := hashBytes(input.Content)
	return TrustEnvelope{
		SchemaVersion: input.SchemaVersion, EnvelopeID: input.EnvelopeID, OperationID: input.OperationID,
		Provenance: input.Provenance, TrustClass: input.TrustClass, ContentRole: input.ContentRole,
		ContentKind: input.ContentKind, SourceFence: input.SourceFence, ContentRef: input.ContentRef,
		MediaType: input.MediaType, ContentSize: int64(len(input.Content)), ContentHash: hash,
		SourceAgent: input.SourceAgent, SourceIncarnation: input.SourceIncarnation, SourceTurn: input.SourceTurn,
		EventCursor: input.EventCursor, ContentCursor: input.ContentCursor, Truncated: input.Truncated,
		Inert: input.Inert, ExpiresAtUnix: input.ExpiresAtUnix, Metadata: input.Metadata, Link: input.Link,
	}
}

// NewProviderTextEnvelope 将 provider text 固定降级为 untrusted/quoted_data。
func NewProviderTextEnvelope(input ProviderTextInput) (TrustEnvelope, ContentTicket, error) {
	if !utf8.ValidString(input.Text) {
		return TrustEnvelope{}, ContentTicket{}, invalidTicket("provider text is not valid UTF-8")
	}
	return NewEnvelope(EnvelopeInput{
		SchemaVersion: SchemaVersion, EnvelopeID: input.EnvelopeID, TicketID: input.TicketID,
		OperationID: input.OperationID,
		Provenance:  Provenance{OriginKind: OriginProviderOutput, SourceSubject: input.SourceSubject, VerifiedTransport: input.VerifiedTransport},
		TrustClass:  TrustUntrustedExternalData, ContentRole: RoleQuotedData, ContentKind: ContentText,
		SourceFence: input.SourceFence, ContentRef: input.ContentRef, MediaType: input.MediaType,
		SourceAgent: input.SourceAgent, SourceIncarnation: input.SourceIncarnation, SourceTurn: input.SourceTurn,
		EventCursor: input.EventCursor, ContentCursor: input.ContentCursor, Truncated: input.Truncated,
		ExpiresAtUnix: input.ExpiresAtUnix, Content: []byte(input.Text),
	})
}

// NewProviderText 是 NewProviderTextEnvelope 的短名称别名。
func NewProviderText(input ProviderTextInput) (TrustEnvelope, ContentTicket, error) {
	return NewProviderTextEnvelope(input)
}

// NewMetadataEnvelope 将 MCP/A2A metadata 固定降级为 untrusted/quoted_data。
func NewMetadataEnvelope(input MetadataInput) (TrustEnvelope, ContentTicket, error) {
	if !utf8.Valid(input.Raw) {
		return TrustEnvelope{}, ContentTicket{}, invalidTicket("metadata bytes are not valid UTF-8")
	}
	if err := validateJSONShape(input.Raw); err != nil {
		return TrustEnvelope{}, ContentTicket{}, err
	}
	digest, err := ComputeMetadataDigest(input.Raw)
	if err != nil {
		return TrustEnvelope{}, ContentTicket{}, err
	}
	metadata := MetadataInfo{
		MetadataKind: input.MetadataKind, OriginKind: input.OriginKind, SourceSubject: input.SourceSubject,
		SourceFence: input.SourceFence, TransportAuthRef: input.TransportAuthRef,
		RegistryRevision: input.RegistryRevision, MetadataDigest: digest, ContentRef: input.ContentRef,
		MediaType: input.MediaType, SchemaValidationReceipt: input.SchemaValidationReceipt,
		TrustDecisionID: input.TrustDecisionID,
	}
	return NewEnvelope(EnvelopeInput{
		SchemaVersion: SchemaVersion, EnvelopeID: input.EnvelopeID, TicketID: input.TicketID,
		OperationID: input.OperationID,
		Provenance:  Provenance{OriginKind: input.OriginKind, SourceSubject: input.SourceSubject, VerifiedTransport: input.VerifiedTransport},
		TrustClass:  TrustUntrustedExternalData, ContentRole: RoleQuotedData, ContentKind: ContentMetadata,
		SourceFence: input.SourceFence, ContentRef: input.ContentRef, MediaType: input.MediaType,
		SourceAgent: input.SourceAgent, SourceIncarnation: input.SourceIncarnation, SourceTurn: input.SourceTurn,
		EventCursor: input.EventCursor, ContentCursor: input.ContentCursor, Truncated: input.Truncated,
		ExpiresAtUnix: input.ExpiresAtUnix, Metadata: metadata, Content: input.Raw,
	})
}

// NewMCPMetadataEnvelope 是 MCP tools/list metadata 的固定来源构造器。
func NewMCPMetadataEnvelope(input MetadataInput) (TrustEnvelope, ContentTicket, error) {
	input.MetadataKind = MetadataMCPToolsList
	input.OriginKind = OriginMCPMetadata
	return NewMetadataEnvelope(input)
}

// NewA2AMetadataEnvelope 是 A2A Agent Card metadata 的固定来源构造器。
func NewA2AMetadataEnvelope(input MetadataInput) (TrustEnvelope, ContentTicket, error) {
	input.MetadataKind = MetadataA2AAgentCard
	input.OriginKind = OriginA2AMetadata
	return NewMetadataEnvelope(input)
}

// NewLinkEnvelope 将 URI/icon/artifact link 转为 inert metadata，禁止自动 fetch。
func NewLinkEnvelope(input LinkInput) (TrustEnvelope, ContentTicket, error) {
	link, err := NewLinkMetadata(input.URI, input.RedirectPolicyID, input.EgressPolicyDigest)
	if err != nil {
		return TrustEnvelope{}, ContentTicket{}, err
	}
	return NewEnvelope(EnvelopeInput{
		SchemaVersion: SchemaVersion, EnvelopeID: input.EnvelopeID, TicketID: input.TicketID,
		OperationID: input.OperationID,
		Provenance:  Provenance{OriginKind: OriginProviderMetadata, SourceSubject: input.SourceSubject, VerifiedTransport: input.VerifiedTransport},
		TrustClass:  TrustUntrustedExternalData, ContentRole: RoleQuotedData, ContentKind: ContentLink,
		SourceFence: input.SourceFence, ContentRef: input.ContentRef, MediaType: input.MediaType,
		SourceAgent: input.SourceAgent, SourceIncarnation: input.SourceIncarnation, SourceTurn: input.SourceTurn,
		EventCursor: input.EventCursor, ContentCursor: input.ContentCursor, Inert: true,
		ExpiresAtUnix: input.ExpiresAtUnix, Link: link, Content: []byte(input.URI),
	})
}

// NewImageEnvelope 将图片 bytes 标记为 inert，不自动解码、展示或网络请求。
func NewImageEnvelope(input ImageInput) (TrustEnvelope, ContentTicket, error) {
	return NewEnvelope(EnvelopeInput{
		SchemaVersion: SchemaVersion, EnvelopeID: input.EnvelopeID, TicketID: input.TicketID,
		OperationID: input.OperationID,
		Provenance:  Provenance{OriginKind: OriginProviderOutput, SourceSubject: input.SourceSubject, VerifiedTransport: input.VerifiedTransport},
		TrustClass:  TrustUntrustedExternalData, ContentRole: RoleQuotedData, ContentKind: ContentImage,
		SourceFence: input.SourceFence, ContentRef: input.ContentRef, MediaType: input.MediaType,
		SourceAgent: input.SourceAgent, SourceIncarnation: input.SourceIncarnation, SourceTurn: input.SourceTurn,
		EventCursor: input.EventCursor, ContentCursor: input.ContentCursor, Inert: true,
		Truncated: input.Truncated, ExpiresAtUnix: input.ExpiresAtUnix, Content: input.Content,
	})
}

// NewContentTicket 构造有界 ticket；Content 始终复制到私有内存。
func NewContentTicket(input ContentTicketInput) (ContentTicket, error) {
	ticket := ContentTicket{
		SchemaVersion: input.SchemaVersion, TicketID: input.TicketID, OperationID: input.OperationID,
		EnvelopeDigest: input.EnvelopeDigest, ContentRef: input.ContentRef, SourceFence: input.SourceFence,
		MediaType: input.MediaType, ContentSize: input.ContentSize, ContentHash: input.ContentHash,
		Truncated: input.Truncated, ExpiresAtUnix: input.ExpiresAtUnix, body: append([]byte(nil), input.Content...),
	}
	if err := ticket.Validate(); err != nil {
		return ContentTicket{}, err
	}
	return ticket, nil
}

func newTicketForEnvelope(envelope TrustEnvelope, ticketID string, content []byte) (ContentTicket, error) {
	if !validText(ticketID, 256) {
		return ContentTicket{}, invalidTicket("ticket_id is invalid")
	}
	ticket, err := NewContentTicket(ContentTicketInput{
		SchemaVersion: SchemaVersion, TicketID: ticketID, OperationID: envelope.OperationID,
		EnvelopeDigest: envelope.EnvelopeDigest, ContentRef: envelope.ContentRef,
		SourceFence: envelope.SourceFence, MediaType: envelope.MediaType,
		ContentSize: envelope.ContentSize, ContentHash: envelope.ContentHash, Truncated: envelope.Truncated,
		ExpiresAtUnix: envelope.ExpiresAtUnix, Content: content,
	})
	if err != nil {
		return ContentTicket{}, fmt.Errorf("%w: build ticket for envelope", err)
	}
	return ticket, nil
}

// Body 返回 ticket 正文的独立副本；修改返回值不会修改 ticket。
func (t ContentTicket) Body() []byte { return append([]byte(nil), t.body...) }

// ContentBytes 返回 ticket 正文副本，作为 Body 的语义别名。
func (t ContentTicket) ContentBytes() []byte { return t.Body() }
