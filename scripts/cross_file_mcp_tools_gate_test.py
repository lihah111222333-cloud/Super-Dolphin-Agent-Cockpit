import pathlib
import tempfile
import textwrap
import unittest

from scripts.cross_file_mcp_tools_gate import MCPToolsGateError, verify_tools


class MCPToolsGateTest(unittest.TestCase):
    def _server(self, directory: pathlib.Path, names: tuple[str, ...]) -> pathlib.Path:
        path = directory / "server.py"
        path.write_text(textwrap.dedent(f"""\
            #!/usr/bin/env python3
            import json, sys
            for line in sys.stdin:
                request = json.loads(line)
                if "id" not in request:
                    continue
                if request["method"] == "initialize":
                    result = {{"protocolVersion": "2024-11-05", "capabilities": {{}}}}
                else:
                    result = {{"tools": [{{"name": name, "inputSchema": {{"type": "object"}}}} for name in {names!r}]}}
                print(json.dumps({{"jsonrpc": "2.0", "id": request["id"], "result": result}}), flush=True)
        """), encoding="utf-8")
        path.chmod(0o755)
        return path

    def test_exact_three_tools_pass(self):
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            evidence = verify_tools(
                str(self._server(root, ("structure", "xref", "diagnostics"))),
                root, root / "stderr", 2,
            )
            self.assertEqual(evidence["tools"], ["diagnostics", "structure", "xref"])

    def test_missing_tool_fails_closed(self):
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            with self.assertRaisesRegex(MCPToolsGateError, "tools/list mismatch"):
                verify_tools(
                    str(self._server(root, ("structure", "xref"))),
                    root, root / "stderr", 2,
                )


if __name__ == "__main__":
    unittest.main()
