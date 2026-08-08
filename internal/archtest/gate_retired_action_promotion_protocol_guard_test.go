package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestGateRetiredActionPromotionProtocolStaysAbsent locks the deletion boundary
// for the former signed action and accepted-image promotion protocol. The live
// gate package still owns SourceSpec, ResultStatus, and shared digest checks;
// this guard only rejects the retired protocol's declarations and wire markers.
func TestGateRetiredActionPromotionProtocolStaysAbsent(t *testing.T) {
	root := findRepoRoot(t)
	gateDir := filepath.Join(root, "internal", "devtools", "gate")
	assertRetiredGateProtocolFilesAbsent(t, gateDir)
	scanRetiredGateProductionRoots(t, root, []string{
		"internal/devtools/gate",
		"internal/devtools/gatehook",
		"internal/devtools/remoteci",
		"cmd/super-dolphin-gate",
	})
}

func assertRetiredGateProtocolFilesAbsent(t *testing.T, gateDir string) {
	t.Helper()
	for _, retired := range []string{"action_grant_validation.go", "contracts_validation_release.go"} {
		path := filepath.Join(gateDir, retired)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("retired gate protocol file %s was reintroduced", retired)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat retired gate protocol file %s: %v", retired, err)
		}
	}
}

func scanRetiredGateProductionRoots(t *testing.T, root string, relativeRoots []string) {
	t.Helper()
	for _, relativeRoot := range relativeRoots {
		productionRoot := filepath.Join(root, filepath.FromSlash(relativeRoot))
		if err := scanRetiredGateProductionRoot(t, relativeRoot, productionRoot); err != nil {
			t.Fatalf("walk production root %s: %v", relativeRoot, err)
		}
	}
}

func scanRetiredGateProductionRoot(t *testing.T, relativeRoot, productionRoot string) error {
	t.Helper()
	return filepath.WalkDir(productionRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return scanRetiredGateProductionFile(t, relativeRoot, productionRoot, path, entry)
	})
}

func scanRetiredGateProductionFile(t *testing.T, relativeRoot, productionRoot, path string, entry os.DirEntry) error {
	t.Helper()
	if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
		return nil
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	relativePath, err := filepath.Rel(productionRoot, path)
	if err != nil {
		return err
	}
	displayPath := filepath.ToSlash(filepath.Join(relativeRoot, relativePath))
	for _, violation := range retiredGateProtocolViolations(string(source), path) {
		t.Errorf("%s reintroduces retired action/promotion protocol %s", displayPath, violation)
	}
	return nil
}

func TestRetiredGateProtocolGuardRejectsDeclarationAndWireMarkerFixtures(t *testing.T) {
	fixtures := []struct {
		name   string
		source string
		want   string
	}{
		{name: "type", source: "package fixture\ntype ActionGrant struct{}\n", want: "declaration ActionGrant"},
		{name: "writer", source: "package fixture\nfunc PromoteAcceptedImage() {}\n", want: "declaration PromoteAcceptedImage"},
		{name: "wire marker", source: "package fixture\nconst audience = \"image.promote\"\n", want: "wire marker image.promote"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			violations := retiredGateProtocolViolations(fixture.source, fixture.name)
			if !slices.Contains(violations, fixture.want) {
				t.Fatalf("retired protocol fixture violations = %v, want %q", violations, fixture.want)
			}
		})
	}
}

func retiredGateProtocolViolations(source, filename string) []string {
	file, err := parser.ParseFile(token.NewFileSet(), filename, source, parser.ParseComments)
	if err != nil {
		return []string{"parse error: " + err.Error()}
	}
	violations := retiredGateDeclarationViolations(file)
	violations = append(violations, retiredGateWireMarkerViolations(source)...)
	sort.Strings(violations)
	return violations
}

func retiredGateProtocolDeclarations() map[string]struct{} {
	return map[string]struct{}{
		"SignatureAlgorithm": {}, "SignatureAlgorithmEd25519": {}, "SignerIdentity": {},
		"ImageIdentity": {}, "TrustedRunnerIdentity": {}, "TrustedAdapterIdentity": {},
		"ActionAudience": {}, "ActionAudienceGitPush": {}, "ActionAudienceRelease": {},
		"ActionAudienceImagePromote": {}, "GrantRequest": {}, "ReleaseAsset": {},
		"ActionGrantState": {}, "ActionGrantStateIssued": {}, "ActionGrantStateConsumed": {},
		"ActionGrantStateExpired": {}, "ActionGrantStateRevoked": {}, "ActionGrant": {},
		"AcceptedImageRecordSchemaVersion": {}, "PromotionRecordSchemaVersion": {},
		"AcceptedImageRecord": {}, "PromotionRecord": {}, "ActionGrantSigningPayload": {},
		"VerifyActionGrant": {}, "ActionGrantDigest": {}, "AcceptedImageSigningPayload": {},
		"AcceptedImageRecordDigest": {}, "ValidateActionAttemptID": {}, "validateActionGrantIdentity": {},
		"validateActionGrantTimeline": {}, "validateIssuedGrant": {}, "validateConsumedGrant": {},
		"validateExpiredGrant": {}, "validateRevokedGrant": {}, "validateReleaseTarget": {},
		"validateReleaseAssets": {}, "validateActionOID": {}, "validateNonZeroActionOID": {},
		"PromoteAcceptedImage": {}, "WriteAcceptedImage": {}, "CreateImageCache": {},
		"PromoteRemoteBaselineState": {}, "PromoteRemoteBaselineStateWithRefreshLease": {},
	}
}

func retiredGateDeclarationViolations(file *ast.File) []string {
	var violations []string
	for _, declaration := range file.Decls {
		if violation := retiredGateDeclarationViolation(declaration); violation != "" {
			violations = append(violations, violation)
		}
	}
	return violations
}

func retiredGateDeclarationViolation(declaration ast.Decl) string {
	if function, ok := declaration.(*ast.FuncDecl); ok {
		return retiredGateDeclarationNameViolation(function.Name.Name)
	}
	general, ok := declaration.(*ast.GenDecl)
	if !ok {
		return ""
	}
	for _, spec := range general.Specs {
		if violation := retiredGateSpecViolation(spec); violation != "" {
			return violation
		}
	}
	return ""
}

func retiredGateSpecViolation(spec ast.Spec) string {
	value, ok := spec.(*ast.ValueSpec)
	if typeSpec, isType := spec.(*ast.TypeSpec); isType {
		return retiredGateDeclarationNameViolation(typeSpec.Name.Name)
	}
	if !ok || len(value.Names) != 1 {
		return ""
	}
	return retiredGateDeclarationNameViolation(value.Names[0].Name)
}

func retiredGateDeclarationNameViolation(name string) string {
	if _, retired := retiredGateProtocolDeclarations()[name]; retired {
		return "declaration " + name
	}
	return ""
}

func retiredGateWireMarkerViolations(source string) []string {
	var violations []string
	for _, marker := range []string{"image.promote", "action_grant", "accepted_image", "promotion_record"} {
		if strings.Contains(source, marker) {
			violations = append(violations, "wire marker "+marker)
		}
	}
	return violations
}
