package main

import (
	"fmt"
	"sort"
	"strings"
)

const canonicalSchemaDialect = "https://json-schema.org/draft/2020-12/schema"
const diagnosticIDPattern = "^diag-[A-Za-z0-9_.-]{1,123}$"

type schemaPosition uint8

const (
	schemaPositionGeneral schemaPosition = iota
	schemaPositionRoot
	schemaPositionAllOfEntry
)

var supportedSchemaKeywords = map[string]struct{}{
	"$ref":                 {},
	"additionalProperties": {},
	"allOf":                {},
	"anyOf":                {},
	"const":                {},
	"else":                 {},
	"enum":                 {},
	"if":                   {},
	"items":                {},
	"maxItems":             {},
	"maxLength":            {},
	"minLength":            {},
	"not":                  {},
	"pattern":              {},
	"properties":           {},
	"required":             {},
	"then":                 {},
	"type":                 {},
	"uniqueItems":          {},
}

func validateSupportedSchemaDocument(value any, name string) ([]string, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema %s root must be an object", name)
	}
	if _, ok := root["$schema"]; !ok {
		return nil, fmt.Errorf("schema %s root must declare $schema", name)
	}
	references := map[string]struct{}{}
	if err := validateSchemaNode(root, "$", schemaPositionRoot, references); err != nil {
		return nil, fmt.Errorf("schema %s: %w", name, err)
	}
	result := make([]string, 0, len(references))
	for reference := range references {
		result = append(result, reference)
	}
	sort.Strings(result)
	return result, nil
}

// validateSchemaNode 递归验证单个 schema 节点，并收集可由运行时解析的顶层引用。
func validateSchemaNode(node map[string]any, path string, position schemaPosition, references map[string]struct{}) error {
	if err := validateNodeKeywords(node, path, position); err != nil {
		return err
	}
	referenceOnly, err := validateReferenceNode(node, path, references)
	if err != nil || referenceOnly {
		return err
	}
	if err := validateScalarKeywords(node, path); err != nil {
		return err
	}
	if err := validateKeywordContexts(node, path, position); err != nil {
		return err
	}
	if err := validateObjectKeywords(node, path, references); err != nil {
		return err
	}
	if err := validateArrayKeywords(node, path, references); err != nil {
		return err
	}
	return validateCompositionKeywords(node, path, references)
}

// validateNodeKeywords 拒绝白名单外或位置不合法的 schema 关键字。
func validateNodeKeywords(node map[string]any, path string, position schemaPosition) error {
	keys := sortedKeys(node)
	for _, keyword := range keys {
		if position == schemaPositionRoot && isRootMetadataKeyword(keyword) {
			if err := validateRootMetadata(keyword, node[keyword], path); err != nil {
				return err
			}
			continue
		}
		if _, ok := supportedSchemaKeywords[keyword]; !ok {
			return fmt.Errorf("%s uses unsupported keyword %q", path, keyword)
		}
	}
	return nil
}

// validateReferenceNode 校验运行时支持的纯顶层名称引用并登记目标。
func validateReferenceNode(node map[string]any, path string, references map[string]struct{}) (bool, error) {
	if reference, ok := node["$ref"]; ok {
		if len(node) != 1 {
			return false, fmt.Errorf("%s uses $ref with unsupported sibling keywords", path)
		}
		target, ok := reference.(string)
		if !ok || target == "" || strings.Contains(target, "#") {
			return false, fmt.Errorf("%s.$ref must name one generated top-level schema", path)
		}
		references[target] = struct{}{}
		return true, nil
	}
	return false, nil
}

// validateKeywordContexts 阻断运行时会因上下文不符而忽略的已知关键字。
func validateKeywordContexts(node map[string]any, path string, position schemaPosition) error {
	if err := validateConditionalContext(node, path, position); err != nil {
		return err
	}
	return validateTypeKeywordContext(node, path)
}

// validateConditionalContext 限定条件关键字只能采用运行时实际执行的 allOf 形态。
func validateConditionalContext(node map[string]any, path string, position schemaPosition) error {
	_, hasIf := node["if"]
	_, hasThen := node["then"]
	_, hasElse := node["else"]
	if err := validateConditionalPosition(path, position, hasIf, hasThen, hasElse); err != nil {
		return err
	}
	if err := validateConditionalCompleteness(path, hasIf, hasThen, hasElse); err != nil {
		return err
	}
	if !hasIf {
		return nil
	}
	return validateConditionalSiblings(node, path)
}

func validateConditionalPosition(path string, position schemaPosition, hasIf, hasThen, hasElse bool) error {
	if (hasIf || hasThen || hasElse) && position != schemaPositionAllOfEntry {
		return fmt.Errorf("%s uses conditional keywords outside an allOf entry", path)
	}
	return nil
}

// validateConditionalCompleteness 拒绝缺失条件或结果分支的半成品条件表达式。
func validateConditionalCompleteness(path string, hasIf, hasThen, hasElse bool) error {
	if (hasThen || hasElse) && !hasIf {
		return fmt.Errorf("%s uses then/else without if", path)
	}
	if hasIf && !hasThen && !hasElse {
		return fmt.Errorf("%s uses if without then or else", path)
	}
	return nil
}

func validateConditionalSiblings(node map[string]any, path string) error {
	for _, keyword := range sortedKeys(node) {
		if keyword != "if" && keyword != "then" && keyword != "else" {
			return fmt.Errorf("%s conditional entry uses ignored sibling keyword %q", path, keyword)
		}
	}
	return nil
}

// validateTypeKeywordContext 阻断与已声明类型不相容且会被运行时忽略的约束。
func validateTypeKeywordContext(node map[string]any, path string) error {
	requiredType, _ := node["type"].(string)
	if hasAnyKeyword(node, "minLength", "maxLength", "pattern") && requiredType != "string" {
		return fmt.Errorf("%s string constraints require type string", path)
	}
	if hasAnyKeyword(node, "properties", "required", "additionalProperties") && requiredType != "" && requiredType != "object" {
		return fmt.Errorf("%s uses object keywords with type %s", path, requiredType)
	}
	if hasAnyKeyword(node, "items", "uniqueItems") && requiredType != "" && requiredType != "array" {
		return fmt.Errorf("%s uses array keywords with type %s", path, requiredType)
	}
	return nil
}

// validateScalarKeywords 校验 Go/JS 双端等价支持的标量关键字子集。
func validateScalarKeywords(node map[string]any, path string) error {
	if err := validateTypeKeyword(node, path); err != nil {
		return err
	}
	if err := validateConstKeyword(node, path); err != nil {
		return err
	}
	if err := validateEnumKeyword(node, path); err != nil {
		return err
	}
	if err := validateMinLengthKeyword(node, path); err != nil {
		return err
	}
	if err := validateMaxLengthKeyword(node, path); err != nil {
		return err
	}
	return validatePatternKeyword(node, path)
}

func validateTypeKeyword(node map[string]any, path string) error {
	if rawType, ok := node["type"]; ok {
		requiredType, ok := rawType.(string)
		if !ok || !isSupportedType(requiredType) {
			return fmt.Errorf("%s.type must be one of object, array, string, or boolean", path)
		}
	}
	return nil
}

func validateConstKeyword(node map[string]any, path string) error {
	if constant, ok := node["const"]; ok && !isSupportedConstraintScalar(constant) {
		return fmt.Errorf("%s.const only supports JSON scalar values", path)
	}
	return nil
}

// validateEnumKeyword 限定 enum 为双端 JSON 相等语义一致的非空标量集合。
func validateEnumKeyword(node map[string]any, path string) error {
	if rawEnum, ok := node["enum"]; ok {
		enum, ok := rawEnum.([]any)
		if !ok || len(enum) == 0 {
			return fmt.Errorf("%s.enum must be a non-empty array", path)
		}
		for index, candidate := range enum {
			if !isSupportedConstraintScalar(candidate) {
				return fmt.Errorf("%s.enum[%d] only supports JSON scalar values", path, index)
			}
		}
	}
	return nil
}

func validateMinLengthKeyword(node map[string]any, path string) error {
	if rawMinLength, ok := node["minLength"]; ok {
		minLength, ok := rawMinLength.(float64)
		if !ok || minLength != 1 {
			return fmt.Errorf("%s.minLength only supports the exact value 1", path)
		}
	}
	return nil
}

func validatePatternKeyword(node map[string]any, path string) error {
	if rawPattern, ok := node["pattern"]; ok {
		pattern, ok := rawPattern.(string)
		if !ok || pattern != diagnosticIDPattern {
			return fmt.Errorf("%s.pattern only supports the canonical diagnostic ID pattern", path)
		}
	}
	return nil
}

func validateMaxLengthKeyword(node map[string]any, path string) error {
	if rawMaxLength, ok := node["maxLength"]; ok {
		maxLength, ok := rawMaxLength.(float64)
		if !ok || maxLength < 0 || maxLength != float64(int(maxLength)) {
			return fmt.Errorf("%s.maxLength must be a non-negative integer", path)
		}
	}
	return nil
}

// validateObjectKeywords 校验对象关键字形状并递归进入 properties。
func validateObjectKeywords(node map[string]any, path string, references map[string]struct{}) error {
	if err := validateAdditionalPropertiesKeyword(node, path); err != nil {
		return err
	}
	if err := validateRequiredKeyword(node, path); err != nil {
		return err
	}
	return validatePropertiesKeyword(node, path, references)
}

func validateAdditionalPropertiesKeyword(node map[string]any, path string) error {
	if rawAdditional, ok := node["additionalProperties"]; ok {
		if _, ok := rawAdditional.(bool); !ok {
			return fmt.Errorf("%s.additionalProperties must be boolean", path)
		}
	}
	return nil
}

// validateRequiredKeyword 校验 required 是非空、唯一的字段名集合。
func validateRequiredKeyword(node map[string]any, path string) error {
	if rawRequired, ok := node["required"]; ok {
		required, ok := rawRequired.([]any)
		if !ok || len(required) == 0 {
			return fmt.Errorf("%s.required must be a non-empty string array", path)
		}
		seen := map[string]struct{}{}
		for index, rawField := range required {
			field, ok := rawField.(string)
			if !ok || field == "" {
				return fmt.Errorf("%s.required[%d] must be a non-empty string", path, index)
			}
			if _, exists := seen[field]; exists {
				return fmt.Errorf("%s.required contains duplicate field %q", path, field)
			}
			seen[field] = struct{}{}
		}
	}
	return nil
}

// validatePropertiesKeyword 动态枚举 properties 并递归验证每个字段 schema。
func validatePropertiesKeyword(node map[string]any, path string, references map[string]struct{}) error {
	rawProperties, ok := node["properties"]
	if !ok {
		return nil
	}
	properties, ok := rawProperties.(map[string]any)
	if !ok {
		return fmt.Errorf("%s.properties must be an object", path)
	}
	for _, field := range sortedKeys(properties) {
		child, ok := properties[field].(map[string]any)
		if !ok {
			return fmt.Errorf("%s.properties.%s must be a schema object", path, field)
		}
		if err := validateSchemaNode(child, path+".properties."+field, schemaPositionGeneral, references); err != nil {
			return err
		}
	}
	return nil
}

// validateArrayKeywords 校验数组约束，并限定 uniqueItems 的双端等价元素类型。
func validateArrayKeywords(node map[string]any, path string, references map[string]struct{}) error {
	if err := validateMaxItemsKeyword(node, path); err != nil {
		return err
	}
	if rawUnique, ok := node["uniqueItems"]; ok {
		unique, ok := rawUnique.(bool)
		if !ok {
			return fmt.Errorf("%s.uniqueItems must be boolean", path)
		}
		if unique && !hasScalarArrayItems(node) {
			return fmt.Errorf("%s.uniqueItems only supports string or boolean items", path)
		}
	}
	rawItems, ok := node["items"]
	if !ok {
		return nil
	}
	items, ok := rawItems.(map[string]any)
	if !ok {
		return fmt.Errorf("%s.items must be a schema object", path)
	}
	return validateSchemaNode(items, path+".items", schemaPositionGeneral, references)
}

func validateMaxItemsKeyword(node map[string]any, path string) error {
	if rawMaxItems, ok := node["maxItems"]; ok {
		maxItems, ok := rawMaxItems.(float64)
		if !ok || maxItems < 0 || maxItems != float64(int(maxItems)) {
			return fmt.Errorf("%s.maxItems must be a non-negative integer", path)
		}
	}
	return nil
}

// validateCompositionKeywords 校验组合关键字并递归进入其 schema 分支。
func validateCompositionKeywords(node map[string]any, path string, references map[string]struct{}) error {
	for _, keyword := range []string{"allOf", "anyOf"} {
		if err := validateSchemaArrayKeyword(node, path, keyword, references); err != nil {
			return err
		}
	}
	if err := validateNotKeyword(node, path, references); err != nil {
		return err
	}
	return validateConditionalBranches(node, path, references)
}

// validateSchemaArrayKeyword 校验 allOf/anyOf 的非空 schema 数组及每个分支。
func validateSchemaArrayKeyword(node map[string]any, path, keyword string, references map[string]struct{}) error {
	rawEntries, ok := node[keyword]
	if !ok {
		return nil
	}
	entries, ok := rawEntries.([]any)
	if !ok || len(entries) == 0 {
		return fmt.Errorf("%s.%s must be a non-empty schema array", path, keyword)
	}
	for index, rawEntry := range entries {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.%s[%d] must be a schema object", path, keyword, index)
		}
		position := schemaPositionGeneral
		if keyword == "allOf" {
			position = schemaPositionAllOfEntry
		}
		if err := validateSchemaNode(entry, fmt.Sprintf("%s.%s[%d]", path, keyword, index), position, references); err != nil {
			return err
		}
	}
	return nil
}

func validateNotKeyword(node map[string]any, path string, references map[string]struct{}) error {
	if rawNot, ok := node["not"]; ok {
		notSchema, ok := rawNot.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.not must be a schema object", path)
		}
		if err := validateSchemaNode(notSchema, path+".not", schemaPositionGeneral, references); err != nil {
			return err
		}
	}
	return nil
}

func validateConditionalBranches(node map[string]any, path string, references map[string]struct{}) error {
	for _, keyword := range []string{"if", "then", "else"} {
		rawBranch, ok := node[keyword]
		if !ok {
			continue
		}
		branch, ok := rawBranch.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.%s must be a schema object", path, keyword)
		}
		if err := validateSchemaNode(branch, path+"."+keyword, schemaPositionGeneral, references); err != nil {
			return err
		}
	}
	return nil
}

// validateSchemaReferences 确保每个 $ref 在同批生成的 canonical schema 中有唯一目标。
func validateSchemaReferences(schemas []renderedSchema) error {
	names := make(map[string]struct{}, len(schemas))
	for _, schema := range schemas {
		names[schema.name] = struct{}{}
	}
	for _, schema := range schemas {
		for _, reference := range schema.references {
			if _, ok := names[reference]; !ok {
				return fmt.Errorf("schema %s references unknown generated schema %q", schema.name, reference)
			}
		}
	}
	return nil
}

func validateRootMetadata(keyword string, value any, path string) error {
	text, ok := value.(string)
	if !ok || text == "" {
		return fmt.Errorf("%s.%s must be a non-empty string", path, keyword)
	}
	if keyword == "$schema" && text != canonicalSchemaDialect {
		return fmt.Errorf("%s.$schema must equal %q", path, canonicalSchemaDialect)
	}
	return nil
}

func isRootMetadataKeyword(keyword string) bool {
	return keyword == "$schema" || keyword == "$id" || keyword == "title"
}

func isSupportedType(value string) bool {
	return value == "object" || value == "array" || value == "string" || value == "boolean"
}

func isSupportedConstraintScalar(value any) bool {
	switch value.(type) {
	case nil, string, bool, float64:
		return true
	default:
		return false
	}
}

func hasScalarArrayItems(node map[string]any) bool {
	items, ok := node["items"].(map[string]any)
	if !ok {
		return false
	}
	itemType, ok := items["type"].(string)
	return ok && (itemType == "string" || itemType == "boolean")
}

func hasAnyKeyword(node map[string]any, keywords ...string) bool {
	for _, keyword := range keywords {
		if _, ok := node[keyword]; ok {
			return true
		}
	}
	return false
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
