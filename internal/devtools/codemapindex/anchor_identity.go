package codemapindex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const (
	// AnchorManifestSchemaVersion 是 path:line 内容身份清单的唯一受支持 schema。
	AnchorManifestSchemaVersion = 1
	// AnchorManifestGenerator 标识清单只能由 codemap canonical generator 生成。
	AnchorManifestGenerator = GeneratorAnchor + "/anchor-identities"
)

// AnchorManifest 是普通 path:line 锚点的生成式内容身份清单。
type AnchorManifest struct {
	SchemaVersion int              `json:"schema_version"`
	Generator     string           `json:"generator"`
	Anchors       []AnchorIdentity `json:"anchors"`
}

// AnchorIdentity 把 codemap 中的锚点位置绑定到源码内容哈希及可选 Go 符号。
type AnchorIdentity struct {
	CodemapFile   string `json:"codemap_file"`
	CodemapLine   int    `json:"codemap_line"`
	TargetPath    string `json:"target_path"`
	LineSpec      string `json:"line_spec"`
	ContentSHA256 string `json:"content_sha256"`
	Symbol        string `json:"symbol,omitempty"`
}

// BuildAnchorManifest 从 validator 使用的同一批 Markdown 语义输入生成身份清单。
func BuildAnchorManifest(root string, docs []SemanticMarkdown) (AnchorManifest, error) {
	manifest := AnchorManifest{
		SchemaVersion: AnchorManifestSchemaVersion,
		Generator:     AnchorManifestGenerator,
		Anchors:       make([]AnchorIdentity, 0),
	}
	for _, doc := range docs {
		identities, err := anchorIdentitiesForDocument(root, doc)
		if err != nil {
			return AnchorManifest{}, err
		}
		manifest.Anchors = append(manifest.Anchors, identities...)
	}
	manifest.Anchors = uniqueAnchorIdentities(manifest.Anchors)
	sort.Slice(manifest.Anchors, func(i, j int) bool {
		return anchorIdentityLess(manifest.Anchors[i], manifest.Anchors[j])
	})
	return manifest, nil
}

// anchorIdentitiesForDocument 收集单卷正文中的锚点，并跳过生成卷及显式缺失路径。
func anchorIdentitiesForDocument(root string, doc SemanticMarkdown) ([]AnchorIdentity, error) {
	if doc.File == "13-archtest-boundaries.md" {
		return nil, nil
	}
	declaredAbsent := declaredCodemapAbsences(doc)
	var collected []AnchorIdentity
	for _, lineIndex := range narrativeLineIndexes(doc.Lines) {
		for _, code := range inlineCodeRe.FindAllStringSubmatch(doc.Lines[lineIndex], -1) {
			identities, err := identitiesForInlineCode(root, doc.File, lineIndex+1, code[1], declaredAbsent)
			if err != nil {
				return nil, err
			}
			collected = append(collected, identities...)
		}
	}
	return collected, nil
}

// uniqueAnchorIdentities 按文档位置与目标锚点去重，保持首次出现的身份记录。
func uniqueAnchorIdentities(identities []AnchorIdentity) []AnchorIdentity {
	unique := make([]AnchorIdentity, 0, len(identities))
	seen := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		key := fmt.Sprintf("%s\x00%d\x00%s\x00%s", identity.CodemapFile, identity.CodemapLine, identity.TargetPath, identity.LineSpec)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, identity)
	}
	return unique
}

// anchorIdentityLess 定义生成清单的稳定排序顺序。
func anchorIdentityLess(left, right AnchorIdentity) bool {
	if left.CodemapFile != right.CodemapFile {
		return left.CodemapFile < right.CodemapFile
	}
	if left.CodemapLine != right.CodemapLine {
		return left.CodemapLine < right.CodemapLine
	}
	if left.TargetPath != right.TargetPath {
		return left.TargetPath < right.TargetPath
	}
	return left.LineSpec < right.LineSpec
}

// MarshalAnchorManifest 输出稳定、可审查且以换行结尾的 schema v1 JSON。
func MarshalAnchorManifest(manifest AnchorManifest) ([]byte, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal codemap anchor manifest: %w", err)
	}
	return append(data, '\n'), nil
}

// WriteAnchorManifest 创建或替换 canonical manifest，供无旧文件的 v1 迁移与常规 refresh 共用。
func WriteAnchorManifest(path string, manifest AnchorManifest) error {
	data, err := MarshalAnchorManifest(manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write codemap anchor manifest: %w", err)
	}
	return nil
}

// ValidateAnchorManifest 严格校验 schema，再与当前源码生成结果逐字段比较。
func ValidateAnchorManifest(data []byte, expected AnchorManifest) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var actual AnchorManifest
	if err := decoder.Decode(&actual); err != nil {
		return fmt.Errorf("decode codemap anchor manifest: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	if actual.SchemaVersion != AnchorManifestSchemaVersion {
		return fmt.Errorf(
			"unsupported codemap anchor manifest schema_version %d (want %d)",
			actual.SchemaVersion,
			AnchorManifestSchemaVersion,
		)
	}
	if actual.Generator != AnchorManifestGenerator {
		return fmt.Errorf("invalid codemap anchor manifest generator %q", actual.Generator)
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("codemap anchor manifest is stale; run `make codemap-refresh`")
	}
	return nil
}

func identitiesForInlineCode(
	root, codemapFile string,
	codemapLine int,
	code string,
	declaredAbsent map[string]struct{},
) ([]AnchorIdentity, error) {
	var identities []AnchorIdentity
	for _, match := range repoFileRefRe.FindAllStringSubmatch(code, -1) {
		if match[2] == "" {
			continue
		}
		if _, absent := declaredAbsent[normalizeRepoRelative(match[1])]; absent {
			continue
		}
		identity, err := buildAnchorIdentity(root, codemapFile, codemapLine, match[1], match[2])
		if err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

// buildAnchorIdentity 读取单个目标锚点，计算内容哈希并补充可解析的 Go 符号身份。
func buildAnchorIdentity(root, codemapFile string, codemapLine int, rawPath, lineSpec string) (AnchorIdentity, error) {
	relative := normalizeRepoRelative(rawPath)
	absolute, err := resolveRepoPath(root, relative)
	if err != nil {
		return AnchorIdentity{}, fmt.Errorf("%s:%d invalid anchor target %s: %w", codemapFile, codemapLine, relative, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return AnchorIdentity{}, fmt.Errorf("%s:%d read anchor target %s: %w", codemapFile, codemapLine, relative, err)
	}
	if info.IsDir() {
		return AnchorIdentity{}, fmt.Errorf("%s:%d anchor target is a directory: %s", codemapFile, codemapLine, relative)
	}
	lines, err := readLines(absolute)
	if err != nil {
		return AnchorIdentity{}, fmt.Errorf("%s:%d read anchor target %s: %w", codemapFile, codemapLine, relative, err)
	}
	selected, err := selectedAnchorLines(lineSpec, len(lines))
	if err != nil {
		return AnchorIdentity{}, fmt.Errorf("%s:%d invalid anchor %s:%s: %w", codemapFile, codemapLine, relative, lineSpec, err)
	}
	selectedContent := make([]string, 0, len(selected))
	for _, line := range selected {
		selectedContent = append(selectedContent, lines[line-1])
	}
	encoded, err := json.Marshal(selectedContent)
	if err != nil {
		return AnchorIdentity{}, fmt.Errorf("marshal selected anchor content: %w", err)
	}
	sum := sha256.Sum256(encoded)
	identity := AnchorIdentity{
		CodemapFile:   codemapFile,
		CodemapLine:   codemapLine,
		TargetPath:    relative,
		LineSpec:      lineSpec,
		ContentSHA256: hex.EncodeToString(sum[:]),
	}
	if filepath.Ext(relative) == ".go" {
		identity.Symbol = enclosingGoSymbol(absolute, selected[0])
	}
	return identity, nil
}

// selectedAnchorLines 把多段行号规范展开为有序、去重且已校验边界的行号。
func selectedAnchorLines(spec string, totalLines int) ([]int, error) {
	parts := strings.FieldsFunc(strings.TrimSuffix(spec, "*"), func(value rune) bool {
		return value == ',' || value == '/'
	})
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty line spec")
	}
	seen := make(map[int]struct{})
	var selected []int
	for _, part := range parts {
		start, end, err := parseAnchorRange(part, totalLines)
		if err != nil {
			return nil, err
		}
		for line := start; line <= end; line++ {
			if _, duplicate := seen[line]; duplicate {
				continue
			}
			seen[line] = struct{}{}
			selected = append(selected, line)
		}
	}
	sort.Ints(selected)
	return selected, nil
}

// parseAnchorRange 解析单行或闭区间，并拒绝倒序或越界范围。
func parseAnchorRange(part string, totalLines int) (int, int, error) {
	bounds := strings.SplitN(strings.TrimSuffix(part, "*"), "-", 2)
	start, err := strconv.Atoi(bounds[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid line %q", bounds[0])
	}
	end := start
	if len(bounds) == 2 {
		end, err = strconv.Atoi(bounds[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid line %q", bounds[1])
		}
	}
	if start < 1 || end < start || end > totalLines {
		return 0, 0, fmt.Errorf("range %d-%d exceeds %d lines", start, end, totalLines)
	}
	return start, end, nil
}

// enclosingGoSymbol 返回覆盖目标行的最窄 Go 声明名称；无法解析时返回空字符串。
func enclosingGoSymbol(path string, line int) string {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		return ""
	}
	bestName := ""
	bestSpan := int(^uint(0) >> 1)
	ast.Inspect(parsed, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		start := files.Position(node.Pos()).Line
		end := files.Position(node.End()).Line
		if line < start || line > end {
			return true
		}
		name := goNodeSymbol(files, node)
		if name != "" && end-start < bestSpan {
			bestName = name
			bestSpan = end - start
		}
		return true
	})
	return bestName
}

// goNodeSymbol 把函数、方法、类型和值声明归一化为稳定符号名。
func goNodeSymbol(files *token.FileSet, node ast.Node) string {
	switch value := node.(type) {
	case *ast.FuncDecl:
		if value.Recv == nil || len(value.Recv.List) == 0 {
			return value.Name.Name
		}
		var receiver bytes.Buffer
		if err := format.Node(&receiver, files, value.Recv.List[0].Type); err != nil {
			return value.Name.Name
		}
		return receiver.String() + "." + value.Name.Name
	case *ast.TypeSpec:
		return value.Name.Name
	case *ast.ValueSpec:
		names := make([]string, 0, len(value.Names))
		for _, name := range value.Names {
			names = append(names, name.Name)
		}
		return strings.Join(names, ",")
	default:
		return ""
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing codemap anchor manifest data: %w", err)
	}
	return fmt.Errorf("codemap anchor manifest contains trailing JSON value")
}
