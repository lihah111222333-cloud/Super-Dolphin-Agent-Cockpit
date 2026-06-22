package workflowtemplates

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var templateIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*/[a-z0-9]+(?:-[a-z0-9]+)*$`)

var allowedOutputTypes = map[string]struct{}{
	"video":    {},
	"pptx":     {},
	"docx":     {},
	"xlsx":     {},
	"markdown": {},
	"pdf":      {},
	"json":     {},
}

var allowedFieldTypes = map[string]struct{}{
	"text":         {},
	"textarea":     {},
	"select":       {},
	"multi_select": {},
	"file_ref":     {},
	"path":         {},
	"datetime":     {},
	"cron":         {},
	"reviewer":     {},
	"number":       {},
	"boolean":      {},
}

var supportedRuntimeNodeTypes = map[string]struct{}{
	"agent":      {},
	"automation": {},
}

var supportedRuntimeCapabilities = map[string]struct{}{
	"workflow.node.agent":        {},
	"workflow.node.automation":   {},
	"workflow.output.sharedfile": {},
	"workflow.output.artifact":   {},
	"workflow.final_output":      {},
}

func validateTemplate(tpl Template) error {
	for _, check := range []func(Template) error{
		validateTemplateFields,
		validateUIFields,
		validateNodes,
		validateTemplateOutputPaths,
		validateCompatibility,
		validateVideoContract,
	} {
		if err := check(tpl); err != nil {
			return err
		}
	}
	return nil
}

func validatePublishedTemplate(tpl Template) error {
	if err := validateTemplate(tpl); err != nil {
		return err
	}
	return validateRuntimeNodeConfigs(tpl.DAGTemplate.Nodes)
}

func validateTemplateFields(tpl Template) error {
	for _, check := range []func(Template) error{
		validateTemplateIdentity,
		validateTemplateText,
		validateTemplateCategory,
		validateTemplateOutputTypes,
		validateTemplateReviewAndFinal,
	} {
		if err := check(tpl); err != nil {
			return err
		}
	}
	return nil
}

func validateTemplateIdentity(tpl Template) error {
	if !templateIDPattern.MatchString(tpl.ID) {
		return fmt.Errorf("id %q must use category/kebab-case path format", tpl.ID)
	}
	if tpl.Version <= 0 {
		return errors.New("version must be a positive integer")
	}
	return nil
}

func validateTemplateText(tpl Template) error {
	if strings.TrimSpace(tpl.Title.Zh) == "" || strings.TrimSpace(tpl.Title.En) == "" {
		return errors.New("title.zh and title.en are required")
	}
	if strings.TrimSpace(tpl.Description.Zh) == "" || strings.TrimSpace(tpl.Description.En) == "" {
		return errors.New("description.zh and description.en are required")
	}
	return nil
}

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

func validateTemplateOutputTypes(tpl Template) error {
	if len(tpl.OutputTypes) == 0 {
		return errors.New("output_types is required")
	}
	for _, outputType := range tpl.OutputTypes {
		if _, ok := allowedOutputTypes[strings.TrimSpace(outputType)]; !ok {
			return fmt.Errorf("output_type %q is not supported", outputType)
		}
	}
	return nil
}

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

func validateUIFields(tpl Template) error {
	seen := make(map[string]struct{}, len(tpl.UISchema))
	for _, field := range tpl.UISchema {
		if err := validateUIField(field, seen, sharedFilePrefixes(tpl.Validation)); err != nil {
			return err
		}
	}
	return nil
}

func validateUIField(field UIField, seen map[string]struct{}, prefixes []string) error {
	key := strings.TrimSpace(field.Key)
	if key == "" {
		return errors.New("ui_schema.key is required")
	}
	if _, exists := seen[key]; exists {
		return fmt.Errorf("ui_schema field %q is duplicated", key)
	}
	seen[key] = struct{}{}
	if _, ok := allowedFieldTypes[strings.TrimSpace(field.Type)]; !ok {
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

func validateUIPathPlaceholder(key string, field UIField, prefixes []string) error {
	if field.Type != "path" || strings.TrimSpace(field.Placeholder.Zh) == "" {
		return nil
	}
	if err := validateOutputPathValue(field.Placeholder.Zh, prefixes); err != nil {
		return fmt.Errorf("ui_schema field %q placeholder: %w", key, err)
	}
	return nil
}

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

func validateRuntimeNodeConfigs(nodes []NodeTemplate) error {
	for _, node := range nodes {
		if err := validateRuntimeNodeConfig(node); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntimeNodeConfig(node NodeTemplate) error {
	key := strings.TrimSpace(node.NodeKey)
	nodeType := strings.TrimSpace(strings.ToLower(node.NodeType))
	if _, ok := supportedRuntimeNodeTypes[nodeType]; !ok {
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

func validateCompatibility(tpl Template) error {
	if err := validateTrustMetadata(tpl.Trust); err != nil {
		return err
	}
	if err := validateCompatibilityRuntime(tpl.Compatibility); err != nil {
		return err
	}
	if err := validateCompatibilityNodeTypes(tpl.Compatibility.NodeTypes); err != nil {
		return err
	}
	return validateCompatibilityCapabilities(tpl.Compatibility.RequiredCapabilities)
}

func validateTrustMetadata(trust TrustMetadata) error {
	if strings.TrimSpace(trust.Level) == "" {
		return errors.New("trust.level is required")
	}
	if strings.TrimSpace(trust.Source) == "" {
		return errors.New("trust.source is required")
	}
	return nil
}

func validateCompatibilityRuntime(compatibility Compatibility) error {
	if strings.TrimSpace(compatibility.Runtime) != "dag-v2" {
		return errors.New("compatibility.runtime must be dag-v2")
	}
	return nil
}

func validateCompatibilityNodeTypes(nodeTypes []string) error {
	if len(nodeTypes) == 0 {
		return errors.New("compatibility.node_types is required")
	}
	for _, nodeType := range nodeTypes {
		normalized := strings.TrimSpace(strings.ToLower(nodeType))
		if _, ok := supportedRuntimeNodeTypes[normalized]; !ok {
			return unsupportedRuntimeNodeTypeError(nodeType, normalized)
		}
	}
	return nil
}

func unsupportedRuntimeNodeTypeError(nodeType, normalized string) error {
	if normalized == "hybrid" || normalized == "hitl" || normalized == "human" {
		return fmt.Errorf("compatibility node_type %q is not available until runtime support lands", nodeType)
	}
	return fmt.Errorf("compatibility node_type %q is not supported", nodeType)
}

func validateCompatibilityCapabilities(capabilities []string) error {
	if len(capabilities) == 0 {
		return errors.New("compatibility.required_capabilities is required")
	}
	for _, capability := range capabilities {
		normalized := strings.TrimSpace(capability)
		if _, ok := supportedRuntimeCapabilities[normalized]; !ok {
			return fmt.Errorf("compatibility required_capability %q is not supported", capability)
		}
	}
	return nil
}

func validateVideoContract(tpl Template) error {
	if !hasOutputType(tpl.OutputTypes, "video") {
		return nil
	}
	if strings.TrimSpace(tpl.FinalOutput.Kind) != "artifact" {
		return errors.New("video template final_output.kind must be artifact")
	}
	return validateVideoArtifactContract(tpl)
}

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

func validateVideoArtifactPath(pathTemplate string) error {
	if strings.Contains(pathTemplate, "{{run_id}}") || strings.Contains(pathTemplate, "{{run_key}}") || strings.Contains(pathTemplate, "{{output_path}}") {
		return nil
	}
	return errors.New("video artifact path_template must include {{run_id}}, {{run_key}} or {{output_path}}")
}

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

func hasOutputType(items []string, want string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) == want {
			return true
		}
	}
	return false
}

func objectMap(value any) (map[string]any, bool) {
	out, ok := value.(map[string]any)
	return out, ok && out != nil
}

func findNode(nodes []NodeTemplate, key string) (NodeTemplate, bool) {
	for _, node := range nodes {
		if node.NodeKey == key {
			return node, true
		}
	}
	return NodeTemplate{}, false
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
