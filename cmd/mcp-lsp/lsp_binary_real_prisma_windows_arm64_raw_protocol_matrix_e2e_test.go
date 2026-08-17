//go:build windows && arm64 && e2e

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"
)

const realPrismaRawProtocolMatrixEnv = "MCP_LSP_REAL_PRISMA_RAW_PROTOCOL_MATRIX_WINDOWS_E2E"

// TestRealPrismaWindowsRawProtocolMatrix 逐个坐标复核 Prisma 原始 LSP 的
// hover/definition/references。它是 TARGETED_DIAGNOSTIC/NON_LIFECYCLE，
// 不把短时 raw server 运行冒充为生产 MCP 或十五分钟生命周期证明。
func TestRealPrismaWindowsRawProtocolMatrix(t *testing.T) {
	if os.Getenv(realPrismaRawProtocolMatrixEnv) != "1" {
		t.Skipf("set %s=1 to run the targeted raw Prisma protocol matrix", realPrismaRawProtocolMatrixEnv)
	}
	if runtime.GOOS != "windows" {
		t.Fatalf("raw Prisma protocol matrix requires Windows, got %s", runtime.GOOS)
	}
	if err := validateRealPrismaRawProject(newRealPrismaRawProject()); err != nil {
		t.Fatalf("Prisma raw project drift guard: %v", err)
	}

	started := time.Now()
	root := realNodeRepoRoot(t)
	realNodeProvisionWindowsVCLibsDesktopAppLocal(t)
	nodeDist, npmBin := realNodeBundle(t, root)
	pins := realNodeScriptPins(t, root)
	installDir := t.TempDir()
	registerRealMCPTempRootCleanup(t, installDir)
	realNodeInstall(t, npmBin, nodeDist, installDir, pins)
	realNodeVerifyInstall(t, installDir)
	realNodeVerifyNativeAstGrepRuntime(t, installDir)

	servers := realNodeServerCasesForLanguage("prisma")
	if len(servers) != 1 {
		t.Fatalf("raw Prisma server cases=%d, want exactly one locked server", len(servers))
	}
	observations := make([]realPrismaRawObservation, 0, len(realPrismaRawSequences)*len(realPrismaRawTokens)*len(realPrismaRawMethods))
	capabilitySeen := make(map[string]bool)
	for _, sequence := range realPrismaRawSequences {
		result := runRealPrismaRawSequence(t, nodeDist, installDir, servers[0], sequence)
		observations = append(observations, result.observations...)
		for name, advertised := range result.capabilities {
			capabilitySeen[name] = capabilitySeen[name] || advertised
		}
	}

	counts := make(map[string]map[string]int)
	for _, method := range realPrismaRawMethods {
		counts[method] = map[string]int{"nonempty": 0, "empty": 0, "null": 0, "error": 0}
	}
	for _, observation := range observations {
		counts[observation.method][observation.class]++
	}
	tokensByName := make(map[string]realPrismaRawToken, len(realPrismaRawTokens))
	for _, token := range realPrismaRawTokens {
		tokensByName[token.name] = token
	}
	positiveRequired := 0
	positiveSatisfied := 0
	negativeChecks := 0
	negativeLegalEmpty := 0
	for _, observation := range observations {
		token, ok := tokensByName[observation.token]
		if !ok {
			t.Fatalf("Prisma raw observation references unknown token %q", observation.token)
		}
		if token.negative {
			negativeChecks++
			if observation.class == "null" || observation.class == "empty" {
				negativeLegalEmpty++
			} else {
				t.Errorf("Prisma raw negative control sequence=%s token=%s method=%s class=%s; whitespace must be null or empty", observation.sequence, observation.token, observation.method, observation.class)
			}
		}
		for _, expectedMethod := range token.expectMethods {
			if expectedMethod != observation.method {
				continue
			}
			positiveRequired++
			if observation.class == "nonempty" {
				positiveSatisfied++
			} else {
				t.Errorf("Prisma raw positive control sequence=%s token=%s method=%s class=%s; expected non-empty action-specific result", observation.sequence, observation.token, observation.method, observation.class)
			}
		}
	}
	t.Logf("NON_PASS TARGETED_DIAGNOSTIC/NON_LIFECYCLE Prisma raw matrix observations=%d sequences=%d tokens=%d methods=%d capability_hover=%t capability_definition=%t capability_references=%t action_positive_required=%d action_positive_nonempty=%d negative_checks=%d negative_legal_empty=%d elapsed=%s",
		len(observations), len(realPrismaRawSequences), len(realPrismaRawTokens), len(realPrismaRawMethods), capabilitySeen["hover"], capabilitySeen["definition"], capabilitySeen["references"], positiveRequired, positiveSatisfied, negativeChecks, negativeLegalEmpty, time.Since(started).Round(time.Millisecond))
	for _, method := range realPrismaRawMethods {
		t.Logf("Prisma raw matrix method=%s nonempty=%d empty=%d null=%d error=%d", method, counts[method]["nonempty"], counts[method]["empty"], counts[method]["null"], counts[method]["error"])
		if !capabilitySeen[method] {
			t.Errorf("Prisma initialize did not advertise %s; official capability contract evidence is missing", method)
		}
	}
	if positiveSatisfied != positiveRequired {
		t.Errorf("Prisma action-specific positive controls=%d/%d; no global method count can substitute for these controls", positiveSatisfied, positiveRequired)
	}
	if negativeLegalEmpty != negativeChecks {
		t.Errorf("Prisma whitespace negative controls legal_empty/null=%d/%d", negativeLegalEmpty, negativeChecks)
	}
}

var realPrismaRawMethods = []string{"hover", "definition", "references"}

var realPrismaRawTokens = []realPrismaRawToken{
	{name: "user-model-documentation", document: "User.prisma", line: 1, character: 9, needle: "User", expectMethods: []string{"hover", "references"}},
	{name: "post-model-documentation", document: "Post.prisma", line: 1, character: 9, needle: "Post", expectMethods: []string{"hover", "references"}},
	{name: "post-type-reference", document: "User.prisma", line: 5, character: 13, needle: "Post", expectMethods: []string{"hover", "definition", "references"}},
	{name: "user-model-reference", document: "Post.prisma", line: 6, character: 15, needle: "User", expectMethods: []string{"hover", "definition", "references"}},
	{name: "id-field-declaration", document: "User.prisma", line: 2, character: 5, needle: "id"},
	{name: "author-field", document: "Post.prisma", line: 6, character: 5, needle: "author"},
	{name: "relation-attribute", document: "Post.prisma", line: 6, character: 22, needle: "relation"},
	{name: "invalid-author-user-gap", document: "Post.prisma", line: 6, character: 11, whitespace: true, negative: true},
}

var realPrismaRawSequences = []realPrismaRawSequence{
	{name: "didOpen-only"},
	{name: "didOpen-full-didChange", fullChange: true},
	{name: "didOpen-full-didChange-didSave", fullChange: true, save: true},
	{name: "configuration-didOpen-full-didChange-didSave", configuration: true, fullChange: true, save: true},
}

type realPrismaRawToken struct {
	name          string
	document      string
	line          int
	character     int
	needle        string
	whitespace    bool
	expectMethods []string
	negative      bool
}

type realPrismaRawDocument struct {
	name    string
	content string
}

type realPrismaRawProject struct {
	target    string
	documents []realPrismaRawDocument
}

func newRealPrismaRawProject() realPrismaRawProject {
	return realPrismaRawProject{
		target: "User.prisma",
		documents: []realPrismaRawDocument{
			{
				name: "User.prisma",
				content: `/// This is the user of the platform
model User {
    id    String @id @default(uuid()) @map("_id")
    name  String
    email String
    posts Post[]

    address Address

    favouriteAnimal FavouriteAnimal
}
`,
			},
			{
				name: "Post.prisma",
				content: `/// This is a blog post
model Post {
    id       String @id @default(uuid()) @map("_id")
    title    String
    content  String
    authorId String
    author   User   @relation(fields: [authorId], references: [id])
}
`,
			},
			{
				name: "address.prisma",
				content: `/// Petrichor V
type Address {
    /// ISO 3166-2 standard
    country String
    POBox   Int
}
`,
			},
			{
				name: "animal.prisma",
				content: `/// My favourite is the red panda, could you tell?
enum FavouriteAnimal {
    RedPanda
    Cat
    Dog
}
`,
			},
			{
				name: "config.prisma",
				content: `datasource db {
  provider = "mongodb"
}

generator client {
  provider = "prisma-client-js"
  previewFeatures = []
}
`,
			},
		},
	}
}

// validateRealPrismaRawProject 把 raw LSP 的 0-based 坐标锁定到官方 31.11.0
// 多文件 fixture 的真实 token；坐标漂移必须在启动 Node 之前 fail-fast。
func validateRealPrismaRawProject(project realPrismaRawProject) error {
	documents := make(map[string]string, len(project.documents))
	for _, document := range project.documents {
		documents[document.name] = document.content
	}
	for _, token := range realPrismaRawTokens {
		content, ok := documents[token.document]
		if !ok {
			return fmt.Errorf("token %s references missing document %s", token.name, token.document)
		}
		lines := strings.Split(content, "\n")
		if token.line < 0 || token.line >= len(lines) {
			return fmt.Errorf("token %s line=%d outside %s line count=%d", token.name, token.line, token.document, len(lines))
		}
		runes := []rune(lines[token.line])
		if token.character < 0 || token.character >= len(runes) {
			return fmt.Errorf("token %s character=%d outside %s line=%d length=%d", token.name, token.character, token.document, token.line, len(runes))
		}
		if token.whitespace {
			if !unicode.IsSpace(runes[token.character]) {
				return fmt.Errorf("token %s expected whitespace at %s:%d:%d, got %q", token.name, token.document, token.line, token.character, runes[token.character])
			}
			continue
		}
		needle := []rune(token.needle)
		tokenStart := strings.Index(string(runes), token.needle)
		if len(needle) == 0 || tokenStart < 0 || token.character < tokenStart || token.character >= tokenStart+len(needle) {
			return fmt.Errorf("token %s expected position inside %q at %s:%d:%d, got line %q", token.name, token.needle, token.document, token.line, token.character, string(runes))
		}
	}
	return nil
}

type realPrismaRawSequence struct {
	name          string
	configuration bool
	fullChange    bool
	save          bool
}

type realPrismaRawObservation struct {
	sequence string
	token    string
	method   string
	class    string
}

type realPrismaRawSequenceResult struct {
	observations []realPrismaRawObservation
	capabilities map[string]bool
}

func runRealPrismaRawSequence(t *testing.T, nodeDist, installDir string, server realNodeServerCase, sequence realPrismaRawSequence) realPrismaRawSequenceResult {
	t.Helper()
	fixtureRoot := t.TempDir()
	project := newRealPrismaRawProject()
	projectFiles := make(map[string]string, len(project.documents))
	for _, document := range project.documents {
		path := filepath.Join(fixtureRoot, document.name)
		if err := os.WriteFile(path, []byte(document.content), 0o600); err != nil {
			t.Fatalf("write raw Prisma fixture %s: %v", document.name, err)
		}
		projectFiles[document.name] = document.content
	}
	fixture := filepath.Join(fixtureRoot, project.target)
	targetContent, ok := projectFiles[project.target]
	if !ok {
		t.Fatalf("raw Prisma project target %s is not present in locked project", project.target)
	}
	script := filepath.Join(installDir, "node_modules", filepath.FromSlash(server.script))
	if !fileExists(script) {
		t.Fatalf("locked Prisma server script is missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePathForDist(nodeDist), append([]string{script}, server.args...)...)
	cmd.Dir = fixtureRoot
	cmd.Env = realNodeEnvironment(os.Environ(), nodeDist, installDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("raw Prisma stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("raw Prisma stdout: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("raw Prisma stderr: %v", err)
	}
	documents := make(map[string]string, len(project.documents))
	for _, document := range project.documents {
		documents[realFileURI(filepath.Join(fixtureRoot, document.name))] = document.content
	}
	client := &realLSPClient{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    bufio.NewReader(stdout),
		stderr:    &realNodeBuffer{},
		documents: documents,
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start raw Prisma server: %v", err)
	}
	pid := cmd.Process.Pid
	tracked := make(map[realMCPProcessKey]realMCPProcessIdentity)
	startToken, err := windowsGoplsProcessStartIdentity(pid)
	if err != nil {
		t.Fatalf("capture raw Prisma PID=%d start identity: %v", pid, err)
	}
	tracked[realMCPProcessKey{PID: pid, StartToken: startToken}] = realMCPProcessIdentity{PID: pid, StartToken: startToken, Name: "node-prisma-raw", Language: "prisma"}
	t.Logf("Prisma raw sequence=%s pid=%d start_identity=%s fixture_digest=%s", sequence.name, pid, startToken, realPrismaRawDigest([]byte(realFileURI(fixture))))
	go func() { _, _ = io.Copy(client.stderr, stderr) }()

	shutdownSent := false
	exitSent := false
	defer func() {
		if !shutdownSent {
			t.Logf("Prisma raw sequence=%s shutdown_sent=false", sequence.name)
		}
		treeCaptured := trackRealMCPProcessTree(t, pid, "raw-prisma-matrix-"+sequence.name, tracked)
		client.close(t)
		if !treeCaptured {
			t.Errorf("Prisma raw sequence=%s process tree snapshot unavailable", sequence.name)
		}
		requireRealMCPProcessIdentitiesGone(t, tracked)
		t.Logf("Prisma raw sequence=%s shutdown_sent=%t exit_sent=%t zero_residual=true", sequence.name, shutdownSent, exitSent)
	}()

	rootURI := realFileURI(fixtureRoot)
	initialized, err := client.request(ctx, "initialize", realInitializeParams(rootURI))
	if err != nil {
		t.Fatalf("Prisma raw sequence=%s initialize: %v", sequence.name, err)
	}
	capabilities := prismaRawCapabilities(initialized)
	t.Logf("Prisma raw sequence=%s capabilities hover=%t definition=%t references=%t", sequence.name, capabilities["hover"], capabilities["definition"], capabilities["references"])
	if err := client.notify("initialized", map[string]any{}); err != nil {
		t.Fatalf("Prisma raw sequence=%s initialized: %v", sequence.name, err)
	}
	if sequence.configuration {
		if err := client.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"prisma": map[string]any{}}}); err != nil {
			t.Fatalf("Prisma raw sequence=%s configuration: %v", sequence.name, err)
		}
	}
	uri := realFileURI(fixture)
	for _, document := range project.documents {
		documentURI := realFileURI(filepath.Join(fixtureRoot, document.name))
		if err := client.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{"uri": documentURI, "languageId": server.languageID, "version": 1, "text": document.content},
		}); err != nil {
			t.Fatalf("Prisma raw sequence=%s didOpen document=%s: %v", sequence.name, document.name, err)
		}
	}
	if sequence.fullChange {
		if err := client.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": 2},
			"contentChanges": []map[string]any{{"text": targetContent}},
		}); err != nil {
			t.Fatalf("Prisma raw sequence=%s didChange: %v", sequence.name, err)
		}
	}
	if sequence.save {
		if err := client.notify("textDocument/didSave", map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"text":         targetContent,
		}); err != nil {
			t.Fatalf("Prisma raw sequence=%s didSave: %v", sequence.name, err)
		}
	}
	readiness, readinessErr := client.request(ctx, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": uri}})
	if readinessErr != nil {
		t.Logf("Prisma raw sequence=%s readiness=documentSymbol error=%s", sequence.name, prismaRawRedactError(readinessErr.Error(), fixtureRoot))
	} else {
		t.Logf("Prisma raw sequence=%s readiness=documentSymbol class=%s bytes=%d sha256=%s", sequence.name, prismaRawResponseClass(readiness), len(readiness), realPrismaRawDigest(readiness))
	}

	result := realPrismaRawSequenceResult{capabilities: capabilities}
	for _, token := range realPrismaRawTokens {
		tokenPath := filepath.Join(fixtureRoot, token.document)
		tokenURI := realFileURI(tokenPath)
		for _, method := range realPrismaRawMethods {
			params := map[string]any{
				"textDocument": map[string]any{"uri": tokenURI},
				"position":     map[string]any{"line": token.line, "character": token.character},
			}
			if method == "references" {
				params["context"] = map[string]any{"includeDeclaration": true}
			}
			requestMethod := "textDocument/" + method
			raw, requestErr := client.request(ctx, requestMethod, params)
			observation := realPrismaRawObservation{sequence: sequence.name, token: token.name, method: method}
			if requestErr != nil {
				observation.class = "error"
				t.Logf("Prisma raw sequence=%s token=%s method=%s line=%d character=%d class=error detail=%s", sequence.name, token.name, method, token.line, token.character, prismaRawRedactError(requestErr.Error(), fixtureRoot))
			} else {
				observation.class = prismaRawResponseClass(raw)
				t.Logf("Prisma raw sequence=%s token=%s method=%s line=%d character=%d class=%s bytes=%d sha256=%s", sequence.name, token.name, method, token.line, token.character, observation.class, len(raw), realPrismaRawDigest(raw))
			}
			result.observations = append(result.observations, observation)
		}
	}
	if len(client.serverMessages) > 0 {
		t.Logf("Prisma raw sequence=%s server_messages=%s", sequence.name, prismaRawRedactError(strings.Join(client.serverMessages, " | "), fixtureRoot, installDir))
	}
	if stderrText := client.stderr.String(); stderrText != "" {
		t.Logf("Prisma raw sequence=%s stderr_bytes=%d stderr_sha256=%s stderr=%s", sequence.name, len(stderrText), realPrismaRawDigest([]byte(stderrText)), prismaRawRedactError(stderrText, fixtureRoot, installDir))
	}
	for index := len(project.documents) - 1; index >= 0; index-- {
		documentURI := realFileURI(filepath.Join(fixtureRoot, project.documents[index].name))
		if err := client.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": documentURI}}); err != nil {
			t.Errorf("Prisma raw sequence=%s didClose document=%s: %v", sequence.name, project.documents[index].name, err)
		}
	}
	if _, err := client.request(ctx, "shutdown", nil); err != nil {
		t.Errorf("Prisma raw sequence=%s shutdown: %v", sequence.name, err)
	} else {
		shutdownSent = true
	}
	if err := client.notify("exit", nil); err != nil {
		t.Errorf("Prisma raw sequence=%s exit: %v", sequence.name, err)
	} else {
		exitSent = true
	}
	return result
}

func prismaRawCapabilities(raw json.RawMessage) map[string]bool {
	var envelope struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return map[string]bool{}
	}
	result := make(map[string]bool, 3)
	for _, method := range realPrismaRawMethods {
		rootKey := method + "Provider"
		if value, ok := envelope.Capabilities[rootKey]; ok && prismaRawCapabilityValue(value) {
			result[method] = true
			continue
		}
		var textDocument map[string]json.RawMessage
		if value, ok := envelope.Capabilities["textDocument"]; ok && json.Unmarshal(value, &textDocument) == nil {
			result[method] = prismaRawCapabilityValue(textDocument[method])
		}
	}
	return result
}

func prismaRawCapabilityValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) && !bytes.Equal(trimmed, []byte("false"))
}

func prismaRawResponseClass(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "error"
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return "null"
	}
	if realJSONNonEmpty(trimmed) {
		return "nonempty"
	}
	return "empty"
}

func realPrismaRawDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func prismaRawRedactError(value string, roots ...string) string {
	for _, root := range roots {
		value = strings.ReplaceAll(value, root, "<redacted-root>")
		value = strings.ReplaceAll(value, filepath.ToSlash(root), "<redacted-root>")
	}
	if strings.TrimSpace(value) == "" {
		return "<empty-error>"
	}
	return value
}
