package orchestration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sharedfile"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
)

func TestDAGSubscriber_ArtifactTargetImportsStructuredVideoResult(t *testing.T) {
	sourcePath := "/tmp/video-with-audio/final.mp4"
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-video",
		NodeKey:  "generate_video_mp4",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(42),
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"video","cwd":"/tmp/node-cwd"},
			"outputs":{
				"to_node_result":false,
				"to_artifact":{
					"source_tool":"video_with_audio",
					"source_path_field":"output_path",
					"path_template":"dag/douyin/daily-video/{{run_id}}/final.mp4",
					"content_type":"video/mp4",
					"allowed_extensions":[".mp4"],
					"allowed_source_roots":["/tmp/video-with-audio"],
					"max_bytes":524288000,
					"overwrite":"fail"
				}
			}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	importer := &dagSubscriberArtifactImporterSpy{}
	writer := &dagSubscriberSharedFileWriterSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-artifact", AgentID: "agent-artifact"}},
		&dagSubscriberStopSpy{},
	)
	deps.ArtifactImporter = importer
	deps.SharedFileWriter = writer
	ev := newTurnCompletedEvent("thr-artifact", true, `{"success":true,"output_path":"`+sourcePath+`"}`)
	ev.Summary = "natural language report mentioning /Users/ai/Movie/final.mp4 must be ignored"

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), ev)

	wantTarget := "dag/douyin/daily-video/42/final.mp4"
	assertArtifactImportCall(t, importer, sourcePath, wantTarget)
	assertArtifactCompletion(t, flow, writer, wantTarget)
}

func assertArtifactImportCall(t *testing.T, importer *dagSubscriberArtifactImporterSpy, sourcePath, wantTarget string) {
	t.Helper()
	if len(importer.imports) != 1 {
		t.Fatalf("artifact imports = %d, want 1", len(importer.imports))
	}
	gotImport := importer.imports[0]
	if gotImport.SourcePath != sourcePath {
		t.Fatalf("SourcePath = %q, want %q", gotImport.SourcePath, sourcePath)
	}
	if gotImport.TargetPath != wantTarget {
		t.Fatalf("TargetPath = %q, want %q", gotImport.TargetPath, wantTarget)
	}
	if gotImport.ContentType != "video/mp4" || gotImport.Overwrite != "fail" {
		t.Fatalf("content type / overwrite lost: %+v", gotImport)
	}
}

func assertArtifactCompletion(t *testing.T, flow *dagSubscriberFlowSpy, writer *dagSubscriberSharedFileWriterSpy, wantTarget string) {
	t.Helper()
	if len(writer.writes) != 0 {
		t.Fatalf("SharedFileWriter writes = %d, want 0; artifact importer owns binary copy", len(writer.writes))
	}
	if len(flow.claimCalls) != 1 {
		t.Fatalf("claimCalls = %d, want 1 before artifact import", len(flow.claimCalls))
	}
	if got := string(flow.claimCalls[0].Result); got != `{"sharedfile":{"path":"`+wantTarget+`"}}` {
		t.Fatalf("ClaimNodeOutputMaterialization.Result = %s", got)
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if got := string(flow.completeCalls[0].Result); got != `{"sharedfile":{"path":"`+wantTarget+`"}}` {
		t.Fatalf("CompleteNodeInput.Result = %s", got)
	}
}

func TestDAGSubscriber_ArtifactTargetRejectsNaturalLanguagePathFallback(t *testing.T) {
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-video",
		NodeKey:  "generate_video_mp4",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(43),
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"video","cwd":"/tmp/node-cwd"},
			"outputs":{
				"to_node_result":false,
				"to_artifact":{
					"source_tool":"video_with_audio",
					"source_path_field":"output_path",
					"path_template":"dag/douyin/daily-video/{{run_id}}/final.mp4",
					"allowed_extensions":[".mp4"],
					"overwrite":"fail"
				}
			}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	importer := &dagSubscriberArtifactImporterSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-artifact-natural", AgentID: "agent-artifact"}},
		&dagSubscriberStopSpy{},
	)
	deps.ArtifactImporter = importer
	ev := newTurnCompletedEvent("thr-artifact-natural", true, "")
	ev.Summary = "done: saved to /Users/ai/Movies/final.mp4"

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), ev)

	if len(importer.imports) != 0 {
		t.Fatalf("artifact imports = %d, want 0 when only natural language path is available", len(importer.imports))
	}
	if len(flow.completeCalls) != 0 || len(flow.claimCalls) != 0 {
		t.Fatalf("complete/claim calls = %d/%d, want 0/0", len(flow.completeCalls), len(flow.claimCalls))
	}
	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(flow.failCalls))
	}
	if got := flow.failCalls[0].Reason; !strings.Contains(got, "structured JSON") {
		t.Fatalf("failure reason = %q, want structured JSON", got)
	}
}

func TestDAGSubscriber_DocumentArtifactTargetGeneratesRealFiles(t *testing.T) {
	tests := []struct {
		name          string
		pathTemplate  string
		wantTarget    string
		contentType   string
		wantExtension string
		assertBytes   func(*testing.T, []byte)
	}{
		{
			name:          "docx",
			pathTemplate:  "dag/gov/approval/{{run_id}}/final.docx",
			wantTarget:    "dag/gov/approval/44/final.docx",
			contentType:   "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			wantExtension: ".docx",
			assertBytes:   assertDOCXBytes,
		},
		{
			name:          "pdf",
			pathTemplate:  "dag/gov/approval/{{run_id}}/final.pdf",
			wantTarget:    "dag/gov/approval/44/final.pdf",
			contentType:   "application/pdf",
			wantExtension: ".pdf",
			assertBytes:   assertPDFBytes,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
				DagKey:   "dag-approval",
				NodeKey:  "final_pack",
				NodeType: "agent",
				Status:   "running",
				RunID:    dagSubscriberTestRunID(44),
				Config: testRawConfig(t, `{
					"exec":{"agent_key":"writer","cwd":"/tmp/node-cwd"},
					"outputs":{
						"to_node_result":false,
						"to_artifact":{
							"source_tool":"document_renderer",
							"source_text_field":"document_text",
							"path_template":"`+tc.pathTemplate+`",
							"content_type":"`+tc.contentType+`",
							"overwrite":"replace"
						}
					}
				}`),
			}}}
			flow := &dagSubscriberFlowSpy{}
			importer := &dagSubscriberArtifactImporterSpy{}
			deps := setupDAGSubscriberDeps(
				lookup,
				flow,
				&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-doc-" + tc.name, AgentID: "agent-doc"}},
				&dagSubscriberStopSpy{},
			)
			deps.ArtifactImporter = importer
			ev := newTurnCompletedEvent("thr-doc-"+tc.name, true, `{"document_text":"# 审批材料\n\n事项：数据共享申请\n\n结论：建议通过。"}`)

			handleDAGTurnCompleted(context.Background(), deps, discardLogger(), ev)

			if len(importer.imports) != 1 {
				t.Fatalf("artifact imports = %d, want 1", len(importer.imports))
			}
			gotImport := importer.imports[0]
			if gotImport.TargetPath != tc.wantTarget {
				t.Fatalf("TargetPath = %q, want %q", gotImport.TargetPath, tc.wantTarget)
			}
			if gotImport.ContentType != tc.contentType {
				t.Fatalf("ContentType = %q, want %q", gotImport.ContentType, tc.contentType)
			}
			if !strings.HasSuffix(strings.ToLower(gotImport.SourcePath), tc.wantExtension) {
				t.Fatalf("SourcePath = %q, want extension %s", gotImport.SourcePath, tc.wantExtension)
			}
			tc.assertBytes(t, importer.sourceBytes)
			assertArtifactCompletion(t, flow, &dagSubscriberSharedFileWriterSpy{}, tc.wantTarget)
		})
	}
}

func assertDOCXBytes(t *testing.T, data []byte) {
	t.Helper()
	if len(data) < 4 || string(data[:2]) != "PK" {
		t.Fatalf("docx bytes must start with ZIP signature; got %q", data[:min(len(data), 4)])
	}
	if !strings.Contains(string(data), "[Content_Types].xml") {
		t.Fatalf("docx zip missing [Content_Types].xml entry")
	}
}
func assertPDFBytes(t *testing.T, data []byte) {
	t.Helper()
	if !strings.HasPrefix(string(data), "%PDF-") {
		t.Fatalf("pdf bytes must start with %%PDF-; got %q", string(data[:min(len(data), 5)]))
	}
	if !strings.Contains(string(data), "%%EOF") {
		t.Fatalf("pdf bytes missing EOF marker")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type dagSubscriberArtifactImporterSpy struct {
	imports     []sharedfile.ImportLocalFileParams
	sourceBytes []byte
	err         error
}

func (s *dagSubscriberArtifactImporterSpy) ImportLocalFile(_ context.Context, params sharedfile.ImportLocalFileParams) (*sharedfile.SharedFile, error) {
	if s.err != nil {
		return nil, s.err
	}
	if strings.TrimSpace(params.TargetPath) == "" {
		return nil, errors.New("target path required")
	}
	sourceBytes, err := os.ReadFile(params.SourcePath)
	if err == nil {
		s.sourceBytes = sourceBytes
	}
	s.imports = append(s.imports, params)
	return &sharedfile.SharedFile{Path: params.TargetPath}, nil
}
