package main

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

func TestProductionGoRequirementMatchesPortableGoContract(t *testing.T) {
	requirement, err := productionGoRequirementFromEntries([]sourceexport.TreeEntry{{
		Path: productionGoModPath,
		Data: []byte("module example.test/repo\n\ngo " + strings.TrimPrefix(portableGoVersion, "go") + "\n"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if requirement.Minimum != portableGoVersion || requirement.Preferred != portableGoVersion {
		t.Fatalf("requirement = %#v, want portable version %q", requirement, portableGoVersion)
	}
}

func TestProductionGoRequirementFromEntries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		goMod     string
		minimum   string
		preferred string
		wantErr   bool
	}{
		{name: "go directive", goMod: "module example.test/repo\n\ngo 1.26.5\n", minimum: "go1.26.5", preferred: "go1.26.5"},
		{name: "newer toolchain", goMod: "module example.test/repo\n\ngo 1.26.5\n\ntoolchain go1.27.1\n", minimum: "go1.26.5", preferred: "go1.27.1"},
		{name: "default toolchain", goMod: "module example.test/repo\n\ngo 1.26.5\n\ntoolchain default\n", minimum: "go1.26.5", preferred: "go1.26.5"},
		{name: "missing go", goMod: "module example.test/repo\n", wantErr: true},
		{name: "older toolchain", goMod: "module example.test/repo\n\ngo 1.26.5\n\ntoolchain go1.24.9\n", wantErr: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			requirement, err := productionGoRequirementFromEntries([]sourceexport.TreeEntry{{
				Path: productionGoModPath,
				Data: []byte(test.goMod),
			}})
			if test.wantErr {
				if err == nil {
					t.Fatal("invalid candidate go.mod was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if requirement.Minimum != test.minimum || requirement.Preferred != test.preferred {
				t.Fatalf("requirement = %#v, want minimum=%q preferred=%q", requirement, test.minimum, test.preferred)
			}
		})
	}
}

func TestProductionGoRequirementRejectsMissingAndDuplicateGoMod(t *testing.T) {
	t.Parallel()
	if _, err := productionGoRequirementFromEntries(nil); err == nil {
		t.Fatal("missing go.mod was accepted")
	}
	entries := []sourceexport.TreeEntry{
		{Path: productionGoModPath, Data: []byte("module one\n\ngo 1.26.5\n")},
		{Path: productionGoModPath, Data: []byte("module two\n\ngo 1.26.5\n")},
	}
	if _, err := productionGoRequirementFromEntries(entries); err == nil {
		t.Fatal("duplicate go.mod entries were accepted")
	}
}

func TestResolveRemoteBaselineGoToolchainUsesRootModule(t *testing.T) {
	t.Parallel()
	entries := []sourceexport.TreeEntry{
		{Path: "go.mod", Data: []byte("module root\n\ngo 1.26.5\n")},
		{Path: "build/gate/runtime-proxy/go.mod", Data: []byte("module proxy\n\ngo 1.26.5\n")},
		{Path: "build/gate/runtime-tools/go.mod", Data: []byte("module tools\n\ngo 1.26.5\n")},
		{Path: "third_party/example/go.mod", Data: []byte("module thirdparty\n\ngo 1.24.0\n\ntoolchain go1.26.1\n")},
		{Path: "docs/go.mod.example", Data: []byte("not a module")},
	}
	toolchain, err := resolveRemoteBaselineGoToolchain(entries)
	if err != nil {
		t.Fatal(err)
	}
	if toolchain != "go1.26.5" {
		t.Fatalf("remote baseline Go toolchain = %q, want go1.26.5", toolchain)
	}
}

func TestValidateProductionGoToolchainRequirementRequiresPreferredVersion(t *testing.T) {
	requirement := productionGoRequirement{Minimum: "go1.26.5", Preferred: "go1.26.5"}
	for _, test := range []struct {
		version string
		wantErr bool
	}{
		{version: "go1.26.5"},
		{version: "go1.26.4", wantErr: true},
		{version: "go1.26.6", wantErr: true},
		{version: "go1.27.1", wantErr: true},
	} {
		t.Run(test.version, func(t *testing.T) {
			err := validateProductionGoToolchainRequirement(productionGoToolchain{Version: "go version " + test.version + " test/arch"}, requirement)
			if (err != nil) != test.wantErr {
				t.Fatalf("validate version %s error = %v, wantErr %v", test.version, err, test.wantErr)
			}
		})
	}
}

func TestResolveRemoteBaselineGoToolchainRejectsMissingOrInvalidModules(t *testing.T) {
	t.Parallel()
	if _, err := resolveRemoteBaselineGoToolchain(nil); err == nil {
		t.Fatal("candidate tree without a Go module was accepted")
	}
	entries := []sourceexport.TreeEntry{{Path: "nested/go.mod", Data: []byte("module nested\n")}}
	if _, err := resolveRemoteBaselineGoToolchain(entries); err == nil {
		t.Fatal("candidate tree with an invalid nested Go module was accepted")
	}
}
