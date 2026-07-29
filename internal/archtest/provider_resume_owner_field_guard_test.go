package archtest

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"golang.org/x/tools/go/packages"
)

const resumeSessionRequestImportPath = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"

// TestProviderRecoveryFinalResumeMapperFieldGuard 锁定 binding owner 到最终 provider 请求的真实 mapper。
func TestProviderRecoveryFinalResumeMapperFieldGuard(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	inventory, err := discoverTypedResumeSessionConstructions(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := finalResumeMapperRegistry()
	if err := validateProviderRecoveryConstructions(
		fieldSetForConstructionRegistry(registry),
		registry,
		inventory,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if !structFieldSet(reflect.TypeFor[contract.SessionBinding]())["ProviderRecoveryHome"] {
		t.Fatal("SessionBinding producer is missing ProviderRecoveryHome")
	}
}

// TestProviderRecoveryFinalResumeMapperMutationMatrix 锁定删字段与错误 owner selector 均 fail-first。
func TestProviderRecoveryFinalResumeMapperMutationMatrix(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	path := filepath.Join(root, "internal/provider/unified/session_resolver_auto_resume.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	from := []byte("\t\tClaudeHome:         autoResumeClaudeHome(provider, binding.ProviderRecoveryHome),\n")
	if bytes.Count(source, from) != 1 {
		t.Fatalf("final resume owner mapper count = %d, want 1", bytes.Count(source, from))
	}
	tests := []struct {
		name      string
		rewrite   []byte
		wantError string
	}{
		{name: "deleted field", rewrite: nil, wantError: "field set"},
		{name: "runtime override", rewrite: []byte("\t\tClaudeHome:         strings.TrimSpace(runtimeConfig[\"claudeHome\"].(string)),\n"), wantError: "expression"},
		{name: "empty constant", rewrite: []byte("\t\tClaudeHome:         \"\",\n"), wantError: "empty constant"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			overlay := bytes.Replace(source, from, tc.rewrite, 1)
			inventory, err := discoverTypedResumeSessionConstructions(root, map[string][]byte{path: overlay})
			if err != nil {
				t.Fatal(err)
			}
			registry := finalResumeMapperRegistry()
			err = validateProviderRecoveryConstructions(
				fieldSetForConstructionRegistry(registry),
				registry,
				inventory,
				nil,
			)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("final resume mapper mutation error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

func finalResumeMapperRegistry() map[string]providerRecoveryConstructionGuard {
	return map[string]providerRecoveryConstructionGuard{
		"internal/provider/unified/session_resolver_auto_resume.go:buildAutoResumeRequest": {
			producer: reflect.TypeFor[contract.SessionBinding](),
			fields: map[string]string{
				"Provider": "provider", "AgentID": "binding.AgentID",
				"ThreadID": "autoResumePublicThreadID(publicThreadID)", "ProviderThreadID": "providerThreadID",
				"CWD": "cwd", "Config": "clone.RuntimeConfigMap(runtimeConfig)",
				"PromptSnapshot": "cloneAutoResumePromptSnapshot(promptSnapshot)",
				"ClaudeHome":     "autoResumeClaudeHome(provider, binding.ProviderRecoveryHome)",
				"CodexHome":      "codexHome", "CodexInstanceKey": "codexInstanceKey",
				"CodexModelProvider": "codexModelProvider",
			},
		},
	}
}

func fieldSetForConstructionRegistry(registry map[string]providerRecoveryConstructionGuard) map[string]bool {
	fields := map[string]bool{}
	for _, construction := range registry {
		for field := range construction.fields {
			fields[field] = true
		}
	}
	return fields
}

func discoverTypedResumeSessionConstructions(root string, overlay map[string][]byte) (map[string]providerRecoveryConstruction, error) {
	config := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:     root,
		Overlay: overlay,
	}
	loaded, err := packages.Load(config, "./internal/provider/unified")
	if err != nil {
		return nil, fmt.Errorf("load unified package for final resume owner guard: %w", err)
	}
	inventory := map[string]providerRecoveryConstruction{}
	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("type-check final resume owner guard package %s: %s", pkg.PkgPath, pkg.Errors[0])
		}
		for index, file := range pkg.Syntax {
			relative, err := filepath.Rel(root, pkg.CompiledGoFiles[index])
			if err != nil {
				return nil, fmt.Errorf("rel final resume owner source: %w", err)
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Body == nil {
					continue
				}
				var fields map[string]string
				var discoveryErr error
				ast.Inspect(function.Body, func(node ast.Node) bool {
					literal, ok := node.(*ast.CompositeLit)
					if !ok || !isResumeSessionRequestType(pkg.TypesInfo.TypeOf(literal)) {
						return true
					}
					if fields != nil {
						discoveryErr = fmt.Errorf("%s contains multiple ResumeSessionRequest constructions", function.Name.Name)
						return false
					}
					fields, discoveryErr = providerRecoveryLiteralFields(pkg.Fset, literal)
					return discoveryErr == nil
				})
				if discoveryErr != nil {
					return nil, discoveryErr
				}
				if fields == nil {
					continue
				}
				id := filepath.ToSlash(relative) + ":" + function.Name.Name
				inventory[id] = providerRecoveryConstruction{fields: fields}
			}
		}
	}
	return inventory, nil
}

func isResumeSessionRequestType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(pointer.Elem())
	}
	named, ok := typ.(*types.Named)
	return ok &&
		named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == resumeSessionRequestImportPath &&
		named.Obj().Name() == "ResumeSessionRequest"
}
