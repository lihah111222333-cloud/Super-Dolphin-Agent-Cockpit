import { guardedBackendResponse } from "./backendApi.guardedResponse.testSupport.js";

export function dashboardPromptResponse() {
  return {
    prompts: [
      {
        id: 17,
        prompt_key: "main/reviewer",
        title: "Reviewer",
        agent_key: "main",
        tool_name: "",
        prompt_text: "Review carefully.",
        when_to_use: "When reviewing code.",
        variables: {},
        tags: ["review"],
        enabled: true,
        manually_edited: false,
        priority: 0,
        created_by: "",
        updated_by: "",
        created_at: "2026-07-13T00:00:00Z",
        updated_at: "2026-07-13T00:00:01Z",
        description: "Review prompt",
      },
    ],
  };
}

export function guardedOpsPromptMemoryResponse(method) {
  return guardedBackendResponse(method);
}
