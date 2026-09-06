from __future__ import annotations

import copy
import json
import shutil
import tempfile
import unittest
from pathlib import Path

import scripts.orch_acceptance_inventory as inventory


ROOT = Path(__file__).resolve().parents[1]


class AcceptanceInventoryTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        for relative in ("docs/work/proposals/orch-subagent-s0", "docs/work/plans/orch-subagent-upgrade"):
            source = ROOT / relative
            destination = self.root / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copytree(source, destination)
        guard = self.root / "scripts/orch_upgrade_gate_guard_test.go"
        guard.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(ROOT / "scripts/orch_upgrade_gate_guard_test.go", guard)
        self.path = self.root / inventory.INVENTORY_RELATIVE
        self.registry_path = self.root / inventory.REGISTRY_RELATIVE
        self.payload = json.loads(self.path.read_text(encoding="utf-8"))
        self.addCleanup(self.temp.cleanup)

    def write(self, payload) -> None:
        self.path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    def write_registry(self, payload) -> None:
        self.registry_path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    def test_real_inventory_covers_all_tokens_and_blockers_without_authorizing(self) -> None:
        result = inventory.validate_inventory(self.root)
        self.assertEqual(result["authority"], "planning_only")
        self.assertEqual(result["release_decision"], "NO-GO")
        self.assertEqual(len(result["acceptance"]), 51)
        self.assertEqual({item["status"] for item in result["acceptance"]}, {"planned"})
        self.assertEqual({item["id"] for item in result["blockers"]}, set(inventory.BLOCKERS))

    def test_duplicate_token_mapping_fails_closed(self) -> None:
        payload = copy.deepcopy(self.payload)
        payload["acceptance"].append(copy.deepcopy(payload["acceptance"][0]))
        self.write(payload)
        with self.assertRaisesRegex(inventory.InventoryError, "duplicate acceptance"):
            inventory.validate_inventory(self.root)

    def test_missing_path_and_generic_self_reference_fail_closed(self) -> None:
        payload = copy.deepcopy(self.payload)
        payload["acceptance"][0]["evidence"][0]["path"] = "docs/work/plans/orch-subagent-upgrade/missing-fixture.md"
        self.write(payload)
        with self.assertRaisesRegex(inventory.InventoryError, "missing or escapes"):
            inventory.validate_inventory(self.root)

        payload = copy.deepcopy(self.payload)
        payload["acceptance"][0]["evidence"][0]["path"] = inventory.INVENTORY_RELATIVE
        self.write(payload)
        with self.assertRaisesRegex(inventory.InventoryError, "self-reference"):
            inventory.validate_inventory(self.root)

        payload = copy.deepcopy(self.payload)
        payload["acceptance"][0]["evidence"][0]["path"] = (
            "docs/work/plans/orch-subagent-upgrade/./acceptance-inventory.json"
        )
        self.write(payload)
        with self.assertRaisesRegex(inventory.InventoryError, "self-reference"):
            inventory.validate_inventory(self.root)

    def test_acceptance_evidence_must_match_proposal_source_path_and_line(self) -> None:
        payload = copy.deepcopy(self.payload)
        payload["acceptance"][0]["evidence"][0]["line"] += 1
        self.write(payload)
        with self.assertRaisesRegex(inventory.InventoryError, "proposal source mapping"):
            inventory.validate_inventory(self.root)

    def test_proposal_manifest_cannot_be_coordinated_omission(self) -> None:
        proposal = self.root / "docs/work/proposals/orch-subagent-s0/a2a-gateway-spec.md"
        text = proposal.read_text(encoding="utf-8")
        proposal.write_text(text.replace("TestA2AUnknownRemoteOutcomeIsNotRetried", "", 1), encoding="utf-8")
        with self.assertRaisesRegex(inventory.InventoryError, "manifest drift"):
            inventory.validate_inventory(self.root)

    def test_token_line_matching_requires_token_boundaries(self) -> None:
        with self.assertRaisesRegex(inventory.InventoryError, "absent"):
            inventory._line("TestBoundaryExtra", 1, "TestBoundary", "reference")

    def test_status_and_release_decision_cannot_be_promoted(self) -> None:
        payload = copy.deepcopy(self.payload)
        payload["release_decision"] = "GO"
        self.write(payload)
        with self.assertRaisesRegex(inventory.InventoryError, "NO-GO"):
            inventory.validate_inventory(self.root)

        payload = copy.deepcopy(self.payload)
        payload["acceptance"][0]["status"] = "GREEN"
        self.write(payload)
        with self.assertRaisesRegex(inventory.InventoryError, "invalid status"):
            inventory.validate_inventory(self.root)

    def test_blocker_status_drift_is_rejected(self) -> None:
        payload = copy.deepcopy(self.payload)
        payload["blockers"][0]["status"] = "observed"
        self.write(payload)
        with self.assertRaisesRegex(inventory.InventoryError, "status must preserve"):
            inventory.validate_inventory(self.root)

    def test_blocker_registry_requires_full_schema_unique_ids_and_types(self) -> None:
        registry = json.loads(self.registry_path.read_text(encoding="utf-8"))

        malformed = copy.deepcopy(registry)
        malformed["blockers"][0]["title"] = 42
        self.write_registry(malformed)
        with self.assertRaisesRegex(inventory.InventoryError, "title"):
            inventory.validate_inventory(self.root)

        malformed = copy.deepcopy(registry)
        malformed["blockers"][1]["id"] = malformed["blockers"][0]["id"]
        self.write_registry(malformed)
        with self.assertRaisesRegex(inventory.InventoryError, "duplicate blocker registry id"):
            inventory.validate_inventory(self.root)

        malformed = copy.deepcopy(registry)
        malformed["unexpected"] = True
        self.write_registry(malformed)
        with self.assertRaisesRegex(inventory.InventoryError, "fields drift"):
            inventory.validate_inventory(self.root)


if __name__ == "__main__":
    unittest.main()
