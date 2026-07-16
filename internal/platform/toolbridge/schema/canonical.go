package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxRawSchemaBytes       = 256 * 1024
	maxCanonicalSchemaBytes = 256 * 1024
	maxDecodedValues        = 8192
	maxNestingDepth         = 64
	maxObjectMembers        = 4096
	maxSingleObjectMembers  = 2048
	maxArrayElements        = 4096
	maxSingleArrayElements  = 2048
	maxReferenceKeywords    = 256
	maxRegexStrings         = 128
	maxRegexStringBytes     = 4 * 1024
	maxRegexBytes           = 32 * 1024
	maxJSONStringBytes      = 16 * 1024
	maxJSONStringsBytes     = 192 * 1024
	maxNumberLexemeBytes    = 128
)

const defaultDraftURI = "https://json-schema.org/draft/2020-12/schema"

var supportedDraftURIs = map[string]struct{}{
	"http://json-schema.org/draft-04/schema":       {},
	"http://json-schema.org/draft-06/schema":       {},
	"http://json-schema.org/draft-07/schema":       {},
	"https://json-schema.org/draft/2019-09/schema": {},
	defaultDraftURI: {},
}

type valueKind uint8

const (
	kindNull valueKind = iota
	kindBool
	kindString
	kindNumber
	kindObject
	kindArray
)

type jsonValue struct {
	kind    valueKind
	text    string
	boolean bool
	object  map[string]*jsonValue
	array   []*jsonValue
}

// CanonicalSchema 是通过冻结预算扫描后的唯一 schema 身份。
type CanonicalSchema struct {
	Bytes  json.RawMessage
	Digest string
	Draft  string
}

type scanner struct {
	decoder       *json.Decoder
	values        int
	objectMembers int
	arrayElements int
	stringBytes   int
	references    int
	regexStrings  int
	regexBytes    int
}

// Canonicalize 严格解析、预算扫描并生成确定性 JSON bytes 和 SHA-256。
func Canonicalize(raw []byte) (CanonicalSchema, error) {
	if len(raw) > maxRawSchemaBytes {
		return CanonicalSchema{}, newDiagnostic(CodeInputTooLarge, "raw schema exceeds 256 KiB", nil)
	}
	root, _, err := parseJSON(raw, true)
	if err != nil {
		return CanonicalSchema{}, err
	}
	if root.kind != kindObject {
		return CanonicalSchema{}, newDiagnostic(CodeRootNotObject, "schema root must be an object", nil)
	}
	typeValue, ok := root.object["type"]
	if !ok || typeValue.kind != kindString || typeValue.text != "object" {
		return CanonicalSchema{}, newDiagnostic(CodeRootNotObject, "schema root type must be exactly object", nil)
	}
	draft := defaultDraftURI
	if schemaValue, ok := root.object["$schema"]; ok {
		draft = schemaValue.text
	}
	var canonical bytes.Buffer
	encodeCanonical(&canonical, root)
	if canonical.Len() > maxCanonicalSchemaBytes {
		return CanonicalSchema{}, newDiagnostic(CodeInputTooLarge, "canonical schema exceeds 256 KiB", nil)
	}
	sum := sha256.Sum256(canonical.Bytes())
	return CanonicalSchema{
		Bytes:  append(json.RawMessage(nil), canonical.Bytes()...),
		Digest: hex.EncodeToString(sum[:]),
		Draft:  draft,
	}, nil
}

// parseJSON 严格解码单个 JSON 值，并按模式执行 schema 结构预扫描。
func parseJSON(raw []byte, schemaMode bool) (*jsonValue, *scanner, error) {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return nil, nil, newDiagnostic(CodeInvalidEnvelope, "JSON must be non-empty valid UTF-8", nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	scan := &scanner{decoder: decoder}
	root, err := scan.readValue(1)
	if err != nil {
		return nil, nil, err
	}
	if schemaMode && root.kind == kindObject {
		if err := scan.scanSchema(root); err != nil {
			return nil, nil, err
		}
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected trailing token %v", token)
		}
		return nil, nil, newDiagnostic(CodeInvalidEnvelope, "JSON has trailing bytes", err)
	}
	return root, scan, nil
}

func (s *scanner) readValue(depth int) (*jsonValue, error) {
	if depth > maxNestingDepth {
		return nil, newDiagnostic(CodeBudgetExceeded, "JSON nesting depth exceeds 64", nil)
	}
	s.values++
	if s.values > maxDecodedValues {
		return nil, newDiagnostic(CodeBudgetExceeded, "decoded JSON values exceed 8192", nil)
	}
	token, err := s.decoder.Token()
	if err != nil {
		return nil, newDiagnostic(CodeInvalidEnvelope, "invalid JSON value", err)
	}
	return s.valueFromToken(token, depth)
}

// valueFromToken 将已计入预算的 token 转换为内部 JSON 值。
func (s *scanner) valueFromToken(token json.Token, depth int) (*jsonValue, error) {
	switch value := token.(type) {
	case json.Delim:
		return s.readDelimitedValue(value, depth)
	case string:
		return s.readStringValue(value)
	case json.Number:
		return readNumberValue(value)
	case bool:
		return &jsonValue{kind: kindBool, boolean: value}, nil
	case nil:
		return &jsonValue{kind: kindNull}, nil
	default:
		return nil, newDiagnostic(CodeInvalidEnvelope, "unsupported JSON token", nil)
	}
}

func (s *scanner) readDelimitedValue(delimiter json.Delim, depth int) (*jsonValue, error) {
	switch delimiter {
	case '{':
		return s.readObject(depth)
	case '[':
		return s.readArray(depth)
	default:
		return nil, newDiagnostic(CodeInvalidEnvelope, "unexpected closing delimiter", nil)
	}
}

func (s *scanner) readStringValue(value string) (*jsonValue, error) {
	if err := s.countString(value); err != nil {
		return nil, err
	}
	return &jsonValue{kind: kindString, text: value}, nil
}

func readNumberValue(value json.Number) (*jsonValue, error) {
	if len(value.String()) > maxNumberLexemeBytes {
		return nil, newDiagnostic(CodeBudgetExceeded, "numeric lexeme exceeds 128 bytes", nil)
	}
	return &jsonValue{kind: kindNumber, text: value.String()}, nil
}

func (s *scanner) readObject(depth int) (*jsonValue, error) {
	object := make(map[string]*jsonValue)
	members := 0
	for s.decoder.More() {
		members++
		if err := s.readObjectMember(object, members, depth); err != nil {
			return nil, err
		}
	}
	if err := s.readClosingDelimiter('}', "object is not terminated"); err != nil {
		return nil, err
	}
	return &jsonValue{kind: kindObject, object: object}, nil
}

func (s *scanner) readObjectMember(object map[string]*jsonValue, members, depth int) error {
	key, err := s.readObjectKey(object)
	if err != nil {
		return err
	}
	if err := s.recordObjectMember(members); err != nil {
		return err
	}
	child, err := s.readValue(depth + 1)
	if err != nil {
		return err
	}
	object[key] = child
	return nil
}

func (s *scanner) readObjectKey(object map[string]*jsonValue) (string, error) {
	keyToken, err := s.decoder.Token()
	if err != nil {
		return "", newDiagnostic(CodeInvalidEnvelope, "invalid object key", err)
	}
	key, ok := keyToken.(string)
	if !ok {
		return "", newDiagnostic(CodeInvalidEnvelope, "object key must be a string", nil)
	}
	if err := s.countString(key); err != nil {
		return "", err
	}
	if _, duplicate := object[key]; duplicate {
		return "", newDiagnostic(CodeInvalidEnvelope, "duplicate object key", nil)
	}
	return key, nil
}

func (s *scanner) recordObjectMember(members int) error {
	s.objectMembers++
	if members > maxSingleObjectMembers || s.objectMembers > maxObjectMembers {
		return newDiagnostic(CodeBudgetExceeded, "object member budget exceeded", nil)
	}
	return nil
}

func (s *scanner) readClosingDelimiter(expected json.Delim, message string) error {
	closing, err := s.decoder.Token()
	if err != nil {
		return newDiagnostic(CodeInvalidEnvelope, message, err)
	}
	if closing != expected {
		return newDiagnostic(CodeInvalidEnvelope, message, nil)
	}
	return nil
}

// readArray 解码数组并同时执行单数组和累计元素预算。
func (s *scanner) readArray(depth int) (*jsonValue, error) {
	array := make([]*jsonValue, 0)
	for s.decoder.More() {
		s.arrayElements++
		if len(array) >= maxSingleArrayElements || s.arrayElements > maxArrayElements {
			return nil, newDiagnostic(CodeBudgetExceeded, "array element budget exceeded", nil)
		}
		child, err := s.readValue(depth + 1)
		if err != nil {
			return nil, err
		}
		array = append(array, child)
	}
	closing, err := s.decoder.Token()
	if err != nil || closing != json.Delim(']') {
		return nil, newDiagnostic(CodeInvalidEnvelope, "array is not terminated", err)
	}
	return &jsonValue{kind: kindArray, array: array}, nil
}

func (s *scanner) countString(value string) error {
	length := len(value)
	if length > maxJSONStringBytes {
		return newDiagnostic(CodeBudgetExceeded, "JSON string exceeds 16 KiB", nil)
	}
	s.stringBytes += length
	if s.stringBytes > maxJSONStringsBytes {
		return newDiagnostic(CodeBudgetExceeded, "cumulative JSON strings exceed 192 KiB", nil)
	}
	return nil
}

// scanSchema 只在真实 schema 节点校验结构关键字并确定性递归。
func (s *scanner) scanSchema(value *jsonValue) error {
	if value.kind == kindBool {
		return nil
	}
	if value.kind != kindObject {
		return newDiagnostic(CodeCompileFailed, "nested schema must be an object or boolean", nil)
	}
	for _, key := range sortedObjectKeys(value.object) {
		member := value.object[key]
		if err := s.validateSchemaMember(key, member); err != nil {
			return err
		}
		if err := s.scanNestedSchemaMember(key, member); err != nil {
			return err
		}
	}
	return nil
}

// scanNestedSchemaMember 按关键字语义定位嵌套 schema，避免误判普通属性名。
func (s *scanner) scanNestedSchemaMember(key string, value *jsonValue) error {
	if _, ok := objectSchemaKeywords[key]; ok || key == "patternProperties" {
		return s.scanSchemaMap(value)
	}
	if _, ok := arraySchemaKeywords[key]; ok {
		return s.scanSchemaArray(value)
	}
	if _, ok := schemaOrBoolKeywords[key]; ok {
		return s.scanSchema(value)
	}
	switch key {
	case "items":
		if value.kind == kindArray {
			return s.scanSchemaArray(value)
		}
		return s.scanSchema(value)
	case "dependencies":
		return s.scanSchemaDependencies(value)
	default:
		return nil
	}
}

func (s *scanner) scanSchemaMap(value *jsonValue) error {
	for _, key := range sortedObjectKeys(value.object) {
		if err := s.scanSchema(value.object[key]); err != nil {
			return err
		}
	}
	return nil
}

func (s *scanner) scanSchemaArray(value *jsonValue) error {
	for _, item := range value.array {
		if err := s.scanSchema(item); err != nil {
			return err
		}
	}
	return nil
}

// scanSchemaDependencies 兼容旧草案中 schema 或属性依赖的联合形态。
func (s *scanner) scanSchemaDependencies(value *jsonValue) error {
	if value.kind != kindObject {
		return nil
	}
	for _, key := range sortedObjectKeys(value.object) {
		dependency := value.object[key]
		if dependency.kind != kindObject && dependency.kind != kindBool {
			continue
		}
		if err := s.scanSchema(dependency); err != nil {
			return err
		}
	}
	return nil
}

func (s *scanner) validateSchemaMember(key string, value *jsonValue) error {
	handled, err := s.validateSchemaIdentityMember(key, value)
	if handled {
		return err
	}
	handled, err = s.validateSchemaRegexMember(key, value)
	if handled {
		return err
	}
	return validateSchemaShapeMember(key, value)
}

var referenceKeywords = map[string]struct{}{
	"$ref": {}, "$dynamicRef": {}, "$recursiveRef": {},
}

var forbiddenSchemaKeywords = map[string]struct{}{
	"$id": {}, "id": {}, "$vocabulary": {},
}

var objectSchemaKeywords = map[string]struct{}{
	"properties": {}, "$defs": {}, "definitions": {}, "dependentSchemas": {},
}

var arraySchemaKeywords = map[string]struct{}{
	"allOf": {}, "anyOf": {}, "oneOf": {}, "prefixItems": {},
}

var schemaOrBoolKeywords = map[string]struct{}{
	"not": {}, "if": {}, "then": {}, "else": {}, "contains": {}, "propertyNames": {},
	"contentSchema": {}, "additionalProperties": {}, "additionalItems": {},
	"unevaluatedProperties": {}, "unevaluatedItems": {},
}

func (s *scanner) validateSchemaIdentityMember(key string, value *jsonValue) (bool, error) {
	if _, ok := referenceKeywords[key]; ok {
		return true, s.validateReference(key, value)
	}
	if _, ok := forbiddenSchemaKeywords[key]; ok {
		return true, newDiagnostic(CodeExternalRefForbidden, key+" is forbidden", nil)
	}
	switch key {
	case "$schema":
		return true, validateDraftKeyword(value)
	case "type":
		return true, validateTypeKeyword(value)
	default:
		return false, nil
	}
}

func (s *scanner) validateReference(key string, value *jsonValue) error {
	if value.kind != kindString {
		return newDiagnostic(CodeCompileFailed, key+" must be a string", nil)
	}
	s.references++
	if s.references > maxReferenceKeywords {
		return newDiagnostic(CodeBudgetExceeded, "reference keyword budget exceeded", nil)
	}
	if !isLocalJSONPointer(value.text) {
		return newDiagnostic(CodeExternalRefForbidden, key+" must be a local JSON Pointer", nil)
	}
	return nil
}

func validateDraftKeyword(value *jsonValue) error {
	if value.kind != kindString {
		return newDiagnostic(CodeDraftUnsupported, "$schema must be a string", nil)
	}
	if _, ok := supportedDraftURIs[value.text]; !ok {
		return newDiagnostic(CodeDraftUnsupported, "unsupported $schema URI", nil)
	}
	return nil
}

func validateTypeKeyword(value *jsonValue) error {
	if !validTypeKeyword(value) {
		return newDiagnostic(CodeCompileFailed, "type must be a string or unique string array", nil)
	}
	return nil
}

func (s *scanner) validateSchemaRegexMember(key string, value *jsonValue) (bool, error) {
	switch key {
	case "patternProperties":
		return true, s.validatePatternProperties(value)
	case "pattern":
		return true, s.validatePattern(value)
	default:
		return false, nil
	}
}

func (s *scanner) validatePatternProperties(value *jsonValue) error {
	if value.kind != kindObject {
		return newDiagnostic(CodeCompileFailed, "patternProperties must be an object", nil)
	}
	for pattern := range value.object {
		if err := s.countRegex(pattern); err != nil {
			return err
		}
	}
	return nil
}

func (s *scanner) validatePattern(value *jsonValue) error {
	if value.kind != kindString {
		return newDiagnostic(CodeCompileFailed, "pattern must be a string", nil)
	}
	return s.countRegex(value.text)
}

// validateSchemaShapeMember 校验已识别结构关键字的冻结值形态。
func validateSchemaShapeMember(key string, value *jsonValue) error {
	if _, ok := objectSchemaKeywords[key]; ok {
		return requireSchemaKind(key, value, kindObject, " must be an object")
	}
	if key == "required" {
		return validateRequiredKeyword(value)
	}
	if _, ok := arraySchemaKeywords[key]; ok {
		return requireSchemaKind(key, value, kindArray, " must be an array")
	}
	if _, ok := schemaOrBoolKeywords[key]; ok {
		return validateSchemaOrBoolKeyword(key, value)
	}
	if key == "items" {
		return validateItemsKeyword(value)
	}
	return nil
}

func requireSchemaKind(key string, value *jsonValue, kind valueKind, suffix string) error {
	if value.kind != kind {
		return newDiagnostic(CodeCompileFailed, key+suffix, nil)
	}
	return nil
}

func validateRequiredKeyword(value *jsonValue) error {
	if !arrayContainsOnlyStrings(value) {
		return newDiagnostic(CodeCompileFailed, "required must be an array of strings", nil)
	}
	return nil
}

func validateSchemaOrBoolKeyword(key string, value *jsonValue) error {
	if value.kind != kindObject && value.kind != kindBool {
		return newDiagnostic(CodeCompileFailed, key+" must be a schema object or boolean", nil)
	}
	return nil
}

func validateItemsKeyword(value *jsonValue) error {
	if value.kind != kindObject && value.kind != kindBool && value.kind != kindArray {
		return newDiagnostic(CodeCompileFailed, "items must be a schema or schema array", nil)
	}
	return nil
}

func (s *scanner) countRegex(pattern string) error {
	s.regexStrings++
	s.regexBytes += len(pattern)
	if len(pattern) > maxRegexStringBytes || s.regexStrings > maxRegexStrings || s.regexBytes > maxRegexBytes {
		return newDiagnostic(CodeBudgetExceeded, "regular expression budget exceeded", nil)
	}
	return nil
}

// validTypeKeyword 验证 type 是非空字符串或无重复的字符串数组。
func validTypeKeyword(value *jsonValue) bool {
	if value.kind == kindString {
		return value.text != ""
	}
	if value.kind != kindArray || len(value.array) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(value.array))
	for _, item := range value.array {
		if item.kind != kindString || item.text == "" {
			return false
		}
		if _, exists := seen[item.text]; exists {
			return false
		}
		seen[item.text] = struct{}{}
	}
	return true
}

func arrayContainsOnlyStrings(value *jsonValue) bool {
	if value.kind != kindArray {
		return false
	}
	for _, item := range value.array {
		if item.kind != kindString {
			return false
		}
	}
	return true
}

// isLocalJSONPointer 仅接受当前文档根或合法的本地 JSON Pointer。
func isLocalJSONPointer(reference string) bool {
	if reference == "#" {
		return true
	}
	if !strings.HasPrefix(reference, "#/") {
		return false
	}
	for index := 2; index < len(reference); index++ {
		if reference[index] != '~' {
			continue
		}
		if index+1 >= len(reference) || (reference[index+1] != '0' && reference[index+1] != '1') {
			return false
		}
		index++
	}
	return true
}

func sortedObjectKeys(values map[string]*jsonValue) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// encodeCanonical 以冻结的确定性规则编码内部 JSON 值。
func encodeCanonical(buffer *bytes.Buffer, value *jsonValue) {
	switch value.kind {
	case kindNull:
		buffer.WriteString("null")
	case kindBool:
		encodeCanonicalBool(buffer, value.boolean)
	case kindString:
		encoded, _ := json.Marshal(value.text)
		buffer.Write(encoded)
	case kindNumber:
		buffer.WriteString(value.text)
	case kindArray:
		encodeCanonicalArray(buffer, value.array)
	case kindObject:
		encodeCanonicalObject(buffer, value.object)
	}
}

func encodeCanonicalBool(buffer *bytes.Buffer, value bool) {
	if value {
		buffer.WriteString("true")
		return
	}
	buffer.WriteString("false")
}

func encodeCanonicalArray(buffer *bytes.Buffer, values []*jsonValue) {
	buffer.WriteByte('[')
	for index, item := range values {
		if index > 0 {
			buffer.WriteByte(',')
		}
		encodeCanonical(buffer, item)
	}
	buffer.WriteByte(']')
}

func encodeCanonicalObject(buffer *bytes.Buffer, values map[string]*jsonValue) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	buffer.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		encoded, _ := json.Marshal(key)
		buffer.Write(encoded)
		buffer.WriteByte(':')
		encodeCanonical(buffer, values[key])
	}
	buffer.WriteByte('}')
}
