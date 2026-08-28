import json
import pathlib
import tempfile
import unittest

from scripts.grader_feedback import (
    ContradictoryReportError,
    canonical_source_hash,
    FailureKind,
    FeedbackIdentity,
    FeedbackInput,
    GraderFeedbackError,
    MissingFileError,
    Status,
    UnknownSchemaError,
    extract_grader_feedback,
    parse_grader_feedback,
    parse_test_output,
    validate_grader_feedback,
)


FIXTURES = pathlib.Path(__file__).with_name("testdata") / "grader_feedback"


class GraderFeedbackTests(unittest.TestCase):
    def fixture(self, name):
        return (FIXTURES / name).read_text(encoding="utf-8")

    def test_passed_test_name_containing_fail_is_not_failure(self):
        result = parse_test_output(self.fixture("passed_grammar_fail.txt"))
        self.assertEqual(result.status, Status.PASSED.value)
        self.assertTrue(result.passed)
        self.assertEqual(result.failing_test_ids, ())
        self.assertEqual(result.failure_kind, "")

    def test_unresolved_runner_chatter_is_feedback_unavailable(self):
        result = parse_test_output(self.fixture("runner_13398_unresolved.txt"))
        self.assertEqual(result.failure_kind, FailureKind.FEEDBACK_UNAVAILABLE.value)
        self.assertEqual(result.status, Status.UNRESOLVED.value)
        self.assertFalse(result.can_enter_code_repair_round())

    def test_pytest_failure_extracts_structured_fields_and_redacts_ansi(self):
        raw = "\x1b[31m" + self.fixture("pytest_failure.txt") + "\x1b[0m"
        result = parse_test_output(raw)
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)
        self.assertEqual(result.failing_test_ids, ("tests/test_cds.py::test_cds_grammar",))
        self.assertIn("AssertionError", result.exception_summary)
        self.assertIn("assert 'ass Foo:'", result.assertion_summary)
        self.assertTrue(result.can_enter_code_repair_round())
        self.assertNotIn("\x1b", json.dumps(result.to_dict()))

    def test_failure_categories_are_mutually_exclusive_and_infra_skips_repair(self):
        result = parse_test_output(self.fixture("infra_timeout.txt"))
        self.assertEqual(result.failure_kind, FailureKind.TIMEOUT.value)
        self.assertFalse(result.code_repair_eligible)
        self.assertFalse(result.as_runner_result()["code_repair_eligible"])

        for raw, expected in (
            ("PATCH_APPLY_FAILURE: patch apply failed at hunk 2", FailureKind.PATCH_APPLY_FAILURE.value),
            ("Infrastructure error: executor unavailable", FailureKind.INFRASTRUCTURE_ERROR.value),
            ("Evaluation runner error: command timed out after 660 seconds", FailureKind.TIMEOUT.value),
        ):
            item = parse_test_output(raw)
            self.assertEqual(item.failure_kind, expected)
            self.assertFalse(item.can_enter_code_repair_round())

    def test_agent_transport_jsonl_is_not_relocalized_as_model_failure(self):
        result = parse_test_output(self.fixture("agent_transport.jsonl"))
        self.assertEqual(result.status, Status.FAILED.value)
        self.assertEqual(result.failure_kind, FailureKind.AGENT_TRANSPORT_FAILURE.value)
        self.assertEqual(result.phase, "agent_turn")
        self.assertIsNone(result.collected_count)
        self.assertIsNone(result.executed_count)
        self.assertFalse(result.code_repair_eligible)
        self.assertIn("agent transport disconnected", result.exception_summary)
        runner = result.as_runner_result()
        self.assertEqual(runner["grader_failure_kind"], FailureKind.AGENT_TRANSPORT_FAILURE.value)
        self.assertEqual(runner["phase"], "agent_turn")
        self.assertIsNone(runner["collected_count"])

    def test_agent_transport_marker_variants_are_classified_strictly(self):
        for marker in (
            "turn.failed",
            "stream disconnected before completion",
            "DNS lookup failed",
            "TLS handshake EOF",
        ):
            with self.subTest(marker=marker):
                result = parse_test_output(marker)
                self.assertEqual(
                    result.failure_kind,
                    FailureKind.AGENT_TRANSPORT_FAILURE.value,
                )
                self.assertEqual(result.phase, "agent_turn")

    def test_collection_import_error_reports_phase_count_and_missing_symbol(self):
        result = parse_test_output(self.fixture("collection_import.txt"))
        self.assertEqual(result.status, Status.FAILED.value)
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)
        self.assertEqual(result.phase, "test")
        self.assertEqual(result.collected_count, 0)
        self.assertEqual(result.executed_count, 0)
        self.assertEqual(result.failing_test_ids, ("tests/unittest_pyreverse_writer.py",))
        self.assertEqual(result.missing_symbols, ("get_annotation",))
        self.assertTrue(result.code_repair_eligible)

    def test_setup_warning_as_error_reports_harness_phase_and_counts(self):
        result = parse_test_output(self.fixture("harness_setup_warning.txt"))
        self.assertEqual(result.status, Status.FAILED.value)
        self.assertEqual(result.failure_kind, FailureKind.TEST_HARNESS_FAILURE.value)
        self.assertEqual(result.phase, "harness")
        self.assertEqual(result.collected_count, 67)
        self.assertEqual(result.executed_count, 152)
        self.assertFalse(result.code_repair_eligible)
        self.assertIn("PytestRemovedIn8Warning", result.exception_summary)

    def test_official_unresolved_accepts_concrete_harness_failure_output(self):
        report = json.dumps({
            "schema_version": 2,
            "resolved_ids": [],
            "unresolved_ids": ["astropy__astropy-8707"],
            "error_ids": [],
            "completed_ids": ["astropy__astropy-8707"],
        })
        result = parse_grader_feedback(report, self.fixture("harness_setup_warning.txt"))
        self.assertEqual(result.status, Status.FAILED.value)
        self.assertEqual(result.failure_kind, FailureKind.TEST_HARNESS_FAILURE.value)
        self.assertEqual(result.phase, "harness")
        self.assertFalse(result.code_repair_eligible)

    def test_successful_test_description_with_timeout_word_is_not_runner_timeout(self):
        result = parse_test_output(
            "tests/test_http.py::test_timeout_parameter PASSED\n"
            "The timeout parameter can be customized. ... ok\n"
            "tests/test_http.py::test_actual_behavior FAILED\n"
            "AssertionError: wrong value\n"
            "1 failed, 1 passed\n"
        )
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)

    def test_passed_report_drops_context_only_blank_failure(self):
        report = json.dumps({
            "schema_version": 2,
            "resolved_ids": ["sphinx-doc__sphinx-7462"],
            "unresolved_ids": [],
            "error_ids": [],
            "completed_ids": ["sphinx-doc__sphinx-7462"],
        })
        result = parse_grader_feedback(
            report,
            "/testbed/sphinx/ext/todo.py:98\n1 passed\n",
        )
        self.assertTrue(result.passed)
        self.assertEqual(result.failures, ())
        self.assertEqual(result.phase, "test")

    def test_text_line_zero_is_treated_as_unknown_location(self):
        result = parse_test_output(
            "test_id: tests/test_example.py::test_failure\n"
            "line: 0\n"
            "AssertionError: wrong value\n"
            "1 failed\n"
        )
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)
        self.assertEqual(result.failures[0].line, None)

    def test_source_code_assignment_named_line_is_not_location_metadata(self):
        result = parse_test_output(
            "line = line.split('#', 1)[0].strip()\n"
            "tests/test_example.py::test_failure FAILED\n"
            "AssertionError: wrong value\n"
            "1 failed\n"
        )
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)

    def test_nested_pytest_sessions_do_not_conflict_on_collected_counts(self):
        result = parse_test_output(
            "collected 0 items\n"
            "collected 1 item\n"
            "1 passed in 0.01s\n"
            "collected 3 items\n"
            "tests/test_example.py::test_failure FAILED\n"
            "AssertionError: wrong value\n"
            "2 failed, 51 passed, 1 skipped\n"
        )
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)
        self.assertEqual(result.collected_count, 3)
        self.assertGreaterEqual(result.executed_count or 0, 51)

    def test_nested_collection_error_does_not_override_executed_main_tests(self):
        result = parse_test_output(
            "collected 0 items / 1 error\n"
            "ImportError: expected nested fixture error\n"
            "collected 54 items\n"
            "FAILED tests/test_main.py::test_failure\n"
            "AssertionError: wrong value\n"
            "2 failed, 51 passed, 1 skipped\n"
        )
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)
        self.assertEqual(result.phase, "test")

    def test_nested_setup_error_does_not_override_outer_assertion_failure(self):
        result = parse_test_output(
            "ERROR at setup of nested_fixture_test\n"
            "collected 54 items\n"
            "FAILED tests/test_main.py::test_failure\n"
            "AssertionError: wrong value\n"
            "2 failed, 51 passed, 1 skipped\n"
        )
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)
        self.assertEqual(result.phase, "test")

    def test_structured_phase_counts_and_missing_symbols_are_preserved(self):
        raw = json.dumps(
            {
                "schema": "super-dolphin/grader-feedback/v1",
                "schema_version": 1,
                "status": "failed",
                "phase": "collection",
                "collected_count": 0,
                "executed_count": 0,
                "missing_symbols": ["infer_node"],
                "failure": {
                    "kind": "TEST_COLLECTION_IMPORT_ERROR",
                    "failing_test_ids": ["tests/test_pyreverse.py"],
                    "exception": "ImportError",
                },
            }
        )
        result = parse_grader_feedback(raw)
        self.assertEqual(result.failure_kind, FailureKind.TEST_COLLECTION_IMPORT_ERROR.value)
        self.assertEqual(result.phase, "collection")
        self.assertEqual(result.collected_count, 0)
        self.assertEqual(result.executed_count, 0)
        self.assertEqual(result.missing_symbols, ("infer_node",))
        self.assertFalse(result.code_repair_eligible)

    def test_new_failure_kinds_keep_strict_source_provenance(self):
        raw = self.fixture("agent_transport.jsonl")
        result = parse_grader_feedback(
            raw,
            strict_provenance=True,
            source_artifact="agent.log",
            source_hash=canonical_source_hash(raw, None),
        )
        self.assertEqual(result.failure_kind, FailureKind.AGENT_TRANSPORT_FAILURE.value)
        with self.assertRaises(ContradictoryReportError):
            parse_grader_feedback(
                raw,
                strict_provenance=True,
                source_artifact="agent.log",
                source_hash="a" * 64,
            )

    def test_official_json_report_preserves_source_identity_and_deduplicates(self):
        result = extract_grader_feedback(FeedbackInput(report_path=FIXTURES / "official_model_report.json"))
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)
        self.assertEqual(result.source_artifact, "candidate.patch")
        self.assertEqual(len(result.failing_test_ids), 1)
        self.assertEqual(result.source_hash, "a" * 64)

    def test_real_swebench_v2_report_without_schema_is_unresolved(self):
        result = extract_grader_feedback(
            FeedbackInput(report_path=FIXTURES / "official_swebench_v2_report.json")
        )
        self.assertEqual(result.status, Status.UNRESOLVED.value)
        self.assertEqual(result.failure_kind, FailureKind.FEEDBACK_UNAVAILABLE.value)
        self.assertFalse(result.passed)
        self.assertEqual(result.source_artifact, "candidate.patch")
        self.assertEqual(result.source_hash, "b" * 64)

    def test_real_swebench_v2_error_ids_are_infrastructure(self):
        raw = json.dumps({
            "schema_version": 2,
            "resolved_ids": [],
            "unresolved_ids": [],
            "error_ids": ["astropy__astropy-13398"],
            "completed_ids": [],
        })
        result = parse_grader_feedback(raw)
        self.assertEqual(result.failure_kind, FailureKind.INFRASTRUCTURE_ERROR.value)
        self.assertFalse(result.can_enter_code_repair_round())

    def test_swebench_resolved_true_is_success_even_when_test_output_is_noisy(self):
        report = json.dumps({
            "instance_id": "task-resolved",
            "resolved": True,
            "completed": True,
        })
        result = parse_grader_feedback(
            report,
            "FAILED tests/stale.py::test_old - AssertionError: stale output\n1 failed\n",
        )
        self.assertEqual(result.status, Status.PASSED.value)
        self.assertTrue(result.passed)
        self.assertEqual(result.failure_kind, "")
        self.assertEqual(result.official_resolved, True)
        self.assertEqual(result.official_completed, True)
        self.assertEqual(result.official_instance_id, "task-resolved")

    def test_runner_mapping_preserves_official_outcome_fields(self):
        identity = {
            "question_id": "task-resolved",
            "arm_id": "with-lsp",
            "session_id": "session-1",
            "thread_id": "thread-1",
            "patch_hash": "a" * 64,
        }
        result = validate_grader_feedback(
            {
                "schema": "super-dolphin/grader-feedback/v1",
                "status": "PASSED",
                "passed": True,
                "failure_kind": "",
                "identity": identity,
                "official_resolved": True,
                "official_completed": True,
                "official_instance_id": "task-resolved",
            },
            **identity,
        )
        self.assertIs(result.official_resolved, True)
        self.assertIs(result.official_completed, True)
        self.assertEqual(result.official_instance_id, "task-resolved")

    def test_swebench_aggregate_resolved_ids_are_success_truth(self):
        report = json.dumps({
            "schema_version": 2,
            "resolved_ids": ["task-resolved"],
            "unresolved_ids": [],
            "error_ids": [],
            "completed_ids": ["task-resolved"],
        })
        result = parse_grader_feedback(
            report,
            "{malformed stale test output",
        )
        self.assertEqual(result.status, Status.PASSED.value)
        self.assertTrue(result.passed)

    def test_swebench_resolved_false_completed_is_model_failure_without_traceback(self):
        report = json.dumps({
            "instance_id": "task-failed",
            "resolved": False,
            "completed": True,
        })
        result = parse_grader_feedback(report, "")
        self.assertEqual(result.status, Status.FAILED.value)
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)
        self.assertTrue(result.can_enter_code_repair_round())
        self.assertEqual(result.failing_test_ids, ())

    def test_swebench_unresolved_without_completion_remains_unverified(self):
        report = json.dumps({
            "instance_id": "task-pending",
            "resolved": False,
            "completed": False,
        })
        result = parse_grader_feedback(report, "1 passed\n")
        self.assertEqual(result.status, Status.UNRESOLVED.value)
        self.assertEqual(result.failure_kind, FailureKind.FEEDBACK_UNAVAILABLE.value)
        self.assertFalse(result.passed)

    def test_missing_report_and_output_is_unverified(self):
        result = parse_grader_feedback(None, None)
        self.assertEqual(result.status, Status.UNRESOLVED.value)
        self.assertEqual(result.failure_kind, FailureKind.FEEDBACK_UNAVAILABLE.value)
        self.assertFalse(result.passed)

    def test_official_infrastructure_error_does_not_enter_model_repair(self):
        report = json.dumps({
            "instance_id": "task-infra",
            "resolved": False,
            "completed": True,
            "error": "container unavailable",
        })
        result = parse_grader_feedback(report)
        self.assertEqual(result.status, Status.FAILED.value)
        self.assertEqual(result.failure_kind, FailureKind.INFRASTRUCTURE_ERROR.value)
        self.assertFalse(result.can_enter_code_repair_round())

    def test_swebench_unresolved_plus_pytest_failure_is_model_feedback(self):
        report = json.dumps({
            "schema_version": 2,
            "resolved_ids": [],
            "unresolved_ids": ["astropy__astropy-14369"],
            "error_ids": [],
            "completed_ids": ["astropy__astropy-14369"],
        })
        result = parse_grader_feedback(
            report,
            "FAILED tests/test_cds.py::test_cds - AssertionError: wrong unit\n"
            "E   AssertionError: wrong unit\n",
        )
        self.assertEqual(result.status, Status.FAILED.value)
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)
        self.assertTrue(result.can_enter_code_repair_round())

    def test_unknown_schema_missing_file_and_contradiction_fail_fast(self):
        with self.assertRaises(UnknownSchemaError):
            parse_grader_feedback('{"schema":"grader/v9","status":"failed"}')
        with self.assertRaises(MissingFileError):
            extract_grader_feedback(FeedbackInput(report_path=FIXTURES / "missing.json"))
        with self.assertRaises(ContradictoryReportError):
            parse_grader_feedback(
                "status: failed\nfailure_kind: timeout\n",
                "FAILED tests/test_x.py::test_x\n",
            )

    def test_bounded_and_secret_safe_summary(self):
        result = parse_test_output(
            "FAILED tests/test_x.py::test_x - AssertionError: token=secret-value\n"
            "E   AssertionError: token=secret-value\n"
        )
        self.assertNotIn("secret-value", result.exception_summary)
        self.assertLessEqual(len(result.exception_summary), 4096)

    def test_pip_dependency_error_is_not_a_failing_test_id(self):
        result = parse_test_output(
            "ERROR: pip's dependency resolver does not currently take into account all packages\n"
            "FAILED tests/test_units.py::test_units - AssertionError: wrong unit\n"
        )
        self.assertEqual(result.failing_test_ids, ("tests/test_units.py::test_units",))

    def test_structured_envelope_binds_identity_and_preserves_failure_details(self):
        result = extract_grader_feedback(
            FeedbackInput(report_path=FIXTURES / "structured_model_failure.json")
        )
        self.assertEqual(result.status, Status.FAILED.value)
        self.assertEqual(result.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)
        self.assertEqual(result.question_id, "sphinx-doc__sphinx-10323")
        self.assertEqual(result.arm_id, "with-lsp")
        self.assertEqual(result.session_id, "session-10323")
        self.assertEqual(result.thread_id, "thread-10323")
        self.assertEqual(result.patch_hash, "a" * 64)
        self.assertEqual(len(result.failures), 1)
        failure = result.failures[0]
        self.assertEqual(failure.file, "tests/test_directive_code.py")
        self.assertEqual(failure.line, 255)
        self.assertEqual(failure.exception, "AssertionError")
        self.assertEqual(failure.actual, "'ass Foo:'")
        self.assertEqual(failure.expected, "'class Foo:'")
        self.assertIn("AssertionError: normalized output differs", failure.traceback_tail)
        self.assertNotIn("Requirement already satisfied", "\n".join(result.evidence))
        envelope = result.as_runner_result()
        self.assertEqual(envelope["grader_failure_kind"], FailureKind.MODEL_TEST_FAILURE.value)
        self.assertEqual(envelope["question_id"], result.question_id)
        self.assertEqual(envelope["patch_hash"], result.patch_hash)

    def test_structured_identity_must_match_expected_runner_identity(self):
        kwargs = {
            "question_id": "sphinx-doc__sphinx-10323",
            "arm_id": "with-lsp",
            "session_id": "session-10323",
            "thread_id": "thread-10323",
            "patch_hash": "a" * 64,
        }
        result = extract_grader_feedback(
            FeedbackInput(report_path=FIXTURES / "structured_model_failure.json", **kwargs)
        )
        self.assertTrue(result.can_enter_code_repair_round())
        with self.assertRaises(ContradictoryReportError):
            extract_grader_feedback(
                FeedbackInput(
                    report_path=FIXTURES / "structured_model_failure.json",
                    **{**kwargs, "arm_id": "no-lsp"},
                )
            )
        # Raw output has no declared identity, so an expected runner identity
        # must not be silently manufactured into evidence.
        with self.assertRaises(ContradictoryReportError):
            parse_grader_feedback(
                self.fixture("pytest_failure.txt"),
                identity=FeedbackIdentity(
                    question_id="q-1",
                    arm_id="with-lsp",
                    session_id="s-1",
                    thread_id="t-1",
                    patch_hash="b" * 64,
                ),
            )

    def test_failure_name_without_actionable_evidence_is_feedback_unavailable(self):
        result = parse_test_output(self.fixture("feedback_unavailable_only_failure.txt"))
        self.assertEqual(result.status, Status.UNRESOLVED.value)
        self.assertEqual(result.failure_kind, FailureKind.FEEDBACK_UNAVAILABLE.value)
        self.assertEqual(result.failing_test_ids, ("tests/test_units.py::test_units",))
        self.assertFalse(result.can_enter_code_repair_round())

    def test_partial_identity_fails_closed(self):
        with self.assertRaises(GraderFeedbackError):
            parse_grader_feedback(
                '{"schema":"super-dolphin/grader-feedback/v1",'
                '"status":"unresolved",'
                '"identity":{"question_id":"q-only"}}'
            )

    def test_failed_status_without_typed_failure_is_unresolved(self):
        result = parse_grader_feedback(
            '{"schema":"super-dolphin/grader-feedback/v1",'
            '"status":"failed",'
            '"tests":{"failing_test_ids":["tests/test_x.py::test_x"]}}'
        )
        self.assertEqual(result.status, Status.UNRESOLVED.value)
        self.assertEqual(result.failure_kind, FailureKind.FEEDBACK_UNAVAILABLE.value)
        self.assertFalse(result.can_enter_code_repair_round())

    def test_runner_validator_accepts_envelope_and_rejects_bool(self):
        result = extract_grader_feedback(
            FeedbackInput(report_path=FIXTURES / "structured_model_failure.json")
        )
        bound = validate_grader_feedback(
            result.as_runner_result(),
            question_id=result.question_id,
            arm_id=result.arm_id,
            session_id=result.session_id,
            thread_id=result.thread_id,
            patch_hash=result.patch_hash,
        )
        self.assertEqual(bound.failure_kind, FailureKind.MODEL_TEST_FAILURE.value)
        raw_envelope = {
            "schema": "super-dolphin/grader-feedback/v1",
            "status": "failed",
            "identity": {
                "question_id": result.question_id,
                "arm_id": result.arm_id,
                "session_id": result.session_id,
                "thread_id": result.thread_id,
                "patch_hash": result.patch_hash,
            },
            "failure": {
                "kind": "MODEL_TEST_FAILURE",
                "failing_test_ids": list(result.failing_test_ids),
                "exception": result.exception_summary,
                "assertion": result.assertion_summary,
            },
        }
        self.assertEqual(
            validate_grader_feedback(
                raw_envelope,
                question_id=result.question_id,
                arm_id=result.arm_id,
                session_id=result.session_id,
                thread_id=result.thread_id,
                patch_hash=result.patch_hash,
            ).failure_kind,
            FailureKind.MODEL_TEST_FAILURE.value,
        )
        with self.assertRaises(GraderFeedbackError):
            validate_grader_feedback(
                False,
                question_id=result.question_id,
                arm_id=result.arm_id,
                session_id=result.session_id,
                thread_id=result.thread_id,
                patch_hash=result.patch_hash,
            )

    def test_strict_provenance_recomputes_report_hash_for_success_and_failure(self):
        failure_raw = self.fixture("pytest_failure.txt")
        failure_hash = canonical_source_hash(failure_raw, None)
        failure = parse_grader_feedback(
            failure_raw,
            strict_provenance=True,
            source_artifact="official-test-output.txt",
            source_hash=failure_hash,
        )
        self.assertEqual(failure.status, Status.FAILED.value)
        self.assertEqual(failure.source_artifact, "official-test-output.txt")
        self.assertEqual(failure.source_hash, failure_hash)

        success_raw = self.fixture("passed_grammar_fail.txt")
        success_hash = canonical_source_hash(None, success_raw)
        success = parse_grader_feedback(
            None,
            success_raw,
            strict_provenance=True,
            source_artifact="official-pass-output.txt",
            source_hash=success_hash,
        )
        self.assertEqual(success.status, Status.PASSED.value)
        self.assertTrue(success.source_artifact)
        self.assertEqual(success.source_hash, success_hash)
        with self.assertRaises(ContradictoryReportError):
            parse_grader_feedback(
                failure_raw,
                strict_provenance=True,
                source_artifact="official-test-output.txt",
                source_hash="a" * 64,
            )

    def test_strict_adapter_rejects_typed_object_and_requires_original_source(self):
        raw = self.fixture("structured_model_failure.json")
        parsed = extract_grader_feedback(FeedbackInput(report_path=FIXTURES / "structured_model_failure.json"))
        with self.assertRaises(GraderFeedbackError):
            validate_grader_feedback(
                parsed,
                question_id=parsed.question_id,
                arm_id=parsed.arm_id,
                session_id=parsed.session_id,
                thread_id=parsed.thread_id,
                patch_hash=parsed.patch_hash,
                strict_provenance=True,
                official_report=raw,
            )

        envelope = parsed.as_runner_result()
        nested = dict(envelope["grader_feedback"])
        nested.pop("source_artifact", None)
        nested.pop("source_hash", None)
        envelope["grader_feedback"] = nested
        bound = validate_grader_feedback(
            envelope,
            question_id=parsed.question_id,
            arm_id=parsed.arm_id,
            session_id=parsed.session_id,
            thread_id=parsed.thread_id,
            patch_hash=parsed.patch_hash,
            strict_provenance=True,
            official_report=raw,
        )
        self.assertTrue(bound.source_artifact)
        self.assertEqual(bound.source_hash, canonical_source_hash(raw, None))
        with self.assertRaises(GraderFeedbackError):
            validate_grader_feedback(
                envelope,
                question_id=parsed.question_id,
                arm_id=parsed.arm_id,
                session_id=parsed.session_id,
                thread_id=parsed.thread_id,
                patch_hash=parsed.patch_hash,
                strict_provenance=True,
            )


if __name__ == "__main__":
    unittest.main()
