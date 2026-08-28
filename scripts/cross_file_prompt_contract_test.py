import hashlib
import json
import pathlib
import tempfile
import unittest

from scripts.cross_file_prompt_contract import (
    ALLOWED_ARM_DIFFERENCES,
    LSP_ADDENDUM_MARKER,
    NATIVE_BASELINE_MARKER,
    PROMPT_CONTRACT_SCHEMA,
    PromptContract,
    PromptContractError,
    load_canonical_prompt,
    load_prompt_contract,
    prompt_identity_material,
    prompt_sha256,
    split_shared_prompt,
    validate_manifest_prompt_contract,
    validate_prompt_pair,
)


def _write_prompt(path, *, suffix=b""):
    payload = (
        b"Shared instructions.\n\n"
        + NATIVE_BASELINE_MARKER.encode("utf-8")
        + b"\n\nNative only.\n\n"
        + LSP_ADDENDUM_MARKER.encode("utf-8")
        + b"\n\nWith-LSP only.\n"
        + suffix
    )
    path.write_bytes(payload)
    return payload


class CrossFilePromptContractTest(unittest.TestCase):
    def test_checked_lsp_addendum_keeps_localization_optional_and_closes_completion_impact(self):
        prompt = (pathlib.Path(__file__).parent.parent / "shared-developer-instructions.md").read_text(
            encoding="utf-8"
        )
        _, addendum = prompt.split(LSP_ADDENDUM_MARKER, 1)
        self.assertIn("During localization, decide autonomously", addendum)
        self.assertIn("there is no target adoption rate", addendum)
        self.assertIn("mandatory static completion check for changed, LSP-supported production code", addendum)
        self.assertIn("A patch-caused changed-line Error or Warning blocks completion", addendum)
        self.assertIn("explicitly record the failed attempt, tool error, and affected files", addendum)
        self.assertIn("Before completion, inspect the stable production diff", addendum)
        self.assertIn("For each changed existing class, function, method, field", addendum)
        self.assertIn("use `xref` from the exact final definition", addendum)
        self.assertIn("a cross-file contract change cannot finish with diagnostics alone", addendum)
        self.assertIn("do not waive the completion-stage `structure` and `diagnostics`", addendum)
        self.assertNotIn("Skip semantic tools", addendum)
        self.assertIn("Discovery remains fully optional and Agent-directed", addendum)
        self.assertNotIn("On a complex task", addendum)
        self.assertNotIn("Prefer semantic tools", addendum)
        self.assertIn("official grader", addendum)

    def test_loads_only_exact_canonical_prompt_hash(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "shared-developer-instructions.md"
            path.write_text("latest prompt\n", encoding="utf-8")
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            self.assertEqual(load_canonical_prompt(path, digest), "latest prompt\n")
            path.write_text("stale prompt\n", encoding="utf-8")
            with self.assertRaisesRegex(PromptContractError, "drifted"):
                load_canonical_prompt(path, digest)

    def test_rejects_old_addendum_even_with_matching_hash(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "lsp-smoke-addendum.md"
            path.write_text("old prompt\n", encoding="utf-8")
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            with self.assertRaisesRegex(PromptContractError, "canonical latest"):
                load_canonical_prompt(path, digest)

    def test_rejects_legacy_55_prompt_even_with_matching_hash(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "55-lsp-basics.md"
            path.write_text("old prompt\n", encoding="utf-8")
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            with self.assertRaisesRegex(PromptContractError, "canonical latest"):
                load_canonical_prompt(path, digest)

    def test_split_preserves_exact_canonical_bytes_and_hashes_each_surface(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "shared-developer-instructions.md"
            payload = _write_prompt(path)
            common, addendum = split_shared_prompt(payload)
            self.assertEqual(common + addendum, payload)
            contract = load_prompt_contract(path, prompt_sha256(payload), "Fix LSP issue")
            self.assertEqual(contract.common_developer_instructions, common)
            self.assertEqual(contract.with_lsp_addendum, addendum)
            self.assertEqual(contract.canonical_prompt_sha256, prompt_sha256(payload))
            self.assertEqual(
                contract.common_developer_instructions_sha256,
                prompt_sha256(common),
            )
            self.assertEqual(
                contract.with_lsp_addendum_sha256,
                prompt_sha256(addendum),
            )

    def test_arm_payload_keeps_task_text_out_of_system_addendum(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "shared-developer-instructions.md"
            payload = _write_prompt(path)
            contract = load_prompt_contract(
                path,
                prompt_sha256(payload),
                "Fix LSP diagnostics issue",
            )
            no_lsp = contract.arm_payload("no_lsp")
            with_lsp = contract.arm_payload("with_lsp")
            self.assertEqual(no_lsp["task_prompt"], with_lsp["task_prompt"])
            self.assertEqual(no_lsp["task_prompt_sha256"], with_lsp["task_prompt_sha256"])
            self.assertNotIn(LSP_ADDENDUM_MARKER, no_lsp["system_prompt"])
            self.assertIn(LSP_ADDENDUM_MARKER, with_lsp["system_prompt"])
            self.assertEqual(
                no_lsp["system_prompt"].encode("utf-8"),
                contract.common_developer_instructions,
            )
            self.assertEqual(
                with_lsp["system_prompt"].encode("utf-8"),
                contract.common_developer_instructions + contract.with_lsp_addendum,
            )
            self.assertEqual(with_lsp["system_overlay"], contract.with_lsp_addendum.decode())
            self.assertEqual(with_lsp["system_overlay_sha256"], contract.with_lsp_addendum_sha256)
            self.assertEqual(
                with_lsp["system_prompt"].count(NATIVE_BASELINE_MARKER),
                1,
            )
            self.assertNotIn(LSP_ADDENDUM_MARKER, no_lsp["task_prompt"])

    def test_serialized_contract_is_strict_and_identity_covers_all_hashes(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "shared-developer-instructions.md"
            payload = _write_prompt(path)
            contract = load_prompt_contract(path, prompt_sha256(payload), "Fix issue")
            serialized = contract.to_dict()
            self.assertEqual(serialized["schema"], PROMPT_CONTRACT_SCHEMA)
            self.assertEqual(serialized["allowed_arm_differences"], list(ALLOWED_ARM_DIFFERENCES))
            self.assertEqual(PromptContract.from_dict(serialized).to_dict(), serialized)
            self.assertEqual(
                prompt_identity_material(contract),
                {
                    "task_prompt_sha256": contract.task_prompt_sha256,
                    "common_developer_instructions_sha256": contract.common_developer_instructions_sha256,
                    "with_lsp_addendum_sha256": contract.with_lsp_addendum_sha256,
                    "canonical_prompt_sha256": contract.canonical_prompt_sha256,
                },
            )
            drifted = dict(serialized)
            drifted["task_prompt"] = "Fix changed issue"
            with self.assertRaisesRegex(PromptContractError, "task_prompt_sha256 drifted"):
                PromptContract.from_dict(drifted)
            unknown = dict(serialized)
            unknown["unexpected"] = True
            with self.assertRaisesRegex(PromptContractError, "unknown field"):
                PromptContract.from_dict(unknown)

    def test_pair_requires_byte_identical_task_prompt_and_allows_only_addendum_difference(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "shared-developer-instructions.md"
            payload = _write_prompt(path)
            first = load_prompt_contract(path, prompt_sha256(payload), "Fix issue")
            second = load_prompt_contract(path, prompt_sha256(payload), "Fix issue")
            validate_prompt_pair(first, second)
            drifted = load_prompt_contract(path, prompt_sha256(payload), "Fix other issue")
            with self.assertRaisesRegex(PromptContractError, "byte-identical"):
                validate_prompt_pair(first, drifted)

    def test_rejects_missing_or_duplicate_conditional_marker(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "shared-developer-instructions.md"
            payload = _write_prompt(path)
            with self.assertRaisesRegex(PromptContractError, "conditional With-LSP marker"):
                split_shared_prompt(payload.replace(
                    LSP_ADDENDUM_MARKER.encode("utf-8"), b"## Other addendum"
                ))
            with self.assertRaisesRegex(PromptContractError, "conditional With-LSP marker"):
                split_shared_prompt(payload + LSP_ADDENDUM_MARKER.encode("utf-8"))

    def test_locked_manifest_binds_canonical_reconstruction_and_per_question_policy(self):
        root = pathlib.Path(__file__).resolve().parent.parent
        manifest_path = root / "manifests" / "cross-file-focus-combined.lock.json"
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        validated = validate_manifest_prompt_contract(
            manifest,
            repository_root=root,
        )
        self.assertEqual(validated["canonical_prompt_sha256"], manifest["prompt_sha256"])
        self.assertEqual(
            validated["allowed_arm_differences"], ["system.with_lsp_addendum"]
        )

        missing = json.loads(json.dumps(manifest))
        del missing["prompt_contract"]["with_lsp_addendum_sha256"]
        with self.assertRaisesRegex(PromptContractError, "missing field"):
            validate_manifest_prompt_contract(missing, repository_root=root)

        drifted = json.loads(json.dumps(manifest))
        drifted["prompt_contract"]["common_developer_instructions_sha256"] = "0" * 64
        with self.assertRaisesRegex(PromptContractError, "common instructions hash drifted"):
            validate_manifest_prompt_contract(drifted, repository_root=root)


if __name__ == "__main__":
    unittest.main()
