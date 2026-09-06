package credentialactivation

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestActivationDTOsContainNoBearerOrMutableTransportFields(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[PreparedCredential](), reflect.TypeFor[ActiveCredentialReceipt](), reflect.TypeFor[CurrentBinding](), reflect.TypeFor[SecurityTimeQuery](), reflect.TypeFor[SecurityTimeObservation](), reflect.TypeFor[AntiReplayRequest](), reflect.TypeFor[AntiReplayReceipt](), reflect.TypeFor[ActivationRequest](), reflect.TypeFor[Candidate](),
	} {
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
			if strings.Contains(strings.ToLower(field.Name+" "+jsonName), "bearer") || strings.Contains(strings.ToLower(field.Name+" "+jsonName), "token") || strings.Contains(strings.ToLower(field.Name+" "+jsonName), "secret") {
				t.Fatalf("%s contains credential material field %s", typ.Name(), field.Name)
			}
			switch field.Type.Kind() {
			case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Interface:
				t.Fatalf("%s.%s carries mutable or opaque values", typ.Name(), field.Name)
			}
		}
	}
}

func TestActivationCandidateProductionImportsRemainNarrow(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	production := filepath.Join(filepath.Dir(current), "candidate.go")
	file, err := parser.ParseFile(token.NewFileSet(), production, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"context": true, "crypto/sha256": true, "encoding/hex": true, "encoding/json": true, "errors": true, "fmt": true, "reflect": true, "strings": true, "time": true, "unicode": true, "unicode/utf8": true}
	for _, imported := range file.Imports {
		name, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if !allowed[name] {
			t.Fatalf("candidate imports implementation dependency %q", name)
		}
	}
}

func TestActivationCandidateIsNotWiredIntoProduction(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "../../../../../"))
	candidateDir := filepath.Dir(current)
	candidateImport := "/cmd/mcp-orch/standalone/managedruntime/credentialactivation"
	for _, tree := range []string{"cmd", "internal", "pkg"} {
		if err := scanActivationTree(filepath.Join(root, tree), candidateDir, candidateImport); err != nil {
			t.Fatal(err)
		}
	}
}

func scanActivationTree(root, candidateDir, candidateImport string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return inspectActivationSource(path, entry, candidateDir, candidateImport)
	})
}

func inspectActivationSource(path string, entry os.DirEntry, candidateDir, candidateImport string) error {
	if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(path, candidateDir+string(filepath.Separator)) {
		return nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return err
	}
	for _, imported := range file.Imports {
		value, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			return err
		}
		if strings.HasSuffix(value, candidateImport) {
			return &activationWiringError{path: path}
		}
	}
	return nil
}

type activationWiringError struct{ path string }

func (e *activationWiringError) Error() string {
	return "credential activation candidate is wired: " + e.path
}

func TestActivationJSONSurfaceIsStable(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[PreparedCredential](), reflect.TypeFor[ActiveCredentialReceipt](), reflect.TypeFor[CurrentBinding](), reflect.TypeFor[SecurityTimeQuery](), reflect.TypeFor[SecurityTimeObservation](), reflect.TypeFor[AntiReplayRequest](), reflect.TypeFor[AntiReplayReceipt](), reflect.TypeFor[ActivationRequest](), reflect.TypeFor[Candidate]()} {
		fields := make([]string, typ.NumField())
		for index := range fields {
			fields[index] = typ.Field(index).Tag.Get("json")
		}
		for _, field := range fields {
			if field == "" || strings.Contains(field, ",") {
				t.Fatalf("%s has non-exact json field", typ.Name())
			}
		}
		if !sort.StringsAreSorted(fields) {
			sort.Strings(fields)
		}
	}
}
