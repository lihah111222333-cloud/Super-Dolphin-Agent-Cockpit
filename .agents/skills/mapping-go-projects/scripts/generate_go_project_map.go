package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const mapVersion = "1"

type projectMap struct {
	Version     string       `json:"version"`
	Generated   string       `json:"generated_at"`
	Root        string       `json:"root"`
	Module      string       `json:"module,omitempty"`
	Counts      counts       `json:"counts"`
	Packages    []pkgInfo    `json:"packages"`
	Symbols     []symbolInfo `json:"symbols"`
	Imports     []importInfo `json:"imports"`
	Skipped     []string     `json:"skipped,omitempty"`
	Limitations []string     `json:"limitations"`
}

type counts struct {
	Packages int `json:"packages"`
	Files    int `json:"files"`
	Imports  int `json:"imports"`
	Symbols  int `json:"symbols"`
}

type pkgInfo struct {
	Name       string         `json:"name"`
	ImportPath string         `json:"import_path"`
	Dir        string         `json:"dir"`
	Doc        string         `json:"doc,omitempty"`
	Files      []string       `json:"files"`
	Imports    []string       `json:"imports"`
	Symbols    map[string]int `json:"symbols"`
}

type symbolInfo struct {
	Kind          string      `json:"kind"`
	Name          string      `json:"name"`
	QualifiedName string      `json:"qualified_name"`
	Package       string      `json:"package"`
	ImportPath    string      `json:"import_path"`
	File          string      `json:"file"`
	Line          int         `json:"line"`
	EndLine       int         `json:"end_line"`
	Receiver      string      `json:"receiver,omitempty"`
	Signature     string      `json:"signature,omitempty"`
	Doc           string      `json:"doc,omitempty"`
	Exported      bool        `json:"exported"`
	Fields        []fieldInfo `json:"fields,omitempty"`
}

type fieldInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Tag  string `json:"tag,omitempty"`
	Line int    `json:"line"`
}

type importInfo struct {
	FromPackage string `json:"from_package"`
	FromPath    string `json:"from_path"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Alias       string `json:"alias,omitempty"`
	Path        string `json:"path"`
}

type packageBuilder struct {
	info    pkgInfo
	imports map[string]struct{}
}

func main() {
	var root string
	var outDir string
	var includeGenerated bool
	flag.StringVar(&root, "root", ".", "project root")
	flag.StringVar(&outDir, "out", ".project-map", "output directory")
	flag.BoolVar(&includeGenerated, "include-generated", false, "include generated Go files")
	flag.Parse()

	absRoot, err := filepath.Abs(root)
	if err != nil {
		exitf("resolve root: %v", err)
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		exitf("resolve symlink root: %v", err)
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		exitf("resolve output directory: %v", err)
	}

	module := readModule(absRoot)
	pm, err := buildProjectMap(absRoot, module, includeGenerated)
	if err != nil {
		exitf("%v", err)
	}
	pm.Root = absRoot

	if err := os.MkdirAll(absOut, 0o755); err != nil {
		exitf("create output directory: %v", err)
	}
	if err := writeJSON(filepath.Join(absOut, "project-map.json"), pm); err != nil {
		exitf("write project-map.json: %v", err)
	}
	if err := writeMarkdown(filepath.Join(absOut, "PROJECT_MAP.md"), pm); err != nil {
		exitf("write PROJECT_MAP.md: %v", err)
	}
	if err := writePackagesTSV(filepath.Join(absOut, "packages.tsv"), pm.Packages); err != nil {
		exitf("write packages.tsv: %v", err)
	}
	if err := writeSymbolsTSV(filepath.Join(absOut, "symbols.tsv"), pm.Symbols); err != nil {
		exitf("write symbols.tsv: %v", err)
	}
	if err := writeImportsTSV(filepath.Join(absOut, "imports.tsv"), pm.Imports); err != nil {
		exitf("write imports.tsv: %v", err)
	}

	fmt.Printf("Project map generated: %s\n", absOut)
	fmt.Printf("Packages: %d, files: %d, symbols: %d, imports: %d\n", pm.Counts.Packages, pm.Counts.Files, pm.Counts.Symbols, pm.Counts.Imports)
}

func buildProjectMap(root, module string, includeGenerated bool) (projectMap, error) {
	pm := projectMap{
		Version:   mapVersion,
		Generated: time.Now().UTC().Format(time.RFC3339),
		Module:    module,
		Limitations: []string{
			"syntactic AST index only; it does not prove behavior or type-checked references",
			"generated files are skipped by default unless -include-generated is used",
			"source files remain the authority for edits and claims",
		},
	}

	packages := map[string]*packageBuilder{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if shouldSkipDir(root, path, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		if strings.HasSuffix(entry.Name(), "_generated.go") && !includeGenerated {
			rel := relPath(root, path)
			pm.Skipped = append(pm.Skipped, rel)
			return nil
		}
		if !includeGenerated {
			generated, err := isGeneratedGoFile(path)
			if err != nil {
				return err
			}
			if generated {
				pm.Skipped = append(pm.Skipped, relPath(root, path))
				return nil
			}
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", relPath(root, path), err)
		}
		relFile := relPath(root, path)
		dir := relPath(root, filepath.Dir(path))
		if dir == "." {
			dir = "."
		}
		importPath := packageImportPath(module, dir)
		key := dir + "\x00" + file.Name.Name

		builder, ok := packages[key]
		if !ok {
			builder = &packageBuilder{
				info: pkgInfo{
					Name:       file.Name.Name,
					ImportPath: importPath,
					Dir:        dir,
					Doc:        firstDocLine(file.Doc),
					Symbols:    map[string]int{},
				},
				imports: map[string]struct{}{},
			}
			packages[key] = builder
		}
		if builder.info.Doc == "" {
			builder.info.Doc = firstDocLine(file.Doc)
		}
		builder.info.Files = append(builder.info.Files, relFile)
		pm.Counts.Files++

		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				importPath = strings.Trim(imp.Path.Value, `"`)
			}
			alias := ""
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			builder.imports[importPath] = struct{}{}
			pm.Imports = append(pm.Imports, importInfo{
				FromPackage: file.Name.Name,
				FromPath:    builder.info.ImportPath,
				File:        relFile,
				Line:        fset.Position(imp.Pos()).Line,
				Alias:       alias,
				Path:        importPath,
			})
		}

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				pm.Symbols = append(pm.Symbols, symbolsFromGenDecl(fset, d, builder.info, relFile)...)
			case *ast.FuncDecl:
				pm.Symbols = append(pm.Symbols, symbolFromFuncDecl(fset, d, builder.info, relFile))
			}
		}
		return nil
	})
	if err != nil {
		return pm, err
	}

	for _, sym := range pm.Symbols {
		for _, builder := range packages {
			if builder.info.ImportPath == sym.ImportPath && builder.info.Name == sym.Package {
				builder.info.Symbols[sym.Kind]++
				break
			}
		}
	}

	for _, builder := range packages {
		for imp := range builder.imports {
			builder.info.Imports = append(builder.info.Imports, imp)
		}
		sort.Strings(builder.info.Files)
		sort.Strings(builder.info.Imports)
		pm.Packages = append(pm.Packages, builder.info)
	}

	sort.Slice(pm.Packages, func(i, j int) bool {
		if pm.Packages[i].Dir == pm.Packages[j].Dir {
			return pm.Packages[i].Name < pm.Packages[j].Name
		}
		return pm.Packages[i].Dir < pm.Packages[j].Dir
	})
	sort.Slice(pm.Symbols, func(i, j int) bool {
		if pm.Symbols[i].File == pm.Symbols[j].File {
			if pm.Symbols[i].Line == pm.Symbols[j].Line {
				return pm.Symbols[i].Name < pm.Symbols[j].Name
			}
			return pm.Symbols[i].Line < pm.Symbols[j].Line
		}
		return pm.Symbols[i].File < pm.Symbols[j].File
	})
	sort.Slice(pm.Imports, func(i, j int) bool {
		if pm.Imports[i].File == pm.Imports[j].File {
			if pm.Imports[i].Line == pm.Imports[j].Line {
				return pm.Imports[i].Path < pm.Imports[j].Path
			}
			return pm.Imports[i].Line < pm.Imports[j].Line
		}
		return pm.Imports[i].File < pm.Imports[j].File
	})
	sort.Strings(pm.Skipped)

	pm.Counts.Packages = len(pm.Packages)
	pm.Counts.Imports = len(pm.Imports)
	pm.Counts.Symbols = len(pm.Symbols)
	return pm, nil
}

func symbolsFromGenDecl(fset *token.FileSet, decl *ast.GenDecl, pkg pkgInfo, file string) []symbolInfo {
	var out []symbolInfo
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			kind := "type"
			var fields []fieldInfo
			switch t := s.Type.(type) {
			case *ast.StructType:
				kind = "struct"
				fields = fieldsFromList(fset, t.Fields)
			case *ast.InterfaceType:
				kind = "interface"
				fields = fieldsFromList(fset, t.Methods)
			}
			out = append(out, symbolInfo{
				Kind:          kind,
				Name:          s.Name.Name,
				QualifiedName: qualify(pkg, s.Name.Name),
				Package:       pkg.Name,
				ImportPath:    pkg.ImportPath,
				File:          file,
				Line:          fset.Position(s.Pos()).Line,
				EndLine:       fset.Position(s.End()).Line,
				Signature:     compact(formatNode(fset, s.Type)),
				Doc:           firstDocLine(docForSpec(decl.Doc, s.Doc)),
				Exported:      ast.IsExported(s.Name.Name),
				Fields:        fields,
			})
		case *ast.ValueSpec:
			kind := strings.ToLower(decl.Tok.String())
			for _, name := range s.Names {
				signature := ""
				if s.Type != nil {
					signature = compact(formatNode(fset, s.Type))
				}
				out = append(out, symbolInfo{
					Kind:          kind,
					Name:          name.Name,
					QualifiedName: qualify(pkg, name.Name),
					Package:       pkg.Name,
					ImportPath:    pkg.ImportPath,
					File:          file,
					Line:          fset.Position(name.Pos()).Line,
					EndLine:       fset.Position(s.End()).Line,
					Signature:     signature,
					Doc:           firstDocLine(docForSpec(decl.Doc, s.Doc)),
					Exported:      ast.IsExported(name.Name),
				})
			}
		}
	}
	return out
}

func symbolFromFuncDecl(fset *token.FileSet, fn *ast.FuncDecl, pkg pkgInfo, file string) symbolInfo {
	kind := "func"
	receiver := ""
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		kind = "method"
		receiver = receiverName(fset, fn.Recv.List[0].Type)
	}
	qualified := qualify(pkg, fn.Name.Name)
	if receiver != "" {
		qualified = qualify(pkg, receiver+"."+fn.Name.Name)
	}
	return symbolInfo{
		Kind:          kind,
		Name:          fn.Name.Name,
		QualifiedName: qualified,
		Package:       pkg.Name,
		ImportPath:    pkg.ImportPath,
		File:          file,
		Line:          fset.Position(fn.Pos()).Line,
		EndLine:       fset.Position(fn.End()).Line,
		Receiver:      receiver,
		Signature:     compact(formatNode(fset, fn.Type)),
		Doc:           firstDocLine(fn.Doc),
		Exported:      ast.IsExported(fn.Name.Name),
	}
}

func qualify(pkg pkgInfo, name string) string {
	if pkg.ImportPath == "." || pkg.ImportPath == "" {
		return pkg.Name + "." + name
	}
	return pkg.ImportPath + "." + name
}

func fieldsFromList(fset *token.FileSet, fields *ast.FieldList) []fieldInfo {
	if fields == nil {
		return nil
	}
	var out []fieldInfo
	for _, field := range fields.List {
		fieldType := compact(formatNode(fset, field.Type))
		tag := ""
		if field.Tag != nil {
			tag = field.Tag.Value
		}
		if len(field.Names) == 0 {
			out = append(out, fieldInfo{
				Name: fieldType,
				Type: fieldType,
				Tag:  tag,
				Line: fset.Position(field.Pos()).Line,
			})
			continue
		}
		for _, name := range field.Names {
			out = append(out, fieldInfo{
				Name: name.Name,
				Type: fieldType,
				Tag:  tag,
				Line: fset.Position(name.Pos()).Line,
			})
		}
	}
	return out
}

func readModule(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func packageImportPath(module, dir string) string {
	cleanDir := strings.TrimPrefix(filepath.ToSlash(dir), "./")
	if module == "" {
		if cleanDir == "." || cleanDir == "" {
			return "."
		}
		return "./" + cleanDir
	}
	if cleanDir == "." || cleanDir == "" {
		return module
	}
	return module + "/" + cleanDir
}

func shouldSkipDir(root, path, name string) bool {
	if path == root {
		return false
	}
	switch name {
	case ".git", ".agents", ".github", ".githooks", ".project-map", "vendor", "node_modules", "dist", "build", "tmp", "coverage", "testdata":
		return true
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	return false
}

func isGeneratedGoFile(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	prefix := string(data)
	if len(prefix) > 4096 {
		prefix = prefix[:4096]
	}
	return strings.Contains(prefix, "Code generated") && strings.Contains(prefix, "DO NOT EDIT"), nil
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	if rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}

func docForSpec(groupDoc, specDoc *ast.CommentGroup) *ast.CommentGroup {
	if specDoc != nil {
		return specDoc
	}
	return groupDoc
}

func firstDocLine(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(group.Text()), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func receiverName(fset *token.FileSet, expr ast.Expr) string {
	switch r := expr.(type) {
	case *ast.Ident:
		return r.Name
	case *ast.StarExpr:
		return receiverName(fset, r.X)
	case *ast.IndexExpr:
		return receiverName(fset, r.X)
	case *ast.IndexListExpr:
		return receiverName(fset, r.X)
	default:
		return compact(formatNode(fset, expr))
	}
}

func formatNode(fset *token.FileSet, node any) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return ""
	}
	return buf.String()
}

func compact(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func writeJSON(path string, pm projectMap) error {
	data, err := json.MarshalIndent(pm, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func writeMarkdown(path string, pm projectMap) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Project Map\n\n")
	fmt.Fprintf(&b, "Generated: `%s`\n\n", pm.Generated)
	if pm.Module != "" {
		fmt.Fprintf(&b, "Module: `%s`\n\n", pm.Module)
	}
	fmt.Fprintf(&b, "Counts: `%d` packages, `%d` files, `%d` symbols, `%d` imports.\n\n", pm.Counts.Packages, pm.Counts.Files, pm.Counts.Symbols, pm.Counts.Imports)
	fmt.Fprintf(&b, "Query first with:\n\n")
	fmt.Fprintf(&b, "```bash\npython3 .agents/skills/mapping-go-projects/scripts/query_project_map.py --root . SymbolName\nrg -n 'SymbolName' .project-map/symbols.tsv\n```\n\n")
	fmt.Fprintf(&b, "## Packages\n\n")
	fmt.Fprintf(&b, "| Package | Import Path | Dir | Files | Symbols |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | ---: | --- |\n")
	for _, pkg := range pm.Packages {
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %d | %s |\n", pkg.Name, pkg.ImportPath, pkg.Dir, len(pkg.Files), symbolCounts(pkg.Symbols))
	}
	fmt.Fprintf(&b, "\n## Symbols\n\n")
	fmt.Fprintf(&b, "| Kind | Name | Qualified | Location | Signature |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- |\n")
	for _, sym := range pm.Symbols {
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | `%s:%d` | `%s` |\n", sym.Kind, sym.Name, sym.QualifiedName, sym.File, sym.Line, escapeMarkdownCell(sym.Signature))
	}
	if len(pm.Skipped) > 0 {
		fmt.Fprintf(&b, "\n## Skipped Files\n\n")
		for _, file := range pm.Skipped {
			fmt.Fprintf(&b, "- `%s`\n", file)
		}
	}
	fmt.Fprintf(&b, "\n## Limitations\n\n")
	for _, item := range pm.Limitations {
		fmt.Fprintf(&b, "- %s\n", item)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func symbolCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func writePackagesTSV(path string, packages []pkgInfo) error {
	var b strings.Builder
	fmt.Fprintf(&b, "package\timport_path\tdir\tfiles\timports\tdoc\n")
	for _, pkg := range packages {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n",
			tsv(pkg.Name),
			tsv(pkg.ImportPath),
			tsv(pkg.Dir),
			tsv(strings.Join(pkg.Files, ",")),
			tsv(strings.Join(pkg.Imports, ",")),
			tsv(pkg.Doc),
		)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeSymbolsTSV(path string, symbols []symbolInfo) error {
	var b strings.Builder
	fmt.Fprintf(&b, "kind\tname\tqualified_name\tpackage\timport_path\tfile\tline\tend_line\treceiver\tsignature\tdoc\texported\tfields\n")
	for _, sym := range symbols {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%t\t%s\n",
			tsv(sym.Kind),
			tsv(sym.Name),
			tsv(sym.QualifiedName),
			tsv(sym.Package),
			tsv(sym.ImportPath),
			tsv(sym.File),
			sym.Line,
			sym.EndLine,
			tsv(sym.Receiver),
			tsv(sym.Signature),
			tsv(sym.Doc),
			sym.Exported,
			tsv(formatFields(sym.Fields)),
		)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeImportsTSV(path string, imports []importInfo) error {
	var b strings.Builder
	fmt.Fprintf(&b, "from_package\tfrom_path\tfile\tline\talias\timport\n")
	for _, imp := range imports {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%d\t%s\t%s\n",
			tsv(imp.FromPackage),
			tsv(imp.FromPath),
			tsv(imp.File),
			imp.Line,
			tsv(imp.Alias),
			tsv(imp.Path),
		)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func formatFields(fields []fieldInfo) string {
	if len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		if field.Tag != "" {
			parts = append(parts, fmt.Sprintf("%s %s %s", field.Name, field.Type, field.Tag))
		} else {
			parts = append(parts, fmt.Sprintf("%s %s", field.Name, field.Type))
		}
	}
	return strings.Join(parts, "; ")
}

func tsv(value string) string {
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func escapeMarkdownCell(value string) string {
	value = strings.ReplaceAll(value, "`", "'")
	value = strings.ReplaceAll(value, "|", "\\|")
	return value
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
