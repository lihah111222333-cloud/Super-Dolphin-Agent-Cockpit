import unittest

from scripts.cross_file_execution_metrics import (
    ExecutionMetricsError,
    build_execution_metrics,
    build_unavailable_execution_metrics,
)


class CrossFileExecutionMetricsTest(unittest.TestCase):
    def test_records_lsp_availability_adoption_and_tool_calls(self):
        metrics = build_execution_metrics(
            mode="with_lsp",
            prompt="fix issue",
            prompt_sections={
                "task": "fix issue",
                "common_developer_instructions": "common\n",
                "with_lsp_addendum": "## Conditional With-LSP addendum\naddendum\n",
            },
            events=(
                {
                    "type": "mcp_tool_call",
                    "tool": "structure",
                    "call_id": "call-1",
                    "arguments": {},
                },
                {
                    "type": "mcp_tool_call",
                    "tool": "xref",
                    "call_id": "call-2",
                    "arguments": {},
                },
                # A completion event for the same call must not double count.
                {
                    "type": "mcp_tool_result",
                    "tool": "structure",
                    "call_id": "call-1",
                    "result": {},
                },
                {
                    "type": "mcp_tool_call",
                    "tool": "diagnostics",
                    "call_id": "call-3",
                    "arguments": {},
                },
                {
                    "type": "item.started",
                    "item": {
                        "id": "call-4",
                        "type": "mcp_tool_call",
                        "name": "mcp_lsp/structure",
                        "arguments": {},
                    },
                },
            ),
            usage={
                "input_tokens": 100,
                "cached_input_tokens": 30,
                "output_tokens": 20,
                "reasoning_output_tokens": 4,
            },
        )
        self.assertTrue(metrics["lsp_available"])
        self.assertTrue(metrics["adopted"])
        self.assertEqual(metrics["calls"], 4)
        self.assertEqual(metrics["tool"]["by_name"], {"diagnostics": 1, "structure": 2, "xref": 1})
        self.assertEqual(metrics["tokens"]["uncached_input_tokens"], 70)
        self.assertEqual(metrics["tokens"]["cached_input_tokens_90pct"], 27)
        self.assertEqual(metrics["tokens"]["effective_input_tokens"], 97)
        self.assertEqual(metrics["tokens"]["total_tokens"], 124)
        self.assertEqual(metrics["prompt"]["sections"]["task"]["bytes"], len("fix issue"))

    def test_no_lsp_zero_calls_is_valid_non_adoption(self):
        metrics = build_execution_metrics(
            mode="no_lsp",
            prompt="fix issue",
            events=(),
            usage={"input_tokens": 1, "cached_input_tokens": 1, "output_tokens": 0},
        )
        self.assertFalse(metrics["lsp_available"])
        self.assertFalse(metrics["adopted"])
        self.assertEqual(metrics["calls"], 0)
        self.assertEqual(metrics["tokens"]["uncached_input_tokens"], 0)
        self.assertEqual(metrics["tokens"]["cached_input_tokens_90pct"], 0.9)

    def test_missing_usage_is_explicitly_unavailable_not_zero(self):
        metrics = build_unavailable_execution_metrics(
            mode="with_lsp",
            prompt="fix issue",
            reason="stream disconnected",
        )
        self.assertEqual(metrics["status"], "unavailable")
        self.assertEqual(metrics["tokens"], {"status": "unavailable"})
        self.assertEqual(metrics["calls"], 0)

    def test_invalid_cached_count_fails_fast(self):
        with self.assertRaisesRegex(ExecutionMetricsError, "cannot exceed"):
            build_execution_metrics(
                mode="with_lsp",
                prompt="fix issue",
                events=(),
                usage={"input_tokens": 1, "cached_input_tokens": 2, "output_tokens": 0},
            )


if __name__ == "__main__":
    unittest.main()
