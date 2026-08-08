package remoteci

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/token"
	"sort"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func digestWorkerExecutionClosure(
	closure *workerExecutionGoClosure,
	assets *workerExecutionAssets,
) (string, error) {
	return digestWorkerExecutionClosureWithSemanticEnvironment(
		closure,
		assets,
		cicontract.WorkerExecutionEnvironmentSchemaVersion,
		cicontract.WorkerExecutionProvenanceID,
		cicontract.CanonicalWorkerExecutionEnvironment(),
	)
}

// digestWorkerExecutionClosureWithSemanticEnvironment 让契约测试显式证明
// schema/provenance bump 会改变 worker contract digest；生产入口仍只传 canonical owner 材料。
func digestWorkerExecutionClosureWithSemanticEnvironment(
	closure *workerExecutionGoClosure,
	assets *workerExecutionAssets,
	semanticEnvironmentSchema string,
	workerExecutionProvenance string,
	semanticEnvironment []string,
) (string, error) {
	hasher := sha256.New()
	fmt.Fprintf(hasher, "worker-execution-contract-schema %d\n", workerExecutionContractSchemaVersion)
	fmt.Fprintf(hasher, "platform %s\n", workerExecutionPlatform)
	fmt.Fprintf(hasher, "semantic-environment-schema %s\n", semanticEnvironmentSchema)
	fmt.Fprintf(hasher, "execution-provenance %s\n", workerExecutionProvenance)
	semanticEnvironment = append([]string(nil), semanticEnvironment...)
	sort.Strings(semanticEnvironment)
	for _, assignment := range semanticEnvironment {
		fmt.Fprintf(hasher, "environment %s\n", assignment)
	}
	units := make([]*workerExecutionGoUnit, 0, len(closure.selected))
	for _, unit := range closure.selected {
		units = append(units, unit)
	}
	sort.Slice(units, func(left, right int) bool { return units[left].key < units[right].key })
	for _, unit := range units {
		content, err := workerExecutionUnitContent(unit)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hasher, "go %s %s %s %x\n",
			unit.filePath, unit.packageName, unit.kind, sha256.Sum256(content))
		imports := sortedRemoteStringSet(closure.usedImports[unit.key])
		for _, importPath := range imports {
			fmt.Fprintf(hasher, "import %s %s\n", unit.key, importPath)
		}
	}
	for _, entry := range sortedRemoteGitTreeEntries(assets.entries) {
		fmt.Fprintf(hasher, "asset %s %s %s %s\n", entry.mode, entry.kind, entry.objectID, entry.path)
	}
	fragmentKeys := make([]string, 0, len(assets.fragments))
	for key := range assets.fragments {
		fragmentKeys = append(fragmentKeys, key)
	}
	sort.Strings(fragmentKeys)
	for _, key := range fragmentKeys {
		fragment := assets.fragments[key]
		fmt.Fprintf(hasher, "%s %s %s %x\n",
			fragment.kind, fragment.path, fragment.name, sha256.Sum256(fragment.content))
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionUnitContent(unit *workerExecutionGoUnit) ([]byte, error) {
	content := unit.content
	if content == nil {
		content = unit.node
	}
	start := workerExecutionContentStart(content)
	startOffset := unit.fileSet.PositionFor(start, false).Offset
	endOffset := unit.fileSet.PositionFor(content.End(), false).Offset
	if startOffset < 0 || endOffset < startOffset || endOffset > len(unit.source) {
		return nil, fmt.Errorf("worker execution source range for %q is invalid", unit.filePath)
	}
	return unit.source[startOffset:endOffset], nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionContentStart(content ast.Node) token.Pos {
	if documented, ok := content.(interface{ DocComment() *ast.CommentGroup }); ok {
		if comment := documented.DocComment(); comment != nil {
			return comment.Pos()
		}
	}
	return workerExecutionDeclarationStart(content)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionDeclarationStart(content ast.Node) token.Pos {
	switch value := content.(type) {
	case *ast.FuncDecl:
		return workerExecutionDocumentedStart(value.Doc, content.Pos())
	case *ast.GenDecl:
		return workerExecutionDocumentedStart(value.Doc, content.Pos())
	case *ast.TypeSpec:
		return workerExecutionDocumentedStart(value.Doc, content.Pos())
	case *ast.ValueSpec:
		return workerExecutionDocumentedStart(value.Doc, content.Pos())
	default:
		return content.Pos()
	}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionDocumentedStart(doc *ast.CommentGroup, fallback token.Pos) token.Pos {
	if doc == nil {
		return fallback
	}
	return doc.Pos()
}
