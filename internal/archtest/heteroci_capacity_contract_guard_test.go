package archtest

import (
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// TestHeterogeneousCIManifestUsesDynamicNodesAndLocalAuthority exercises the
// public validator while statically rejecting a fixed-size node array or host
// alias/count owner in the manifest production source.
func TestHeterogeneousCIManifestUsesDynamicNodesAndLocalAuthority(t *testing.T) {
	root := findRepoRoot(t)
	manifestPath := filepath.Join(root, "internal", "devtools", "heteroci", "manifest.go")
	manifestFile := parseRemoteCIContractGuardFile(t, manifestPath)
	assertHeterociManifestFieldShape(t, manifestFile)
	assertHeterociDynamicNodeCounts(t)
	assertHeterociInvalidRemoteAuthority(t)
}

// assertHeterociManifestFieldShape keeps the manifest AST checks independent
// from public validation cases so each failure retains its original message.
func assertHeterociManifestFieldShape(t *testing.T, manifestFile *ast.File) {
	t.Helper()
	manifestType := remoteCIStructTypeByName(manifestFile, "Manifest")
	if manifestType == nil {
		t.Fatal("heteroci manifest must declare Manifest")
	}
	nodeConfigType := remoteCIStructTypeByName(manifestFile, "NodeConfig")
	assertHeterociStringField(t, nodeConfigType, "NodeConfig", "ProbeIdentity")
	assertHeterociProbeIdentityJSONTag(t, nodeConfigType)
	assertHeterociDynamicNodeSlice(t, manifestType)
	if violations := heterociFixedNodeMetadataViolations(manifestFile); len(violations) != 0 {
		t.Fatalf("heteroci manifest declares fixed host count/alias metadata: %v", violations)
	}
}

func assertHeterociProbeIdentityJSONTag(t *testing.T, nodeConfigType *ast.StructType) {
	t.Helper()
	probeIdentityField := heterociManifestField(nodeConfigType, "ProbeIdentity")
	if probeIdentityField.Tag == nil || !strings.Contains(probeIdentityField.Tag.Value, `json:"probe_identity"`) {
		t.Fatal("heteroci NodeConfig.ProbeIdentity must bind json probe_identity")
	}
}

func assertHeterociDynamicNodeSlice(t *testing.T, manifestType *ast.StructType) {
	t.Helper()
	nodesField := heterociManifestField(manifestType, "Nodes")
	if nodesField == nil {
		t.Fatal("heteroci Manifest must declare Nodes")
	}
	nodes, ok := nodesField.Type.(*ast.ArrayType)
	if !ok || nodes.Len != nil {
		t.Fatal("heteroci Manifest.Nodes must be a dynamically sized slice")
	}
	if _, ok := nodes.Elt.(*ast.Ident); !ok {
		t.Fatalf("heteroci Manifest.Nodes element type = %T, want named NodeConfig", nodes.Elt)
	}
}

func assertHeterociDynamicNodeCounts(t *testing.T) {
	t.Helper()
	for _, nodeCount := range []int{1, 2, 3} {
		manifest := newHeterogeneousCIManifest(nodeCount)
		if err := manifest.Validate(); err != nil {
			t.Fatalf("Validate(%d dynamic nodes): %v", nodeCount, err)
		}
		if got := len(manifest.Nodes); got != nodeCount {
			t.Fatalf("dynamic node count = %d, want %d", got, nodeCount)
		}
	}
}

func assertHeterociInvalidRemoteAuthority(t *testing.T) {
	t.Helper()
	invalidAuthority := newHeterogeneousCIManifest(1)
	invalidAuthority.Authority = "REMOTE_AUTHORITATIVE"
	assertHeterogeneousCIManifestRejects(t, invalidAuthority, "remote authority")
}

// TestHeterogeneousCIExecutionPoolRequiresExactTopology 锁定单 Ubuntu 双运行时生产池，同时保留 generic manifest 校验。
func TestHeterogeneousCIExecutionPoolRequiresExactTopology(t *testing.T) {
	valid := newHeterogeneousCIManifest(1)
	valid.Nodes[0].NodeID = "caller-node"
	valid.Nodes[0].Endpoint = "ssh://caller-host"
	valid.Nodes[0].Capabilities = append(valid.Nodes[0].Capabilities, heteroci.CapabilityConfig{
		Runtime: heteroci.RuntimeUbuntuWineContainer, Image: "ubuntu-wine:9.0", ImageDigest: "sha256:" + strings.Repeat("3", 64),
		SourceRevision: "https://github.com/wine-mirror/wine@refs/tags/wine-9.0", QualificationSuite: "wine-windows-amd64/v1",
	})
	if err := valid.ValidateSingleUbuntuWineExecutionPool(); err != nil {
		t.Fatalf("ValidateSingleUbuntuWineExecutionPool(valid): %v", err)
	}
	for _, nodeCount := range []int{0, 2, 4} {
		candidate := newHeterogeneousCIManifest(nodeCount)
		if err := candidate.ValidateSingleUbuntuWineExecutionPool(); err == nil {
			t.Fatalf("ValidateSingleUbuntuWineExecutionPool(%d nodes) unexpectedly succeeded", nodeCount)
		}
	}
	physicalWindows := cloneHeterogeneousCIManifest(valid)
	physicalWindows.Nodes[0].Platform = heteroci.PlatformWindowsAMD64
	if err := physicalWindows.ValidateSingleUbuntuWineExecutionPool(); err == nil {
		t.Fatal("ValidateSingleUbuntuWineExecutionPool accepted a physical Windows node")
	}
}

// TestHeterogeneousCISnapshotCarriesProbeIdentity keeps live receipt identity
// typed at the snapshot boundary instead of collapsing it to RuntimeKind values.
func TestHeterogeneousCISnapshotCarriesProbeIdentity(t *testing.T) {
	root := findRepoRoot(t)
	snapshotFile := parseRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "heteroci", "snapshot.go"))
	snapshotType := remoteCIStructTypeByName(snapshotFile, "NodeSnapshot")
	if snapshotType == nil {
		t.Fatal("heteroci snapshot must declare NodeSnapshot")
	}
	assertHeterociStringField(t, snapshotType, "NodeSnapshot", "ProbeIdentity")
	assertHeterociStringField(t, snapshotType, "NodeSnapshot", "ProbeBootID")
	activeCapabilities := heterociManifestField(snapshotType, "ActiveCapabilities")
	if activeCapabilities == nil {
		t.Fatal("heteroci NodeSnapshot must declare ActiveCapabilities")
	}
	activeType, ok := activeCapabilities.Type.(*ast.ArrayType)
	if !ok {
		t.Fatalf("heteroci NodeSnapshot.ActiveCapabilities type = %T, want slice", activeCapabilities.Type)
	}
	if runtimeKind, ok := activeType.Elt.(*ast.Ident); ok && runtimeKind.Name == "RuntimeKind" {
		t.Fatal("heteroci NodeSnapshot.ActiveCapabilities must not remain []RuntimeKind")
	}
}

func heterociManifestField(structure *ast.StructType, name string) *ast.Field {
	if structure == nil {
		return nil
	}
	for _, field := range structure.Fields.List {
		for _, identifier := range field.Names {
			if identifier.Name == name {
				return field
			}
		}
	}
	return nil
}

func heterociFixedNodeMetadataViolations(file *ast.File) []string {
	forbidden := map[string]bool{
		"HostCount": true, "NodeCount": true, "DefaultHostCount": true,
		"DefaultNodeCount": true, "HostAliases": true, "NodeAliases": true,
		"DefaultHostAliases": true, "DefaultNodeAliases": true,
	}
	violations := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && forbidden[identifier.Name] {
			violations[identifier.Name] = true
		}
		return true
	})
	return remoteCIViolationList(violations)
}

func newHeterogeneousCIManifest(nodeCount int) heteroci.Manifest {
	manifest := heteroci.Manifest{
		SchemaVersion: heteroci.ManifestSchemaVersion,
		Authority:     heteroci.AuthorityLocalNonAuthoritative,
		Nodes:         make([]heteroci.NodeConfig, nodeCount),
	}
	for index := range manifest.Nodes {
		nodeID := fmt.Sprintf("test-node-%d", index+1)
		manifest.Nodes[index] = heteroci.NodeConfig{
			NodeID: nodeID, Endpoint: "https://" + nodeID + ".invalid/runner", ProbeIdentity: fmt.Sprintf("sha256:%064x", index+1), Platform: heteroci.PlatformLinuxAMD64,
			Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeUbuntuContainer, Image: "ubuntu:22.04", ImageDigest: "sha256:" + strings.Repeat("2", 64)}},
		}
	}
	return manifest
}

func assertHeterociStringField(t *testing.T, structure *ast.StructType, owner, fieldName string) {
	t.Helper()
	field := heterociManifestField(structure, fieldName)
	if field == nil {
		t.Fatalf("heteroci %s must declare %s", owner, fieldName)
	}
	fieldType, ok := field.Type.(*ast.Ident)
	if !ok || fieldType.Name != "string" {
		t.Fatalf("heteroci %s.%s type = %T, want string", owner, fieldName, field.Type)
	}
}

func assertHeterogeneousCIManifestRejects(t *testing.T, manifest heteroci.Manifest, context string) {
	t.Helper()
	if err := manifest.Validate(); err == nil {
		t.Fatalf("%s: Manifest.Validate unexpectedly succeeded", context)
	}
}

func cloneHeterogeneousCIManifest(manifest heteroci.Manifest) heteroci.Manifest {
	clone := manifest
	clone.Nodes = slices.Clone(manifest.Nodes)
	for index := range clone.Nodes {
		clone.Nodes[index].Capabilities = slices.Clone(manifest.Nodes[index].Capabilities)
	}
	return clone
}

// TestHeterogeneousCIProductionCannotReachRemoteAuthority keeps local
// temporary-artifact semantics below remote SQLite/PASS/release/ImageCache
// owners. The guard is scoped to this package's production Go files, so it
// cannot be bypassed by unrelated repository strings or host names.
func TestHeterogeneousCIProductionCannotReachRemoteAuthority(t *testing.T) {
	root := findRepoRoot(t)
	paths := heterociProductionGoFiles(t, root)
	if len(paths) == 0 {
		t.Fatal("heteroci production package has no Go source")
	}
	for _, path := range paths {
		file := parseRemoteCIContractGuardFile(t, path)
		for _, violation := range heterociRemoteAuthorityViolations(file) {
			t.Errorf("%s reaches a remote authority owner: %s", relativeRemoteCIContractPath(t, root, path), violation)
		}
	}
}

func heterociProductionGoFiles(t *testing.T, root string) []string {
	t.Helper()
	directory := filepath.Join(root, "internal", "devtools", "heteroci")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read heteroci production directory: %v", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		paths = append(paths, filepath.Join(directory, entry.Name()))
	}
	slices.Sort(paths)
	return paths
}

func heterociRemoteAuthorityViolations(file *ast.File) []string {
	const modulePrefix = "github.com/lihah111222333-cloud/super-dolphin-agent/"
	forbiddenImportPrefixes := []string{
		modulePrefix + "internal/devtools/gate",
		modulePrefix + "internal/platform/db",
		modulePrefix + "internal/store",
		modulePrefix + "cmd/super-dolphin-gate",
		modulePrefix + "cmd/super-dolphin-release-manifest",
	}
	violations := make(map[string]bool)
	forbiddenAliases := heterociForbiddenRemoteImportAliases(file, forbiddenImportPrefixes, violations)
	forbiddenCalls := map[string]bool{
		"NewDurationLedgerStore": true, "OpenSQLiteAuthority": true,
		"LookupRemoteWorkloadPassEvidence": true, "RecordRemoteWorkloadPassBatch": true,
		"FinalizeRemoteCIRunAuthorityWithSamples": true, "CreateImageCache": true,
		"DescribeImageCache": true, "PromoteImageCache": true,
	}
	ast.Inspect(file, func(node ast.Node) bool {
		heterociRecordRemoteAuthorityCall(node, forbiddenAliases, forbiddenCalls, violations)
		return true
	})
	return remoteCIViolationList(violations)
}

func heterociForbiddenRemoteImportAliases(file *ast.File, prefixes []string, violations map[string]bool) map[string]string {
	aliases := make(map[string]string)
	for _, importSpec := range file.Imports {
		importPath := strings.Trim(importSpec.Path.Value, "\"")
		for _, prefix := range prefixes {
			if !heterociMatchesRemoteImportPrefix(importPath, prefix) {
				continue
			}
			alias := heterociImportAlias(importSpec, importPath)
			violations["import "+importPath] = true
			if alias != "_" && alias != "." {
				aliases[alias] = importPath
			}
			break
		}
	}
	return aliases
}

func heterociMatchesRemoteImportPrefix(importPath, prefix string) bool {
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

func heterociImportAlias(importSpec *ast.ImportSpec, importPath string) string {
	if importSpec.Name != nil {
		return importSpec.Name.Name
	}
	return filepath.Base(importPath)
}

func heterociRecordRemoteAuthorityCall(node ast.Node, aliases map[string]string, forbiddenCalls, violations map[string]bool) {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return
	}
	switch function := call.Fun.(type) {
	case *ast.SelectorExpr:
		heterociRecordRemoteSelectorCall(function, aliases, violations)
		heterociRecordForbiddenCall(function.Sel.Name, forbiddenCalls, violations)
	case *ast.Ident:
		heterociRecordForbiddenCall(function.Name, forbiddenCalls, violations)
	}
}

func heterociRecordRemoteSelectorCall(function *ast.SelectorExpr, aliases map[string]string, violations map[string]bool) {
	packageName, ok := function.X.(*ast.Ident)
	if !ok {
		return
	}
	if importPath, forbidden := aliases[packageName.Name]; forbidden {
		violations["call "+packageName.Name+"."+function.Sel.Name+" ("+importPath+")"] = true
	}
}

func heterociRecordForbiddenCall(name string, forbiddenCalls, violations map[string]bool) {
	if forbiddenCalls[name] {
		violations["call "+name] = true
	}
}
