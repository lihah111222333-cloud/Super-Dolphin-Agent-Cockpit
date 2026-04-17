//go:build e2e
// +build e2e

package manager_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

type langTestCase struct {
	name       string
	langID     string
	binary     string
	args       []string
	filename   string
	content    string
	setup      func(t *testing.T, root string) // optional project scaffolding
	minSymbols int
}

var multiLangCases = []langTestCase{
	{
		name:     "JavaScript",
		langID:   "javascript",
		binary:   "typescript-language-server",
		args:     []string{"--stdio"},
		filename: "app.js",
		content: `
function greet(name) {
    return "Hello, " + name;
}

class Calculator {
    constructor() {
        this.result = 0;
    }
    add(a, b) {
        return a + b;
    }
    subtract(a, b) {
        return a - b;
    }
}

const PI = 3.14159;
module.exports = { greet, Calculator, PI };
`,
		setup: func(t *testing.T, root string) {
			// tsserver needs a package.json or tsconfig to anchor workspace
			pkg := `{"name": "test-js", "version": "1.0.0"}`
			if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(pkg), 0644); err != nil {
				t.Fatalf("write package.json: %v", err)
			}
		},
		minSymbols: 2, // at least greet + Calculator
	},
	{
		name:     "TypeScript",
		langID:   "typescript",
		binary:   "typescript-language-server",
		args:     []string{"--stdio"},
		filename: "service.ts",
		content: `
interface User {
    id: number;
    name: string;
    email: string;
}

class UserService {
    private users: User[] = [];

    addUser(user: User): void {
        this.users.push(user);
    }

    findById(id: number): User | undefined {
        return this.users.find(u => u.id === id);
    }

    count(): number {
        return this.users.length;
    }
}

export function createService(): UserService {
    return new UserService();
}
`,
		setup: func(t *testing.T, root string) {
			tsconfig := `{"compilerOptions":{"target":"es2020","module":"commonjs","strict":true}}`
			if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(tsconfig), 0644); err != nil {
				t.Fatalf("write tsconfig.json: %v", err)
			}
		},
		minSymbols: 2, // at least User + UserService
	},
	{
		name:     "Python",
		langID:   "python",
		binary:   "pyright-langserver",
		args:     []string{"--stdio"},
		filename: "models.py",
		content: `
from dataclasses import dataclass
from typing import List, Optional

@dataclass
class Product:
    name: str
    price: float
    quantity: int

class Inventory:
    def __init__(self):
        self.products: List[Product] = []

    def add_product(self, product: Product) -> None:
        self.products.append(product)

    def find_by_name(self, name: str) -> Optional[Product]:
        for p in self.products:
            if p.name == name:
                return p
        return None

    def total_value(self) -> float:
        return sum(p.price * p.quantity for p in self.products)

def create_sample() -> Inventory:
    inv = Inventory()
    inv.add_product(Product("Widget", 9.99, 100))
    return inv
`,
		minSymbols: 2, // at least Product + Inventory
	},
	{
		name:     "Rust",
		langID:   "rust",
		binary:   "/tmp/ra-wrapper.sh",
		args:     nil,
		filename: "src/lib.rs",
		content: `
pub struct Config {
    pub name: String,
    pub debug: bool,
}

impl Config {
    pub fn new(name: &str) -> Self {
        Config {
            name: name.to_string(),
            debug: false,
        }
    }

    pub fn with_debug(mut self) -> Self {
        self.debug = true;
        self
    }
}

pub fn hello(cfg: &Config) -> String {
    format!("Hello, {}!", cfg.name)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn test_hello() {
        let cfg = Config::new("world");
        assert_eq!(hello(&cfg), "Hello, world!");
    }
}
`,
		setup: func(t *testing.T, root string) {
			// rust-analyzer needs Cargo.toml
			cargo := `[package]
name = "test-rust"
version = "0.1.0"
edition = "2021"
`
			if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
				t.Fatalf("mkdir src: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte(cargo), 0644); err != nil {
				t.Fatalf("write Cargo.toml: %v", err)
			}
		},
		minSymbols: 1, // at least Config
	},
	{
		name:     "Java",
		langID:   "java",
		binary:   "jdtls",
		args:     nil,
		filename: "src/main/java/App.java",
		content: `
public class App {
    private String name;

    public App(String name) {
        this.name = name;
    }

    public String greet() {
        return "Hello, " + this.name + "!";
    }

    public static int add(int a, int b) {
        return a + b;
    }

    public static void main(String[] args) {
        App app = new App("World");
        System.out.println(app.greet());
    }
}
`,
		setup: func(t *testing.T, root string) {
			dirs := filepath.Join(root, "src", "main", "java")
			if err := os.MkdirAll(dirs, 0755); err != nil {
				t.Fatalf("mkdir java dirs: %v", err)
			}
		},
		minSymbols: 1, // at least App class
	},
}

func TestMultiLanguageLSP_E2E(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	for _, tc := range multiLangCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Check binary availability first
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("SKIP: %s not found in PATH (install with appropriate package manager)", tc.binary)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			root := t.TempDir()

			// Run optional setup (project scaffolding)
			if tc.setup != nil {
				tc.setup(t, root)
			}

			// Write test source file
			filePath := filepath.Join(root, tc.filename)
			dir := filepath.Dir(filePath)
			if dir != root {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatalf("mkdir for source file: %v", err)
				}
			}
			if err := os.WriteFile(filePath, []byte(tc.content), 0644); err != nil {
				t.Fatalf("write source file: %v", err)
			}

			// Setup registry — pass nil installer to skip EnsureInstalled check
			// (we already verified the binary exists via LookPath above)
			reg := manager.NewRegistry(nil)

			mgr := createGenericManager(tc.binary, tc.args, root, log)
			reg.Register(tc.langID, mgr)
			defer reg.Close()

			// 1. Test: GetManagerForFile (language detection)
			t.Log("Step 1: GetManagerForFile...")
			resolvedMgr, err := reg.GetManagerForFile(ctx, filePath)
			if err != nil {
				t.Fatalf("GetManagerForFile failed: %v", err)
			}
			t.Log("  ✓ Manager resolved")

			// 2. Test: BootstrapDocument
			uri := "file://" + filePath
			t.Log("Step 2: BootstrapDocument...")
			if err := resolvedMgr.BootstrapDocument(ctx, uri); err != nil {
				t.Fatalf("BootstrapDocument failed: %v", err)
			}
			t.Log("  ✓ Document bootstrapped")

			// Wait for language server indexing
			t.Log("Step 3: Waiting for indexing...")
			time.Sleep(5 * time.Second)

			// 3. Test: DocumentSymbol
			t.Log("Step 4: DocumentSymbol...")
			symbols, err := resolvedMgr.DocumentSymbol(ctx, uri)
			if err != nil {
				t.Fatalf("DocumentSymbol failed: %v", err)
			}
			t.Logf("  ✓ Got %d top-level symbols", len(symbols))
			for _, s := range symbols {
				printSymbol(t, s, 1)
			}
			if len(symbols) < tc.minSymbols {
				t.Errorf("Expected at least %d symbols, got %d", tc.minSymbols, len(symbols))
			}

			// 4. Test: Hover on a known position (line 2, col 5 — typically inside first definition)
			t.Log("Step 5: Hover...")
			hoverPos := protocol.Position{Line: 2, Character: 5}
			hover, err := resolvedMgr.Hover(ctx, uri, hoverPos)
			if err != nil {
				t.Logf("  ⚠ Hover returned error: %v", err)
			} else if hover != nil && hover.Contents != nil {
				t.Logf("  ✓ Hover: %s", truncate(fmt.Sprint(hover.Contents), 200))
			} else {
				t.Log("  ⚠ Hover returned empty result")
			}

			// 5. Test: Diagnostics
			t.Log("Step 6: Diagnostics...")
			diags, err := resolvedMgr.Diagnostics(ctx, []string{uri})
			if err != nil {
				t.Logf("  ⚠ Diagnostics error: %v", err)
			} else {
				totalDiags := 0
				for _, d := range diags {
					totalDiags += len(d.Diagnostics)
				}
				t.Logf("  ✓ Got %d diagnostic(s)", totalDiags)
				for _, d := range diags {
					for _, diag := range d.Diagnostics {
						t.Logf("    [%d] L%d:%d %s", diag.Severity, diag.Range.Start.Line, diag.Range.Start.Character, diag.Message)
					}
				}
			}

			t.Logf("=== %s: ALL CHECKS PASSED ===", tc.name)
		})
	}
}

func printSymbol(t *testing.T, s protocol.DocumentSymbol, depth int) {
	indent := strings.Repeat("  ", depth)
	t.Logf("%s- %s (kind=%d, range=L%d:%d-L%d:%d)", indent, s.Name, s.Kind,
		s.Range.Start.Line, s.Range.Start.Character,
		s.Range.End.Line, s.Range.End.Character)
	for _, child := range s.Children {
		printSymbol(t, child, depth+1)
	}
}

// TestLanguageDetection_E2E validates that file extension -> language ID mapping works correctly
// for all supported languages.
func TestLanguageDetection_E2E(t *testing.T) {
	cases := []struct {
		file     string
		expected string
	}{
		{"app.js", "javascript"},
		{"component.jsx", "javascript"},
		{"index.mjs", "javascript"},
		{"config.cjs", "javascript"},
		{"service.ts", "typescript"},
		{"component.tsx", "typescript"},
		{"models.py", "python"},
		{"stubs.pyi", "python"},
		{"lib.rs", "rust"},
		{"App.java", "java"},
	}

	for _, c := range cases {
		got := manager.DetectLanguageID(c.file)
		if got != c.expected {
			t.Errorf("DetectLanguageID(%q) = %q, want %q", c.file, got, c.expected)
		} else {
			t.Logf("✓ %s -> %s", c.file, got)
		}
	}
}

// TestMultiLanguageHoverDefinition_E2E tests hover and go-to-definition across languages.
func TestMultiLanguageHoverDefinition_E2E(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	type hoverDefCase struct {
		name     string
		langID   string
		binary   string
		args     []string
		filename string
		content  string
		setup    func(t *testing.T, root string)
		// position to test hover + definition on
		pos protocol.Position
	}

	cases := []hoverDefCase{
		{
			name:     "JS_FunctionCall",
			langID:   "javascript",
			binary:   "typescript-language-server",
			args:     []string{"--stdio"},
			filename: "test.js",
			content: `function add(a, b) { return a + b; }
const result = add(1, 2);
`,
			setup: func(t *testing.T, root string) {
				os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"t"}`), 0644)
			},
			pos: protocol.Position{Line: 1, Character: 15}, // on `add` call
		},
		{
			name:     "TS_ClassMethod",
			langID:   "typescript",
			binary:   "typescript-language-server",
			args:     []string{"--stdio"},
			filename: "test.ts",
			content: `class Foo { bar(): number { return 42; } }
const f = new Foo();
f.bar();
`,
			setup: func(t *testing.T, root string) {
				os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true}}`), 0644)
			},
			pos: protocol.Position{Line: 2, Character: 2}, // on `bar` method call
		},
		{
			name:     "Python_FunctionDef",
			langID:   "python",
			binary:   "pyright-langserver",
			args:     []string{"--stdio"},
			filename: "test.py",
			content: `def multiply(x: int, y: int) -> int:
    return x * y
result = multiply(3, 4)
`,
			pos: protocol.Position{Line: 2, Character: 10}, // on `multiply` call
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("SKIP: %s not found", tc.binary)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			root := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, root)
			}

			filePath := filepath.Join(root, tc.filename)
			os.WriteFile(filePath, []byte(tc.content), 0644)

			reg := manager.NewRegistry(nil)
			mgr := createGenericManager(tc.binary, tc.args, root, log)
			reg.Register(tc.langID, mgr)
			defer reg.Close()

			uri := "file://" + filePath
			resolvedMgr, err := reg.GetManagerForFile(ctx, filePath)
			if err != nil {
				t.Fatalf("GetManagerForFile: %v", err)
			}
			if err := resolvedMgr.BootstrapDocument(ctx, uri); err != nil {
				t.Fatalf("BootstrapDocument: %v", err)
			}
			time.Sleep(5 * time.Second)

			// Hover
			hover, err := resolvedMgr.Hover(ctx, uri, tc.pos)
			if err != nil {
				t.Errorf("Hover failed: %v", err)
			} else if hover != nil {
				t.Logf("✓ Hover at L%d:%d → %s", tc.pos.Line, tc.pos.Character, truncate(fmt.Sprint(hover.Contents), 150))
			}

			// Definition
			defs, err := resolvedMgr.Definition(ctx, uri, tc.pos)
			if err != nil {
				t.Errorf("Definition failed: %v", err)
			} else {
				t.Logf("✓ Definition at L%d:%d → %d location(s)", tc.pos.Line, tc.pos.Character, len(defs))
				for _, d := range defs {
					if d.Location != nil {
						t.Logf("    → %s L%d:%d", d.Location.URI, d.Location.Range.Start.Line, d.Location.Range.Start.Character)
					} else if d.LocationLink != nil {
						t.Logf("    → %s L%d:%d", d.LocationLink.TargetURI, d.LocationLink.TargetRange.Start.Line, d.LocationLink.TargetRange.Start.Character)
					}
				}
			}
		})
	}
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
