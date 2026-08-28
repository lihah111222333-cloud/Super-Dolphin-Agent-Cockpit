import copy
import hashlib
import json
import pathlib
import tempfile
import unittest

from scripts import cross_file_benchmark_runner as benchmark_runner
from scripts.cross_file_evidence_adapter_test import (
    GRADER_ARTIFACT,
    SIGNER,
    CrossFileEvidenceAdapterTest as _AdapterFixtureBuilder,
)
from scripts.cross_file_identity_envelope import build_identity_envelope, sha256_text
from scripts.cross_file_grader_bridge import build_signed_feedback
from scripts.cross_file_signed_receipts import Ed25519Signer


ROOT = pathlib.Path(__file__).parent
GRADER_SIGNER = Ed25519Signer.generate("benchmark-grader-role")


class CrossFileBenchmarkRunnerTest(unittest.TestCase):
    def setUp(self):
        self.builder = _AdapterFixtureBuilder()
        self.builder.setUp()

    @staticmethod
    def _identity(arm):
        bindings = arm["identity_envelope"]["bindings"]
        return {
            "question_id": bindings["task"]["id"],
            "arm_id": arm["arm_id"],
            "mode": arm["mode"],
            "session_id": bindings["session"]["id"],
            "thread_id": bindings["thread"]["id"],
            "patch_hash": bindings["patch"]["id"],
            "final_tree_hash": bindings["final_tree"]["tree_sha"],
        }

    @staticmethod
    def _trajectory(mode):
        if mode == "no_lsp":
            return []
        path = "src/shared.py"
        return [
            {
                "type": "mcp_tool_call",
                "tool": "structure",
                "arguments": {"file_path": path},
                "result": {"locations": [f"{path}:10:2"]},
            },
            {
                "type": "mcp_tool_call",
                "tool": "xref",
                "arguments": {"pos": f"{path}:10:2"},
                "result": {"locations": []},
            },
            {
                "type": "mcp_tool_call",
                "tool": "diagnostics",
                "arguments": {
                    "file_paths": [path],
                    "diff_filter": {
                        "mode": "changed_lines",
                        "changed_lines": {path: [10]},
                    },
                },
                "result": {"rows": [], "status": "clean"},
            },
        ]

    def _grader_result(self, identity, passed, *, round_number=1, attempt=1):
        """Build every fake grader response through the production bridge."""

        source = self._source(identity, passed)
        test_output = "1 passed\n" if passed else "1 failed\n"
        request_id = (
            f"grader-{identity['question_id']}-{identity['arm_id']}-"
            f"{round_number}-{attempt}"
        )
        receipt = build_signed_feedback(
            task=identity["question_id"],
            arm=identity["arm_id"],
            session=identity["session_id"],
            thread=identity["thread_id"],
            patch_hash=identity["patch_hash"],
            final_tree_hash=identity.get("final_tree_hash", "c" * 40),
            request_id=request_id,
            seq=attempt,
            signer=GRADER_SIGNER,
            report=source,
            test_output=test_output,
            stdout=b"fixture grader stdout",
            stderr=b"fixture grader stderr",
        )
        signed_receipt = receipt["receipt"]
        digest = hashlib.sha256(
            json.dumps(
                signed_receipt,
                ensure_ascii=False,
                sort_keys=True,
                separators=(",", ":"),
            ).encode("utf-8")
        ).hexdigest()
        return {
            "feedback": receipt,
            "official_report": source,
            "official_test_output": test_output,
            "official_stdout": "fixture grader stdout",
            "official_stderr": "fixture grader stderr",
            "grader_request_id": request_id,
            "grader_receipt_digest": digest,
            "grader_receipt_public_key": GRADER_SIGNER.public_key.to_dict(),
        }

    def _feedback(self, identity, passed):
        return self._grader_result(identity, passed)["feedback"]

    @staticmethod
    def _source(identity, passed):
        payload = {
            "schema": "super-dolphin/grader-feedback/v1",
            "status": "PASSED" if passed else "FAILED",
            "identity": {
                key: identity[key]
                for key in ("question_id", "arm_id", "session_id", "thread_id", "patch_hash")
            },
        }
        if not passed:
            payload.update({
                "failure_kind": "MODEL_TEST_FAILURE",
                "failing_test_ids": ["fixture_test"],
                "assertion_summary": "expected true",
                "failures": [{
                    "test_id": "fixture_test",
                    "file": "tests/test_fixture.py",
                    "line": 1,
                    "exception": "AssertionError",
                    "message": "expected true",
                }],
            })
        return json.dumps(payload, sort_keys=True)

    def _rebind_patch(self, arm, patch_hash):
        result = copy.deepcopy(arm)
        bindings = copy.deepcopy(result["identity_envelope"]["bindings"])
        bindings["patch"]["id"] = patch_hash
        bindings["patch"]["sha256"] = sha256_text(f"patch-{patch_hash}")
        envelope = build_identity_envelope(
            bindings,
            receipt_signing_key=result["identity_envelope"].get("receipt_signing_key"),
            grader_receipt_signing_key=GRADER_SIGNER.public_key.to_dict(),
        )
        result["identity_envelope"] = envelope
        result["identity_envelopes"] = {
            name: copy.deepcopy(envelope)
            for name in ("runner_events", "arm_evidence", "grader_feedback", "usage")
        }
        result["code_agent_id"] = envelope["bindings"]["agent"]["id"]
        result["arm_evidence"] = self.builder._canonical_arm_evidence(
            envelope,
            result["mode"],
            result["arm_id"],
        )
        for field in ("grader_feedback", "official_report", "official_test_output", "runner_events"):
            result.pop(field, None)
        return result

    def _plan(self, *, no_grader=None, with_rounds=None):
        base = self.builder.fixture()
        no_arm = self._rebind_patch(base["arms"]["no_lsp"], "e" * 64)
        with_arm = self._rebind_patch(base["arms"]["with_lsp"], "e" * 64)
        for arm in (no_arm, with_arm):
            for field in ("grader_feedback", "official_report", "official_test_output", "runner_events"):
                arm.pop(field, None)
        no_identity = self._identity(no_arm)
        with_identity = self._identity(with_arm)

        def grader(identity, passed=True):
            return self._grader_result(identity, passed)

        no_grader = no_grader or grader(no_identity)
        with_grader = grader(with_identity)
        no_arm["rounds"] = [{
            "round": 1,
            "request": {
                "patch_hash": no_identity["patch_hash"],
                "changed_files": ["src/shared.py"],
                "trajectory": [],
                "mode": "no_lsp",
            },
            "grader": no_grader,
        }]
        if with_rounds is None:
            with_rounds = [{
                "round": 1,
                "request": {
                    "patch_hash": with_identity["patch_hash"],
                    "changed_files": ["src/shared.py"],
                    "trajectory": self._trajectory("with_lsp"),
                    "mode": "with_lsp",
                },
                "grader": with_grader,
            }]
        with_arm["rounds"] = with_rounds
        return {
            "schema": benchmark_runner.RUN_PLAN_SCHEMA,
            "run_id": "benchmark-runner-test",
            "max_concurrency": 1,
            "pricing_manifest": base["pricing_manifest"],
            "questions": [{
                "question_id": base["question_id"],
                "discovery_shape": base["discovery_shape"],
                "arms": {"no_lsp": no_arm, "with_lsp": with_arm},
            }],
        }

    def _live_plan(self):
        plan = self._plan()
        for arm in plan["questions"][0]["arms"].values():
            keep = {"mode", "arm_id"}
            for field in tuple(arm):
                if field not in keep:
                    arm.pop(field, None)
        return plan

    def _driver(self, *, pass_round=1, transport_once=False, agent_error=False):
        owner = self

        class Driver:
            def __init__(self):
                self.agent_calls = []
                self.grader_calls = []
                self._transport_pending = transport_once

            def run_agent(self, context):
                self.agent_calls.append((context.mode, context.round_number))
                if context.mode == "no_lsp":
                    patch_hash = context.identity.get("patch_hash", "e" * 64)
                else:
                    patch_hash = (
                        chr(ord("a") + context.round_number - 1) * 64
                        if pass_round > 1
                        else context.identity.get("patch_hash", "e" * 64)
                    )
                source = owner.builder.fixture()["arms"][context.mode]
                source = owner._rebind_patch(source, patch_hash)
                artifacts = {
                    "identity_envelope": source["identity_envelope"],
                    "identity_envelopes": source["identity_envelopes"],
                    "server_generation": source["server_generation"],
                    "arm_evidence": source["arm_evidence"],
                    "changed_files": source["changed_files"],
                    "usage": source["usage"],
                }
                if agent_error:
                    return {
                        "transport_error": "agent transport unavailable",
                        **artifacts,
                    }
                return {"request": {
                    "patch_hash": patch_hash,
                    "changed_files": ["src/shared.py"],
                    "trajectory": owner._trajectory(context.mode),
                    "mode": context.mode,
                }, **artifacts}

            def run_grader(self, context):
                self.grader_calls.append((context.mode, context.round_number))
                if context.mode == "no_lsp" and self._transport_pending:
                    self._transport_pending = False
                    return {"transport_error": "grader transport unavailable"}
                passed = context.mode == "no_lsp" or context.round_number >= pass_round
                identity = {
                    **context.request.__dict__,
                }
                return owner._grader_result({
                        "question_id": context.question_id,
                        "arm_id": context.request.arm_id,
                        "session_id": context.request.session_id,
                        "thread_id": context.request.thread_id,
                        "patch_hash": context.request.patch_hash,
                    }, passed, round_number=context.round_number)

            def run_evidence_recovery(self, context, pending):
                patch_hash = context.identity["patch_hash"]
                source = owner._rebind_patch(
                    owner.builder.fixture()["arms"][context.mode], patch_hash
                )
                return {"request": {
                    "patch_hash": patch_hash,
                    "changed_files": ["src/shared.py"],
                    "trajectory": owner._trajectory(context.mode),
                    "mode": context.mode,
                    "evidence_attempt": 1,
                },
                    "identity_envelope": source["identity_envelope"],
                    "identity_envelopes": source["identity_envelopes"],
                    "server_generation": source["server_generation"],
                    "arm_evidence": source["arm_evidence"],
                    "changed_files": source["changed_files"],
                    "usage": source["usage"],
                }

            def run_infra_retry(self, context, pending):
                identity = {
                    "question_id": context.question_id,
                    "arm_id": context.request.arm_id,
                    "session_id": context.request.session_id,
                    "thread_id": context.request.thread_id,
                    "patch_hash": context.request.patch_hash,
                }
                return owner._grader_result(identity, True, attempt=2)

        return Driver()

    def test_benchmark_runner_executes_both_arms_through_adapter_and_audit(self):
        plan = self._live_plan()
        driver = self._driver()
        with tempfile.TemporaryDirectory() as directory:
            execution = benchmark_runner.execute_plan(plan, state_dir=directory, driver=driver)
        audit_result = execution.audit_result
        self.assertIsNotNone(audit_result)
        assert audit_result is not None
        self.assertEqual(len(audit_result.questions), 1)
        self.assertTrue(audit_result.questions[0].passed)
        self.assertEqual([item.runner.snapshot().status for item in execution.arms], ["succeeded", "succeeded"])
        for arm in execution.arms:
            patch_hash = arm.artifacts["identity_envelope"]["bindings"]["patch"]["id"]
            self.assertEqual(arm.final_grader_receipt["patch_hash"], patch_hash)
            evidence = arm.artifacts["arm_evidence"]
            if arm.mode == "no_lsp":
                self.assertEqual(evidence["isolation"]["patch_hash"], patch_hash)
            else:
                self.assertTrue(all(item["patch_hash"] == patch_hash for item in evidence["receipts"]))

    def test_successful_execution_exposes_raw_authoritative_and_complete_final(self):
        plan = self._live_plan()
        driver = self._driver()
        with tempfile.TemporaryDirectory() as directory:
            execution = benchmark_runner.execute_plan(plan, state_dir=directory, driver=driver)
            report = execution.to_dict()
        self.assertTrue(execution.scoreable)
        self.assertEqual(report["raw"]["arm_count"], 2)
        self.assertEqual(report["raw"]["records"], report["raw_records"])
        self.assertEqual(report["authoritative"]["arm_count"], 2)
        self.assertEqual(report["authoritative"]["pair_count"], 1)
        self.assertEqual(
            report["authoritative"]["pairs"][0]["quality_scores"],
            {"no_lsp": 10, "with_lsp": 10},
        )
        self.assertEqual(len(report["final"]["arms"]), 2)
        self.assertIsNotNone(report["final"]["audit"])

    def test_final_output_keeps_failed_arm_and_successful_sibling_isolated(self):
        plan = self._live_plan()
        driver = self._driver()
        original = driver.run_agent

        def fail_with_lsp(context):
            if context.mode == "with_lsp":
                raise RuntimeError("one arm only")
            return original(context)

        driver.run_agent = fail_with_lsp
        with tempfile.TemporaryDirectory() as directory:
            execution = benchmark_runner.execute_plan(plan, state_dir=directory, driver=driver)
            report = execution.to_dict()
        self.assertFalse(execution.scoreable)
        self.assertEqual(len(report["final"]["arms"]), 2)
        by_mode = {row["mode"]: row for row in report["final"]["arms"]}
        self.assertEqual(by_mode["with_lsp"]["status"], "contract_failure")
        self.assertEqual(by_mode["no_lsp"]["status"], "succeeded")

    def test_parallel_plan_requires_per_arm_driver_factory(self):
        plan = self._live_plan()
        plan["max_concurrency"] = 2
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(
                benchmark_runner.PlanError, "per-arm execution-driver factory"
            ):
                benchmark_runner.execute_plan(
                    plan, state_dir=directory, driver=self._driver()
                )

    def test_live_grader_rejects_missing_executor_source_stream(self):
        arm = self.builder.fixture()["arms"]["no_lsp"]
        identity = self._identity(arm)
        raw = {
            "feedback": self._feedback(identity, True),
            "official_report": self._source(identity, True),
            "official_test_output": self._source(identity, True),
            "official_stdout": b"stdout",
            "grader_request_id": "request-1",
            "grader_receipt_digest": "d" * 64,
            "grader_receipt_public_key": SIGNER.public_key.to_dict(),
        }
        with self.assertRaisesRegex(benchmark_runner.PlanError, "official_stderr"):
            benchmark_runner._strict_feedback(
                raw,
                identity=identity,
                patch_hash=identity["patch_hash"],
                identity_envelope=arm["identity_envelope"],
                require_executor_artifacts=True,
            )

    def test_live_grader_missing_official_report_is_unverified(self):
        arm = self.builder.fixture()["arms"]["no_lsp"]
        identity = self._identity(arm)
        request_id = "missing-report-request"
        test_output = "FAILED tests/test_x.py::test_x - AssertionError: noisy\n"
        feedback = build_signed_feedback(
            task=identity["question_id"],
            arm=identity["arm_id"],
            session=identity["session_id"],
            thread=identity["thread_id"],
            patch_hash=identity["patch_hash"],
            final_tree_hash=identity["final_tree_hash"],
            request_id=request_id,
            seq=1,
            signer=GRADER_SIGNER,
            report=None,
            test_output=test_output,
            stdout="",
            stderr="",
        )
        signed_receipt = feedback["receipt"]
        raw = {
            "feedback": feedback,
            "official_report": None,
            "official_test_output": test_output,
            "official_stdout": "",
            "official_stderr": "",
            "grader_request_id": request_id,
            "grader_receipt_digest": hashlib.sha256(
                json.dumps(
                    signed_receipt,
                    ensure_ascii=False,
                    sort_keys=True,
                    separators=(",", ":"),
                ).encode("utf-8")
            ).hexdigest(),
            "grader_receipt_public_key": GRADER_SIGNER.public_key.to_dict(),
        }
        result = benchmark_runner._strict_feedback(
            raw,
            identity=identity,
            patch_hash=identity["patch_hash"],
            identity_envelope=arm["identity_envelope"],
            require_executor_artifacts=True,
        )[0]
        self.assertEqual(result.failure_kind, "FEEDBACK_UNAVAILABLE")
        self.assertFalse(result.code_repair_eligible)

    def test_plan_requires_explicit_max_concurrency(self):
        plan = self._live_plan()
        plan.pop("max_concurrency")
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(
                benchmark_runner.PlanError, "max_concurrency"
            ):
                benchmark_runner.execute_plan(
                    plan, state_dir=directory, driver=self._driver()
                )

    def test_same_session_three_quality_rounds_reach_round_three(self):
        base = self.builder.fixture()
        no_arm = self._rebind_patch(base["arms"]["no_lsp"], "e" * 64)
        with_arm = self._rebind_patch(base["arms"]["with_lsp"], "e" * 64)
        plan = self._live_plan()
        driver = self._driver(pass_round=3)
        with tempfile.TemporaryDirectory() as directory:
            execution = benchmark_runner.execute_plan(plan, state_dir=directory, driver=driver)
        runner = execution.arms[1].runner
        self.assertEqual(runner.snapshot().status, "succeeded")
        self.assertEqual(runner.snapshot().last_round, 3)
        self.assertEqual(runner.snapshot().quality_rounds_used, 3)
        self.assertEqual(len({event.session_id for event in runner.events}), 1)
        self.assertEqual(
            [event.patch_hash[0] for event in runner.events if event.kind == "quality_round"],
            ["a", "b", "c"],
        )

    def test_transport_retry_stays_in_same_quality_round_without_extra_quality_use(self):
        plan = self._live_plan()
        driver = self._driver(transport_once=True)
        with tempfile.TemporaryDirectory() as directory:
            execution = benchmark_runner.execute_plan(plan, state_dir=directory, driver=driver)
        runner = execution.arms[0].runner
        self.assertEqual(runner.snapshot().status, "succeeded")
        self.assertEqual(runner.snapshot().last_round, 1)
        self.assertEqual(runner.snapshot().quality_rounds_used, 1)
        self.assertEqual([event.kind for event in runner.events], [
            "quality_round", "grader_result", "infra_retry", "grader_result",
        ])

    def test_legacy_semantic_gap_blocks_grader_before_success(self):
        with tempfile.TemporaryDirectory() as directory:
            state_path = pathlib.Path(directory) / "django__django-16560.with_lsp.state.json"
            runner = benchmark_runner.FiveRoundRunner(
                state_path,
                question_id="django__django-16560",
            )
            request = self._driver_request("with_lsp")
            identity = {
                "question_id": "django__django-16560",
                "arm_id": request.arm_id,
                "session_id": request.session_id,
                "thread_id": request.thread_id,
                "patch_hash": request.patch_hash,
            }
            feedback = benchmark_runner.GraderFeedback(
                status="PASSED",
                failure_kind="",
                passed=True,
                official_resolved=True,
                official_completed=True,
                official_instance_id=identity["question_id"],
                **identity,
            )
            runner.submit_quality_round(
                request,
                lambda _: feedback,
            )
        self.assertEqual(runner.snapshot().status, "evidence_recovery_required")
        self.assertEqual(runner.snapshot().last_round, 0)
        self.assertEqual(runner.snapshot().quality_rounds_used, 0)
        self.assertEqual(
            [event.kind for event in runner.events],
            ["quality_round"],
        )

    def test_pending_infrastructure_child_resumes_from_persisted_driver_artifact(self):
        plan = self._live_plan()
        driver = self._driver()
        with tempfile.TemporaryDirectory() as directory:
            state_path = pathlib.Path(directory) / "django__django-16560.no_lsp.state.json"
            context = benchmark_runner.AgentContext(
                question_id="django__django-16560",
                mode="no_lsp",
                round_number=1,
                identity={"question_id": "django__django-16560", "arm_id": "no-lsp", "mode": "no_lsp"},
                arm={"mode": "no_lsp", "arm_id": "no-lsp"},
                runner_snapshot={"status": "new", "last_round": 0},
            )
            artifact = driver.run_agent(context)
            bindings = artifact["identity_envelope"]["bindings"]
            request = benchmark_runner.RoundInput(
                round_number=1,
                arm_id="no-lsp",
                session_id=bindings["session"]["id"],
                thread_id=bindings["thread"]["id"],
                code_agent_id=bindings["agent"]["id"],
                patch_hash=bindings["patch"]["id"],
                changed_files=("src/shared.py",),
                trajectory=(),
                mode="no_lsp",
            )
            seeded = benchmark_runner.FiveRoundRunner(
                state_path,
                question_id="django__django-16560",
            )
            seeded.submit_quality_round(
                request,
                lambda _: (_ for _ in ()).throw(RuntimeError("grader transport")),
            )
            benchmark_runner._persist_artifact(
                benchmark_runner._artifact_state_path(state_path),
                artifact,
                request,
            )
            execution = benchmark_runner.execute_plan(
                plan,
                state_dir=directory,
                driver=driver,
            )
        runner = execution.arms[0].runner
        self.assertEqual(runner.snapshot().status, "succeeded")
        self.assertEqual(runner.snapshot().last_round, 1)
        self.assertEqual(runner.snapshot().quality_rounds_used, 1)
        self.assertEqual(
            [event.kind for event in runner.events],
            ["quality_round", "grader_result", "infra_retry", "grader_result"],
        )

    def _driver_request(self, mode):
        return benchmark_runner.RoundInput(
            round_number=1,
            arm_id="with-lsp",
            session_id="session-with_lsp",
            thread_id="thread-with_lsp",
            code_agent_id="with_lsp-agent",
            patch_hash="e" * 64,
            changed_files=("src/shared.py",),
            trajectory=(),
            mode=mode,
        )

    def test_old_signed_arm_receipt_replay_is_rejected_after_dynamic_patch_update(self):
        plan = self._live_plan()
        driver = self._driver()
        original_run_agent = driver.run_agent

        def stale_arm_evidence(context):
            result = original_run_agent(context)
            if context.mode == "with_lsp":
                stale = self.builder.fixture()["arms"]["with_lsp"]
                result["arm_evidence"] = copy.deepcopy(stale["arm_evidence"])
            return result

        driver.run_agent = stale_arm_evidence
        with tempfile.TemporaryDirectory() as directory:
            execution = benchmark_runner.execute_plan(plan, state_dir=directory, driver=driver)
        self.assertFalse(execution.scoreable)
        self.assertIsNone(execution.audit_result)
        self.assertIn("signed receipt patch_hash drifted", execution.error or "")

    def test_cli_emits_only_canonical_audit_and_has_no_bypass_imports(self):
        plan = self._plan()
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            input_path = root / "plan.json"
            output_path = root / "report.json"
            input_path.write_text(json.dumps(plan), encoding="utf-8")
            code = benchmark_runner.main([
                "--input", str(input_path),
                "--state-dir", str(root / "state"),
                "--mode", "evidence-replay",
                "--output", str(output_path),
            ])
            self.assertEqual(code, 3)
            report = json.loads(output_path.read_text(encoding="utf-8"))
        self.assertEqual(report["schema"], "super-dolphin/cross-file-benchmark-runner/v1")
        self.assertEqual(report["execution_mode"], "evidence_replay")
        self.assertFalse(report["scoreable"])
        source = pathlib.Path(benchmark_runner.__file__).read_text(encoding="utf-8")
        self.assertIn("FiveRoundRunner", source)
        self.assertIn("validate_grader_feedback", source)
        self.assertIn("audit_evidence_set", source)
        self.assertNotIn("ClosedLoopGuard", source)
        self.assertNotIn("parse_grader_feedback", source)

    def test_prefilled_grader_evidence_cannot_enter_live_execution(self):
        plan = self._plan()
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(benchmark_runner.PlanError, "explicit execution driver"):
                benchmark_runner.execute_plan(plan, state_dir=directory)
            with self.assertRaisesRegex(benchmark_runner.PlanError, "dynamic evidence must come from"):
                benchmark_runner.execute_plan(plan, state_dir=directory, driver=self._driver())
            replay = benchmark_runner.replay_evidence_plan(plan, state_dir=directory)
        self.assertEqual(replay.execution_mode, "evidence_replay")
        self.assertFalse(replay.scoreable)
        self.assertEqual(replay.to_dict()["execution_mode"], "evidence_replay")

    def test_unscoreable_terminal_status_is_persisted_and_reported(self):
        plan = self._live_plan()
        driver = self._driver(agent_error=True)
        with tempfile.TemporaryDirectory() as directory:
            execution = benchmark_runner.execute_plan(plan, state_dir=directory, driver=driver)
            report = execution.to_dict()
            no_arm = execution.arms[0]
            state_path = pathlib.Path(directory) / "django__django-16560.no_lsp.state.json"
            self.assertTrue(state_path.exists())
            persisted = json.loads(state_path.read_text(encoding="utf-8"))
        self.assertFalse(execution.scoreable)
        self.assertIsNone(execution.audit_result)
        self.assertEqual(no_arm.status, "unverified")
        self.assertEqual(report["arms"][0]["status"], "unverified")
        self.assertIsNotNone(report["arms"][0]["artifacts"])
        self.assertEqual(persisted["status"], "unverified")

    def test_lane_scheduler_isolates_arm_exception_and_continues_queue(self):
        plan = self._live_plan()
        first = plan["questions"][0]
        second = copy.deepcopy(first)
        second["question_id"] = "django__django-16561"
        second["arms"]["no_lsp"]["arm_id"] = "django__django-16561-no-lsp"
        second["arms"]["with_lsp"]["arm_id"] = "django__django-16561-with-lsp"
        plan["questions"] = [first, second]
        plan["max_concurrency"] = 2
        calls = []

        class FailingDriver:
            def run_agent(self, context):
                calls.append((context.question_id, context.mode))
                raise RuntimeError("isolated arm failure")

            def run_grader(self, context):  # pragma: no cover - Agent always fails.
                raise AssertionError

            def run_evidence_recovery(self, context, pending):  # pragma: no cover
                raise AssertionError

            def run_infra_retry(self, context, pending):  # pragma: no cover
                raise AssertionError

        class Factory:
            def for_arm(self, question_id, mode, arm):
                return FailingDriver()

        with tempfile.TemporaryDirectory() as directory:
            execution = benchmark_runner.execute_plan(
                plan, state_dir=directory, driver=Factory()
            )
        self.assertEqual(len(calls), 4)
        self.assertEqual(len(execution.arms), 4)
        self.assertTrue(all(arm.status == "contract_failure" for arm in execution.arms))
        self.assertFalse(execution.scoreable)
        self.assertIsNone(execution.audit_result)


if __name__ == "__main__":
    unittest.main()
