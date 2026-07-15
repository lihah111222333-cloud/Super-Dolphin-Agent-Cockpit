import importlib.util
import json
import os
import pathlib
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer


SCRIPT_PATH = pathlib.Path(__file__).with_name("dag_m3_dogfood.py")


def load_script():
    spec = importlib.util.spec_from_file_location("dag_m3_dogfood", SCRIPT_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def start_test_http_server(testcase, handler):
    server = HTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    def cleanup():
        server.shutdown()
        thread.join(timeout=5)
        server.server_close()

    testcase.addCleanup(cleanup)
    return server


class M3DogfoodHarnessTest(unittest.TestCase):
    def test_main_ops_build_prompt_template_first_ten_node_dag(self):
        dogfood = load_script()

        ops = dogfood.build_main_ops("agent_m3", "reports/m3-dogfood", cwd="/repo/current")
        add_ops = [op for op in ops if op["op"] == "add_node"]
        update_ops = [op for op in ops if op["op"] == "update_node"]

        self.assertEqual(len(add_ops), 10)
        self.assertEqual(len(update_ops), 0)
        self.assertTrue(all(op["node"]["node_type"] == "agent" for op in add_ops))
        self.assertFalse(any("command_ref" in op["node"] for op in add_ops))
        self.assertTrue(all(op["node"]["config"]["exec"]["cwd"] == "/repo/current" for op in add_ops))
        node_keys = {op["node"]["node_key"] for op in add_ops}
        self.assertTrue(all(
            set(op["node"].get("depends_on", [])) <= node_keys
            for op in add_ops
        ))

        agent_keys = {
            op["node"]["config"]["exec"]["agent_key"]
            for op in add_ops
        }
        self.assertEqual(len(agent_keys), 10)
        self.assertIn("morning_briefer", agent_keys)
        self.assertIn("pr_summarizer", agent_keys)
        self.assertIn("health_reporter", agent_keys)
        self.assertIn("docs-writer", agent_keys)
        self.assertNotIn("paper_summarizer", agent_keys)
        self.assertNotIn("topic_curator", agent_keys)
        self.assertNotIn("trip_briefer", agent_keys)

        pr_summary = next(op for op in add_ops if op["node"]["node_key"] == "pr_summarizer")
        pr_config = pr_summary["node"]["config"]
        self.assertEqual(
            pr_config["outputs"]["to_sharedfile"]["path"],
            "reports/m3-dogfood/pr_summarizer.md",
        )
        self.assertFalse(pr_config["outputs"].get("to_node_result", False))
        self.assertIn(">4KB", pr_config["first_turn"])
        self.assertIn("data_inspector", pr_config["inputs"]["from_nodes"])
        self.assertNotIn("summarization", pr_config["inputs"])

    def test_negative_ops_and_metrics_cover_hard_thresholds(self):
        dogfood = load_script()

        ops = dogfood.build_negative_ops("agent_m3", "reports/m3-dogfood", cwd="/repo/current")
        add = next(op for op in ops if op["op"] == "add_node")
        self.assertFalse(any(op["op"] == "update_node" for op in ops))
        outputs = add["node"]["config"]["outputs"]
        self.assertTrue(outputs["to_node_result"])
        self.assertNotIn("to_sharedfile", outputs)
        self.assertIn(">4KB", add["node"]["config"]["first_turn"])
        self.assertEqual(add["node"]["config"]["exec"]["cwd"], "/repo/current")
        self.assertEqual(add["node"]["config"]["exec"]["agent_key"], "pr_summarizer")

        self.assertEqual(
            dogfood.required_metric_names(),
            ("dispatch_failed_total", "retry_count_per_node"),
        )

    def test_http_url_defaults_to_mcp_endpoint(self):
        dogfood = load_script()

        self.assertEqual(
            dogfood._normalize_mcp_url("http://127.0.0.1:1234"),
            "http://127.0.0.1:1234/mcp",
        )
        self.assertEqual(
            dogfood._normalize_mcp_url("http://127.0.0.1:1234/mcp"),
            "http://127.0.0.1:1234/mcp",
        )

    def test_clients_initialize_empty_tool_scope(self):
        dogfood = load_script()

        http_client = dogfood.HTTPMCPClient("http://127.0.0.1:1234", 1)
        stdio_client = dogfood.StdioMCPClient(["mcp-orch"], ".", {}, 1)

        self.assertEqual(http_client.tool_scope, {})
        self.assertEqual(stdio_client.tool_scope, {})

    def test_stdio_client_rejects_missing_process_after_start(self):
        dogfood = load_script()
        client = dogfood.StdioMCPClient(["mcp-orch"], ".", {}, 1)
        client.start = lambda: None

        with self.assertRaisesRegex(dogfood.MCPError, "process did not start"):
            client.request("initialize")

    def test_stdio_client_rejects_missing_process_pipes(self):
        dogfood = load_script()
        client = dogfood.StdioMCPClient(["mcp-orch"], ".", {}, 1)

        class ProcessWithoutPipes:
            stdin = None
            stdout = None

            @staticmethod
            def poll():
                return None

        client.proc = ProcessWithoutPipes()

        with self.assertRaisesRegex(dogfood.MCPError, "process pipes are unavailable"):
            client.request("initialize")

    def test_http_client_bypasses_environment_proxy_for_local_mcp(self):
        dogfood = load_script()
        server = start_test_http_server(self, FakeMCPHandler)

        set_proxy_env(self, "http://127.0.0.1:1")

        client = dogfood.HTTPMCPClient("http://127.0.0.1:%d" % server.server_port, 5)
        result = client.request("initialize", {"protocolVersion": "2024-11-05"})

        self.assertEqual(result["serverInfo"]["name"], "fake-mcp")

    def test_call_tool_sends_trusted_scope_metadata(self):
        dogfood = load_script()
        client = FakeClient()
        client.tool_scope = {
            "_agentId": "agent_m3",
            "_threadId": "thread_m3",
            "_callId": "call_m3",
            "_cwd": "/repo/current",
            "_workspaceRoots": ["/repo/current"],
        }

        dogfood.call_tool(client, "prompt_list", {"keyword": ""})

        params = client.raw_calls[0]
        self.assertEqual(params["_cwd"], "/repo/current")
        self.assertEqual(params["_agentId"], "agent_m3")
        self.assertEqual(params["arguments"], {"keyword": ""})
        self.assertNotIn("_cwd", params["arguments"])

    def test_call_tool_raises_structured_tool_errors(self):
        dogfood = load_script()
        client = FakeClient(result={
            "success": False,
            "error": "prompt tools require trusted cwd",
            "code": "lsp_unavailable",
            "retryable": True,
        })

        with self.assertRaises(dogfood.MCPError) as cm:
            dogfood.call_tool(client, "prompt_list", {"keyword": ""})

        self.assertIn("prompt_list failed", str(cm.exception))
        self.assertIn("lsp_unavailable", str(cm.exception))
        self.assertIn("trusted cwd", str(cm.exception))

    def test_validate_prompts_accepts_prompt_list_items_envelope(self):
        dogfood = load_script()
        rows = [
            {"agent_key": key, "enabled": True}
            for key in dogfood.REQUIRED_AGENT_KEYS
        ]

        dogfood.validate_prompts({"items": rows, "total": len(rows)})

    def test_metrics_fetch_keeps_environment_proxy_for_remote_url(self):
        dogfood = load_script()
        proxy = start_test_http_server(self, FakeMetricsProxyHandler)
        proxy.requests = 0
        set_proxy_env(self, "http://127.0.0.1:%d" % proxy.server_port)
        args = Args(
            metrics_url="http://example.invalid/metrics",
            allow_missing_metrics=False,
            require_metric_samples=False,
            metrics_family_only=True,
            dag_key="test-dag",
        )

        dogfood.check_metrics(args)

        self.assertEqual(proxy.requests, 1)

    def test_metrics_fetch_bypasses_environment_proxy_for_local_url(self):
        dogfood = load_script()
        server = start_test_http_server(self, FakeMetricsProxyHandler)
        server.requests = 0
        set_proxy_env(self, "http://127.0.0.1:1")
        args = Args(
            metrics_url="http://127.0.0.1:%d/metrics" % server.server_port,
            allow_missing_metrics=False,
            require_metric_samples=False,
            metrics_family_only=True,
            dag_key="test-dag",
        )

        dogfood.check_metrics(args)

        self.assertEqual(server.requests, 1)

    def test_dispatch_ready_nodes_only_assigns_ready_unassigned_nodes(self):
        dogfood = load_script()
        client = FakeClient()

        dispatched = dogfood.dispatch_ready_nodes(client, "dag-a", 42, [
            {"node_key": "ready-empty", "status": "ready", "assigned_to": ""},
            {"node_key": "ready-owned", "status": "ready", "assigned_to": "agent_old"},
            {"node_key": "pending-empty", "status": "pending", "assigned_to": ""},
            {"node_key": "done-empty", "status": "done", "assigned_to": ""},
        ], "agent_m3", verbose=False)

        self.assertEqual(dispatched, ["ready-empty"])
        self.assertEqual(client.calls, [
            ("task_dispatch_node", {
                "dag_key": "dag-a",
                "node_key": "ready-empty",
                "run_id": 42,
                "assigned_to": "agent_m3",
            })
        ])

    def test_negative_failure_reason_must_match_size_cap(self):
        dogfood = load_script()

        good = {
            "node_key": "long_node_result_rejected",
            "result": json.dumps({"kind": "exhausted_retries", "reason": "result exceeds 4KB size cap (4097 > 4096 bytes), configure outputs.to_sharedfile (ADR-006)"}),
        }
        dogfood.assert_size_cap_failure([good])

        bad = {
            "node_key": "long_node_result_rejected",
            "result": json.dumps({"kind": "exhausted_retries", "reason": "transient: launch failed"}),
        }
        with self.assertRaises(dogfood.MCPError):
            dogfood.assert_size_cap_failure([bad])

        too_broad = {
            "node_key": "long_node_result_rejected",
            "result": json.dumps({"kind": "exhausted_retries", "reason": "task asked for >4KB but launch failed"}),
        }
        with self.assertRaises(dogfood.MCPError):
            dogfood.assert_size_cap_failure([too_broad])

    def test_prometheus_parser_distinguishes_families_and_samples(self):
        dogfood = load_script()

        parsed = dogfood.parse_prometheus_metrics("""
# HELP dispatch_failed_total Number of failures.
# TYPE dispatch_failed_total counter
dispatch_failed_total 1
# HELP retry_count_per_node Highest retry.
# TYPE retry_count_per_node counter
retry_count_per_node{dag_key="d",node_key="n"} 3
""")

        self.assertEqual(parsed["dispatch_failed_total"]["samples"], ["dispatch_failed_total 1"])
        self.assertEqual(
            parsed["retry_count_per_node"]["samples"],
            ['retry_count_per_node{dag_key="d",node_key="n"} 3'],
        )

    def test_metric_sample_validation_requires_explicit_retry_probe_dag(self):
        dogfood = load_script()
        families = dogfood.parse_prometheus_metrics("""
dispatch_failed_total 1
retry_count_per_node{dag_key="retry-probe",node_key="n1"} 3
""")

        dogfood.validate_metric_samples(families, "retry-probe")

        current_dag_only = dogfood.parse_prometheus_metrics("""
dispatch_failed_total 1
retry_count_per_node{dag_key="test-dag",node_key="n1"} 3
""")
        with self.assertRaises(dogfood.MCPError):
            dogfood.validate_metric_samples(current_dag_only, "retry-probe")

        zero_dispatch = dogfood.parse_prometheus_metrics("""
dispatch_failed_total 0
retry_count_per_node{dag_key="retry-probe",node_key="n1"} 3
""")
        with self.assertRaises(dogfood.MCPError):
            dogfood.validate_metric_samples(zero_dispatch, "retry-probe")

    def test_strict_metrics_requires_explicit_retry_probe_key(self):
        dogfood = load_script()
        server = start_test_http_server(self, FakeMetricsProxyHandler)
        server.requests = 0
        args = Args(
            metrics_url="http://127.0.0.1:%d/metrics" % server.server_port,
            allow_missing_metrics=False,
            require_metric_samples=True,
            metrics_family_only=False,
            dag_key="test-dag",
        )

        with self.assertRaises(dogfood.MCPError):
            dogfood.check_metrics(args)

    def test_strict_metrics_accepts_explicit_retry_probe_key(self):
        dogfood = load_script()
        server = start_test_http_server(self, FakeMetricsProxyHandler)
        server.requests = 0
        args = Args(
            metrics_url="http://127.0.0.1:%d/metrics" % server.server_port,
            allow_missing_metrics=False,
            require_metric_samples=True,
            metrics_family_only=False,
            dag_key="test-dag",
            retry_metrics_dag_key="test-dag",
        )

        dogfood.check_metrics(args)

        self.assertEqual(server.requests, 1)

    def test_metric_family_check_accepts_retry_collector_without_samples(self):
        dogfood = load_script()
        families = dogfood.parse_prometheus_metrics("""
dispatch_failed_total 0
retry_count_per_node_overflow_total 0
""")

        self.assertEqual(dogfood.missing_required_metric_names(families, require_metric_samples=False), [])
        self.assertEqual(
            dogfood.missing_required_metric_names(families, require_metric_samples=True),
            ["retry_count_per_node"],
        )

    def test_parse_args_requires_metric_samples_by_default(self):
        dogfood = load_script()

        strict = dogfood.parse_args(["--mode", "run"])
        family_only = dogfood.parse_args(["--mode", "run", "--metrics-family-only"])
        forced_strict = dogfood.parse_args(["--mode", "run", "--metrics-family-only", "--require-metric-samples"])

        self.assertTrue(strict.require_metric_samples)
        self.assertFalse(family_only.require_metric_samples)
        self.assertTrue(forced_strict.require_metric_samples)

    def test_parse_args_derives_default_shared_prefix_from_dag_key(self):
        dogfood = load_script()
        old_shared_prefix = os.environ.pop("M3_DOGFOOD_SHARED_PREFIX", None)
        self.addCleanup(restore_env_var, "M3_DOGFOOD_SHARED_PREFIX", old_shared_prefix)

        args = dogfood.parse_args(["--mode", "dry-run", "--dag-key", "m3-dogfood-20260514-130000"])
        explicit = dogfood.parse_args([
            "--mode", "dry-run",
            "--dag-key", "m3-dogfood-20260514-130000",
            "--shared-prefix", "reports/custom",
        ])

        self.assertEqual(args.shared_prefix, "reports/m3-dogfood-20260514-130000")
        self.assertEqual(explicit.shared_prefix, "reports/custom")

    def test_negative_failure_reason_rejects_sharedfile_hint_without_size_cap(self):
        dogfood = load_script()
        bad = {
            "node_key": "long_node_result_rejected",
            "result": json.dumps({"kind": "exhausted_retries", "reason": "configure outputs.to_sharedfile"}),
        }

        with self.assertRaises(dogfood.MCPError):
            dogfood.assert_size_cap_failure([bad])

    def test_wait_for_dag_requires_expected_node_keys_before_terminal_success(self):
        dogfood = load_script()
        client = FakeDAGClient([{
            "run": {"id": 42},
            "nodes": [{"node_key": "only-one", "status": "done"}],
        }])

        with self.assertRaises(dogfood.MCPError):
            dogfood.wait_for_dag(
                client,
                "dag-a",
                "run-a",
                timeout_sec=0.01,
                poll_sec=0,
                assignee="agent_m3",
                expect_failed=False,
                expected_node_keys=["only-one", "missing-node"],
            )

    def test_wait_for_dag_rejects_non_object_detail(self):
        dogfood = load_script()
        client = FakeDAGClient([None])

        with self.assertRaisesRegex(dogfood.MCPError, "unexpected payload shape"):
            dogfood.wait_for_dag(
                client,
                "dag-a",
                "run-a",
                timeout_sec=0.01,
                poll_sec=0,
                assignee="agent_m3",
                expect_failed=False,
            )

    def test_wait_for_dag_accepts_partial_poll_before_full_terminal_success(self):
        dogfood = load_script()
        client = FakeDAGClient([
            {"run": {"id": 42}, "nodes": [{"node_key": "first", "status": "running"}]},
            {"run": {"id": 42}, "nodes": [
                {"node_key": "first", "status": "done"},
                {"node_key": "second", "status": "done"},
            ]},
        ])

        dogfood.wait_for_dag(
            client,
            "dag-a",
            "run-a",
            timeout_sec=1,
            poll_sec=0,
            assignee="agent_m3",
            expect_failed=False,
            expected_node_keys=["first", "second"],
        )

        self.assertEqual(
            [call[1]["name"] for call in client.calls],
            ["task_get_run", "task_get_run"],
        )


class FakeClient:
    def __init__(self, result=None):
        self.calls = []
        self.raw_calls = []
        self.result = {"ok": True} if result is None else result

    def request(self, method, params):
        self.raw_calls.append(params)
        self.calls.append((params["name"], params["arguments"]))
        return {"structuredContent": self.result}


class FakeDAGClient:
    def __init__(self, details):
        self.details = list(details)
        self.calls = []

    def request(self, method, params):
        self.calls.append((method, params))
        if params["name"] == "task_get_run":
            if len(self.details) > 1:
                return {"structuredContent": self.details.pop(0)}
            return {"structuredContent": self.details[0]}
        if params["name"] == "task_dispatch_node":
            return {"structuredContent": {"ok": True}}
        raise AssertionError("unexpected tool %s" % params["name"])


class FakeMCPHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        if length:
            self.rfile.read(length)
        payload = {
            "jsonrpc": "2.0",
            "id": 1,
            "result": {
                "capabilities": {"tools": {}},
                "protocolVersion": "2024-11-05",
                "serverInfo": {"name": "fake-mcp", "version": "test"},
            },
        }
        body = json.dumps(payload).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        return


class FakeMetricsProxyHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.server.requests += 1
        body = b"dispatch_failed_total 1\nretry_count_per_node{dag_key=\"test-dag\",node_key=\"n1\"} 1\n"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        return


class Args:
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)


PROXY_ENV_VARS = (
    "http_proxy",
    "https_proxy",
    "all_proxy",
    "HTTP_PROXY",
    "HTTPS_PROXY",
    "ALL_PROXY",
    "no_proxy",
    "NO_PROXY",
)


def set_proxy_env(testcase, proxy_url):
    old = {name: os.environ.get(name) for name in PROXY_ENV_VARS}
    for name in PROXY_ENV_VARS:
        os.environ.pop(name, None)
    for name in ("http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"):
        os.environ[name] = proxy_url
    testcase.addCleanup(restore_proxy_env, old)


def restore_proxy_env(old):
    for name in PROXY_ENV_VARS:
        if old[name] is None:
            os.environ.pop(name, None)
        else:
            os.environ[name] = old[name]


def restore_env_var(name, value):
    if value is None:
        os.environ.pop(name, None)
    else:
        os.environ[name] = value


if __name__ == "__main__":
    unittest.main()
