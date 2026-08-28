import json
import base64
import hashlib
import tempfile
import pathlib
import unittest

from scripts.cross_file_artifact_bridge import (
    ArtifactBridgeError,
    EXECUTOR_FINAL_ATTESTATION_KIND,
    ExecutorAttestationError,
    PersistentAttestationReplayStore,
    build_agent_manifest,
    build_executor_final_attestation,
    parse_codex_jsonl,
    usage_from_events,
    validate_executor_final_attestation,
    build_no_lsp_executor_attestation,
    derive_canonical_arm_evidence_from_attestation,
)
from scripts.cross_file_go_receipts import SCHEMA as GO_RECEIPT_SCHEMA, _go_json
from scripts.cross_file_signed_receipts import Ed25519Signer, Ed25519Verifier


class ArtifactBridgeTest(unittest.TestCase):
    def _events(self):
        return (
            {"type": "thread.started", "thread_id": "thread-1", "session_id": "thread-1"},
            {"type": "turn.completed", "usage": {
                "input_tokens": 10, "cached_input_tokens": 2,
                "output_tokens": 3, "reasoning_tokens": 4,
            }},
        )

    def test_manifest_binds_real_identity_patch_tree_and_trajectory(self):
        result = build_agent_manifest(
            events=self._events(), patch_hash="a" * 64, tree_sha="b" * 40,
            changed_files=["b.py", "a.py", "a.py"], mode="with_lsp",
        )
        self.assertEqual(result["session_id"], "thread-1")
        self.assertEqual(result["request"]["changed_files"], ["a.py", "b.py"])
        self.assertEqual(usage_from_events(self._events())["reasoning_tokens"], 4)

    def test_usage_accepts_codex_reasoning_output_tokens_name(self):
        events = (
            {
                "type": "turn.completed",
                "usage": {
                    "input_tokens": 10,
                    "cached_input_tokens": 2,
                    "output_tokens": 3,
                    "reasoning_output_tokens": 4,
                },
            },
        )
        self.assertEqual(usage_from_events(events)["reasoning_tokens"], 4)

    def test_malformed_jsonl_and_identity_drift_fail_closed(self):
        with self.assertRaises(ArtifactBridgeError):
            parse_codex_jsonl("not-json")
        lines = "\n".join(json.dumps(item) for item in (
            {"thread_id": "a", "session_id": "a"},
            {"thread_id": "b", "session_id": "b"},
        ))
        with self.assertRaises(ArtifactBridgeError):
            build_agent_manifest(
                events=parse_codex_jsonl(lines), patch_hash="a" * 64,
                tree_sha="b" * 40, changed_files=[], mode="no_lsp",
            )

    def _go_log(self, signer):
        public = base64.b64decode(signer.public_key.public_key)
        unsigned = {
            "schema": GO_RECEIPT_SCHEMA,
            "sequence": 1,
            "payload": {
                "receipt_id": "receipt-1",
                "tool": "diagnostics",
                "status": "success",
                "arguments": {"file_path": "a.py"},
                "result": {"rows": []},
            },
            "signer_public_key_sha256": "sha256:" + hashlib.sha256(public).hexdigest(),
        }
        material = _go_json(unsigned)
        record = {
            **unsigned,
            "event_digest": "sha256:" + hashlib.sha256(material).hexdigest(),
            "signature": base64.b64encode(signer.sign(material)).decode("ascii"),
        }
        return json.dumps(record, separators=(",", ":")) + "\n"

    def test_executor_final_attestation_validates_native_chain_and_separates_issuers(self):
        go_signer = Ed25519Signer.generate("mcp-lsp")
        executor_signer = Ed25519Signer.generate("executor")
        receipt_log = self._go_log(go_signer)
        attestation = build_executor_final_attestation(
            receipt_log=receipt_log,
            task="task-1",
            arm="with_lsp",
            session="session-1",
            thread="thread-1",
            patch_hash="a" * 64,
            final_tree_hash="b" * 40,
            server_generation="generation-1",
            mode="with_lsp", run_id="run-1", attempt=1, nonce="nonce-1234567890",
            issued_at="2026-08-26T00:00:00+00:00",
            prompt={"id": "prompt-1", "sha256": "c" * 64},
            isolation_policy={"network": "server-only", "sidecar": "mcp-lsp"},
            signer=executor_signer,
            verifier=Ed25519Verifier(go_signer.public_key),
        )
        self.assertEqual(attestation["signature"]["issuer"], "executor")
        self.assertEqual(attestation["signature"]["kind"], EXECUTOR_FINAL_ATTESTATION_KIND)
        result = validate_executor_final_attestation(
            attestation,
            receipt_log=receipt_log,
            verifier=Ed25519Verifier(executor_signer.public_key),
            receipt_verifier=Ed25519Verifier(go_signer.public_key),
            task="task-1",
            arm="with_lsp",
            session="session-1",
            thread="thread-1",
            patch_hash="a" * 64,
            final_tree_hash="b" * 40,
            server_generation="generation-1",
            mode="with_lsp", run_id="run-1", attempt=1, nonce="nonce-1234567890",
            issued_at="2026-08-26T00:00:00+00:00",
            prompt={"id": "prompt-1", "sha256": "c" * 64},
            isolation_policy={"network": "server-only", "sidecar": "mcp-lsp"},
        )
        self.assertEqual(result.receipt_head_digest, attestation["receipt_head_digest"])
        self.assertEqual(result.final_tree_hash, "b" * 40)

    def test_native_attestation_projects_without_python_mcp_signature(self):
        go_signer = Ed25519Signer.generate("mcp-lsp")
        executor_signer = Ed25519Signer.generate("executor")
        receipt_log = self._go_log(go_signer)
        attestation = build_executor_final_attestation(
            receipt_log=receipt_log, task="task-1", arm="with_lsp", session="session-1",
            thread="thread-1", patch_hash="a" * 64, final_tree_hash="b" * 40,
            server_generation="generation-1", mode="with_lsp", run_id="run-1", attempt=1,
            nonce="nonce-1234567890", issued_at="2026-08-26T00:00:00+00:00",
            prompt="prompt", isolation_policy={"network": "server-only"},
            signer=executor_signer, verifier=Ed25519Verifier(go_signer.public_key),
        )
        projection = derive_canonical_arm_evidence_from_attestation(
            attestation, receipt_log=receipt_log,
            executor_verifier=Ed25519Verifier(executor_signer.public_key),
            receipt_verifier=Ed25519Verifier(go_signer.public_key), task="task-1",
            arm="with_lsp", session="session-1", thread="thread-1", patch_hash="a" * 64,
            final_tree_hash="b" * 40, server_generation="generation-1", mode="with_lsp",
            run_id="run-1",
        )
        self.assertEqual(len(projection.receipts), 1)
        self.assertNotIn("signature", projection.receipts[0].to_dict())

    def test_executor_final_attestation_rejects_chain_or_binding_tampering(self):
        go_signer = Ed25519Signer.generate("mcp-lsp")
        executor_signer = Ed25519Signer.generate("executor")
        receipt_log = self._go_log(go_signer)
        attestation = build_executor_final_attestation(
            receipt_log=receipt_log,
            task="task-1", arm="with_lsp", session="session-1", thread="thread-1",
            patch_hash="a" * 64, final_tree_hash="b" * 40,
            server_generation="generation-1", prompt="prompt",
            mode="with_lsp", run_id="run-1", attempt=1, nonce="nonce-1234567890",
            issued_at="2026-08-26T00:00:00+00:00",
            isolation_policy={"network": "server-only"}, signer=executor_signer,
            verifier=Ed25519Verifier(go_signer.public_key),
        )
        with self.assertRaises(ExecutorAttestationError):
            validate_executor_final_attestation(
                attestation, receipt_log=receipt_log.replace("a.py", "b.py"),
                verifier=Ed25519Verifier(executor_signer.public_key),
                receipt_verifier=Ed25519Verifier(go_signer.public_key),
                task="task-1", arm="with_lsp", session="session-1", thread="thread-1",
                patch_hash="a" * 64, final_tree_hash="b" * 40,
                server_generation="generation-1",
                mode="with_lsp", run_id="run-1", attempt=1, nonce="nonce-1234567890",
                issued_at="2026-08-26T00:00:00+00:00",
            )
        with self.assertRaises(ExecutorAttestationError):
            validate_executor_final_attestation(
                {**attestation, "final_tree_hash": "c" * 40},
                receipt_log=receipt_log,
                verifier=Ed25519Verifier(executor_signer.public_key),
                receipt_verifier=Ed25519Verifier(go_signer.public_key),
                task="task-1", arm="with_lsp", session="session-1", thread="thread-1",
                patch_hash="a" * 64, final_tree_hash="b" * 40,
                server_generation="generation-1",
                mode="with_lsp", run_id="run-1", attempt=1, nonce="nonce-1234567890",
                issued_at="2026-08-26T00:00:00+00:00",
            )

    def test_no_lsp_attestation_scans_real_stdout_and_rejects_mcp_events(self):
        signer = Ed25519Signer.generate("executor")
        kwargs = {
            "task": "task-1", "arm": "no_lsp", "session": "session-1",
            "thread": "thread-1", "patch_hash": "a" * 64,
            "final_tree_hash": "b" * 40, "server_generation": "generation-1",
            "run_id": "run-1", "attempt": 1, "nonce": "nonce-1234567890",
            "issued_at": "2026-08-26T00:00:00+00:00", "prompt": "prompt",
            "isolation_policy": {
                "model_egress": {"allowed": True, "proxy": None},
                "mcp_sidecar": {"disabled": True},
                "mcp_ingress": {"count": 0},
            },
            "signer": signer,
        }
        attestation = build_no_lsp_executor_attestation(
            stdout=json.dumps({"type": "thread.started"}), **kwargs
        )
        self.assertEqual(attestation["receipt_count"], 0)
        self.assertEqual(attestation["isolation_policy"]["ingress_count"], 0)
        with self.assertRaises(ExecutorAttestationError):
            build_no_lsp_executor_attestation(
                stdout=json.dumps({"type": "mcp_tool_call", "tool": "diagnostics"}),
                **kwargs,
            )

    def test_no_lsp_attestation_ignores_mcp_words_in_untrusted_output(self):
        signer = Ed25519Signer.generate("executor")
        attestation = build_no_lsp_executor_attestation(
            stdout=json.dumps(
                {
                    "type": "item.completed",
                    "item": {
                        "type": "command_execution",
                        "aggregated_output": (
                            "/repo/builtin_frames.py: Internet access disabled; "
                            "diagnostics mentioned by a test fixture"
                        ),
                    },
                }
            ),
            task="task-1", arm="no_lsp", session="session-1", thread="thread-1",
            patch_hash="a" * 64, final_tree_hash="b" * 40,
            server_generation="generation-1", run_id="run-1", attempt=1,
            nonce="nonce-1234567890", issued_at="2026-08-26T00:00:00+00:00",
            prompt="prompt",
            isolation_policy={
                "model_egress": {"allowed": True, "proxy": None},
                "mcp_sidecar": {"disabled": True}, "mcp_ingress": {"count": 0},
            },
            signer=signer,
        )
        self.assertEqual(attestation["receipt_count"], 0)

    def test_persistent_replay_store_rejects_second_consumption(self):
        signer = Ed25519Signer.generate("executor")
        receipt = self._go_log(signer)
        attestation = build_executor_final_attestation(
            receipt_log=receipt, task="task-1", arm="with_lsp", session="session-1",
            thread="thread-1", patch_hash="a" * 64, final_tree_hash="b" * 40,
            server_generation="generation-1", mode="with_lsp", run_id="run-1",
            attempt=1, nonce="nonce-1234567890", issued_at="2026-08-26T00:00:00+00:00",
            prompt="prompt", isolation_policy={"network": "server-only"},
            signer=signer, verifier=Ed25519Verifier(signer.public_key),
        )
        with tempfile.TemporaryDirectory() as directory:
            store = PersistentAttestationReplayStore(pathlib.Path(directory) / "replay.json")
            store.consume(attestation)
            with self.assertRaises(ExecutorAttestationError):
                store.consume(attestation)


if __name__ == "__main__":
    unittest.main()
