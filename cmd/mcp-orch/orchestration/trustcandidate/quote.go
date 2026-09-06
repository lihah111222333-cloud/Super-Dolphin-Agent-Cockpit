package trustcandidate

import (
	"encoding/binary"
	"fmt"
	"strconv"
)

const quotedFieldCount = 19

// RenderQuoted 将 ticket 正文包装为固定 quoted-data frame；输入正文永不解释为 role/control。
func RenderQuoted(envelope TrustEnvelope, ticket ContentTicket) (QuotedOutput, error) {
	if err := envelope.ValidateForQuote(); err != nil {
		return QuotedOutput{}, err
	}
	if err := ticket.ValidateAgainst(envelope); err != nil {
		return QuotedOutput{}, err
	}
	output := QuotedOutput{
		WireVersion: QuotedWireVersion, EnvelopeDigest: envelope.EnvelopeDigest,
		SourceAgent: envelope.SourceAgent, SourceIncarnation: envelope.SourceIncarnation,
		SourceTurn: envelope.SourceTurn, EventCursor: envelope.EventCursor,
		ContentCursor: envelope.ContentCursor, SourceFence: envelope.SourceFence,
		ContentHash: envelope.ContentHash, ContentSize: envelope.ContentSize,
		Provenance: envelope.Provenance, TrustClass: TrustUntrustedExternalData,
		ContentRole: RoleQuotedData, ContentKind: envelope.ContentKind, MediaType: envelope.MediaType,
		ExecutionBoundary: QuotedExecutionBoundary, Truncated: envelope.Truncated, Inert: false, body: ticket.Body(),
	}
	if err := output.Validate(); err != nil {
		return QuotedOutput{}, err
	}
	return output, nil
}

// RenderQuotedEnvelope 是 RenderQuoted 的语义别名，便于调用方明确 renderer 边界。
func RenderQuotedEnvelope(envelope TrustEnvelope, ticket ContentTicket) (QuotedOutput, error) {
	return RenderQuoted(envelope, ticket)
}

// QuotedOutput 是 length-delimited user/data frame；body 为私有副本。
type QuotedOutput struct {
	WireVersion       string      `json:"wire_version"`
	EnvelopeDigest    string      `json:"envelope_digest"`
	SourceAgent       string      `json:"source_agent"`
	SourceIncarnation string      `json:"source_incarnation"`
	SourceTurn        string      `json:"source_turn"`
	EventCursor       string      `json:"event_cursor"`
	ContentCursor     string      `json:"content_cursor"`
	SourceFence       string      `json:"source_fence"`
	ContentHash       string      `json:"content_hash"`
	ContentSize       int64       `json:"content_size"`
	Provenance        Provenance  `json:"provenance"`
	TrustClass        TrustClass  `json:"trust_class"`
	ContentRole       ContentRole `json:"content_role"`
	ContentKind       ContentKind `json:"content_kind"`
	ExecutionBoundary string      `json:"execution_boundary"`
	MediaType         string      `json:"media_type"`
	Truncated         bool        `json:"truncated"`
	Inert             bool        `json:"inert"`
	body              []byte
}

// Validate 校验 quoted frame 的固定 wire/version/role 与正文 hash。
func (q QuotedOutput) Validate() error {
	if err := validateQuotedHeader(q); err != nil {
		return err
	}
	if err := q.Provenance.validate(); err != nil {
		return err
	}
	return validateQuotedTuple(q)
}

// validateQuotedHeader 校验 quoted frame 的版本、游标、正文大小与 hash。
func validateQuotedHeader(q QuotedOutput) error {
	if q.WireVersion != QuotedWireVersion || !validDigest(q.EnvelopeDigest) {
		return invalidEnvelope("quoted output wire or envelope digest is invalid")
	}
	if !validText(q.SourceAgent, 512) || !validText(q.SourceIncarnation, 512) || !validText(q.SourceTurn, 512) {
		return invalidEnvelope("quoted output source identity is invalid")
	}
	if !validText(q.EventCursor, 512) || !validText(q.ContentCursor, 512) || !validText(q.SourceFence, 512) {
		return invalidEnvelope("quoted output cursor or fence is invalid")
	}
	return validateQuotedBody(q)
}

// validateQuotedBody 校验正文 bounded size 与 SHA-256 digest。
func validateQuotedBody(q QuotedOutput) error {
	if !validDigest(q.ContentHash) || q.ContentSize <= 0 || q.ContentSize > MaxContentBytes {
		return invalidEnvelope("quoted output content size or hash is invalid")
	}
	if q.ContentSize != int64(len(q.body)) || hashBytes(q.body) != q.ContentHash {
		return invalidEnvelope("quoted output body does not match size/hash")
	}
	return nil
}

// validateQuotedTuple 校验 quoted-data 的 provenance、role、kind 与媒体字段。
func validateQuotedTuple(q QuotedOutput) error {
	if err := validateQuotedTrustTuple(q); err != nil {
		return err
	}
	if err := validateQuotedKind(q); err != nil {
		return err
	}
	if err := validateQuotedBoundary(q); err != nil {
		return err
	}
	if err := validateQuotedMetadata(q); err != nil {
		return err
	}
	return validateQuotedMedia(q)
}

// validateQuotedTrustTuple 保持外部来源永远为 untrusted/quoted_data。
func validateQuotedTrustTuple(q QuotedOutput) error {
	if q.TrustClass != TrustUntrustedExternalData || q.ContentRole != RoleQuotedData || !untrustedOrigin(q.Provenance.OriginKind) {
		return fmt.Errorf("%w: quoted output role/trust tuple is invalid", ErrQuarantined)
	}
	return nil
}

// validateQuotedKind 限制 renderer 只能处理 text 或 metadata。
func validateQuotedKind(q QuotedOutput) error {
	if q.ContentKind != ContentText && q.ContentKind != ContentMetadata {
		return fmt.Errorf("%w: quoted output cannot carry link/image/control", ErrInertContent)
	}
	return nil
}

// validateQuotedBoundary 校验 fixed non-executable boundary token。
func validateQuotedBoundary(q QuotedOutput) error {
	if q.ExecutionBoundary != QuotedExecutionBoundary {
		return fmt.Errorf("%w: quoted output execution boundary is invalid", ErrQuarantined)
	}
	return nil
}

// validateQuotedMetadata 对 metadata quote 再执行 bounded JSON shape 检查。
func validateQuotedMetadata(q QuotedOutput) error {
	if q.ContentKind != ContentMetadata {
		return nil
	}
	return validateJSONShape(q.body)
}

// validateQuotedMedia 校验 media type 和 inert flag。
func validateQuotedMedia(q QuotedOutput) error {
	if !validText(q.MediaType, 256) || q.Inert {
		return invalidEnvelope("quoted output media/inert flag is invalid")
	}
	return nil
}

// Encode 以固定 length-delimited 字段生成 bounded frame，不做字符串 role 拼接。
func (q QuotedOutput) Encode() ([]byte, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	return encodeQuotedFields(quotedFields(q))
}

// quotedFields 按稳定顺序生成每个 length-delimited 字段，不拼接角色字符串。
func quotedFields(q QuotedOutput) [][]byte {
	return [][]byte{
		[]byte(q.WireVersion), []byte(q.EnvelopeDigest), []byte(q.SourceAgent), []byte(q.SourceIncarnation),
		[]byte(q.SourceTurn), []byte(q.EventCursor), []byte(q.ContentCursor), []byte(q.SourceFence),
		[]byte(q.ContentHash), []byte(q.Provenance.OriginKind), []byte(q.Provenance.SourceSubject),
		[]byte(q.TrustClass), []byte(q.ContentRole), []byte(q.ContentKind), []byte(q.ExecutionBoundary),
		[]byte(q.MediaType), []byte(strconv.FormatInt(q.ContentSize, 10)),
		[]byte(encodeFlags(q.Provenance.VerifiedTransport, q.Truncated)), q.body,
	}
}

// encodeQuotedFields 编码固定字段并在每一步检查 metadata/body/frame 上限。
func encodeQuotedFields(fields [][]byte) ([]byte, error) {
	result := make([]byte, 0, len(fields[quotedFieldCount-1])+1024)
	for index, field := range fields {
		if err := validateQuotedFieldSize(index, len(field)); err != nil {
			return nil, err
		}
		result = appendLengthDelimited(result, field)
		if len(result) > MaxQuotedBytes {
			return nil, fmt.Errorf("%w: quoted frame exceeds bound", ErrJSONTooLarge)
		}
	}
	return result, nil
}

// validateQuotedFieldSize 区分 metadata 字段上限与正文上限。
func validateQuotedFieldSize(index, length int) error {
	limit := 4096
	if index == quotedFieldCount-1 {
		limit = MaxContentBytes
	}
	if length > limit {
		return fmt.Errorf("%w: quoted field exceeds bound", ErrJSONTooLarge)
	}
	return nil
}

// Bytes 返回编码 frame 的独立副本；失败时返回 nil，调用方应先处理 Encode 错误。
func (q QuotedOutput) Bytes() []byte {
	encoded, err := q.Encode()
	if err != nil {
		return nil
	}
	return encoded
}

// ContentBytes 返回 quoted body 的独立副本。
func (q QuotedOutput) ContentBytes() []byte { return append([]byte(nil), q.body...) }

// EncodeQuoted 是 QuotedOutput.Encode 的函数形式。
func EncodeQuoted(output QuotedOutput) ([]byte, error) { return output.Encode() }

// DecodeQuoted 严格解码 length-delimited quoted frame，并拒绝未知角色/字段。
func DecodeQuoted(payload []byte) (QuotedOutput, error) {
	fields, err := readQuotedFields(payload)
	if err != nil {
		return QuotedOutput{}, err
	}
	output, err := decodeQuotedFields(fields)
	if err != nil {
		return QuotedOutput{}, err
	}
	if err := output.Validate(); err != nil {
		return QuotedOutput{}, err
	}
	return output, nil
}

// readQuotedFields 读取固定数量 length-delimited 字段并拒绝尾随数据。
func readQuotedFields(payload []byte) ([][]byte, error) {
	if len(payload) == 0 || len(payload) > MaxQuotedBytes {
		return nil, fmt.Errorf("%w: quoted frame exceeds bound", ErrJSONTooLarge)
	}
	reader := quotedReader{payload: payload}
	fields := make([][]byte, quotedFieldCount)
	for index := range fields {
		field, err := reader.next()
		if err != nil {
			return nil, err
		}
		fields[index] = field
	}
	if !reader.empty() {
		return nil, fmt.Errorf("%w: quoted frame has trailing bytes", ErrInvalidJSON)
	}
	return fields, nil
}

// decodeQuotedFields 将字段解析为结构化 quoted output，不解释正文中的 role 标签。
func decodeQuotedFields(fields [][]byte) (QuotedOutput, error) {
	contentSize, err := strconv.ParseInt(string(fields[16]), 10, 64)
	if err != nil {
		return QuotedOutput{}, fmt.Errorf("%w: quoted content_size is invalid", ErrInvalidJSON)
	}
	verified, truncated, err := decodeFlags(fields[17])
	if err != nil {
		return QuotedOutput{}, err
	}
	return QuotedOutput{
		WireVersion: string(fields[0]), EnvelopeDigest: string(fields[1]), SourceAgent: string(fields[2]),
		SourceIncarnation: string(fields[3]), SourceTurn: string(fields[4]), EventCursor: string(fields[5]),
		ContentCursor: string(fields[6]), SourceFence: string(fields[7]), ContentHash: string(fields[8]),
		ContentSize: contentSize, Provenance: Provenance{OriginKind: OriginKind(fields[9]), SourceSubject: string(fields[10]), VerifiedTransport: verified},
		TrustClass: TrustClass(fields[11]), ContentRole: ContentRole(fields[12]), ContentKind: ContentKind(fields[13]),
		ExecutionBoundary: string(fields[14]), MediaType: string(fields[15]), Truncated: truncated, Inert: false,
		body: append([]byte(nil), fields[18]...),
	}, nil
}

// DecodeQuotedOutput 是 DecodeQuoted 的语义别名。
func DecodeQuotedOutput(payload []byte) (QuotedOutput, error) { return DecodeQuoted(payload) }

func appendLengthDelimited(dst, field []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(field)))
	dst = append(dst, length[:]...)
	return append(dst, field...)
}

type quotedReader struct {
	payload []byte
	offset  int
}

func (r *quotedReader) next() ([]byte, error) {
	if len(r.payload)-r.offset < 4 {
		return nil, fmt.Errorf("%w: quoted field length is truncated", ErrInvalidJSON)
	}
	length := int(binary.BigEndian.Uint32(r.payload[r.offset : r.offset+4]))
	r.offset += 4
	if length < 0 || length > MaxContentBytes || length > len(r.payload)-r.offset {
		return nil, fmt.Errorf("%w: quoted field length exceeds bound", ErrJSONTooLarge)
	}
	field := r.payload[r.offset : r.offset+length]
	r.offset += length
	return field, nil
}

func (r *quotedReader) empty() bool { return r.offset == len(r.payload) }

func encodeFlags(verified, truncated bool) string {
	flags := []byte{'0', '0'}
	if verified {
		flags[0] = '1'
	}
	if truncated {
		flags[1] = '1'
	}
	return string(flags)
}

// decodeFlags 解码 quoted frame 的 verified_transport/truncated 双标志。
func decodeFlags(value []byte) (verified, truncated bool, err error) {
	if len(value) != 2 || (value[0] != '0' && value[0] != '1') || (value[1] != '0' && value[1] != '1') {
		return false, false, fmt.Errorf("%w: quoted flags are invalid", ErrInvalidJSON)
	}
	return value[0] == '1', value[1] == '1', nil
}
