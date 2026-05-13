import importlib.util
import json
import pathlib
import unittest


SCRIPT_PATH = pathlib.Path(__file__).with_name("dag_m3_dogfood.py")


def load_script():
    spec = importlib.util.spec_from_file_location("dag_m3_dogfood", SCRIPT_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class M3DogfoodHarnessTest(unittest.TestCase):
    def test_main_ops_build_prompt_template_first_ten_node_dag(self):
        dogfood = load_script()

        ops = dogfood.build_main_ops("agent_m3", "reports/m3-dogfood")
        add_ops = [op for op in ops if op["op"] == "add_node"]
        update_ops = [op for op in ops if op["op"] == "update_node"]

        self.assertEqual(len(add_ops), 10)
        self.assertEqual(len(update_ops), 0)
        self.assertTrue(all(op["node"]["node_type"] == "agent" for op in add_ops))
        self.assertFalse(any("command_ref" in op["node"] for op in add_ops))

        agent_keys = {
            op["node"]["config"]["exec"]["agent_key"]
            for op in add_ops
        }
        self.assertGreaterEqual(len(agent_keys), 3)
        self.assertIn("morning_briefer", agent_keys)
        self.assertIn("paper_summarizer", agent_keys)
        self.assertIn("topic_curator", agent_keys)

        paper = next(op for op in add_ops if op["node"]["node_key"] == "paper_summarizer")
        paper_config = paper["node"]["config"]
        self.assertEqual(
            paper_config["outputs"]["to_sharedfile"]["path"],
            "reports/m3-dogfood/paper_summarizer.md",
        )
        self.assertFalse(paper_config["outputs"].get("to_node_result", False))
        self.assertIn(">4KB", paper_config["first_turn"])
        self.assertIn("topic_curator", paper_config["inputs"]["from_nodes"])
        self.assertNotIn("summarization", paper_config["inputs"])

    def test_negative_ops_and_metrics_cover_hard_thresholds(self):
        dogfood = load_script()

        ops = dogfood.build_negative_ops("agent_m3", "reports/m3-dogfood")
        add = next(op for op in ops if op["op"] == "add_node")
        self.assertFalse(any(op["op"] == "update_node" for op in ops))
        outputs = add["node"]["config"]["outputs"]
        self.assertTrue(outputs["to_node_result"])
        self.assertNotIn("to_sharedfile", outputs)
        self.assertIn(">4KB", add["node"]["config"]["first_turn"])

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

    def test_dispatch_ready_nodes_only_assigns_ready_unassigned_nodes(self):
        dogfood = load_script()
        client = FakeClient()

        dispatched = dogfood.dispatch_ready_nodes(client, "dag-a", [
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

    def test_metric_sample_validation_requires_current_dag_retry(self):
        dogfood = load_script()
        families = dogfood.parse_prometheus_metrics("""
dispatch_failed_total 1
retry_count_per_node{dag_key="test-dag",node_key="n1"} 3
""")

        dogfood.validate_metric_samples(families, "test-dag")

        wrong_dag = dogfood.parse_prometheus_metrics("""
dispatch_failed_total 1
retry_count_per_node{dag_key="other",node_key="n1"} 3
""")
        with self.assertRaises(dogfood.MCPError):
            dogfood.validate_metric_samples(wrong_dag, "test-dag")

        zero_dispatch = dogfood.parse_prometheus_metrics("""
dispatch_failed_total 0
retry_count_per_node{dag_key="test-dag",node_key="n1"} 3
""")
        with self.assertRaises(dogfood.MCPError):
            dogfood.validate_metric_samples(zero_dispatch, "test-dag")


class FakeClient:
    def __init__(self):
        self.calls = []

    def request(self, method, params):
        self.calls.append((params["name"], params["arguments"]))
        return {"structuredContent": {"ok": True}}
        self.assertEqual(
            dogfood._normalize_mcp_url("http://127.0.0.1:1234/mcp"),
            "http://127.0.0.1:1234/mcp",
        )


if __name__ == "__main__":
    unittest.main()
