package acpnode

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

var (
	rawMessageType    = reflect.TypeOf(json.RawMessage(nil))
	jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

func outboundTooLarge(max int, detail string) error {
	return fmt.Errorf("%w: %s exceeds %d bytes", ErrOutboundMessageTooLarge, detail, max)
}

func addBoundedSize(total, addition, max int) (int, error) {
	if addition < 0 || total > max-addition {
		return 0, outboundTooLarge(max, "JSON value")
	}
	return total + addition, nil
}

func addBoundedField(total *int, name string, valueSize, max int) error {
	addition := len(name) + 3 + valueSize
	if *total > 2 {
		addition++
	}
	var err error
	*total, err = addBoundedSize(*total, addition, max)
	return err
}

func addBoundedRawField(total *int, name string, raw json.RawMessage, max int) error {
	if raw == nil {
		return nil
	}
	if len(raw) > max {
		return outboundTooLarge(max, name)
	}
	return addBoundedField(total, name, len(raw), max)
}

// preflightMessageBounded 在 encoding/json 进入无界 encodeState 前检查完整 envelope。
func preflightMessageBounded(m Message, max int) error {
	total := 2
	if err := addMessageHeader(&total, m, max); err != nil {
		return err
	}
	if err := addMessageBody(&total, m, max); err != nil {
		return err
	}
	if total > max {
		return outboundTooLarge(max, "outbound message")
	}
	return nil
}

// addMessageHeader 累加固定 envelope 字段及其方法、标识符大小。
func addMessageHeader(total *int, m Message, max int) error {
	jsonrpcSize, err := estimateJSONString(m.JSONRPC, max)
	if err != nil {
		return err
	}
	if err := addBoundedField(total, "jsonrpc", jsonrpcSize, max); err != nil {
		return err
	}
	if len(m.ID) > max {
		return outboundTooLarge(max, "JSON-RPC id")
	}
	if err := addBoundedRawField(total, "id", m.ID, max); err != nil {
		return err
	}
	if m.Method == "" {
		return nil
	}
	methodSize, err := estimateJSONString(m.Method, max)
	if err != nil {
		return err
	}
	return addBoundedField(total, "method", methodSize, max)
}

// addMessageBody 累加 params、result 或 error 的有界原始 JSON 大小。
func addMessageBody(total *int, m Message, max int) error {
	if err := addBoundedRawField(total, "params", m.Params, max); err != nil {
		return err
	}
	if err := addBoundedRawField(total, "result", m.Result, max); err != nil {
		return err
	}
	if m.Error == nil {
		return nil
	}
	errorSize, err := estimateRPCError(m.Error, max)
	if err != nil {
		return err
	}
	return addBoundedField(total, "error", errorSize, max)
}

func estimateRPCError(rpcErr *RPCError, max int) (int, error) {
	total := 2
	if err := addBoundedField(&total, "code", 24, max); err != nil {
		return 0, err
	}
	messageSize, err := estimateJSONString(rpcErr.Message, max)
	if err != nil {
		return 0, err
	}
	if err := addBoundedField(&total, "message", messageSize, max); err != nil {
		return 0, err
	}
	if err := addBoundedRawField(&total, "data", rpcErr.Data, max); err != nil {
		return 0, err
	}
	return total, nil
}

func estimateJSONString(value string, max int) (int, error) {
	size := 2
	for index := 0; index < len(value); {
		addition, width := encodedJSONStringRune(value[index:])
		var err error
		size, err = addBoundedSize(size, addition, max)
		if err != nil {
			return 0, outboundTooLarge(max, "JSON string")
		}
		index += width
	}
	return size, nil
}

// encodedJSONStringRune 计算单个 UTF-8 单元在 JSON 字符串中的保守长度。
func encodedJSONStringRune(value string) (addition, width int) {
	c := value[0]
	switch {
	case c == '"' || c == '\\':
		return 2, 1
	case c < 0x20:
		return encodedControlRune(c)
	case c == '<' || c == '>' || c == '&':
		return 6, 1
	case c < utf8.RuneSelf:
		return 1, 1
	}
	return encodedUnicodeRune(value)
}

func encodedControlRune(c byte) (addition, width int) {
	if strings.ContainsRune("\b\f\n\r\t", rune(c)) {
		return 2, 1
	}
	return 6, 1
}

func encodedUnicodeRune(value string) (addition, width int) {
	r, width := utf8.DecodeRuneInString(value)
	if r == utf8.RuneError && width == 1 || r == '\u2028' || r == '\u2029' {
		return 6, width
	}
	return width, width
}

func preflightJSONValue(value any, max int) error {
	if max <= 0 {
		return fmt.Errorf("acp: invalid JSON bound %d", max)
	}
	_, err := estimateJSONValue(reflect.ValueOf(value), max, 0)
	return err
}

// estimateJSONValue 递归估算标准 JSON 值并在遇到无界 marshaler 时 fail-fast。
func estimateJSONValue(value reflect.Value, max, depth int) (int, error) {
	if depth > MaxJSONDepth {
		return 0, fmt.Errorf("acp: JSON depth exceeds %d", MaxJSONDepth)
	}
	if !value.IsValid() {
		return 4, nil
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return 4, nil
		}
		return estimateJSONValue(value.Elem(), max, depth+1)
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 4, nil
		}
		if value.Type().Elem() == rawMessageType {
			return estimateRawMessage(value.Elem(), max)
		}
		return estimateJSONValue(value.Elem(), max, depth+1)
	}
	if value.Type() == rawMessageType {
		return estimateRawMessage(value, max)
	}
	if err := rejectUnboundedMarshaler(value, max); err != nil {
		return 0, err
	}
	return estimateJSONKind(value, max, depth)
}

func rejectUnboundedMarshaler(value reflect.Value, max int) error {
	typeOfValue := value.Type()
	if typeOfValue.Implements(jsonMarshalerType) || reflect.PointerTo(typeOfValue).Implements(jsonMarshalerType) {
		return outboundTooLarge(max, "custom json.Marshaler")
	}
	if typeOfValue.Implements(textMarshalerType) || reflect.PointerTo(typeOfValue).Implements(textMarshalerType) {
		return outboundTooLarge(max, "custom text marshaler")
	}
	return nil
}

// estimateJSONKind 按 JSON 基础类型分派到精确或保守大小估算器。
func estimateJSONKind(value reflect.Value, max, depth int) (int, error) {
	switch value.Kind() {
	case reflect.Bool:
		if value.Bool() {
			return 4, nil
		}
		return 5, nil
	case reflect.String:
		return estimateJSONString(value.String(), max)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return 24, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return 24, nil
	case reflect.Float32, reflect.Float64:
		return 32, nil
	case reflect.Slice, reflect.Array:
		return estimateJSONCollection(value, max, depth)
	case reflect.Map:
		return estimateJSONMap(value, max, depth)
	case reflect.Struct:
		return estimateJSONStruct(value, max, depth)
	default:
		return 0, &json.UnsupportedTypeError{Type: value.Type()}
	}
}

func estimateJSONCollection(value reflect.Value, max, depth int) (int, error) {
	if value.Kind() == reflect.Slice && value.IsNil() {
		return 4, nil
	}
	if value.Type().Elem().Kind() == reflect.Uint8 {
		return estimateJSONBytes(value.Len(), max)
	}
	return estimateJSONSequence(value, max, depth)
}

func estimateJSONBytes(length, max int) (int, error) {
	if length > (max-2)/4*3+2 {
		return 0, outboundTooLarge(max, "JSON bytes")
	}
	return 2 + ((length+2)/3)*4, nil
}

// estimateJSONSequence 估算数组并限制元素数量和递归深度。
func estimateJSONSequence(value reflect.Value, max, depth int) (int, error) {
	if value.Len() > MaxMembers {
		return 0, fmt.Errorf("acp: JSON members exceed %d", MaxMembers)
	}
	size := 2
	for index := 0; index < value.Len(); index++ {
		itemSize, err := estimateJSONValue(value.Index(index), max, depth+1)
		if err != nil {
			return 0, err
		}
		size, err = addBoundedSize(size, itemSize+1, max)
		if err != nil {
			return 0, err
		}
	}
	if value.Len() > 0 {
		size--
	}
	return size, nil
}

// estimateJSONMap 估算对象键值对并拒绝无界自定义键编码器。
func estimateJSONMap(value reflect.Value, max, depth int) (int, error) {
	if value.IsNil() {
		return 4, nil
	}
	if value.Len() > MaxMembers {
		return 0, fmt.Errorf("acp: JSON members exceed %d", MaxMembers)
	}
	size := 2
	iter := value.MapRange()
	for iter.Next() {
		keySize, err := estimateJSONMapKey(iter.Key(), max)
		if err != nil {
			return 0, err
		}
		valueSize, err := estimateJSONValue(iter.Value(), max, depth+1)
		if err != nil {
			return 0, err
		}
		size, err = addBoundedSize(size, keySize+valueSize+2, max)
		if err != nil {
			return 0, err
		}
	}
	if value.Len() > 0 {
		size--
	}
	return size, nil
}

// estimateJSONStruct 按 JSON tags 估算结构体字段的保守 envelope 大小。
func estimateJSONStruct(value reflect.Value, max, depth int) (int, error) {
	size := 2
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		if field.PkgPath != "" && !field.Anonymous {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fieldSize, err := estimateJSONValue(value.Field(index), max, depth+1)
		if err != nil {
			return 0, err
		}
		size, err = addBoundedSize(size, len(name)+fieldSize+4, max)
		if err != nil {
			return 0, err
		}
	}
	if size > 2 {
		size--
	}
	return size, nil
}

func estimateRawMessage(value reflect.Value, max int) (int, error) {
	if value.IsNil() {
		return 4, nil
	}
	if value.Len() > max {
		return 0, outboundTooLarge(max, "json.RawMessage")
	}
	return value.Len(), nil
}

func estimateJSONMapKey(key reflect.Value, max int) (int, error) {
	if key.Type().Implements(textMarshalerType) || reflect.PointerTo(key.Type()).Implements(textMarshalerType) {
		return 0, outboundTooLarge(max, "custom text marshaler")
	}
	switch key.Kind() {
	case reflect.String:
		return estimateJSONString(key.String(), max)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return 26, nil
	default:
		return 0, &json.UnsupportedTypeError{Type: key.Type()}
	}
}
