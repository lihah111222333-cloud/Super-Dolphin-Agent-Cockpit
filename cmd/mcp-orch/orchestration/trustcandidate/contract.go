// Package trustcandidate 提供仅用于候选验证的 TRUST envelope 与 quoted-output seam。
//
// 本包是 transport-neutral、stdlib-only 的边界。它不拥有 provider、store、
// CONTENT key/object backend、OUT、EventStore、Fx 或任何 network/UI sink；所有
// sink admission 都是不可变的、零副作用的判定值，不能被当作生产授权。
package trustcandidate

import "errors"

const (
	// SchemaVersion 是候选 envelope、ticket 与 canonical event 的版本。
	SchemaVersion int64 = 1
	// QuotedWireVersion 是唯一 quoted-data renderer 的 wire 版本。
	QuotedWireVersion = "v3.orchestration.quoted_data.v1"
	// QuotedExecutionBoundary 固定 quoted frame 只能作为 user/data，不可执行。
	QuotedExecutionBoundary = "quoted_user_data_only"
	// MaxContentBytes 是候选单正文和 metadata body 的硬上限。
	MaxContentBytes = 64 * 1024
	// MaxEnvelopeJSONBytes 是 canonical envelope/event JSON 的硬上限。
	MaxEnvelopeJSONBytes = 128 * 1024
	// MaxQuotedBytes 是 length-delimited quoted frame 的硬上限。
	MaxQuotedBytes = 128 * 1024
	// MaxJSONDepth 是严格 JSON scanner 允许的最大嵌套深度。
	MaxJSONDepth = 16
	// MaxObjectMembers 是每个 JSON object/array 允许的最大成员数。
	MaxObjectMembers = 64
)

// 兼容性别名保持上限名称明确而不引入第二套数值。
const (
	MaxJSONBytes          = MaxEnvelopeJSONBytes
	MaxContentTicketBytes = MaxContentBytes
)

var (
	// ErrInvalidEnvelope 表示 envelope 字段、判别联合或 digest 无效。
	ErrInvalidEnvelope = errors.New("trust candidate envelope is invalid")
	// ErrInvalidTicket 表示 content ticket 字段或正文绑定无效。
	ErrInvalidTicket = errors.New("trust candidate content ticket is invalid")
	// ErrInvalidJSON 表示 JSON 不是受限、单值、严格对象。
	ErrInvalidJSON = errors.New("trust candidate JSON is invalid")
	// ErrJSONTooLarge 表示 JSON 或 quoted frame 超出硬上限。
	ErrJSONTooLarge = errors.New("trust candidate JSON is too large")
	// ErrJSONTooDeep 表示 JSON 嵌套超过硬上限。
	ErrJSONTooDeep = errors.New("trust candidate JSON is too deep")
	// ErrDuplicateField 表示对象含 duplicate key。
	ErrDuplicateField = errors.New("trust candidate JSON contains duplicate field")
	// ErrUnknownField 表示对象含 schema 未登记字段。
	ErrUnknownField = errors.New("trust candidate JSON contains unknown field")
	// ErrNullField 表示字段显式为 null；候选 schema 不使用 null 分支。
	ErrNullField = errors.New("trust candidate JSON contains null field")
	// ErrQuarantined 表示输入只能隔离，不能进入 canonical event/control。
	ErrQuarantined = errors.New("trust candidate input is quarantined")
	// ErrUnverified 表示 transport/provenance 尚未验证。
	ErrUnverified = errors.New("trust candidate provenance is unverified")
	// ErrControlRejected 表示 candidate 不签发 control instruction。
	ErrControlRejected = errors.New("trust candidate control instruction is rejected")
	// ErrInertContent 表示 link/image/主动 sink 只能保持 inert。
	ErrInertContent = errors.New("trust candidate content is inert")
	// ErrUnknownSink 表示 sink kind 不在固定枚举中。
	ErrUnknownSink = errors.New("trust candidate sink is unknown")
	// ErrInvalidLink 表示 URI/link metadata 不安全或不一致。
	ErrInvalidLink = errors.New("trust candidate link metadata is invalid")
	// ErrTicketMismatch 表示 ticket 与 envelope 的字段未逐字绑定。
	ErrTicketMismatch = errors.New("trust candidate ticket does not match envelope")
)

// OriginKind 是内容的不可升级来源分类。
type OriginKind string

const (
	OriginProviderOutput   OriginKind = "provider_output"
	OriginProviderMetadata OriginKind = "provider_metadata"
	OriginPeerOutput       OriginKind = "peer_output"
	OriginA2AOutput        OriginKind = "a2a_output"
	OriginTranscript       OriginKind = "transcript"
	OriginArtifact         OriginKind = "artifact"
	OriginMemoryRecall     OriginKind = "memory_recall"
	OriginRoomHistory      OriginKind = "room_history"
	OriginMCPMetadata      OriginKind = "mcp_metadata"
	OriginA2AMetadata      OriginKind = "a2a_metadata"
	OriginUserInput        OriginKind = "user_input"
	OriginInternalOwner    OriginKind = "internal_owner"
)

// TrustClass 是内容信任等级；transport authentication 不会改变它。
type TrustClass string

const (
	TrustUntrustedExternalData   TrustClass = "untrusted_external_data"
	TrustAuthenticatedPrincipal  TrustClass = "authenticated_principal_content"
	TrustControllerSignedControl TrustClass = "controller_signed_control"
	TrustInternalControl         TrustClass = "internal_control"
)

// 常用 wire 名称别名保持调用方不必复制字符串常量。
const (
	UntrustedExternalData         = TrustUntrustedExternalData
	AuthenticatedPrincipalContent = TrustAuthenticatedPrincipal
	ControllerSignedControl       = TrustControllerSignedControl
	InternalControl               = TrustInternalControl
)

// ContentRole 是正文可解释的角色判别联合。
type ContentRole string

const (
	RoleQuotedData         ContentRole = "quoted_data"
	RoleUserContent        ContentRole = "user_content"
	RoleControlInstruction ContentRole = "control_instruction"
	RoleToolResult         ContentRole = "tool_result"
)

const (
	QuotedData         = RoleQuotedData
	UserContent        = RoleUserContent
	ControlInstruction = RoleControlInstruction
	ToolResult         = RoleToolResult
)

// ContentKind 标注正文是否为 text、metadata、link、image 或 control。
type ContentKind string

const (
	ContentText     ContentKind = "text"
	ContentMetadata ContentKind = "metadata"
	ContentLink     ContentKind = "link"
	ContentImage    ContentKind = "image"
	ContentControl  ContentKind = "control"
)

// MetadataKind 是 MCP/A2A metadata 的固定来源分支。
type MetadataKind string

const (
	MetadataMCPToolsList MetadataKind = "mcp_tools_list"
	MetadataA2AAgentCard MetadataKind = "a2a_agent_card"
)

// SinkKind 是候选可观察的 sink 分类；本包不执行任何 sink。
type SinkKind string

const (
	SinkModelSystemPrompt SinkKind = "model_system_prompt"
	SinkModelUserData     SinkKind = "model_user_data"
	SinkToolArgument      SinkKind = "tool_argument"
	SinkToolResult        SinkKind = "tool_result"
	SinkUIHTMLMarkdown    SinkKind = "ui_html_markdown"
	SinkUIANSIOSC         SinkKind = "ui_ansi_osc"
	SinkUILinkImageMedia  SinkKind = "ui_link_image_media"
	SinkMCPMetadata       SinkKind = "mcp_metadata"
	SinkA2AMetadata       SinkKind = "a2a_metadata"
	SinkNavigation        SinkKind = "navigation"
	SinkNetwork           SinkKind = "network"
	SinkFile              SinkKind = "file"
)

// DecisionKind 是零副作用 sink 判定结果。
type DecisionKind string

const (
	DecisionAllowedQuoted DecisionKind = "allowed_quoted"
	DecisionDenied        DecisionKind = "denied"
	DecisionQuarantined   DecisionKind = "quarantined"
	DecisionInert         DecisionKind = "inert"
)

// Provenance 是来源、subject 与 transport 验证结果；不含授权信息。
type Provenance struct {
	OriginKind        OriginKind `json:"origin_kind"`
	SourceSubject     string     `json:"source_subject"`
	VerifiedTransport bool       `json:"verified_transport"`
}

// ContentRef 是 CONTENT owner 将来签发的 opaque 引用；本候选只校验格式。
type ContentRef struct {
	Ref     string `json:"ref"`
	Version int64  `json:"version"`
	Digest  string `json:"digest"`
}

// MetadataInfo 保存 metadata ingress 必需字段；它不能改变 canonical registry/grant。
type MetadataInfo struct {
	MetadataKind            MetadataKind `json:"metadata_kind"`
	OriginKind              OriginKind   `json:"origin_kind"`
	SourceSubject           string       `json:"source_subject"`
	SourceFence             string       `json:"source_fence"`
	TransportAuthRef        string       `json:"transport_auth_ref"`
	RegistryRevision        int64        `json:"registry_revision"`
	MetadataDigest          string       `json:"metadata_digest"`
	ContentRef              ContentRef   `json:"content_ref"`
	MediaType               string       `json:"media_type"`
	SchemaValidationReceipt string       `json:"schema_validation_receipt"`
	TrustDecisionID         string       `json:"trust_decision_id"`
}

// LinkMetadata 将 URI 降级为 inert metadata；任何 receipt 字段在本候选中必须为空。
type LinkMetadata struct {
	URI                string `json:"uri"`
	URIDigest          string `json:"uri_digest"`
	Scheme             string `json:"scheme"`
	Host               string `json:"host"`
	PortOrAbsent       string `json:"port_or_absent"`
	PathDigest         string `json:"path_digest"`
	RedirectPolicyID   string `json:"redirect_policy_id"`
	EgressPolicyDigest string `json:"egress_policy_digest"`
	UserConsentID      string `json:"user_consent_id_or_absent"`
	FetchReceipt       string `json:"fetch_receipt_or_absent"`
}

// TrustEnvelope 是带 digest 的不可变 provenance/trust/content-role 候选快照。
// 修改任一字段都会使 EnvelopeDigest 校验失败；正文永远不在 envelope 中。
type TrustEnvelope struct {
	SchemaVersion     int64        `json:"schema_version"`
	EnvelopeID        string       `json:"envelope_id"`
	OperationID       string       `json:"operation_id"`
	Provenance        Provenance   `json:"provenance"`
	TrustClass        TrustClass   `json:"trust_class"`
	ContentRole       ContentRole  `json:"content_role"`
	ContentKind       ContentKind  `json:"content_kind"`
	SourceFence       string       `json:"source_fence"`
	ContentRef        ContentRef   `json:"content_ref"`
	MediaType         string       `json:"media_type"`
	ContentSize       int64        `json:"content_size"`
	ContentHash       string       `json:"content_hash"`
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
	EnvelopeDigest    string       `json:"envelope_digest"`
}

// ContentTicket 是有界正文的内存候选票据；body 为私有副本，不会被 JSON 序列化。
type ContentTicket struct {
	SchemaVersion  int64      `json:"schema_version"`
	TicketID       string     `json:"ticket_id"`
	OperationID    string     `json:"operation_id"`
	EnvelopeDigest string     `json:"envelope_digest"`
	ContentRef     ContentRef `json:"content_ref"`
	SourceFence    string     `json:"source_fence"`
	MediaType      string     `json:"media_type"`
	ContentSize    int64      `json:"content_size"`
	ContentHash    string     `json:"content_hash"`
	Truncated      bool       `json:"truncated"`
	ExpiresAtUnix  int64      `json:"expires_at_unix"`
	body           []byte
}

// ContentTicketInput 是 NewContentTicket 的严格构造输入。
type ContentTicketInput struct {
	SchemaVersion  int64      `json:"schema_version"`
	TicketID       string     `json:"ticket_id"`
	OperationID    string     `json:"operation_id"`
	EnvelopeDigest string     `json:"envelope_digest"`
	ContentRef     ContentRef `json:"content_ref"`
	SourceFence    string     `json:"source_fence"`
	MediaType      string     `json:"media_type"`
	ContentSize    int64      `json:"content_size"`
	ContentHash    string     `json:"content_hash"`
	Truncated      bool       `json:"truncated"`
	ExpiresAtUnix  int64      `json:"expires_at_unix"`
	Content        []byte     `json:"-"`
}

// ProviderTextInput 是 provider 原文 ingress 的候选字段；trust/role 由构造器固定。
type ProviderTextInput struct {
	EnvelopeID        string     `json:"envelope_id"`
	TicketID          string     `json:"ticket_id"`
	OperationID       string     `json:"operation_id"`
	SourceSubject     string     `json:"source_subject"`
	SourceFence       string     `json:"source_fence"`
	SourceAgent       string     `json:"source_agent"`
	SourceIncarnation string     `json:"source_incarnation"`
	SourceTurn        string     `json:"source_turn"`
	EventCursor       string     `json:"event_cursor"`
	ContentCursor     string     `json:"content_cursor"`
	VerifiedTransport bool       `json:"verified_transport"`
	ContentRef        ContentRef `json:"content_ref"`
	MediaType         string     `json:"media_type"`
	ExpiresAtUnix     int64      `json:"expires_at_unix"`
	Text              string     `json:"text"`
	Truncated         bool       `json:"truncated"`
}

// MetadataInput 是 MCP/A2A metadata ingress 的候选字段。
type MetadataInput struct {
	EnvelopeID              string       `json:"envelope_id"`
	TicketID                string       `json:"ticket_id"`
	OperationID             string       `json:"operation_id"`
	MetadataKind            MetadataKind `json:"metadata_kind"`
	OriginKind              OriginKind   `json:"origin_kind"`
	SourceSubject           string       `json:"source_subject"`
	SourceFence             string       `json:"source_fence"`
	SourceAgent             string       `json:"source_agent"`
	SourceIncarnation       string       `json:"source_incarnation"`
	SourceTurn              string       `json:"source_turn"`
	EventCursor             string       `json:"event_cursor"`
	ContentCursor           string       `json:"content_cursor"`
	VerifiedTransport       bool         `json:"verified_transport"`
	TransportAuthRef        string       `json:"transport_auth_ref"`
	RegistryRevision        int64        `json:"registry_revision"`
	SchemaValidationReceipt string       `json:"schema_validation_receipt"`
	TrustDecisionID         string       `json:"trust_decision_id"`
	ContentRef              ContentRef   `json:"content_ref"`
	MediaType               string       `json:"media_type"`
	ExpiresAtUnix           int64        `json:"expires_at_unix"`
	Raw                     []byte       `json:"-"`
	Truncated               bool         `json:"truncated"`
}

// LinkInput 是 link/icon/artifact URI ingress 的候选字段。
type LinkInput struct {
	EnvelopeID         string     `json:"envelope_id"`
	TicketID           string     `json:"ticket_id"`
	OperationID        string     `json:"operation_id"`
	SourceSubject      string     `json:"source_subject"`
	SourceFence        string     `json:"source_fence"`
	SourceAgent        string     `json:"source_agent"`
	SourceIncarnation  string     `json:"source_incarnation"`
	SourceTurn         string     `json:"source_turn"`
	EventCursor        string     `json:"event_cursor"`
	ContentCursor      string     `json:"content_cursor"`
	VerifiedTransport  bool       `json:"verified_transport"`
	ContentRef         ContentRef `json:"content_ref"`
	MediaType          string     `json:"media_type"`
	ExpiresAtUnix      int64      `json:"expires_at_unix"`
	URI                string     `json:"uri"`
	RedirectPolicyID   string     `json:"redirect_policy_id"`
	EgressPolicyDigest string     `json:"egress_policy_digest"`
}

// ImageInput 是 image/media ingress 的候选字段；图片默认 inert，不自动 fetch/render。
type ImageInput struct {
	EnvelopeID        string     `json:"envelope_id"`
	TicketID          string     `json:"ticket_id"`
	OperationID       string     `json:"operation_id"`
	SourceSubject     string     `json:"source_subject"`
	SourceFence       string     `json:"source_fence"`
	SourceAgent       string     `json:"source_agent"`
	SourceIncarnation string     `json:"source_incarnation"`
	SourceTurn        string     `json:"source_turn"`
	EventCursor       string     `json:"event_cursor"`
	ContentCursor     string     `json:"content_cursor"`
	VerifiedTransport bool       `json:"verified_transport"`
	ContentRef        ContentRef `json:"content_ref"`
	MediaType         string     `json:"media_type"`
	ExpiresAtUnix     int64      `json:"expires_at_unix"`
	Content           []byte     `json:"-"`
	Truncated         bool       `json:"truncated"`
}

// CanonicalEvent 是仅含 metadata/ref 的事件候选；正文仍留在 ticket 的短期内存副本。
type CanonicalEvent struct {
	SchemaVersion   int64         `json:"schema_version"`
	Envelope        TrustEnvelope `json:"envelope"`
	ContentTicketID string        `json:"content_ticket_id"`
}

// CanonicalControl 是结构占位，候选包永远不会构造成功的 control branch。
type CanonicalControl struct {
	SchemaVersion  int64       `json:"schema_version"`
	EnvelopeDigest string      `json:"envelope_digest"`
	OperationID    string      `json:"operation_id"`
	ContentRole    ContentRole `json:"content_role"`
}

// SinkDecision 是零副作用判定；计数用于证明未发生 network/file/navigation/tool 调用。
type SinkDecision struct {
	SchemaVersion   int64        `json:"schema_version"`
	EnvelopeDigest  string       `json:"envelope_digest"`
	SinkKind        SinkKind     `json:"sink_kind"`
	Decision        DecisionKind `json:"decision"`
	Allowed         bool         `json:"allowed"`
	Inert           bool         `json:"inert"`
	NetworkCalls    int64        `json:"network_calls"`
	FileCalls       int64        `json:"file_calls"`
	NavigationCalls int64        `json:"navigation_calls"`
	ToolCalls       int64        `json:"tool_calls"`
	A2ACalls        int64        `json:"a2a_calls"`
	Reason          string       `json:"reason"`
}

// UserConsent 仅记录候选输入，不授予本包执行 link/network/navigation 的能力。
type UserConsent struct {
	ConsentID     string `json:"consent_id"`
	Explicit      bool   `json:"explicit"`
	ExpiresAtUnix int64  `json:"expires_at_unix"`
}
