package remoteportable

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

func TestBuildPortableGoWorkloadInventoryOmitsDirectoriesWithOnlyDisabledFiles(t *testing.T) {
	reader := newInventoryBuildabilityReader(map[string]string{
		"ignored/ignored.go": "//go:build ignore\n\npackage ignored\n",
		"custom/custom.go":   "//go:build custom_disabled\n\npackage custom\n",
		"mixed/disabled.go":  "//go:build ignore\n\npackage mixed\n",
		"mixed/enabled.go":   "package mixed\n",
	})

	inventory, err := BuildPortableGoWorkloadInventory(context.Background(), reader, "tree", string(RepositoryWorkloadPlatformLinuxAMD64), inventoryBuildabilityModules(), []string{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".\x00./mixed"}
	if !reflect.DeepEqual(inventory.GoPackages, want) {
		t.Fatalf("GoPackages = %#v, want %#v", inventory.GoPackages, want)
	}
}

func TestBuildPortableGoWorkloadInventoryHonorsTargetGOOSAndGOARCH(t *testing.T) {
	reader := newInventoryBuildabilityReader(map[string]string{
		"linux-only/linux-only_linux.go":               "package linuxonly\n",
		"windows-only/windows-only_windows.go":         "package windowsonly\n",
		"amd64-only/amd64-only_amd64.go":               "package amd64only\n",
		"arm64-only/arm64-only_arm64.go":               "package arm64only\n",
		"linux-arm64/linux-arm64_linux_arm64.go":       "package linuxarm64\n",
		"linux-amd64/linux-amd64_linux_amd64.go":       "package linuxamd64\n",
		"windows-amd64/windows-amd64_windows_amd64.go": "package windowsamd64\n",
		"tagged/tagged.go":                             "//go:build windows && amd64\n\npackage tagged\n",
		"linuxish/pkg_linuxish.go":                     "package linuxish\n",
		"plan9-only/plan9-only_plan9.go":               "package plan9only\n",
	})
	modules := inventoryBuildabilityModules()

	cases := []struct {
		name     string
		platform RepositoryWorkloadPlatform
		want     []string
	}{
		{
			name:     "linux amd64",
			platform: RepositoryWorkloadPlatformLinuxAMD64,
			want:     []string{".\x00./amd64-only", ".\x00./linux-amd64", ".\x00./linux-only", ".\x00./linuxish"},
		},
		{
			name:     "windows amd64",
			platform: RepositoryWorkloadPlatformWindowsAMD64,
			want:     []string{".\x00./amd64-only", ".\x00./linuxish", ".\x00./tagged", ".\x00./windows-amd64", ".\x00./windows-only"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			inventory, err := BuildPortableGoWorkloadInventory(context.Background(), reader, "tree", string(testCase.platform), modules, []string{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(inventory.GoPackages, testCase.want) {
				t.Fatalf("GoPackages = %#v, want %#v", inventory.GoPackages, testCase.want)
			}
		})
	}
}

func TestBuildPortableGoWorkloadInventoryOmitsPackageWithAnyUnmatchedEmbed(t *testing.T) {
	reader := newInventoryBuildabilityReader(map[string]string{
		"embed-only/embed.go":             "package embedonly\n\nimport _ \"embed\"\n\n//go:embed assets/missing.txt\nvar missing []byte\n",
		"embed-mixed/embed.go":            "package embedmixed\n\nimport _ \"embed\"\n\n//go:embed assets/missing.txt\nvar missing []byte\n",
		"embed-mixed/valid.go":            "package embedmixed\n",
		"embed-matched/embed.go":          "package embedmatched\n\nimport _ \"embed\"\n\n//go:embed assets/data.txt\nvar data []byte\n",
		"embed-matched/assets/data.txt":   "data\n",
		"embed-no-import/embed.go":        "package embednoimport\n\n//go:embed assets/data.txt\nvar data []byte\n",
		"embed-no-import/assets/data.txt": "data\n",
	})

	inventory, err := BuildPortableGoWorkloadInventory(context.Background(), reader, "tree", string(RepositoryWorkloadPlatformLinuxAMD64), inventoryBuildabilityModules(), []string{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".\x00./embed-matched"}
	if !reflect.DeepEqual(inventory.GoPackages, want) {
		t.Fatalf("GoPackages = %#v, want %#v", inventory.GoPackages, want)
	}
}

func TestPortableGoEmbedPatternMatchesHonorsVisibilityAndModuleBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		pattern  string
		entry    string
		modules  []string
		excluded []string
		want     bool
	}{
		{name: "visible directory descendant", pattern: "assets", entry: "pkg/assets/data.txt", modules: []string{"."}, want: true},
		{name: "ordinary directory omits hidden descendant", pattern: "assets", entry: "pkg/assets/.hidden", modules: []string{"."}, want: false},
		{name: "all directory includes hidden descendant", pattern: "all:assets", entry: "pkg/assets/.hidden", modules: []string{"."}, want: true},
		{name: "explicit hidden file remains visible", pattern: "assets/.hidden", entry: "pkg/assets/.hidden", modules: []string{"."}, want: true},
		{name: "declared nested module cannot satisfy pattern", pattern: "nested", entry: "pkg/nested/data.txt", modules: []string{".", "pkg/nested"}, want: false},
		{name: "excluded nested module cannot satisfy pattern", pattern: "excluded", entry: "pkg/excluded/data.txt", modules: []string{"."}, excluded: []string{"pkg/excluded"}, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			entries := []GitTreeEntry{{Mode: "100644", Kind: "blob", Path: testCase.entry}}
			matched, err := portableGoEmbedPatternMatches("pkg", testCase.pattern, entries, testCase.modules, ".", testCase.excluded)
			if err != nil {
				t.Fatal(err)
			}
			if matched != testCase.want {
				t.Fatalf("matched = %v, want %v", matched, testCase.want)
			}
		})
	}
}

func TestBuildPortableGoWorkloadInventoryHonorsDisabledCGO(t *testing.T) {
	reader := newInventoryBuildabilityReader(map[string]string{
		"cgo-only/cgo.go":  "package cgoonly\n\nimport \"C\"\n",
		"mixed/cgo.go":     "package mixed\n\nimport \"C\"\n",
		"mixed/regular.go": "package mixed\n",
	})

	inventory, err := BuildPortableGoWorkloadInventory(context.Background(), reader, "tree", string(RepositoryWorkloadPlatformLinuxAMD64), inventoryBuildabilityModules(), []string{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".\x00./mixed"}
	if !reflect.DeepEqual(inventory.GoPackages, want) {
		t.Fatalf("GoPackages = %#v, want %#v", inventory.GoPackages, want)
	}
}

func TestBuildPortableGoWorkloadInventoryOmitsV3StyleBuildOnlyDirectories(t *testing.T) {
	reader := newInventoryBuildabilityReader(map[string]string{
		"cmd/agent-terminal/main.go":     "package main\n",
		"cmd/agent-terminal/frontend.go": "package main\n\nimport _ \"embed\"\n\n//go:embed frontend/dist\nvar frontend []byte\n",
		"build-ignore/ignored.go":        "//go:build ignore\n\npackage ignored\n",
		"cmd/mcp-lsp/lsp.go":             "package lsp\n",
	})

	inventory, err := BuildPortableGoWorkloadInventory(context.Background(), reader, "tree", string(RepositoryWorkloadPlatformLinuxAMD64), inventoryBuildabilityModules(), []string{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".\x00./cmd/mcp-lsp"}
	if !reflect.DeepEqual(inventory.GoPackages, want) {
		t.Fatalf("GoPackages = %#v, want %#v", inventory.GoPackages, want)
	}
}

func inventoryBuildabilityModules() []RepositoryWorkloadModule {
	return []RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/inventory"}}
}

func newInventoryBuildabilityReader(files map[string]string) inventoryTreeReader {
	files["go.mod"] = "module example.invalid/inventory\n\ngo 1.26.0\n"
	paths := make([]string, 0, len(files))
	blobs := make(map[string][]byte, len(files))
	for filePath, contents := range files {
		paths = append(paths, filePath)
		blobs[filePath] = []byte(contents)
	}
	sort.Strings(paths)
	entries := make([]GitTreeEntry, 0, len(paths))
	for _, filePath := range paths {
		entries = append(entries, GitTreeEntry{Mode: "100644", Kind: "blob", Path: filePath})
	}
	return inventoryTreeReader{entries: entries, blobs: blobs}
}
