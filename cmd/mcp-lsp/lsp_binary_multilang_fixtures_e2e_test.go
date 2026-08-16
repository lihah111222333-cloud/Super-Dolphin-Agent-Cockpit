//go:build e2e

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBinaryColdStartCSSFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-css"}`)
	return writeBinaryColdStartFile(t, root, "style.css", "body { color: black; }\n")
}

func writeBinaryColdStartHTMLFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-html"}`)
	return writeBinaryColdStartFile(t, root, "index.html", "<main>Hello</main>\n")
}

func writeBinaryColdStartJSONFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-json"}`+"\n")
}

func writeBinaryColdStartYAMLFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "config.yaml", "name: binary-cold-yaml\n")
}

func writeBinaryColdStartMarkdownFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "README.md", "# Binary Cold Markdown\n")
}

func writeBinaryColdStartVueFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-vue"}`)
	return writeBinaryColdStartFile(t, root, "App.vue", "<template><main>Hello</main></template>\n")
}

func writeBinaryColdStartSvelteFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-svelte"}`)
	return writeBinaryColdStartFile(t, root, "App.svelte", "<main>Hello</main>\n")
}

func writeBinaryColdStartCFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeBinaryColdStartFile(t, root, "main.c", "int main(void) { return 0; }\n")
}

func writeBinaryColdStartCPPFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeBinaryColdStartFile(t, root, "main.cpp", "int main() { return 0; }\n")
}

func writeBinaryColdStartObjectiveCFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeBinaryColdStartFile(t, root, "main.m", "int main(void) { return 0; }\n")
}

func writeBinaryColdStartObjectiveCPPFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeBinaryColdStartFile(t, root, "main.mm", "int main() { return 0; }\n")
}

func writeBinaryColdStartSwiftFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "Package.swift", "// swift-tools-version: 6.0\n")
	return writeBinaryColdStartFile(t, root, "Sources/App/main.swift", "print(\"hello\")\n")
}

func writeBinaryColdStartCSharpFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "global.json", `{"sdk":{"rollForward":"latestFeature"}}`)
	writeBinaryColdStartFile(t, root, "App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup></Project>`)
	return writeBinaryColdStartFile(t, root, "Program.cs", "class Program { static void Main() {} }\n")
}

func writeBinaryColdStartPHPFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "composer.json", `{"name":"example/binary-cold-php"}`)
	return writeBinaryColdStartFile(t, root, "index.php", "<?php echo 'hello';\n")
}

func writeBinaryColdStartRubyFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "Gemfile", "source 'https://rubygems.org'\n")
	return writeBinaryColdStartFile(t, root, "app.rb", "puts 'hello'\n")
}

func writeBinaryColdStartKotlinFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "settings.gradle.kts", "pluginManagement {}\n")
	return writeBinaryColdStartFile(t, root, "src/Main.kt", "fun main() {}\n")
}

func writeBinaryColdStartDartFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "pubspec.yaml", "name: binary_cold_dart\n")
	return writeBinaryColdStartFile(t, root, "lib/main.dart", "void main() {}\n")
}

func writeBinaryColdStartLuaFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, ".luarc.json", "{}\n")
	return writeBinaryColdStartFile(t, root, "init.lua", "local value = 1\n")
}

func writeBinaryColdStartDockerFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "Dockerfile", "FROM scratch\n")
}

func writeBinaryColdStartTerraformFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "main.tf", "terraform {}\n")
}

func writeBinaryColdStartGraphQLFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-graphql"}`)
	return writeBinaryColdStartFile(t, root, "schema.graphql", "type Query { hello: String }\n")
}

func writeBinaryColdStartPrismaFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-prisma"}`)
	return writeBinaryColdStartFile(t, root, "schema.prisma", "datasource db { provider = \"sqlite\" url = \"file:dev.db\" }\n")
}

func writeBinaryColdStartProtoFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "proto/example.proto", "syntax = \"proto3\";\nmessage Example {}\n")
}

func writeBinaryColdStartGoFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "go.mod", "module example.test/binarycoldgo\n\ngo 1.25.0\n")
	return writeBinaryColdStartFile(t, root, "main.go", "package main\n")
}

func writeBinaryColdStartGoModFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "go.mod", "module example.test/binarycoldgomod\n\ngo 1.25.0\n")
}

func writeBinaryColdStartGoSumFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "go.mod", "module example.test/binarycoldgosum\n\ngo 1.25.0\n")
	return writeBinaryColdStartFile(t, root, "go.sum", "example.com/dependency v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n")
}

func writeBinaryColdStartGoWorkFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "module/go.mod", "module example.test/binarycoldgowork\n\ngo 1.25.0\n")
	return writeBinaryColdStartFile(t, root, "go.work", "go 1.25.0\n\nuse ./module\n")
}

func writeBinaryColdStartJavaFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "pom.xml", "<project></project>\n")
	return writeBinaryColdStartFile(t, root, "src/Main.java", "class Main {}\n")
}

func writeBinaryColdStartJavaScriptFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-js"}`)
	return writeBinaryColdStartFile(t, root, "app.js", "export const value = 1\n")
}

func writeBinaryColdStartJavaScriptReactFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-jsx"}`)
	return writeBinaryColdStartFile(t, root, "app.jsx", "export const View = () => null\n")
}

func writeBinaryColdStartPythonFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "pyproject.toml", "[project]\nname = \"binary-cold-python\"\n")
	return writeBinaryColdStartFile(t, root, "app.py", "value = 1\n")
}

func writeBinaryColdStartRustFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "Cargo.toml", "[package]\nname = \"binary_cold_rust\"\nversion = \"0.1.0\"\n")
	return writeBinaryColdStartFile(t, root, "src/main.rs", "fn main() {}\n")
}

func writeBinaryColdStartShellFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "Makefile", "all:\n\t@true\n")
	return writeBinaryColdStartFile(t, root, "scripts/run.sh", "#!/usr/bin/env bash\n")
}

func writeBinaryColdStartSQLFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "sqlc.yaml", "version: '2'\n")
	return writeBinaryColdStartFile(t, root, "schema.sql", "select 1;\n")
}

func writeBinaryColdStartTypeScriptFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "tsconfig.json", `{"compilerOptions":{}}`)
	return writeBinaryColdStartFile(t, root, "app.ts", "export const value: number = 1\n")
}

func writeBinaryColdStartTypeScriptReactFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "tsconfig.json", `{"compilerOptions":{"jsx":"react-jsx"}}`)
	return writeBinaryColdStartFile(t, root, "app.tsx", "export const View = () => null\n")
}

func writeBinaryColdStartFile(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}
