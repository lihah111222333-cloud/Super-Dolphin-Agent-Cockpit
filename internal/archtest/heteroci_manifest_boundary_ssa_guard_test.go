package archtest

import (
	"fmt"
	"go/types"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

type heterociManifestBoundaryObjects struct {
	open          types.Object
	openFile      types.Object
	readFile      types.Object
	fsOpen        types.Object
	jsonDecode    types.Object
	jsonUnmarshal types.Object
}

func (guard *heterociManifestSSAGuard) aggregateManifestBoundaryFindings() []string {
	objects := guard.aggregateManifestBoundaryObjects()
	var findings []string
	for _, function := range guard.allFunctions() {
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				if finding := guard.aggregateManifestFileIOFinding(function, call.Common(), objects); finding != "" {
					findings = append(findings, finding)
				}
				if finding := guard.aggregateManifestJSONFinding(function, call.Common(), objects); finding != "" {
					findings = append(findings, finding)
				}
			}
		}
	}
	return findings
}

func (guard *heterociManifestSSAGuard) aggregateManifestBoundaryObjects() heterociManifestBoundaryObjects {
	osPackage := findHeterociManifestSSATypesPackage(guard.pkg.Types, "os", make(map[*types.Package]bool))
	fsPackage := findHeterociManifestSSATypesPackage(guard.pkg.Types, "io/fs", make(map[*types.Package]bool))
	jsonPackage := findHeterociManifestSSATypesPackage(guard.pkg.Types, "encoding/json", make(map[*types.Package]bool))
	return heterociManifestBoundaryObjects{
		open:          heterociManifestBoundaryObject(osPackage, "Open"),
		openFile:      heterociManifestBoundaryObject(osPackage, "OpenFile"),
		readFile:      heterociManifestBoundaryObject(osPackage, "ReadFile"),
		fsOpen:        heterociManifestBoundaryMethod(fsPackage, "FS", "Open", false),
		jsonDecode:    heterociManifestBoundaryMethod(jsonPackage, "Decoder", "Decode", true),
		jsonUnmarshal: heterociManifestBoundaryObject(jsonPackage, "Unmarshal"),
	}
}

func heterociManifestBoundaryObject(pkg *types.Package, name string) types.Object {
	if pkg == nil {
		return nil
	}
	return pkg.Scope().Lookup(name)
}

func heterociManifestBoundaryMethod(pkg *types.Package, typeName, methodName string, pointer bool) types.Object {
	if pkg == nil {
		return nil
	}
	typeObject, _ := pkg.Scope().Lookup(typeName).(*types.TypeName)
	if typeObject == nil {
		return nil
	}
	typ := typeObject.Type()
	if pointer {
		typ = types.NewPointer(typ)
	}
	selection := types.NewMethodSet(typ).Lookup(pkg, methodName)
	if selection == nil {
		return nil
	}
	return selection.Obj()
}

func heterociManifestBoundaryCallObject(common *ssa.CallCommon) types.Object {
	if common == nil {
		return nil
	}
	if static := common.StaticCallee(); static != nil {
		return static.Object()
	}
	return common.Method
}

func (guard *heterociManifestSSAGuard) aggregateManifestFileIOFinding(
	function *ssa.Function,
	common *ssa.CallCommon,
	objects heterociManifestBoundaryObjects,
) string {
	name := heterociManifestBoundaryFileIOName(heterociManifestBoundaryCallObject(common), objects)
	if name == "" || function.Object() == guard.loader {
		return ""
	}
	return fmt.Sprintf("manifest-file-io %s calls %s outside LoadManifestFile", guard.functionIdentity(function), name)
}

func heterociManifestBoundaryFileIOName(object types.Object, objects heterociManifestBoundaryObjects) string {
	switch object {
	case objects.open:
		return "os.Open"
	case objects.openFile:
		return "os.OpenFile"
	case objects.readFile:
		return "os.ReadFile"
	case objects.fsOpen:
		return "io/fs.FS.Open"
	default:
		return ""
	}
}

func (guard *heterociManifestSSAGuard) aggregateManifestJSONFinding(
	function *ssa.Function,
	common *ssa.CallCommon,
	objects heterociManifestBoundaryObjects,
) string {
	object := heterociManifestBoundaryCallObject(common)
	name := heterociManifestBoundaryJSONName(object, objects)
	if name == "" || function.Object() == guard.decoder || function.Object() == guard.loader {
		return ""
	}
	if !guard.aggregateManifestJSONWritesManifest(function, common) {
		return ""
	}
	return fmt.Sprintf("manifest-json %s writes Manifest through %s outside canonical lifecycle", guard.functionIdentity(function), name)
}

func heterociManifestBoundaryJSONName(object types.Object, objects heterociManifestBoundaryObjects) string {
	switch object {
	case objects.jsonDecode:
		return "json.Decoder.Decode"
	case objects.jsonUnmarshal:
		return "json.Unmarshal"
	default:
		return ""
	}
}

func (guard *heterociManifestSSAGuard) aggregateManifestJSONWritesManifest(function *ssa.Function, common *ssa.CallCommon) bool {
	if slices.ContainsFunc(common.Args, guard.aggregateManifestValue) {
		return true
	}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			store, ok := instruction.(*ssa.Store)
			if ok && (guard.aggregateManifestType(store.Addr.Type()) || guard.aggregateManifestType(store.Val.Type())) {
				return true
			}
		}
	}
	return false
}

func (guard *heterociManifestSSAGuard) aggregateManifestValue(value ssa.Value) bool {
	if value == nil {
		return false
	}
	if guard.aggregateManifestType(value.Type()) {
		return true
	}
	switch typed := value.(type) {
	case *ssa.ChangeInterface:
		return guard.aggregateManifestValue(typed.X)
	case *ssa.ChangeType:
		return guard.aggregateManifestValue(typed.X)
	case *ssa.Convert:
		return guard.aggregateManifestValue(typed.X)
	case *ssa.MakeInterface:
		return guard.aggregateManifestValue(typed.X)
	case *ssa.UnOp:
		return guard.aggregateManifestValue(typed.X)
	default:
		return false
	}
}

func (guard *heterociManifestSSAGuard) aggregateManifestType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	if types.Identical(typ, guard.manifestType) {
		return true
	}
	pointer, ok := typ.(*types.Pointer)
	return ok && guard.aggregateManifestType(pointer.Elem())
}

func TestHeterociManifestBoundaryProductionGreen(t *testing.T) {
	guard := loadHeterociManifestSSAGuard(t, nil)
	if findings := guard.aggregateManifestBoundaryFindings(); len(findings) != 0 {
		t.Fatalf("production manifest boundary findings:\n%s", strings.Join(findings, "\n"))
	}
}

func TestHeterociManifestBoundaryRejectsOutParamDecoders(t *testing.T) {
	source := `package heteroci
import (
	aliasjson "encoding/json"
	aliasos "os"
)
func LoadManifestInto(path string, out *Manifest) error {
	file, err := aliasos.Open(path)
	if err != nil { return err }
	return aliasjson.NewDecoder(file).Decode(out)
}
func LoadManifestIntoRead(path string, out *Manifest) error {
	raw, err := aliasos.ReadFile(path)
	if err != nil { return err }
	return aliasjson.Unmarshal(raw, out)
}
`
	findings := loadHeterociManifestSSAGuard(t, heterociManifestSSAAdditionalOverlay(t, source)).aggregateFindings()
	assertHeterociManifestSSAFinding(t, findings, "manifest-file-io", "LoadManifestInto")
	assertHeterociManifestSSAFinding(t, findings, "manifest-json", "LoadManifestInto")
	assertHeterociManifestSSAFinding(t, findings, "manifest-file-io", "LoadManifestIntoRead")
	assertHeterociManifestSSAFinding(t, findings, "manifest-json", "LoadManifestIntoRead")
}

func TestHeterociManifestBoundaryRejectsMethodAliasDecoder(t *testing.T) {
	source := `package heteroci
import (
	"encoding/json"
	"os"
)
type manifestAlias = Manifest
type manifestMethodLoader struct{}
func (manifestMethodLoader) LoadManifestInto(path string, out *manifestAlias) error {
	file, err := os.OpenFile(path, os.O_RDONLY, 0o600)
	if err != nil { return err }
	return json.NewDecoder(file).Decode(out)
}
`
	findings := loadHeterociManifestSSAGuard(t, heterociManifestSSAAdditionalOverlay(t, source)).aggregateFindings()
	assertHeterociManifestSSAFinding(t, findings, "manifest-file-io", "LoadManifestInto")
	assertHeterociManifestSSAFinding(t, findings, "manifest-json", "LoadManifestInto")
}

func TestHeterociManifestBoundaryRejectsFSOpenDecoder(t *testing.T) {
	source := `package heteroci
import (
	"encoding/json"
	"io/fs"
)
func illicitManifestFSOpen(source fs.FS, out *Manifest) error {
	file, err := source.Open("manifest.json")
	if err != nil { return err }
	return json.NewDecoder(file).Decode(out)
}
`
	findings := loadHeterociManifestSSAGuard(t, heterociManifestSSAAdditionalOverlay(t, source)).aggregateFindings()
	assertHeterociManifestSSAFinding(t, findings, "manifest-file-io", "illicitManifestFSOpen")
	assertHeterociManifestSSAFinding(t, findings, "manifest-json", "illicitManifestFSOpen")
}

func TestHeterociManifestBoundaryIgnoresLocalShadows(t *testing.T) {
	source := `package heteroci
type localFile struct{}
type localDecoder struct{}
func Open(string) (*localFile, error) { return nil, nil }
func Unmarshal([]byte, any) error { return nil }
func (localDecoder) Decode(any) error { return nil }
func illicitManifestLocalShadow(path string, out *Manifest) error {
	_, _ = Open(path)
	_ = Unmarshal(nil, out)
	var decoder localDecoder
	_ = decoder.Decode(out)
	return nil
}
`
	findings := loadHeterociManifestSSAGuard(t, heterociManifestSSAAdditionalOverlay(t, source)).aggregateManifestBoundaryFindings()
	if len(findings) != 0 {
		t.Fatalf("local shadow/external names produced findings: %v", findings)
	}
}
