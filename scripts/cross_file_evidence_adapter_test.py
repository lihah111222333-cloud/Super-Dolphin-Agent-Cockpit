import copy
import hashlib
import json
import pathlib
import unittest

from scripts import cross_file_ab_audit as audit_module
from scripts import cross_file_evidence_adapter as adapter_module
from scripts.cross_file_identity_envelope import (
    IDENTITY_COMPONENTS,
    build_identity_envelope,
    sha256_text,
)
from scripts.cross_file_lsp_receipts import (
    ARM_EVIDENCE_SCHEMA,
    ISOLATION_SCHEMA,
    SCHEMA as RECEIPT_SCHEMA,
    canonical_isolation_attestation_payload,
    digest_json,
    digest_text,
)
from scripts.cross_file_signed_receipts import (
    Ed25519Signer,
    attach_signature,
    sign_grader_receipt,
)
from scripts.grader_feedback import GraderFeedback, Status


ROOT = pathlib.Path(__file__).parent
SIGNER = Ed25519Signer.generate("adapter-key-01")
GRADER_ARTIFACT = {"id": "grader-v1", "sha256": "b" * 64}


class CrossFileEvidenceAdapterTest(unittest.TestCase):
    def setUp(self):
        self.audit_module = audit_module
        self.adapter = adapter_module

    def _bindings(self, *, task_id: str, session: str, thread: str, agent: str) -> dict:
        values = {}
        for name in IDENTITY_COMPONENTS:
            if name == "base":
                values[name] = {
                    "commit_sha": "a" * 40,
                    "tree_sha": "b" * 40,
                    "sha256": sha256_text(f"base-{session}"),
                }
            elif name == "final_tree":
                values[name] = {"tree_sha": "c" * 40, "sha256": sha256_text(f"tree-{session}")}
            else:
                values[name] = {"id": f"{name}-v1", "sha256": sha256_text(f"{name}-{session}")}
        values["task"]["id"] = task_id
        values["session"]["id"] = session
        values["thread"]["id"] = thread
        values["agent"]["id"] = agent
        values["patch"]["id"] = "d" * 64
        values["toolchain"]["id"] = "toolchain-v1"
        values["pricing"]["id"] = "fixture-pricing-v1"
        return values

    def _identity_envelope(self, mode: str, *, task_id: str, suffix: str = ""):
        session = f"{suffix}session-{mode}"
        thread = f"{suffix}thread-{mode}"
        agent = f"{suffix}{mode}-agent"
        return build_identity_envelope(
            self._bindings(task_id=task_id, session=session, thread=thread, agent=agent),
            receipt_signing_key=SIGNER.public_key.to_dict(),
        )

    @staticmethod
    def _audit_identity(envelope, arm_id: str, mode: str):
        bindings = envelope["bindings"]
        return {
            "question_id": bindings["task"]["id"],
            "arm_id": arm_id,
            "mode": mode,
            "session_id": bindings["session"]["id"],
            "thread_id": bindings["thread"]["id"],
            "patch_hash": bindings["patch"]["id"],
            "identity_hash": envelope["binding_sha256"],
        }

    def _runner_events(self, identity, code_agent_id):
        common = {
            "arm_id": identity["arm_id"],
            "question_id": identity["question_id"],
            "session_id": identity["session_id"],
            "thread_id": identity["thread_id"],
            "code_agent_id": code_agent_id,
            "patch_hash": identity["patch_hash"],
            "scoreable": True,
        }
        return [
            {
                "sequence": 1,
                "kind": "quality_round",
                "round_number": 1,
                "attempt": 0,
                "status": "active",
                "decision": "grader_allowed",
                **common,
            },
            {
                "sequence": 2,
                "kind": "grader_result",
                "round_number": 1,
                "attempt": 1,
                "status": "succeeded",
                "decision": "grader_allowed",
                "grader_started": True,
                "grader_passed": True,
                "quality_round_consumed": True,
                "parent_sequence": 1,
                **common,
            },
        ]

    @staticmethod
    def _receipt(
        seq,
        tool,
        arguments,
        result,
        *,
        task,
        arm,
        session,
        thread,
        patch_hash,
        final_tree_hash,
    ):
        payload = {
            "schema": RECEIPT_SCHEMA,
            "source": "mcp-server",
            "task": task,
            "arm": arm,
            "session": session,
            "thread": thread,
            "seq": seq,
            "request_id": f"request-{seq}",
            "args_hash": digest_json(arguments),
            "result_hash": digest_json(result),
            "status": "success",
            "server_generation": "generation-01",
            "patch_hash": patch_hash,
            "final_tree_hash": final_tree_hash,
            "tool": tool,
            "arguments": arguments,
            "result": result,
        }
        return attach_signature(
            payload,
            signer=SIGNER,
            kind="mcp_server_receipt",
            issuer="mcp-server",
        )

    def _canonical_arm_evidence(self, envelope, mode: str, arm_id: str):
        bindings = envelope["bindings"]
        task = bindings["task"]["id"]
        session = bindings["session"]["id"]
        thread = bindings["thread"]["id"]
        patch_hash = bindings["patch"]["id"]
        final_tree_hash = bindings["final_tree"]["tree_sha"]
        if mode == "with_lsp":
            path = "src/shared.py"
            receipts = [
                self._receipt(
                    1,
                    "structure",
                    {"action": "document_symbol", "file_path": path},
                    {"locations": [f"{path}:10:2"]},
                    task=task,
                    arm=arm_id,
                    session=session,
                    thread=thread,
                    patch_hash=patch_hash,
                    final_tree_hash=final_tree_hash,
                ),
                self._receipt(
                    2,
                    "xref",
                    {"action": "references", "pos": f"{path}:10:2"},
                    {"locations": []},
                    task=task,
                    arm=arm_id,
                    session=session,
                    thread=thread,
                    patch_hash=patch_hash,
                    final_tree_hash=final_tree_hash,
                ),
                self._receipt(
                    3,
                    "diagnostics",
                    {
                        "file_paths": [path],
                        "diff_filter": {
                            "mode": "changed_lines",
                            "changed_lines": {path: [10]},
                        },
                    },
                    {"rows": [], "status": "clean"},
                    task=task,
                    arm=arm_id,
                    session=session,
                    thread=thread,
                    patch_hash=patch_hash,
                    final_tree_hash=final_tree_hash,
                ),
            ]
            return {
                "schema": ARM_EVIDENCE_SCHEMA,
                "mode": mode,
                "task": task,
                "arm": arm_id,
                "session": session,
                "thread": thread,
                "receipts": receipts,
                "isolation": None,
            }
        empty = []
        policy_hash = hashlib.sha256(f"{mode}-policy".encode()).hexdigest()
        isolation = {
            "schema": ISOLATION_SCHEMA,
            "mode": "no_lsp",
            "task": task,
            "arm": arm_id,
            "session": session,
            "thread": thread,
            "issuer": "executor",
            "patch_hash": patch_hash,
            "final_tree_hash": final_tree_hash,
            "network": {"disabled": True, "status": "enforced", "policy_hash": policy_hash},
            "sidecar": {"disabled": True, "status": "enforced", "policy_hash": policy_hash},
            "ingress": {"count": 0, "receipts": empty, "log_hash": digest_json(empty)},
            "raw_prompt": "fix the task",
            "raw_prompt_hash": digest_text("fix the task"),
        }
        isolation["attestation_hash"] = digest_json(
            canonical_isolation_attestation_payload(isolation)
        )
        isolation = attach_signature(
            isolation,
            signer=SIGNER,
            kind="no_lsp_isolation",
            issuer="executor",
        )
        return {
            "schema": ARM_EVIDENCE_SCHEMA,
            "mode": mode,
            "task": task,
            "arm": arm_id,
            "session": session,
            "thread": thread,
            "receipts": [],
            "isolation": isolation,
        }

    def _feedback(self, identity):
        feedback = GraderFeedback(
            schema="super-dolphin/grader-feedback/v1",
            status=Status.PASSED.value,
            failure_kind="",
            question_id=identity["question_id"],
            arm_id=identity["arm_id"],
            session_id=identity["session_id"],
            thread_id=identity["thread_id"],
            patch_hash=identity["patch_hash"],
            structured=True,
            passed=True,
        ).to_dict()
        return {
            "receipt": sign_grader_receipt(
                feedback,
                task=identity["question_id"],
                arm=identity["arm_id"],
                session=identity["session_id"],
                thread=identity["thread_id"],
                seq=1,
                request_id=f"grader-{identity['arm_id']}",
                patch_hash=identity["patch_hash"],
                grader_artifact=GRADER_ARTIFACT,
                signer=SIGNER,
            )
        }

    @staticmethod
    def _official_source(identity, schema):
        return json.dumps({
            "schema": schema,
            "status": "PASSED",
            "identity": {
                key: identity[key]
                for key in ("question_id", "arm_id", "session_id", "thread_id", "patch_hash")
            },
        }, sort_keys=True)

    def fixture(self, *, task_id: str = "django__django-16560", suffix: str = ""):
        pricing_material = {
            "schema": self.audit_module.PRICING_MANIFEST_SCHEMA,
            "manifest_id": "fixture-pricing-v1",
            "currency": "USD",
            "input_usd_per_token": "0.0001",
            "output_usd_per_token": "0.0002",
            "cached_input_discount": "0.9",
        }
        pricing = {
            **pricing_material,
            "manifest_hash": self.audit_module._canonical_hash(pricing_material),
        }
        arms = {}
        for mode, arm_id in (("no_lsp", "no-lsp"), ("with_lsp", "with-lsp")):
            envelope = self._identity_envelope(mode, task_id=task_id, suffix=suffix)
            identity = self._audit_identity(envelope, arm_id, mode)
            surface_envelopes = {
                name: copy.deepcopy(envelope)
                for name in ("runner_events", "arm_evidence", "grader_feedback", "usage")
            }
            arms[mode] = {
                "mode": mode,
                "arm_id": arm_id,
                "code_agent_id": envelope["bindings"]["agent"]["id"],
                "server_generation": "generation-01",
                "identity_envelope": envelope,
                "identity_envelopes": surface_envelopes,
                "runner_events": {
                    "schema_version": self.adapter.RUNNER_EVENT_SCHEMA_VERSION,
                    "events": self._runner_events(identity, envelope["bindings"]["agent"]["id"]),
                },
                "arm_evidence": self._canonical_arm_evidence(envelope, mode, arm_id),
                "grader_feedback": self._feedback(identity),
                "official_report": self._official_source(
                    identity, "super-dolphin/grader-feedback/v1"
                ),
                "official_test_output": self._official_source(
                    identity, "super-dolphin/grader-feedback/v1"
                ),
                "usage": {
                    "input_tokens": 1000,
                    "cached_input_tokens": 100,
                    "output_tokens": 100,
                },
                "pricing": {"manifest_hash": pricing["manifest_hash"]},
                "changed_files": ["src/shared.py"],
            }
        return {
            "schema": self.adapter.ADAPTER_SCHEMA,
            "run_id": "adapter-e2e-v2",
            "question_id": task_id,
            "discovery_shape": "cross_file",
            "pricing_manifest": pricing,
            "arms": arms,
        }

    def test_real_17_component_identity_and_canonical_arm_evidence_e2e(self):
        payload = self.fixture()
        self.assertEqual(len(payload["arms"]["no_lsp"]["identity_envelope"]["bindings"]), 17)
        self.assertNotEqual(
            payload["arms"]["with_lsp"]["identity_envelope"]["bindings"]["toolchain"]["id"],
            payload["arms"]["with_lsp"]["server_generation"],
        )
        canonical = self.adapter.build_canonical_input(payload)
        result = self.adapter.audit_evidence(payload)
        self.assertEqual(canonical["schema"], self.audit_module.SCHEMA)
        self.assertEqual(len(result.questions), 1)
        self.assertTrue(result.questions[0].passed)
        self.assertEqual(result.coverage_ledger["with_lsp_chain_complete"], 1)
        self.assertEqual(result.coverage_ledger["no_lsp_clean"], 1)

    def test_multi_question_build_and_audit_uses_one_policy_and_pricing_manifest(self):
        first = self.fixture()
        second = self.fixture(task_id="django__django-16560-q2", suffix="q2-")
        payload = {
            "schema": self.adapter.ADAPTER_SCHEMA,
            "run_id": "adapter-multi-v1",
            "pricing_manifest": first["pricing_manifest"],
            "questions": [
                {
                    "question_id": first["question_id"],
                    "discovery_shape": first["discovery_shape"],
                    "arms": first["arms"],
                },
                {
                    "question_id": second["question_id"],
                    "discovery_shape": second["discovery_shape"],
                    "arms": second["arms"],
                },
            ],
        }
        result = self.adapter.audit_evidence_many(payload)
        self.assertEqual(len(result.questions), 2)
        self.assertTrue(all(question.passed for question in result.questions))

        duplicate_source = self.fixture(task_id="django__django-16560-q2")
        duplicate = copy.deepcopy(payload)
        duplicate["questions"][1]["arms"] = duplicate_source["arms"]
        with self.assertRaisesRegex(self.audit_module.ContractFailure, "duplicate session_id"):
            self.adapter.build_canonical_inputs(duplicate)

    def test_runner_sequence_parent_and_terminal_rules_are_fail_fast(self):
        payload = self.fixture()
        events = payload["arms"]["no_lsp"]["runner_events"]["events"]
        events[1]["sequence"] = 3
        with self.assertRaisesRegex(self.audit_module.ContractFailure, "contiguous"):
            self.adapter.build_canonical_input(payload)

        payload = self.fixture()
        payload["arms"]["no_lsp"]["runner_events"]["events"][1]["parent_sequence"] = 2
        with self.assertRaisesRegex(self.audit_module.ContractFailure, "parent must precede"):
            self.adapter.build_canonical_input(payload)

        payload = self.fixture()
        payload["arms"]["no_lsp"]["runner_events"]["events"][0]["status"] = "succeeded"
        with self.assertRaisesRegex(self.audit_module.ContractFailure, "terminal runner status"):
            self.adapter.build_canonical_input(payload)

    def test_evidence_hash_covers_full_identity_and_canonical_arm_surfaces(self):
        canonical = self.adapter.build_canonical_input(self.fixture())
        original = canonical["evidence_hash"]
        source = canonical["metadata"]["evidence_adapter"]["questions"][0]["arms"]["no_lsp"]["grader_source"]
        self.assertTrue(source["source_hash"])
        self.assertTrue(source["official_report_hash"])
        self.assertTrue(source["official_test_output_hash"])
        mutated = copy.deepcopy(canonical)
        mutated["metadata"]["evidence_adapter"]["questions"][0]["arms"]["with_lsp"]["identity_envelope"]["bindings"]["model"]["id"] = "drifted"
        self.assertNotEqual(
            original,
            self.audit_module.canonical_evidence_hash(mutated),
        )

    def test_missing_required_evidence_surface_fails_fast(self):
        for field in ("identity_envelope", "identity_envelopes", "runner_events", "arm_evidence", "grader_feedback", "usage", "pricing"):
            with self.subTest(field=field):
                payload = self.fixture()
                del payload["arms"]["no_lsp"][field]
                with self.assertRaises(self.audit_module.AuditError):
                    self.adapter.build_canonical_input(payload)
        payload = self.fixture()
        del payload["arms"]["no_lsp"]["official_report"]
        del payload["arms"]["no_lsp"]["official_test_output"]
        with self.assertRaises(Exception):
            self.adapter.build_canonical_input(payload)

    def test_identity_surface_and_runner_drift_fail_fast(self):
        payload = self.fixture()
        payload["arms"]["with_lsp"]["identity_envelopes"]["usage"]["bindings"]["task"]["id"] = "drifted"
        with self.assertRaises(Exception):
            self.adapter.build_canonical_input(payload)

        payload = self.fixture()
        payload["arms"]["with_lsp"]["runner_events"]["events"][0]["code_agent_id"] = "other-agent"
        with self.assertRaisesRegex(self.audit_module.IdentityError, "code_agent_id"):
            self.adapter.build_canonical_input(payload)

    def test_canonical_receipt_and_isolation_tampering_is_not_revalidated_locally(self):
        payload = self.fixture()
        receipt = payload["arms"]["with_lsp"]["arm_evidence"]["receipts"][0]
        receipt["arguments"]["file_path"] = "src/other.py"
        with self.assertRaises(Exception):
            self.adapter.build_canonical_input(payload)

        payload = self.fixture()
        payload["arms"]["no_lsp"]["arm_evidence"]["isolation"]["network"]["disabled"] = False
        with self.assertRaises(Exception):
            self.adapter.build_canonical_input(payload)

    def test_active_or_unverified_runner_never_reaches_audit(self):
        for status in ("active", "unverified", "infrastructure_failure"):
            with self.subTest(status=status):
                payload = self.fixture()
                payload["arms"]["no_lsp"]["runner_events"]["events"][-1]["status"] = status
                with self.assertRaisesRegex(self.audit_module.ContractFailure, "not scoreable"):
                    self.adapter.build_canonical_input(payload)


if __name__ == "__main__":
    unittest.main()
