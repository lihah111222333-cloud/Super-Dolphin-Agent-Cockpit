package workflowtemplates

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// templateIDPattern 约束模板 ID 使用 category/kebab-case 形式，避免 UI 和持久化层出现歧义。
var templateIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*/[a-z0-9]+(?:-[a-z0-9]+)*$`)

// validationRules 保存单个 Registry 私有的模板校验白名单。
type validationRules struct {
	allowedOutputTypes           map[string]struct{}
	allowedFieldTypes            map[string]struct{}
	supportedRuntimeNodeTypes    map[string]struct{}
	supportedRuntimeCapabilities map[string]struct{}
}

// newValidationRules 为一个 Registry 创建不与其他实例共享的校验规则。
func newValidationRules() validationRules {
	return validationRules{
		allowedOutputTypes: map[string]struct{}{
			"video": {}, "pptx": {}, "docx": {}, "xlsx": {}, "markdown": {}, "pdf": {}, "json": {},
		},
		allowedFieldTypes: map[string]struct{}{
			"text": {}, "textarea": {}, "select": {}, "multi_select": {}, "file_ref": {}, "path": {}, "datetime": {}, "cron": {}, "reviewer": {}, "number": {}, "boolean": {},
		},
		supportedRuntimeNodeTypes: map[string]struct{}{"agent": {}, "automation": {}},
		supportedRuntimeCapabilities: map[string]struct{}{
			"workflow.node.agent": {}, "workflow.node.automation": {}, "workflow.output.sharedfile": {}, "workflow.output.artifact": {}, "workflow.final_output": {},
		},
	}
}

// validateTemplate 执行模板静态校验，不要求运行时可执行配置完整。
func validateTemplate(tpl Template, rules validationRules) error {
	if err := validateTemplateFields(tpl, rules); err != nil {
		return err
	}
	if err := validateUIFields(tpl, rules); err != nil {
		return err
	}
	if err := validateNodes(tpl); err != nil {
		return err
	}
	if err := validateTemplateOutputPaths(tpl); err != nil {
		return err
	}
	if err := validateCompatibility(tpl, rules); err != nil {
		return err
	}
	if err := validateVideoContract(tpl); err != nil {
		return err
	}
	return validateDocumentContract(tpl)
}

// validatePublishedTemplate 在静态校验后追加运行时节点配置校验，用于保存/发布路径。
func validatePublishedTemplate(tpl Template, rules validationRules) error {
	if err := validateTemplate(tpl, rules); err != nil {
		return err
	}
	return validateRuntimeNodeConfigs(tpl.DAGTemplate.Nodes, rules)
}

// validateTemplateFields 聚合模板元数据、产物类型和最终节点关系校验。
func validateTemplateFields(tpl Template, rules validationRules) error {
	if err := validateTemplateIdentity(tpl); err != nil {
		return err
	}
	if err := validateTemplateText(tpl); err != nil {
		return err
	}
	if err := validateTemplateCategory(tpl); err != nil {
		return err
	}
	if err := validateTemplateOutputTypes(tpl, rules); err != nil {
		return err
	}
	return validateTemplateReviewAndFinal(tpl)
}

// validateTemplateIdentity 校验模板 ID 和版本号，保证版本索引可排序且不为空。
func validateTemplateIdentity(tpl Template) error {
	if !templateIDPattern.MatchString(tpl.ID) {
		return fmt.Errorf("id %q must use category/kebab-case path format", tpl.ID)
	}
	if tpl.Version <= 0 {
		return errors.New("version must be a positive integer")
	}
	return nil
}

// validateTemplateText 要求中英文标题和描述同时存在，避免前端展示缺文案。
func validateTemplateText(tpl Template) error {
	if strings.TrimSpace(tpl.Title.Zh) == "" || strings.TrimSpace(tpl.Title.En) == "" {
		return errors.New("title.zh and title.en are required")
	}
	if strings.TrimSpace(tpl.Description.Zh) == "" || strings.TrimSpace(tpl.Description.En) == "" {
		return errors.New("description.zh and description.en are required")
	}
	return nil
}

// validateTemplateCategory 限制模板只能进入已支持的业务分类。
func validateTemplateCategory(tpl Template) error {
	switch strings.TrimSpace(tpl.Category) {
	case "government-enterprise":
	case "":
		return errors.New("category is required")
	default:
		return fmt.Errorf("category %q is not supported", tpl.Category)
	}
	if strings.TrimSpace(tpl.BusinessFlow) == "" {
		return errors.New("business_flow is required")
	}
	return nil
}

// validateTemplateOutputTypes 校验模板至少声明一个已支持的输出类型。
func validateTemplateOutputTypes(tpl Template, rules validationRules) error {
	if len(tpl.OutputTypes) == 0 {
		return errors.New("output_types is required")
	}
	for _, outputType := range tpl.OutputTypes {
		if _, ok := rules.allowedOutputTypes[strings.TrimSpace(outputType)]; !ok {
			return fmt.Errorf("output_type %q is not supported", outputType)
		}
	}
	return nil
}

// validateTemplateReviewAndFinal 校验人工复核、UI schema 和最终输出节点的强约束。
func validateTemplateReviewAndFinal(tpl Template) error {
	if !tpl.RequiresReview {
		return errors.New("requires_review must be true")
	}
	if len(tpl.UISchema) == 0 {
		return errors.New("ui_schema is required")
	}
	if strings.TrimSpace(tpl.DAGTemplate.FinalNodeKey) == "" {
		return errors.New("dag_template.final_node_key is required")
	}
	if strings.TrimSpace(tpl.FinalOutput.NodeKey) != strings.TrimSpace(tpl.DAGTemplate.FinalNodeKey) {
		return errors.New("final_output.node_key must match dag_template.final_node_key")
	}
	if len(sharedFilePrefixes(tpl.Validation)) == 0 {
		return errors.New("validation.sharedfile_prefixes is required")
	}
	return nil
}

// validateUIFields 校验 UI schema 的字段唯一性、文案和路径占位符。
func validateUIFields(tpl Template, rules validationRules) error {
	seen := make(map[string]struct{}, len(tpl.UISchema))
	for _, field := range tpl.UISchema {
		if err := validateUIField(field, seen, sharedFilePrefixes(tpl.Validation), rules); err != nil {
			return err
		}
	}
	return nil
}

// validateUIField 校验单个 UI 字段，并登记 key 以阻止重复字段。
func validateUIField(field UIField, seen map[string]struct{}, prefixes []string, rules validationRules) error {
	key := strings.TrimSpace(field.Key)
	if key == "" {
		return errors.New("ui_schema.key is required")
	}
	if _, exists := seen[key]; exists {
		return fmt.Errorf("ui_schema field %q is duplicated", key)
	}
	seen[key] = struct{}{}
	if _, ok := rules.allowedFieldTypes[strings.TrimSpace(field.Type)]; !ok {
		return fmt.Errorf("ui_schema field %q has unsupported type %q", key, field.Type)
	}
	if err := validateUIFieldText(key, field); err != nil {
		return err
	}
	if err := validateUIOptions(key, field); err != nil {
		return err
	}
	return validateUIPathPlaceholder(key, field, prefixes)
}

// validateUIFieldText 要求字段的中文标签、占位和帮助文案完整。
func validateUIFieldText(key string, field UIField) error {
	if strings.TrimSpace(field.Label.Zh) == "" {
		return fmt.Errorf("ui_schema field %q label.zh is required", key)
	}
	if strings.TrimSpace(field.Placeholder.Zh) == "" {
		return fmt.Errorf("ui_schema field %q placeholder.zh is required", key)
	}
	if strings.TrimSpace(field.Help.Zh) == "" {
		return fmt.Errorf("ui_schema field %q help.zh is required", key)
	}
	return nil
}

// validateUIOptions 校验枚举型字段的选项值和中文标签。
func validateUIOptions(key string, field UIField) error {
	if field.Type != "select" && field.Type != "multi_select" {
		return nil
	}
	for _, option := range field.Options {
		if strings.TrimSpace(option.Value) == "" || strings.TrimSpace(option.Label.Zh) == "" {
			return fmt.Errorf("ui_schema field %q has invalid option", key)
		}
	}
	return nil
}

// validateUIPathPlaceholder 对 path 字段的占位路径复用输出路径白名单。
func validateUIPathPlaceholder(key string, field UIField, prefixes []string) error {
	if field.Type != "path" || strings.TrimSpace(field.Placeholder.Zh) == "" {
		return nil
	}
	if err := validateOutputPathValue(field.Placeholder.Zh, prefixes); err != nil {
		return fmt.Errorf("ui_schema field %q placeholder: %w", key, err)
	}
	return nil
}

// validateNodes 校验 DAG 节点列表、节点 key 唯一性和最终复核关系。
func validateNodes(tpl Template) error {
	nodeIndex := make(map[string]int, len(tpl.DAGTemplate.Nodes))
	if len(tpl.DAGTemplate.Nodes) == 0 {
		return errors.New("dag_template.nodes is required")
	}
	for index, node := range tpl.DAGTemplate.Nodes {
		if err := validateNodeTemplate(tpl, node, nodeIndex, index); err != nil {
			return err
		}
	}
	return validateFinalReviewRelationship(tpl, nodeIndex)
}

// validateNodeTemplate 校验单个节点的基础字段、UI 配置和输出映射。
func validateNodeTemplate(tpl Template, node NodeTemplate, nodeIndex map[string]int, index int) error {
	key := strings.TrimSpace(node.NodeKey)
	if key == "" {
		return errors.New("node_key is required")
	}
	if _, exists := nodeIndex[key]; exists {
		return fmt.Errorf("duplicate node_key %q", key)
	}
	if strings.TrimSpace(node.Title) == "" || strings.TrimSpace(node.NodeType) == "" {
		return fmt.Errorf("node %s title and node_type are required", key)
	}
	if strings.TrimSpace(node.AssignedTo) == "" {
		return fmt.Errorf("node %s assigned_to is required", key)
	}
	if err := validateNodeUIConfig(key, node.Config); err != nil {
		return err
	}
	if err := validateNodeOutputMapping(tpl, node); err != nil {
		return err
	}
	nodeIndex[key] = index
	return nil
}

// validateRuntimeNodeConfigs 校验节点配置是否满足当前运行时真正能执行的 contract。
func validateRuntimeNodeConfigs(nodes []NodeTemplate, rules validationRules) error {
	for _, node := range nodes {
		if err := validateRuntimeNodeConfig(node, rules); err != nil {
			return err
		}
	}
	return nil
}

// validateRuntimeNodeConfig 校验单个运行时节点类型和 agent exec.cwd 要求。
func validateRuntimeNodeConfig(node NodeTemplate, rules validationRules) error {
	key := strings.TrimSpace(node.NodeKey)
	nodeType := strings.TrimSpace(strings.ToLower(node.NodeType))
	if _, ok := rules.supportedRuntimeNodeTypes[nodeType]; !ok {
		if nodeType == "hybrid" || nodeType == "hitl" || nodeType == "human" {
			return fmt.Errorf("node %s node_type %q is not available until runtime support lands", key, node.NodeType)
		}
		return fmt.Errorf("node %s node_type %q is not supported", key, node.NodeType)
	}
	if nodeType != "agent" {
		return nil
	}
	exec, ok := objectMap(node.Config["exec"])
	if !ok {
		return fmt.Errorf("node %s config.exec.cwd is required", key)
	}
	if strings.TrimSpace(fmt.Sprint(exec["cwd"])) == "" || fmt.Sprint(exec["cwd"]) == "<nil>" {
		return fmt.Errorf("node %s config.exec.cwd is required", key)
	}
	return nil
}

// validateNodeUIConfig 校验节点 UI 元数据，确保工作台能展示和解释执行计划。
func validateNodeUIConfig(key string, config map[string]any) error {
	ui, ok := objectMap(config["ui"])
	if !ok {
		return fmt.Errorf("node %s config.ui is required", key)
	}
	for _, field := range []string{"stage_key", "stage_title", "execution_mode", "operation_summary", "model_action"} {
		if strings.TrimSpace(fmt.Sprint(ui[field])) == "" || fmt.Sprint(ui[field]) == "<nil>" {
			return fmt.Errorf("node %s config.ui.%s is required", key, field)
		}
	}
	for _, field := range []string{"skills", "input_sources", "expected_outputs"} {
		if _, exists := ui[field]; !exists {
			return fmt.Errorf("node %s config.ui.%s is required", key, field)
		}
	}
	return nil
}

// validateNodeOutputMapping 校验普通节点和最终节点的输出目标配置。
func validateNodeOutputMapping(tpl Template, node NodeTemplate) error {
	outputs, ok := objectMap(node.Config["outputs"])
	if !ok {
		return fmt.Errorf("node %s config.outputs is required", node.NodeKey)
	}
	if node.NodeKey != tpl.DAGTemplate.FinalNodeKey {
		if _, hasShared := objectMap(outputs["to_sharedfile"]); hasShared {
			return nil
		}
		if _, hasArtifact := objectMap(outputs["to_artifact"]); hasArtifact {
			return nil
		}
		return fmt.Errorf("node %s outputs.to_sharedfile or outputs.to_artifact is required", node.NodeKey)
	}
	return validateFinalNodeOutputMapping(tpl, outputs)
}

// validateFinalNodeOutputMapping 校验最终节点输出与模板 final_output 声明完全一致。
func validateFinalNodeOutputMapping(tpl Template, outputs map[string]any) error {
	switch strings.TrimSpace(tpl.FinalOutput.Kind) {
	case "sharedfile":
		shared, ok := objectMap(outputs["to_sharedfile"])
		if !ok {
			return errors.New("final_output sharedfile requires final node outputs.to_sharedfile")
		}
		if strings.TrimSpace(fmt.Sprint(shared["path"])) != strings.TrimSpace(tpl.FinalOutput.PathTemplate) {
			return errors.New("final_output.path_template must match final node outputs.to_sharedfile.path")
		}
	case "artifact":
		artifact, ok := objectMap(outputs["to_artifact"])
		if !ok {
			return errors.New("final_output artifact requires final node outputs.to_artifact")
		}
		if strings.TrimSpace(fmt.Sprint(artifact["path_template"])) != strings.TrimSpace(tpl.FinalOutput.PathTemplate) {
			return errors.New("final_output.path_template must match final node outputs.to_artifact.path_template")
		}
	default:
		return fmt.Errorf("final_output.kind %q is not supported", tpl.FinalOutput.Kind)
	}
	return nil
}

// validateFinalReviewRelationship 确认 review 节点存在、位于 final 之前并被 final 依赖。
func validateFinalReviewRelationship(tpl Template, nodeIndex map[string]int) error {
	finalIndex, ok := nodeIndex[tpl.DAGTemplate.FinalNodeKey]
	if !ok {
		return errors.New("final_node_key must match an existing node")
	}
	reviewKey := reviewNodeKey(tpl)
	if reviewKey == "" {
		return errors.New("review node is required")
	}
	if tpl.Validation.RequireReviewBeforeFinal && nodeIndex[reviewKey] >= finalIndex {
		return errors.New("review node must appear before final node")
	}
	finalNode := tpl.DAGTemplate.Nodes[finalIndex]
	if !contains(finalNode.DependsOn, reviewKey) {
		return errors.New("final node must depend on review node")
	}
	return nil
}

// validateCompatibility 校验 trust、runtime、node type 和能力声明，阻止模板声明未实现能力。
func validateCompatibility(tpl Template, rules validationRules) error {
	if err := validateTrustMetadata(tpl.Trust); err != nil {
		return err
	}
	if err := validateCompatibilityRuntime(tpl.Compatibility); err != nil {
		return err
	}
	if err := validateCompatibilityNodeTypes(tpl.Compatibility.NodeTypes, rules); err != nil {
		return err
	}
	return validateCompatibilityCapabilities(tpl.Compatibility.RequiredCapabilities, rules)
}

// validateTrustMetadata 要求模板来源和可信等级明确。
func validateTrustMetadata(trust TrustMetadata) error {
	if strings.TrimSpace(trust.Level) == "" {
		return errors.New("trust.level is required")
	}
	if strings.TrimSpace(trust.Source) == "" {
		return errors.New("trust.source is required")
	}
	return nil
}

// validateCompatibilityRuntime 将模板限定在当前 dag-v2 runtime。
func validateCompatibilityRuntime(compatibility Compatibility) error {
	if strings.TrimSpace(compatibility.Runtime) != "dag-v2" {
		return errors.New("compatibility.runtime must be dag-v2")
	}
	return nil
}

// validateCompatibilityNodeTypes 校验 compatibility 中声明的节点类型均已支持。
func validateCompatibilityNodeTypes(nodeTypes []string, rules validationRules) error {
	if len(nodeTypes) == 0 {
		return errors.New("compatibility.node_types is required")
	}
	for _, nodeType := range nodeTypes {
		normalized := strings.TrimSpace(strings.ToLower(nodeType))
		if _, ok := rules.supportedRuntimeNodeTypes[normalized]; !ok {
			return unsupportedRuntimeNodeTypeError(nodeType, normalized)
		}
	}
	return nil
}

// unsupportedRuntimeNodeTypeError 对规划中但未落地的节点类型返回更明确的错误。
func unsupportedRuntimeNodeTypeError(nodeType, normalized string) error {
	if normalized == "hybrid" || normalized == "hitl" || normalized == "human" {
		return fmt.Errorf("compatibility node_type %q is not available until runtime support lands", nodeType)
	}
	return fmt.Errorf("compatibility node_type %q is not supported", nodeType)
}

// validateCompatibilityCapabilities 校验模板所需能力均在当前运行时白名单内。
func validateCompatibilityCapabilities(capabilities []string, rules validationRules) error {
	if len(capabilities) == 0 {
		return errors.New("compatibility.required_capabilities is required")
	}
	for _, capability := range capabilities {
		normalized := strings.TrimSpace(capability)
		if _, ok := rules.supportedRuntimeCapabilities[normalized]; !ok {
			return fmt.Errorf("compatibility required_capability %q is not supported", capability)
		}
	}
	return nil
}

// validateVideoContract 对 video 输出模板追加 artifact 输出约束。
func validateVideoContract(tpl Template) error {
	if !hasOutputType(tpl.OutputTypes, "video") {
		return nil
	}
	if strings.TrimSpace(tpl.FinalOutput.Kind) != "artifact" {
		return errors.New("video template final_output.kind must be artifact")
	}
	return validateVideoArtifactContract(tpl)
}

// validateVideoArtifactContract 校验 video 模板最终节点必须接入 video_with_audio 产物。
func validateVideoArtifactContract(tpl Template) error {
	finalNode, ok := findNode(tpl.DAGTemplate.Nodes, tpl.DAGTemplate.FinalNodeKey)
	if !ok {
		return errors.New("video template final node is missing")
	}
	outputs, ok := objectMap(finalNode.Config["outputs"])
	if !ok {
		return errors.New("video template final node outputs are required")
	}
	artifact, ok := objectMap(outputs["to_artifact"])
	if !ok {
		return errors.New("video template outputs.to_artifact is required")
	}
	if fmt.Sprint(artifact["source_tool"]) != "video_with_audio" {
		return errors.New("video template source_tool must be video_with_audio")
	}
	if fmt.Sprint(artifact["source_path_field"]) != "output_path" {
		return errors.New("video template source_path_field must be output_path")
	}
	return validateVideoArtifactPath(fmt.Sprint(artifact["path_template"]))
}

// validateVideoArtifactPath 要求 video artifact 路径包含运行时可替换的唯一占位。
func validateVideoArtifactPath(pathTemplate string) error {
	if strings.Contains(pathTemplate, "{{run_id}}") || strings.Contains(pathTemplate, "{{run_key}}") || strings.Contains(pathTemplate, "{{output_path}}") {
		return nil
	}
	return errors.New("video artifact path_template must include {{run_id}}, {{run_key}} or {{output_path}}")
}

// validateDocumentContract 确保文档类模板最终产物走内置渲染器，避免把普通文本伪装成 docx/pdf。
func validateDocumentContract(tpl Template) error {
	if !hasDocumentOutputType(tpl.OutputTypes) {
		return nil
	}
	if strings.TrimSpace(tpl.FinalOutput.Kind) != "artifact" {
		return errors.New("document template final_output.kind must be artifact")
	}
	artifact, err := documentArtifactTarget(tpl)
	if err != nil {
		return err
	}
	return validateDocumentArtifactFields(artifact)
}

// documentArtifactTarget 读取文档模板最终节点的 artifact 输出配置。
func documentArtifactTarget(tpl Template) (map[string]any, error) {
	finalNode, ok := findNode(tpl.DAGTemplate.Nodes, tpl.DAGTemplate.FinalNodeKey)
	if !ok {
		return nil, errors.New("document template final node is missing")
	}
	outputs, ok := objectMap(finalNode.Config["outputs"])
	if !ok {
		return nil, errors.New("document template final node outputs are required")
	}
	artifact, ok := objectMap(outputs["to_artifact"])
	if !ok {
		return nil, errors.New("document template outputs.to_artifact is required")
	}
	return artifact, nil
}

// validateDocumentArtifactFields 校验文档模板必须走 document_renderer 文本输入路径。
func validateDocumentArtifactFields(artifact map[string]any) error {
	if fmt.Sprint(artifact["source_tool"]) != "document_renderer" {
		return errors.New("document template source_tool must be document_renderer")
	}
	if fmt.Sprint(artifact["source_text_field"]) != "document_text" {
		return errors.New("document template source_text_field must be document_text")
	}
	if strings.TrimSpace(fmt.Sprint(artifact["source_path_field"])) != "" && fmt.Sprint(artifact["source_path_field"]) != "<nil>" {
		return errors.New("document template must not use source_path_field")
	}
	if !validDocumentArtifactPath(fmt.Sprint(artifact["path_template"])) {
		return errors.New("document template artifact path_template must include {{output_format}} or end with .docx/.pdf")
	}
	return nil
}

// validDocumentArtifactPath 判断文档 artifact 路径是否能表达 docx/pdf 目标格式。
func validDocumentArtifactPath(pathTemplate string) bool {
	path := strings.ToLower(strings.TrimSpace(pathTemplate))
	return strings.Contains(path, "{{output_format}}") || strings.HasSuffix(path, ".docx") || strings.HasSuffix(path, ".pdf")
}

// reviewNodeKey 按稳定顺序选择模板中的 review 节点 key。
func reviewNodeKey(tpl Template) string {
	keys := make([]string, 0)
	for _, node := range tpl.DAGTemplate.Nodes {
		key := strings.TrimSpace(node.NodeKey)
		if key == "review" || strings.Contains(strings.ToLower(key), "review") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// hasOutputType 判断输出类型列表是否包含指定类型。
func hasOutputType(items []string, want string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) == want {
			return true
		}
	}
	return false
}

// hasDocumentOutputType 判断模板是否声明 docx 或 pdf 输出。
func hasDocumentOutputType(items []string) bool {
	return hasOutputType(items, "docx") || hasOutputType(items, "pdf")
}

// objectMap 安全提取 map[string]any，nil map 视为不存在。
func objectMap(value any) (map[string]any, bool) {
	out, ok := value.(map[string]any)
	return out, ok && out != nil
}

// findNode 按 node_key 查找模板节点。
func findNode(nodes []NodeTemplate, key string) (NodeTemplate, bool) {
	for _, node := range nodes {
		if node.NodeKey == key {
			return node, true
		}
	}
	return NodeTemplate{}, false
}

// contains 判断字符串切片是否包含目标值。
func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
