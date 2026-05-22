import importlib.util
import json
import os
import pathlib
import socket
import stat
import tempfile
import threading
import unittest
import unittest.mock


SCRIPT_PATH = pathlib.Path(__file__).with_name("prompt_intent_e2e.py")


def load_script():
    spec = importlib.util.spec_from_file_location("prompt_intent_e2e", SCRIPT_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def start_test_rpc_server(testcase, responses):
    listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    listener.bind(("127.0.0.1", 0))
    listener.listen(1)

    def serve_once():
        conn, _ = listener.accept()
        with conn:
            data = b""
            while not data.endswith(b"\n"):
                chunk = conn.recv(4096)
                if not chunk:
                    return
                data += chunk
            for response in responses:
                line = json.dumps(response, separators=(",", ":")).encode("utf-8") + b"\n"
                conn.sendall(line)

    thread = threading.Thread(target=serve_once, daemon=True)
    thread.start()

    def cleanup():
        listener.close()
        thread.join(timeout=5)

    testcase.addCleanup(cleanup)
    return listener.getsockname()


class PromptIntentE2ETest(unittest.TestCase):
    def test_json_rpc_client_ignores_notifications_until_matching_response_id(self):
        script = load_script()
        host, port = start_test_rpc_server(
            self,
            [
                {"jsonrpc": "2.0", "method": "agent/launched", "params": {"agent_id": "a1"}},
                {"jsonrpc": "2.0", "id": 999, "result": {"ignored": True}},
                {"jsonrpc": "2.0", "id": 1, "result": {"thread_id": "t1"}},
            ],
        )

        client = script.JSONRPCClient("%s:%s" % (host, port), timeout=1)

        self.assertEqual(client.request("thread/start", {"cwd": "/tmp"}), {"thread_id": "t1"})

    def test_json_rpc_client_fails_fast_when_connection_closes_before_matching_response(self):
        script = load_script()
        host, port = start_test_rpc_server(
            self,
            [{"jsonrpc": "2.0", "method": "agent/launched", "params": {"agent_id": "a1"}}],
        )
        client = script.JSONRPCClient("%s:%s" % (host, port), timeout=1)

        with self.assertRaisesRegex(script.E2EError, "closed before response id 1"):
            client.request("thread/start", {})

    def test_run_psql_scalar_sends_sql_on_stdin_and_preserves_variables(self):
        script = load_script()
        sql = "SELECT prompt_snapshot FROM agent_threads WHERE thread_id = :'thread_id';"

        with tempfile.TemporaryDirectory() as tmp:
            tmp_path = pathlib.Path(tmp)
            capture_path = tmp_path / "capture.json"
            fake_psql = tmp_path / "psql"
            fake_psql.write_text(
                """#!/usr/bin/env python3
import json
import os
import sys

capture = {
    "argv": sys.argv,
    "stdin": sys.stdin.read(),
}
with open(os.environ["PSQL_CAPTURE"], "w", encoding="utf-8") as fh:
    json.dump(capture, fh, sort_keys=True)
print("scalar-value")
""",
                encoding="utf-8",
            )
            fake_psql.chmod(fake_psql.stat().st_mode | stat.S_IXUSR)
            path = "%s%s%s" % (tmp_path, os.pathsep, os.environ.get("PATH", ""))

            with unittest.mock.patch.dict(os.environ, {"PATH": path, "PSQL_CAPTURE": str(capture_path)}):
                got = script.run_psql_scalar(
                    "postgres://user:pass@127.0.0.1/db",
                    sql,
                    {"thread_id": "thread-123"},
                )

            self.assertEqual(got, "scalar-value")
            capture = json.loads(capture_path.read_text(encoding="utf-8"))
            self.assertNotIn("-c", capture["argv"])
            self.assertEqual(capture["stdin"], sql)
            self.assertIn("-v", capture["argv"])
            self.assertIn("thread_id=thread-123", capture["argv"])

    def test_fixture_health_must_match_local_fixture_hash(self):
        script = load_script()
        with tempfile.TemporaryDirectory() as tmp:
            fixture_path = pathlib.Path(tmp) / "fixture.json"
            fixture_path.write_text('{"health":{"provider":"e2e-fixture"}}', encoding="utf-8")
            expected_hash = script.fixture_content_hash(fixture_path)

            script.assert_fixture_health(
                {"provider": "e2e-fixture", "fixture_path_hash": expected_hash},
                fixture_path,
            )
            with self.assertRaisesRegex(script.E2EError, "fixture hash mismatch"):
                script.assert_fixture_health(
                    {"provider": "e2e-fixture", "fixture_path_hash": "not-this-fixture"},
                    fixture_path,
                )

    def test_parse_args_generates_and_validates_run_id(self):
        script = load_script()
        args = script.parse_args([
            "--rpc-addr", "127.0.0.1:18099",
            "--mcp-url", "http://127.0.0.1:1/mcp",
            "--database-url", "postgres://example/db",
            "--dream-fixture", "/tmp/fixture.json",
            "--cwd-a", "/tmp/a",
            "--cwd-b", "/tmp/b",
            "--run-id", "r123-ab",
        ])
        self.assertEqual(args.run_id, "r123-ab")
        with self.assertRaisesRegex(script.E2EError, "--run-id"):
            script.parse_args([
                "--rpc-addr", "127.0.0.1:18099",
                "--mcp-url", "http://127.0.0.1:1/mcp",
                "--database-url", "postgres://example/db",
                "--dream-fixture", "/tmp/fixture.json",
                "--cwd-a", "/tmp/a",
                "--cwd-b", "/tmp/b",
                "--run-id", "bad_id",
            ])

    def test_assert_rendered_run_id_rejects_missing_or_unrendered_placeholder(self):
        script = load_script()
        script.assert_rendered_run_id("prompt-intent-e2e-r123", "r123", "recall_topic")
        with self.assertRaisesRegex(script.E2EError, "does not contain run id"):
            script.assert_rendered_run_id("prompt-intent-e2e-old", "r123", "recall_topic")
        with self.assertRaisesRegex(script.E2EError, "unrendered run id placeholder"):
            script.assert_rendered_run_id("prompt-intent-e2e-{{RUN_ID}} r123", "r123", "recall_topic")

    def test_assert_recall_catalog_scope_rejects_other_cwd_topic_leak(self):
        script = load_script()
        same_snapshot = "recall_catalog\nprompt-intent-e2e-r123\nmetadata only"
        other_snapshot = "recall_catalog\nother project metadata"

        script.assert_same_cwd_recall_catalog(same_snapshot, "prompt-intent-e2e-r123")
        script.assert_other_cwd_recall_catalog(other_snapshot, "prompt-intent-e2e-r123")
        with self.assertRaisesRegex(script.E2EError, "unexpectedly contains marker"):
            script.assert_other_cwd_recall_catalog(same_snapshot, "prompt-intent-e2e-r123")

    def test_assert_external_recall_snapshot_rejects_default_rule_and_body_leak(self):
        script = load_script()
        safe_snapshot = "recall_catalog\nexternal-provider-notes-r123\navailable_experts\nmain/expert/sqlc"

        script.assert_external_recall_snapshot(
            safe_snapshot,
            "external-provider-notes-r123",
            recall_body="EXTERNAL_PROVIDER_RECALL_BODY_MARKER",
            prompt_key="main/knowledge/external-provider-notes-r123",
        )
        with self.assertRaisesRegex(script.E2EError, "unexpectedly contains marker"):
            script.assert_external_recall_snapshot(
                "recall_catalog\nproject_default_rules\nexternal-provider-notes-r123",
                "external-provider-notes-r123",
                recall_body="EXTERNAL_PROVIDER_RECALL_BODY_MARKER",
                prompt_key="main/knowledge/external-provider-notes-r123",
            )
        with self.assertRaisesRegex(script.E2EError, "unexpectedly contains marker"):
            script.assert_external_recall_snapshot(
                "recall_catalog\nexternal-provider-notes-r123\nEXTERNAL_PROVIDER_RECALL_BODY_MARKER",
                "external-provider-notes-r123",
                recall_body="EXTERNAL_PROVIDER_RECALL_BODY_MARKER",
                prompt_key="main/knowledge/external-provider-notes-r123",
            )
        with self.assertRaisesRegex(script.E2EError, "unexpectedly contains marker"):
            script.assert_external_recall_snapshot(
                "recall_catalog\nexternal-provider-notes-r123\navailable_experts\nmain/knowledge/external-provider-notes-r123",
                "external-provider-notes-r123",
                recall_body="EXTERNAL_PROVIDER_RECALL_BODY_MARKER",
                prompt_key="main/knowledge/external-provider-notes-r123",
            )


if __name__ == "__main__":
    unittest.main()
