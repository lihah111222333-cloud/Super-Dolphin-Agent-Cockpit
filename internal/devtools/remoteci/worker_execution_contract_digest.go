package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func digestWorkerExecutionClosure(
	closure *workerExecutionGoClosure,
	assets *workerExecutionAssets,
) (string, error) {
	return digestWorkerExecutionClosureWithEnvironmentAndContent(
		closure,
		assets,
		workerExecutionContractSchemaVersion,
		cicontract.WorkerExecutionEnvironmentSchemaVersion,
		cicontract.WorkerExecutionProvenanceID,
		cicontract.CanonicalWorkerExecutionEnvironment(),
		workerExecutionScopedUnitContent,
	)
}

// digestWorkerExecutionClosurePreviousV4 保留旧环境摘要的完整函数体与注释语义，供历史 PASS 严格复核。
func digestWorkerExecutionClosurePreviousV4(closure *workerExecutionGoClosure, assets *workerExecutionAssets) (string, error) {
	return digestWorkerExecutionClosureWithEnvironmentAndContent(
		closure, assets, workerExecutionPreviousSchemaVersion,
		cicontract.WorkerExecutionEnvironmentSchemaVersion,
		cicontract.WorkerExecutionProvenanceID,
		cicontract.CanonicalWorkerExecutionEnvironment(),
		workerExecutionUnitContent,
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
	return digestWorkerExecutionClosureWithEnvironmentAndContent(
		closure, assets, workerExecutionContractSchemaVersion,
		semanticEnvironmentSchema, workerExecutionProvenance, semanticEnvironment,
		workerExecutionScopedUnitContent,
	)
}

// digestWorkerExecutionClosureWithEnvironmentAndContent 以指定 schema 与源码投影生成确定性 worker 身份。
func digestWorkerExecutionClosureWithEnvironmentAndContent(
	closure *workerExecutionGoClosure,
	assets *workerExecutionAssets,
	contractSchemaVersion int,
	semanticEnvironmentSchema string,
	workerExecutionProvenance string,
	semanticEnvironment []string,
	unitContent func(*workerExecutionGoUnit) ([]byte, error),
) (string, error) {
	hasher := sha256.New()
	fmt.Fprintf(hasher, "worker-execution-contract-schema %d\n", contractSchemaVersion)
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
		content, err := unitContent(unit)
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

// workerExecutionScopedUnitContent 将 SQLC 专属函数体留给其 full-tree workload 输入摘要，公共身份仍冻结签名与 import。
func workerExecutionScopedUnitContent(unit *workerExecutionGoUnit) ([]byte, error) {
	if unit == nil {
		return nil, fmt.Errorf("worker execution unit is required")
	}
	if unit.kind == "func" && strings.HasPrefix(unit.filePath, "internal/devtools/gate/executor_sqlc") && strings.HasSuffix(unit.filePath, ".go") {
		function, ok := unit.node.(*ast.FuncDecl)
		if !ok {
			return nil, fmt.Errorf("scoped worker execution function %q has invalid syntax node", unit.key)
		}
		header := *function
		header.Doc = nil
		header.Body = nil
		var output bytes.Buffer
		if err := format.Node(&output, unit.fileSet, &header); err != nil {
			return nil, fmt.Errorf("format scoped worker execution function %q: %w", unit.key, err)
		}
		return output.Bytes(), nil
	}
	return workerExecutionUnitContentWithoutComments(unit)
}

func workerExecutionUnitContentWithoutComments(unit *workerExecutionGoUnit) ([]byte, error) {
	content := unit.content
	if content == nil {
		content = unit.node
	}
	startOffset := unit.fileSet.PositionFor(content.Pos(), false).Offset
	endOffset := unit.fileSet.PositionFor(content.End(), false).Offset
	if startOffset < 0 || endOffset < startOffset || endOffset > len(unit.source) {
		return nil, fmt.Errorf("worker execution source range for %q is invalid", unit.filePath)
	}
	return unit.source[startOffset:endOffset], nil
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
