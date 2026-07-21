import { fileURLToPath } from "node:url";
import {
  createUITestMCPServer,
  loadUITestContract,
  runStdioMCPServer,
} from "./ui-test-mcp-runtime.mjs";

export { browserLaunchOptions } from "./ui-test-mcp-runtime.mjs";
export { validateBaseURL } from "./ui-test-mcp-protocol.mjs";
export { createToolDefinitions } from "./ui-test-mcp-tools.mjs";
export { createUITestMCPServer, loadUITestContract, runStdioMCPServer };

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  runStdioMCPServer().catch((error) => {
    process.stderr.write(`[ui-test-mcp] ${error.message}\n`);
    process.exitCode = 1;
  });
}
