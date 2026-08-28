import base64
import copy
import pathlib
import tempfile
import unittest

from cryptography.hazmat.primitives import serialization

from scripts.cross_file_identity_envelope import (
    IdentityEnvelopeError,
    build_identity_envelope,
    parse_identity_envelope,
)
from scripts.cross_file_signed_receipts import (
    MCP_RECEIPT_KIND,
    Ed25519Signer,
    Ed25519Verifier,
    ReceiptReplayError,
    ReceiptReplayGuard,
    SignatureVerificationError,
    SigningKeyError,
    attach_signature,
    rebind_signed_evidence,
    sign_final_evidence,
    sign_grader_receipt,
    sign_payload,
    validate_grader_receipt,
    validate_receipt_context,
    validate_final_evidence,
    verify_signed_record,
)


PATCH_HASH = "a" * 64
ARTIFACT = {"id": "grader-v1", "sha256": "b" * 64}


def _payload() -> dict:
    return {
        "schema": "super-dolphin/mcp-server-receipt/v1",
        "task": "task-01",
        "arm": "with-lsp",
        "session": "session-01",
        "thread": "thread-01",
        "seq": 1,
        "request_id": "request-01",
        "args_hash": "c" * 64,
        "result_hash": "d" * 64,
        "status": "success",
        "server_generation": "generation-01",
        "patch_hash": PATCH_HASH,
        "grader_artifact": ARTIFACT,
        "tool": "structure",
        "arguments": {"file_path": "src/a.py"},
        "result": {"locations": ["src/a.py:10:2"]},
    }


def _bindings() -> dict:
    return {
        "manifest": {"id": "manifest-v1", "sha256": "sha256:" + "1" * 64},
        "dataset": {"id": "dataset-v1", "sha256": "sha256:" + "2" * 64},
        "task": {"id": "task-01", "sha256": "sha256:" + "3" * 64},
        "base": {
            "commit_sha": "a" * 40,
            "tree_sha": "b" * 40,
            "sha256": "sha256:" + "4" * 64,
        },
        "source_image": {"id": "image-v1", "sha256": "sha256:" + "5" * 64},
        "toolchain": {"id": "toolchain-v1", "sha256": "sha256:" + "6" * 64},
        "grader": {"id": "grader-v1", "sha256": "sha256:" + "7" * 64},
        "runner": {"id": "runner-v1", "sha256": "sha256:" + "8" * 64},
        "model": {"id": "model-v1", "sha256": "sha256:" + "9" * 64},
        "prompt": {"id": "prompt-v1", "sha256": "sha256:" + "a" * 64},
        "pricing": {"id": "pricing-v1", "sha256": "sha256:" + "b" * 64},
        "scoring": {"id": "scoring-v1", "sha256": "sha256:" + "c" * 64},
        "session": {"id": "session-01", "sha256": "sha256:" + "d" * 64},
        "thread": {"id": "thread-01", "sha256": "sha256:" + "e" * 64},
        "agent": {"id": "agent-v1", "sha256": "sha256:" + "f" * 64},
        "patch": {"id": PATCH_HASH, "sha256": "sha256:" + "0" * 64},
        "final_tree": {"tree_sha": "e" * 40, "sha256": "sha256:" + "1" * 64},
    }


class SignedReceiptTests(unittest.TestCase):
    def setUp(self):
        self.signer = Ed25519Signer.generate("key-01")
        self.verifier = Ed25519Verifier(self.signer.public_key)

    def test_signature_covers_complete_payload_and_context(self):
        signed = attach_signature(
            _payload(),
            signer=self.signer,
            kind=MCP_RECEIPT_KIND,
            issuer="mcp-server",
        )
        payload = verify_signed_record(
            signed,
            verifier=self.verifier,
            kind=MCP_RECEIPT_KIND,
            issuer="mcp-server",
        )
        validate_receipt_context(
            payload,
            task="task-01",
            arm="with-lsp",
            session="session-01",
            thread="thread-01",
            patch_hash=PATCH_HASH,
            grader_artifact=ARTIFACT,
        )
        self.assertEqual(signed["signature"]["public_key_id"], "key-01")
        self.assertNotIn("private", repr(self.signer).lower())

    def test_tamper_and_wrong_key_are_rejected(self):
        signed = attach_signature(
            _payload(), signer=self.signer, kind=MCP_RECEIPT_KIND, issuer="mcp-server"
        )
        tampered = copy.deepcopy(signed)
        tampered["result"]["locations"] = ["src/other.py:10:2"]
        with self.assertRaises(SignatureVerificationError):
            verify_signed_record(
                tampered,
                verifier=self.verifier,
                kind=MCP_RECEIPT_KIND,
                issuer="mcp-server",
            )

        wrong = Ed25519Verifier(Ed25519Signer.generate("key-02").public_key)
        with self.assertRaises(SignatureVerificationError):
            verify_signed_record(
                signed,
                verifier=wrong,
                kind=MCP_RECEIPT_KIND,
                issuer="mcp-server",
            )

        wrong_id = copy.deepcopy(signed)
        wrong_id["signature"]["public_key_id"] = "key-02"
        with self.assertRaises(SignatureVerificationError):
            verify_signed_record(
                wrong_id,
                verifier=self.verifier,
                kind=MCP_RECEIPT_KIND,
                issuer="mcp-server",
            )

    def test_replay_guard_rejects_request_and_signature_reuse(self):
        signed = attach_signature(
            _payload(), signer=self.signer, kind=MCP_RECEIPT_KIND, issuer="mcp-server"
        )
        guard = ReceiptReplayGuard()
        guard.observe(signed, signed["signature"])
        with self.assertRaises(ReceiptReplayError):
            guard.observe(signed, signed["signature"])

        second = copy.deepcopy(signed)
        second["request_id"] = "request-02"
        with self.assertRaises(ReceiptReplayError):
            guard.observe(second, second["signature"])

    def test_private_key_is_read_only_from_explicit_absolute_path(self):
        raw = self.signer._private_key.private_bytes(  # noqa: SLF001 - test-only serialization.
            encoding=serialization.Encoding.Raw,
            format=serialization.PrivateFormat.Raw,
            encryption_algorithm=serialization.NoEncryption(),
        )
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "signing.key"
            path.write_bytes(raw)
            loaded = Ed25519Signer.from_file(path, key_id="key-01")
            self.assertEqual(loaded.public_key, self.signer.public_key)
            with self.assertRaises(SigningKeyError):
                Ed25519Signer.from_file("relative.key", key_id="key-01")
        with self.assertRaises(SigningKeyError):
            Ed25519Signer.from_file("/definitely/missing/key", key_id="key-01")

    def test_public_key_is_bound_into_identity_envelope(self):
        envelope = build_identity_envelope(
            _bindings(), receipt_signing_key=self.signer.public_key.to_dict()
        )
        parsed = parse_identity_envelope(envelope)
        self.assertEqual(
            parsed.to_dict()["receipt_signing_key"], self.signer.public_key.to_dict()
        )
        self.assertEqual(
            Ed25519Verifier.from_identity_envelope(envelope).binding,
            self.signer.public_key,
        )
        tampered = copy.deepcopy(envelope)
        tampered["receipt_signing_key"]["public_key"] = base64.b64encode(b"x" * 32).decode()
        with self.assertRaises(IdentityEnvelopeError):
            parse_identity_envelope(tampered)

    def test_signed_grader_receipt_binds_patch_and_artifact(self):
        receipt = sign_grader_receipt(
            {"status": "PASSED", "passed": True},
            task="task-01",
            arm="with-lsp",
            session="session-01",
            thread="thread-01",
            seq=1,
            request_id="grader-request-01",
            patch_hash=PATCH_HASH,
            grader_artifact=ARTIFACT,
            signer=self.signer,
        )
        result = validate_grader_receipt(
            receipt,
            verifier=self.verifier,
            task="task-01",
            arm="with-lsp",
            session="session-01",
            thread="thread-01",
            patch_hash=PATCH_HASH,
            grader_artifact=ARTIFACT,
        )
        self.assertEqual(result["status"], "PASSED")
        mutated = copy.deepcopy(receipt)
        mutated["grader_artifact"]["id"] = "other"
        with self.assertRaises(SignatureVerificationError):
            validate_grader_receipt(
                mutated,
                verifier=self.verifier,
                task="task-01",
                arm="with-lsp",
                session="session-01",
                thread="thread-01",
                patch_hash=PATCH_HASH,
                grader_artifact=ARTIFACT,
            )

    def test_patch_identity_arm_session_artifact_and_final_tree_replay_are_rejected(self):
        final_tree = "e" * 40
        old = sign_final_evidence(
            _payload(),
            signer=self.signer,
            kind=MCP_RECEIPT_KIND,
            issuer="mcp-server",
            task="task-01",
            arm="with-lsp",
            session="session-01",
            thread="thread-01",
            seq=1,
            request_id="request-01",
            patch_hash=PATCH_HASH,
            grader_artifact=ARTIFACT,
            final_tree_hash=final_tree,
        )
        validate_final_evidence(
            old,
            verifier=self.verifier,
            kind=MCP_RECEIPT_KIND,
            issuer="mcp-server",
            task="task-01",
            arm="with-lsp",
            session="session-01",
            thread="thread-01",
            patch_hash=PATCH_HASH,
            grader_artifact=ARTIFACT,
            final_tree_hash=final_tree,
        )

        mutations = (
            ("patch_hash", "c" * 64),
            ("task", "task-02"),
            ("arm", "no-lsp"),
            ("session", "session-02"),
            ("grader_artifact", {"id": "other", "sha256": "f" * 64}),
            ("final_tree_hash", "f" * 40),
        )
        for field, replacement in mutations:
            with self.subTest(field=field):
                tampered = copy.deepcopy(old)
                tampered[field] = replacement
                with self.assertRaises(SignatureVerificationError):
                    validate_final_evidence(
                        tampered,
                        verifier=self.verifier,
                        kind=MCP_RECEIPT_KIND,
                        issuer="mcp-server",
                        task="task-01",
                        arm="with-lsp",
                        session="session-01",
                        thread="thread-01",
                        patch_hash=PATCH_HASH,
                        grader_artifact=ARTIFACT,
                        final_tree_hash=final_tree,
                    )

        rebound = rebind_signed_evidence(
            old,
            signer=self.signer,
            kind=MCP_RECEIPT_KIND,
            issuer="mcp-server",
            task="task-02",
            arm="with-lsp",
            session="session-02",
            thread="thread-02",
            seq=2,
            request_id="request-02",
            patch_hash="c" * 64,
            grader_artifact={"id": "grader-v2", "sha256": "f" * 64},
            final_tree_hash="f" * 40,
        )
        self.assertNotEqual(rebound["signature"], old["signature"])
        validated = validate_final_evidence(
            rebound,
            verifier=self.verifier,
            kind=MCP_RECEIPT_KIND,
            issuer="mcp-server",
            task="task-02",
            arm="with-lsp",
            session="session-02",
            thread="thread-02",
            patch_hash="c" * 64,
            grader_artifact={"id": "grader-v2", "sha256": "f" * 64},
            final_tree_hash="f" * 40,
        )
        self.assertEqual(validated["seq"], 2)


if __name__ == "__main__":
    unittest.main()
