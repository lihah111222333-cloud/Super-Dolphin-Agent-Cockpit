package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

// remoteGateCLICompileClosureProtocolASTSHA256 冻结 accepted materializer 可跨代解释的闭包算法。
// 不得为接纳新功能而更新此值；host-only 编译输入必须放在独立文件和调用链中。
const remoteGateCLICompileClosureProtocolASTSHA256 = "f528dcce5c92689304f101044eef7d18896f2bb6ad6beb3c598c148a1d798691"

func TestRemoteGateCLICompileClosureProtocolRemainsCrossGenerationStable(t *testing.T) {
	source, err := os.ReadFile("gate_cli_compile_closure.go")
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "gate_cli_compile_closure.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var canonical bytes.Buffer
	if err := format.Node(&canonical, fileSet, parsed); err != nil {
		t.Fatal(err)
	}
	got := sha256.Sum256(canonical.Bytes())
	if gotString := hex.EncodeToString(got[:]); gotString != remoteGateCLICompileClosureProtocolASTSHA256 {
		t.Fatalf("remote Gate CLI compile-closure protocol changed: got %s, want %s; host-only inputs must not change the accepted materializer protocol", gotString, remoteGateCLICompileClosureProtocolASTSHA256)
	}
}
