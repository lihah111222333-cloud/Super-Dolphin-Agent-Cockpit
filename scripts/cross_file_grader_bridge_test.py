import hashlib
import json
import pathlib
import tempfile
import unittest

from scripts.cross_file_grader_bridge import (
    GraderBridgeInputError,
    GraderBridgeProvenanceError,
    build_driver_feedback_envelope,
    build_grader_artifact,
    build_signed_feedback,
    validate_grader_artifact_hashes,
    validate_official_grader_sources,
    write_feedback_file,
)
from scripts.grader_feedback import canonical_source_hash
from scripts.cross_file_signed_receipts import (
    Ed25519Signer,
    Ed25519Verifier,
    SignatureVerificationError,
    validate_grader_receipt,
)


class GraderBridgeTest(unittest.TestCase):
    def setUp(self):
        self.signer = Ed25519Signer.generate("grader-bridge-test")
        self.common = {
            "task": "django__django-16560",
            "arm": "with-lsp",
            "session": "session-16560",
            "thread": "thread-16560",
            "patch_hash": "a" * 64,
            "final_tree_hash": "b" * 40,
            "request_id": "grader-request-1",
            "seq": 1,
            "signer": self.signer,
        }
        self.report = json.dumps(
            {
                "schema_version": 2,
                "resolved_ids": [],
                "unresolved_ids": ["django__django-16560"],
                "error_ids": [],
                "completed_ids": ["django__django-16560"],
            },
            sort_keys=True,
        )
        self.test_output = (
            "collected 1 item\n"
            "FAILED tests/test_models.py::test_name - AssertionError: wrong name\n"
            "E   AssertionError: wrong name\n"
            "1 failed\n"
        )

    def test_real_four_surfaces_are_hashed_and_receipt_is_verifiable(self):
        stdout = b"pytest version 8.0\n"
        stderr = b""
        signed = build_signed_feedback(
            **self.common,
            report=self.report,
            test_output=self.test_output,
            stdout=stdout,
            stderr=stderr,
        )
        receipt = signed["receipt"]
        result = receipt["result"]
        hashes = result["grader_artifact_hashes"]
        self.assertEqual(hashes["report"], hashlib.sha256(self.report.encode()).hexdigest())
        self.assertEqual(
            hashes["test_output"], hashlib.sha256(self.test_output.encode()).hexdigest()
        )
        self.assertEqual(hashes["stdout"], hashlib.sha256(stdout).hexdigest())
        self.assertEqual(hashes["stderr"], hashlib.sha256(stderr).hexdigest())
        self.assertEqual(receipt["final_tree_hash"], "b" * 40)
        self.assertEqual(receipt["patch_hash"], "a" * 64)
        verified = validate_grader_receipt(
            receipt,
            verifier=Ed25519Verifier(self.signer.public_key),
            task=self.common["task"],
            arm=self.common["arm"],
            session=self.common["session"],
            thread=self.common["thread"],
            patch_hash=self.common["patch_hash"],
            final_tree_hash=self.common["final_tree_hash"],
            grader_artifact=receipt["grader_artifact"],
        )
        self.assertEqual(verified["failure_kind"], "MODEL_TEST_FAILURE")
        self.assertTrue(verified["code_repair_eligible"])

    def test_resolved_report_is_not_reversed_by_noisy_test_output(self):
        report = json.dumps({
            "schema_version": 2,
            "resolved_ids": [self.common["task"]],
            "unresolved_ids": [],
            "error_ids": [],
            "completed_ids": [self.common["task"]],
        })
        signed = build_signed_feedback(
            **self.common,
            report=report,
            test_output=(
                "FAILED baseline/test_old.py::test_old - AssertionError\n"
                "/testbed/pkg/source.py:98\n1 failed\n"
            ),
            stdout=b"",
            stderr=b"",
        )
        result = signed["receipt"]["result"]
        self.assertTrue(result["passed"])
        self.assertEqual(result["status"], "PASSED")
        self.assertEqual(result["failures"], [])

    def test_single_instance_resolved_true_is_success_truth(self):
        report = json.dumps({
            "instance_id": self.common["task"],
            "resolved": True,
            "completed": True,
        })
        signed = build_signed_feedback(
            **self.common,
            report=report,
            test_output="FAILED stale/test.py::test_old - AssertionError: stale\n",
            stdout=b"",
            stderr=b"",
        )
        result = signed["receipt"]["result"]
        self.assertEqual(result["status"], "PASSED")
        self.assertTrue(result["passed"])
        self.assertEqual(result["failure_kind"], "")
        self.assertEqual(result["official_resolved"], True)
        self.assertEqual(result["official_completed"], True)
        self.assertEqual(
            result["official_source_hash"],
            canonical_source_hash(report, "FAILED stale/test.py::test_old - AssertionError: stale\n"),
        )

    def test_single_instance_resolved_false_completed_is_model_failure(self):
        report = json.dumps({
            "instance_id": self.common["task"],
            "resolved": False,
            "completed": True,
        })
        signed = build_signed_feedback(
            **self.common,
            report=report,
            test_output="",
            stdout=b"",
            stderr=b"",
        )
        result = signed["receipt"]["result"]
        self.assertEqual(result["failure_kind"], "MODEL_TEST_FAILURE")
        self.assertTrue(result["code_repair_eligible"])

    def test_missing_official_report_is_signed_unverified_not_model_failure(self):
        signed = build_signed_feedback(
            **self.common,
            report=None,
            test_output="FAILED tests/test_x.py::test_x - AssertionError: noisy\n",
            stdout=b"",
            stderr=b"",
        )
        result = signed["receipt"]["result"]
        self.assertEqual(result["failure_kind"], "FEEDBACK_UNAVAILABLE")
        self.assertEqual(result["status"], "UNRESOLVED")
        self.assertFalse(result["code_repair_eligible"])

    def test_official_grader_error_is_infrastructure(self):
        report = json.dumps({
            "instance_id": self.common["task"],
            "resolved": False,
            "completed": True,
            "error": "container unavailable",
        })
        signed = build_signed_feedback(
            **self.common,
            report=report,
            test_output="",
            stdout=b"",
            stderr=b"",
        )
        result = signed["receipt"]["result"]
        self.assertEqual(result["failure_kind"], "INFRASTRUCTURE_ERROR")
        self.assertFalse(result["code_repair_eligible"])

    def test_official_grader_error_needs_no_test_output(self):
        report = json.dumps({"error": "executor unavailable"})
        signed = build_signed_feedback(
            **self.common,
            report=report,
            test_output=None,
            stdout=b"",
            stderr=b"",
        )
        result = signed["receipt"]["result"]
        self.assertEqual(result["failure_kind"], "INFRASTRUCTURE_ERROR")

    def test_stdout_stderr_can_classify_runner_failure_but_not_create_model_failure(self):
        signed = build_signed_feedback(
            **self.common,
            report=json.dumps({
                "schema_version": 2,
                "resolved_ids": [],
                "unresolved_ids": [self.common["task"]],
                "error_ids": [],
                "completed_ids": [],
            }),
            test_output="runner unresolved: no structured test result\n",
            stdout="",
            stderr="executor infrastructure error: container unavailable\n",
        )
        result = signed["receipt"]["result"]
        self.assertEqual(result["failure_kind"], "INFRASTRUCTURE_ERROR")
        self.assertFalse(result["code_repair_eligible"])
        source_feedback = validate_official_grader_sources(
            json.dumps({
                "schema_version": 2,
                "resolved_ids": [],
                "unresolved_ids": [self.common["task"]],
                "error_ids": [],
                "completed_ids": [],
            }),
            "runner unresolved: no structured test result\n",
            stdout="",
            stderr="executor infrastructure error: container unavailable\n",
        )
        self.assertEqual(source_feedback.failure_kind, "INFRASTRUCTURE_ERROR")

    def test_driver_envelope_shape_is_exact_and_feedback_file_is_atomic(self):
        envelope = build_driver_feedback_envelope(
            **self.common,
            report=self.report,
            test_output=self.test_output,
            stdout="grader stdout",
            stderr="grader stderr",
        )
        self.assertEqual(set(envelope), {"feedback", "official_report", "official_test_output"})
        self.assertEqual(set(envelope["feedback"]), {"receipt"})
        with tempfile.TemporaryDirectory() as directory:
            destination = pathlib.Path(directory) / "feedback.json"
            write_feedback_file(destination, envelope["feedback"])
            self.assertEqual(
                json.loads(destination.read_text())["receipt"]["schema"],
                "super-dolphin/grader-receipt/v1",
            )

    def test_missing_official_surface_or_drifted_tree_fails_closed(self):
        with self.assertRaises(GraderBridgeInputError):
            build_signed_feedback(
                **self.common,
                report=self.report,
                test_output="",
            )
        with self.assertRaises(GraderBridgeInputError):
            build_signed_feedback(
                **{**self.common, "final_tree_hash": "not-a-tree"},
                report=self.report,
                test_output=self.test_output,
            )

    def test_artifact_manifest_is_stable_for_same_raw_bytes(self):
        first = build_grader_artifact(
            task="task-1",
            request_id="request-1",
            report=b"report\r\n",
            test_output=b"test",
            stdout=None,
            stderr=b"",
        )
        second = build_grader_artifact(
            task="task-1",
            request_id="request-1",
            report=b"report\r\n",
            test_output=b"test",
            stdout=None,
            stderr=b"",
        )
        self.assertEqual(first.binding, second.binding)
        self.assertFalse(first.to_dict()["sources"]["stdout"]["present"])
        self.assertEqual(first.hashes()["report"], hashlib.sha256(b"report\r\n").hexdigest())

    def test_executor_hashes_are_independent_of_receipt_artifact(self):
        artifact = build_grader_artifact(
            task="task-1",
            request_id="request-1",
            report=b"report",
            test_output=b"test",
            stdout=b"stdout",
            stderr=b"stderr",
        )
        result = {"grader_artifact_hashes": artifact.hashes()}
        validate_grader_artifact_hashes(result, artifact)
        result["grader_artifact_hashes"] = dict(artifact.hashes(), report="0" * 64)
        with self.assertRaisesRegex(Exception, "source hashes drifted"):
            validate_grader_artifact_hashes(result, artifact)

    def test_receipt_signed_by_wrong_role_key_is_rejected(self):
        wrong_role = Ed25519Signer.generate("executor-role-key")
        signed = build_signed_feedback(
            **{**self.common, "signer": wrong_role},
            report=self.report,
            test_output=self.test_output,
            stdout=b"stdout",
            stderr=b"stderr",
        )
        receipt = signed["receipt"]
        with self.assertRaises(SignatureVerificationError):
            validate_grader_receipt(
                receipt,
                verifier=Ed25519Verifier(self.signer.public_key),
                task=self.common["task"],
                arm=self.common["arm"],
                session=self.common["session"],
                thread=self.common["thread"],
                patch_hash=self.common["patch_hash"],
                final_tree_hash=self.common["final_tree_hash"],
                grader_artifact=receipt["grader_artifact"],
            )


if __name__ == "__main__":
    unittest.main()
