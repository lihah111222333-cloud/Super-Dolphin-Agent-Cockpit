package nodeexec

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestArtifactTarget_RoundTrip(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{
		"exec":{"agent_key":"video","cwd":"/tmp/node-cwd"},
		"outputs":{
			"to_node_result":false,
			"to_artifact":{
				"source_tool":"video_with_audio",
				"source_path_field":"output_path",
				"path_template":"dag/douyin/daily-video/{{run_id}}/final.mp4",
				"content_type":"video/mp4",
				"allowed_extensions":[".mp4"],
				"allowed_source_roots":["${HOME}/Movies"],
				"max_bytes":524288000,
				"overwrite":"fail"
			}
		}
	}`)
	got, err := ParseAgentConfig(raw)
	if err != nil {
		t.Fatalf("ParseAgentConfig() error = %v", err)
	}
	target := got.Outputs.ToArtifact
	if target == nil {
		t.Fatalf("Outputs.ToArtifact = nil")
	}
	assertArtifactTargetSelector(t, target)
	assertArtifactTargetPolicy(t, target)
	assertArtifactTargetRoundTripJSON(t, got)
}

func assertArtifactTargetRoundTripJSON(t *testing.T, got *AgentNodeConfig) {
	t.Helper()
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !contains(string(data), `"to_artifact":`) || !contains(string(data), `"source_tool":"video_with_audio"`) {
		t.Fatalf("to_artifact did not round-trip in JSON: %s", data)
	}
}

func assertArtifactTargetSelector(t *testing.T, target *ArtifactTarget) {
	t.Helper()
	if target.SourceTool != "video_with_audio" {
		t.Fatalf("SourceTool = %q", target.SourceTool)
	}
	if target.SourcePathField != "output_path" {
		t.Fatalf("SourcePathField = %q", target.SourcePathField)
	}
	if target.PathTemplate != "dag/douyin/daily-video/{{run_id}}/final.mp4" {
		t.Fatalf("PathTemplate = %q", target.PathTemplate)
	}
}

func assertArtifactTargetPolicy(t *testing.T, target *ArtifactTarget) {
	t.Helper()
	if target.ContentType != "video/mp4" {
		t.Fatalf("ContentType = %q", target.ContentType)
	}
	if len(target.AllowedExtensions) != 1 || target.AllowedExtensions[0] != ".mp4" {
		t.Fatalf("AllowedExtensions = %+v", target.AllowedExtensions)
	}
	if len(target.AllowedSourceRoots) != 1 || target.AllowedSourceRoots[0] != "${HOME}/Movies" {
		t.Fatalf("AllowedSourceRoots = %+v", target.AllowedSourceRoots)
	}
	if target.MaxBytes != 524288000 || target.Overwrite != "fail" {
		t.Fatalf("size/overwrite lost: %+v", target)
	}
}

func TestArtifactTextTarget_RoundTrip(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{
		"exec":{"agent_key":"writer","cwd":"/tmp/node-cwd"},
		"outputs":{
			"to_node_result":false,
			"to_artifact":{
				"source_tool":"document_renderer",
				"source_text_field":"document_text",
				"path_template":"dag/government/{{run_id}}/final.docx",
				"content_type":"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
				"max_bytes":10485760,
				"overwrite":"replace"
			}
		}
	}`)
	got, err := ParseAgentConfig(raw)
	if err != nil {
		t.Fatalf("ParseAgentConfig() error = %v", err)
	}
	target := got.Outputs.ToArtifact
	if target == nil {
		t.Fatalf("Outputs.ToArtifact = nil")
	}
	if target.SourcePathField != "" {
		t.Fatalf("SourcePathField = %q, want empty for generated document", target.SourcePathField)
	}
	if target.SourceTextField != "document_text" {
		t.Fatalf("SourceTextField = %q, want document_text", target.SourceTextField)
	}
	plan, err := BuildArtifactTextPlan(target, `{"document_text":"审批材料正文"}`, 88)
	if err != nil {
		t.Fatalf("BuildArtifactTextPlan() error = %v", err)
	}
	if plan.TargetPath != "dag/government/88/final.docx" || plan.SourceText != "审批材料正文" {
		t.Fatalf("text plan = %+v", plan)
	}
}

func TestArtifactTarget_MissingRequiredFieldsRejected(t *testing.T) {
	t.Parallel()
	base := `{"exec":{"agent_key":"video","cwd":"/tmp/node-cwd"},"outputs":{"to_artifact":%s}}`
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "missing_source_tool", raw: `{"source_path_field":"output_path","path_template":"dag/x/{{run_id}}/final.mp4"}`, want: "source_tool"},
		{name: "missing_source_path_field", raw: `{"source_tool":"video_with_audio","path_template":"dag/x/{{run_id}}/final.mp4"}`, want: "source_path_field"},
		{name: "missing_path_template", raw: `{"source_tool":"video_with_audio","source_path_field":"output_path"}`, want: "path_template"},
		{name: "path_template_without_run_token", raw: `{"source_tool":"video_with_audio","source_path_field":"output_path","path_template":"dag/x/final.mp4"}`, want: "{{run_key}} or {{run_id}}"},
		{name: "path_and_text_sources_conflict", raw: `{"source_tool":"document_renderer","source_path_field":"output_path","source_text_field":"document_text","path_template":"dag/x/{{run_id}}/final.docx"}`, want: "mutually exclusive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAgentConfig(json.RawMessage(fmt.Sprintf(base, tc.raw)))
			if err == nil {
				t.Fatalf("ParseAgentConfig() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
