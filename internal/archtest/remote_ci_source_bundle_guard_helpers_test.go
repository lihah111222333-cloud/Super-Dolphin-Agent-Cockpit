package archtest

import (
	"go/ast"
	"strings"
)

// remoteCISourceBundleViolations rejects the retired source-delta producer and
// consumer even when a future implementation renames the old package alias.
// The accepted path is a SourceSpec-backed bundle plus strict manifest.
func remoteCISourceBundleViolations(file *ast.File) []string {
	violations := remoteCISourceBundleIdentifierViolations(file)
	for key, value := range remoteCISourceBundleLiteralViolations(file) {
		violations[key] = value
	}
	for key, value := range remoteCISourceBundleSelectorViolations(file) {
		violations[key] = value
	}
	return remoteCIViolationList(violations)
}

func remoteCISourceBundleIdentifierViolations(file *ast.File) map[string]bool {
	violations := map[string]bool{}
	for identifier := range remoteCIForbiddenIdentifiers(file) {
		normalized := strings.ToLower(identifier)
		if normalized == "patchformat" || normalized == "patchkey" || normalized == "patchsha256" ||
			normalized == "patchsize" || normalized == "patchpath" || strings.Contains(normalized, "sourcedelta") {
			violations["identifier "+identifier] = true
		}
	}
	return violations
}

func remoteCISourceBundleLiteralViolations(file *ast.File) map[string]bool {
	violations := map[string]bool{}
	for _, literal := range remoteCIStringLiterals(file) {
		normalized := strings.ToLower(literal)
		if strings.Contains(normalized, "source delta") || strings.Contains(normalized, "source-delta") ||
			strings.Contains(normalized, "git apply") || strings.Contains(normalized, "source.patch") ||
			strings.Contains(normalized, "source.manifest.json") {
			violations["literal "+literal] = true
		}
	}
	return violations
}

func remoteCISourceBundleSelectorViolations(file *ast.File) map[string]bool {
	violations := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "Build" && selector.Sel.Name != "Verify") {
			return true
		}
		if name := strings.ToLower(remoteCIExpressionName(selector.X)); strings.Contains(name, "source") {
			violations["source."+selector.Sel.Name] = true
		}
		return true
	})
	return violations
}
