package wails

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	rpcpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestNewRPCHandlersRegistersNativeDialogRoutes(t *testing.T) {
	t.Parallel()

	handlers := NewRPCHandlers(&App{}, nil, nil).Handlers
	for _, method := range []string{
		"ui/selectProjectDir",
		"ui/selectProjectDirs",
		"ui/selectFiles",
		"ui/selectDatasourceImportFile",
		"ui/readDroppedTextFiles",
		"ui/buildInfo",
		"ui/saveClipboardImage",
		"ui/saveTextFile",
		"ui/log",
		"ui/windowBootstrap/get",
		"ui/openNewWindow",
	} {
		if _, ok := handlers[method]; !ok {
			t.Fatalf("handler %q is not registered", method)
		}
	}
}

func TestSelectDatasourceImportFileReturnsPickerToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	sourcePath := filepath.Join(t.TempDir(), "source.txt")
	defaultPath := filepath.Dir(sourcePath)
	app := &App{
		datasourceImportPickerTokens: newDatasourceImportPickerTokens(func() time.Time { return now }),
		selectFileInvoker: func(gotDefaultPath string, gotFilters []selectFileFilter) (string, error) {
			if gotDefaultPath != defaultPath {
				t.Fatalf("defaultPath = %q, want %q", gotDefaultPath, defaultPath)
			}
			if len(gotFilters) != 1 || gotFilters[0].Pattern != "*.txt" {
				t.Fatalf("filters = %+v, want txt filter", gotFilters)
			}
			return sourcePath, nil
		},
	}
	server := newWailsRPCServer(t, app)

	raw, err := server.Dispatch(context.Background(), "ui/selectDatasourceImportFile", mustJSON(t, map[string]any{
		"defaultPath": defaultPath,
		"filters": []map[string]string{
			{"displayName": "Text", "pattern": "*.txt"},
		},
	}))
	if err != nil {
		t.Fatalf("Dispatch(ui/selectDatasourceImportFile) error = %v", err)
	}
	var got datasourceImportFileSelection
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.SourcePath != sourcePath || got.PickerToken == "" {
		t.Fatalf("selection = %+v, want source path and picker token", got)
	}
	if !app.VerifyDatasourceImportPickerToken(got.SourcePath, got.PickerToken) {
		t.Fatal("VerifyDatasourceImportPickerToken() = false, want true")
	}
	if app.VerifyDatasourceImportPickerToken(got.SourcePath, got.PickerToken) {
		t.Fatal("VerifyDatasourceImportPickerToken() replay = true, want one-time token")
	}
}

func TestSelectDatasourceImportFileCancelReturnsEmptySelection(t *testing.T) {
	t.Parallel()

	app := &App{
		datasourceImportPickerTokens: newDatasourceImportPickerTokens(nil),
		selectFileInvoker: func(string, []selectFileFilter) (string, error) {
			return "", nil
		},
	}
	server := newWailsRPCServer(t, app)

	raw, err := server.Dispatch(context.Background(), "ui/selectDatasourceImportFile", mustJSON(t, map[string]any{}))
	if err != nil {
		t.Fatalf("Dispatch(ui/selectDatasourceImportFile) error = %v", err)
	}
	var got datasourceImportFileSelection
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.SourcePath != "" || got.PickerToken != "" {
		t.Fatalf("selection = %+v, want empty cancel result", got)
	}
}

func TestSelectFilesFiltersNormalizeSupportedPatterns(t *testing.T) {
	t.Parallel()

	got := normalizeSelectFileFilters([]selectFileFilter{
		{DisplayName: " PDF/TXT/TEXT ", Pattern: " *.pdf;*.txt;*.text "},
		{DisplayName: "", Pattern: "*.exe"},
		{DisplayName: "blank", Pattern: ""},
	})

	if len(got) != 1 {
		t.Fatalf("filters len = %d, want 1", len(got))
	}
	if got[0].DisplayName != "PDF/TXT/TEXT" || got[0].Pattern != "*.pdf;*.txt;*.text" {
		t.Fatalf("filter = %+v, want trimmed datasource filter", got[0])
	}
}

func TestCurrentBuildInfoIncludesPackagedAppVersion(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_UPDATE_VERSION", "1.0.2")

	info := currentBuildInfo()

	if info["appVersion"] != "1.0.2" {
		t.Fatalf("appVersion = %q, want packaged update version", info["appVersion"])
	}
}

func TestReadDroppedTextFilesRouteReadsRecentDroppedText(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte("hello\r\nworld\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	app := &App{}
	app.recordDroppedFiles([]string{path}, &application.DropTargetDetails{ElementID: "prompt-intent-drop-zone"})
	server := newWailsRPCServer(t, app)

	raw, err := server.Dispatch(context.Background(), "ui/readDroppedTextFiles", mustJSON(t, map[string]any{
		"files":    []string{path},
		"targetId": "prompt-intent-drop-zone",
	}))
	if err != nil {
		t.Fatalf("Dispatch(ui/readDroppedTextFiles) error = %v", err)
	}

	var result struct {
		Files []struct {
			Path      string `json:"path"`
			Name      string `json:"name"`
			Text      string `json:"text"`
			SizeBytes int64  `json:"sizeBytes"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/readDroppedTextFiles) error = %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("files len = %d, want 1", len(result.Files))
	}
	if result.Files[0].Path != path || result.Files[0].Name != "notes.md" {
		t.Fatalf("file metadata = %#v, want dropped path/name", result.Files[0])
	}
	if result.Files[0].Text != "hello\nworld\n" {
		t.Fatalf("text = %q, want normalized line endings", result.Files[0].Text)
	}
	if result.Files[0].SizeBytes <= 0 {
		t.Fatalf("sizeBytes = %d, want positive", result.Files[0].SizeBytes)
	}
}

func TestReadDroppedTextFilesRouteConvertsDroppedXLSXToMarkdown(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "prices.xlsx")
	writeXLSXFixture(t, path, []xlsxFixtureSheet{
		{
			Name: "报价",
			Rows: [][]xlsxFixtureCell{
				{sharedCell("产品"), inlineCell("套餐"), sharedCell("价格"), sharedCell("有效期")},
				{sharedCell("基础版"), inlineCell("月付"), sharedCell("99元/月"), sharedCell("2026-12-31")},
				{sharedCell("专业版"), inlineCell("年付"), sharedCell("2999元/年"), sharedCell("2026-12-31")},
			},
		},
	})
	app := &App{}
	app.recordDroppedFiles([]string{path}, &application.DropTargetDetails{ElementID: "prompt-intent-drop-zone"})
	server := newWailsRPCServer(t, app)

	raw, err := server.Dispatch(context.Background(), "ui/readDroppedTextFiles", mustJSON(t, map[string]any{
		"files":    []string{path},
		"targetId": "prompt-intent-drop-zone",
	}))
	if err != nil {
		t.Fatalf("Dispatch(ui/readDroppedTextFiles) error = %v", err)
	}

	var result struct {
		Files []struct {
			Name string `json:"name"`
			Text string `json:"text"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/readDroppedTextFiles) error = %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("files len = %d, want 1", len(result.Files))
	}
	if result.Files[0].Name != "prices.xlsx" {
		t.Fatalf("name = %q, want prices.xlsx", result.Files[0].Name)
	}
	want := strings.Join([]string{
		"Sheet：报价",
		"",
		"| 产品 | 套餐 | 价格 | 有效期 |",
		"| --- | --- | --- | --- |",
		"| 基础版 | 月付 | 99元/月 | 2026-12-31 |",
		"| 专业版 | 年付 | 2999元/年 | 2026-12-31 |",
	}, "\n")
	if result.Files[0].Text != want {
		t.Fatalf("xlsx text =\n%s\nwant:\n%s", result.Files[0].Text, want)
	}
}

func TestReadDroppedTextFilesRouteAllowsLargeDroppedXLSXUnderXLSXLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "large-prices.xlsx")
	writeXLSXFixtureWithExtra(t, path, []xlsxFixtureSheet{
		{
			Name: "报价",
			Rows: [][]xlsxFixtureCell{
				{sharedCell("产品"), sharedCell("价格")},
				{sharedCell("基础版"), sharedCell("99元/月")},
			},
		},
	}, bytes.Repeat([]byte("x"), int(maxDroppedTextFileBytes)+1024))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", path, err)
	}
	if info.Size() <= maxDroppedTextFileBytes {
		t.Fatalf("xlsx fixture size = %d, want larger than text limit %d", info.Size(), maxDroppedTextFileBytes)
	}
	app := &App{}
	app.recordDroppedFiles([]string{path}, &application.DropTargetDetails{ElementID: "prompt-intent-drop-zone"})
	server := newWailsRPCServer(t, app)

	raw, err := server.Dispatch(context.Background(), "ui/readDroppedTextFiles", mustJSON(t, map[string]any{
		"files":    []string{path},
		"targetId": "prompt-intent-drop-zone",
	}))
	if err != nil {
		t.Fatalf("Dispatch(ui/readDroppedTextFiles) error = %v", err)
	}
	var result struct {
		Files []struct {
			Text string `json:"text"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/readDroppedTextFiles) error = %v", err)
	}
	if len(result.Files) != 1 || !strings.Contains(result.Files[0].Text, "| 基础版 | 99元/月 |") {
		t.Fatalf("xlsx import result = %#v, want parsed price rows", result.Files)
	}
}

func TestReadDroppedTextFilesRouteRejectsUndroppedOrWrongTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	app := &App{}
	server := newWailsRPCServer(t, app)

	_, err := server.Dispatch(context.Background(), "ui/readDroppedTextFiles", mustJSON(t, map[string]any{
		"files":    []string{path},
		"targetId": "prompt-intent-drop-zone",
	}))
	if err == nil || !strings.Contains(err.Error(), "was not recently dropped") {
		t.Fatalf("undropped Dispatch error = %v, want recent-drop rejection", err)
	}

	app.recordDroppedFiles([]string{path}, &application.DropTargetDetails{ElementID: "chat-input-bar"})
	_, err = server.Dispatch(context.Background(), "ui/readDroppedTextFiles", mustJSON(t, map[string]any{
		"files":    []string{path},
		"targetId": "prompt-intent-drop-zone",
	}))
	if err == nil || !strings.Contains(err.Error(), "was not recently dropped") {
		t.Fatalf("wrong target Dispatch error = %v, want recent-drop rejection", err)
	}
}

func TestReadDroppedTextFilesRouteRejectsBinaryFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "archive.bin")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	app := &App{}
	app.recordDroppedFiles([]string{path}, &application.DropTargetDetails{ElementID: "prompt-intent-drop-zone"})
	server := newWailsRPCServer(t, app)

	_, err := server.Dispatch(context.Background(), "ui/readDroppedTextFiles", mustJSON(t, map[string]any{
		"files":    []string{path},
		"targetId": "prompt-intent-drop-zone",
	}))
	if err == nil || !strings.Contains(err.Error(), "binary file is not supported") {
		t.Fatalf("Dispatch error = %v, want binary rejection", err)
	}
}

func TestUILogRouteAcceptsClientMetaAndCountsEntries(t *testing.T) {
	t.Parallel()

	server := newWailsRPCServer(t, &App{})
	raw, err := server.Dispatch(context.Background(), "ui/log", json.RawMessage(`{
		"entries":[
			{"level":"warn","scope":"thread","event":"opened","seq":1},
			{"level":"error","scope":"ui","event":"hydrate_failed","seq":2}
		],
		"_aoClientKind":"desktop-wails",
		"_aoClientRoute":"/chat"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/log) error = %v", err)
	}

	var result struct {
		OK       bool `json:"ok"`
		Ingested int  `json:"ingested"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/log) error = %v", err)
	}
	if !result.OK || result.Ingested != 2 {
		t.Fatalf("ui/log result = %#v, want ok=true ingested=2", result)
	}
}

func TestWindowBootstrapRouteConsumesSnapshotOnce(t *testing.T) {
	t.Parallel()

	server := newWailsRPCServer(t, &App{
		windowBootstrap: map[string]any{"page": "chat"},
	})

	first := dispatchBootstrapGet(t, server)
	if first.Snapshot["page"] != "chat" {
		t.Fatalf("first snapshot = %#v, want page=chat", first.Snapshot)
	}

	second := dispatchBootstrapGet(t, server)
	if second.Snapshot != nil {
		t.Fatalf("second snapshot = %#v, want nil", second.Snapshot)
	}
}

func TestOpenNewWindowRouteDefaultsGroupAndEncodesSnapshot(t *testing.T) {
	t.Parallel()

	var capturedGroup string
	var capturedN int
	var capturedBootstrap string
	var capturedCWD string
	server := newWailsRPCServer(t, &App{
		group: "team-alpha",
		openNewWindowInvoker: func(group string, n int, uiBootstrap, cwd string) (string, error) {
			capturedGroup = group
			capturedN = n
			capturedBootstrap = uiBootstrap
			capturedCWD = cwd
			return "window-7", nil
		},
	})

	raw, err := server.Dispatch(context.Background(), "ui/openNewWindow", json.RawMessage(`{
		"cwd":"/tmp/project",
		"snapshot":{"page":"chat"},
		"_aoClientKind":"desktop-wails",
		"_aoClientRoute":"/chat"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/openNewWindow) error = %v", err)
	}

	var result struct {
		OK       bool   `json:"ok"`
		WindowID string `json:"windowId"`
		CWD      string `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/openNewWindow) error = %v", err)
	}
	assertOpenNewWindowResult(t, result.OK, result.WindowID, result.CWD)
	assertOpenNewWindowParams(t, capturedGroup, capturedN, capturedCWD)
	snapshot, err := decodeWindowBootstrapSnapshot(capturedBootstrap)
	if err != nil {
		t.Fatalf("decodeWindowBootstrapSnapshot() error = %v", err)
	}
	if snapshot["page"] != "chat" {
		t.Fatalf("captured snapshot = %#v, want page=chat", snapshot)
	}
}

func assertOpenNewWindowResult(t *testing.T, ok bool, windowID, cwd string) {
	t.Helper()

	if !ok {
		t.Fatal("ui/openNewWindow ok = false, want true")
	}
	if windowID != "window-7" {
		t.Fatalf("ui/openNewWindow windowID = %q, want window-7", windowID)
	}
	if cwd != "/tmp/project" {
		t.Fatalf("ui/openNewWindow cwd = %q, want /tmp/project", cwd)
	}
}

func assertOpenNewWindowParams(t *testing.T, group string, n int, cwd string) {
	t.Helper()

	if group != "team-alpha" {
		t.Fatalf("captured group = %q, want team-alpha", group)
	}
	if n != 0 {
		t.Fatalf("captured n = %d, want 0", n)
	}
	if cwd != "/tmp/project" {
		t.Fatalf("captured cwd = %q, want /tmp/project", cwd)
	}
}

func TestHandleCopyTextHeadlessReturnsSoftFailure(t *testing.T) {
	t.Parallel()

	result, err := handleCopyText(&App{}, "hello")
	if err != nil {
		t.Fatalf("handleCopyText() error = %v", err)
	}
	if ok, _ := result["ok"].(bool); ok {
		t.Fatalf("handleCopyText() ok = true, want false")
	}
	if result["error"] != "clipboard not available in headless mode" {
		t.Fatalf("handleCopyText() error = %#v", result["error"])
	}
}

func TestCopyTextPreservesLeadingAndTrailingWhitespace(t *testing.T) {
	source, err := os.ReadFile("rpc.go")
	if err != nil {
		t.Fatalf("ReadFile(rpc.go) error = %v", err)
	}
	text := string(source)
	if strings.Contains(text, "handleCopyText(app, strings.TrimSpace(p.Text))") {
		t.Fatal("ui/copyText trims clipboard text before calling handleCopyText")
	}
	if !strings.Contains(text, "return handleCopyText(app, p.Text)") {
		t.Fatal("ui/copyText must pass p.Text to handleCopyText without trimming")
	}
}

func TestCopyTextRejectsBlankText(t *testing.T) {
	_, err := handleCopyText(&App{}, " \n\t ")
	if err == nil {
		t.Fatal("handleCopyText() error = nil, want blank text rejection")
	}
	if !strings.Contains(err.Error(), "clipboard text is empty") {
		t.Fatalf("handleCopyText() error = %v, want clipboard text is empty", err)
	}
}

// TestCodeSaveRejectsNullContent 锁定 ui/code/save 的持久化边界：
// JSON null 不能被当成空字符串写入并清空已有文件。
func TestCodeSaveRejectsNullContent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "src", "app.js")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(target, []byte("const oldValue = true;\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(old) error = %v", err)
	}

	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewRPCHandlers(&App{}, &config.Config{ProjectRoot: root}, nil).Handlers)

	_, err := server.Dispatch(context.Background(), "ui/code/save", json.RawMessage(`{
		"filePath":"src/app.js",
		"content":null
	}`))
	if err == nil {
		t.Fatal("Dispatch(ui/code/save) error = nil, want null content rejection")
	}
	if !strings.Contains(err.Error(), "content must be a string") {
		t.Fatalf("Dispatch(ui/code/save) error = %v, want content must be a string", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if string(data) != "const oldValue = true;\n" {
		t.Fatalf("file content = %q, want original content unchanged", string(data))
	}
}

func newWailsRPCServer(t *testing.T, app *App) *rpcpkg.Server {
	t.Helper()

	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewRPCHandlers(app, nil, nil).Handlers)
	return server
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}

type xlsxFixtureSheet struct {
	Name string
	Rows [][]xlsxFixtureCell
}

type xlsxFixtureCell struct {
	Text   string
	Inline bool
}

func sharedCell(text string) xlsxFixtureCell {
	return xlsxFixtureCell{Text: text}
}

func inlineCell(text string) xlsxFixtureCell {
	return xlsxFixtureCell{Text: text, Inline: true}
}

func writeXLSXFixture(t *testing.T, path string, sheets []xlsxFixtureSheet) {
	t.Helper()

	writeXLSXFixtureWithExtra(t, path, sheets, nil)
}

func writeXLSXFixtureWithExtra(t *testing.T, path string, sheets []xlsxFixtureSheet, extra []byte) {
	t.Helper()

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	sharedIndex := map[string]int{}
	var sharedValues []string
	for _, sheet := range sheets {
		for _, row := range sheet.Rows {
			for _, cell := range row {
				if cell.Inline {
					continue
				}
				if _, ok := sharedIndex[cell.Text]; ok {
					continue
				}
				sharedIndex[cell.Text] = len(sharedValues)
				sharedValues = append(sharedValues, cell.Text)
			}
		}
	}

	addZipFile(t, writer, "[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  <Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>
</Types>`)
	addZipFile(t, writer, "_rels/.rels", `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`)
	addZipFile(t, writer, "xl/workbook.xml", xlsxFixtureWorkbookXML(sheets))
	addZipFile(t, writer, "xl/_rels/workbook.xml.rels", xlsxFixtureWorkbookRelsXML(sheets))
	addZipFile(t, writer, "xl/sharedStrings.xml", xlsxFixtureSharedStringsXML(sharedValues))
	for i, sheet := range sheets {
		addZipFile(t, writer, fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), xlsxFixtureWorksheetXML(sheet.Rows, sharedIndex))
	}
	if len(extra) > 0 {
		addZipBytesFile(t, writer, "xl/media/large.bin", extra)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close xlsx zip writer error = %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func addZipBytesFile(t *testing.T, writer *zip.Writer, name string, body []byte) {
	t.Helper()

	header := &zip.FileHeader{Name: name, Method: zip.Store}
	file, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatalf("Create(%s) error = %v", name, err)
	}
	if _, err := file.Write(body); err != nil {
		t.Fatalf("Write(%s) error = %v", name, err)
	}
}

func addZipFile(t *testing.T, writer *zip.Writer, name, body string) {
	t.Helper()

	file, err := writer.Create(name)
	if err != nil {
		t.Fatalf("Create(%s) error = %v", name, err)
	}
	if _, err := file.Write([]byte(body)); err != nil {
		t.Fatalf("Write(%s) error = %v", name, err)
	}
}

func xlsxFixtureWorkbookXML(sheets []xlsxFixtureSheet) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	for i, sheet := range sheets {
		fmt.Fprintf(&b, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlAttr(sheet.Name), i+1, i+1)
	}
	b.WriteString(`</sheets></workbook>`)
	return b.String()
}

func xlsxFixtureWorkbookRelsXML(sheets []xlsxFixtureSheet) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := range sheets {
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, i+1)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

func xlsxFixtureSharedStringsXML(values []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`, len(values), len(values))
	for _, value := range values {
		fmt.Fprintf(&b, `<si><t>%s</t></si>`, xmlText(value))
	}
	b.WriteString(`</sst>`)
	return b.String()
}

func xlsxFixtureWorksheetXML(rows [][]xlsxFixtureCell, sharedIndex map[string]int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for rowIndex, row := range rows {
		fmt.Fprintf(&b, `<row r="%d">`, rowIndex+1)
		for colIndex, cell := range row {
			ref := fmt.Sprintf("%s%d", xlsxFixtureColumnName(colIndex+1), rowIndex+1)
			if cell.Inline {
				fmt.Fprintf(&b, `<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, ref, xmlText(cell.Text))
				continue
			}
			fmt.Fprintf(&b, `<c r="%s" t="s"><v>%d</v></c>`, ref, sharedIndex[cell.Text])
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

func xlsxFixtureColumnName(col int) string {
	var out []byte
	for col > 0 {
		col--
		out = append([]byte{byte('A' + col%26)}, out...)
		col /= 26
	}
	return string(out)
}

func xmlAttr(value string) string {
	return xmlText(value)
}

func xmlText(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

func dispatchBootstrapGet(t *testing.T, server *rpcpkg.Server) struct {
	Snapshot map[string]any `json:"snapshot"`
} {
	t.Helper()

	raw, err := server.Dispatch(context.Background(), "ui/windowBootstrap/get", json.RawMessage(`{
		"_aoClientKind":"desktop-wails",
		"_aoClientRoute":"/chat"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/windowBootstrap/get) error = %v", err)
	}

	var result struct {
		Snapshot map[string]any `json:"snapshot"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal(ui/windowBootstrap/get) error = %v", err)
	}
	return result
}
