from __future__ import annotations

import copy
import hashlib
import json
import subprocess
import sys
import unittest
from pathlib import Path

import scripts.orch_acceptance_inventory as inventory
import scripts.orch_s0_executable_receipt as receipt
import scripts.orch_upgrade_gate as gate


ROOT = Path(__file__).resolve().parents[1]


def _valid_receipt() -> dict:
    checked = inventory.validate_inventory(ROOT)
    token_item = checked["acceptance"][0]
    token = token_item["token"]
    source_ref = token_item["evidence"][0]
    source_path = ROOT / source_ref["path"]
    graph = gate.validate_inputs(ROOT)["graph"]
    node = next(node for node in graph["nodes"] if node["document"] == source_ref["path"])
    depends_on = sorted({
        edge["from"] for edge in graph["edges"]
        if edge["to"] == node["id"] and edge["kind"] != "provider_enable"
    })
    index = gate.validate_inputs(ROOT)["spec_execution_index"]
    command = next(command for item in index["specs"] for command in item["commands"] if token in command["acceptance_tokens"])
    runner_path = ROOT / command["runner"]
    canonical = json.dumps(depends_on, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return {
        "schema_version": 1,
        "authority": "planning_only",
        "release_decision": "NO-GO",
        "binding": {
            "token": token,
            "spec_id": node["id"],
            "command_id": command["id"],
            "depends_on": depends_on,
            "dependency_digest": hashlib.sha256(canonical.encode()).hexdigest(),
            "source": {
                "path": source_ref["path"],
                "symbol": source_ref["symbol"],
                "line": source_ref["line"],
                "sha256": hashlib.sha256(source_path.read_bytes()).hexdigest(),
            },
        },
        "execution": {
            "status": "planned",
            "runner_path": command["runner"],
            "argv": command["argv"],
            "command_id": command["id"],
            "expected_exit": command["expected_exit"],
            "expected_status": command["expected_status"],
            "expected_output": command["expected_output"],
            "command_digest": "1" * 64,
            "runner_digest": hashlib.sha256(runner_path.read_bytes()).hexdigest(),
        },
        "authorization": {
            "granted": False,
            "runtime_effect": "none",
            "capability_lease": None,
        },
    }


class ExecutableReceiptTests(unittest.TestCase):
    def test_aggregate_receipt_covers_all_tokens_and_blockers(self) -> None:
        path = ROOT / "docs/work/plans/orch-subagent-upgrade/s0-aggregate-receipt.json"
        aggregate = json.loads(path.read_text(encoding="utf-8"))
        result = receipt.validate_aggregate(ROOT, aggregate)
        self.assertEqual(result["release_decision"], "NO-GO")
        self.assertEqual(len(result["receipts"]), 51)

    def test_aggregate_missing_token_fails_closed(self) -> None:
        path = ROOT / "docs/work/plans/orch-subagent-upgrade/s0-aggregate-receipt.json"
        aggregate = json.loads(path.read_text(encoding="utf-8"))
        aggregate["receipts"].pop()
        aggregate["acceptance_tokens"] = aggregate["acceptance_tokens"][:-1]
        with self.assertRaisesRegex(receipt.ReceiptError, "51 acceptance tokens"):
            receipt.validate_aggregate(ROOT, aggregate)

    def test_cli_aggregate_only(self) -> None:
        command = [sys.executable, str(ROOT / "scripts/orch_s0_executable_receipt.py"), "--root", str(ROOT), "--aggregate-only", "--aggregate", "docs/work/plans/orch-subagent-upgrade/s0-aggregate-receipt.json"]
        completed = subprocess.run(command, capture_output=True, text=True, check=False)
        self.assertEqual(completed.returncode, 0, completed.stderr)
    def test_valid_receipt_is_non_authorizing(self) -> None:
        result = receipt.validate_receipt(ROOT, _valid_receipt())
        self.assertEqual(result["authority"], "planning_only")
        self.assertFalse(result["authorization"]["granted"])

    def test_source_or_dependency_drift_fails_closed(self) -> None:
        payload = _valid_receipt()
        payload["binding"]["source"]["sha256"] = "f" * 64
        with self.assertRaisesRegex(receipt.ReceiptError, "sha256"):
            receipt.validate_receipt(ROOT, payload)

        payload = _valid_receipt()
        payload["binding"]["depends_on"] = payload["binding"]["depends_on"] + ["TRUST"]
        with self.assertRaisesRegex(receipt.ReceiptError, "depends_on"):
            receipt.validate_receipt(ROOT, payload)

    def test_authorization_fields_cannot_be_promoted(self) -> None:
        for field, value in (("granted", True), ("runtime_effect", "execute"), ("capability_lease", "lease-1")):
            payload = copy.deepcopy(_valid_receipt())
            payload["authorization"][field] = value
            with self.subTest(field=field):
                with self.assertRaisesRegex(receipt.ReceiptError, "authorization"):
                    receipt.validate_receipt(ROOT, payload)

    def test_duplicate_json_key_is_rejected(self) -> None:
        raw = json.dumps(_valid_receipt(), ensure_ascii=False)
        raw = raw.replace('"schema_version": 1,', '"schema_version": 1, "schema_version": 1,', 1)
        with self.assertRaisesRegex(receipt.ReceiptError, "duplicate JSON key"):
            receipt.validate_receipt(ROOT, raw)

    def test_runner_cannot_be_a_planning_input_or_validator(self) -> None:
        payload = _valid_receipt()
        payload["execution"]["runner_path"] = "docs/work/plans/orch-subagent-upgrade/acceptance-inventory.json"
        with self.assertRaisesRegex(receipt.ReceiptError, "self-reference"):
            receipt.validate_receipt(ROOT, payload)

    def test_placeholder_command_digest_is_rejected(self) -> None:
        payload = _valid_receipt()
        payload["execution"]["command_digest"] = "0" * 64
        with self.assertRaisesRegex(receipt.ReceiptError, "placeholder"):
            receipt.validate_receipt(ROOT, payload)


if __name__ == "__main__":
    unittest.main()
