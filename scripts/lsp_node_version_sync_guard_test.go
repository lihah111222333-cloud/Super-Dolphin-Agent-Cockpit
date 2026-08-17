package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

type lspNodePackageConstMapping struct {
	packageName string
	constName   string
}

// lspNodePackageConstMappingTable 是 Node 包名到 runtime.go 版本常量的唯一映射表；表中不复制版本值。
func lspNodePackageConstMappingTable() []lspNodePackageConstMapping {
	return []lspNodePackageConstMapping{
		{packageName: "typescript-language-server", constName: "typeScriptLanguageServerInstallVersion"},
		{packageName: "typescript", constName: "typeScriptInstallVersion"},
		{packageName: "vscode-langservers-extracted", constName: "vscodeLangserversExtractedInstallVersion"},
		{packageName: "vscode-markdown-languageservice", constName: "vscodeMarkdownLanguageServiceInstallVersion"},
		// Windows Markdown 运行时显式携带 markdown-it；守卫复用同一锁定常量，避免漏检这项解析器依赖。
		{packageName: "markdown-it", constName: "runtimeMarkdownItInstallVersion"},
		{packageName: "pyright", constName: "pyrightInstallVersion"},
		{packageName: "yaml-language-server", constName: "yamlLanguageServerInstallVersion"},
		{packageName: "@vue/language-server", constName: "vueLanguageServerInstallVersion"},
		{packageName: "svelte-language-server", constName: "svelteLanguageServerInstallVersion"},
		{packageName: "intelephense", constName: "intelephenseInstallVersion"},
		{packageName: "dockerfile-language-server-nodejs", constName: "dockerfileLanguageServerInstallVersion"},
		{packageName: "graphql-language-service-cli", constName: "graphqlLanguageServiceCLIInstallVersion"},
		{packageName: "@prisma/language-server", constName: "prismaLanguageServerInstallVersion"},
		{packageName: "bash-language-server", constName: "bashLanguageServerInstallVersion"},
		{packageName: "shellcheck", constName: "shellcheckInstallVersion"},
	}
}

type lspNodePackageSpec struct {
	packageName string
	version     string
	constName   string
}

type lspWindowsNpmPackages struct {
	base        map[string]lspNodePackageSpec
	conditional map[string]lspNodePackageSpec
	all         map[string]lspNodePackageSpec
}

func TestLSPNodeVersionRuntimeAndWindowsBundleStayInSync(t *testing.T) {
	// Node 版本常量分布在 runtime.go 与 Markdown 支持源码；拼接两份规范事实源只扩大静态守卫，不改变生产编排。
	runtimeSource := strings.Join([]string{
		readScript(t, "../cmd/mcp-lsp/runtime.go"),
		readScript(t, "../cmd/mcp-lsp/runtime_markdown_support.go"),
	}, "\n")
	windowsBundleSource := readScript(t, "prepare_lsp_bundle_windows.ps1")
	mappings := lspNodePackageConstMappingTable()
	assertLSPNodePackageConstMapping(t, mappings)

	runtimeVersions := parseLSPNodeRuntimeVersions(t, runtimeSource, mappings)
	runtimePackages := parseLSPNodeRuntimePackages(t, runtimeSource, mappings, runtimeVersions)
	assertLSPNodePackageSet(t, "runtime default Node adapter", runtimePackages, mappings)

	windowsPackages := parseLSPWindowsNpmPackages(t, windowsBundleSource)
	assertLSPNodePinsMatch(t, runtimePackages, windowsPackages.all, "runtime vs Windows bundle")
	assertLSPWindowsNpmPackageCohort(t, runtimePackages, windowsPackages)
	assertLSPMarkdownPackagePair(t, runtimePackages, windowsPackages.all)
	assertLSPNodeShellcheckConditions(t, runtimeSource, windowsBundleSource, runtimePackages)
}

func assertLSPNodePackageConstMapping(t *testing.T, mappings []lspNodePackageConstMapping) {
	t.Helper()
	if len(mappings) == 0 {
		t.Fatal("Node package-to-constant mapping is empty")
	}
	packages := make(map[string]struct{}, len(mappings))
	constants := make(map[string]struct{}, len(mappings))
	for _, mapping := range mappings {
		if strings.TrimSpace(mapping.packageName) == "" || strings.TrimSpace(mapping.constName) == "" {
			t.Fatalf("Node package-to-constant mapping contains an empty field: %#v", mapping)
		}
		if _, exists := packages[mapping.packageName]; exists {
			t.Fatalf("Node package mapping duplicates package %q", mapping.packageName)
		}
		if _, exists := constants[mapping.constName]; exists {
			t.Fatalf("Node package mapping duplicates runtime constant %q", mapping.constName)
		}
		if regexp.MustCompile(`@[0-9]`).MatchString(mapping.packageName) {
			t.Fatalf("Node package mapping must not copy a version value: %#v", mapping)
		}
		packages[mapping.packageName] = struct{}{}
		constants[mapping.constName] = struct{}{}
	}
}

func parseLSPNodeRuntimeVersions(t *testing.T, source string, mappings []lspNodePackageConstMapping) map[string]string {
	t.Helper()
	versionPattern := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	versions := make(map[string]string, len(mappings))
	for _, mapping := range mappings {
		pattern := regexp.MustCompile(`(?m)^\s*(?:const\s+)?` + regexp.QuoteMeta(mapping.constName) + `\s*=\s*"([^"]+)"\s*$`)
		matches := pattern.FindAllStringSubmatch(source, -1)
		if len(matches) != 1 {
			t.Fatalf("runtime constant %s occurrence count = %d, want exactly one", mapping.constName, len(matches))
		}
		version := matches[0][1]
		if !versionPattern.MatchString(version) {
			t.Fatalf("runtime constant %s is not an exact version: %q", mapping.constName, version)
		}
		versions[mapping.constName] = version
	}
	return versions
}

func parseLSPNodeRuntimePackages(t *testing.T, source string, mappings []lspNodePackageConstMapping, versions map[string]string) map[string]lspNodePackageSpec {
	t.Helper()
	byPackage := make(map[string]string, len(mappings))
	for _, mapping := range mappings {
		byPackage[mapping.packageName] = mapping.constName
	}

	regions := []string{
		lspNodeFunctionRegion(t, source, "func runtimeNPMInstallerSpecsForPlatform"),
		lspNodeFunctionRegion(t, source, "func runtimeShellNPMInstallerConfigForTarget"),
	}
	var expressions []lspNodePackageSpec
	for _, region := range regions {
		for _, arguments := range lspNodeNPMCallArguments(region) {
			expressions = append(expressions, parseLSPNodePackageExpressions(t, arguments)...)
		}
		appendPattern := regexp.MustCompile(`(?s)packages\s*=\s*append\(packages,\s*(.*?)\)`)
		for _, match := range appendPattern.FindAllStringSubmatch(region, -1) {
			expressions = append(expressions, parseLSPNodePackageExpressions(t, match[1])...)
		}
		shellPackages := regexp.MustCompile(`(?s)packages\s*:=\s*\[\]string\s*\{(.*?)\}`).FindStringSubmatch(region)
		if len(shellPackages) == 2 {
			expressions = append(expressions, parseLSPNodePackageExpressions(t, shellPackages[1])...)
		}
	}
	if len(expressions) == 0 {
		t.Fatal("runtime Node install package expressions are empty")
	}

	packages := make(map[string]lspNodePackageSpec, len(expressions))
	for _, expression := range expressions {
		wantConst, known := byPackage[expression.packageName]
		if !known {
			t.Fatalf("runtime contains an extra or unknown Node package %q", expression.packageName)
		}
		if expression.constName == "" {
			t.Fatalf("runtime Node package %q is not derived from a version constant", expression.packageName)
		}
		if expression.constName != wantConst {
			t.Fatalf("runtime Node package %q uses constant %q, want %q", expression.packageName, expression.constName, wantConst)
		}
		wantVersion, ok := versions[expression.constName]
		if !ok {
			t.Fatalf("runtime Node package %q references missing constant %q", expression.packageName, expression.constName)
		}
		if expression.version != "" && expression.version != wantVersion {
			t.Fatalf("runtime Node package %q version %q disagrees with %s=%q", expression.packageName, expression.version, expression.constName, wantVersion)
		}
		expression.version = wantVersion
		if previous, exists := packages[expression.packageName]; exists && previous.constName != expression.constName {
			t.Fatalf("runtime Node package %q has conflicting constants %q and %q", expression.packageName, previous.constName, expression.constName)
		}
		packages[expression.packageName] = expression
	}
	return packages
}

func parseLSPNodePackageExpressions(t *testing.T, arguments string) []lspNodePackageSpec {
	t.Helper()
	literalPattern := regexp.MustCompile(`"([^"\\]*)"(?:\s*\+\s*([A-Za-z_][A-Za-z0-9_]*))?`)
	matches := literalPattern.FindAllStringSubmatch(arguments, -1)
	if len(matches) == 0 && strings.TrimSpace(arguments) != "" {
		t.Fatalf("runtime Node install arguments contain no package literal: %q", strings.TrimSpace(arguments))
	}
	packages := make([]lspNodePackageSpec, 0, len(matches))
	for _, match := range matches {
		literal := match[1]
		constName := match[2]
		if constName != "" {
			if !strings.HasSuffix(literal, "@") {
				t.Fatalf("runtime Node package literal %q must end with @ before its constant", literal)
			}
			packages = append(packages, lspNodePackageSpec{packageName: strings.TrimSuffix(literal, "@"), constName: constName})
			continue
		}
		packageName, version, ok := splitLSPNodePackageSpec(literal)
		if !ok {
			t.Fatalf("runtime Node package literal %q is unpinned", literal)
		}
		packages = append(packages, lspNodePackageSpec{packageName: packageName, version: version})
	}
	return packages
}

func splitLSPNodePackageSpec(spec string) (packageName, version string, ok bool) {
	separator := strings.LastIndex(spec, "@")
	if separator <= 0 || separator == len(spec)-1 {
		return "", "", false
	}
	version = spec[separator+1:]
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`).MatchString(version) {
		return "", "", false
	}
	return spec[:separator], version, true
}

func lspNodeFunctionRegion(t *testing.T, source, signature string) string {
	t.Helper()
	start := strings.Index(source, signature)
	if start < 0 {
		t.Fatalf("runtime source is missing %s", signature)
	}
	rest := source[start:]
	if next := strings.Index(rest[len(signature):], "\nfunc "); next >= 0 {
		return rest[:len(signature)+next]
	}
	return rest
}

func lspNodeNPMCallArguments(region string) []string {
	const marker = "runtimeNPMInstallArgs("
	var arguments []string
	for offset := 0; offset < len(region); {
		relative := strings.Index(region[offset:], marker)
		if relative < 0 {
			break
		}
		start := offset + relative + len(marker) - 1
		end := lspNodeBalancedDelimiter(region, start, '(', ')')
		argumentsText := region[start+1 : end]
		if strings.TrimSpace(argumentsText) != "packages..." {
			arguments = append(arguments, argumentsText)
		}
		offset = end + 1
	}
	return arguments
}

func lspNodeBalancedDelimiter(source string, start int, opening, closing byte) int {
	depth := 0
	for index := start; index < len(source); index++ {
		switch source[index] {
		case opening:
			depth++
		case closing:
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	panic("unterminated runtime Node install argument list")
}

func parseLSPWindowsNpmPackages(t *testing.T, source string) lspWindowsNpmPackages {
	t.Helper()
	baseBlock := regexp.MustCompile(`(?s)\$LSPNpmPackages\s*=\s*@\((.*?)\r?\n\)`).FindStringSubmatch(source)
	if len(baseBlock) != 2 {
		t.Fatal("Windows bundle source is missing the LSPNpmPackages array")
	}
	base := make(map[string]lspNodePackageSpec)
	additions := make(map[string]lspNodePackageSpec)
	add := func(target map[string]lspNodePackageSpec, raw, location string) {
		packageName, version, ok := splitLSPNodePackageSpec(raw)
		if !ok {
			t.Fatalf("Windows bundle %s package %q is unpinned", location, raw)
		}
		if _, exists := target[packageName]; exists {
			t.Fatalf("Windows bundle %s package %q is duplicated", location, packageName)
		}
		target[packageName] = lspNodePackageSpec{packageName: packageName, version: version}
	}

	entryPattern := regexp.MustCompile(`'([^']+)'`)
	for _, match := range entryPattern.FindAllStringSubmatch(baseBlock[1], -1) {
		add(base, match[1], "base")
	}
	if len(base) == 0 {
		t.Fatal("Windows bundle LSPNpmPackages base array is empty")
	}
	additionPattern := regexp.MustCompile(`(?m)\$LSPNpmPackages\s*\+=\s*'([^']+)'`)
	for _, match := range additionPattern.FindAllStringSubmatch(source, -1) {
		add(additions, match[1], "conditional")
	}
	all := make(map[string]lspNodePackageSpec, len(base)+len(additions))
	for packageName, spec := range base {
		all[packageName] = spec
	}
	for packageName, spec := range additions {
		if _, exists := all[packageName]; exists {
			t.Fatalf("Windows bundle package %q is duplicated between base and conditional lists", packageName)
		}
		all[packageName] = spec
	}
	return lspWindowsNpmPackages{base: base, conditional: additions, all: all}
}

func assertLSPNodePackageSet(t *testing.T, label string, got map[string]lspNodePackageSpec, mappings []lspNodePackageConstMapping) {
	t.Helper()
	want := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		want = append(want, mapping.packageName)
	}
	assertLSPPackageNames(t, label, got, want)
}

func assertLSPPackageNames(t *testing.T, label string, got map[string]lspNodePackageSpec, want []string) {
	t.Helper()
	gotNames := make([]string, 0, len(got))
	for packageName := range got {
		gotNames = append(gotNames, packageName)
	}
	slices.Sort(want)
	slices.Sort(gotNames)
	if !slices.Equal(gotNames, want) {
		t.Fatalf("%s package set = %#v, want %#v", label, gotNames, want)
	}
}

func assertLSPNodePinsMatch(t *testing.T, runtimePackages, bundlePackages map[string]lspNodePackageSpec, label string) {
	t.Helper()
	for packageName, runtimePackage := range runtimePackages {
		bundlePackage, ok := bundlePackages[packageName]
		if !ok {
			t.Fatalf("%s is missing package %q", label, packageName)
		}
		if runtimePackage.version != bundlePackage.version {
			t.Fatalf("%s package %q version = %q, want %q", label, packageName, bundlePackage.version, runtimePackage.version)
		}
	}
}

func assertLSPWindowsNpmPackageCohort(t *testing.T, runtimePackages map[string]lspNodePackageSpec, bundle lspWindowsNpmPackages) {
	t.Helper()
	const astGrepPackage = "@ast-grep/cli"
	wantBaseNames := make([]string, 0, len(runtimePackages))
	for packageName := range runtimePackages {
		if packageName != "shellcheck" {
			wantBaseNames = append(wantBaseNames, packageName)
		}
	}
	wantBaseNames = append(wantBaseNames, astGrepPackage)
	assertLSPPackageNames(t, "Windows bundle base Node package", bundle.base, wantBaseNames)

	if len(bundle.conditional) != 1 {
		t.Fatalf("Windows bundle conditional Node package count = %d, want one shellcheck package", len(bundle.conditional))
	}
	shellcheck, ok := bundle.conditional["shellcheck"]
	if !ok || shellcheck.version != runtimePackages["shellcheck"].version {
		t.Fatalf("Windows bundle conditional shellcheck package = %#v, want shellcheck@%s", shellcheck, runtimePackages["shellcheck"].version)
	}
	if _, ok := bundle.all[astGrepPackage]; !ok {
		t.Fatalf("Windows bundle is missing allowed bundle-only package %q", astGrepPackage)
	}
	for packageName := range bundle.all {
		if packageName != astGrepPackage {
			if _, ok := runtimePackages[packageName]; !ok {
				t.Fatalf("Windows bundle contains unexpected package %q", packageName)
			}
		}
	}
}

// assertLSPMarkdownPackagePair 守卫 Windows Markdown 服务三件套在 runtime 与 bundle 中的同一性。
func assertLSPMarkdownPackagePair(t *testing.T, runtimePackages, bundlePackages map[string]lspNodePackageSpec) {
	t.Helper()
	for _, packageName := range []string{"vscode-langservers-extracted", "vscode-markdown-languageservice", "markdown-it"} {
		if _, ok := runtimePackages[packageName]; !ok {
			t.Fatalf("runtime Markdown package pair is missing %q", packageName)
		}
		if _, ok := bundlePackages[packageName]; !ok {
			t.Fatalf("Windows bundle Markdown package pair is missing %q", packageName)
		}
	}
	for _, packageName := range []string{"vscode-langservers-extracted", "vscode-markdown-languageservice", "markdown-it"} {
		if runtimePackages[packageName].version != bundlePackages[packageName].version {
			t.Fatalf("Markdown package %q pin differs between runtime and Windows bundle", packageName)
		}
	}
}

func assertLSPNodeShellcheckConditions(t *testing.T, runtimeSource, windowsBundleSource string, runtimePackages map[string]lspNodePackageSpec) {
	t.Helper()
	for _, runtimeCondition := range []string{
		"func runtimeWindowsArchitecture(goarch string) string",
		`case "386", "x86", "i386", "i686":`,
		`return "x86"`,
		"func runtimeShellcheckNPMAvailableForTarget(goos, goarch string) bool",
		`return runtimeWindowsArchitecture(goarch) == "x64"`,
		`packages = append(packages, "shellcheck@"+shellcheckInstallVersion)`,
	} {
		if !strings.Contains(runtimeSource, runtimeCondition) {
			t.Fatalf("runtime shellcheck architecture guard is missing %q", runtimeCondition)
		}
	}
	shellcheck, ok := runtimePackages["shellcheck"]
	if !ok {
		t.Fatal("runtime shellcheck package is missing")
	}
	bundleCondition := `-not $OmitShellcheck -and $ShellcheckBin.Trim() -eq '' -and $WindowsPackageArch -ne 'arm64'`
	conditionPattern := regexp.MustCompile(`(?s)if\s*\(\s*` + regexp.QuoteMeta(bundleCondition) + `\s*\)\s*\{\s*\$LSPNpmPackages\s*\+=\s*'` + regexp.QuoteMeta("shellcheck@"+shellcheck.version) + `'\s*\}`)
	if !conditionPattern.MatchString(windowsBundleSource) {
		t.Fatalf("Windows bundle shellcheck package does not retain the exact conditional cohort: %s", bundleCondition)
	}
	for _, condition := range []string{"if ($OmitShellcheck)", "if ($WindowsPackageArch -eq 'arm64')"} {
		if !strings.Contains(windowsBundleSource, condition) {
			t.Fatalf("Windows bundle shellcheck source is missing condition %q", condition)
		}
	}
}
