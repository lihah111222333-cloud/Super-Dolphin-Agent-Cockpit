import dataclasses
import hashlib
import unittest

from scripts.cross_file_lsp_receipts import (
    ARM_EVIDENCE_SCHEMA,
    DiffFilterError,
    ISOLATION_SCHEMA,
    MCPServerReceipt,
    SCHEMA,
    IsolationError,
    ReceiptChainError,
    ReceiptHashError,
    ReceiptIdentityError,
    ReceiptSignatureError,
    ReceiptSequenceError,
    ReceiptStatusError,
    UnknownFieldError,
    build_canonical_arm_evidence,
    canonical_isolation_attestation_payload,
    digest_json,
    digest_text,
    scan_no_lsp_prompt,
    validate_lsp_receipts,
    validate_canonical_arm_evidence,
    validate_no_lsp_isolation,
    validate_receipts,
    validate_server_receipt,
    normalize_diagnostics_arguments,
    parse_diff_filter,
)
from scripts.cross_file_signed_receipts import (
    Ed25519Signer,
    Ed25519Verifier,
    attach_signature,
    sign_payload,
)


IDENTITY = {
    "task": "task-01",
    "arm": "with-lsp",
    "session": "session-01",
    "thread": "thread-01",
    "server_generation": "generation-01",
}
SIGNER = Ed25519Signer.generate("receipt-key-01")
VERIFIER = Ed25519Verifier(SIGNER.public_key)
PATCH_HASH = "a" * 64
FINAL_TREE_HASH = "e" * 40
GRADER_ARTIFACT = {"id": "grader-v1", "sha256": "b" * 64}


def _receipt(seq, tool, arguments, result, **overrides):
    value = {
        "schema": SCHEMA,
        "source": "mcp-server",
        **IDENTITY,
        "seq": seq,
        "request_id": f"request-{seq}",
        "args_hash": digest_json(arguments),
        "result_hash": digest_json(result),
        "status": "success",
        "patch_hash": PATCH_HASH,
        "final_tree_hash": FINAL_TREE_HASH,
        "tool": tool,
        "arguments": arguments,
        "result": result,
    }
    value.update(overrides)
    return attach_signature(
        value,
        signer=SIGNER,
        kind="mcp_server_receipt",
        issuer="mcp-server",
    )


def _lsp_stream():
    return [
        _receipt(
            1,
            "structure",
            {"action": "document_symbol", "file_path": "src/a.py"},
            {"locations": ["src/a.py:10:2"]},
        ),
        _receipt(
            2,
            "xref",
            {"action": "references", "pos": "src/a.py:10:2"},
            {"locations": []},
        ),
        _receipt(
            3,
            "diagnostics",
            {
                "file_paths": ["src/a.py"],
                "diff_filter": {
                    "mode": "changed_lines",
                    "changed_lines": {"src/a.py": [10]},
                },
            },
            {"rows": [], "status": "clean"},
        ),
    ]


def _resign(value):
    payload = {key: item for key, item in value.items() if key != "signature"}
    value["signature"] = sign_payload(
        payload,
        signer=SIGNER,
        kind="mcp_server_receipt",
        issuer="mcp-server",
    )
    return value


def _validate_stream(stream):
    return validate_receipts(
        stream,
        task="task-01",
        arm="with-lsp",
        session="session-01",
        thread="thread-01",
        server_generation="generation-01",
        verifier=VERIFIER,
    )


def _isolation(*, ingress=None, count=0, network_disabled=True, sidecar_disabled=True):
    ingress = [] if ingress is None else ingress
    raw_prompt = "Fix the requested behavior in the repository."
    value = {
        "schema": ISOLATION_SCHEMA,
        "mode": "no_lsp",
        "task": "task-01",
        "arm": "no-lsp",
        "session": "session-01",
        "thread": "thread-01",
        "issuer": "executor",
        "patch_hash": PATCH_HASH,
        "final_tree_hash": FINAL_TREE_HASH,
        "network": {
            "disabled": network_disabled,
            "status": "enforced",
            "policy_hash": hashlib.sha256(b"network-policy").hexdigest(),
        },
        "sidecar": {
            "disabled": sidecar_disabled,
            "status": "enforced",
            "policy_hash": hashlib.sha256(b"sidecar-policy").hexdigest(),
        },
        "ingress": {
            "count": count,
            "receipts": ingress,
            "log_hash": digest_json(ingress),
        },
        "raw_prompt_hash": digest_text(raw_prompt),
        "raw_prompt": raw_prompt,
    }
    value["attestation_hash"] = digest_json(canonical_isolation_attestation_payload(value))
    return attach_signature(
        value,
        signer=SIGNER,
        kind="no_lsp_isolation",
        issuer="executor",
    )


class DiffFilterTests(unittest.TestCase):
    def test_object_diff_filter_is_canonical_and_matches_file_paths(self):
        value = parse_diff_filter(
            {
                "mode": "changed_lines",
                "changed_lines": {"src/z.py": [9, 2], "src/a.py": [4]},
            },
            target_files=("src/a.py", "src/z.py"),
        )
        self.assertEqual(value["mode"], "changed_lines")
        self.assertEqual(value["changed_lines"], {"src/a.py": [4], "src/z.py": [2, 9]})

        normalized = normalize_diagnostics_arguments(
            {
                "file_paths": ["src/z.py", "src/a.py"],
                "diff_filter": {
                    "mode": "changed_lines",
                    "changed_lines": {"src/a.py": [4], "src/z.py": [2, 9]},
                },
            },
            target_files=("src/a.py", "src/z.py"),
            require_file_paths=True,
        )
        self.assertEqual(normalized["file_paths"], ["src/a.py", "src/z.py"])

    def test_diff_filter_rejects_wrong_mode_unknown_fields_and_bad_lines(self):
        base = {"mode": "changed_lines", "changed_lines": {"src/a.py": [1]}}
        for value, message in (
            ({"mode": "all", "changed_lines": {"src/a.py": [1]}}, "mode"),
            ({"mode": "changed_lines", "changed_lines": {"src/a.py": [0]}}, "positive"),
            ({"mode": "changed_lines", "changed_lines": {"src/a.py": [True]}}, "positive"),
            ({"mode": "changed_lines", "changed_lines": {"src/a.py": [1, 1]}}, "duplicate"),
            ({"mode": "changed_lines", "changed_lines": {}}, "must not be empty"),
            ({**base, "extra": True}, "unknown"),
        ):
            with self.subTest(value=value):
                with self.assertRaisesRegex(DiffFilterError, message):
                    parse_diff_filter(value)

    def test_diff_filter_rejects_missing_and_extra_target_files(self):
        value = {"mode": "changed_lines", "changed_lines": {"src/a.py": [1]}}
        with self.assertRaisesRegex(DiffFilterError, "missing target"):
            parse_diff_filter(value, target_files=("src/a.py", "src/b.py"))
        extra = {"mode": "changed_lines", "changed_lines": {"src/a.py": [1], "src/c.py": [2]}}
        with self.assertRaisesRegex(DiffFilterError, "extra file"):
            parse_diff_filter(extra, target_files=("src/a.py",))

    def test_diagnostics_arguments_require_object_filter_and_exact_file_paths(self):
        arguments = {
            "file_paths": ["src/a.py", "src/b.py"],
            "diff_filter": {
                "mode": "changed_lines",
                "changed_lines": {"src/a.py": [1]},
            },
        }
        with self.assertRaisesRegex(DiffFilterError, "missing target"):
            normalize_diagnostics_arguments(arguments, require_file_paths=True)

        arguments["diff_filter"]["changed_lines"]["src/b.py"] = [2]
        arguments["diff_filter"]["changed_lines"]["src/c.py"] = [3]
        with self.assertRaisesRegex(DiffFilterError, "extra file"):
            normalize_diagnostics_arguments(arguments, require_file_paths=True)

        no_file_paths = {
            "file_path": "src/a.py",
            "diff_filter": {"mode": "changed_lines", "changed_lines": {"src/a.py": [1]}},
        }
        with self.assertRaisesRegex(DiffFilterError, "file_paths"):
            normalize_diagnostics_arguments(no_file_paths, require_file_paths=True)


class MCPServerReceiptTests(unittest.TestCase):
    def test_successful_server_receipts_bind_hashes_identity_and_generation(self):
        stream = validate_receipts(
            _lsp_stream(),
            task="task-01",
            arm="with-lsp",
            session="session-01",
            thread="thread-01",
            server_generation="generation-01",
            verifier=VERIFIER,
        )
        self.assertEqual([item.seq for item in stream], [1, 2, 3])
        self.assertEqual(stream[0].tool, "structure")
        self.assertEqual(validate_server_receipt(stream[1], verifier=VERIFIER).request_id, "request-2")

    def test_every_required_server_field_is_fail_closed(self):
        required = (
            "source",
            "task",
            "arm",
            "session",
            "thread",
            "seq",
            "request_id",
            "args_hash",
            "result_hash",
            "status",
            "server_generation",
            "patch_hash",
            "signature",
        )
        for field in required:
            with self.subTest(field=field):
                value = _lsp_stream()[0]
                del value[field]
                with self.assertRaises(ValueError):
                    validate_server_receipt(value, verifier=VERIFIER)

    def test_argument_and_result_tampering_is_rejected(self):
        value = _lsp_stream()[0]
        value["arguments"]["file_path"] = "src/other.py"
        with self.assertRaises(ReceiptHashError):
            validate_server_receipt(value)

        value = _lsp_stream()[0]
        value["result"] = {"locations": ["src/other.py:10:2"]}
        with self.assertRaises(ReceiptHashError):
            validate_server_receipt(value)

    def test_error_timeout_and_transport_closed_never_count_as_success(self):
        for status in ("error", "timeout", "transport_closed"):
            with self.subTest(status=status):
                value = _lsp_stream()[0]
                value["status"] = status
                with self.assertRaises(ReceiptStatusError):
                    validate_server_receipt(value, verifier=VERIFIER)

    def test_unknown_fields_tools_and_non_server_issuers_are_rejected(self):
        value = _lsp_stream()[0]
        value["untrusted_claim"] = True
        with self.assertRaises(UnknownFieldError):
            validate_server_receipt(value, verifier=VERIFIER)

        value = _lsp_stream()[0]
        value["tool"] = "grep"
        with self.assertRaises(ValueError):
            validate_server_receipt(value, verifier=VERIFIER)

        value = _lsp_stream()[0]
        value["source"] = "agent-jsonl"
        with self.assertRaises(ValueError):
            validate_server_receipt(value, verifier=VERIFIER)

    def test_stream_rejects_duplicate_ids_sequence_gaps_and_generation_drift(self):
        stream = _lsp_stream()
        stream[1]["request_id"] = stream[0]["request_id"]
        _resign(stream[1])
        with self.assertRaises(ReceiptSequenceError):
            _validate_stream(stream)

        stream = _lsp_stream()
        stream[1]["seq"] = 4
        _resign(stream[1])
        with self.assertRaises(ReceiptSequenceError):
            _validate_stream(stream)

        stream = _lsp_stream()
        stream[2]["server_generation"] = "generation-02"
        _resign(stream[2])
        with self.assertRaises(ReceiptIdentityError):
            _validate_stream(stream)

    def test_stream_identity_drift_is_rejected(self):
        stream = _lsp_stream()
        stream[1]["arm"] = "no-lsp"
        _resign(stream[1])
        with self.assertRaises(ReceiptIdentityError):
            _validate_stream(stream)

    def test_structure_to_xref_chain_uses_real_prior_structure_position(self):
        stream = _lsp_stream()
        validated = _validate_stream(stream)
        coverage = validate_lsp_receipts(
            validated,
            changed_files=("src/a.py",),
            verifier=VERIFIER,
        )
        self.assertEqual(coverage.structure_positions, ("src/a.py:10:2",))
        self.assertEqual(coverage.diagnostics_files, ("src/a.py",))

        before_structure = [validated[1], validated[0], validated[2]]
        with self.assertRaises(ReceiptChainError):
            validate_lsp_receipts(before_structure, changed_files=("src/a.py",), verifier=VERIFIER)

        wrong_position = _lsp_stream()
        wrong_position[1]["arguments"]["pos"] = "src/a.py:11:2"
        wrong_position[1]["args_hash"] = digest_json(wrong_position[1]["arguments"])
        _resign(wrong_position[1])
        validated_wrong = _validate_stream(wrong_position)
        with self.assertRaises(ReceiptChainError):
            validate_lsp_receipts(validated_wrong, changed_files=("src/a.py",), verifier=VERIFIER)

    def test_diagnostics_must_name_each_changed_file(self):
        stream = _lsp_stream()
        stream[2]["arguments"]["file_paths"] = ["src/other.py"]
        stream[2]["arguments"]["diff_filter"]["changed_lines"] = {"src/other.py": [10]}
        stream[2]["args_hash"] = digest_json(stream[2]["arguments"])
        _resign(stream[2])
        validated = _validate_stream(stream)
        with self.assertRaisesRegex(ReceiptChainError, "missing files"):
            validate_lsp_receipts(validated, changed_files=("src/a.py",), verifier=VERIFIER)

        stream = _lsp_stream()
        del stream[2]["arguments"]["file_paths"]
        stream[2]["args_hash"] = digest_json(stream[2]["arguments"])
        _resign(stream[2])
        validated = _validate_stream(stream)
        with self.assertRaisesRegex(ReceiptChainError, "file_paths"):
            validate_lsp_receipts(validated, changed_files=("src/a.py",), verifier=VERIFIER)

        stream = _lsp_stream()
        stream[2]["arguments"]["file_paths"] = ["src/a.py", "src/b.py"]
        stream[2]["arguments"]["diff_filter"]["changed_lines"]["src/b.py"] = [11]
        stream[2]["args_hash"] = digest_json(stream[2]["arguments"])
        _resign(stream[2])
        validated = _validate_stream(stream)
        with self.assertRaisesRegex(ReceiptChainError, "extra files"):
            validate_lsp_receipts(validated, changed_files=("src/a.py",), verifier=VERIFIER)

    def test_receipt_round_trip_is_hash_stable(self):
        value = _lsp_stream()[0]
        receipt = validate_server_receipt(value, verifier=VERIFIER)
        self.assertEqual(validate_server_receipt(receipt.to_dict(), verifier=VERIFIER), receipt)

    def test_typed_receipts_are_revalidated_at_the_trust_boundary(self):
        receipt = validate_server_receipt(_lsp_stream()[0], verifier=VERIFIER)
        forged = dataclasses.replace(receipt, status="error")
        self.assertIsInstance(forged, MCPServerReceipt)
        with self.assertRaises(ReceiptStatusError):
            validate_server_receipt(forged, verifier=VERIFIER)

    def test_signed_receipts_reject_patch_artifact_and_identity_replay(self):
        for label, kwargs, expected_error in (
            (
                "old patch",
                {"patch_hash": "c" * 64},
                ReceiptSignatureError,
            ),
            (
                "new grader artifact",
                {"grader_artifact": {"id": "grader-v2", "sha256": "e" * 64}},
                ReceiptSignatureError,
            ),
            ("cross task", {"task": "other-task"}, ReceiptIdentityError),
            ("cross arm", {"arm": "no-lsp"}, ReceiptIdentityError),
            ("cross session", {"session": "other-session"}, ReceiptIdentityError),
        ):
            with self.subTest(label=label):
                params = {
                    "task": "task-01",
                    "arm": "with-lsp",
                    "session": "session-01",
                    "thread": "thread-01",
                    "server_generation": "generation-01",
                    "verifier": VERIFIER,
                }
                params.update(kwargs)
                with self.assertRaises(expected_error):
                    validate_receipts(_lsp_stream(), **params)


class NoLSPIsolationTests(unittest.TestCase):
    def test_executor_attestation_requires_disabled_network_sidecar_and_empty_log(self):
        result = validate_no_lsp_isolation(
            _isolation(),
            task="task-01",
            arm="no-lsp",
            session="session-01",
            thread="thread-01",
            verifier=VERIFIER,
        )
        self.assertEqual(result.ingress_log_hash, digest_json([]))
        self.assertEqual(result.issuer, "executor")
        self.assertEqual(result.raw_prompt_hash, digest_text(result.raw_prompt))

    def test_attestation_hash_binds_complete_policy_ingress_and_identity(self):
        evidence = _isolation()
        result = validate_no_lsp_isolation(evidence, verifier=VERIFIER)
        self.assertEqual(
            result.attestation_hash,
            digest_json(canonical_isolation_attestation_payload(evidence)),
        )

        for mutate in (
            lambda value: value["network"].update({"policy_hash": "0" * 64}),
            lambda value: value["sidecar"].update({"policy_hash": "1" * 64}),
            lambda value: value.update({"task": "other-task"}),
            lambda value: value["ingress"].update({"log_hash": "2" * 64}),
        ):
            with self.subTest(mutate=mutate):
                tampered = _isolation()
                mutate(tampered)
                with self.assertRaises(IsolationError):
                    validate_no_lsp_isolation(tampered, verifier=VERIFIER)

    def test_executor_issuer_and_raw_prompt_are_required_and_bound(self):
        evidence = _isolation()
        evidence["issuer"] = "agent"
        with self.assertRaisesRegex(IsolationError, "issuer"):
            validate_no_lsp_isolation(evidence, verifier=VERIFIER)

        evidence = _isolation()
        evidence["raw_prompt"] = "Fix this behavior without changing the hash."
        with self.assertRaises(IsolationError):
            validate_no_lsp_isolation(evidence, verifier=VERIFIER)

        evidence = _isolation()
        evidence["raw_prompt_hash"] = "3" * 64
        with self.assertRaises(IsolationError):
            validate_no_lsp_isolation(evidence, verifier=VERIFIER)

    def test_restricted_no_lsp_prompt_scan_rejects_every_semantic_marker(self):
        for marker in ("LSP", "MCP", "structure", "xref", "diagnostics", "MCP-LSP", "structure_to_xref"):
            with self.subTest(marker=marker):
                with self.assertRaises(IsolationError):
                    scan_no_lsp_prompt(f"Please use {marker} to solve this task.")
        raw = "  Fix only the requested behavior.  "
        self.assertEqual(scan_no_lsp_prompt(raw), raw)

    def test_network_or_sidecar_enabled_is_not_no_lsp(self):
        for field in ("network_disabled", "sidecar_disabled"):
            with self.subTest(field=field):
                evidence = _isolation(**{field: False})
                with self.assertRaises(IsolationError):
                    validate_no_lsp_isolation(evidence, verifier=VERIFIER)

    def test_any_ingress_receipt_including_an_error_is_contamination(self):
        ingress = [{"status": "error", "tool": "diagnostics"}]
        for kwargs in (
            {"ingress": ingress, "count": 1},
            {"ingress": [], "count": 1},
        ):
            with self.subTest(kwargs=kwargs):
                with self.assertRaises(IsolationError):
                    validate_no_lsp_isolation(_isolation(**kwargs), verifier=VERIFIER)

    def test_ingress_log_hash_identity_schema_and_unknown_fields_are_strict(self):
        evidence = _isolation()
        evidence["ingress"]["log_hash"] = "0" * 64
        with self.assertRaises(IsolationError):
            validate_no_lsp_isolation(evidence, verifier=VERIFIER)

        evidence = _isolation()
        evidence["task"] = "other-task"
        with self.assertRaises(ReceiptIdentityError):
            validate_no_lsp_isolation(evidence, task="task-01", verifier=VERIFIER)

        evidence = _isolation()
        evidence["unexpected"] = True
        with self.assertRaises(UnknownFieldError):
            validate_no_lsp_isolation(evidence, verifier=VERIFIER)

        evidence = _isolation()
        evidence["schema"] = "super-dolphin/no-lsp-isolation/v1"
        with self.assertRaises(IsolationError):
            validate_no_lsp_isolation(evidence, verifier=VERIFIER)

    def test_no_lsp_mode_cannot_be_relabelled_as_with_lsp(self):
        evidence = _isolation()
        evidence["mode"] = "with_lsp"
        with self.assertRaises(IsolationError):
            validate_no_lsp_isolation(evidence, verifier=VERIFIER)


class CanonicalArmEvidenceTests(unittest.TestCase):
    def test_with_lsp_canonical_evidence_contains_validated_receipts_and_coverage(self):
        payload = {
            "schema": ARM_EVIDENCE_SCHEMA,
            "mode": "with_lsp",
            "task": "task-01",
            "arm": "with-lsp",
            "session": "session-01",
            "thread": "thread-01",
            "receipts": _lsp_stream(),
            "isolation": None,
        }
        evidence = validate_canonical_arm_evidence(
            payload,
            task="task-01",
            arm="with-lsp",
            session="session-01",
            thread="thread-01",
            changed_files=("src/a.py",),
            server_generation="generation-01",
            patch_hash=PATCH_HASH,
            final_tree_hash=FINAL_TREE_HASH,
            verifier=VERIFIER,
        )
        self.assertEqual(evidence.schema, ARM_EVIDENCE_SCHEMA)
        coverage = evidence.coverage
        self.assertIsNotNone(coverage)
        if coverage is None:
            self.fail("with_lsp evidence must include coverage")
        self.assertEqual(coverage.diagnostics_files, ("src/a.py",))
        self.assertEqual(len(evidence.receipts), 3)
        self.assertEqual(
            validate_canonical_arm_evidence(
                evidence.to_dict(),
                changed_files=("src/a.py",),
                server_generation="generation-01",
                patch_hash=PATCH_HASH,
                final_tree_hash=FINAL_TREE_HASH,
                verifier=VERIFIER,
            ).evidence_hash,
            evidence.evidence_hash,
        )

    def test_no_lsp_canonical_evidence_contains_only_isolation_attestation(self):
        evidence = build_canonical_arm_evidence(
            mode="no_lsp",
            task="task-01",
            arm="no-lsp",
            session="session-01",
            thread="thread-01",
            receipts=(),
            isolation=_isolation(),
            patch_hash=PATCH_HASH,
            final_tree_hash=FINAL_TREE_HASH,
            verifier=VERIFIER,
        )
        self.assertEqual(evidence.receipts, ())
        self.assertIsNone(evidence.coverage)
        isolation = evidence.isolation
        self.assertIsNotNone(isolation)
        if isolation is None:
            self.fail("no_lsp evidence must include isolation")
        self.assertEqual(isolation.task, "task-01")
        self.assertEqual(evidence.to_dict()["receipts"], [])

    def test_canonical_arm_mode_cannot_mix_receipts_and_isolation(self):
        with_lsp = {
            "schema": ARM_EVIDENCE_SCHEMA,
            "mode": "with_lsp",
            "task": "task-01",
            "arm": "with-lsp",
            "session": "session-01",
            "thread": "thread-01",
            "receipts": _lsp_stream(),
            "isolation": _isolation(),
        }
        with self.assertRaisesRegex(ReceiptChainError, "cannot contain"):
            validate_canonical_arm_evidence(
                with_lsp,
                changed_files=("src/a.py",),
                patch_hash=PATCH_HASH,
                final_tree_hash=FINAL_TREE_HASH,
                verifier=VERIFIER,
            )

        no_lsp = dict(with_lsp)
        no_lsp["mode"] = "no_lsp"
        no_lsp["arm"] = "no-lsp"
        no_lsp["receipts"] = [_lsp_stream()[0]]
        with self.assertRaises(IsolationError):
            validate_canonical_arm_evidence(
                no_lsp,
                patch_hash=PATCH_HASH,
                final_tree_hash=FINAL_TREE_HASH,
                verifier=VERIFIER,
            )

    def test_canonical_arm_evidence_rejects_tampered_identity_or_unknown_summary(self):
        payload = {
            "schema": ARM_EVIDENCE_SCHEMA,
            "mode": "with_lsp",
            "task": "task-01",
            "arm": "with-lsp",
            "session": "session-01",
            "thread": "thread-01",
            "receipts": _lsp_stream(),
            "isolation": None,
        }
        payload["receipts"][0]["task"] = "other-task"
        with self.assertRaises(ReceiptIdentityError):
            validate_canonical_arm_evidence(
                payload,
                changed_files=("src/a.py",),
                patch_hash=PATCH_HASH,
                final_tree_hash=FINAL_TREE_HASH,
                verifier=VERIFIER,
            )

        payload = {
            "schema": ARM_EVIDENCE_SCHEMA,
            "mode": "no_lsp",
            "task": "task-01",
            "arm": "no-lsp",
            "session": "session-01",
            "thread": "thread-01",
            "receipts": [],
            "isolation": _isolation(),
            "coverage": {"passed": True},
        }
        with self.assertRaises(UnknownFieldError):
            validate_canonical_arm_evidence(
                payload,
                patch_hash=PATCH_HASH,
                final_tree_hash=FINAL_TREE_HASH,
                verifier=VERIFIER,
            )


if __name__ == "__main__":
    unittest.main()
