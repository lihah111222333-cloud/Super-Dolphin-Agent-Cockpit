import copy
import json
import pathlib
import unittest
from decimal import Decimal

from scripts.cross_file_ab_audit import (
    APPROVED_POLICY,
    CACHE_INPUT_DISCOUNT,
    POLICY_HASH,
    PRICING_MANIFEST_SCHEMA,
    ContractFailure,
    CostError,
    MissingEvidenceError,
    OfficialReportError,
    UnknownFieldError,
    adapt_codex_jsonl,
    audit,
    canonical_evidence_hash,
    canonical_policy_hash,
    _canonical_hash,
)


FIXTURES = pathlib.Path(__file__).with_name("testdata") / "cross_file_ab_audit"

_PRICING_MATERIAL = {
    "schema": PRICING_MANIFEST_SCHEMA,
    "manifest_id": "test-pricing-v1",
    "currency": "USD",
    "input_usd_per_token": "0.0001",
    "output_usd_per_token": "0.0002",
    "cached_input_discount": CACHE_INPUT_DISCOUNT,
}
_PRICING_MANIFEST = {
    **_PRICING_MATERIAL,
    "manifest_hash": _canonical_hash(_PRICING_MATERIAL),
}


def _reseal(payload):
    payload["evidence_hash"] = canonical_evidence_hash(payload)
    return payload


def _identity(question_id, mode, arm_id, *, session=None, patch_hash="patch-common"):
    return {
        "question_id": question_id,
        "arm_id": arm_id,
        "mode": mode,
        "session_id": session or f"session-{question_id}-{mode}",
        "thread_id": f"thread-{question_id}-{mode}",
        "patch_hash": patch_hash,
    }


def _common_identity(question_id):
    return {
        "task": question_id,
        "base": "base-common",
        "source_image": "image-common",
        "model": "model-common",
        "prompt": "prompt-common",
        "policy": POLICY_HASH,
        "grader": "grader-common",
    }


def _rounds(kind="quality", passed=True, number=1):
    return [{"round": number, "kind": kind, "status": "passed" if passed else "failed", "passed": passed}]


def _arm(question_id, mode, arm_id, *, rounds=None, passed=True, cost=0.10, session=None):
    identity = _identity(question_id, mode, arm_id, session=session)
    summary = {
        **identity,
        "schema": "super-dolphin/cross-file-ab-summary/v1",
        "guard_terminal": True,
        "guard_status": "succeeded" if passed else "max_rounds_reached",
        "rounds": rounds or _rounds(),
        "passed": passed,
        "passed_to_grading": passed,
        "common_identity": _common_identity(question_id),
    }
    if mode == "with_lsp":
        summary["changed_files"] = [f"src/{question_id}.py"]
        events = [
            {"type": "run_started", **identity},
            {
                "type": "mcp_tool_call",
                "tool": "structure",
                "arguments": {"file_path": f"src/{question_id}.py"},
                "result": {"locations": [f"src/{question_id}.py:10:2"]},
            },
            {
                "type": "mcp_tool_call",
                "tool": "xref",
                "arguments": {"pos": f"src/{question_id}.py:10:2"},
                "result": {"locations": []},
            },
            {
                "type": "mcp_tool_call",
                "tool": "diagnostics",
                "arguments": {
                    "file_path": f"src/{question_id}.py",
                    "diff_filter": {
                        "mode": "changed_lines",
                        "changed_lines": {f"src/{question_id}.py": [1]},
                    },
                },
                "result": {"status": "clean"},
            },
        ]
    else:
        events = [{"type": "run_started", **identity}]
    grader = {
        **identity,
        "schema": "official-grader/v1",
        "status": "passed" if passed else "failed",
        "passed": passed,
        "common_identity": _common_identity(question_id),
    }
    return {
        "summary": summary,
        "agent_jsonl": "\n".join(json.dumps(event, sort_keys=True) for event in events),
        "grader": grader,
        "cost": {
            "input_tokens": int(round(cost * 10000)),
            "cached_input_tokens": 0,
            "output_tokens": 0,
        },
    }


def representative_fixture():
    shapes = json.loads((FIXTURES / "representative_24_discovery_shapes.json").read_text(encoding="utf-8"))
    questions = []
    for index, item in enumerate(shapes, 1):
        qid = item["question_id"]
        no_rounds = _rounds()
        lsp_rounds = _rounds()
        no_cost, lsp_cost = 0.10, 0.10
        if index == 2:
            lsp_rounds = [*_rounds(passed=False), *_rounds(passed=True, number=2)]
            lsp_cost = 0.20
        if index == 3:
            lsp_rounds = [
                {"round": 1, "kind": "evidence_recovery", "status": "recovered", "passed": True, "recovered": True, "contract_failure": True},
                {"round": 2, "kind": "quality", "status": "passed", "passed": True},
            ]
        if index == 4:
            lsp_rounds = [
                {"round": 1, "kind": "infra_retry", "status": "recovered", "passed": True, "recovered": True, "infrastructure_failure": True},
                {"round": 2, "kind": "quality", "status": "passed", "passed": True},
            ]
        questions.append({
            "question_id": qid,
            "discovery_shape": item["discovery_shape"],
            "arms": {
                "no_lsp": _arm(qid, "no_lsp", f"{qid}-a", rounds=no_rounds, cost=no_cost),
                "with_lsp": _arm(qid, "with_lsp", f"{qid}-b", rounds=lsp_rounds, cost=lsp_cost),
            },
        })
    payload = {
        "schema": "super-dolphin/cross-file-ab-audit/v1",
        "policy": APPROVED_POLICY,
        "policy_hash": POLICY_HASH,
        "pricing_manifest": _PRICING_MANIFEST,
        "questions": questions,
    }
    return _reseal(payload)


class CrossFileABAuditTests(unittest.TestCase):
    def test_representative_24_discovery_shapes_are_complete_and_scoreable(self):
        payload = representative_fixture()
        self.assertEqual(len(payload["questions"]), 24)
        result = audit(payload)
        self.assertEqual(len(result.questions), 24)
        self.assertEqual(result.coverage_ledger["question_count"], 24)
        self.assertEqual(result.coverage_ledger["no_lsp_clean"], 24)
        self.assertEqual(result.coverage_ledger["with_lsp_chain_complete"], 24)
        self.assertEqual(result.questions[1].arms["with_lsp"].quality_score, 7)
        self.assertEqual(result.questions[1].arms["no_lsp"].cost_score, 0)
        self.assertEqual(result.questions[1].arms["with_lsp"].cost_score, 0)
        # Evidence recovery and infrastructure retry do not consume a quality
        # score slot: each is the first successful quality attempt.
        self.assertEqual(result.questions[2].arms["with_lsp"].quality_score, 10)
        self.assertEqual(result.questions[3].arms["with_lsp"].quality_score, 10)

    def test_report_contains_flat_per_question_fields_and_ledger(self):
        report = audit(representative_fixture()).to_dict()
        row = report["questions"][0]
        self.assertIn("pass", row)
        self.assertIn("fail", row)
        self.assertEqual(set(row["round"]), {"no_lsp", "with_lsp"})
        self.assertEqual(set(row["cost"]), {"no_lsp", "with_lsp"})
        self.assertEqual(set(row["score"]), {"no_lsp", "with_lsp"})
        self.assertIn("coverage_ledger", report)
        self.assertIn("leaderboard", report)
        self.assertEqual(
            report["scoring_policy"]["quality_points_by_first_passing_round"],
            [10, 7, 4],
        )
        self.assertFalse(report["scoring_policy"]["cost_points_enabled"])
        self.assertFalse(report["scoring_policy"]["token_usage_scored"])

    def test_no_lsp_rejects_mcp_and_lsp_prompt_marker(self):
        payload = representative_fixture()
        arm = payload["questions"][0]["arms"]["no_lsp"]
        arm["agent_jsonl"] = json.dumps({"type": "prompt", "text": "Use LSP structure now"})
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "No-LSP"):
            audit(payload)

        payload = representative_fixture()
        arm = payload["questions"][0]["arms"]["no_lsp"]
        arm["agent_jsonl"] = json.dumps({"type": "mcp_tool_call", "tool": "diagnostics", "arguments": {}})
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "zero MCP"):
            audit(payload)

    def test_with_lsp_requires_structure_before_xref_and_diff_filter_per_file(self):
        payload = representative_fixture()
        events = [json.loads(line) for line in payload["questions"][0]["arms"]["with_lsp"]["agent_jsonl"].splitlines()]
        events[1], events[2] = events[2], events[1]
        payload["questions"][0]["arms"]["with_lsp"]["agent_jsonl"] = "\n".join(json.dumps(event) for event in events)
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "previous structure"):
            audit(payload)

        payload = representative_fixture()
        arm = payload["questions"][0]["arms"]["with_lsp"]
        arm["summary"]["changed_files"].append("src/other.py")
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "diff_filter missing"):
            audit(payload)

    def test_real_codex_transport_fields_do_not_override_evidence(self):
        payload = representative_fixture()
        arm = payload["questions"][0]["arms"]["with_lsp"]
        events = [json.loads(line) for line in arm["agent_jsonl"].splitlines()]
        events.append({
            "type": "turn.completed",
            "usage": {"input_tokens": 10, "cached_input_tokens": 9, "output_tokens": 1},
        })
        events.append({
            "type": "item.completed",
            "item": {
                "type": "command_execution",
                "aggregated_output": "focused test output",
                "exit_code": 0,
            },
        })
        arm["agent_jsonl"] = "\n".join(json.dumps(event) for event in events)
        _reseal(payload)
        self.assertEqual(len(audit(payload).questions), 24)

    def test_real_codex_transport_partial_identity_and_payload_adapter(self):
        payload = representative_fixture()
        arm = payload["questions"][0]["arms"]["with_lsp"]
        fixture = (FIXTURES / "real_codex_transport.jsonl").read_text(encoding="utf-8").strip()
        arm["agent_jsonl"] = fixture + "\n" + arm["agent_jsonl"]
        _reseal(payload)
        self.assertEqual(len(audit(payload).questions), 24)

        wrapped = json.dumps({
            "type": "response_item",
            "payload": {"type": "thread.started", "thread_id": "thread-q01-with_lsp"},
        })
        adapted = adapt_codex_jsonl(wrapped)
        self.assertEqual(adapted, ({"type": "thread.started", "thread_id": "thread-q01-with_lsp"},))

    def test_partial_or_complete_transport_identity_conflict_fails_fast(self):
        payload = representative_fixture()
        arm = payload["questions"][0]["arms"]["with_lsp"]
        arm["agent_jsonl"] = json.dumps({
            "type": "thread.started",
            "thread_id": "thread-drifted",
        }) + "\n" + arm["agent_jsonl"]
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "identity/hash drifted"):
            audit(payload)

        payload = representative_fixture()
        arm = payload["questions"][0]["arms"]["with_lsp"]
        arm["agent_jsonl"] = json.dumps({
            "type": "thread.started",
            "thread_id": "thread-q01-with_lsp",
            "identity": {"thread_id": "thread-conflicting"},
        }) + "\n" + arm["agent_jsonl"]
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "conflicting aliases"):
            audit(payload)

        payload = representative_fixture()
        arm = payload["questions"][0]["arms"]["with_lsp"]
        identity = _identity("q01", "with_lsp", "q01-b")
        identity["patch_hash"] = "patch-drifted"
        arm["agent_jsonl"] = json.dumps({"type": "run_started", **identity})
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "identity/hash drifted"):
            audit(payload)

    def test_each_arm_requires_strict_identity_envelope(self):
        payload = representative_fixture()
        del payload["questions"][0]["arms"]["with_lsp"]["summary"]["thread_id"]
        _reseal(payload)
        with self.assertRaisesRegex(MissingEvidenceError, "thread_id"):
            audit(payload)

    def test_ab_arms_may_use_different_patches_and_report_file_divergence(self):
        payload = representative_fixture()
        q = payload["questions"][0]
        no = q["arms"]["no_lsp"]
        with_lsp = q["arms"]["with_lsp"]
        for arm, patch_hash in ((no, "patch-no-lsp"), (with_lsp, "patch-with-lsp")):
            arm["summary"]["patch_hash"] = patch_hash
            arm["grader"]["patch_hash"] = patch_hash
            events = [json.loads(line) for line in arm["agent_jsonl"].splitlines()]
            for event in events:
                if "patch_hash" in event:
                    event["patch_hash"] = patch_hash
                if event.get("tool") == "diagnostics":
                    arguments = event["arguments"]
                    arguments.pop("file_path", None)
                    arguments["file_paths"] = ["src/q01.py", "src/with_lsp.py"]
                    arguments["diff_filter"]["changed_lines"] = {
                        "src/q01.py": [1],
                        "src/with_lsp.py": [1],
                    }
            arm["agent_jsonl"] = "\n".join(json.dumps(event, sort_keys=True) for event in events)
        no["summary"]["changed_files"] = ["src/q01.py", "src/no_lsp.py"]
        with_lsp["summary"]["changed_files"] = ["src/q01.py", "src/with_lsp.py"]
        _reseal(payload)
        result = audit(payload)
        row = result.questions[0]
        self.assertFalse(row.patch_equal)
        self.assertEqual(row.patch_hashes, {"no_lsp": "patch-no-lsp", "with_lsp": "patch-with-lsp"})
        self.assertEqual(row.changed_files_overlap, ("src/q01.py",))
        self.assertEqual(row.changed_files_divergence, ("src/no_lsp.py", "src/with_lsp.py"))
        self.assertEqual(row.arms["no_lsp"].patch_hash, "patch-no-lsp")
        self.assertEqual(row.arms["with_lsp"].patch_hash, "patch-with-lsp")

    def test_ab_common_identity_drift_is_rejected_even_with_different_patches(self):
        payload = representative_fixture()
        q = payload["questions"][0]
        for arm, patch_hash in (
            (q["arms"]["no_lsp"], "patch-no-lsp"),
            (q["arms"]["with_lsp"], "patch-with-lsp"),
        ):
            arm["summary"]["patch_hash"] = patch_hash
            arm["grader"]["patch_hash"] = patch_hash
            events = [json.loads(line) for line in arm["agent_jsonl"].splitlines()]
            for event in events:
                if "patch_hash" in event:
                    event["patch_hash"] = patch_hash
            arm["agent_jsonl"] = "\n".join(json.dumps(event, sort_keys=True) for event in events)
        q["arms"]["with_lsp"]["summary"]["common_identity"]["base"] = "base-drifted"
        q["arms"]["with_lsp"]["grader"]["common_identity"]["base"] = "base-drifted"
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "common identity mismatch"):
            audit(payload)

    def test_grader_common_identity_drift_is_rejected(self):
        payload = representative_fixture()
        payload["questions"][0]["arms"]["with_lsp"]["grader"]["common_identity"]["model"] = "model-drifted"
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "common identity drifted"):
            audit(payload)

    def test_missing_arm_duplicate_session_and_id_drift_fail_fast(self):
        payload = representative_fixture()
        del payload["questions"][0]["arms"]["with_lsp"]
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "missing arm"):
            audit(payload)

        payload = representative_fixture()
        q = payload["questions"][0]
        q["arms"]["no_lsp"] = _arm(q["question_id"], "no_lsp", "shared", session="shared-session")
        q["arms"]["with_lsp"] = _arm(q["question_id"], "with_lsp", "other", session="shared-session")
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "duplicate session"):
            audit(payload)

        payload = representative_fixture()
        payload["questions"][0]["arms"]["with_lsp"]["grader"]["patch_hash"] = "drifted"
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "identity/hash drifted"):
            audit(payload)

    def test_cost_fields_unknown_fields_and_official_contradictions_fail_fast(self):
        payload = representative_fixture()
        payload["questions"][0]["arms"]["no_lsp"]["cost"] = {}
        _reseal(payload)
        with self.assertRaises(CostError):
            audit(payload)

        payload = representative_fixture()
        payload["questions"][0]["arms"]["no_lsp"]["summary"]["unknown"] = True
        _reseal(payload)
        with self.assertRaises(UnknownFieldError):
            audit(payload)

        payload = representative_fixture()
        grader = payload["questions"][0]["arms"]["no_lsp"]["grader"]
        grader["status"], grader["passed"] = "failed", True
        _reseal(payload)
        with self.assertRaises(OfficialReportError):
            audit(payload)

    def test_unrecovered_contract_failure_and_passed_to_grading_are_not_success(self):
        payload = representative_fixture()
        arm = payload["questions"][0]["arms"]["with_lsp"]
        arm["summary"]["rounds"] = [{"round": 1, "kind": "quality", "status": "failed", "passed": False, "contract_failure": True}]
        arm["summary"]["guard_status"] = "max_rounds_reached"
        arm["summary"]["passed"] = False
        arm["summary"]["passed_to_grading"] = False
        arm["grader"]["status"] = "failed"
        arm["grader"]["passed"] = False
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "not recovered"):
            audit(payload)

        payload = representative_fixture()
        arm = payload["questions"][0]["arms"]["no_lsp"]
        arm["summary"]["passed_to_grading"] = True
        arm["summary"]["passed"] = False
        arm["summary"]["guard_status"] = "max_rounds_reached"
        arm["grader"]["status"] = "failed"
        arm["grader"]["passed"] = False
        _reseal(payload)
        with self.assertRaises(OfficialReportError):
            audit(payload)

    def test_single_arm_success_has_no_cost_score(self):
        payload = representative_fixture()
        q = payload["questions"][5]
        q["arms"]["no_lsp"] = _arm(q["question_id"], "no_lsp", f"{q['question_id']}-a", passed=False, rounds=[
            {"round": n, "kind": "quality", "status": "failed", "passed": False} for n in range(1, 4)
        ])
        _reseal(payload)
        result = audit(payload)
        self.assertTrue(result.questions[5].arms["with_lsp"].passed)
        self.assertEqual(result.questions[5].arms["with_lsp"].cost_score, 0)
        self.assertEqual(result.questions[5].arms["no_lsp"].cost_score, 0)

    def test_json_output_is_serializable(self):
        rendered = audit(representative_fixture()).json()
        self.assertEqual(json.loads(rendered)["schema"], "super-dolphin/cross-file-ab-audit/v1")

    def test_canonical_integrity_requires_pre_run_policy_and_input_hash(self):
        payload = representative_fixture()
        del payload["policy_hash"]
        with self.assertRaisesRegex(ContractFailure, "policy_hash"):
            audit(payload)

        payload = representative_fixture()
        payload["questions"][0]["discovery_shape"] = "drifted"
        with self.assertRaisesRegex(ContractFailure, "evidence_hash"):
            audit(payload)

        payload = representative_fixture()
        payload["policy"] = copy.deepcopy(APPROVED_POLICY)
        payload["policy"]["quality_points_by_first_passing_round"][0] = 21
        payload["policy_hash"] = canonical_policy_hash(payload["policy"])
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "approved business scoring policy"):
            audit(payload)

    def test_locked_pricing_recomputes_raw_tokens_and_cached_input_at_nine_tenths(self):
        payload = representative_fixture()
        cost = payload["questions"][0]["arms"]["no_lsp"]["cost"]
        cost.update({"input_tokens": 1000, "cached_input_tokens": 100, "output_tokens": 100, "usd": "0.11900"})
        _reseal(payload)
        result = audit(payload)
        self.assertEqual(result.questions[0].arms["no_lsp"].cost_usd, Decimal("0.11900"))
        self.assertEqual(result.questions[0].arms["no_lsp"].cached_input_tokens, 100)

        payload = representative_fixture()
        cost = payload["questions"][0]["arms"]["no_lsp"]["cost"]
        cost["usd"] = "0.12000"
        _reseal(payload)
        with self.assertRaisesRegex(CostError, "recomputation"):
            audit(payload)

        payload = representative_fixture()
        cost = payload["questions"][0]["arms"]["no_lsp"]["cost"]
        cost["cached_input_tokens"] = cost["input_tokens"] + 1
        _reseal(payload)
        with self.assertRaisesRegex(CostError, "cannot exceed"):
            audit(payload)

    def test_active_infra_censored_and_unverified_evidence_is_not_scoreable(self):
        for field, value, expected in (
            ("guard_status", "active", "not scoreable"),
            ("censored", True, "not scoreable"),
            ("verification_status", "unverified", "not scoreable"),
        ):
            payload = representative_fixture()
            payload["questions"][0]["arms"]["no_lsp"]["summary"][field] = value
            _reseal(payload)
            with self.assertRaisesRegex(ContractFailure, expected):
                audit(payload)

        payload = representative_fixture()
        arm = payload["questions"][0]["arms"]["no_lsp"]
        arm["summary"]["guard_status"] = "max_rounds_reached"
        arm["summary"]["rounds"] = [{"round": 1, "kind": "quality", "status": "failed", "passed": False}]
        arm["summary"]["passed"] = False
        arm["summary"]["passed_to_grading"] = False
        arm["grader"]["status"] = "infra"
        arm["grader"]["passed"] = False
        _reseal(payload)
        with self.assertRaisesRegex(ContractFailure, "not scoreable"):
            audit(payload)

    def test_report_exposes_pass_quality_usd_layers_without_statistical_claim(self):
        report = audit(representative_fixture()).to_dict()
        self.assertEqual(set(report["integrity"]), {"policy_hash", "evidence_hash", "pricing_manifest_hash"})
        self.assertEqual(report["scoring_policy"]["policy_hash"], POLICY_HASH)
        self.assertTrue(report["scoring_policy"]["business_points_are_descriptive_only"])
        self.assertFalse(report["statistics"]["eligible"])
        self.assertIn("pass", report["dimensions"]["no_lsp"])
        self.assertIn("quality", report["dimensions"]["no_lsp"])
        self.assertIn("usd", report["dimensions"]["no_lsp"])
        self.assertIn("business_points", report["questions"][0]["arms"]["no_lsp"]["dimensions"])


if __name__ == "__main__":
    unittest.main()
