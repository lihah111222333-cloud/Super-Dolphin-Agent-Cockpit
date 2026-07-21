import { expect, it } from "vitest";
import { spawnSync } from "node:child_process";
import { readFile, realpath, symlink } from "node:fs/promises";
import { join } from "node:path";
import {
  auditRpcContracts,
  astReferencesFacadeForTest,
  collectHardcodedPayloadGuardFindingsFromSources,
  collectSidebarRequiredFieldFindingsFromSources,
} from "./rpc-contract-audit.mjs";
import { REPO_ROOT, createShadowRepo } from "./rpc-audit-test-support.mjs";

describe("rpc contract audit", { timeout: 30000 }, () => {
  it("skips deep AST traversal when a file has no relevant facade bindings", () => {
    const unrelatedStatement = {
      type: "ExpressionStatement",
      get expression() {
        throw new Error("unrelated AST was traversed");
      },
    };
    const ast = {
      type: "File",
      program: {
        type: "Program",
        body: [unrelatedStatement],
      },
    };

    expect(
      astReferencesFacadeForTest(
        ast,
        "frontend-app/src/unrelated.js",
        { key: "CONFIG_READ", facade: "readConfig" },
        new Map([["readConfig", "CONFIG_READ"]]),
        new Map(),
      ),
    ).toBe(false);
  });

  it("accepts the production matrix after response policy migration", async () => {
    const report = await auditRpcContracts({ repoRoot: REPO_ROOT });

    expect(report).toEqual(
      expect.objectContaining({
        missingResponsePolicies: [],
        missingFrontendResponseValidators: [],
        invalidResponsePolicyEvidence: [],
        responseContractStrategies: expect.arrayContaining([
          expect.objectContaining({
            key: "UI_SIDEBAR_GET",
            method: "ui/sidebar/get",
            frontendValidator: true,
          }),
          expect.objectContaining({
            key: "THREAD_FORK",
            method: "thread/fork",
            frontendValidator: true,
          }),
        ]),
      }),
    );
    expect(report.missingRegistryKeys).toEqual([]);
    expect(report.registryWithoutRpcMethods).toEqual([]);
    expect(report.mismatchedRegistryMethods).toEqual([]);
    expect(report.p0MissingBackendHandlers).toEqual([]);
    expect(report.backendHandlers).toContainEqual({
      file: "internal/module/turn/rpc.go",
      method: "turn/interrupt",
    });
    expect(report.backendHandlers).not.toContainEqual(
      expect.objectContaining({ method: "http://json-schema.org/draft-04/schema" }),
    );
    expect(report.backendHandlers).not.toContainEqual(
      expect.objectContaining({ method: "darwin/arm64" }),
    );
    expect(report.backendHandlers.every(({ method }) => !method.includes("\n"))).toBe(true);
    expect(report.allowedPayloadRegistryDrift).toEqual([]);
    expect(report.hardcodedPayloadGuardFindings).toEqual([]);
    expect(report.auditStats).toEqual(
      expect.objectContaining({
        productionFacadeReferenceIndexBuilds: 1,
      }),
    );
    expect(report.sidebarRequiredFieldFindings).toEqual([]);
    expect(report.responseContractStrategies).toEqual(
      expect.arrayContaining([
        {
          key: "UI_SIDEBAR_GET",
          method: "ui/sidebar/get",
          matrixPolicy: "sidebarStateResponse",
          frontendValidator: true,
        },
        {
          key: "THREAD_FORK",
          method: "thread/fork",
          matrixPolicy: "threadForkResponse",
          frontendValidator: true,
        },
      ]),
    );
    expect(report.frontendPayloadKeysByMethod.get("thread/start")).toEqual(
      expect.arrayContaining(["manualSkillSelection", "manual_skill_selection", "provider"]),
    );
    expect(report.frontendPayloadKeysByMethod.get("turn/start")).toEqual(
      expect.arrayContaining([
        "isWorktree",
        "is_worktree",
        "manualSkillSelection",
        "manual_skill_selection",
      ]),
    );
    expect(report.goPayloadKeysByMethod.get("turn/start")).toEqual(
      expect.arrayContaining(["thread_id", "threadId", "selected_skill_refs", "selectedSkillRefs"]),
    );
    expect(report.frontendPayloadKeysByMethod.get("turn/interrupt")).toEqual(
      expect.arrayContaining(["expectedTurnId", "requestId", "threadId"]),
    );
    expect(report.goPayloadKeysByMethod.get("turn/interrupt")).toEqual(
      expect.arrayContaining([
        "expected_turn_id",
        "expectedTurnId",
        "request_id",
        "requestId",
        "thread_id",
        "threadId",
      ]),
    );
  }, 30000);

  it("audits runtime payload builders when facade shadows stay unchanged", async () => {
    const runtimePath = "frontend-app/src/shared/api/backend/backendApiFactoryThread.js";
    const runtimeSource = await readFile(join(REPO_ROOT, runtimePath), "utf8");
    const mutatedSource = runtimeSource.replace(
      "takePayloadField(unused, 'provider')",
      "takePayloadField(unused, 'provider_shadow')",
    );
    expect(mutatedSource).not.toBe(runtimeSource);
    const repoRoot = await createShadowRepo({ [runtimePath]: mutatedSource });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.allowedPayloadRegistryDrift).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          method: "thread/start",
          missingFrontendKeys: expect.arrayContaining(["provider"]),
          extraFrontendKeys: expect.arrayContaining(["provider_shadow"]),
        }),
      ]),
    );
  });

  it("audits runtime RPC methods when facade shadows stay unchanged", async () => {
    const methodsPath = "frontend-app/src/shared/api/backend/backendRpcMethods.js";
    const methodsSource = await readFile(join(REPO_ROOT, methodsPath), "utf8");
    const mutatedSource = methodsSource.replace(
      "  THREAD_PROMPT_HISTORY: 'thread/promptHistory',\n",
      "",
    );
    expect(mutatedSource).not.toBe(methodsSource);
    const repoRoot = await createShadowRepo({ [methodsPath]: mutatedSource });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.registryWithoutRpcMethods).toContain("THREAD_PROMPT_HISTORY");
  });

  it("fails payload drift when the Stop mapper drops expectedTurnId", async () => {
    const mapperPath = "frontend-app/src/shared/api/backend/backendApiFactoryThread.js";
    const mapperSource = await readFile(join(REPO_ROOT, mapperPath), "utf8");
    const mutatedSource = mapperSource.replace(
      "    { key: 'expectedTurnId', value: takePayloadField(unused, 'expectedTurnId') },\n",
      "",
    );
    expect(mutatedSource).not.toBe(mapperSource);
    const repoRoot = await createShadowRepo({ [mapperPath]: mutatedSource });

    const report = await auditRpcContracts({ repoRoot });

    expect(report.allowedPayloadRegistryDrift).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          method: "turn/interrupt",
          missingFrontendKeys: expect.arrayContaining(["expectedTurnId"]),
        }),
      ]),
    );
  }, 15000);

  it("derives Sidebar required fields from Go tags and detects missing or stale consumer entries", async () => {
    const goSource = await readFile(join(REPO_ROOT, "internal/module/uistate/state.go"), "utf8");
    const runtimePath = "frontend-app/src/shared/api/response-validators/runtime/sidebar-state.js";
    const runtimeSource = await readFile(join(REPO_ROOT, runtimePath), "utf8");
    const missingConsumerSource = runtimeSource.replace("'workspace', ", "");
    const staleProducerSource = goSource.replace(
      'Workspace             WorkspacePanel            `json:"workspace"`',
      'Workspace             WorkspacePanel            `json:"workspace,omitempty"`',
    );

    expect(missingConsumerSource).not.toBe(runtimeSource);
    expect(staleProducerSource).not.toBe(goSource);
    const repoRoot = await createShadowRepo({ [runtimePath]: missingConsumerSource });
    const report = await auditRpcContracts({ repoRoot });
    expect(report.sidebarRequiredFieldFindings).toEqual(["missing:workspace"]);
    expect(
      collectSidebarRequiredFieldFindingsFromSources({
        goSource: staleProducerSource,
        runtimeSource,
      }),
    ).toEqual(["stale:workspace"]);
  });

  it("exits the real CLI with the exact Sidebar required-field drift", async () => {
    const runtimePath = "frontend-app/src/shared/api/response-validators/runtime/sidebar-state.js";
    const auditScriptPath = "frontend-app/scripts/rpc-contract-audit.mjs";
    const runtimeSource = await readFile(join(REPO_ROOT, runtimePath), "utf8");
    const auditScriptSource = await readFile(join(REPO_ROOT, auditScriptPath), "utf8");
    const missingConsumerSource = runtimeSource.replace("'workspace', ", "");

    expect(missingConsumerSource).not.toBe(runtimeSource);
    const repoRoot = await createShadowRepo({
      [runtimePath]: missingConsumerSource,
      [auditScriptPath]: auditScriptSource,
    });
    await symlink(
      join(REPO_ROOT, "frontend-app/node_modules"),
      join(repoRoot, "frontend-app/node_modules"),
    );

    const canonicalRepoRoot = await realpath(repoRoot);
    const result = spawnSync(process.execPath, [join(canonicalRepoRoot, auditScriptPath)], {
      cwd: join(canonicalRepoRoot, "frontend-app"),
      encoding: "utf8",
    });

    expect(result.stdout).toContain("Sidebar required field drift: 1");
    expect(result.status).toBe(1);
    expect(result.stderr).toContain(
      '\nSidebar required field drift:\n[\n  "missing:workspace"\n]\n',
    );
  });

  it("rejects malformed Sidebar tags and an unreferenced runtime required-field registry", async () => {
    const goSource = await readFile(join(REPO_ROOT, "internal/module/uistate/state.go"), "utf8");
    const runtimeSource = await readFile(
      join(REPO_ROOT, "frontend-app/src/shared/api/response-validators/runtime/sidebar-state.js"),
      "utf8",
    );
    const malformedGoSource = goSource.replace('json:"workspace"', 'yaml:"workspace"');
    const unreferencedRegistrySource = runtimeSource.replace(
      "for (const requiredField of SIDEBAR_REQUIRED_RESPONSE_KEYS)",
      "for (const requiredField of [])",
    );

    expect(() =>
      collectSidebarRequiredFieldFindingsFromSources({
        goSource: malformedGoSource,
        runtimeSource,
      }),
    ).toThrow("Sidebar.Workspace must declare exactly one json tag");
    expect(
      collectSidebarRequiredFieldFindingsFromSources({
        goSource,
        runtimeSource: unreferencedRegistrySource,
      }),
    ).toEqual(["runtime:SIDEBAR_REQUIRED_RESPONSE_KEYS is not used by the required-field check"]);
  });

  it.each(["continue;", "return value;"])(
    "rejects a Sidebar required-field check made unreachable by unconditional %s",
    async (controlTransfer) => {
      const goSource = await readFile(join(REPO_ROOT, "internal/module/uistate/state.go"), "utf8");
      const runtimeSource = await readFile(
      join(REPO_ROOT, "frontend-app/src/shared/api/response-validators/runtime/sidebar-state.js"),
        "utf8",
      );
      const loopHeader = "for (const requiredField of SIDEBAR_REQUIRED_RESPONSE_KEYS) {\n";

      const unreachableCheckSource = runtimeSource.replace(
        loopHeader,
        `${loopHeader}    ${controlTransfer}\n`,
      );
      expect(unreachableCheckSource).not.toBe(runtimeSource);
      expect(
        collectSidebarRequiredFieldFindingsFromSources({
          goSource,
          runtimeSource: unreachableCheckSource,
        }),
      ).toEqual(["runtime:SIDEBAR_REQUIRED_RESPONSE_KEYS is not used by the required-field check"]);
    },
  );

  it("detects frontend and Go hardcoded payload guard sources", () => {
    const findings = collectHardcodedPayloadGuardFindingsFromSources({
      frontendSource: `
        export const RPC_ALLOWED_PAYLOAD_KEYS = new Set([
          'threadId',
        ])
        const THREAD_START_ALLOWED_KEYS = new Set([
          'threadId',
        ])
      `,
      goSources: new Map([
        ["internal/module/thread/rpc_types.go", "var startParamWireFields = map[string]struct{}{}"],
      ]),
    });

    expect(findings).toEqual([
      "frontend-app/src/shared/api/backendApi.js:RPC_ALLOWED_PAYLOAD_KEYS",
      "frontend-app/src/shared/api/backendApi.js:THREAD_START_ALLOWED_KEYS",
      "internal/module/thread/rpc_types.go:startParamWireFields",
    ]);
  });
});
