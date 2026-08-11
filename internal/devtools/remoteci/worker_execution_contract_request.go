package remoteci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

const workerExecutionRequestSourcePath = "internal/devtools/remoteci/coordinator_request.go"

// addWorkerExecutionRequestSemanticFragment keeps only the request fields that
// define the worker runtime boundary. Job/shard/resource/OSS/ECI bookkeeping is
// deliberately not part of this fragment.
func (assets *workerExecutionAssets) addWorkerExecutionRequestSemanticFragment() error {
	request, err := workerExecutionRequestFunction(assets)
	if err != nil || request == nil {
		return err
	}
	content, present, err := workerExecutionRequestCanonicalContent(request)
	if err != nil || !present {
		return err
	}
	assets.fragments["request-runtime"] = workerExecutionFragment{
		kind: "request-runtime", path: workerExecutionRequestSourcePath,
		name: "createRequest-canonical", content: content,
	}
	return nil
}

// 从精确源码快照定位 createRequest，缺失或语法错误均不静默吞掉。
func workerExecutionRequestFunction(assets *workerExecutionAssets) (*ast.FuncDecl, error) {
	if assets == nil || assets.snapshot == nil {
		return nil, fmt.Errorf("worker execution request semantic snapshot is required")
	}
	source, ok := assets.snapshot.goSources[workerExecutionRequestSourcePath]
	if !ok {
		return nil, nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), workerExecutionRequestSourcePath, source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse worker execution request source: %w", err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name != nil && function.Name.Name == "createRequest" {
			return function, nil
		}
	}
	return nil, nil
}

// 将 createRequest 的 worker 命令、环境、挂载和 init 配置规范化为片段。
func workerExecutionRequestCanonicalContent(request *ast.FuncDecl) ([]byte, bool, error) {
	if request == nil || request.Body == nil {
		return nil, false, nil
	}
	collector := newWorkerExecutionRequestCollector(request)
	ast.Inspect(request.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if err := collector.collectComposite(literal); err != nil {
			collector.err = err
			return false
		}
		return true
	})
	if collector.err != nil {
		return nil, false, collector.err
	}
	if !collector.hasRequest {
		return nil, false, nil
	}
	if err := collector.validate(); err != nil {
		return nil, false, err
	}
	parts := append([]string(nil), collector.parts...)
	sort.Strings(parts)
	return []byte(strings.Join(parts, "\n")), true, nil
}

type workerExecutionRequestCollector struct {
	dynamic    map[string]struct{}
	seen       map[string]struct{}
	found      map[string]struct{}
	parts      []string
	hasRequest bool
	err        error
}

// 创建只把请求参数视为动态值的 AST 收集器，保留运行时常量与结构。
func newWorkerExecutionRequestCollector(function *ast.FuncDecl) *workerExecutionRequestCollector {
	dynamic := map[string]struct{}{
		"coordinator": {}, "jobID": {}, "shard": {}, "resources": {},
		"bootstrapRequestKey": {}, "bootstrapRequestDigest": {},
		"fullRequestKey": {}, "fullRequestDigest": {}, "manifestDigest": {},
		"input": {}, "groupName": {}, "initContainer": {}, "mainEnvironment": {},
		"mainMounts": {}, "initMounts": {},
	}
	if function != nil && function.Type != nil && function.Type.Params != nil {
		for _, field := range function.Type.Params.List {
			for _, name := range field.Names {
				dynamic[name.Name] = struct{}{}
			}
		}
	}
	return &workerExecutionRequestCollector{
		dynamic: dynamic,
		seen:    make(map[string]struct{}),
		found:   make(map[string]struct{}),
	}
}

func (collector *workerExecutionRequestCollector) collectComposite(literal *ast.CompositeLit) error {
	if literal == nil {
		return nil
	}
	return collector.collectCompositeAs(literal, workerExecutionRequestCompositeType(literal.Type))
}

func (collector *workerExecutionRequestCollector) collectCompositeAs(literal *ast.CompositeLit, typeName string) error {
	if literal == nil {
		return nil
	}
	key := fmt.Sprintf("%d:%d", literal.Pos(), literal.End())
	if _, ok := collector.seen[key]; ok {
		return nil
	}
	collector.seen[key] = struct{}{}
	if typeName == "CreateRequest" {
		collector.hasRequest = true
	}
	if err := collector.collectCompositeFields(literal, typeName); err != nil {
		return err
	}
	return collector.collectNestedComposites(literal, typeName)
}

// 收集 CreateRequest 运行时字段并拒绝无法规范化的表达式。
func (collector *workerExecutionRequestCollector) collectCompositeFields(literal *ast.CompositeLit, typeName string) error {
	for _, element := range literal.Elts {
		if err := collector.collectCompositeField(element, typeName); err != nil {
			return err
		}
	}
	return nil
}

// 收集单个字段，未列入运行时契约的字段会被忽略。
func (collector *workerExecutionRequestCollector) collectCompositeField(element ast.Expr, typeName string) error {
	field, ok := element.(*ast.KeyValueExpr)
	if !ok {
		return nil
	}
	name, ok := field.Key.(*ast.Ident)
	if !ok || !workerExecutionRequestField(typeName, name.Name) {
		return nil
	}
	label := workerExecutionRequestFieldLabel(typeName, name.Name)
	value, err := collector.expression(field.Value)
	if err != nil {
		return err
	}
	collector.parts = append(collector.parts, label+"="+value)
	collector.found[label] = struct{}{}
	return nil
}

// 收集匿名的嵌套挂载、配置文件和容器复合字面量。
func (collector *workerExecutionRequestCollector) collectNestedComposites(literal *ast.CompositeLit, typeName string) error {
	for _, element := range literal.Elts {
		nested, ok := element.(*ast.CompositeLit)
		if ok && nested.Type == nil {
			if err := collector.collectCompositeAs(nested, typeName); err != nil {
				return err
			}
		}
	}
	return nil
}

// 递归规范化请求字段表达式，遇到未建模节点即 fail-fast。
func (collector *workerExecutionRequestCollector) expression(expression ast.Expr) (string, error) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		return value.Value, nil
	case *ast.Ident:
		return collector.expressionIdent(value)
	case *ast.SelectorExpr:
		return collector.expressionSelector(value)
	case *ast.CallExpr:
		return collector.expressionCall(value)
	case *ast.CompositeLit:
		return collector.expressionComposite(value)
	case *ast.KeyValueExpr:
		return collector.expressionKeyValue(value)
	case *ast.ArrayType:
		return "[]", nil
	case *ast.BinaryExpr, *ast.ParenExpr, *ast.UnaryExpr, *ast.IndexExpr, *ast.IndexListExpr:
		return collector.expressionOperator(expression)
	default:
		return "", fmt.Errorf("worker execution request expression %T is unsupported", expression)
	}
}

func (collector *workerExecutionRequestCollector) expressionIdent(value *ast.Ident) (string, error) {
	if _, dynamic := collector.dynamic[value.Name]; dynamic {
		return "<dynamic>", nil
	}
	return value.Name, nil
}

func (collector *workerExecutionRequestCollector) expressionSelector(value *ast.SelectorExpr) (string, error) {
	base, err := collector.expression(value.X)
	if err != nil {
		return "", err
	}
	if base == "<dynamic>" {
		return base, nil
	}
	return base + "." + value.Sel.Name, nil
}

// 仅允许已纳入 Worker 边界的命令脚本 helper 调用。
func (collector *workerExecutionRequestCollector) expressionCall(value *ast.CallExpr) (string, error) {
	function, err := collector.expression(value.Fun)
	if err != nil {
		return "", err
	}
	if function == "string" {
		if len(value.Args) != 1 {
			return "", fmt.Errorf("worker execution request conversion %q has %d arguments", function, len(value.Args))
		}
		return collector.expression(value.Args[0])
	}
	if function != "remoteWorkerSupervisorCommand" && function != "remoteShardBootstrapSH" && function != "remoteWorkerEnvironment" {
		return "", fmt.Errorf("worker execution request call %q is not canonical", function)
	}
	arguments := make([]string, 0, len(value.Args))
	for _, argument := range value.Args {
		item, err := collector.expression(argument)
		if err != nil {
			return "", err
		}
		arguments = append(arguments, item)
	}
	return "call:" + function + "(" + strings.Join(arguments, ",") + ")", nil
}

func (collector *workerExecutionRequestCollector) expressionComposite(value *ast.CompositeLit) (string, error) {
	items := make([]string, 0, len(value.Elts))
	for _, element := range value.Elts {
		item, err := collector.expression(element)
		if err != nil {
			return "", err
		}
		items = append(items, item)
	}
	return "{" + strings.Join(items, ",") + "}", nil
}

func (collector *workerExecutionRequestCollector) expressionKeyValue(value *ast.KeyValueExpr) (string, error) {
	key, err := collector.expression(value.Key)
	if err != nil {
		return "", err
	}
	item, err := collector.expression(value.Value)
	if err != nil {
		return "", err
	}
	return key + ":" + item, nil
}

// 规范化二元、一元、索引等静态表达式，动态部分由下层统一标记。
func (collector *workerExecutionRequestCollector) expressionOperator(expression ast.Expr) (string, error) {
	switch value := expression.(type) {
	case *ast.BinaryExpr:
		return collector.expressionBinary(value)
	case *ast.ParenExpr:
		return collector.expressionParen(value)
	case *ast.UnaryExpr:
		return collector.expressionUnary(value)
	case *ast.IndexExpr:
		return collector.expressionIndex(value)
	case *ast.IndexListExpr:
		return collector.expressionIndexList(value)
	default:
		return "", fmt.Errorf("worker execution request operator %T is unsupported", expression)
	}
}

func (collector *workerExecutionRequestCollector) expressionBinary(value *ast.BinaryExpr) (string, error) {
	left, err := collector.expression(value.X)
	if err != nil {
		return "", err
	}
	right, err := collector.expression(value.Y)
	if err != nil {
		return "", err
	}
	return left + value.Op.String() + right, nil
}

func (collector *workerExecutionRequestCollector) expressionParen(value *ast.ParenExpr) (string, error) {
	item, err := collector.expression(value.X)
	return "(" + item + ")", err
}

func (collector *workerExecutionRequestCollector) expressionUnary(value *ast.UnaryExpr) (string, error) {
	item, err := collector.expression(value.X)
	return value.Op.String() + item, err
}

func (collector *workerExecutionRequestCollector) expressionIndex(value *ast.IndexExpr) (string, error) {
	base, err := collector.expression(value.X)
	if err != nil {
		return "", err
	}
	index, err := collector.expression(value.Index)
	return base + "[" + index + "]", err
}

func (collector *workerExecutionRequestCollector) expressionIndexList(value *ast.IndexListExpr) (string, error) {
	base, err := collector.expression(value.X)
	if err != nil {
		return "", err
	}
	indices := make([]string, 0, len(value.Indices))
	for _, index := range value.Indices {
		item, err := collector.expression(index)
		if err != nil {
			return "", err
		}
		indices = append(indices, item)
	}
	return base + "[" + strings.Join(indices, ",") + "]", nil
}

func (collector *workerExecutionRequestCollector) validate() error {
	required := []string{
		"request.Command", "request.Args", "request.Environment", "request.InitContainer",
		"request.MainVolumeMounts", "request.InitVolumeMounts", "request.ConfigFileVolumes",
		"init.Command", "init.Args", "init.Environment",
		"volume.Name", "volume.MountPath", "volume.ReadOnly",
		"config-volume.Name", "config-volume.DefaultMode", "config-volume.ConfigFileToPath",
		"config-path.Path", "config-path.Content", "config-path.Mode",
	}
	for _, label := range required {
		if _, ok := collector.found[label]; !ok {
			return fmt.Errorf("worker execution request semantic field %q is missing", label)
		}
	}
	return nil
}

func workerExecutionRequestCompositeType(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.SelectorExpr:
		return value.Sel.Name
	case *ast.Ident:
		return value.Name
	case *ast.ArrayType:
		return workerExecutionRequestCompositeType(value.Elt)
	default:
		return ""
	}
}

// 判断结构字段是否属于 Worker 请求语义边界，排除编排和资源字段。
func workerExecutionRequestField(typeName, field string) bool {
	switch typeName {
	case "CreateRequest":
		return strings.Contains(" Command Args Environment InitContainer MainVolumeMounts InitVolumeMounts ConfigFileVolumes ", " "+field+" ")
	case "InitContainer":
		return strings.Contains(" Command Args Environment ", " "+field+" ")
	case "VolumeMount":
		return strings.Contains(" Name MountPath ReadOnly ", " "+field+" ")
	case "ConfigFileVolume":
		return strings.Contains(" Name DefaultMode ConfigFileToPath ", " "+field+" ")
	case "ConfigFileToPath":
		return strings.Contains(" Path Content Mode ", " "+field+" ")
	default:
		return false
	}
}

func workerExecutionRequestFieldLabel(typeName, field string) string {
	prefix := map[string]string{
		"CreateRequest":    "request",
		"InitContainer":    "init",
		"VolumeMount":      "volume",
		"ConfigFileVolume": "config-volume",
		"ConfigFileToPath": "config-path",
	}[typeName]
	if prefix == "" {
		prefix = strings.ToLower(typeName)
	}
	return prefix + "." + field
}
