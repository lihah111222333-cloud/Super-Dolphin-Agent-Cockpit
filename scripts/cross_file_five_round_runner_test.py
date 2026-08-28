import importlib.util
import dataclasses
import hashlib
import pathlib
import sys
import tempfile
import unittest

from scripts.grader_feedback import FailureEvidence, FailureKind as FeedbackFailureKind, GraderFeedback, Status as FeedbackStatus


ROOT = pathlib.Path(__file__).parent


def load_module(name: str, path: pathlib.Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {name}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


class FiveRoundRunnerTest(unittest.TestCase):
    def setUp(self):
        self.g = load_module(
            "cross_file_closed_loop_guard",
            ROOT / "cross_file_closed_loop_guard.py",
        )
        self.runner_module = load_module(
            "cross_file_five_round_runner",
            ROOT / "cross_file_five_round_runner.py",
        )

    @staticmethod
    def diagnostics(*paths):
        return [
            {
                "type": "mcp_tool_call",
                "tool": "diagnostics",
                "arguments": {"file_paths": list(paths)},
            }
        ]

    def semantic_completion(self, *paths):
        if not paths:
            return []
        first = paths[0]
        return [
            {
                "type": "mcp_tool_call",
                "tool": "structure",
                "arguments": {"action": "document_symbol", "file_path": first},
            },
            {
                "type": "mcp_tool_call",
                "tool": "xref",
                "arguments": {"action": "references", "pos": f"{first}:1:1"},
            },
            *self.diagnostics(*paths),
        ]

    def request(
        self,
        round_number=1,
        *,
        patch_hash="h1",
        files=("src/a.py",),
        trajectory=None,
        session_id="session-1",
        thread_id="thread-1",
        code_agent_id="with-lsp-agent",
        action=None,
        **kwargs,
    ):
        if trajectory is None:
            trajectory = self.semantic_completion(*files)
        if len(patch_hash) != 64 or any(character not in "0123456789abcdefABCDEF" for character in patch_hash):
            patch_hash = hashlib.sha256(patch_hash.encode("utf-8")).hexdigest()
        values = dict(
            round_number=round_number,
            arm_id="with-lsp",
            session_id=session_id,
            thread_id=thread_id,
            code_agent_id=code_agent_id,
            patch_hash=patch_hash,
            changed_files=files,
            trajectory=trajectory,
            mode="with_lsp",
            action=action or ("start" if round_number == 1 else "resume"),
        )
        values.update(kwargs)
        return self.g.RoundInput(**values)

    @staticmethod
    def feedback(
        request,
        *,
        passed=False,
        kind=FeedbackFailureKind.MODEL_TEST_FAILURE.value,
        missing_symbols=(),
        phase="",
    ):
        question_id = "question-1"
        if passed:
            return GraderFeedback(
                status=FeedbackStatus.PASSED.value,
                failure_kind="",
                passed=True,
                question_id=question_id,
                arm_id=request.arm_id,
                session_id=request.session_id,
                thread_id=request.thread_id,
                patch_hash=request.patch_hash,
            )
        if kind in {FeedbackFailureKind.FEEDBACK_UNAVAILABLE.value}:
            status = FeedbackStatus.UNRESOLVED.value
            failures = ()
        else:
            status = FeedbackStatus.FAILED.value
            failures = (
                FailureEvidence(
                    test_id="tests/test_x.py::test_x",
                    file="tests/test_x.py",
                    line=1,
                    exception="AssertionError",
                    message="wrong result",
                    actual="1",
                    expected="2",
                    traceback_tail="AssertionError: wrong result",
                ),
            )
        return GraderFeedback(
            status=status,
            failure_kind=kind,
            failing_test_ids=("tests/test_x.py::test_x",) if failures else (),
            failures=failures,
            question_id=question_id,
            arm_id=request.arm_id,
            session_id=request.session_id,
            thread_id=request.thread_id,
            patch_hash=request.patch_hash,
            phase=phase,
            missing_symbols=tuple(missing_symbols),
            structured=bool(failures),
            code_repair_eligible=kind == FeedbackFailureKind.MODEL_TEST_FAILURE.value,
        )

    def test_strict_three_quality_rounds_and_scoreable_terminal_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(state, question_id="question-1")

            for number in range(1, 4):
                request = self.request(
                    number,
                    patch_hash=f"h{number}",
                    files=(f"src/{number}.py",),
                )
                step = runner.submit_quality_round(
                    request,
                    lambda _, request=request: self.feedback(request),
                )
                self.assertFalse(step.grader.quality_accepted)

            snapshot = runner.snapshot()
            self.assertEqual(snapshot.status, self.g.RunnerStatus.MAX_ROUNDS_REACHED.value)
            self.assertTrue(snapshot.terminal)
            self.assertTrue(snapshot.scoreable)
            self.assertFalse(snapshot.passed)
            self.assertEqual(snapshot.last_round, 3)
            self.assertEqual(snapshot.quality_rounds_used, 3)
            self.assertEqual(len(runner.events), 6)
            with self.assertRaises(self.runner_module.PendingActionError):
                runner.submit_quality_round(
                    self.request(6, patch_hash="h6", files=("src/6.py",)),
                    lambda _, request=self.request(6, patch_hash="h6", files=("src/6.py",)): self.feedback(request, passed=True),
                )

    def test_zero_lsp_calls_with_production_changes_are_not_scoreable(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            first = self.runner_module.FiveRoundRunner(state, question_id="question-1")
            request = self.request(
                files=("src/a.py", "src/b.py"),
                trajectory=(),
            )
            completed = first.submit_quality_round(
                request,
                lambda _, request=request: self.feedback(request, passed=True),
            )
            self.assertIsNone(completed.grader)
            self.assertEqual(
                completed.outcome.decision,
                self.g.RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value,
            )
            self.assertFalse(completed.outcome.grader_allowed)

            restored = self.runner_module.FiveRoundRunner.from_store(
                state, question_id="question-1"
            )
            self.assertEqual(
                restored.snapshot().status,
                self.g.RunnerStatus.EVIDENCE_RECOVERY_REQUIRED.value,
            )

    def test_partial_legacy_lsp_coverage_cannot_bypass_semantic_gate(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(state, question_id="question-1")
            request = self.request(
                files=("src/a.py", "src/b.py"),
                trajectory=self.diagnostics("src/a.py"),
            )
            completed = runner.submit_quality_round(
                request,
                lambda _, request=request: self.feedback(request, passed=True),
            )
            self.assertIsNone(completed.grader)
            self.assertEqual(
                completed.outcome.decision,
                self.g.RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value,
            )
            self.assertFalse(completed.outcome.grader_allowed)
            self.assertEqual([event.kind for event in runner.events], ["quality_round"])

    def test_infrastructure_retry_is_same_round_and_does_not_consume_quality_budget(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(state, max_infra_retries=1, question_id="question-1")
            first = runner.submit_quality_round(
                self.request(),
                lambda _: (_ for _ in ()).throw(RuntimeError("grader transport")),
            )
            self.assertTrue(first.needs_infra_retry)
            self.assertEqual(runner.snapshot().status, self.g.RunnerStatus.INFRASTRUCTURE_FAILURE.value)
            self.assertEqual(runner.snapshot().quality_rounds_used, 0)
            self.assertFalse(runner.snapshot().scoreable)

            retry_request = self.request(round_number=1, action="resume")
            accepted = runner.retry_infrastructure(
                retry_request,
                lambda _, request=retry_request: self.feedback(request, passed=True),
            )
            self.assertTrue(accepted.grader.quality_accepted)
            self.assertEqual(runner.snapshot().status, self.g.RunnerStatus.SUCCEEDED.value)
            self.assertEqual(runner.snapshot().last_round, 1)
            self.assertEqual(runner.snapshot().quality_rounds_used, 1)
            self.assertEqual(
                [event.kind for event in runner.events],
                ["quality_round", "grader_result", "infra_retry", "grader_result"],
            )
            self.assertEqual({event.round_number for event in runner.events}, {1})
            self.assertEqual(runner.events[2].attempt, 2)

    def test_exhausted_infrastructure_is_unverified_and_never_scoreable(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(state, max_infra_retries=1, question_id="question-1")
            fail = lambda _: (_ for _ in ()).throw(RuntimeError("transport"))
            first = runner.submit_quality_round(self.request(), fail)
            self.assertTrue(first.needs_infra_retry)
            second = runner.retry_infrastructure(
                self.request(round_number=1, action="resume"),
                fail,
            )
            self.assertTrue(second.terminal)
            self.assertEqual(runner.snapshot().status, self.g.RunnerStatus.UNVERIFIED.value)
            self.assertFalse(runner.snapshot().scoreable)
            self.assertFalse(runner.snapshot().passed)
            self.assertEqual(runner.snapshot().quality_rounds_used, 0)
            self.assertEqual(runner.events[-1].kind, "terminal")
            with self.assertRaises(self.runner_module.PendingActionError):
                runner.retry_infrastructure(
                    self.request(round_number=1, action="resume"),
                    lambda _: True,
                )

    def test_identity_drift_is_rejected_before_next_quality_round(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(state, question_id="question-1")
            first_request = self.request()
            runner.submit_quality_round(first_request, lambda _, request=first_request: self.feedback(request))
            with self.assertRaises(self.g.SessionIdentityError):
                runner.submit_quality_round(
                    self.request(
                        round_number=2,
                        patch_hash="h2",
                        files=("src/b.py",),
                        session_id="new-session",
                    ),
                    lambda _: True,
                )
            self.assertEqual(runner.snapshot().status, self.g.RunnerStatus.ACTIVE.value)

    def test_bare_bool_grader_is_rejected_before_quality_classification(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(state, question_id="question-1")
            request = self.request()
            with self.assertRaises(self.runner_module.RunnerProtocolError):
                runner.submit_quality_round(request, lambda _: False)
            self.assertEqual(runner.snapshot().status, self.g.RunnerStatus.ACTIVE.value)
            self.assertEqual(runner.snapshot().quality_rounds_used, 0)

    def test_feedback_unavailable_is_terminal_unverified_without_retry(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(state, question_id="question-1")
            request = self.request()
            step = runner.submit_quality_round(
                request,
                lambda _, request=request: self.feedback(
                    request, kind=FeedbackFailureKind.FEEDBACK_UNAVAILABLE.value
                ),
            )
            self.assertEqual(step.feedback.failure_kind, FeedbackFailureKind.FEEDBACK_UNAVAILABLE.value)
            self.assertTrue(step.terminal)
            self.assertFalse(step.needs_infra_retry)
            self.assertEqual(runner.snapshot().status, self.g.RunnerStatus.UNVERIFIED.value)
            self.assertFalse(runner.snapshot().scoreable)
            self.assertEqual(runner.snapshot().quality_rounds_used, 0)
            self.assertEqual([event.kind for event in runner.events], ["quality_round", "grader_result", "terminal"])

    def test_structured_timeout_retries_same_quality_round(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(
                state, max_infra_retries=1, question_id="question-1"
            )
            first_request = self.request()
            first = runner.submit_quality_round(
                first_request,
                lambda _, request=first_request: self.feedback(
                    request, kind=FeedbackFailureKind.TIMEOUT.value
                ),
            )
            self.assertTrue(first.needs_infra_retry)
            self.assertFalse(first.feedback.code_repair_eligible)
            self.assertEqual(runner.snapshot().quality_rounds_used, 0)
            retry_request = self.request(round_number=1, action="resume")
            second = runner.retry_infrastructure(
                retry_request,
                lambda _, request=retry_request: self.feedback(request, passed=True),
            )
            self.assertTrue(second.feedback.passed)
            self.assertTrue(second.grader.quality_accepted)
            self.assertEqual(runner.snapshot().status, self.g.RunnerStatus.SUCCEEDED.value)
            self.assertEqual(runner.snapshot().last_round, 1)
            self.assertEqual(runner.snapshot().quality_rounds_used, 1)

    def test_completed_unresolved_is_model_failure_and_advances_next_quality_round(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(state, question_id="question-1")
            first_request = self.request(patch_hash="h1")
            unresolved = {
                "resolved": False,
                "completed": True,
                "question_id": "question-1",
                "arm_id": first_request.arm_id,
                "session_id": first_request.session_id,
                "thread_id": first_request.thread_id,
                "patch_hash": first_request.patch_hash,
            }
            first = runner.submit_quality_round(
                first_request,
                lambda _: unresolved,
            )
            self.assertEqual(
                first.feedback.failure_kind,
                FeedbackFailureKind.MODEL_TEST_FAILURE.value,
            )
            self.assertTrue(first.code_repair_eligible)
            self.assertFalse(first.needs_infra_retry)
            self.assertEqual(runner.snapshot().quality_rounds_used, 1)
            self.assertEqual(runner.snapshot().last_round, 1)
            self.assertEqual(runner.snapshot().status, self.g.RunnerStatus.ACTIVE.value)

            second_request = self.request(
                round_number=2,
                patch_hash="h2",
            )
            second = runner.submit_quality_round(
                second_request,
                lambda _, request=second_request: self.feedback(request, passed=True),
            )
            self.assertTrue(second.grader.quality_accepted)
            self.assertEqual(runner.snapshot().status, self.g.RunnerStatus.SUCCEEDED.value)
            self.assertEqual(runner.snapshot().quality_rounds_used, 2)
            self.assertEqual(
                [event.kind for event in runner.events],
                ["quality_round", "grader_result", "quality_round", "grader_result"],
            )

    def test_typed_completed_unresolved_feedback_keeps_official_truth(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(state, question_id="question-1")
            request = self.request()
            typed = GraderFeedback(
                status=FeedbackStatus.UNRESOLVED.value,
                failure_kind=FeedbackFailureKind.FEEDBACK_UNAVAILABLE.value,
                question_id="question-1",
                arm_id=request.arm_id,
                session_id=request.session_id,
                thread_id=request.thread_id,
                patch_hash=request.patch_hash,
                official_resolved=False,
                official_completed=True,
            )
            step = runner.submit_quality_round(request, lambda _: typed)
            self.assertEqual(
                step.feedback.failure_kind,
                FeedbackFailureKind.MODEL_TEST_FAILURE.value,
            )
            self.assertFalse(step.feedback.official_resolved)
            self.assertTrue(step.feedback.official_completed)
            self.assertTrue(step.code_repair_eligible)
            self.assertEqual(runner.snapshot().quality_rounds_used, 1)

    def test_candidate_missing_symbol_is_repairable_even_during_collection(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(state, question_id="question-1")
            request = self.request()
            step = runner.submit_quality_round(
                request,
                lambda _, request=request: self.feedback(
                    request,
                    kind=FeedbackFailureKind.TEST_COLLECTION_IMPORT_ERROR.value,
                    phase="collection",
                    missing_symbols=("CandidateAPI",),
                ),
            )
            self.assertEqual(
                step.feedback.failure_kind,
                FeedbackFailureKind.MODEL_TEST_FAILURE.value,
            )
            self.assertEqual(step.missing_symbols, ("CandidateAPI",))
            self.assertTrue(step.code_repair_eligible)
            self.assertFalse(step.needs_infra_retry)
            self.assertEqual(runner.snapshot().quality_rounds_used, 1)

    def test_harness_failure_retries_same_quality_round(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(
                state, max_infra_retries=1, question_id="question-1"
            )
            first_request = self.request()
            first = runner.submit_quality_round(
                first_request,
                lambda _, request=first_request: self.feedback(
                    request,
                    kind=FeedbackFailureKind.TEST_HARNESS_FAILURE.value,
                    phase="harness",
                ),
            )
            self.assertTrue(first.needs_infra_retry)
            self.assertEqual(first.feedback.phase, "harness")
            self.assertEqual(runner.snapshot().quality_rounds_used, 0)
            self.assertEqual(runner.snapshot().last_round, 1)

            retry_request = self.request(round_number=1, action="resume")
            retry = runner.retry_infrastructure(
                retry_request,
                lambda _, request=retry_request: self.feedback(request, passed=True),
            )
            self.assertTrue(retry.grader.quality_accepted)
            self.assertEqual(runner.snapshot().status, self.g.RunnerStatus.SUCCEEDED.value)
            self.assertEqual(runner.snapshot().quality_rounds_used, 1)
            self.assertEqual(runner.snapshot().last_round, 1)

    def test_mcp_transport_failure_retries_same_round_without_quality_consumption(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(
                state, max_infra_retries=1, question_id="question-1"
            )
            first_request = self.request(
                trajectory=(),
                server_receipts=[],
                transport_failed=True,
            )
            first = runner.submit_quality_round(
                first_request,
                lambda _: self.fail("MCP transport failure must block grading"),
            )
            self.assertEqual(first.status, self.g.RunnerStatus.INFRASTRUCTURE_FAILURE.value)
            self.assertTrue(first.needs_infra_retry)
            self.assertEqual(runner.snapshot().quality_rounds_used, 0)
            self.assertEqual(runner.snapshot().last_round, 1)

            retry_request = self.request(
                round_number=1,
                action="resume",
                trajectory=(),
                server_receipts=[],
            )
            retry = runner.retry_infrastructure(
                retry_request,
                lambda _, request=retry_request: self.feedback(request, passed=True),
            )
            self.assertTrue(retry.grader.quality_accepted)
            self.assertEqual(runner.snapshot().quality_rounds_used, 1)
            self.assertEqual(runner.snapshot().last_round, 1)

    def test_declared_noncanonical_transport_failure_is_same_round_infra(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(
                state, max_infra_retries=1, question_id="question-1"
            )
            request = self.request(trajectory=(), transport_failed=True)
            first = runner.submit_quality_round(
                request,
                lambda _: self.fail("declared transport failure must block grading"),
            )
            self.assertEqual(first.status, self.g.RunnerStatus.INFRASTRUCTURE_FAILURE.value)
            self.assertTrue(first.needs_infra_retry)
            self.assertEqual(runner.snapshot().quality_rounds_used, 0)

    def test_structured_feedback_identity_drift_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory) / "state.json"
            runner = self.runner_module.FiveRoundRunner(state, question_id="question-1")
            request = self.request()
            wrong = dataclasses.replace(
                self.feedback(request),
                question_id="question-other",
            )
            with self.assertRaises(self.runner_module.RunnerProtocolError):
                runner.submit_quality_round(request, lambda _: wrong)

    def test_agent_transport_collection_and_harness_are_non_quality_infra(self):
        for kind in self.runner_module.NON_QUALITY_INFRA_FEEDBACK_KINDS:
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as directory:
                state = pathlib.Path(directory) / "state.json"
                runner = self.runner_module.FiveRoundRunner(
                    state, max_infra_retries=1, question_id="question-1"
                )
                request = self.request()
                step = runner.submit_quality_round(
                    request,
                    lambda _, request=request, kind=kind: self.feedback(request, kind=kind),
                )
                self.assertEqual(step.grader.failure_kind, self.g.FailureKind.INFRASTRUCTURE.value)
                self.assertEqual(step.feedback.failure_kind, kind)
                self.assertFalse(step.feedback.code_repair_eligible)
                self.assertEqual(runner.snapshot().quality_rounds_used, 0)
                self.assertEqual(runner.snapshot().status, self.g.RunnerStatus.INFRASTRUCTURE_FAILURE.value)
                self.assertEqual(runner.events[-1].feedback_failure_kind, kind)


if __name__ == "__main__":
    unittest.main()
