package acpnode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ID is the lossless JSON-RPC request identifier used on the wire.
type ID struct {
	raw json.RawMessage
	key string
}

// String 返回不经浮点转换的 JSON-RPC 标识原文。
// String returns the original bytes retained for wire serialization.
func (i ID) String() string { return string(i.raw) }

// decodeID 只接受官方 null、字符串和有界 int64，并把原文字节与语义键分开保存。
func decodeID(raw json.RawMessage) (ID, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return ID{}, fmt.Errorf("acp: invalid json-rpc id")
	}
	lossless := cloneRaw(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return ID{raw: lossless, key: "null"}, nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return ID{}, fmt.Errorf("acp: invalid json-rpc string id: %w", err)
		}
		return ID{raw: lossless, key: "string:" + s}, nil
	}
	value, err := parseJSONInt64(trimmed)
	if err == nil {
		return ID{raw: lossless, key: "int:" + strconv.FormatInt(value, 10)}, nil
	}
	return ID{}, fmt.Errorf("acp: json-rpc id must be null, an int64, or a string")
}

// semanticIDKey 返回仅用于查找、去重和取消的 type-tagged 语义键。
func semanticIDKey(raw json.RawMessage) (string, error) {
	id, err := decodeID(raw)
	if err != nil {
		return "", err
	}
	return id.key, nil
}

// parseJSONInt64 将所有精确表示 int64 的合法 JSON 数字编码归一化。
func parseJSONInt64(raw []byte) (int64, error) {
	mantissa, negative, exponent, err := splitJSONNumber(string(raw))
	if err != nil {
		return 0, err
	}
	digits, fractionScale, err := splitJSONMantissa(mantissa)
	if err != nil {
		return 0, err
	}
	if strings.Trim(digits, "0") == "" {
		return 0, nil
	}
	coefficient, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return 0, fmt.Errorf("malformed JSON number")
	}
	return normalizeJSONInt64(coefficient, fractionScale-exponent, negative, len(raw))
}

// splitJSONNumber 拆分 JSON 数字的符号、尾数和指数。
func splitJSONNumber(text string) (string, bool, int64, error) {
	if text == "" || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) {
		return "", false, 0, fmt.Errorf("not a JSON number")
	}
	negative := text[0] == '-'
	if negative {
		text = text[1:]
	}
	if text == "" {
		return "", false, 0, fmt.Errorf("not a JSON number")
	}
	exponent := int64(0)
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		parsed, err := strconv.ParseInt(text[index+1:], 10, 64)
		if err != nil {
			return "", false, 0, fmt.Errorf("exponent is out of range")
		}
		exponent = parsed
		text = text[:index]
	}
	return text, negative, exponent, nil
}

// splitJSONMantissa 合并整数和小数数字，并返回小数位数。
func splitJSONMantissa(text string) (string, int64, error) {
	integerPart, fractionPart, hasFraction := strings.Cut(text, ".")
	if integerPart == "" || (hasFraction && fractionPart == "") {
		return "", 0, fmt.Errorf("malformed JSON number")
	}
	return integerPart + fractionPart, int64(len(fractionPart)), nil
}

// normalizeJSONInt64 以有界大整数执行精确整除和 int64 范围校验。
func normalizeJSONInt64(coefficient *big.Int, scale int64, negative bool, rawLength int) (int64, error) {
	// A value outside this lexical bound cannot be an int64 unless it is zero,
	// which was handled above; keep the conversion bounded before allocating.
	if scale > int64(rawLength)+1 || scale < -int64(rawLength)-1 {
		return 0, fmt.Errorf("number is outside int64")
	}
	if scale > 0 {
		denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(scale), nil)
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(coefficient, denominator, remainder)
		if remainder.Sign() != 0 {
			return 0, fmt.Errorf("fractional JSON number")
		}
		coefficient = quotient
	} else if scale < 0 {
		coefficient.Mul(coefficient, new(big.Int).Exp(big.NewInt(10), big.NewInt(-scale), nil))
	}
	if negative {
		coefficient.Neg(coefficient)
	}
	if !coefficient.IsInt64() {
		return 0, fmt.Errorf("number is outside int64")
	}
	return coefficient.Int64(), nil
}

// Message is the bounded JSON-RPC 2.0 envelope used by the ACP peer.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// readMessage reads exactly one newline-delimited JSON value without reading
// ahead. The no-buffering rule is important because callers may pass an
// io.Reader that is shared with the next protocol message.
func readMessage(r io.Reader, max int) (Message, error) {
	if err := validateReaderBounds(r, max); err != nil {
		return Message{}, err
	}
	b, err := readLine(r, max)
	if err != nil {
		return Message{}, err
	}
	trimmed, err := validateWireBytes(b)
	if err != nil {
		return Message{}, err
	}
	return decodeWireMessage(trimmed)
}

func validateReaderBounds(r io.Reader, max int) error {
	if r == nil {
		return fmt.Errorf("acp: nil wire reader")
	}
	if max <= 0 {
		return fmt.Errorf("acp: invalid wire bound")
	}
	return nil
}

// validateWireBytes 拒绝 BOM、NUL、非法 UTF-8 和超出结构边界的线协议。
func validateWireBytes(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("acp: empty wire message")
	}
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return nil, fmt.Errorf("acp: contaminated wire")
	}
	if bytes.Contains(b, []byte{0xef, 0xbb, 0xbf}) {
		return nil, fmt.Errorf("acp: bom is not allowed")
	}
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("acp: wire value must be an object")
	}
	if err := validateJSONValue(trimmed, MaxJSONDepth, MaxMembers); err != nil {
		return nil, err
	}
	return trimmed, nil
}

func decodeWireMessage(trimmed []byte) (Message, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return Message{}, fmt.Errorf("acp: decode envelope fields: %w", err)
	}
	var m Message
	if err := json.Unmarshal(trimmed, &m); err != nil {
		return Message{}, fmt.Errorf("acp: decode envelope: %w", err)
	}
	if err := validateEnvelope(m, fields); err != nil {
		return Message{}, err
	}
	return m, nil
}

// readLine 逐字节读取单条消息，避免缓冲器越界消费下一条协议帧。
func readLine(r io.Reader, max int) ([]byte, error) {
	line := make([]byte, 0, minInt(max, 4096))
	var one [1]byte
	noProgress := 0
	for {
		progress, done, err := readLineStep(r, &line, max, one[:])
		if done {
			return line, nil
		}
		if err != nil {
			return nil, err
		}
		if progress {
			noProgress = 0
		} else {
			noProgress++
			if noProgress >= 100 {
				return nil, io.ErrNoProgress
			}
		}
	}
}

// readLineStep 执行一次有界读取并区分换行、进展和底层错误。
func readLineStep(r io.Reader, line *[]byte, max int, one []byte) (progress, done bool, err error) {
	n, readErr := r.Read(one)
	if n > 0 {
		if one[0] == '\n' {
			if len(*line) > 0 && (*line)[len(*line)-1] == '\r' {
				*line = (*line)[:len(*line)-1]
			}
			return true, true, nil
		}
		*line = append(*line, one[0])
		if len(*line) > max {
			return true, false, fmt.Errorf("acp: message exceeds %d bytes", max)
		}
	}
	if readErr != nil {
		if len(*line) > 0 {
			return n > 0, false, fmt.Errorf("acp: unterminated wire message: %w", readErr)
		}
		return n > 0, false, readErr
	}
	return n > 0, false, nil
}

// validateJSONValue 以令牌遍历方式检查深度、成员数和重复键。
// validateJSONValue performs a duplicate-key-aware, bounded token walk.
func validateJSONValue(raw []byte, maxDepth, maxMembers int) error {
	if maxDepth <= 0 || maxMembers <= 0 {
		return fmt.Errorf("acp: invalid JSON bounds")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	members := 0
	if err := validateJSONToken(dec, 0, maxDepth, maxMembers, &members); err != nil {
		return err
	}
	if token, err := dec.Token(); err != io.EOF {
		if err != nil {
			return fmt.Errorf("acp: trailing JSON: %w", err)
		}
		return fmt.Errorf("acp: trailing JSON token %v", token)
	}
	return nil
}

// validateJSONToken 递归验证对象或数组边界，并把底层解析错误原样上报。
func validateJSONToken(dec *json.Decoder, depth, maxDepth, maxMembers int, members *int) error {
	token, err := dec.Token()
	if err != nil {
		return fmt.Errorf("acp: JSON token: %w", err)
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}
	if depth >= maxDepth {
		return fmt.Errorf("acp: JSON depth exceeds %d", maxDepth)
	}
	switch delim {
	case '{':
		return validateJSONObject(dec, depth, maxDepth, maxMembers, members)
	case '[':
		return validateJSONArray(dec, depth, maxDepth, maxMembers, members)
	default:
		return fmt.Errorf("acp: unexpected JSON delimiter %q", delim)
	}
}

// validateJSONObject 检查对象键唯一性并累计嵌套成员预算。
func validateJSONObject(dec *json.Decoder, depth, maxDepth, maxMembers int, members *int) error {
	seen := make(map[string]struct{})
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return fmt.Errorf("acp: JSON object key: %w", err)
		}
		name, ok := key.(string)
		if !ok {
			return fmt.Errorf("acp: JSON object key is not a string")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("acp: duplicate JSON object key %q", name)
		}
		seen[name] = struct{}{}
		if err := countJSONMember(members, maxMembers); err != nil {
			return err
		}
		if err := validateJSONToken(dec, depth+1, maxDepth, maxMembers, members); err != nil {
			return err
		}
	}
	return expectJSONDelimiter(dec, '}', "object")
}

func validateJSONArray(dec *json.Decoder, depth, maxDepth, maxMembers int, members *int) error {
	for dec.More() {
		if err := countJSONMember(members, maxMembers); err != nil {
			return err
		}
		if err := validateJSONToken(dec, depth+1, maxDepth, maxMembers, members); err != nil {
			return err
		}
	}
	return expectJSONDelimiter(dec, ']', "array")
}

func countJSONMember(members *int, maxMembers int) error {
	*members++
	if *members > maxMembers {
		return fmt.Errorf("acp: JSON members exceed %d", maxMembers)
	}
	return nil
}

func expectJSONDelimiter(dec *json.Decoder, want json.Delim, kind string) error {
	end, err := dec.Token()
	if err != nil {
		return fmt.Errorf("acp: JSON %s end: %w", kind, err)
	}
	if end != want {
		return fmt.Errorf("acp: malformed JSON %s", kind)
	}
	return nil
}

// validateEnvelope 按 JSON-RPC 请求、通知或响应形态执行互斥字段守卫。
func validateEnvelope(m Message, fields map[string]json.RawMessage) error {
	if m.JSONRPC != "2.0" {
		return fmt.Errorf("acp: jsonrpc must be 2.0")
	}
	_, hasID := fields["id"]
	_, hasMethod := fields["method"]
	_, hasParams := fields["params"]
	_, hasResult := fields["result"]
	_, hasError := fields["error"]
	if hasID {
		if _, err := decodeID(m.ID); err != nil {
			return err
		}
	}
	if hasMethod {
		return validateRequestEnvelope(m, hasParams, hasResult, hasError)
	}
	if !hasID {
		return fmt.Errorf("acp: envelope has neither method nor id")
	}
	return validateResponseEnvelope(m, fields, hasParams, hasResult, hasError)
}

// validateRequestEnvelope 执行请求、通知和 params shape 的互斥字段守卫。
func validateRequestEnvelope(m Message, hasParams, hasResult, hasError bool) error {
	if m.Method == "" {
		return fmt.Errorf("acp: method must not be empty")
	}
	if hasResult || hasError {
		return fmt.Errorf("acp: request cannot contain result or error")
	}
	if hasParams {
		if err := validateParamsShape(m.Params, m.Method); err != nil {
			return err
		}
	}
	return nil
}

func validateParamsShape(raw json.RawMessage, method string) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		if method == "" {
			return fmt.Errorf("acp: params must be an object or array")
		}
		return fmt.Errorf("acp: %s params must be an object or array", method)
	}
	return nil
}

// validateResponseEnvelope 强制响应带精确一个 result 或 error 且保留合法 id。
func validateResponseEnvelope(m Message, fields map[string]json.RawMessage, hasParams, hasResult, hasError bool) error {
	if hasParams {
		return fmt.Errorf("acp: response cannot contain params")
	}
	if hasResult == hasError {
		return fmt.Errorf("acp: response must contain exactly one of result or error")
	}
	if hasError {
		if m.Error == nil {
			return fmt.Errorf("acp: invalid error response")
		}
		var errorFields map[string]json.RawMessage
		if err := json.Unmarshal(fields["error"], &errorFields); err != nil {
			return fmt.Errorf("acp: invalid error response: %w", err)
		}
		if _, ok := errorFields["code"]; !ok {
			return fmt.Errorf("acp: error code is required")
		}
		if _, ok := errorFields["message"]; !ok {
			return fmt.Errorf("acp: error message is required")
		}
	}
	return nil
}

func marshalMessage(m Message) ([]byte, error) {
	return marshalMessageBounded(m, DefaultMaxMessage)
}

// ErrOutboundMessageTooLarge identifies a bounded preflight rejection before
// any pending entry or wire write is created.
var ErrOutboundMessageTooLarge = errors.New("acp: outbound message exceeds limit")

type boundedJSONBuffer struct {
	bytes.Buffer
	max int
}

// Write 将编码器输出限制在预检后的 envelope 上限内。
func (b *boundedJSONBuffer) Write(p []byte) (int, error) {
	if len(p) > b.max-b.Len() {
		return 0, ErrOutboundMessageTooLarge
	}
	return b.Buffer.Write(p)
}

// marshalMessageBounded 先执行无界 marshaler/RawMessage 预检，再编码有界 JSON。
func marshalMessageBounded(m Message, max int) ([]byte, error) {
	if max <= 0 {
		return nil, fmt.Errorf("acp: invalid outbound message bound %d", max)
	}
	if err := preflightMessageBounded(m, max); err != nil {
		return nil, err
	}
	if err := validateOutgoingEnvelope(m); err != nil {
		return nil, err
	}
	buffer := &boundedJSONBuffer{max: max + 1}
	encoder := json.NewEncoder(buffer)
	if err := encoder.Encode(m); err != nil {
		if errors.Is(err, ErrOutboundMessageTooLarge) {
			return nil, fmt.Errorf("%w: outbound message exceeds %d bytes", ErrOutboundMessageTooLarge, max)
		}
		return nil, err
	}
	payload := buffer.Bytes()
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		return nil, fmt.Errorf("acp: bounded JSON encoder omitted line terminator")
	}
	payload = payload[:len(payload)-1]
	if len(payload) > max {
		return nil, fmt.Errorf("%w: outbound message exceeds %d bytes", ErrOutboundMessageTooLarge, max)
	}
	return append([]byte(nil), payload...), nil
}

func validateOutgoingEnvelope(m Message) error {
	if m.JSONRPC != "2.0" {
		return fmt.Errorf("acp: jsonrpc must be 2.0")
	}
	if len(m.ID) > 0 {
		if _, err := decodeID(m.ID); err != nil {
			return err
		}
	}
	if m.Method != "" {
		return validateOutgoingRequest(m)
	}
	return validateOutgoingResponse(m)
}

// validateOutgoingRequest 校验出站请求 envelope 以及 object/array params。
func validateOutgoingRequest(m Message) error {
	if m.Result != nil || m.Error != nil {
		return fmt.Errorf("acp: request cannot contain result or error")
	}
	if m.Params == nil {
		return nil
	}
	if err := validateJSONValue(m.Params, MaxJSONDepth, MaxMembers); err != nil {
		return fmt.Errorf("acp: invalid params: %w", err)
	}
	if err := validateParamsShape(m.Params, m.Method); err != nil {
		return err
	}
	return nil
}

func validateOutgoingResponse(m Message) error {
	if len(m.ID) == 0 || (m.Result == nil) == (m.Error == nil) {
		return fmt.Errorf("acp: invalid response envelope")
	}
	if m.Result == nil {
		return validateOutgoingError(m.Error)
	}
	if err := validateJSONValue(m.Result, MaxJSONDepth, MaxMembers); err != nil {
		return fmt.Errorf("acp: invalid result: %w", err)
	}
	return nil
}

func validateOutgoingError(rpcErr *RPCError) error {
	if rpcErr == nil {
		return fmt.Errorf("acp: invalid error response")
	}
	if rpcErr.Data == nil {
		return nil
	}
	if err := validateJSONValue(rpcErr.Data, MaxJSONDepth, MaxMembers); err != nil {
		return fmt.Errorf("acp: invalid error data: %w", err)
	}
	return nil
}

func writeMessage(w io.Writer, m Message) error {
	if w == nil {
		return fmt.Errorf("acp: nil wire writer")
	}
	b, err := marshalMessage(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	n, err := w.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

func writeMessageBounded(w io.Writer, m Message, max int) error {
	b, err := marshalMessageBounded(m, max)
	if err != nil {
		return err
	}
	b = appendWireLine(b)
	n, err := w.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

// writeMessageBoundedContext 将序列化写入置于上下文和时限内，阻塞时主动关闭流。
func writeMessageBoundedContext(ctx context.Context, w io.WriteCloser, m Message, max int, timeout time.Duration, closeWriter func() error) error {
	return writeMessageBoundedContextTracked(ctx, w, m, max, timeout, closeWriter, nil)
}

// writeMessageBoundedContextTracked 追踪可阻塞协议写入并在超时后保留 owner。
func writeMessageBoundedContextTracked(ctx context.Context, w io.WriteCloser, m Message, max int, timeout time.Duration, closeWriter func() error, track func(*writeOwner)) error {
	if ctx == nil {
		return fmt.Errorf("acp: nil write context")
	}
	if w == nil {
		return fmt.Errorf("acp: nil wire writer")
	}
	if timeout <= 0 {
		return fmt.Errorf("acp: invalid write timeout")
	}
	b, err := marshalMessageBounded(m, max)
	if err != nil {
		return err
	}
	b = appendWireLine(b)
	if closeWriter == nil {
		closeWriter = w.Close
	}
	return writeBytesBoundedContextTracked(ctx, w, b, timeout, closeWriter, track)
}

func appendWireLine(payload []byte) []byte {
	withLine := make([]byte, len(payload)+1)
	copy(withLine, payload)
	withLine[len(payload)] = '\n'
	return withLine
}

// response preserves the small helper used by focused wire tests. Marshal
// failures are converted to a valid JSON-RPC error-only response.
func response(id json.RawMessage, result any, rpcErr *RPCError) Message {
	if rpcErr != nil {
		return Message{JSONRPC: "2.0", ID: cloneRaw(id), Error: cloneRPCError(rpcErr)}
	}
	b, err := mustJSON(result)
	if err != nil {
		return Message{JSONRPC: "2.0", ID: cloneRaw(id), Error: &RPCError{Code: -32603, Message: "internal error"}}
	}
	return Message{JSONRPC: "2.0", ID: cloneRaw(id), Result: b}
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func cloneRPCError(e *RPCError) *RPCError {
	if e == nil {
		return nil
	}
	return &RPCError{Code: e.Code, Message: e.Message, Data: cloneRaw(e.Data)}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// mustJSON is retained as an explicit error-returning helper so callers
// cannot silently turn marshal failures into malformed protocol payloads.
func mustJSON(v any) (json.RawMessage, error) {
	return mustJSONBounded(v, DefaultMaxMessage)
}

// mustJSONBounded 预检值并以同一 wire 上限编码，确保 pending admission 前 fail-fast。
func mustJSONBounded(v any, max int) (json.RawMessage, error) {
	if max <= 0 {
		return nil, fmt.Errorf("acp: invalid JSON bound %d", max)
	}
	if err := preflightJSONValue(v, max); err != nil {
		return nil, err
	}
	buffer := &boundedJSONBuffer{max: max + 1}
	encoder := json.NewEncoder(buffer)
	if err := encoder.Encode(v); err != nil {
		if errors.Is(err, ErrOutboundMessageTooLarge) {
			return nil, fmt.Errorf("%w: JSON value exceeds %d bytes", ErrOutboundMessageTooLarge, max)
		}
		return nil, err
	}
	payload := buffer.Bytes()
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		return nil, fmt.Errorf("acp: bounded JSON encoder omitted line terminator")
	}
	payload = payload[:len(payload)-1]
	if len(payload) > max {
		return nil, fmt.Errorf("%w: JSON value exceeds %d bytes", ErrOutboundMessageTooLarge, max)
	}
	if err := validateJSONValue(payload, MaxJSONDepth, MaxMembers); err != nil {
		return nil, err
	}
	return cloneRaw(payload), nil
}

// methodParamsObject 要求指定方法的 params 是对象并执行统一 JSON 边界检查。
func methodParamsObject(raw json.RawMessage, method string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("acp: %s params are required", method)
	}
	if err := validateJSONValue(raw, MaxJSONDepth, MaxMembers); err != nil {
		return nil, fmt.Errorf("acp: %s params: %w", method, err)
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil || params == nil {
		if err != nil {
			return nil, fmt.Errorf("acp: %s params object: %w", method, err)
		}
		return nil, fmt.Errorf("acp: %s params must be an object", method)
	}
	return params, nil
}

func requiredString(params map[string]json.RawMessage, name string) (string, error) {
	raw, ok := params[name]
	if !ok {
		return "", fmt.Errorf("acp: missing %s", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		if err != nil {
			return "", fmt.Errorf("acp: invalid %s: %w", name, err)
		}
		return "", fmt.Errorf("acp: %s must not be empty", name)
	}
	return value, nil
}

func cloneRawMap(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for key, value := range in {
		out[key] = cloneRaw(value)
	}
	return out
}
